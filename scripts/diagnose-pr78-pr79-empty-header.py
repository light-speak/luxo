"""Plan and verify the interleaved PR 78 and 79 empty Header comparisons."""

from collections import defaultdict
from csv import DictReader
from decimal import Decimal
import importlib.util
import json
from pathlib import Path
import sys


LUVIA = "pkg/lux/luvia"
H2 = "pkg/lux/h2transport"
RPC = "pkg/lux/rpc"
AUTH = "BenchmarkAuthMiddlewareIsolation"
PR78 = [
    (LUVIA, "default", "^BenchmarkGatewayIdentity$/^authenticated$/^before$", ("BenchmarkGatewayIdentity/authenticated/before",)),
    (LUVIA, "default", "^BenchmarkMergeDestination$/^columnar=true$/^rows=1$/^direct=true$",
     ("BenchmarkMergeDestination/columnar=true/rows=1/direct=true",)),
    (LUVIA, "default", f"^{AUTH}$/^anonymous$/^after$", (f"{AUTH}/anonymous/after",)),
    (LUVIA, "default", "^BenchmarkTraceEncodingComparison$/^true$/^reservation-candidate$",
     ("BenchmarkTraceEncodingComparison/true/reservation-candidate",)),
    (RPC, "default", "^BenchmarkRPCPoolCloseSafetyReverse$/^serial$/^after$",
     ("BenchmarkRPCPoolCloseSafetyReverse/serial/after",)),
    (H2, "default", "^BenchmarkTaskDeadlineReplacement$/^(1|8|128)$/^conditional$",
     tuple(f"BenchmarkTaskDeadlineReplacement/{count}/conditional" for count in (1, 8, 128))),
    (H2, "shutdown", "^BenchmarkShutdownOneOpeningConnection$", ("BenchmarkShutdownOneOpeningConnection",)),
]
GUARD = [
    (LUVIA, "guard", f"^{AUTH}$/^(valid|anonymous|invalid|inherited-missing|inherited-other-key)$/^(before|after)$",
     tuple(f"{AUTH}/{kind}/{side}" for kind in ("valid", "anonymous", "invalid", "inherited-missing",
                                                 "inherited-other-key") for side in ("before", "after"))),
    (LUVIA, "guard", "^BenchmarkDiagnosticGatewayAuthHeaders$",
     tuple(f"BenchmarkDiagnosticGatewayAuthHeaders/{sample}" for sample in
           ("ordinary-nonempty", "websocket-query", "repeated-authorization"))),
]


def load_plan(directory, suite):
    spec = importlib.util.spec_from_file_location("qualified_schedule", directory / "schedule-helper.py")
    helper = importlib.util.module_from_spec(spec)
    sys.dont_write_bytecode = True
    spec.loader.exec_module(helper)
    scope = json.loads((directory / "original-pr79-scope.json").read_text())["pr79"]
    if suite == "pr78":
        release = PR78
    else:
        release = [(case["package"], case["variant"], case["filter"],
                    [leaf.removesuffix("-2") for leaf in case["leaves"]]) for case in scope["cases"]]
    helper.CASES = {f"{i:02}": case for i, case in enumerate(release + GUARD, 1)}
    release_cases = tuple(helper.CASES)[:len(release)]
    guard_cases = tuple(helper.CASES)[len(release):]
    helper.COMPARISONS = [
        ("release-aa", "base", "base", release_cases),
        ("release-ah", "base", "head", release_cases),
        ("release-ap", "base", "patched", release_cases),
        ("guard-hh", "guard-head", "guard-head", guard_cases),
        ("guard-hp", "guard-head", "patched", guard_cases),
    ]
    return helper


