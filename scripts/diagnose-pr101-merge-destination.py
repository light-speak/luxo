#!/usr/bin/env python3
"""Bind the original confirmed leaf and summarize native Go linker evidence."""

import json
from hashlib import sha256
from pathlib import Path
import re
import sys
from zipfile import ZipFile


RUN = 45676516589
JOB = 139426143735
ARTIFACT = 15051468702
DIGEST = "fee7c3c17a7cf6df498c2e08239bc105e37717f0d23edd9f58ec70c541a0c3e1"
BASE = "7ba8d7fa68f0f096af907bd860f01589c3b2dbbd"
HEAD = "91433a84653715d6d78a9d18725de918921669b8"
PACKAGE = "pkg/lux/luvia"
LEAF = "BenchmarkMergeDestination/columnar=true/rows=1/direct=true-2"
BATCH = "pkg/lux/luvia\tdefault\t^BenchmarkMergeDestination$/^columnar=true$/^rows=1$/^direct=true$\n"
ENTRY = {"Package": PACKAGE, "Name": LEAF}
CPU = "Intel(R) Xeon(R) 6975P-C"
INSTRUCTION = re.compile(r"\b(0x[\da-fA-F]+)\s+([\da-fA-F]+)\s+(.+)$")
CALL = re.compile(r"\bCALL\s+(.*)\(SB\)")


def verify_original(read):
    assert read("benchmark-revisions.txt") == f"base_sha={BASE}\ntested_sha={HEAD}\n"
    assert read("benchmark-confirmation-batch.tsv") == BATCH
    assert read("benchmark-confirmation-targets.tsv") == f"{PACKAGE}\t{LEAF}\n"
    expected = read("benchmark-confirmation-expected.json")
    assert json.loads(expected) == {"Base": [ENTRY], "Head": [ENTRY]}
    env = read("benchmark-environment.txt")
    assert re.search(rf"(?m)^Model name:\s+{re.escape(CPU)}$", env)
    tools = json.loads(env[env.rfind("{"):])
    assert tools == {"CGO_ENABLED": "1", "GOAMD64": "v1", "GOARCH": "amd64", "GOOS": "linux", "GOVERSION": "go1.27.1"}
    verdict = read("benchmark-gate.txt")
    assert verdict.count("confirmed performance regression:") == 1 and LEAF.removeprefix("Benchmark") in verdict
    for name in ("benchmark-control-gate.txt", "benchmark-control-reversed-gate.txt"):
        assert read(name).startswith("performance gate passed:")
    assert "+6.10%" in verdict and "+6.42%" in verdict
    return expected


def bind(zip_path, output):
    assert sha256(zip_path.read_bytes()).hexdigest() == DIGEST
    with ZipFile(zip_path) as archive:
        assert archive.testzip() is None

        def read(name):
            member, = (m for m in archive.namelist() if Path(m).name == name)
            return archive.read(member).decode()

        expected = verify_original(read)
        (output / "expected.json").write_text(expected)
        (output / "original-profile.txt").write_text(read("benchmark-compile-profile.txt"))
        (output / "confirmation-batch.tsv").write_text(BATCH)
        (output / "original-gate.txt").write_text(read("benchmark-gate.txt"))
    print(json.dumps({"run": RUN, "job": JOB, "artifact": ARTIFACT, "digest": DIGEST, "base": BASE, "head": HEAD,
                      "cpu": CPU, "name": LEAF, "profile": "default"}, indent=2))


def disassembly(path):
    functions = {}
    name = None
    for line in path.read_text().splitlines():
        if line.startswith("TEXT "):
            name = line.split()[1].removesuffix("(SB)")
            assert name not in functions, name
            functions[name] = {"bytes": [], "calls": set()}
        else:
            match = INSTRUCTION.search(line)
            if match and name:
                _, opcode, text = match.groups()
                functions[name]["bytes"].append(bytes.fromhex(opcode))
                call = CALL.search(text)
                if call:
                    functions[name]["calls"].add(call.group(1))
    return functions


def symbols(directory, arm):
    functions = disassembly(directory / f"{arm}.hot.objdump.txt")
    result = {}
    for line in (directory / f"{arm}.nm.txt").read_text().splitlines():
        fields = line.split(maxsplit=3)
        if len(fields) != 4 or fields[3] not in functions:
            continue
        address, size, _, name = fields
        instructions = functions[name]["bytes"]
        raw = b"".join(instructions)
        if raw:
            address = int(address, 16)
            result[name] = {"address": hex(address), "size": int(size), "align16": address % 16,
                            "align64": address % 64, "pageOffset": address % 4096,
                            "disassembledBytes": len(raw), "opcodeSha256": sha256(raw).hexdigest(),
                            "calls": sorted(functions[name]["calls"])}
    assert any(".BenchmarkMergeDestination" in name for name in result), "missing benchmark symbol"
    assert any(name.endswith(".appendMergedColumnar") for name in result), "missing merge body"
    return result


def compare(directory):
    left, right = symbols(directory, "A"), symbols(directory, "B")
    details = {}
    for name in sorted(left.keys() | right.keys()):
        a, b = left.get(name), right.get(name)
        details[name] = {"A": a, "B": b,
                         "sameDisassembledOpcodes": bool(a and b and a["opcodeSha256"] == b["opcodeSha256"]),
                         "sameCallTargets": bool(a and b and a["calls"] == b["calls"])}
    print(json.dumps(details, indent=2))


def smoke(path):
    lines = path.read_text().splitlines()
    assert all(value in lines for value in ("goos: linux", "goarch: amd64", f"cpu: {CPU}", "PASS"))
    row, = (line.split() for line in lines if line.startswith("Benchmark"))
    assert len(row) == 8 and row[:2] == [LEAF, "1"] and row[3::2] == ["ns/op", "B/op", "allocs/op"]


if __name__ == "__main__":
    mode, *args = sys.argv[1:]
    if mode == "bind":
        bind(*map(Path, args))
    elif mode == "compare":
        compare(*map(Path, args))
    elif mode == "smoke":
        smoke(*map(Path, args))
    else:
        raise SystemExit("usage: bind <original-zip> <evidence-directory> | compare <linkage-directory> | smoke <inventory>")
