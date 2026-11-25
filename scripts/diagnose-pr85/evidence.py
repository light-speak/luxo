#!/usr/bin/env python3
"""Bind the original gate and compare the same-program sample and linker records."""

import json
import math
import re
import sys
from hashlib import sha256
from pathlib import Path
from zipfile import ZipFile

BASE = "cbca8575fdd012835799a8f10a94f4c596d1e9d6"
HEAD = "02c0e428f1e4abe27f049f2644f7b7fecd8dc90b"
DIGEST = "6f08dc18fc15c5bc1804f240e4ea9f2cc9ad3d60ed27121a0c4c9a0f69469c80"
PACKAGE = "pkg/lux/api"
CPU = "Intel(R) Xeon(R) 6975P-C"
PROFILE = "api-non-identity"
ROOT = "BenchmarkLocalRouteExport"
LEAF = f"{ROOT}/routes=128/previous-2"
BATCH = f"{PACKAGE}\t{PROFILE}\t^{ROOT}$/^routes=128$/^previous$\n"
INSTRUCTION = re.compile(r"\b(0x[\da-fA-F]+)\s+([\da-fA-F]+)\s+(.+)$")
CALL = re.compile(r"\bCALL\s+(.*)\(SB\)")


def expected_rows(names):
    entries = [{"Package": PACKAGE, "Name": name} for name in names]
    return {"Base": entries, "Head": entries}


def expected_for(variant):
    if variant == "parse":
        return [f"BenchmarkDiagnosticWriteParseError/{mode}/{name}-2"
                for mode in ("json", "binary") for name in ("BadRequest", "PayloadTooLarge", "Unavailable")]
    counts = {"linked": (128,), "isolated": (1, 16, 128)}[variant]
    return [f"{ROOT}/routes={count}/{shape}-2" for count in counts for shape in ("previous", "current")]


def verify_original(read):
    assert read("benchmark-revisions.txt") == f"base_sha={BASE}\ntested_sha={HEAD}\n"
    assert read("benchmark-confirmation-batch.tsv") == BATCH
    assert read("benchmark-confirmation-targets.tsv") == f"{PACKAGE}\t{LEAF}\n"
    expected = read("benchmark-confirmation-expected.json")
    assert json.loads(expected) == expected_rows([LEAF])
    env = read("benchmark-environment.txt")
    assert re.search(rf"(?m)^Model name:\s+{re.escape(CPU)}$", env)
    assert json.loads(env[env.rfind("{"):]) == {
        "CGO_ENABLED": "1", "GOAMD64": "v1", "GOARCH": "amd64", "GOOS": "linux", "GOVERSION": "go1.27.1",
    }
    verdict = read("benchmark-gate.txt")
    assert verdict.count("confirmed performance regression:") == 1
    assert LEAF.removeprefix("Benchmark") in verdict and "+11.86%" in verdict and "+8.27%" in verdict
    for name in ("benchmark-control-gate.txt", "benchmark-control-reversed-gate.txt"):
        assert read(name).startswith("performance gate passed:")
    return expected, verdict


def bind(zip_path, output):
    assert zip_path.stat().st_size == 121476
    assert sha256(zip_path.read_bytes()).hexdigest() == DIGEST
    with ZipFile(zip_path) as archive:
        assert archive.testzip() is None

        def read(name):
            member, = (member for member in archive.namelist() if Path(member).name == name)
            return archive.read(member).decode()

        expected, verdict = verify_original(read)
        (output / "original-expected.json").write_text(expected)
        (output / "original-gate.txt").write_text(verdict)
        (output / "original-profile.txt").write_text(read("benchmark-compile-profile.txt"))
        (output / "original-batch.tsv").write_text(BATCH)
    print(json.dumps({"run": 45701411412, "job": 139513327496, "artifact": 15063828133,
                      "digest": DIGEST, "base": BASE, "head": HEAD, "cpu": CPU, "profile": PROFILE,
                      "name": LEAF}, indent=2))


def smoke(variant, path):
    lines = path.read_text().splitlines()
    assert all(value in lines for value in ("goos: linux", "goarch: amd64", f"cpu: {CPU}",
                                              "pkg: github.com/light-speak/luxo/" + PACKAGE, "PASS"))
    names = []
    for line in lines:
        if not line.startswith("Benchmark"):
            continue
        row = line.split()
        assert len(row) == 8 and row[1] == "1" and row[3::2] == ["ns/op", "B/op", "allocs/op"], row
        assert all(math.isfinite(float(x)) and float(x) >= 0 for x in row[2::2]), row
        names.append(row[0])
    assert sorted(names) == sorted(expected_for(variant)), names


def disassembly(path):
    functions = {}
    name = None
    for line in path.read_text().splitlines():
        if line.startswith("TEXT "):
            name = line.split()[1].removesuffix("(SB)")
            assert name not in functions, name
            functions[name] = {"bytes": [], "calls": set()}
            continue
        match = INSTRUCTION.search(line)
        if match and name:
            _, opcode, text = match.groups()
            functions[name]["bytes"].append(bytes.fromhex(opcode))
            call = CALL.search(text)
            if call:
                functions[name]["calls"].add(call.group(1))
    return functions


def symbols(directory, arm, variant):
    functions = disassembly(directory / f"{arm}.hot.objdump.txt")
    result = {}
    for line in (directory / f"{arm}.hot.nm.txt").read_text().splitlines():
        address, size, kind, name = line.split(maxsplit=3)
        address = int(address, 16)
        entry = {"address": hex(address), "size": int(size), "align16": address % 16,
                 "align32": address % 32, "align64": address % 64, "pageOffset": address % 4096, "kind": kind}
        if name in functions:
            raw = b"".join(functions[name]["bytes"])
            entry.update(disassembledBytes=len(raw), opcodeSha256=sha256(raw).hexdigest(),
                         calls=sorted(functions[name]["calls"]))
        result[name] = entry
    required = "BenchmarkDiagnosticWriteParseError" if variant == "parse" else ROOT
    assert any(required in name and entry.get("disassembledBytes") for name, entry in result.items()), required
    if variant != "parse":
        assert any(name.endswith(".localRouteSnapshot") for name in result)
    return result


def compare(variant, directory):
    left, right = (symbols(directory, arm, variant) for arm in ("A", "B"))
    detail = {}
    for name in sorted(left.keys() | right.keys()):
        a, b = left.get(name), right.get(name)
        detail[name] = {"A": a, "B": b,
                        "sameOpcodes": bool(a and b and a.get("opcodeSha256") and a["opcodeSha256"] == b.get("opcodeSha256")),
                        "sameCallTargets": bool(a and b and "calls" in a and a["calls"] == b.get("calls"))}
    print(json.dumps(detail, indent=2))


if __name__ == "__main__":
    mode, *args = sys.argv[1:]
    if mode == "bind":
        bind(*map(Path, args))
    elif mode == "plan":
        print(json.dumps(expected_rows(expected_for(*args)), indent=2))
    elif mode == "smoke":
        smoke(args[0], Path(args[1]))
    elif mode == "compare":
        compare(args[0], Path(args[1]))
    else:
        raise SystemExit("use bind, plan, smoke or compare")
