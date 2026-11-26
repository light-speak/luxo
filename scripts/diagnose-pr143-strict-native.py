#!/usr/bin/env python3
"""Bind PR 143 replay inputs and fingerprint the rebuilt Linux executables."""

from collections import Counter
from hashlib import sha256
import json
from pathlib import Path
from struct import Struct
import sys
from zipfile import ZipFile

BASE = "36681c63627f4a3a4805bc6d19e908325614d3e9"
HEAD = "27fddbeee9a994e1ea1330df9b53b36d501c8652"
RUN = 45783705322
SCOPES = {
    "01": {
        "artifact": 15100912340,
        "digest": "81097204b2b2295d493cbd91aa42fd69662f758bd2a15972d00b7b3cd99fe376",
        "cpu": "Intel(R) Xeon(R) 6975P-C",
        "package": "pkg/lux/luvia",
        "profile": "luvia-gateway-identity",
        "filter": "^BenchmarkGatewayIdentity$/^authenticated$/^after$|^BenchmarkGatewayIdentity$/^authenticated$/^before$|^BenchmarkGatewayIdentity$/^typed-nil$/^after$|^BenchmarkGatewayIdentity$/^typed-nil$/^before$",
        "leaves": [
            "BenchmarkGatewayIdentity/authenticated/after-2",
            "BenchmarkGatewayIdentity/authenticated/before-2",
            "BenchmarkGatewayIdentity/typed-nil/after-2",
            "BenchmarkGatewayIdentity/typed-nil/before-2"
        ],
        "confirmed": [
            "GatewayIdentity/authenticated/after-2",
            "GatewayIdentity/typed-nil/after-2"
        ]
    },
    "06": {
        "artifact": 15101670856,
        "digest": "106a461008f5517568ee7579d97f5fd91578b5dbcda4d24c3dd1a6206c58166f",
        "cpu": "AMD EPYC 9V45 96-Core Processor",
        "package": "pkg/lux/luvia",
        "profile": "default",
        "filter": "^BenchmarkAuthMiddlewareIsolation$/^invalid$/^after$|^BenchmarkAuthMiddlewareIsolation$/^invalid$/^before$|^BenchmarkAuthMiddlewareIsolation$/^valid$/^before$",
        "leaves": [
            "BenchmarkAuthMiddlewareIsolation/invalid/after-2",
            "BenchmarkAuthMiddlewareIsolation/invalid/before-2",
            "BenchmarkAuthMiddlewareIsolation/valid/before-2"
        ],
        "confirmed": [
            "AuthMiddlewareIsolation/invalid/after-2",
            "AuthMiddlewareIsolation/invalid/before-2",
            "AuthMiddlewareIsolation/valid/before-2"
        ]
    },
    "07": {
        "artifact": 15101276107,
        "digest": "42517a42ff7c662e49f103c0262bd7a4259bfab10f7581f3b406ce23ccea994b",
        "cpu": "INTEL(R) XEON(R) PLATINUM 8573C",
        "package": "pkg/lux/rpc",
        "profile": "default",
        "filter": "^BenchmarkRPCDeadlineClassification$/^ExpiredEOF$/^After$|^BenchmarkRPCDeadlineClassification$/^ExpiredEOF$/^Before$",
        "leaves": [
            "BenchmarkRPCDeadlineClassification/ExpiredEOF/After-2",
            "BenchmarkRPCDeadlineClassification/ExpiredEOF/Before-2"
        ],
        "confirmed": [
            "RPCDeadlineClassification/ExpiredEOF/After-2"
        ]
    }
}


