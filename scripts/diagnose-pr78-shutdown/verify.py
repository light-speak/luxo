"""Validate the original failed samples and the native rerun."""
import csv
import hashlib
import itertools
import json
import math
from pathlib import Path
import sys
import zipfile


H2, LUVIA, RPC = "pkg/lux/h2transport", "pkg/lux/luvia", "pkg/lux/rpc"
MODELS = {
    "7763": "AMD EPYC 7763 64-Core Processor",
    "9v45": "AMD EPYC 9V45 96-Core Processor",
    "6975": "Intel(R) Xeon(R) 6975P-C",
}
FORMAL = {
    "02": (14854768278, "44bfeaa1f4fc3ffb48ab4b66673f4d7212cc88abee9103eefacab71dc772f749", {
        LUVIA: ("BenchmarkGatewayIdentity/authenticated/before-2",),
    }),
    "04": (14856188994, "6376f29699529f14cb16e3a969da7cf6b12823484be4e5f303f621594fcb4008", {
        LUVIA: ("BenchmarkMergeDestination/columnar=true/rows=1/direct=true-2",),
    }),
    "06": (14857110208, "69aacd3d68cf85326e6e7558d5736f19020b9308cd8a5011dd3fa39ed28c5787", {
        LUVIA: ("BenchmarkAuthMiddlewareIsolation/anonymous/after-2",),
        RPC: ("BenchmarkRPCPoolCloseSafetyReverse/serial/after-2",),
    }),
    "07": (14854812828, "8496713efd6a2e5d233698763b35303e7aba1aac122aee56c5da3c7222c792c8", {
        LUVIA: ("BenchmarkTraceEncodingComparison/true/reservation-candidate-2",),
    }),
}
SHARDS = {"7763": ("06", "07"), "9v45": ("02",), "6975": ("04",)}
GROUPS = ("primary", "confirmation")
ROLES = {"aa": ("base", "base"), "bb": ("head", "head"), "ab": ("base", "head")}
METRICS = ("ns/op", "B/op", "allocs/op")
CASES = {
    "7763": (
        (H2, "default",
         r"^(BenchmarkShutdownOneOpeningConnection|BenchmarkShutdownNoConnections|BenchmarkPriorKnowledgeConnectionLifecycle|BenchmarkWakeControlBatch)$",
         ("BenchmarkShutdownOneOpeningConnection-2", "BenchmarkShutdownNoConnections/fresh-2",
          "BenchmarkShutdownNoConnections/repeated-2", "BenchmarkPriorKnowledgeConnectionLifecycle-2",
          *(f"BenchmarkWakeControlBatch/{size}/combined={mode}-2"
            for size in (1, 8) for mode in ("false", "true")))),
        (LUVIA, "default", "^BenchmarkAuthMiddlewareIsolation$/^anonymous$",
         tuple(f"BenchmarkAuthMiddlewareIsolation/anonymous/{side}-2" for side in ("before", "after"))),
        (RPC, "default", "^BenchmarkRPCPoolCloseSafetyReverse$",
         tuple(f"BenchmarkRPCPoolCloseSafetyReverse/{mode}/{side}-2"
               for mode, side in itertools.product(("serial", "parallel"), ("before", "after")))),
        (LUVIA, "luvia-primary-trace", "^BenchmarkTraceEncodingComparison$/^true$",
         tuple(f"BenchmarkTraceEncodingComparison/true/{side}-2"
               for side in ("previous", "static-reference", "reservation-candidate"))),
    ),
    "9v45": (
        (LUVIA, "luvia-gateway-identity", "^BenchmarkGatewayIdentity$",
         tuple(f"BenchmarkGatewayIdentity/{mode}/{side}-2"
               for mode, side in itertools.product(("anonymous", "authenticated", "typed-nil"), ("before", "after")))),
    ),
    "6975": (
        (LUVIA, "luvia-merge-destination", "^BenchmarkMergeDestination$",
         tuple(f"BenchmarkMergeDestination/columnar={columnar}/rows={rows}/direct={direct}-2"
               for (columnar, rows), direct in itertools.product(
                   (("false", 1), ("true", 1), ("true", 128)), ("false", "true")))),
    ),
}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def binding(lane):
    for shard in SHARDS[lane]:
        identifier, digest, _ = FORMAL[shard]
        print(shard, identifier, digest, sep="\t")


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def check_platform(lines, lane):
    for name, expected in (("goos", "linux"), ("goarch", "amd64"), ("cpu", MODELS[lane])):
        actual = {line.split(":", 1)[1].strip() for line in lines if line.startswith(name + ":")}
        require(actual == {expected}, f"bad {name}: {actual}")


