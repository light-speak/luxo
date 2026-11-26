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
REPLAY = 45804304446
REPLAY_HEAD = "d6690106e85d0608df32ba1a155acef76356dacf"
REPLAY_ARTIFACT = 15109146480
REPLAY_DIGEST = "878eb00c8351964f5361d3ea3f70fc587eac0767f4c091a3031ac5f0c9c43b57"
PACKAGE_PREFIX = "github.com/light-speak/luxo/pkg/lux/luvia."
AUTH_PREFIX = "github.com/light-speak/luxo/pkg/lux/auth."
VERIFIER = AUTH_PREFIX + "VerifyCachedWithExpiry"
BENCHMARK = PACKAGE_PREFIX + "BenchmarkAuthMiddlewareIsolation"
CONSTRUCTORS = {"before": PACKAGE_PREFIX + "authMiddlewareBeforeIsolation",
                "after": PACKAGE_PREFIX + "AuthMiddleware"}
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
        result.setdefault(name, []).append({
            "address": address, "binding": binding, "offset": offset, "size": size, "mod32": address % 32,
            "mod64": address % 64, "sha256": executable_bytes(data, offset, size),
        })
    assert result
    return result


def read_default_reference(scratch, evidence):
    metadata = json.loads((scratch / "replay-metadata.json").read_bytes())
    assert (metadata["id"], metadata["name"], metadata["size_in_bytes"], metadata["digest"]) == (
        REPLAY_ARTIFACT, f"pr143-strict-06-{REPLAY}-2-slot1", 141555, "sha256:" + REPLAY_DIGEST), metadata
    assert (metadata["workflow_run"]["id"], metadata["workflow_run"]["head_sha"]) == (REPLAY, REPLAY_HEAD)
    run = json.loads((scratch / "replay-run.json").read_bytes())
    assert (run["id"], run["run_attempt"], run["head_sha"], run["event"], run["conclusion"]) == (
        REPLAY, 2, REPLAY_HEAD, "push", "failure"), run
    raw = scratch / "replay.zip"
    assert raw.stat().st_size == metadata["size_in_bytes"] and sha256(raw.read_bytes()).hexdigest() == REPLAY_DIGEST
    ref = evidence / "default-reference"
    ref.mkdir()
    with ZipFile(raw) as archive:
        assert archive.testzip() is None
        route = dict(line.split("=", 1) for line in archive.read("runner-route.txt").decode().splitlines())
        assert route == {"workflow": REPLAY_HEAD, "slot": "1", "shard": "06",
                         "actual": SCOPES["06"]["cpu"], "match": "true"}, route
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


def read_disassembly(path):
    functions = {}
    for section in path.read_text().split("TEXT ")[1:]:
        header, *lines = section.splitlines()
        name, separator, _ = header.partition("(SB) ")
        assert separator, header
        body = []
        for line in filter(str.strip, lines):
            source, address, encoding, instruction = line.strip().split(maxsplit=3)
            body.append((source, int(address, 16), bytes.fromhex(encoding), instruction))
        identity = (name, body[0][1])
        assert identity not in functions, identity
        functions[identity] = body
    return functions


def target(pc, encoding, displacement):
    return pc + len(encoding) + int.from_bytes(encoding[displacement:displacement + 4], "little", signed=True)


def function_pointer(binary, address):
    header = Struct("<16sHHIQQQIHHHHHH")
    program = Struct("<IIQQQQQQ")
    ident, kind, machine, _, _, phoff, _, _, _, phsize, phnum, *_ = header.unpack_from(binary)
    assert ident[:7] == b"\x7fELF\x02\x01\x01" and kind == 2 and machine == 62 and phsize == program.size
    for index in range(phnum):
        tag, flags, offset, start, _, length, _, _ = program.unpack_from(binary, phoff + index * phsize)
        if tag == 1 and not flags & 1 and start <= address and address + 8 <= start + length:
            offset += address - start
            assert offset + 8 <= len(binary)
            return int.from_bytes(binary[offset:offset + 8], "little")
    return None


