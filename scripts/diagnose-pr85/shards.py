#!/usr/bin/env python3
"""Replay the original #85 confirmation inventory on the original processor models."""

from hashlib import sha256
from pathlib import Path
from zipfile import ZipFile
import json
import math
import re
import sys

from evidence import BASE, HEAD, disassembly

AMD_7763 = "AMD EPYC 7763 64-Core Processor"
AMD_9V74 = "AMD EPYC 9V74 80-Core Processor"
INTEL = "Intel(R) Xeon(R) 6975P-C"
API = "pkg/lux/api"
RPC = "pkg/lux/rpc"
PROFILES = {API: "api-non-identity", RPC: "default"}

# Each shard retains every confirmation target. The count identifies the leading confirmed regressions.
SHARDS = {
    "01": (AMD_7763, API, 15063718702, 126779, "cb140d44a88b62096801450526b2533733125bc8b6c0e1f36ccd4753c9f6ab08", 3, (
        "StaticValueParams/BytesArray/1/JSON/static", "StaticValueParams/RawJSON/short/JSON/static",
        "StaticValueParams/UUID/JSON/previous", "TaskGroupAbnormalExit/goexit",
    )),
    "02": (INTEL, RPC, 15064588125, 122170, "d7a1ca41a8f9fbebe52861b088e638636e7f6b5dded4d0c2f030daf0adf9a996", 1, (
        "LRPCFrameCapacityReuse/4096/after", "LRPCFrameCapacityReuse/4096/before",
    )),
    "03": (AMD_9V74, API, 15065291911, 128114, "94d640f7cca9d9a654a4721fdca1efbd7bf974bb79a33d91b487f6f2bddf16da", 2, (
        "CanonicalJSONTyped/boolean/1", "CanonicalJSONTyped/enum/1",
        "CanonicalJSONTyped/escaped/1", "CanonicalJSONTyped/unique/1",
    )),
    "04": (AMD_9V74, API, 15063644203, 130876, "0072b07759faff61616d60740dbae7557a29ca1f51091bb6ed1e9f2b6d816b9d", 2, (
        "CompiledCachePolicy/hit=true/before", "DebugTraceTextEncoding/spans=1/text=0/binary/before=false",
    )),
    "07": (AMD_9V74, API, 15064522651, 125979, "d1fea3de73629b86f095fc8787d2791ff4e8d51b6d5d71147dfde0199bcf7335", 1, (
        "CanonicalJSONValue/object/after", "CanonicalJSONValue/object/before",
    )),
}

HOT = {
    AMD_7763: (
        r"BenchmarkStaticValueParams|benchmarkStaticValue|LegacyParamJSON|ParamBytesArray|ParamRawJSON|ParseRawJSON|"
        r"ParamUUID|BenchmarkTaskGroupAbnormalExit|TaskGroup|writeParseError"
    ),
    AMD_9V74: (
        r"BenchmarkCanonicalJSONTyped|BenchmarkCanonicalJSONValue|benchmarkCanonicalJSONValue|prepareJSONRequest|"
        r"writeCanonicalJSON|jsonParamValue|encodeBinaryParam|BenchmarkCompiledCachePolicy|benchmarkCompiledCachePolicy|"
        r"cacheResponseBeforePolicy|preparedCacheRoute|BenchmarkDebugTraceTextEncoding|benchmarkTraceEncoding|"
        r"EncodeDebugTrace|DecodeDebugTrace|writeParseError"
    ),
    INTEL: r"BenchmarkLRPCFrameCapacityReuse|ReadFrame|readFrameBeforeCapacityReuse|retainReadFrame|io[.]ReadFull",
}


def lane(cpu):
    matches = [(shard, row) for shard, row in SHARDS.items() if row[0] == cpu]
    if not matches:
        raise SystemExit(3)
    return matches


def filter_for(names):
    # Go's benchmark filter splits only at top-level slashes and vertical bars.
    return "|".join("/".join(f"^{re.escape(part)}$" for part in ("Benchmark" + name).split("/"))
                    for name in sorted(names))


def verify_original(read, cpu, package, names, confirmed):
    assert read("benchmark-revisions.txt") == f"base_sha={BASE}\ntested_sha={HEAD}\n"
    env = read("benchmark-environment.txt")
    assert re.search(rf"(?m)^Model name:\s+{re.escape(cpu)}$", env)
    assert json.loads(env[env.rfind("{"):]) == {
        "CGO_ENABLED": "1", "GOAMD64": "v1", "GOARCH": "amd64", "GOOS": "linux", "GOVERSION": "go1.27.1",
    }
    profile = PROFILES[package]
    batch = f"{package}\t{profile}\t{filter_for(names)}\n"
    assert read("benchmark-confirmation-batch.tsv") == batch
    targets = sorted(f"{package}\tBenchmark{name}-2" for name in names)
    assert sorted(read("benchmark-confirmation-targets.tsv").splitlines()) == targets
    expected = [{"Package": package, "Name": f"Benchmark{name}-2"} for name in sorted(names)]
    confirmation = read("benchmark-confirmation-expected.json")
    assert json.loads(confirmation) == {"Base": expected, "Head": expected}
    verdict = read("benchmark-gate.txt")
    found = re.findall(rf"(?m)^confirmed performance regression: github.com/light-speak/luxo/{package}/(\S+)-2 sec/op ", verdict)
    assert sorted(found) == sorted(names[:confirmed]), (found, names[:confirmed])
    for filename in ("benchmark-control-gate.txt", "benchmark-control-reversed-gate.txt"):
        assert read(filename).startswith("performance gate passed:")
    return batch, confirmation, verdict, read("benchmark-compile-profile.txt")


