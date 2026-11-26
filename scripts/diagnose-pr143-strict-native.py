#!/usr/bin/env python3
"""Bind PR 143 replay inputs and fingerprint the rebuilt Linux executables."""

from collections import Counter
from hashlib import sha256
import json
import re
from pathlib import Path
from struct import Struct
import sys
from zipfile import ZipFile

BASE = "36681c63627f4a3a4805bc6d19e908325614d3e9"
HEAD = "27fddbeee9a994e1ea1330df9b53b36d501c8652"
RUN = 45783705322
REPLAY = 45791729810
REPLAY_HEAD = "99926f9dac1ebcca449ada8f6c9d33e231bd6a70"
REPLAY_ARTIFACT = 15103451534
REPLAY_DIGEST = "2d10b219bdc8299676ea13fb30e213efa31d7a9b9ff45b3295fd7f6786eb5d71"
PACKAGE_PREFIX = "github.com/light-speak/luxo/pkg/lux/luvia."
HOT_SYMBOLS = {"context.(*valueCtx).Value"} | {PACKAGE_PREFIX + name for name in (
    "newGateway.func1", "BenchmarkGatewayIdentity", "BenchmarkGatewayIdentity.func1",
    "BenchmarkGatewayIdentity.func1.1", "BenchmarkGatewayIdentity.func1.2",
)}
ARM_NAMES = ("base", "head", "base64", "head64")
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
    for line in selected.read_text().splitlines():
        address, size, _, name = line.split(maxsplit=3)
        address, size = int(address, 16), int(size)
        segments_for_symbol = [segment for segment in segments if
            segment["address"] <= address and address + size <= segment["address"] + segment["filesz"]]
        assert len(segments_for_symbol) == 1 and size > 0 and name not in result, line
        segment = segments_for_symbol[0]
        offset = address - segment["address"] + segment["offset"]
        result[name] = {"address": address, "offset": offset, "size": size, "mod32": address % 32,
                        "mod64": address % 64, "sha256": executable_bytes(data, offset, size)}
    assert result
    return result


def read_default_reference(scratch, evidence):
    metadata = json.loads((scratch / "replay-metadata.json").read_bytes())
    assert (metadata["id"], metadata["name"], metadata["size_in_bytes"], metadata["digest"]) == (
        REPLAY_ARTIFACT, f"pr143-strict-01-{REPLAY}-1", 57543, "sha256:" + REPLAY_DIGEST), metadata
    assert (metadata["workflow_run"]["id"], metadata["workflow_run"]["head_sha"]) == (REPLAY, REPLAY_HEAD)
    run = json.loads((scratch / "replay-run.json").read_bytes())
    assert (run["id"], run["run_attempt"], run["head_sha"], run["event"], run["conclusion"]) == (
        REPLAY, 1, REPLAY_HEAD, "push", "failure"), run
    raw = scratch / "replay.zip"
    assert raw.stat().st_size == metadata["size_in_bytes"] and sha256(raw.read_bytes()).hexdigest() == REPLAY_DIGEST
    ref = evidence / "default-reference"
    ref.mkdir()
    with ZipFile(raw) as archive:
        assert archive.testzip() is None
        route = dict(line.split("=", 1) for line in archive.read("runner-route.txt").decode().splitlines())
        assert route == {"workflow": REPLAY_HEAD, "shard": "01", "expected": SCOPES["01"]["cpu"],
                         "actual": SCOPES["01"]["cpu"], "match": "true"}, route
        assert archive.read("source-mode.txt") == (
            f"A checkout={BASE}\nB archive={HEAD}\nattributes files=none in either tree\n").encode()
        for previous, current in (("batch.tsv", "batch.tsv"), ("expected.json", "expected.json"),
                                  ("rebuilt-profile.txt", "reference-profile.txt")):
            assert archive.read(previous) == (evidence / current).read_bytes(), previous
        assert confirmed_targets(archive.read("ab-combined.gate.txt").decode())
        outcomes = dict(line.split("\t") for line in archive.read("gates.tsv").decode().splitlines())
        assert outcomes == {f"{arm}.{group}{suffix}": "0" for arm in ("aa", "bb") for group in (1, 2)
                            for suffix in ("", ".reverse")} | {"ab-combined": "1"}, outcomes
        for arm in ARM_NAMES[:2]:
            for suffix in ("elf.json", "symbols.json", "build.txt", "readelf.txt", "objdump.txt"):
                filename = f"{arm}.{suffix}"
                (ref / filename).write_bytes(archive.read(filename))
        for filename in ("runner-route.txt", "ab-combined.gate.txt"):
            (ref / filename).write_bytes(archive.read(filename))


