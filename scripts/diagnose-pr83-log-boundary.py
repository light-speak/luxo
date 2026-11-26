"""Verify the original PR 83 source and sample contract without compiling Go."""

import argparse
from collections import Counter
from decimal import Decimal
from hashlib import sha256
import json
from pathlib import Path
import re
from zipfile import ZipFile

BASE = "dd70430a0d3f32ba400ed25922d431d206c48ade"
HEAD = "cb0e8dca57971c1da049fc6033e62f91e4f57cac"
CPU = "Intel(R) Xeon(R) 6975P-C"
PACKAGE = "pkg/lux/api"
PROFILE = "api-non-identity"
BOUNDARY = "BenchmarkRequestLogBoundary"
DELIVERED = "BenchmarkRequestLogDelivered"
FILTER = f"^({BOUNDARY}|{DELIVERED})$"
RUN = 45821350412
ARTIFACT = 15120416499
DIGEST = "5693aaa209423ac7917eb3a3fcfd0cbc540d58bf7f976cfd1411050d0fda083b"
BOUNDARY_NAMES = {f"{BOUNDARY}/{mode}-2" for mode in ("disabled", "unsampled", "synchronous", "full")}
NAMES = BOUNDARY_NAMES | {f"{DELIVERED}-2"}
METRICS = {"ns/op", "B/op", "allocs/op"}


def family(data):
    return {item["Name"] for item in data if item["Package"] == PACKAGE and item["Name"].startswith(BOUNDARY + "/")}


def plan(zip_path, meta_path, run_path, out):
    meta = json.loads(meta_path.read_text())
    run = json.loads(run_path.read_text())
    assert (run["id"], run["run_attempt"], run["event"], run["head_sha"], run["conclusion"]) == (
        RUN, 1, "workflow_dispatch", HEAD, "failure")
    assert (meta["id"], meta["name"], meta["digest"], meta["workflow_run"]["id"], meta["workflow_run"]["head_sha"]) == (
        ARTIFACT, "performance-evidence-07-1", "sha256:" + DIGEST, RUN, HEAD)
    raw = zip_path.read_bytes()
    assert len(raw) == meta["size_in_bytes"] and sha256(raw).hexdigest() == DIGEST
    with ZipFile(zip_path) as archive:
        assert archive.testzip() is None
        assert archive.read("benchmark-revisions.txt").decode().splitlines() == [
            f"base_sha={BASE}", f"tested_sha={HEAD}"]
        names = re.findall(r"^Model name:\s*(.+)$",
                           archive.read("benchmark-environment.txt").decode(), re.M)
        assert [name.strip() for name in names] == [CPU]
        expected = json.loads(archive.read("benchmark-expected.json"))
        confirmed = json.loads(archive.read("benchmark-confirmation-expected.json"))
        disabled = {f"{BOUNDARY}/disabled-2"}
        for arm in ("Base", "Head"):
            assert family(expected[arm]) == BOUNDARY_NAMES
            assert family(confirmed[arm]) == disabled
        batch = archive.read("benchmark-batch.tsv").decode().splitlines()
        assert sum(row.startswith(f"{PACKAGE}\t{PROFILE}\t") and BOUNDARY in row for row in batch) == 1
        gate = archive.read("benchmark-gate.txt").decode()
        assert re.findall(r"^confirmed performance regression: .*?/RequestLogBoundary/(\S+) sec/op ", gate, re.M) == [
            "disabled-2"]
        out.mkdir(exist_ok=True, parents=True)
        for name in ("benchmark-gate.txt", "benchmark-revisions.txt", "benchmark-compile-profile.txt",
                     "benchmark-batch.tsv", "benchmark-confirmation-batch.tsv", "benchmark-control-gate.txt",
                     "benchmark-control-reversed-gate.txt"):
            (out / ("original-" + name.removeprefix("benchmark-"))).write_bytes(archive.read(name))
    selected = [{"Package": PACKAGE, "Name": name} for name in sorted(NAMES)]
    (out / "expected.json").write_text(json.dumps({"Base": selected, "Head": selected}, indent=2) + "\n")
    (out / "batch.tsv").write_text(f"{PACKAGE}\t{PROFILE}\t{FILTER}\n")


def check_process(lines, one_iteration):
    assert [line.removeprefix("goarch: ") for line in lines if line.startswith("goarch: ")] == ["amd64"]
    assert [line.removeprefix("goos: ") for line in lines if line.startswith("goos: ")] == ["linux"]
    assert [line.removeprefix("pkg: ") for line in lines if line.startswith("pkg: ")] == ["github.com/light-speak/luxo/" + PACKAGE]
    assert [line.removeprefix("cpu: ").strip() for line in lines if line.startswith("cpu: ")] == [CPU]
    rows = [line.split() for line in lines if line.startswith("Benchmark")]
    assert Counter(row[0] for row in rows) == {name: 1 for name in NAMES}
    for _, iterations, *fields in rows:
        assert (int(iterations) == 1) if one_iteration else (int(iterations) > 1)
        assert len(fields) == 6 and set(fields[1::2]) == METRICS
        for value in map(Decimal, fields[0::2]):
            assert value.is_finite() and value >= 0


def verify_samples(path, count):
    processes = path.read_text().split("\nPASS\n")
    assert len(processes) == count + 1 and not processes[-1].strip(), (path, count)
    for process in processes[:-1]:
        check_process(process.splitlines(), count == 1)


def main():
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command", required=True)
    inputs = sub.add_parser("plan")
    for name in ("artifact", "metadata", "run", "output"):
        inputs.add_argument(name, type=Path)
    samples = sub.add_parser("verify-samples")
    samples.add_argument("count", type=int, choices=(1, 10))
    samples.add_argument("file", type=Path)
    args = parser.parse_args()
    if args.command == "plan":
        plan(args.artifact, args.metadata, args.run, args.output)
    else:
        verify_samples(args.file, args.count)


if __name__ == "__main__":
    main()
