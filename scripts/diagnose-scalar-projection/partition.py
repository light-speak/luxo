import json
from pathlib import Path
import re
import sys


def names_for(evidence):
    expected = json.loads((evidence / "benchmark-expected.json").read_text())
    base, head = ([row["Name"] for row in expected[side]] for side in ("Base", "Head"))
    if base != head or len(base) != 18 or len(set(base)) != 18:
        raise ValueError("unexpected 18-case inventory or mismatched arms")
    if any(row["Package"] != "pkg/codegen" for side in expected.values() for row in side):
        raise ValueError("expected one codegen package")
    return set(base)


def check_inventory(evidence):
    names = names_for(evidence)
    for side in ("base", "head"):
        data = (evidence / f"inventory-{side}.txt").read_text()
        pattern = r"^(Benchmark\S+)[ \t]+1[ \t]+\S+ ns/op[ \t]+\S+ B/op[ \t]+\S+ allocs/op$"
        measured = re.findall(pattern, data, re.M)
        if set(measured) != names or len(measured) != len(names) or data.splitlines().count("PASS") != 5:
            raise ValueError(f"{side} inventory does not match all 18 leaves and 5 successful processes")
    print("1x inventory passed for 18 exact benchmarks in each arm")


def categorise(evidence):
    names = names_for(evidence)
    scope = json.loads((evidence / "scope.json").read_text())
    changed = set(scope["replay"]["semanticallyChanged"])
    pagination = {"BenchmarkAuditScalarPaginationPlan/destructured-query-2"}
    if len(changed) != 6 or not (pagination < changed < names):
        raise ValueError("expected six repair measurements including one changed pagination plan")
    return {
        "equivalent": names - changed,
        "projection-repair": changed - pagination,
        "pagination-repair": pagination,
    }


def partition_sample(input_path, category_for):
    partitioned = {category: [] for category in category_for.values()}
    frame, rows = [], {}
    for line in input_path.read_text().splitlines():
        if line.startswith("Benchmark"):
            category = category_for[line.split()[0]]
            rows.setdefault(category, []).append(line)
        elif line == "PASS":
            if not rows:
                raise ValueError(f"empty sample frame in {input_path.name}")
            for category, selected in rows.items():
                partitioned[category].extend(frame + selected + ["PASS"])
            frame, rows = [], {}
        elif line:
            frame.append(line)
    if frame or rows:
        raise ValueError(f"incomplete sample frame in {input_path.name}")
    return partitioned


def split_group(evidence, group):
    categories = categorise(evidence)
    category_for = {name: category for category, names in categories.items() for name in names}
    for category, names in categories.items():
        cases = [{"Package": "pkg/codegen", "Name": name} for name in sorted(names)]
        expected = {"Base": cases, "Head": cases}
        (evidence / f"expected-{category}.json").write_text(json.dumps(expected, indent=2) + "\n")
    for side in ("base", "head"):
        source = evidence / f"benchmark-{group}-{side}.txt"
        partitioned = partition_sample(source, category_for)
        for category, rows in partitioned.items():
            (evidence / f"{category}-{group}-{side}.txt").write_text("\n".join(rows) + "\n")


def main():
    mode, evidence, *rest = sys.argv[1:]
    evidence = Path(evidence)
    if mode == "inventory" and not rest:
        check_inventory(evidence)
    elif mode == "sample" and rest in (["primary"], ["confirmation"]):
        split_group(evidence, rest[0])
    else:
        raise ValueError("expected inventory EVIDENCE or sample EVIDENCE {primary|confirmation}")


if __name__ == "__main__":
    main()
