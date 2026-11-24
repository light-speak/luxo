"""Verify the test-only fixture split for PR 72 on both revisions."""
import argparse
import csv
import hashlib
import json
from pathlib import Path
import re

PACKAGE = "github.com/light-speak/luxo/pkg/lux/codec"
VARIANTS = {"01": "writer", "02": "reader"}
BENCHMARKS = {"01": "BenchmarkArrayHeaderWrite", "02": "BenchmarkElementPayload"}
OMISSIONS = {
    "writer": (
        "payload_bench_test.go",
        "b254012969b1c2d19d626217f3bb53c571325f03c3bccffef7ea5a871303d76b",
        {"BenchmarkElementPayload", "BenchmarkMissingElementPayload"},
    ),
    "reader": (
        "array_write_bench_test.go",
        "14807acdbef627443873f3c0899becbd924e7533a122c63f7486957f6ea900e2",
        {"BenchmarkArrayHeaderWrite"},
    ),
}
PACKAGE_FIELDS = (
    "ImportPath", "Name", "GoFiles", "CgoFiles", "CFiles", "CXXFiles", "MFiles",
    "HFiles", "FFiles", "SFiles", "SwigFiles", "SwigCXXFiles", "SysoFiles",
    "EmbedFiles", "TestEmbedFiles", "XTestGoFiles", "XTestEmbedFiles",
)
NUMBER = r"[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?"
MEASUREMENT = re.compile(
    rf"^(Benchmark\S+)\s+(\d+)\s+{NUMBER} ns/op\s+{NUMBER} B/op\s+{NUMBER} allocs/op$"
)


def read_json(path):
    return json.loads(path.read_text())


def write_json(path, value):
    path.write_text(json.dumps(value, sort_keys=True, indent=2) + "\n")


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def require(condition, message):
    if not condition:
        raise SystemExit(message)


def make_overlays(source, frozen, role, evidence):
    frozen = frozen.resolve(strict=True)
    original = read_json(frozen)["Replace"]
    manifest = {"fullOverlaySHA256": sha(frozen), "variants": {}}
    for variant, (name, digest, _) in OMISSIONS.items():
        target = str(source.resolve(strict=True) / "pkg/lux/codec" / name)
        fixture = frozen.parent / name
        require(original.get(target) == str(fixture) and sha(fixture) == digest,
                f"{role}: missing or changed frozen fixture {name}")
        split = dict(original)
        split[target] = ""
        path = frozen.parent / f"split-{role}-{variant}.json"
        require(not path.exists(), f"unexpected existing split overlay {path}")
        write_json(path, {"Replace": split})
        manifest["variants"][variant] = {
            "omittedFrozenFile": name, "omittedFileSHA256": digest, "overlaySHA256": sha(path),
        }
        print(f"{variant}\t{path}")
    write_json(evidence / f"split-overlays-{role}.json", manifest)


def expected_cases(evidence):
    expected = read_json(evidence / "expected.json")
    require(expected["Base"] == expected["Head"], "baseline and candidate expected inventories differ")
    leaves = [row["Name"] for row in expected["Base"]]
    require(all(row["Package"] == "pkg/lux/codec" for row in expected["Base"])
            and len(leaves) == len(set(leaves)) == 8, "expected eight distinct codec leaves")
    cases = {case: {leaf for leaf in leaves if leaf.startswith(root + "/")}
             for case, root in BENCHMARKS.items()}
    require(all(len(names) == 4 for names in cases.values()), "expected four leaves in each fixture family")
    return cases


def benchmark_roots(path):
    names = [line for line in path.read_text().splitlines() if line.startswith("Benchmark")]
    require(names and len(names) == len(set(names)), f"invalid benchmark root list {path.name}")
    return set(names)


def measure_names(path, names, one_iteration=False):
    lines = [line.strip() for line in path.read_text().splitlines()]
    headers = {
        key: [line.partition(": ")[2] for line in lines if line.startswith(key + ": ")]
        for key in ("goos", "goarch", "pkg", "cpu")
    }
    require(all(len(value) == 1 and value[0] for value in headers.values())
            and headers["pkg"] == [PACKAGE] and lines[-1:] == ["PASS"], f"invalid benchmark output {path}")
    rows = [MEASUREMENT.fullmatch(line) for line in lines if line.startswith("Benchmark")]
    require(all(rows), f"missing metric or invalid measurement {path}")
    leaves = [row[1] for row in rows]
    require(set(leaves) == names and len(leaves) == len(names), f"unexpected or duplicate leaf {path}")
    if one_iteration:
        require(all(row[2] == "1" for row in rows), f"non-1x inventory measurement {path}")
    return tuple(headers[name][0] for name in ("goos", "goarch", "cpu"))


