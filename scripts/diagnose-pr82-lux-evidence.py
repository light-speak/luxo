"""Bind a PR 82 strict-filter and decimal benchmark experiment to its host."""

from collections import Counter
import csv
from hashlib import sha256
import json
from pathlib import Path
import re
import statistics
import sys
from zipfile import ZipFile


PACKAGE = "github.com/light-speak/luxo/pkg/lux"
BASE = "925f30ee1dcd45882b29130b8ed02836ed88317e"
TESTED = "d98e446c99e1ed2b70a8619fca249c1c9aa45000"
GROUPS = ("primary", "confirmation")
COMPARISONS = ("aa", "ab", "isolated-aa", "isolated-ab")
STRICT = "BenchmarkStrictFilterContract"
DECIMAL = "BenchmarkDecimalAlignment"
FULL = {f"{STRICT}/{item}/{side}-2" for item in
        ("Int", "Float", "Bool", "Time", "String", "String1KiB", "Contains", "Search")
        for side in ("Before", "After")}
FULL.update(f"{DECIMAL}/{operation}/{case}/{side}-2" for operation in ("Add", "Sub")
            for case in ("same", "near", "zero") for side in ("before", "after"))
SCOPES = {
    "04": {"cpu": "AMD EPYC 7763 64-Core Processor", "failure": f"{STRICT}/Time/After-2",
           "control": f"{STRICT}/Time/Before-2", "artifact": 14919831290, "job": 138424262576,
           "sha256": "b045b4a000f6290a68ef4860c1f292ef09c4add6593fa00f385d3e7e935daff4"},
    "08": {"cpu": "Intel(R) Xeon(R) 6975P-C", "failure": f"{DECIMAL}/Add/zero/after-2",
           "control": f"{DECIMAL}/Add/zero/before-2", "artifact": 14919152905, "job": 138424262618,
           "sha256": "addc34c9a86d6f20a77b02321bfcb835b7de20ac1f9e7948d45935d6336adca3"},
}


def write_json(root, file, value):
    (root / file).write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def selected(root):
    choice = json.loads((root / "native-scope.json").read_text())
    return SCOPES[choice["shard"]]


def select(root):
    observed = re.findall(r"^Model name:\s*(.+)$", (root / "environment.txt").read_text(), re.M)
    assert len(observed) == 1
    choices = [key for key, spec in SCOPES.items() if spec["cpu"].casefold() == observed[0].strip().casefold()]
    write_json(root, "native-scope.json", {"actualCPU": observed[0].strip(), "shard": choices[0] if choices else None})
    if not choices:
        print("Run this comparison on the original AMD 7763 or Intel 6975P-C model", file=sys.stderr)
        raise SystemExit(3)
    print(choices[0])


def measure(text, names):
    rows = {}
    for line in text.splitlines():
        fields = line.split()
        if not fields or fields[0] not in names:
            continue
        name, count, time, time_unit, size, size_unit, alloc, alloc_unit = fields
        assert (time_unit, size_unit, alloc_unit) == ("ns/op", "B/op", "allocs/op")
        row = (int(count), float(time), int(size), int(alloc))
        assert row[0] > 0 and all(n >= 0 for n in row[1:])
        rows.setdefault(name, []).append(row)
    return rows


def prior(root, archive):
    spec = selected(root)
    assert sha256(archive.read_bytes()).hexdigest() == spec["sha256"], "official artifact digest changed"
    names = {spec["failure"], spec["control"]}
    medians = {}
    with ZipFile(archive) as z:
        assert z.testzip() is None
        assert z.read("benchmark-revisions.txt").decode() == f"base_sha={BASE}\ntested_sha={TESTED}\n"
        expected = {f"pkg/lux\t{name}" for name in names}
        assert set(z.read("benchmark-confirmation-targets.tsv").decode().splitlines()) == expected
        for group in GROUPS:
            medians[group] = {}
            for side in ("base", "head"):
                raw = z.read(f"benchmark-raw-{group}-{side}.txt").decode()
                cpus = {line[5:].strip().casefold() for line in raw.splitlines() if line.startswith("cpu: ")}
                assert cpus == {spec["cpu"].casefold()}
                rows = measure(raw, names)
                assert set(rows) == names and all(len(samples) == 10 for samples in rows.values())
                medians[group][side] = {name: [statistics.median(row[i] for row in samples) for i in (1, 2, 3)]
                                        for name, samples in rows.items()}
    rows = [{"Package": "pkg/lux", "Name": name} for name in sorted(names)]
    write_json(root, "expected.json", {"Base": rows, "Head": rows})
    filters = ["/".join(f"^{re.escape(part)}$" for part in name.removesuffix("-2").split("/")) for name in sorted(names)]
    (root / "filter.txt").write_text("|".join(filters) + "\n")
    write_json(root, "official-artifact.json", {**spec, "run": 45387275899, "attempt": 1, "tested": TESTED,
                                                 "base": BASE, "mediansNsBytesAllocs": medians})


