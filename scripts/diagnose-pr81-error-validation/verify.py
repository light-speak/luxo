"""Bind the targeted native work to PR 81's original strict run and plan."""
import hashlib
import json
import re
import sys
from pathlib import Path
from zipfile import ZipFile

RUN = 45785939660
BASE = "0a6b143ad667c84f0e517b9ef0a9e9d5bd6ec367"
HEAD = "1f5ce302d3bcf1af0b21e55cb3070c424e96cbc4"
PACKAGE = "pkg/lux/rpc"
PREFIX = "github.com/light-speak/luxo/"
MODELS = {"6975": "Intel(R) Xeon(R) 6975P-C", "7763": "AMD EPYC 7763 64-Core Processor"}
ARTIFACTS = {
    "plan": (15099803870, "benchmark-plan-1",
             "bcf3bd309ec476ca28545c07456dc3e69b7e97b4c68f5031e512dfe51aac4f7d"),
    "04": (15102305351, "performance-evidence-04-1",
           "74787dbfb5e147b728e5727f098b6c140ed071a57546dcd407f2e21792ed51a6"),
    "05": (15101641323, "performance-evidence-05-1",
           "fcdbff11f6bbee2f6027e22a5e1097cb546d0b88cf13be737851dc193cb20f21"),
    "08": (15101312224, "performance-evidence-08-1",
           "09fa9c8fd9079fbb43882b8b602a4c7e2df03f53e9a350962f5a256fd8ea4e12"),
}
CASES = {
    "6975": (("04", "RPCErrorDataMigration", ((), ("decode_without_data_after", "decode_without_data_before"))),),
    "7763": (
        ("05", "RPCMalformedErrorDecode", (("duplicate", "trailing"), ("after", "strict_before"))),
        ("08", "RPCErrorDecode", (("basic", "unicode"), ("after", "before"))),
    ),
}


def selections(shard, root, axes):
    prefix, arms = axes
    selector = "/".join([f"^Benchmark{root}$"] + [
        "^(" + "|".join(axis) + ")$" for axis in axes if axis
    ])
    leaves = {
        "/".join([f"Benchmark{root}", sample, arm] if sample else [f"Benchmark{root}", arm]) + "-2"
        for sample in (prefix or ("",)) for arm in arms
    }
    confirmed = {leaf for leaf in leaves if leaf.endswith("/after-2") or leaf.endswith("_after-2")}
    return shard, selector, leaves, confirmed


def bind_artifact(directory, key):
    artifact, name, digest = ARTIFACTS[key]
    item = json.loads((directory / f"{key}.json").read_text())
    assert item["id"] == artifact and item["name"] == name and item["expired"] is False
    assert item["digest"] == f"sha256:{digest}" and item["workflow_run"]["id"] == RUN
    archive = directory / f"{key}.zip"
    assert hashlib.sha256(archive.read_bytes()).hexdigest() == digest
    return ZipFile(archive)


def read(bundle, file):
    return bundle.read(file).decode()


def prepare(lane, inputs, output):
    run = json.loads((inputs / "run.json").read_text())
    assert run == {"id": RUN, "run_attempt": 1, "event": "workflow_dispatch", "head_sha": HEAD}
    binding = f"base_sha={BASE}\ntested_sha={HEAD}\n"
    with bind_artifact(inputs, "plan") as plan:
        assert read(plan, "benchmark-revisions.txt") == binding
        profile = read(plan, "benchmark-compile-profile.txt")
        (output / "original-profile.txt").write_text(profile)
        all_leaves = []
        direct_leaves = []
        case_rows = []
        for ordinal, case in enumerate(CASES[lane], 1):
            shard, selector, leaves, confirmed = selections(*case)
            with bind_artifact(inputs, shard) as original:
                assert read(original, "benchmark-revisions.txt") == binding
                assert read(original, "benchmark-compile-profile.txt") == profile
                original_batch = read(original, "benchmark-batch.tsv")
                assert original_batch == read(plan, f"benchmark-plan/shard-{shard}.tsv")
                assert any(row.startswith(f"{PACKAGE}\tdefault\t") and f"Benchmark{case[1]}" in row
                           for row in original_batch.splitlines())
                original_plan = json.loads(read(plan, f"benchmark-plan/shard-{shard}.json"))
                assert original_plan == json.loads(read(original, "benchmark-expected.json"))
                for arm in ("Base", "Head"):
                    available = {x["Name"] for x in original_plan[arm] if x["Package"] == PACKAGE}
                    assert leaves <= available
                env = read(original, "benchmark-environment.txt")
                models = re.findall(r"^Model name:\s*(.*?)\s*$", env, flags=re.M)
                assert models == [MODELS[lane]]
                assert json.loads(env[env.index("{\n"):]) == {
                    "CGO_ENABLED": "1", "GOAMD64": "v1", "GOARCH": "amd64",
                    "GOOS": "linux", "GOVERSION": "go1.27.1",
                }
                gate = read(original, "benchmark-gate.txt")
                assert all(f"confirmed performance regression: {PREFIX}{PACKAGE}/{leaf.removeprefix('Benchmark')} sec/op " in gate
                           for leaf in confirmed)
                (output / f"original-{shard}.gate.txt").write_text(gate)
                (output / f"original-{shard}.expected.json").write_text(read(original, "benchmark-expected.json"))
                for direction in ("control", "control-reversed"):
                    assert read(original, f"benchmark-{direction}-gate.txt").startswith("performance gate passed:")
            all_leaves.extend(sorted(leaves))
            direct_leaves.extend(f"{PREFIX}{PACKAGE}/{leaf.removeprefix('Benchmark')}" for leaf in confirmed)
            case_rows.append(f"{ordinal:02d}\t{PACKAGE}\tdefault\t{selector}\n")
        items = [{"Package": PACKAGE, "Name": leaf} for leaf in sorted(all_leaves)]
        (output / "expected.json").write_text(json.dumps({"Base": items, "Head": items}, indent=2) + "\n")
        (output / "cases.tsv").write_text("".join(case_rows))

        (output / "original-direct.txt").write_text("".join(f"{leaf}\n" for leaf in sorted(direct_leaves)))


def inventory(lane, expected_path, measured_path):
    plan = json.loads(expected_path.read_text())
    measured = []
    output = measured_path.read_text().splitlines()
    assert all(line.split(":", 1)[1].strip() == MODELS[lane] for line in output if line.startswith("cpu:"))
    assert any(line.startswith("cpu:") for line in output)
    assert all(line == f"pkg: {PREFIX}{PACKAGE}" for line in output if line.startswith("pkg:"))
    assert any(line.startswith("pkg:") for line in output)
    for line in output:
        if line.startswith("Benchmark"):
            fields = line.split()
            assert fields[1] == "1" and {"ns/op", "B/op", "allocs/op"} <= set(fields)
            measured.append(fields[0])
    assert sorted(measured) == sorted(item["Name"] for item in plan["Head"])
    assert output.count("PASS") == len(CASES[lane])


if __name__ == "__main__":
    command, lane, *args = sys.argv[1:]
    if command == "model":
        print(MODELS[lane])
    elif command == "artifacts":
        for key in ["plan"] + [case[0] for case in CASES[lane]]:
            aid, _, digest = ARTIFACTS[key]
            print(f"{key}\t{aid}\t{digest}")
    elif command == "plan":
        prepare(lane, *map(Path, args))
    elif command == "inventory":
        inventory(lane, *map(Path, args))
    else:
        raise SystemExit(f"unknown command: {command}")