def bind(cpu, directory):
    package = None
    previous_profile = None
    names = []
    for shard, (_, pkg, artifact, size, digest, confirmed, leaves) in lane(cpu):
        if package is None:
            package = pkg
        assert package == pkg
        archive_path = directory / f"{artifact}.zip"
        assert archive_path.stat().st_size == size
        assert sha256(archive_path.read_bytes()).hexdigest() == digest
        with ZipFile(archive_path) as zip_file:
            assert zip_file.testzip() is None
            batch, expected, verdict, label = verify_original(
                lambda member: zip_file.read(member).decode(), cpu, pkg, leaves, confirmed,
            )
        if previous_profile is None:
            previous_profile = label
        assert previous_profile == label
        (directory / f"shard-{shard}.batch.tsv").write_text(batch)
        (directory / f"shard-{shard}.expected.json").write_text(expected)
        (directory / f"shard-{shard}.gate.txt").write_text(verdict)
        names.extend(leaves)
    assert len(set(names)) == len(names)
    profile = PROFILES[package]
    expected = {"Base": [], "Head": []}
    for side in expected:
        expected[side] = [{"Package": package, "Name": f"Benchmark{name}-2"} for name in sorted(names)]
    (directory / "shards.expected.json").write_text(json.dumps(expected, indent=2) + "\n")
    (directory / "original-profile.txt").write_text(previous_profile)
    batch = f"{package}\t{profile}\t{filter_for(names)}\n"
    (directory / "shards.batch.tsv").write_text(batch)
    print(batch.rstrip("\n") + "\t" + HOT[cpu])


def smoke(cpu, directory, path):
    lines = path.read_text().splitlines()
    assert all(row in lines for row in ("goos: linux", "goarch: amd64", f"cpu: {cpu}", "PASS"))
    keys = json.loads((directory / "shards.expected.json").read_text())["Base"]
    package, = {row["Package"] for row in keys}
    assert f"pkg: github.com/light-speak/luxo/{package}" in lines
    expected = [row["Name"] for row in keys]
    seen = []
    for line in lines:
        if line.startswith("Benchmark"):
            row = line.split()
            assert len(row) == 8 and row[1] == "1" and row[3::2] == ["ns/op", "B/op", "allocs/op"]
            assert all(math.isfinite(float(x)) and float(x) >= 0 for x in row[2::2]), row
            seen.append(row[0])
    assert sorted(seen) == sorted(expected), seen


def linkage(directory, out):
    required = {row["Name"].split("/")[0]
                for row in json.loads((directory / "shards.expected.json").read_text())["Base"]}
    arms = {}
    for arm in ("A", "B"):
        objects = disassembly(out / f"{arm}.hot.objdump.txt")
        entries = {}
        for line in (out / f"{arm}.hot.nm.txt").read_text().splitlines():
            addr, size, kind, name = line.split(maxsplit=3)
            address = int(addr, 16)
            entry = {"address": hex(address), "size": int(size), "kind": kind,
                     "mod16": address % 16, "mod32": address % 32, "mod64": address % 64,
                     "pageOffset": address % 4096}
            if name in objects:
                raw = b"".join(objects[name]["bytes"])
                entry.update(opcodeSha256=sha256(raw).hexdigest(), calls=sorted(objects[name]["calls"]))
            entries[name] = entry
        for root in required:
            assert any(name.endswith("." + root) and "opcodeSha256" in entry
                       for name, entry in entries.items()), (arm, root)
        arms[arm] = entries
    details = {}
    for name in sorted(arms["A"].keys() | arms["B"].keys()):
        left, right = (arms[arm].get(name) for arm in ("A", "B"))
        details[name] = {"A": left, "B": right,
                         "sameOpcodes": bool(left and right and left.get("opcodeSha256")
                                             and left["opcodeSha256"] == right.get("opcodeSha256"))}
    print(json.dumps(details, indent=2))


if __name__ == "__main__":
    mode, *args = sys.argv[1:]
    if mode == "artifacts":
        for shard, (_, _, artifact, size, digest, _, _) in lane(args[0]):
            print(f"{artifact}\t{size}\t{digest}\tperformance-evidence-{shard}-1")
    elif mode == "bind":
        bind(args[0], Path(args[1]))
    elif mode == "smoke":
        smoke(args[0], Path(args[1]), Path(args[2]))
    elif mode == "linkage":
        linkage(*map(Path, args))
    else:
        raise SystemExit("use artifacts, bind, smoke or linkage")