def row(line):
    name, count, *columns = line.split()
    values = {unit: float(value) for value, unit in zip(columns[::2], columns[1::2])}
    require(len(columns) == 2 * len(values) and all(unit in values for unit in METRICS)
            and all(math.isfinite(value) and value >= 0 for value in values.values())
            and int(count) > 0 and values["ns/op"] > 0, f"invalid metrics: {line}")
    require(all(values[unit].is_integer() for unit in ("B/op", "allocs/op")),
            f"nonintegral allocation metric: {line}")
    if name.startswith("BenchmarkWakeControlBatch/"):
        writes = 2 if name.endswith("/8/combined=false-2") else 1
        require(values.get("writes/op") == writes, f"wrong control write count: {line}")
    return name, int(count), tuple(values[key] for key in METRICS)


def historical(lane, directory, output):
    run = json.loads((directory / "run.json").read_text())
    require(run == {"event": "pull_request", "head_sha": "397af9c8c6fc6dc807e1dbcbe2e05d8913f58944",
                    "id": 45227864247, "run_attempt": 1, "pull_requests": [
                        {"number": 78, "base": "65f1892e175487503aa36caa86d1eb8a0805f073",
                         "head": "397af9c8c6fc6dc807e1dbcbe2e05d8913f58944"}]},
            "historical pull request run binding")
    write_json(output / "historical-run.json", run)
    results = {}
    for shard in SHARDS[lane]:
        identifier, digest, checks = FORMAL[shard]
        meta = json.loads((directory / f"{shard}.json").read_text())
        require(meta["id"] == identifier and meta["name"] == f"performance-evidence-{shard}-1"
                and not meta["expired"] and meta["digest"] == "sha256:" + digest
                and meta["workflow_run"]["id"] == 45227864247
                and meta["workflow_run"]["head_sha"] == "397af9c8c6fc6dc807e1dbcbe2e05d8913f58944",
                f"artifact metadata mismatch: {shard}")
        archive = directory / f"{shard}.zip"
        require(hashlib.sha256(archive.read_bytes()).hexdigest() == digest, f"archive binding: {shard}")
        records = {}
        with zipfile.ZipFile(archive) as bundle:
            require(bundle.testzip() is None, f"invalid ZIP: {shard}")
            for group, side in itertools.product(GROUPS, ("base", "head")):
                lines = bundle.read(f"benchmark-raw-{group}-{side}.txt").decode().splitlines()
                check_platform(lines, lane)
                observed = {(package, name): [] for package, names in checks.items() for name in names}
                package = ""
                for line in lines:
                    if line.startswith("pkg:"):
                        package = line.split(":", 1)[1].strip().removeprefix("github.com/light-speak/luxo/")
                    if line.startswith("Benchmark") and (package, line.split()[0]) in observed:
                        name, _, metrics = row(line)
                        observed[(package, name)].append(metrics)
                require(all(len(values) == 10 for values in observed.values()), f"missing legacy leaf: {shard}")
                records[f"{group}/{side}"] = {f"{package}/{name}": data for (package, name), data in observed.items()}
        results[shard] = {"id": identifier, "sha256": digest, "cpu": MODELS[lane], "samples": records}
    write_json(output / "historical-samples.json", results)


def number_cases(lane):
    return {f"{i:02}": entry for i, entry in enumerate(CASES[lane], 1)}


