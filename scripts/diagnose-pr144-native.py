#!/usr/bin/env python3
"""Bind PR 144 replay inputs and fingerprint the rebuilt Linux executables."""

from collections import Counter
from hashlib import sha256
import json
from pathlib import Path
from struct import Struct
import sys
from zipfile import ZipFile

BASE = "e9159f125064eccd6499ff5f9426f9c020f05dcc"
HEAD = "79ca343d68b265be411d07166702497eba1cd008"
RUN = 45769946774
SCOPES = {
    "09": {
        "artifact": 15095340498,
        "digest": "fce5ce12a2aa6db4b3e9ec187e3321bea32992d879f2577065553a35df14f34b",
        "cpu": "AMD EPYC 9V74 80-Core Processor",
        "package": "pkg/lux/api",
        "profile": "api-non-identity",
        "filter": "^BenchmarkInternalRouteExport$/^routes=16$/^mixed=true$/^current$",
        "leaves": ["BenchmarkInternalRouteExport/routes=16/mixed=true/current-2"],
    },
    "10": {
        "artifact": 15094702482,
        "digest": "71a93383312e2f86c97e2ee3e0a7133d8eaa3f4b867024a15193cb33d6835508",
        "cpu": "AMD EPYC 9V45 96-Core Processor",
        "package": "pkg/codegen",
        "profile": "default",
        "filter": "|".join(
            "^BenchmarkSoftDeleteIdentifiers$/^" + kind + "$/^" + count + "$"
            for kind, count in (("explicit", "128"), ("implicit", "1"), ("implicit", "128"), ("ordinary", "128"))
        ),
        "leaves": [
            "BenchmarkSoftDeleteIdentifiers/" + name + "-2"
            for name in ("explicit/128", "implicit/1", "implicit/128", "ordinary/128")
        ],
    },
}


def prepare(shard, evidence, actual_cpu):
    actual_cpu = actual_cpu.strip()
    spec = SCOPES[shard]
    metadata = json.loads((evidence / "original-metadata.json").read_bytes())
    assert metadata["id"] == spec["artifact"] and not metadata["expired"], metadata
    assert metadata["name"] == f"performance-evidence-{shard}-1", metadata
    assert metadata["digest"] == "sha256:" + spec["digest"], metadata
    assert metadata["workflow_run"]["id"] == RUN and metadata["workflow_run"]["head_sha"] == HEAD, metadata
    original = evidence / "original.zip"
    assert sha256(original.read_bytes()).hexdigest() == spec["digest"], original
    expected = [{"Package": spec["package"], "Name": name} for name in spec["leaves"]]
    with ZipFile(original) as archive:
        assert archive.read("benchmark-revisions.txt") == f"base_sha={BASE}\ntested_sha={HEAD}\n".encode()
        profile = archive.read("benchmark-compile-profile.txt")
        assert b"luvia-convenience-keys-overlay-v1\n" in profile
        assert b"rpc-owned-request-overlay-v1 request_reuse_test.go\n" in profile
        target = archive.read("benchmark-confirmation-batch.tsv")
        assert target == f"{spec['package']}\t{spec['profile']}\t{spec['filter']}\n".encode()
        inventory = archive.read("benchmark-confirmation-expected.json")
        assert json.loads(inventory) == {"Base": expected, "Head": expected}
        for group in ("confirmation", "control"):
            for arm in ("base", "head"):
                raw = archive.read(f"benchmark-raw-{group}-{arm}.txt").decode()
                assert {line[5:].strip() for line in raw.splitlines() if line.startswith("cpu: ")} == {spec["cpu"]}
                rows = Counter(line.split()[0] for line in raw.splitlines() if line.startswith("Benchmark"))
                assert rows == {leaf: 10 for leaf in spec["leaves"]}, rows
    (evidence / "reference-profile.txt").write_bytes(profile)
    (evidence / "batch.tsv").write_bytes(target)
    (evidence / "expected.json").write_bytes(inventory)
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


if __name__ == "__main__":
    action, *args = sys.argv[1:]
    if action == "prepare":
        shard, evidence, cpu = args
        prepare(shard, Path(evidence), cpu)
    elif action == "elf":
        print(json.dumps(executable_fingerprints(Path(args[0])), indent=2))
    else:
        raise SystemExit("expected prepare <shard> <evidence> <CPU> or elf <binary>")
