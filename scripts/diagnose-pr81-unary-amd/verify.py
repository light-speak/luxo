"""Verify real benchmark sources, CPU, sample pairs, and the historical receipt."""
import csv
import hashlib
import itertools
import json
import math
from pathlib import Path
import re
import statistics
import sys
import zipfile


CPU = "AMD EPYC 9V74 80-Core Processor"
PACKAGE = "pkg/lux/rpc"
GROUPS = ("primary", "confirmation")
FALSE = {f"BenchmarkRPCTraceLocal/false/{version}-2" for version in ("before", "current")}
TRACE = FALSE | {f"BenchmarkRPCTraceLocal/true/{version}-2" for version in ("before", "current")}
LEAVES = TRACE | {"BenchmarkRPC_Echo-2"}
ROLES = {"aa": ("base", "base"), "bh": ("base", "head"),
         "hc": ("head", "candidate"), "bc": ("base", "candidate")}
UNITS = ("ns/op", "B/op", "allocs/op")
FORMAL_SHA = "27da0dfaea6eb5addef98a0198d93d420908345c8d322a3f0ef89a5a3818267d"
ECHO_SHA = "dda115389a6140b1ec5f3d2098145d17eb73d66ab08df27266b4ca481ad2bef4"
OVERLAY = "rpc-boundary-overlay-v2 99658ed681f4110df0db3fc688931fa61cd656f3c62477c04a1ffd4bb567ca8e"


def require(condition, message):
    if not condition:
        raise ValueError(message)