def instruction_without_relocation(encoding, instruction):
    raw = bytes.fromhex(encoding)
    if raw[:1] in (b"\xe8", b"\xe9") and len(raw) == 5 and instruction.endswith("(SB)"):
        return [raw[:1].hex(), instruction]
    if raw[:2] == b"\x83\x3d" and instruction.startswith("CMPL runtime.writeBarrier(SB),"):
        return [(raw[:2] + raw[-1:]).hex(), instruction]
    if instruction.startswith("LEAQ ") and "(IP), " in instruction and len(raw) == 7:
        return [raw[:-4].hex(), instruction.split("(IP), ", 1)[1]]
    if instruction.startswith("LEAQ ") and "(SB), " in instruction and len(raw) == 7:
        return [raw[:-4].hex(), instruction]
    return [raw.hex()]


def normalized_disassembly(path):
    output = {}
    for section in path.read_text().split("TEXT ")[1:]:
        lines = section.splitlines()
        name = lines[0].split()[0].removesuffix("(SB)")
        if name not in HOT_SYMBOLS | {PACKAGE_PREFIX + "New"}:
            continue
        instructions = []
        for line in filter(str.strip, lines[1:]):
            _, address, encoding, instruction = line.strip().split(maxsplit=3)
            instructions.append((int(address, 16), instruction_without_relocation(encoding, instruction.strip())))
        assert name not in output and instructions, name
        first = instructions[0][0]
        output[name] = {"address": first, "shape": [[address - first, *op] for address, op in instructions]}
    assert HOT_SYMBOLS | {PACKAGE_PREFIX + "New"} <= output.keys(), path
    return output


def nearby_text_map(source, output):
    rows = source.read_text().splitlines()
    names = [row.split(maxsplit=3)[-1] for row in rows]
    indexes = [index for index, name in enumerate(names) if name in HOT_SYMBOLS | {PACKAGE_PREFIX + "New"}]
    assert {names[index] for index in indexes} == HOT_SYMBOLS | {PACKAGE_PREFIX + "New"}, names[:2]
    keep = {neighbor for index in indexes for neighbor in range(max(0, index - 2), min(len(rows), index + 3))}
    output.write_text("\n".join(rows[index] for index in sorted(keep)) + "\n")


def executable_shape(elf):
    return {"entry": elf["entry"], "text": elf["text"],
            "segments": [{key: value for key, value in item.items() if key != "sha256"}
                         for item in elf["executableLoadSegments"]]}


def compiler_metadata(file):
    first, *rows = file.read_text().splitlines()
    assert first.split(": ", 1)[-1] == "go1.27.1", first
    return rows


def verify_alignment(evidence, current_source=False):
    def read(file):
        return json.loads(file.read_bytes())

    reference = evidence / "default-reference"
    layout = {}
    for arm in ARM_NAMES:
        elf = read(evidence / f"{arm}.elf.json")
        symbols = read(evidence / f"{arm}.symbols.json")
        assert HOT_SYMBOLS <= symbols.keys(), (arm, symbols.keys())
        assembly = normalized_disassembly(evidence / f"{arm}.objdump.txt")
        assert all(code["address"] == symbols[name]["address"] for name, code in assembly.items()), arm
        layout[arm] = (elf, symbols, assembly)
        (evidence / f"{arm}.normalized.json").write_text(json.dumps(assembly, indent=2, sort_keys=True) + "\n")

    report = {}
    for arm in ARM_NAMES[:2]:
        current, symbols, assembly = layout[arm]
        original_build = compiler_metadata(evidence / f"{arm}.build.txt")
        if not current_source:
            previous = read(reference / f"{arm}.elf.json")
            assert executable_shape(current) == executable_shape(previous), (arm, "default executable code drift")
            for name, identity in read(reference / f"{arm}.symbols.json").items():
                assert symbols[name] == identity, (arm, name, "default symbol drift")
            assert original_build == compiler_metadata(reference / f"{arm}.build.txt"), arm
        aligned_build = compiler_metadata(evidence / f"{arm}64.build.txt")
        assert [row for row in aligned_build if row != "\tbuild\t-ldflags=-funcalign=64"] == original_build, arm
        aligned, aligned_symbols, aligned_assembly = layout[arm + "64"]
        for name, code in assembly.items():
            assert aligned_symbols[name]["mod64"] == 0, (arm, name, "unaligned symbol")
            assert code["shape"] == aligned_assembly[name]["shape"], (arm, name, "link changed instructions")
        production = PACKAGE_PREFIX + "newGateway.func1"
        assert current["text"]["sha256"] != aligned["text"]["sha256"], (arm, "link intervention absent")
        report[arm] = {"identityEntryMod64": [symbols[production]["mod64"], aligned_symbols[production]["mod64"]]}
        if not current_source:
            assert symbols[production]["mod64"] == (0 if arm == "base" else 32), (arm, "original layout absent")
            report[arm].update(sameDefaultELF=current["sha256"] == previous["sha256"],
                               sameDefaultExecutableSegments=current["executableLoadSegments"] == previous["executableLoadSegments"],
                               sameDefaultTextAndSymbolMap=True)
    if current_source:
        def identity(arm):
            return layout[arm][2][PACKAGE_PREFIX + "newGateway.func1"]["shape"]

        report["stockIdentityInstructionsMatch"] = identity("base") == identity("head")
    (evidence / "alignment-verification.json").write_text(json.dumps(report, indent=2) + "\n")


