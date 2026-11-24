"""Verify an isolated PR 72 payload benchmark without altering its timed source."""
import csv
import json
from pathlib import Path
import re
import runpy
import struct
import subprocess
import sys

shared = runpy.run_path(str(Path(__file__).with_name("diagnose-pr72-codec-split.py")))
require, sha, read_json, write_json, measure_names = (
    shared[name] for name in ("require", "sha", "read_json", "write_json", "measure_names")
)
FIXTURE_SHA = {
    "payload_bench_test.go": "b254012969b1c2d19d626217f3bb53c571325f03c3bccffef7ea5a871303d76b",
    "payload_reference_test.go": "34950f700c3be8f0a3b393be2ab5a8d48a004217623717fe8b1707c982834cc3",
    "payload_test.go": "211e58834c609aef0d51e38642254d963c8b3b7c39143ec0a3f3f7ddf43b2124",
}
ROLES = ("base", "candidate")
CPU = "AMD EPYC 7763 64-Core Processor"
HOT = (
    "BenchmarkElementPayload", "BenchmarkElementPayload.func1",
    "payloadReaderCasesBefore.func13", "payloadReaderCasesBefore.func14",
    "payloadReaderCases.func13", "payloadReaderCases.func14",
    "ReadColumnIntPtr", "ReadColumnFloatPtr", "readColumnIntPtrBeforePayload",
    "readColumnFloatPtrBeforePayload", "benchmarkPayloadRead", "runtime.makeslice",
)


def mask(data, begin, end):
    require(0 <= begin < end <= len(data), "invalid fixture declaration span")
    return data[:begin] + bytes(10 if byte == 10 else 32 for byte in data[begin:end]) + data[end:]


def make_overlay(source, frozen, output, manifest):
    original = read_json(frozen)["Replace"]
    require(all(Path(key).parent == source for key in original), "frozen overlay paths differ from codec")
    require(all(key.endswith("_test.go") for key in original), "non-test frozen overlay entry")
    active = {key: "" for key in original}
    output.parent.mkdir(exist_ok=True)
    rows = {}
    for name, expected in FIXTURE_SHA.items():
        key = str(source / name)
        path = Path(original[key])
        require(sha(path) == expected, f"frozen fixture changed: {name}")
        initial = path.read_bytes()
        data = initial
        if name == "payload_bench_test.go":
            data = mask(data, data.index(b"func BenchmarkMissingElementPayload("),
                        data.index(b"func benchmarkPayloadRead("))
        elif name == "payload_test.go":
            data = mask(data, data.index(b"import ("), data.index(b"type payloadReaderCase struct"))
            data = mask(data, data.index(b"func TestElementPayloadPreflight("), len(data))
        require(len(data) == len(initial) and data.count(b"\n") == initial.count(b"\n"),
                f"positional source altered in {name}")
        projected = output.parent / name
        require(not projected.exists(), f"existing fixture projection: {projected}")
        projected.write_bytes(data)
        active[key] = str(projected)
        rows[name] = {"frozenSHA256": expected, "projectedSHA256": sha(projected)}
    write_json(output, {"Replace": active})
    write_json(manifest, rows)


def exact_target_names(evidence):
    result = read_json(evidence / "expected.json")
    require(result["Base"] == result["Head"], "read leaf inventory differs between revisions")
    names = {row["Name"] for row in result["Base"]}
    expected = {
        f"BenchmarkElementPayload/{kind}-nullable/column=true/count=128/version={version}-2"
        for kind in ("int", "float") for version in (0, 1)
    }
    require(names == expected and len(result["Base"]) == 4, "read scope must preserve the four frozen targets")
    return names


def verify_inventory(evidence):
    kinds = ("int", "float", "string", "bool", "bytes", "uuid", "int-nullable", "float-nullable",
             "string-nullable", "bool-nullable", "bytes-nullable", "uuid-nullable",
             "string-arena", "string-nullable-arena")
    cases = {
        f"BenchmarkElementPayload/{kind}/column={column}/count={count}/version={version}-2"
        for index, kind in enumerate(kinds)
        for column in (("false", "true") if index < 6 else ("true",))
        for count in (0, 1, 128) for version in (0, 1)
    }
    require(len(cases) == 120, "expected the complete reader fixture")
    target = exact_target_names(evidence)
    hosts = set()
    for role in ROLES:
        full = read_json(evidence / f"reader-files-{role}-default.json")
        pure = read_json(evidence / f"reader-files-{role}-capsule.json")
        require(set(pure["TestGoFiles"]) == set(FIXTURE_SHA), f"unexpected fixture file in {role} capsule")
        for key in shared["PACKAGE_FIELDS"]:
            require(full.get(key) == pure.get(key), f"changed unrelated source inventory in {role}: {key}")
        require((evidence / f"reader-roots-{role}.txt").read_text().splitlines() == ["BenchmarkElementPayload"],
                f"{role} has unexpected registered benchmark, test or fuzz roots")
        hosts.add(measure_names(evidence / f"reader-all-{role}.txt", cases, one_iteration=True))
        for profile in ("default", "capsule"):
            hosts.add(measure_names(evidence / f"reader-targets-{role}-{profile}.txt", target, one_iteration=True))
    require(len(hosts) == 1, "inventory host changed")
    write_json(evidence / "reader-inventory.json", {"allPerCapsuleRole": 120, "targetsPerRole": sorted(target),
                                                   "host": list(hosts)[0]})