def unique_address(functions, name):
    address, = (address for candidate, address in functions if candidate == name)
    return address


def trace_dispatch(binary, functions):
    setup = functions[BENCHMARK + ".func1", unique_address(functions, BENCHMARK + ".func1")]
    constructors = {role: unique_address(functions, name) for role, name in CONSTRUCTORS.items()}
    slots = {}
    for source, pc, encoding, instruction in setup:
        if source != "middleware_bench_test.go:76" or not instruction.startswith("LEAQ ") or "(IP), " not in instruction:
            continue
        assert len(encoding) == 7 and encoding[1] == 0x8d and encoding[2] & 0xc7 == 5, instruction
        slot = target(pc, encoding, 3)
        address = function_pointer(binary, slot)
        for role, constructor in constructors.items():
            if address == constructor:
                assert role not in slots, (role, instruction)
                slots[role] = slot
    assert set(slots) == set(CONSTRUCTORS), slots

    routes = {}
    verifier = unique_address(functions, VERIFIER)
    for role, constructor in CONSTRUCTORS.items():
        constructor_address = constructors[role]
        body = functions[constructor, constructor_address]
        closure = constructor + ".func1"
        closure_address, = (target(pc, encoding, 3) for _, pc, encoding, instruction in body
                            if instruction.startswith(f"LEAQ {closure}(SB), ") and len(encoding) == 7)
        closure_body = functions[closure, closure_address]
        calls = [pc for _, pc, encoding, instruction in closure_body
                 if instruction == f"CALL {VERIFIER}(SB)" and encoding[:1] == b"\xe8"
                 and target(pc, encoding, 1) == verifier]
        call, = calls
        routes[role] = {"constructor": constructor_address, "functionValue": slots[role], "closure": closure_address,
                        "closureName": closure, "verifier": verifier, "verifierCall": call}
    return routes


def instruction_without_relocation(raw, instruction):
    if raw[:1] in (b"\xe8", b"\xe9") and len(raw) == 5 and instruction.endswith("(SB)"):
        return [raw[:1].hex(), instruction]
    if raw[:2] == b"\x83\x3d" and len(raw) == 7 and "(SB)" in instruction:
        return [(raw[:2] + raw[-1:]).hex(), instruction]
    if len(raw) == 8 and raw[0] >> 4 == 4 and raw[1:3] == b"\x83\x3d" and "(SB)" in instruction:
        return [(raw[:3] + raw[-1:]).hex(), instruction]
    if len(raw) == 7 and raw[0] >> 4 == 4 and raw[1] in (0x89, 0x8b, 0x8d) and raw[2] & 0xc7 == 5:
        if "(IP), " in instruction:
            return [raw[:-4].hex(), instruction.split("(IP), ", 1)[1]]
        if "(SB)" in instruction:
            return [raw[:-4].hex(), instruction]
    return [raw.hex()]


def only_shape(body):
    start = body[0][1]
    return [[pc - start, *instruction_without_relocation(raw, instruction)] for _, pc, raw, instruction in body]


def selected_code(functions, routes):
    names = (VERIFIER, PACKAGE_PREFIX + "extractBearerToken", BENCHMARK + ".func1.1", BENCHMARK + ".func1.1.1")
    selected = {name: (name, unique_address(functions, name)) for name in names}
    selected.update({role: (edge["closureName"], edge["closure"]) for role, edge in routes.items()})
    return {key: {"name": name, "address": pc, "shape": only_shape(functions[name, pc])}
            for key, (name, pc) in selected.items()}


def executable_shape(elf):
    return {key: elf[key] for key in ("entry", "text", "executableLoadSegments")}


def compiler_metadata(file):
    first, *rows = file.read_text().splitlines()
    assert first.split(": ", 1)[-1] == "go1.27.1", first
    return rows


