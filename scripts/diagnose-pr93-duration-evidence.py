"""Audit the exact four codegen leaves and the linked duration emitter."""

from collections import Counter
import csv
from hashlib import sha256
import json
from pathlib import Path
import re
import statistics
import struct
import sys
from zipfile import ZipFile


CPU = "AMD EPYC 7763 64-Core Processor"
PACKAGE = "github.com/light-speak/luxo/pkg/codegen"
ROOT = "BenchmarkCompileDecimalIntegerLiterals"
GROUPS = ("primary", "confirmation")
COMPARISONS = ("aa", "ab", "isolated-aa", "isolated-ab")
NAMES = {f"{ROOT}/{name}-2" for name in
         ("int_plain", "int_leading_zero", "duration_plain", "duration_leading_zero")}
DURATION = {name for name in NAMES if "/duration_" in name}
BASE = "925f30ee1dcd45882b29130b8ed02836ed88317e"
TESTED = "40c81f341753bdf60b689a0ec1f15a608bb264ae"
ARCHIVE = "e7ad88b0ebdf9baf9fe7ef49ee6645d489ab0be469e21d6b4ade4b0074d49d48"


def write_json(root, file, value):
    (root / file).write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def read_rows(text, names):
    rows = {}
    for line in text.splitlines():
        pieces = line.split()
        if not pieces or pieces[0] not in names:
            continue
        name, count, time, time_unit, size, size_unit, alloc, alloc_unit = pieces
        assert (time_unit, size_unit, alloc_unit) == ("ns/op", "B/op", "allocs/op")
        row = (int(count), float(time), int(size), int(alloc))
        assert row[0] > 0 and all(value >= 0 for value in row[1:])
        rows.setdefault(name, []).append(row)
    return rows


def prior(root, archive):
    assert sha256(archive.read_bytes()).hexdigest() == ARCHIVE, "official artifact digest changed"
    medians = {}
    expected_targets = [f"pkg/codegen\t{name}" for name in sorted(DURATION)]
    with ZipFile(archive) as z:
        def read(file):
            return z.read(file).decode()
        assert z.testzip() is None
        assert read("benchmark-revisions.txt") == f"base_sha={BASE}\ntested_sha={TESTED}\n"
        assert read("benchmark-confirmation-targets.tsv").splitlines() == expected_targets
        for group in GROUPS:
            medians[group] = {}
            for side in ("base", "head"):
                raw = read(f"benchmark-raw-{group}-{side}.txt")
                assert {line[5:].strip() for line in raw.splitlines() if line.startswith("cpu: ")} == {CPU}
                rows = read_rows(raw, DURATION)
                assert set(rows) == DURATION and all(len(row) == 10 for row in rows.values())
                assert all(values[2:] == (24, 1) for samples in rows.values() for values in samples)
                medians[group][side] = {name: statistics.median(row[1] for row in samples)
                                        for name, samples in rows.items()}
    write_json(root, "official-artifact.json", {
        "run": 45386469196, "attempt": 1, "job": 138421315041, "artifact": 14917073661,
        "sha256": ARCHIVE, "base": BASE, "tested": TESTED, "cpu": CPU,
        "timingMediansNs": medians, "bytesPerOp": 24, "allocationsPerOp": 1,
    })


def raw_measurements(path, one_iteration=False):
    text = path.read_text()
    rows = read_rows(text, NAMES)
    lines = text.splitlines()
    assert lines[2] == "pkg: " + PACKAGE and lines[-1] == "PASS", path
    assert len(lines) == len(NAMES) + 5, path
    assert set(rows) == NAMES and all(len(samples) == 1 for samples in rows.values()), path
    if one_iteration:
        assert all(samples[0][0] == 1 for samples in rows.values()), path
    return tuple(line.rstrip() for line in lines[:4])


def inventory(root):
    ordinary = [(root / f"{role}-default.roots").read_text().splitlines() for role in ("base", "head")]
    assert ordinary[0] == ordinary[1] and ROOT in ordinary[0]
    assert "BenchmarkGenerateNumericLiteralBindings" in ordinary[0]
    tests = {"TestDecimalLeadingZeroLiteralEmitters", "TestDecimalLeadingZeroGeneratesCanonicalSource",
             "TestNonIntegerLeadingZeroLiteralControls"}
    assert tests <= set(ordinary[0])
    hosts = set()
    for role in ("base", "head"):
        assert (root / f"{role}-isolated.roots").read_text().splitlines() == [ROOT]
        observed = (root / f"{role}-ordinary-tests.log").read_text()
        assert all(f"--- PASS: {name} " in observed for name in tests)
        for variant in ("default", "isolated"):
            hosts.add(raw_measurements(root / f"{role}-{variant}.inventory.txt", one_iteration=True))
    assert len(hosts) == 1
    rows = [{"Package": "pkg/codegen", "Name": name} for name in sorted(NAMES)]
    write_json(root, "expected.json", {"Base": rows, "Head": rows})
    write_json(root, "inventory.json", {"ordinaryRegistrySize": len(ordinary[0]), "leaves": sorted(NAMES),
                                        "ordinaryCorrectnessTests": sorted(tests), "host": hosts.pop()})