def code_symbols(evidence, role, path):
    text = subprocess.check_output(["go", "tool", "nm", "-size", "-sort=address", str(path)], text=True)
    result = {}
    for row in text.splitlines():
        fields = row.split(maxsplit=3)
        if len(fields) == 4 and fields[2] in ("T", "t"):
            pc, size, _, name = fields
            require(name not in result, f"duplicate text symbol: {name}")
            result[name] = (int(pc, 16), int(size))
    require(result and not any("luxo/pkg/lux/codec" in name and
                    re.search(r"AppendArrayHeader|WriteArrayHeader|SelectionMask|\.Test[A-Z]|\.Fuzz[A-Z]", name)
                    for name in result), f"{role} retained writer or other test code")
    (evidence / f"reader-{role}.nm.txt").write_text(text)
    return result


def disassembly(evidence, role, path):
    selector = "(" + "|".join(re.escape(name) for name in HOT) + ")$"
    text = subprocess.check_output(["go", "tool", "objdump", "-s", selector, str(path)], text=True)
    result = {}
    for line in text.splitlines():
        if line.startswith("TEXT "):
            name = line.split()[1].removesuffix("(SB)")
            require(name not in result, f"duplicate hot symbol: {name}")
            result[name] = []
        elif line.strip():
            _, pc, raw, asm = line.strip().split(maxsplit=3)
            result[name].append((int(pc, 16), bytes.fromhex(raw), asm))
    require(len(result) == len(HOT) and all(any(name.endswith(symbol) for name in result) for symbol in HOT),
            f"{role} is missing a watched closure")
    (evidence / f"reader-{role}.objdump.txt").write_text(text)
    return result


def elf(path):
    data = path.read_bytes()
    header = struct.unpack_from("<16sHHIQQQIHHHHHH", data)
    require(header[0][:7] == b"\x7fELF\x02\x01\x01" and header[2] == 62, "requires Linux amd64 ELF")
    start, size, count, index = header[6], *header[-3:]
    sections = [struct.unpack_from("<IIQQQQIIQQ", data, start + size*i) for i in range(count)]
    strings = sections[index]
    names = data[strings[4]:strings[4]+strings[5]]
    addressed = {names[s[0]:names.index(0,s[0])].decode(): (s[3], s[4], s[5]) for s in sections
                 if s[1] != 8 and s[2] & 2 and not s[2] & 1}
    return data, addressed