def plan(directory, suite, helper):
    rows = list(helper.schedule())
    expected_arms, expected_leaves, expected_inventory = (1000, 2120, 25) if suite == "pr78" else (1120, 2480, 28)
    helper.require(len(rows) == len({row["raw"] for row in rows}) == expected_arms, "wrong process schedule")
    helper.require(sum(len(helper.CASES[row["case"]][3]) for row in rows) == expected_leaves, "wrong leaf total")
    helper.require(len(helper.inventory()) == expected_inventory, "wrong inventory size")
    helper.write_tsv(directory / "schedule.tsv", ([row[key] for key in helper.SCHEDULE_KEYS] for row in rows))
    helper.write_tsv(directory / "inventory.tsv",
                     ([role, case, *helper.CASES[case][:3]] for role, case in helper.inventory()))
    helper.write_tsv(directory / "comparisons.tsv", ([name, left, right] for name, left, right, _ in helper.COMPARISONS))
    for name, _, _, cases in helper.COMPARISONS:
        keys = [{"Package": helper.CASES[case][0], "Name": leaf + "-2"}
                for case in cases for leaf in helper.CASES[case][3]]
        helper.write_json(directory / f"{name}.expected.json", {"Base": keys, "Head": keys})
    print(f"{suite}: {expected_inventory} inventory processes, {expected_arms} sample arms, {expected_leaves} leaf observations")


def verify_process(path, case, target, one_iteration):
    package, _, _, expected = case
    lines = path.read_text().splitlines()
    header = [f"goos: {target[0]}", f"goarch: {target[1]}", f"pkg: github.com/light-speak/luxo/{package}"]
    if lines[:3] != header or len(lines) != len(expected) + 5 or not lines[3].startswith("cpu: ") or lines[-1] != "PASS":
        raise SystemExit(f"{path}: wrong benchmark platform, package or completion")
    measurements = {}
    for line in lines[4:-1]:
        name, count, ns, ns_unit, size, size_unit, alloc, alloc_unit = line.split()
        numbers = tuple(Decimal(value) for value in (ns, size, alloc))
        if ((ns_unit, size_unit, alloc_unit) != ("ns/op", "B/op", "allocs/op")
                or int(count) < 1 or one_iteration and int(count) != 1 or name in measurements
                or any(not val.is_finite() or val < 0 for val in numbers)):
            raise SystemExit(f"{path}: invalid or duplicate sample")
        measurements[name] = numbers
    if set(measurements) != {leaf + "-2" for leaf in expected}:
        raise SystemExit(f"{path}: actual samples do not match plan")
    return lines[3].strip(), measurements


def verify(directory, helper, target, measured):
    def sample(path, case, one_iteration=False):
        cpu, values = verify_process(directory / path, helper.CASES[case], target, one_iteration)
        cpus.add(cpu)
        return values

    cpus = set()
    for role, case in helper.inventory():
        sample(f"inventory-{role}-{case}.txt", case, True)
    if measured:
        expected = [{key: row[key] for key in helper.ORDER_KEYS} for row in helper.schedule()]
        with (directory / "order.tsv").open(newline="") as stream:
            actual = list(DictReader(stream, delimiter="\t"))
        helper.require(actual == expected, "unexpected process order or role")
        names = {str(path.relative_to(directory)) for path in (directory / "raw").glob("*.txt")}
        helper.require(names == {row["raw"] for row in expected}, "missing or extra raw sample")
        values = defaultdict(dict)
        for row in expected:
            key = tuple(row[field] for field in ("group", "pair", "comparison", "case"))
            values[key][row["side"]] = sample(row["raw"], row["case"])
        with (directory / "signed-pairs.tsv").open("w") as stream:
            stream.write("group\tpair\tcomparison\tcase\tleaf\tunit\tbase\thead\tdifference\n")
            for key, sides in values.items():
                for name, left in sides["left"].items():
                    for unit, base, head in zip(("ns/op", "B/op", "allocs/op"), left, sides["right"][name]):
                        stream.write("\t".join((*key, name, unit, str(base), str(head), str(head - base))) + "\n")
    helper.require(len(cpus) == 1, "more than one runner CPU in comparison")
    print("Exact leaf names, Go benchmark results, roles and single CPU verified.")


if __name__ == "__main__":
    command, directory, suite, *target = sys.argv[1:]
    directory = Path(directory)
    helper = load_plan(directory, suite)
    if command == "plan":
        plan(directory, suite, helper)
    elif command in ("inventory", "samples"):
        verify(directory, helper, target, command == "samples")
    else:
        raise SystemExit("invalid validation mode")
