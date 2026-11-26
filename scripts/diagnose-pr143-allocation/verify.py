"""Verify the exact original PR 143 allocation failures using the shared sampler."""
import json
from pathlib import Path
import re
import sys
import zipfile

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "diagnose-pr78-shutdown"))
import verify as native


LANE = "6975"
PACKAGE = "pkg/codegen"
BASE = "f9ce659fd7f17bf57cfe0aee7de7ecb8d9b8f2e4"
HEAD = "1b6c73bd67a781beb9e8b783cc7f8f631bf1276e"
SOURCE_RUN = {"event": "workflow_dispatch", "head_sha": HEAD, "id": 45752445232, "run_attempt": 1,
              "pull_requests": [{"number": 143, "base": BASE, "head": HEAD}]}
CHECKED = "BenchmarkCheckedEntryFormattingComparison"
RUNTIME = "BenchmarkAppRuntimeEntry"
CASES = (
    (PACKAGE, "default", f"^{CHECKED}$", tuple(f"{CHECKED}/{mode}/{revision}-2"
     for mode in ("embedded", "gateway", "module") for revision in ("current", "previous"))),
    (PACKAGE, "default", f"^{RUNTIME}$", tuple(f"{RUNTIME}/{mode}-2" for mode in ("events", "functions", "model", "native"))),
)
ARTIFACTS = {
    "02": (15087076519, "2a48174b98e4f85b4db2e4b64e7a12cacca00cf80a75bbc889d6afe9f60a5039", 0),
    "06": (15086648183, "474d52c9dc8be78e33c12f19e2bf33fe819d0c606f471853406cc0cefb8e66d5", 1),
}

native.CASES = {LANE: CASES}
native.HISTORICAL_RUN = SOURCE_RUN
native.SHARDS = {LANE: tuple(ARTIFACTS)}
native.FORMAL = {shard: (identity, sha256, {PACKAGE: CASES[index][3]})
                 for shard, (identity, sha256, index) in ARTIFACTS.items()}


def verify_original_scope(historical, output):
    compile_profiles = set()
    for shard, (_, _, index) in ARTIFACTS.items():
        with zipfile.ZipFile(historical / f"{shard}.zip") as original:
            revisions = original.read("benchmark-revisions.txt").decode().splitlines()
            native.require(revisions == [f"base_sha={BASE}", f"tested_sha={HEAD}"], f"wrong original revisions: {shard}")
            compile_profiles.add(original.read("benchmark-compile-profile.txt"))
            case_names = set(CASES[index][3])
            rows = original.read("benchmark-confirmation-batch.tsv").decode().splitlines()
            package, profile, _ = rows[0].split("\t")
            native.require(len(rows) == 1 and package == PACKAGE and profile == "default", f"wrong shard profile: {shard}")
            expected = {(name.removeprefix("Benchmark"), unit) for name in case_names for unit in ("B/op", "allocs/op")}
            lines = original.read("benchmark-gate.txt").decode().splitlines()
            raw = [re.match(r"confirmed performance regression: github\.com/light-speak/luxo/pkg/codegen/(\S+) (\S+) ", line)
                   for line in lines if line.startswith("confirmed performance regression: ")]
            native.require(all(raw) and len(raw) == len(expected) and {match.groups() for match in raw} == expected,
                           f"confirmed allocation scope differs: {shard}")
            for control in ("benchmark-control-gate.txt", "benchmark-control-reversed-gate.txt"):
                native.require(original.read(control).startswith(b"performance gate passed:"), f"old control failed: {shard}/{control}")
            (output / f"original-{shard}-gate.txt").write_text("\n".join(lines) + "\n")
    native.require(len(compile_profiles) == 1, "official compile profiles disagree")
    (output / "original-compile-profile.txt").write_bytes(compile_profiles.pop())


if __name__ == "__main__":
    command, lane, *args = sys.argv[1:]
    native.require(lane == LANE, "only the original Intel 6975P-C lane is defined")
    if command == "model":
        print(native.MODELS[lane])
    elif command == "artifacts":
        native.binding(lane)
    elif command == "plan":
        historical, output = map(Path, args)
        native.plan(lane, historical, output)
        verify_original_scope(historical, output)
    elif command == "inventory":
        native.read_case(Path(args[1]), lane, args[0], one_iteration=True)
    elif command == "schedule":
        for leg in native.schedule(lane):
            print(*leg, sep="\t")
    elif command == "samples":
        native.fresh(lane, Path(args[0]))
    else:
        raise SystemExit("unknown verifier command")