def verify_variant(evidence, role, variant, full, roots):
    path = evidence / f"split-files-{role}-{variant}.json"
    split = read_json(path)
    filename, _, omitted = OMISSIONS[variant]
    for key in PACKAGE_FIELDS:
        require(full.get(key) == split.get(key), f"{role}-{variant}: changed package file set {key}")
    full_tests = full["TestGoFiles"]
    require(full_tests.count(filename) == 1
            and split["TestGoFiles"] == [name for name in full_tests if name != filename],
            f"{role}-{variant}: unexpected test-file removals")
    split_roots = benchmark_roots(evidence / f"split-roots-{role}-{variant}.txt")
    require(omitted <= roots and split_roots == roots - omitted,
            f"{role}-{variant}: unexpected removed benchmark roots")
    return split_roots


def verify_inventory(evidence):
    cases = expected_cases(evidence)
    hosts = set()
    root_inventories = []
    for role in ("base", "candidate"):
        full = read_json(evidence / f"split-files-{role}-default.json")
        roots = benchmark_roots(evidence / f"split-roots-{role}-default.txt")
        root_inventories.append(roots)
        combined = set().union(*(verify_variant(evidence, role, variant, full, roots) for variant in OMISSIONS))
        require(combined == roots, f"{role}: split benchmark roots do not reconstruct full inventory")
        for case, names in cases.items():
            for variant in ("default", VARIANTS[case]):
                path = evidence / f"split-inventory-{role}-{variant}-{case}.txt"
                hosts.add(measure_names(path, names, one_iteration=True))
    require(root_inventories[0] == root_inventories[1], "baseline and candidate root lists differ")
    require(len(hosts) == 1, "benchmark inventory was sampled on different platforms")
    print("Two roles retain the full benchmark-root union and eight exact 1x leaves on one host")


def verify_samples(evidence):
    cases = expected_cases(evidence)
    comparisons = ("aa", "ap", "split-aa", "split-ap")
    hosts, expected_paths, expected_order = set(), set(), []
    for phase, group in enumerate(("primary", "confirmation")):
        for pair in range(1, 11):
            sides = ("left", "right") if (pair + phase) % 2 else ("right", "left")
            for case, names in cases.items():
                for index in range(len(comparisons)):
                    comparison = comparisons[(index + pair + phase + int(case)) % len(comparisons)]
                    variant = VARIANTS[case] if comparison.startswith("split-") else "default"
                    for side in sides:
                        role = "candidate" if side == "right" and comparison.endswith("ap") else "base"
                        filename = f"raw/{group}.{pair:02}.{comparison}.{case}.{side}.txt"
                        expected_paths.add(filename)
                        expected_order.append([group, f"{pair:02}", comparison, case, side, role, variant, filename])
                        hosts.add(measure_names(evidence / filename, names))
    observed_paths = {path.relative_to(evidence).as_posix() for path in (evidence / "raw").iterdir()}
    require(observed_paths == expected_paths, "missing or unexpected raw samples")
    with (evidence / "order.tsv").open(newline="") as manifest:
        rows = list(csv.reader(manifest, delimiter="\t"))
    header = "group pair comparison case side revision variant raw".split()
    require(rows == [header, *expected_order], "raw samples do not match the paired order and profiles")
    require(len(hosts) == 1 and next(iter(hosts))[:2] == ("linux", "amd64"),
            "samples use different hosts or an unexpected platform")
    print(f"Confirmed {len(expected_paths)} complete native process samples with exact per-case metrics")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    subcommands = parser.add_subparsers(dest="command", required=True)
    overlays = subcommands.add_parser("make-overlays")
    overlays.add_argument("source", type=Path)
    overlays.add_argument("frozen", type=Path)
    overlays.add_argument("role", choices=("base", "candidate"))
    overlays.add_argument("evidence", type=Path)
    for command in ("verify-inventory", "verify-samples"):
        subcommands.add_parser(command).add_argument("evidence", type=Path)
    args = parser.parse_args()
    if args.command == "make-overlays":
        make_overlays(args.source, args.frozen, args.role, args.evidence)
    elif args.command == "verify-inventory":
        verify_inventory(args.evidence)
    else:
        verify_samples(args.evidence)


if __name__ == "__main__":
    main()