def elf_sections(path):
    data = path.read_bytes()
    assert data[:6] == b"\x7fELF\x02\x01", path
    table = struct.unpack_from("<Q", data, 40)[0]
    size, count, string_index = struct.unpack_from("<HHH", data, 58)
    sections = [struct.unpack_from("<IIQQQQIIQQ", data, table + size*i) for i in range(count)]
    start, length = sections[string_index][4:6]
    strings = data[start:start+length]
    result = {}
    for name, kind, flags, address, start, length, *_ in sections:
        label = strings[name:strings.index(0, name)].decode()
        content = data[start:start+length] if kind != 8 else b""
        result[label] = (flags, address, length, sha256(content).hexdigest())
    return result


def code(root, binaries):
    roles = ("base", "head")
    text = []
    for role in roles:
        nm = (root / f"{role}-isolated.nm").read_text()
        assert not re.search(r"semantic\..*resolveParameterTypes", nm), "semantic P1 stayed linked"
        text.append(sorted(line for line in nm.splitlines() if re.match(r"^\s*\w+\s+\d+\s+[Tt]\s+", line)))
    asm = [(root / f"{role}-isolated.asm").read_bytes() for role in roles]
    for role in asm:
        assert sum(line.startswith(b"TEXT ") for line in role.splitlines()) == 5
    sections = [elf_sections(binaries / f"{role}-isolated/pkg-codegen.test") for role in roles]
    assert sections[0].keys() == sections[1].keys()
    write_json(root, "isolated-closure.json", {
        "textSymbols": [len(rows) for rows in text], "sameTextLayout": text[0] == text[1],
        "sameWatchedDisassembly": asm[0] == asm[1], "semanticP1Linked": False,
        "sectionDifferences": sorted(name for name in sections[0] if sections[0][name] != sections[1][name]),
        "sameTextBytes": sections[0][".text"] == sections[1][".text"],
    })


def samples(root):
    expected_rows = []
    distribution = Counter()
    for offset, group in enumerate(GROUPS):
        for pair in range(1, 11):
            sides = ("left", "right") if (pair + offset) % 2 else ("right", "left")
            for step in range(4):
                comparison = COMPARISONS[(step + pair + offset + 1) % 4]
                variant = "isolated" if comparison.startswith("isolated-") else "default"
                for side in sides:
                    role = "head" if comparison.endswith("ab") and side == "right" else "base"
                    raw = f"raw/{group}.{pair:02}.{comparison}.01.{side}.txt"
                    expected_rows.append([group, f"{pair:02}", comparison, "01", side, role, variant, raw])
                    host = raw_measurements(root / raw)
                    assert host == ("goos: linux", "goarch: amd64", "pkg: " + PACKAGE, "cpu: " + CPU)
                    distribution[comparison] += 1
    with (root / "order.tsv").open(newline="") as f:
        observed = list(csv.reader(f, delimiter="\t"))
    assert observed == ["group pair comparison case side revision variant raw".split(), *expected_rows]
    assert {row[-1] for row in expected_rows} == {"raw/" + path.name for path in (root / "raw").iterdir()}
    write_json(root, "samples.json", {"cpu": CPU, "processes": len(expected_rows),
                                      "leafRows": len(expected_rows) * len(NAMES), "byComparison": dict(distribution)})


def verdict(root):
    with (root / "verdicts.tsv").open(newline="") as f:
        status = {row["comparison"]: int(row["gate_exit"]) for row in csv.DictReader(f, delimiter="\t")}
    assert set(status) == set(COMPARISONS)
    prefix = "confirmed performance regression: " + PACKAGE + "/"
    default_failures = {line.removeprefix(prefix).split(" +", 1)[0]
                        for line in (root / "ab.benchgate.log").read_text().splitlines() if line.startswith(prefix)}
    required = {name.removeprefix("Benchmark") + " sec/op" for name in DURATION}
    if status["aa"] or status["isolated-aa"] or status["isolated-ab"]:
        raise SystemExit("A/A control or isolated A/H failed the original gate")
    if status["ab"] != 1 or default_failures != required:
        print("Inconclusive: ordinary A/H did not reproduce exactly the two official duration timing failures", file=sys.stderr)
        raise SystemExit(3)


if __name__ == "__main__":
    commands = {"prior": prior, "inventory": inventory, "code": code, "samples": samples, "verdict": verdict}
    commands[sys.argv[1]](*(Path(arg) for arg in sys.argv[2:]))
