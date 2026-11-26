#!/usr/bin/env python3
"""Bind the original confirmed leaf and summarize native Go linker evidence."""

from collections import Counter
import json
import math
from hashlib import sha256
from pathlib import Path
import re
import sys
from zipfile import ZipFile


RUN = 45745353238
JOB = 139660534283
ARTIFACT = 15083004141
DIGEST = "5709c8ca778e8661d9331afe083092972eed88162501999488c8a41cfefec94d"
BASE = "f9ce659fd7f17bf57cfe0aee7de7ecb8d9b8f2e4"
HEAD = "39d329d07d7729b4b4ddb5915e08bf9d5d07ea9d"
PACKAGE = "pkg/lux/luvia"
LEAVES = ('BenchmarkConvenienceKeyParentValidation/column-small/keyLast=false/current-2', 'BenchmarkConvenienceKeyParentValidation/column-small/keyLast=true/current-2')
BATCH = "pkg/lux/luvia\tdefault\t^BenchmarkConvenienceKeyParentValidation$/^column-small$/^keyLast=false$/^current$|^BenchmarkConvenienceKeyParentValidation$/^column-small$/^keyLast=true$/^current$\n"
ENTRIES = [{"Package": PACKAGE, "Name": leaf} for leaf in LEAVES]
CPU = "INTEL(R) XEON(R) PLATINUM 8573C"
INSTRUCTION = re.compile(r"\b(0x[\da-fA-F]+)\s+([\da-fA-F]+)\s+(.+)$")
CALL = re.compile(r"\bCALL\s+(.*)\(SB\)")


def verify_original(read):
    assert read("benchmark-revisions.txt") == f"base_sha={BASE}\ntested_sha={HEAD}\n"
    assert read("benchmark-confirmation-batch.tsv") == BATCH
    assert read("benchmark-confirmation-targets.tsv") == "".join(f"{PACKAGE}\t{leaf}\n" for leaf in LEAVES)
    expected = read("benchmark-confirmation-expected.json")
    assert json.loads(expected) == {"Base": ENTRIES, "Head": ENTRIES}
    env = read("benchmark-environment.txt")
    assert re.search(rf"(?m)^Model name:\s+{re.escape(CPU)}$", env)
    tools = json.loads(env[env.rfind("{"):])
    assert tools == {"CGO_ENABLED": "1", "GOAMD64": "v1", "GOARCH": "amd64", "GOOS": "linux", "GOVERSION": "go1.27.1"}
    verdict = read("benchmark-gate.txt")
    assert verdict.count("confirmed performance regression:") == len(LEAVES)
    assert all(leaf.removeprefix("Benchmark") in verdict for leaf in LEAVES)
    for name in ("benchmark-control-gate.txt", "benchmark-control-reversed-gate.txt"):
        assert read(name).startswith("performance gate passed:")
    assert all(delta in verdict for delta in ("+6.84%", "+7.50%", "+5.62%", "+5.82%"))
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
                      "cpu": CPU, "leaves": LEAVES, "profile": "default"}, indent=2))


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
    assert any(".BenchmarkConvenienceKeyParentValidation" in name for name in result), "missing benchmark symbol"
    assert any(name.endswith(".ExtractIDColumn") for name in result), "missing column extraction body"
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
    rows = [line.split() for line in lines if line.startswith("Benchmark")]
    assert sorted(row[0] for row in rows) == sorted(LEAVES)
    assert all(len(row) == 8 and row[1] == "1" and row[3::2] == ["ns/op", "B/op", "allocs/op"]
               for row in rows)


def samples(directory):
    groups = {"AA": ("A", "A"), "BB": ("B", "B"), "AB1": ("A", "B"), "AB2": ("A", "B")}
    observed = Counter()
    orders = Counter()
    for line in (directory / "order.tsv").read_text().splitlines():
        round_id, group, left, right, order, _ = line.split("\t")
        assert (left, right) == groups[group] and order in {"LR", "RL"}
        observed[group, int(round_id)] += 1
        orders[group, order] += 1
    assert observed == Counter({(group, round_id): 1 for group in groups for round_id in range(1, 11)})
    assert orders == Counter({(group, order): 5 for group in groups for order in ("LR", "RL")})
    for group in groups:
        for side in ("left", "right"):
            lines = (directory / f"{group}.{side}.raw.txt").read_text().splitlines()
            assert sum(line == "PASS" for line in lines) == 10
            assert sum(line.upper() == f"CPU: {CPU}" for line in lines) == 10
            rows = [line.split() for line in lines if line.startswith("Benchmark")]
            assert Counter(row[0] for row in rows) == Counter({leaf: 10 for leaf in LEAVES})
            for row in rows:
                assert len(row) == 8 and int(row[1]) > 0 and row[3::2] == ["ns/op", "B/op", "allocs/op"]
                assert all(math.isfinite(value) and value >= 0 for value in map(float, row[2::2]))


if __name__ == "__main__":
    mode, *args = sys.argv[1:]
    if mode == "bind":
        bind(*map(Path, args))
    elif mode == "compare":
        compare(*map(Path, args))
    elif mode == "smoke":
        smoke(*map(Path, args))
    elif mode == "samples":
        samples(*map(Path, args))
    else:
        raise SystemExit("usage: bind <original-zip> <directory> | compare <directory> | smoke <inventory> | samples <directory>")
