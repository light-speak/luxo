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


def verify_samples(text, count):
    processes = text.split("\nPASS\n")
    assert len(processes) == count + 1 and not processes[-1].strip(), count
    for process in processes[:-1]:
        check_process(process.splitlines(), count == 1)


STOCK_RUN = 45829013113
STOCK_SHA = "3a82a85f4c3a35512fcec0e6a220772ea515a80a"
STOCK = (
    (15121816705, "c3c5ef31779d94f622d93f9eeb7fd60ff724d1ed33aba10ec59df5e27fe625f8"),
    (15122226439, "ac301ad0b2742d5cd9003e5a85e095ad8b7c5a6dd5ea0bb6f72f85ee08e36c84"),
)
EXPECTED_CONTROLS = {f"{arm}.{group}{suffix}": "0" for arm in ("aa", "bb") for group in (1, 2)
                     for suffix in ("", ".reverse")} | {"ab-combined": "1"}


def check_stock_archive(archive, slot, out):
    assert archive.testzip() is None
    assert archive.read("expected.json") == (out / "expected.json").read_bytes()
    assert archive.read("rebuilt-profile.txt") == (out / "original-compile-profile.txt").read_bytes()
    assert archive.read("original-revisions.txt").decode().splitlines() == [f"base_sha={BASE}", f"tested_sha={HEAD}"]
    route = dict(line.split("=") for line in archive.read("runner-route.txt").decode().splitlines())
    assert route["workflow"] == STOCK_SHA and route["actual"] == CPU and route["match"] == "true"
    gates = dict(line.split("\t") for line in archive.read("gates.tsv").decode().splitlines())
    assert gates == EXPECTED_CONTROLS
    rejected = [line for line in archive.read("ab-combined.gate.txt").decode().splitlines()
                if line.startswith("confirmed performance regression: ")]
    assert len(rejected) == 1 and "/RequestLogBoundary/disabled-2 sec/op" in rejected[0]
    for arm in ("base", "head"):
        verify_samples(archive.read(f"{arm}.inventory.raw").decode(), 1)
    for group in (1, 2):
        for comparison in ("aa", "ab", "bb"):
            for side in ("left", "right"):
                verify_samples(archive.read(f"{comparison}.{group}.{side}.raw").decode(), 10)
    (out / f"stock-slot-{slot}.gate.txt").write_bytes(archive.read("ab-combined.gate.txt"))
    return {
        f"{arm}.{suffix}": archive.read(f"{arm}.{suffix}")
        for arm in ("base", "head")
        for suffix in ("sha256", "text.sha256", "symbols.txt")
    }


def verify_stock(inputs, out):
    run = json.loads((inputs / "run.json").read_text())
    assert (run["id"], run["run_attempt"], run["head_sha"], run["event"], run["conclusion"]) == (
        STOCK_RUN, 1, STOCK_SHA, "push", "failure")
    refs = []
    for slot, (artifact_id, digest) in enumerate(STOCK, 1):
        meta = json.loads((inputs / f"{slot}.json").read_text())
        assert (meta["id"], meta["name"], meta["digest"]) == (
            artifact_id, f"pr83-log-boundary-6975-{slot}-{STOCK_RUN}-1", "sha256:" + digest)
        assert meta["workflow_run"]["id"] == STOCK_RUN and meta["workflow_run"]["head_sha"] == STOCK_SHA
        source = inputs / f"{slot}.zip"
        raw = source.read_bytes()
        assert len(raw) == meta["size_in_bytes"] and sha256(raw).hexdigest() == digest
        with ZipFile(source) as archive:
            refs.append(check_stock_archive(archive, slot, out))
    for name, observed in refs[0].items():
        reference = refs[1][name]
        if name.endswith("sha256"):
            observed, reference = observed.split()[0] + b"\n", reference.split()[0] + b"\n"
        assert observed == reference, name
        (out / ("stock-" + name)).write_bytes(observed)


def main():
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command", required=True)
    inputs = sub.add_parser("plan")
    for name in ("artifact", "metadata", "run", "output"):
        inputs.add_argument(name, type=Path)
    stock = sub.add_parser("verify-stock")
    stock.add_argument("inputs", type=Path)
    stock.add_argument("output", type=Path)
    samples = sub.add_parser("verify-samples")
    samples.add_argument("count", type=int, choices=(1, 10))
    samples.add_argument("file", type=Path)
    args = parser.parse_args()
    if args.command == "plan":
        plan(args.artifact, args.metadata, args.run, args.output)
    elif args.command == "verify-stock":
        verify_stock(args.inputs, args.output)
    else:
        verify_samples(args.file.read_text(), args.count)


if __name__ == "__main__":
    main()