def raw(path, names, at_one=False):
    text = path.read_text()
    lines = [line.rstrip() for line in text.splitlines()]
    rows = measure(text, names)
    assert lines[2] == "pkg: " + PACKAGE and lines[-1] == "PASS", path
    assert len(lines) == len(names) + 5 and set(rows) == names, path
    assert all(len(samples) == 1 and (not at_one or samples[0][0] == 1) for samples in rows.values()), path
    return lines[:4]


def inventory(root):
    defaults = [(root / f"{role}-default.roots").read_text().splitlines() for role in ("base", "head")]
    assert [len(items) for items in defaults] == [137, 140]
    assert set(defaults[0]) <= set(defaults[1])
    added = set(defaults[1]) - set(defaults[0])
    assert len(added) == 3 and all(name.startswith("TestRawCondition") for name in added)
    hosts = set()
    for role in ("base", "head"):
        roots = (root / f"{role}-isolated.roots").read_text().splitlines()
        assert len(roots) == 2 and set(roots) == {STRICT, DECIMAL}
        log = (root / f"{role}-ordinary-tests.log").read_text()
        assert log.splitlines()[-1] == "PASS"
        if role == "head":
            assert all(f"--- PASS: {name} " in log for name in added)
        for variant in ("default", "isolated"):
            hosts.add(tuple(raw(root / f"{role}-{variant}.inventory.txt", FULL, at_one=True)))
    assert len(FULL) == 28 and len(hosts) == 1
    write_json(root, "inventory.json", {"ordinaryRoots": [137, 140], "ordinaryNewCorrectness": sorted(added),
                                        "isolatedLeaves": sorted(FULL), "host": hosts.pop()})


def code(root):
    text = []
    asm = []
    for role in ("base", "head"):
        ordinary, isolated = ((root / f"{role}-{variant}.nm").read_text() for variant in ("default", "isolated"))
        symbol = "lux.(*rawCond).ToSQL"
        assert symbol in ordinary and symbol not in isolated, "raw SQL path entered the isolated linkage"
        assert "lux.BenchmarkStrictFilterContract" in isolated and "lux.BenchmarkDecimalAlignment" in isolated
        text.append(sorted(line for line in isolated.splitlines() if re.match(r"^\s*\w+\s+\d+\s+[Tt]\s+", line)))
        asm.append((root / f"{role}-isolated.asm").read_bytes())
    write_json(root, "isolated-closure.json", {"sameTextLayout": text[0] == text[1], "textSymbols": list(map(len, text)),
                                               "sameWatchedDisassembly": asm[0] == asm[1],
                                               "rawSQLLinked": {"ordinary": True, "isolated": False}})


def samples(root):
    spec = selected(root)
    names = {spec["failure"], spec["control"]}
    order = []
    distribution = Counter()
    for offset, group in enumerate(GROUPS):
        for pair in range(1, 11):
            sides = ("left", "right") if (pair + offset) % 2 else ("right", "left")
            for index in range(4):
                comparison = COMPARISONS[(index + pair + offset + 1) % 4]
                variant = "isolated" if comparison.startswith("isolated-") else "default"
                for side in sides:
                    role = "head" if comparison.endswith("ab") and side == "right" else "base"
                    file = f"raw/{group}.{pair:02}.{comparison}.01.{side}.txt"
                    order.append([group, f"{pair:02}", comparison, "01", side, role, variant, file])
                    host = raw(root / file, names)
                    assert host[:3] == ["goos: linux", "goarch: amd64", "pkg: " + PACKAGE]
                    assert host[3].casefold() == ("cpu: " + spec["cpu"]).casefold()
                    distribution[comparison] += 1
    with (root / "order.tsv").open(newline="") as f:
        assert list(csv.reader(f, delimiter="\t")) == ["group pair comparison case side revision variant raw".split(), *order]
    assert {row[-1] for row in order} == {"raw/"+path.name for path in (root / "raw").iterdir()}
    write_json(root, "samples.json", {"cpu": spec["cpu"], "processes": len(order), "benchmarkRows": len(order)*len(names),
                                      "byComparison": dict(distribution)})


def verdict(root):
    with (root / "verdicts.tsv").open(newline="") as f:
        status = {row["comparison"]: int(row["gate_exit"]) for row in csv.DictReader(f, delimiter="\t")}
    assert set(status) == set(COMPARISONS)
    prefix = "confirmed performance regression: " + PACKAGE + "/"
    observed = {line.removeprefix(prefix).split(" +", 1)[0]
                for line in (root / "ab.benchgate.log").read_text().splitlines() if line.startswith(prefix)}
    expected = {selected(root)["failure"].removeprefix("Benchmark") + " sec/op"}
    if status["aa"] or status["isolated-aa"] or status["isolated-ab"]:
        raise SystemExit("Same-build control or isolated A/H failed the original gate")
    if status["ab"] != 1 or observed != expected:
        print("Inconclusive: ordinary A/H did not reproduce precisely its formal failure", file=sys.stderr)
        raise SystemExit(3)


if __name__ == "__main__":
    commands = {"select": select, "prior": prior, "inventory": inventory, "code": code, "samples": samples, "verdict": verdict}
    commands[sys.argv[1]](*(Path(arg) for arg in sys.argv[2:]))