def verify_alignment(evidence):
    def read(file):
        return json.loads(file.read_bytes())

    reference = evidence / "default-reference"
    layout = {}
    for arm in ARM_NAMES:
        elf = read(evidence / f"{arm}.elf.json")
        symbols = read(evidence / f"{arm}.symbols.json")
        decoded = read_disassembly(evidence / f"{arm}.objdump.txt")
        routes = read(evidence / f"{arm}.dispatch.json")
        code = selected_code(decoded, routes)
        for item in code.values():
            assert sum(row["address"] == item["address"] for row in symbols[item["name"]]) == 1, (arm, item["name"])
        layout[arm] = elf, symbols, code
        (evidence / f"{arm}.normalized.json").write_text(json.dumps(code, indent=2, sort_keys=True) + "\n")

    report = {}
    for arm in ARM_NAMES[:2]:
        current, symbols, code = layout[arm]
        original = read(reference / f"{arm}.elf.json")
        assert executable_shape(current) == executable_shape(original), (arm, "stock ELF segment drift")
        assert symbols == read(reference / f"{arm}.symbols.json"), (arm, "stock symbol drift")
        original_build = compiler_metadata(reference / f"{arm}.build.txt")
        assert compiler_metadata(evidence / f"{arm}.build.txt") == original_build, arm
        aligned_build = compiler_metadata(evidence / f"{arm}64.build.txt")
        flag = "\tbuild\t-ldflags=-funcalign=64"
        assert aligned_build.count(flag) == 1 and [row for row in aligned_build if row != flag] == original_build, arm

        aligned, _, aligned_code = layout[arm + "64"]
        assert current["text"]["sha256"] != aligned["text"]["sha256"], (arm, "alignment absent")
        for name, item in code.items():
            intervention = aligned_code[name]
            assert intervention["address"] % 64 == 0 and intervention["shape"] == item["shape"], (arm, name)
        report[arm] = {"exactStockELF": current["sha256"] == original["sha256"],
                       "layout": {name: [item["address"], aligned_code[name]["address"]] for name, item in code.items()}}
    for left, right in (("base", "head"), ("base64", "head64")):
        for name, item in layout[left][2].items():
            assert item["shape"] == layout[right][2][name]["shape"], (left, right, name)
    (evidence / "alignment-verification.json").write_text(json.dumps(report, indent=2) + "\n")


def confirmed_targets(text):
    prefix = "confirmed performance regression: " + PACKAGE_PREFIX[:-1] + "/"
    rows = [line for line in text.splitlines() if line.startswith("confirmed performance regression:")]
    if not all(line.startswith(prefix) and " sec/op " in line for line in rows):
        return False
    confirmed = {line.removeprefix(prefix).split(" sec/op ", 1)[0] for line in rows}
    allowed = set(SCOPES["06"]["confirmed"])
    required = {name for name in allowed if "/invalid/" in name}
    return required <= confirmed <= allowed and len(confirmed) == len(rows)


FAMILY = {f"BenchmarkAuthMiddlewareIsolation/{sample}/{side}-2"
          for sample in ("valid", "anonymous", "invalid", "inherited-missing", "inherited-other-key")
          for side in ("before", "after")}


def verify_inventory(path):
    rows = [line.split() for line in path.read_text().splitlines() if line.startswith("Benchmark")]
    assert Counter(row[0] for row in rows) == {name: 1 for name in FAMILY}, rows
    for row in rows:
        assert row[1] == "1" and set(row[3::2]) == {"ns/op", "B/op", "allocs/op"}, row


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
    elif action == "verify-alignment":
        verify_alignment(Path(args[0]))
    elif action == "dispatch":
        binary, disassembly = map(Path, args)
        print(json.dumps(trace_dispatch(binary.read_bytes(), read_disassembly(disassembly)), indent=2))
    elif action == "inventory":
        verify_inventory(Path(args[0]))
    elif action == "confirmed-targets":
        raise SystemExit(0 if confirmed_targets(Path(args[0]).read_text()) else 1)
    else:
        raise SystemExit(f"unknown diagnostic action: {action}")