def confirmed_targets(text):
    prefix = "confirmed performance regression: github.com/light-speak/luxo/pkg/lux/luvia/"
    rows = [line for line in text.splitlines() if line.startswith("confirmed performance regression:")]
    if len(rows) != 2 or not all(line.startswith(prefix) and " sec/op " in line for line in rows):
        return False
    confirmed = {line.removeprefix(prefix).split(" sec/op ", 1)[0] for line in rows}
    return confirmed == set(SCOPES["01"]["confirmed"])


def current_plan(evidence):
    root = "BenchmarkGatewayIdentity"
    supported = ("anonymous", "authenticated", "typed-nil")
    leaves = [f"{root}/{subject}/{side}" for subject in supported for side in ("after", "before")]
    package = "pkg/lux/luvia"
    rows = [{"Package": package, "Name": leaf + "-2"} for leaf in leaves]
    (evidence / "expected.json").write_text(json.dumps({"Base": rows, "Head": rows}) + "\n")
    selector = "|".join("/".join(f"^{fragment}$" for fragment in leaf.split("/")) for leaf in leaves)
    (evidence / "batch.tsv").write_text(f"{package}\tluvia-gateway-identity\t{selector}\n")


def current_inventory(expected_path, inventory_path, cpu):
    expected = sorted(item["Name"] for item in json.loads(expected_path.read_bytes())["Base"])
    result = inventory_path.read_text()
    actual = re.findall(r"^(Benchmark\S+)\s+(\d+)\s+", result, re.M)
    assert sorted(name for name, size in actual if size == "1") == expected, "wrong one-iteration fixture inventory"
    assert len(actual) == len(expected) and result.splitlines().count("PASS") == 1, "invalid inventory result"
    assert re.findall(r"^cpu: (.+)$", result, re.M) == [cpu], "inventory CPU differs"


def current_targets(result):
    prefix = "confirmed performance regression: github.com/light-speak/luxo/pkg/lux/luvia/"
    rows = [line for line in result.splitlines() if line.startswith("confirmed performance regression:")]
    if not rows or not all(line.startswith(prefix) and " sec/op " in line for line in rows):
        return False
    names = {line.removeprefix(prefix).split(" sec/op ", 1)[0] for line in rows}
    return "GatewayIdentity/authenticated/after-2" in names and names <= set(SCOPES["01"]["confirmed"])


if __name__ == "__main__":
    action, *args = sys.argv[1:]
    if action == "prepare":
        shard, evidence, cpu = args
        prepare(shard, Path(evidence), cpu)
    elif action == "elf":
        print(json.dumps(executable_fingerprints(Path(args[0])), indent=2))
    elif action == "symbols":
        print(json.dumps(symbol_fingerprints(*map(Path, args)), indent=2, sort_keys=True))
    elif action == "reference":
        read_default_reference(*map(Path, args))
    elif action == "map":
        nearby_text_map(*map(Path, args))
    elif action == "verify-alignment":
        verify_alignment(Path(args[0]))
    elif action == "verify-current-alignment":
        verify_alignment(Path(args[0]), current_source=True)
    elif action == "current-plan":
        current_plan(Path(args[0]))
    elif action == "current-inventory":
        current_inventory(Path(args[0]), Path(args[1]), args[2])
    elif action == "current-targets":
        raise SystemExit(0 if current_targets(Path(args[0]).read_text()) else 1)
    elif action == "confirmed-targets":
        raise SystemExit(0 if confirmed_targets(Path(args[0]).read_text()) else 1)
    else:
        raise SystemExit(f"unknown diagnostic action: {action}")
