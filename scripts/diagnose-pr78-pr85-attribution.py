"""Plan and verify strict native samples for the PR 78 / PR 85 attribution."""

from csv import DictReader
from hashlib import sha256
import json
from pathlib import Path
import re
import sys

PREFIX = "github.com/light-speak/luxo/"
GROUPS = ("primary", "confirmation")
ASSETS = Path(__file__).resolve().parent / "diagnose-pr78-pr85"
CASES = {
    "01": ("pkg/lux/luvia", "default", "^BenchmarkAuthMiddlewareIsolation$/^anonymous$/^(before|after)$",
           ["BenchmarkAuthMiddlewareIsolation/anonymous/" + item for item in ("before", "after")]),
    "02": ("pkg/lux/h2transport", "default", "^BenchmarkTaskDeadlineReplacement$/^(1|8|128)$/^conditional$",
           [f"BenchmarkTaskDeadlineReplacement/{count}/conditional" for count in (1, 8, 128)]),
    "03": ("pkg/lux/h2transport", "shutdown", "^BenchmarkShutdownOneOpeningConnection$",
           ["BenchmarkShutdownOneOpeningConnection"]),
    "04": ("pkg/lux/api", "parse", "^BenchmarkDiagnosticWriteParseError$",
           [f"BenchmarkDiagnosticWriteParseError/{mode}/{kind}" for mode in ("json", "binary")
            for kind in ("BadRequest", "PayloadTooLarge", "Unavailable")]),
}
COMPARISONS = [
    ("full-aa", "base", "base", ("01",)),
    ("full-pr78", "base", "pr78", ("01",)),
    ("full-pr85", "base", "pr85", ("01",)),
    ("controlled-aa", "base-r", "base-r", ("01",)),
    ("controlled-pr78", "base-r", "pr78-r", ("01",)),
    ("controlled-pr85", "base-r", "pr85-r", ("01",)),
    ("h2-aa", "base", "base", ("02", "03")),
    ("h2-pr78", "base", "pr78", ("02", "03")),
    ("parse-aa", "base", "base", ("04",)),
    ("parse-pr85", "base", "pr85", ("04",)),
]
ORDER_KEYS = ("group", "pair", "comparison", "case", "side", "revision", "variant", "raw")
SCHEDULE_KEYS = (*ORDER_KEYS, "package", "filter")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")


def write_tsv(path, rows):
    path.write_text("".join("\t".join(row) + "\n" for row in rows))


def inventory():
    return sorted({(role, case) for _, left, right, cases in COMPARISONS
                   for role in (left, right) for case in cases})


def schedule():
    for offset, group in enumerate(GROUPS):
        for pair in range(1, 11):
            position = ((pair - 1) + offset) % len(COMPARISONS)
            comparisons = COMPARISONS[position:] + COMPARISONS[:position]
            for comparison, left, right, cases in comparisons:
                roles = [("left", left), ("right", right)]
                if (pair + offset) % 2 == 0:
                    roles.reverse()
                for case in cases:
                    package, variant, pattern, _ = CASES[case]
                    for side, role in roles:
                        raw = f"raw/{group}.{pair:02}.{comparison}.{case}.{side}.txt"
                        yield dict(zip(SCHEDULE_KEYS, (
                            group, f"{pair:02}", comparison, case, side, role, variant, raw, package, pattern,
                        )))


def plan(directory):
    directory.mkdir(parents=True, exist_ok=True)
    rows = list(schedule())
    require(len(rows) == 480 and len({row["raw"] for row in rows}) == 480, "unexpected process count")
    require(sum(len(CASES[row["case"]][3]) for row in rows) == 1280, "unexpected leaf count")
    require(len(inventory()) == 12, "unexpected inventory count")
    write_tsv(directory / "schedule.tsv", ([row[key] for key in SCHEDULE_KEYS] for row in rows))
    write_tsv(directory / "inventory.tsv",
              ([role, case, *CASES[case][:3]] for role, case in inventory()))
    write_tsv(directory / "comparisons.tsv", ([name, left, right] for name, left, right, _ in COMPARISONS))
    for name, _, _, cases in COMPARISONS:
        expected = [{"Package": CASES[case][0], "Name": leaf + "-2"}
                    for case in cases for leaf in CASES[case][3]]
        write_json(directory / f"{name}.expected.json", {"Base": expected, "Head": expected})
    print("Planned 480 process arms and 1280 raw leaf measurements.")