def dump(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def context(lines):
    for name, expected in (("goos", "linux"), ("goarch", "amd64"), ("cpu", CPU)):
        actual = {line[len(name) + 1:].strip() for line in lines if line.startswith(name + ":")}
        require(actual == {expected}, f"unexpected {name}: {actual}")


def sample(line):
    fields = line.split()
    require(len(fields) == 8 and tuple(fields[3::2]) == UNITS, f"invalid sample: {line}")
    numbers = (float(fields[2]), int(fields[4]), int(fields[6]))
    require(int(fields[1]) > 0 and all(math.isfinite(value) and value >= 0 for value in numbers)
            and numbers[0] > 0, f"invalid sample values: {line}")
    return fields[0], numbers


def historical(archive, output):
    require(hashlib.sha256(archive.read_bytes()).hexdigest() == FORMAL_SHA, "formal artifact digest")
    with zipfile.ZipFile(archive) as bundle:
        read = lambda name: bundle.read(name).decode("utf-8")
        require(bundle.testzip() is None, "invalid ZIP")
        require(read("benchmark-revisions.txt").splitlines() == [
            "base_sha=1117eb9ffcc0beef977abceec656a351b0988389",
            "tested_sha=53698c53ba297ba48c9cf259560d7116ce6fee68",
        ], "formal tested sources changed")
        require(OVERLAY in read("benchmark-compile-profile.txt").splitlines(), "formal RPC overlay")
        actual = set(read("benchmark-confirmation-targets.tsv").splitlines())
        require(actual == {PACKAGE + "\t" + leaf for leaf in FALSE}, "formal confirmed leaf set")
        data = {}
        for group, side in itertools.product(GROUPS, ("base", "head")):
            lines = read(f"benchmark-raw-{group}-{side}.txt").splitlines()
            context(lines)
            names = TRACE if group == "primary" else FALSE
            measured = {name: [] for name in names}
            for line in lines:
                name = line.partition(" ")[0].split("\t")[0]
                if name in measured:
                    leaf, values = sample(line)
                    measured[leaf].append(values)
            require(all(len(values) == 10 for values in measured.values()), "formal sample counts")
            data[f"{group}-{side}"] = measured
    for side in ("base", "head"):
        require(all(bytes_op == 154 and allocs_op == 4
                    for values in data[f"confirmation-{side}"].values()
                    for _, bytes_op, allocs_op in values), "formal confirmed allocation identity")
    comparisons = {}
    for label, names, groups in (("falseComparisons", FALSE, GROUPS),
                                 ("tracedControls", TRACE - FALSE, ("primary",))):
        rows = []
        for group, leaf in itertools.product(groups, sorted(names)):
            left, right = (data[f"{group}-{side}"][leaf] for side in ("base", "head"))
            rows.append({
                "group": group, "leaf": leaf,
                "mediansNs": [statistics.median(row[0] for row in series) for series in (left, right)],
                "pairedMaxNs": round(max(r[0] - l[0] for l, r in zip(left, right)), 6),
                "observedAllocationPairs": [sorted({(row[1], row[2]) for row in series})
                                            for series in (left, right)],
            })
        comparisons[label] = rows
    dump(output / "formal-receipt.json", {"run": 45414110028, "artifact": 14931523390,
         "sha256": FORMAL_SHA, "cpu": CPU, **comparisons})
    cases = [{"Package": PACKAGE, "Name": leaf} for leaf in sorted(LEAVES)]
    dump(output / "expected.json", {"Base": cases, "Head": cases})
    (output / "rpc-profile.txt").write_text(OVERLAY + "\n", encoding="utf-8")


def expected_schedule():
    for offset, group in enumerate(GROUPS):
        for number in range(1, 11):
            for index in range(len(ROLES)):
                comparison = tuple(ROLES)[(index + number + offset) % len(ROLES)]
                sides = ("left", "right") if (number + offset) % 2 else ("right", "left")
                for side in sides:
                    role = ROLES[comparison][side == "right"]
                    pair = f"{number:02}"
                    yield (group, pair, comparison, "01", side, role, "default",
                           f"raw/{group}.{pair}.{comparison}.01.{side}.txt")


def fresh(output):
    with (output / "order.tsv").open(encoding="utf-8", newline="") as stream:
        rows = list(csv.reader(stream, delimiter="\t"))
    require(rows[0] == "group pair comparison case side revision variant raw".split(), "schedule header")
    require([tuple(row) for row in rows[1:]] == list(expected_schedule()), "samples differ from schedule")
    recorded = {}
    require(len(list((output / "raw").glob("*.txt"))) == len(rows) - 1, "unexpected raw sample files")
    for group, pair, comparison, _, side, role, _, path in rows[1:]:
        lines = (output / path).read_text(encoding="utf-8").splitlines()
        context(lines)
        require({line[4:].strip() for line in lines if line.startswith("pkg:")} ==
                {"github.com/light-speak/luxo/" + PACKAGE}, f"unexpected package: {path}")
        measurements = [sample(line) for line in lines if line.startswith("Benchmark")]
        require(len(measurements) == len(LEAVES) and {leaf for leaf, _ in measurements} == LEAVES,
                f"incorrect benchmark set: {path}")
        recorded[(group, pair, comparison, side)] = dict(measurements)
    comparisons = {}
    for group, comparison in itertools.product(GROUPS, ROLES):
        details = {}
        for leaf in sorted(LEAVES):
            changes = [
                [round(right - left, 6) for left, right in zip(
                    recorded[(group, f"{pair:02}", comparison, "left")][leaf],
                    recorded[(group, f"{pair:02}", comparison, "right")][leaf]
                )] for pair in range(1, 11)
            ]
            details[leaf] = {
                unit: {"signedPairs": [pair[index] for pair in changes],
                       "positiveMax": max(0, max(pair[index] for pair in changes))}
                for index, unit in enumerate(UNITS)
            }
        comparisons[f"{group}-{comparison}"] = details
    dump(output / "paired-deltas.json", {"cpu": CPU, "legs": len(rows) - 1, "comparisons": comparisons})


def echo(path):
    declarations = re.findall(
        rb"^func BenchmarkRPC_Echo\(b \*testing\.B\) \{\n[\s\S]+?^\}\n",
        path.read_bytes(), re.MULTILINE,
    )
    require(len(declarations) == 1, "expected a single real TCP Echo benchmark")
    digest = hashlib.sha256(declarations[0]).hexdigest()
    require(digest == ECHO_SHA, "timed TCP Echo workload changed")
    print(f"{digest}  BenchmarkRPC_Echo")


if __name__ == "__main__":
    command, *args = sys.argv[1:]
    if command == "formal":
        historical(*map(Path, args))
    elif command == "samples":
        fresh(*map(Path, args))
    elif command == "echo":
        echo(*map(Path, args))
    else:
        raise SystemExit("unknown verifier command")