def type_shape(image, address):
    def read(address, size):
        for va, file_offset, length in image[1].values():
            displacement = address-va
            if 0 <= displacement <= length-size:
                return image[0][file_offset+displacement:file_offset+displacement+size]
        raise SystemExit("type or GC mask is outside a read-only section")
    size, ptr, h, flags, align, field_align, kind, _, gc_address, _, _ = struct.unpack(
        "<QQIBBBBQQii", read(address, 48))
    require(0 < ptr <= size and ptr % 8 == 0 and not flags & 16, "unexpected Go type ABI")
    return [size, ptr, h, flags, align, field_align, kind, read(gc_address, (ptr//8+7)//8).hex()]


def rip_target(instruction):
    pc, raw, asm = instruction
    require(len(raw) >= 5 and "(IP)" in asm, f"requires a RIP reference at {pc:#x}")
    return pc + len(raw) + struct.unpack("<i", raw[-4:])[0]


def compare_procedure(left, right, regions):
    require(len(left) == len(right) and left, "instruction count changed")
    for (pc_a, a, asm_a), (pc_b, b, asm_b) in zip(left, right):
        require(pc_a == pc_b and len(a) == len(b), "hot instruction layout changed")
        if a == b:
            continue
        require(len(a) >= 5 and a[:-4] == b[:-4] and "(IP)" in asm_a and "(IP)" in asm_b,
                f"unexpected hot opcode change at {pc_a:#x}")
        addresses = [rip_target(row) for row in ((pc_a, a, asm_a), (pc_b, b, asm_b))]
        matched = False
        for section in (".go.type", ".go.func"):
            starts, _, lengths = zip(*(region[section] for region in regions))
            offsets = [address - start for address, start in zip(addresses, starts)]
            if offsets[0] == offsets[1] and all(0 <= offset < length for offset, length in zip(offsets, lengths)):
                matched = True
                break
        require(matched, f"different hot RIP target in native ELF at {pc_a:#x}")
        yield pc_a - left[0][0], addresses


def verify_closures(evidence, directory):
    paths = [directory / f"{role}-capsule/pkg-lux-codec.test" for role in ROLES]
    signatures = [code_symbols(evidence, role, path) for role, path in zip(ROLES, paths)]
    require(signatures[0] == signatures[1], "capsule procedure layouts differ between roles")
    inputs = [elf(path) for path in paths]
    code = [disassembly(evidence, role, path) for role, path in zip(ROLES, paths)]
    require(code[0].keys() == code[1].keys(), "watched hot symbols differ")
    metadata = {
        "readColumnIntPtrBeforePayload": (38, [8, 8, 3515558699, 40, 8, 8, 22, "01"]),
        "readColumnFloatPtrBeforePayload": (38, [8, 8, 2461758045, 40, 8, 8, 22, "01"]),
        "benchmarkPayloadRead": (1053, [72, 72, 4110003562, 7, 8, 8, 25, "0101"]),
    }
    changes, type_offsets = {}, {}
    for name, left in code[0].items():
        diffs = dict(compare_procedure(left, code[1][name], [img[1] for img in inputs]))
        changes[name] = {offset: [hex(address) for address in pair] for offset, pair in diffs.items()}
        short = name.rsplit(".", 1)[-1]
        if short in metadata:
            offset, expected = metadata[short]
            targets = [rip_target(next(row for row in body if row[0] - body[0][0] == offset))
                       for body in (left, code[1][name])]
            shapes = [type_shape(image, target) for image, target in zip(inputs, targets)]
            require(all(shape == expected for shape in shapes), f"hot type layout changed in {name}")
            type_offsets[name] = {"shape": expected, "mod64": [target % 64 for target in targets]}
    write_json(evidence / "reader-closures.json", {
        "binarySHA256": [sha(p) for p in paths], "roles": ROLES,
        "sameTextSymbols": len(signatures[0]), "changedDataRelocations": changes,
        "hotTypeDescriptors": type_offsets,
        "scopeLimit": "Type relocation may change runtime placement. This is not executable-byte or timing equality.",
    })


def verify_samples(evidence):
    names = exact_target_names(evidence)
    comparisons = ("aa", "ap", "capsule-aa", "capsule-ap")
    hosts, paths, order = set(), set(), []
    for offset, group in enumerate(("primary", "confirmation")):
        for pair in range(1, 11):
            sides = ("left", "right") if (pair + offset) % 2 else ("right", "left")
            for step in range(len(comparisons)):
                comparison = comparisons[(step + pair + offset + 1) % len(comparisons)]
                for side in sides:
                    role = "candidate" if side == "right" and comparison.endswith("ap") else "base"
                    profile = "capsule" if comparison.startswith("capsule-") else "default"
                    raw = f"raw/{group}.{pair:02}.{comparison}.01.{side}.txt"
                    paths.add(raw)
                    order.append([group, f"{pair:02}", comparison, "01", side, role, profile, raw])
                    hosts.add(measure_names(evidence / raw, names))
    require({p.relative_to(evidence).as_posix() for p in (evidence / "raw").iterdir()} == paths,
            "raw leaf process inventory differs")
    with (evidence / "order.tsv").open(newline="") as handle:
        observed = list(csv.reader(handle, delimiter="\t"))
    require(observed == ["group pair comparison case side revision variant raw".split(), *order],
            "recorded process schedule differs")
    require(len(hosts) == 1, "samples used different CPU or OS")
    goos, goarch, cpu = next(iter(hosts))
    models = re.findall(r"^Model name:\s*(.+)$", (evidence / "environment.txt").read_text(), re.M)
    require((goos, goarch) == ("linux", "amd64") and len(models) == 1 and
            cpu.casefold() == models[0].strip().casefold(), "sample CPU differs from runner")
    write_json(evidence / "reader-scope.json", {"actualCPU": cpu, "expectedCPU": CPU, "samples": len(paths),
                                                "exactOriginalModel": cpu.casefold() == CPU.casefold()})


if __name__ == "__main__":
    commands = {"make-overlay": make_overlay, "verify-inventory": verify_inventory,
                "verify-closures": verify_closures, "verify-samples": verify_samples}
    commands[sys.argv[1]](*(Path(arg) for arg in sys.argv[2:]))