def source_check(source, role, patched):
    pins = json.loads((ASSETS / "pins.json").read_text())
    require(role in pins["revisions"], "unknown archived role")
    for path, roles in pins["sourceSHA256"].items():
        require(sha256((source / path).read_bytes()).hexdigest() == roles[role], f"{role}: source drift {path}")
    request_source = "pkg/lux/h2transport/request_headers.go"
    variant = "after" if patched else "before"
    digest = sha256((source / request_source).read_bytes()).hexdigest()
    require(digest == pins["requestHeadSHA256"][variant], f"{role}: request-head {variant} mismatch")
    print(f"{role} {variant} {digest} {request_source}")


def verify_process(case, path, one_iteration):
    package, _, _, leaves = CASES[case]
    lines = path.read_text().splitlines()
    require(len(lines) == len(leaves) + 5, f"{path}: unexpected raw line count")
    require(lines[:3] == ["goos: linux", "goarch: amd64", "pkg: " + PREFIX + package]
            and lines[3].startswith("cpu: ") and lines[-1] == "PASS", f"{path}: invalid runner or process verdict")
    names = []
    for row in lines[4:-1]:
        name, iterations, ns, ns_unit, memory, mem_unit, allocations, alloc_unit = row.split()
        require(int(iterations) > 0 and (not one_iteration or int(iterations) == 1)
                and (ns_unit, mem_unit, alloc_unit) == ("ns/op", "B/op", "allocs/op")
                and all(re.fullmatch(r"[0-9]+(?:\.[0-9]+)?", value) for value in (ns, memory, allocations)),
                f"{path}: invalid sample values")
        names.append(name)
    require(names == [leaf + "-2" for leaf in leaves], f"{path}: invalid case inventory or order")
    return lines[3].strip()


def verify(directory, include_samples):
    cpus = {verify_process(case, directory / f"inventory-{role}-{case}.txt", True) for role, case in inventory()}
    if include_samples:
        expected = [{key: row[key] for key in ORDER_KEYS} for row in schedule()]
        with (directory / "order.tsv").open(newline="") as stream:
            actual = list(DictReader(stream, delimiter="\t"))
        require(actual == expected, "unexpected process order or roles")
        names = {str(path.relative_to(directory)) for path in (directory / "raw").glob("*.txt")}
        require(names == {row["raw"] for row in expected}, "missing or extra samples")
        cpus.update(verify_process(row["case"], directory / row["raw"], False) for row in expected)
    require(len(cpus) == 1, "all process evidence must have the same CPU")
    print("Verified exact raw sample identity, completion, and runner CPU.")


def verify_layout(directory):
    signatures = {}
    for role in ("base", "pr78", "pr85", "base-r", "pr78-r", "pr85-r"):
        path = directory / f"layout-{role}-parser.txt"
        instructions = []
        for line in path.read_text().splitlines():
            fields = line.split()
            if len(fields) >= 4 and fields[0].rsplit("/", 1)[-1].startswith("request_headers.go:") and fields[1].startswith("0x"):
                instructions.append(fields[3])
        require(instructions, f"{role}: linked RequestHead parser missing")
        signatures[role] = {"instructions": len(instructions), "opcodeSHA256": sha256(" ".join(instructions).encode()).hexdigest()}
    for role in ("base", "pr78", "pr85"):
        require(signatures[role]["opcodeSHA256"] != signatures[role + "-r"]["opcodeSHA256"],
                f"{role}: controlled binary parser opcode sequence unchanged")
    write_json(directory / "parser-layout-signatures.json", signatures)


if __name__ == "__main__":
    command, *args = sys.argv[1:]
    if command == "plan":
        plan(Path(args[0]))
    elif command == "sources":
        source_check(Path(args[0]), args[1], args[2] == "after")
    elif command in ("inventory", "samples"):
        verify(Path(args[0]), command == "samples")
    elif command == "layout":
        verify_layout(Path(args[0]))
    else:
        raise SystemExit("unknown attribution command")
