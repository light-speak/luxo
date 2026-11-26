"""Bind PR 83's historical evidence and drive the shared native sampler."""
from collections import Counter
import hashlib
import itertools
import json
from pathlib import Path
import re
import sys
import zipfile

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "diagnose-pr78-shutdown"))
import verify as native


API, H2, RPC = "pkg/lux/api", "pkg/lux/h2transport", "pkg/lux/rpc"
NON_IDENTITY = "api-non-identity"
LANE = "9v45"
OLD_BASE = "65f1892e175487503aa36caa86d1eb8a0805f073"
OLD_HEAD = "f841a4770b83b368730db8c559df4a4da226b736"
OLD_RUN = {"id": 45219367507, "run_attempt": 1, "event": "pull_request", "head_sha": OLD_HEAD,
           "pull_requests": [{"number": 83, "base": OLD_BASE, "head": OLD_HEAD}]}
DIAG_RUN = {"id": 45242193591, "run_attempt": 1, "event": "push",
            "head_sha": "3b2bdc077d09caf5eee62b8fa7c1176d1ecb8138", "pull_requests": []}
DIAG_ARTIFACT = 14856952585
DIAG_DIGEST = "ed53160d2040d34c5890df4784b5b59f52bb869bee169cf5444ba7efcdd941e1"

CASES = (
    (API, NON_IDENTITY, "^BenchmarkRequestLogDelivered$", ("BenchmarkRequestLogDelivered-2",)),
    (API, NON_IDENTITY, "^BenchmarkRequestLogBoundary$/^full$", ("BenchmarkRequestLogBoundary/full-2",)),
    (API, NON_IDENTITY, "^BenchmarkBinaryJSONRawRequest$/^small$", ("BenchmarkBinaryJSONRawRequest/small-2",)),
    (API, "default", "^BenchmarkIdentityConditionValidity$/^(member|missing)$/^OwnReference$",
     ("BenchmarkIdentityConditionValidity/member/OwnReference-2", "BenchmarkIdentityConditionValidity/missing/OwnReference-2")),
    (API, "default", "^BenchmarkIdentityConditionValidity$/^wrong-kind$/^EnumChecked$",
     ("BenchmarkIdentityConditionValidity/wrong-kind/EnumChecked-2",)),
    (H2, "default", "^BenchmarkValidatedHTTPBridge$", (
         "BenchmarkValidatedHTTPBridge//-2", "BenchmarkValidatedHTTPBridge///a%20b?q=1-2", "BenchmarkValidatedHTTPBridge//?-2")),
    (H2, "default", "^BenchmarkTaskDeadlineReplacement$/^(1|8|128)$/^conditional$",
     tuple(f"BenchmarkTaskDeadlineReplacement/{count}/conditional-2" for count in (1, 8, 128))),
    (RPC, "default", "^BenchmarkRPCPoolClosedBorrow$", ("BenchmarkRPCPoolClosedBorrow-2",)),
    (RPC, "default", "^BenchmarkRPCDeadlineClassification$/^NoDeadline$/^(Before|After)$",
     ("BenchmarkRPCDeadlineClassification/NoDeadline/Before-2", "BenchmarkRPCDeadlineClassification/NoDeadline/After-2")),
    (RPC, "default", "^BenchmarkRPCNilContextGuard$/^NoToken$/^After$", ("BenchmarkRPCNilContextGuard/NoToken/After-2",)),
)
SHARD_CASES = {"01": (5, 7), "06": (2, 3, 4, 8), "07": (6, 9)}
ARTIFACTS = {"01": (14848638649, "f4b541030b01f3afae689eee8f12bcaa3b9ffce6193907232bd288c5bfd84c74"),
             "06": (14850682408, "b48d927a800a490e4fe6dd7329a7e36d844c6d838c9986856106b025b501fc6f"),
             "07": (14850118736, "2210b9858c4ffb2c549bcf2bfe095ee71bba6ca7d5e3f39bf327f48307e5a516")}
NOT_REPRODUCED = {"BenchmarkIdentityConditionValidity/member/OwnReference-2",
                  "BenchmarkIdentityConditionValidity/wrong-kind/EnumChecked-2",
                  "BenchmarkValidatedHTTPBridge///a%20b?q=1-2"}