def plan(lane, directory, output):
    historical(lane, directory, output)
    expected = []
    with (output / "cases.tsv").open("w", encoding="utf-8") as file:
        for case_id, (package, profile, pattern, names) in number_cases(lane).items():
            print(case_id, package, profile, pattern, sep="\t", file=file)
            expected.extend({"Name": name, "Package": package} for name in names)
    write_json(output / "expected.json", {"Base": expected, "Head": expected})


def read_case(path, lane, case_id, *, one_iteration=False):
    package, _, _, names = number_cases(lane)[case_id]
    lines = path.read_text().splitlines()
    check_platform(lines, lane)
    require(lines.count("PASS") == 1, f"expected successful process: {path}")
    metadata = {line.split(":", 1)[1].strip() for line in lines if line.startswith("pkg:")}
    require(metadata == {"github.com/light-speak/luxo/" + package}, f"unexpected package: {path}")
    samples = [row(line) for line in lines if line.startswith("Benchmark")]
    require(len(samples) == len(names) and {name for name, _, _ in samples} == set(names), f"wrong leaves: {path}")
    if one_iteration:
        require(all(count == 1 for _, count, _ in samples), f"inventory must be 1x: {path}")
    return {name: values for name, _, values in samples}


def schedule(lane):
    case_indexes = tuple(number_cases(lane))
    for offset, group in enumerate(GROUPS):
        for i in range(1, 11):
            for case_index in range(len(case_indexes)):
                case_id = case_indexes[(case_index + i + offset) % len(case_indexes)]
                _, profile, _, _ = number_cases(lane)[case_id]
                for compare_index in range(len(ROLES)):
                    comparison = tuple(ROLES)[(compare_index + i + offset) % len(ROLES)]
                    sides = ("left", "right") if (i + offset) % 2 else ("right", "left")
                    for side in sides:
                        role = ROLES[comparison][side == "right"]
                        yield group, f"{i:02}", comparison, case_id, side, role, profile, f"raw/{group}.{i:02}.{comparison}.{case_id}.{side}.txt"


def fresh(lane, output):
    with (output / "order.tsv").open(newline="") as stream:
        header, *rows = csv.reader(stream, delimiter="\t")
    require(header == "group pair comparison case side revision variant raw".split()
            and [tuple(r) for r in rows] == list(schedule(lane)), "unexpected pair sequence")
    require(len(list((output / "raw").glob("*.txt"))) == len(rows), "extra or missing raw outputs")
    samples = {}
    for group, i, comp, case, side, _, _, path in rows:
        samples[(group, i, comp, case, side)] = read_case(output / path, lane, case)
    paired = {}
    for group, comp in itertools.product(GROUPS, ROLES):
        series = {}
        for case, (package, _, _, names) in number_cases(lane).items():
            for name in names:
                deltas = [
                    [round(right - left, 6) for left, right in zip(
                        samples[(group, f"{i:02}", comp, case, "left")][name],
                        samples[(group, f"{i:02}", comp, case, "right")][name])]
                    for i in range(1, 11)]
                series[f"{package}/{name}"] = {
                    unit: {"signedPairs": list(values), "positiveMax": max(0, max(values))}
                    for unit, values in zip(METRICS, zip(*deltas))}
        paired[f"{group}/{comp}"] = series
    write_json(output / "paired-deltas.json", {"cpu": MODELS[lane], "processes": len(rows), "results": paired})


if __name__ == "__main__":
    command, lane, *args = sys.argv[1:]
    if command == "model":
        print(MODELS[lane])
    elif command == "artifacts":
        binding(lane)
    elif command == "plan":
        plan(lane, *map(Path, args))
    elif command == "inventory":
        read_case(Path(args[1]), lane, args[0], one_iteration=True)
    elif command == "schedule":
        for leg in schedule(lane):
            print(*leg, sep="\t")
    elif command == "samples":
        fresh(lane, Path(args[0]))
    else:
        raise SystemExit("unknown verifier command")