def prepare(shard, evidence, actual_cpu):
    actual_cpu = actual_cpu.strip()
    spec = SCOPES[shard]
    assert actual_cpu == spec["cpu"], actual_cpu
    metadata = json.loads((evidence / "original-metadata.json").read_bytes())
    assert metadata["id"] == spec["artifact"] and not metadata["expired"], metadata
    assert metadata["name"] == f"performance-evidence-{shard}-1", metadata
    assert metadata["digest"] == "sha256:" + spec["digest"], metadata
    assert metadata["workflow_run"]["id"] == RUN and metadata["workflow_run"]["head_sha"] == HEAD, metadata
    run = json.loads((evidence / "original-run.json").read_bytes())
    assert (run["id"], run["run_attempt"], run["head_sha"], run["event"], run["conclusion"]) == (
        RUN, 1, HEAD, "workflow_dispatch", "failure"), run
    original = evidence / "original.zip"
    assert sha256(original.read_bytes()).hexdigest() == spec["digest"], original
    expected = [{"Package": spec["package"], "Name": name} for name in spec["leaves"]]
    with ZipFile(original) as archive:
        assert archive.read("benchmark-revisions.txt") == f"base_sha={BASE}\ntested_sha={HEAD}\n".encode()
        profile = archive.read("benchmark-compile-profile.txt")
        assert b"luvia-gateway-identity-overlay-v1 ae790127f4acd6df4fe2388d239b2657c999add2e742584284c411bef514e365\n" in profile
        assert b"luvia-convenience-keys-overlay-v1\n" in profile
        assert b"rpc-owned-request-overlay-v1 request_reuse_test.go\n" in profile
        target = archive.read("benchmark-confirmation-batch.tsv")
        assert target == f"{spec['package']}\t{spec['profile']}\t{spec['filter']}\n".encode()
        inventory = archive.read("benchmark-confirmation-expected.json")
        assert json.loads(inventory) == {"Base": expected, "Head": expected}
        gate = archive.read("benchmark-gate.txt").decode()
        assert len(gate.splitlines()) == len(spec["confirmed"]), gate
        prefix = f"confirmed performance regression: github.com/light-speak/luxo/{spec['package']}/"
        assert all(f"{prefix}{leaf} sec/op " in gate for leaf in spec["confirmed"]), gate
        for direction in ("control", "control-reversed"):
            assert archive.read(f"benchmark-{direction}-gate.txt").startswith(b"performance gate passed:")
        for group in ("confirmation", "control"):
            for arm in ("base", "head"):
                raw = archive.read(f"benchmark-raw-{group}-{arm}.txt").decode()
                assert {line[5:].strip() for line in raw.splitlines() if line.startswith("cpu: ")} == {spec["cpu"]}
                rows = Counter(line.split()[0] for line in raw.splitlines() if line.startswith("Benchmark"))
                assert rows == {leaf: 10 for leaf in spec["leaves"]}, rows
    (evidence / "reference-profile.txt").write_bytes(profile)
    (evidence / "batch.tsv").write_bytes(target)
    (evidence / "expected.json").write_bytes(inventory)
    (evidence / "original-gate.txt").write_text(gate, encoding="utf-8")
    binding = {**spec, "base": BASE, "head": HEAD, "run": RUN, "attempt": 1, "actualCpu": actual_cpu,
               "sameCpuAsOriginalShard": actual_cpu == spec["cpu"],
               "coverage": "original targeted fixture only, not the full primary batch"}
    (evidence / "routing.json").write_text(json.dumps(binding, indent=2) + "\n", encoding="utf-8")


def executable_bytes(data, offset, length):
    assert 0 <= offset < len(data) and 0 < length <= len(data) - offset, (offset, length, len(data))
    return sha256(data[offset:offset + length]).hexdigest()


def executable_fingerprints(path):
    data = path.read_bytes()
    header = Struct("<16sHHIQQQIHHHHHH")
    program = Struct("<IIQQQQQQ")
    section = Struct("<IIQQQQIIQQ")
    ident, kind, machine, version, entry, phoff, shoff, _, _, phsize, phnum, shsize, shnum, strings = header.unpack_from(data)
    assert ident[:7] == b"\x7fELF\x02\x01\x01" and kind in (2, 3) and machine == 62 and version == 1
    assert phsize == program.size and shsize == section.size and 0 < strings < shnum
    segments = []
    for index in range(phnum):
        tag, flags, offset, address, _, length, resident, alignment = program.unpack_from(data, phoff + index * phsize)
        if tag == 1 and flags & 1:
            segments.append({"offset": offset, "address": address, "filesz": length, "memsz": resident,
                             "flags": flags, "alignment": alignment, "sha256": executable_bytes(data, offset, length)})
    assert segments
    sections = [section.unpack_from(data, shoff + index * shsize) for index in range(shnum)]
    start, size = sections[strings][4:6]
    names = data[start:start + size]
    text = [entry for entry in sections if names[entry[0]:].split(b"\0", 1)[0] == b".text"]
    assert len(text) == 1
    address, offset, length = text[0][3:6]
    return {"sha256": sha256(data).hexdigest(), "entry": entry,
            "text": {"offset": offset, "address": address, "size": length,
                     "sha256": executable_bytes(data, offset, length)},
            "executableLoadSegments": segments}


def symbol_fingerprints(binary, selected, elf):
    data = binary.read_bytes()
    segments = json.loads(elf.read_bytes())["executableLoadSegments"]
    result = {}
    seen = set()
    for line in selected.read_text().splitlines():
        address, size, binding, name = line.split(maxsplit=3)
        address, size = int(address, 16), int(size)
        identity = (name, address, binding)
        segments_for_symbol = [segment for segment in segments if
            segment["address"] <= address and address + size <= segment["address"] + segment["filesz"]]
        assert len(segments_for_symbol) == 1 and size > 0 and binding in {"t", "T"} and identity not in seen, line
        seen.add(identity)
        segment = segments_for_symbol[0]
        offset = address - segment["address"] + segment["offset"]
        # Inlined closures can have several text bodies with the same name.
        result.setdefault(name, []).append({
            "address": address, "binding": binding, "offset": offset, "size": size, "mod32": address % 32,
            "mod64": address % 64, "sha256": executable_bytes(data, offset, size),
        })
    assert result
    return result


if __name__ == "__main__":
    action, *args = sys.argv[1:]
    if action == "prepare":
        shard, evidence, cpu = args
        prepare(shard, Path(evidence), cpu)
    elif action == "elf":
        print(json.dumps(executable_fingerprints(Path(args[0])), indent=2))
    elif action == "symbols":
        print(json.dumps(symbol_fingerprints(*map(Path, args)), indent=2, sort_keys=True))
    else:
        raise SystemExit("expected prepare <shard> <evidence> <CPU>, elf <binary>, or symbols <binary> <nm> <elf>")