def shard_leaves(indexes):
    result = {}
    for i in indexes:
        package, _, _, names = CASES[i]
        result.setdefault(package, []).extend(names)
    return result


native.CASES = {LANE: CASES}
native.HISTORICAL_RUN = OLD_RUN
native.SHARDS = {LANE: tuple(ARTIFACTS)}
native.FORMAL = {shard: (*proof, shard_leaves(SHARD_CASES[shard])) for shard, proof in ARTIFACTS.items()}
native.SHARD_MODEL = {"07": "7763"}


def check_prior_samples(archive):
    expected = {(package, name) for package, _, _, names in CASES for name in names}
    for comparison, group, side in itertools.product(("aa", "ab"), native.GROUPS, ("left", "right")):
        path = f"{comparison}.{group}.{side}.txt"
        lines = archive.read(path).decode().splitlines()
        native.check_platform(lines, LANE)
        result = Counter()
        for line in lines:
            if line.startswith("pkg:"):
                package = line.split(":", 1)[1].strip().removeprefix("github.com/light-speak/luxo/")
            elif line.startswith("Benchmark"):
                name, _, _ = native.row(line)
                result[(package, name)] += 1
        native.require(result == dict.fromkeys(expected, 10), f"prior diagnostic sample mismatch: {path}")


def check_prior_diagnostic(directory, output):
    run = json.loads((directory / "diag-run.json").read_text())
    metadata = json.loads((directory / "diag.json").read_text())
    native.require(run == DIAG_RUN and metadata["id"] == DIAG_ARTIFACT
                   and metadata["name"] == "hosted-pr83-pr84-pr83-45242193591-1"
                   and metadata["digest"] == "sha256:" + DIAG_DIGEST and not metadata["expired"]
                   and metadata["workflow_run"]["id"] == run["id"]
                   and metadata["workflow_run"]["head_sha"] == run["head_sha"], "prior diagnostic identity mismatch")
    bundle = directory / "diag.zip"
    native.require(hashlib.sha256(bundle.read_bytes()).hexdigest() == DIAG_DIGEST, "prior diagnostic checksum mismatch")
    with zipfile.ZipFile(bundle) as archive:
        native.require(archive.testzip() is None, "prior diagnostic ZIP is damaged")
        revisions = archive.read("revisions.txt").decode().splitlines()
        native.require(revisions == [f"base {OLD_BASE} 7a051ccbdd2ddbf9c341fd7906cd3c5f3e88dc44",
                                     f"head {OLD_HEAD} 827ac6c56bed69eb7e0d80c2394e9d7663e3f62d"],
                       "prior diagnostic source mismatch")
        check_prior_samples(archive)
        native.require(archive.read("aa.benchgate.log").startswith(b"performance gate passed:"), "prior A/A did not pass")
        failures = [line for line in archive.read("ab.benchgate.log").decode().splitlines()
                    if line.startswith("confirmed performance regression: ")]
        actual = {re.search(r"/pkg/lux/(\S+) sec/op", line)[1] for line in failures}
        wanted = {package.removeprefix("pkg/lux/") + "/" + name.removeprefix("Benchmark")
                  for package, _, _, names in CASES[2:] for name in names if name not in NOT_REPRODUCED}
        native.require(len(failures) == len(wanted) == 11 and actual == wanted, "prior diagnostic rejection set mismatch")
    native.write_json(output / "prior-diagnostic.json", {"run": run, "artifact": DIAG_ARTIFACT, "sha256": DIAG_DIGEST,
                                                        "revisions": revisions, "confirmed": failures})


if __name__ == "__main__":
    command, lane, *args = sys.argv[1:]
    native.require(lane == LANE, "only the 9V45 lane is defined")
    if command == "model":
        print(native.MODELS[lane])
    elif command == "artifacts":
        native.binding(lane)
        print("diag", DIAG_ARTIFACT, DIAG_DIGEST, sep="\t")
    elif command == "plan":
        historical, output = map(Path, args)
        check_prior_diagnostic(historical, output)
        native.plan(lane, historical, output)
    elif command == "inventory":
        native.read_case(Path(args[1]), lane, args[0], one_iteration=True)
    elif command == "schedule":
        for leg in native.schedule(lane):
            print(*leg, sep="\t")
    elif command == "samples":
        native.fresh(lane, Path(args[0]))
    else:
        raise SystemExit("unknown verifier command")
