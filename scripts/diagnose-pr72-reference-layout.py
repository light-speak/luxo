from bisect import bisect_right
from collections import defaultdict
from dataclasses import dataclass
from hashlib import sha256
from pathlib import Path
from struct import pack
import json
import re
import subprocess
import sys

PRIOR = {
    "run": 45357102586,
    "attempt": 1,
    "artifactSHA256": "63ef3947cfd8385a03802e9a5b0d9d5a4f8fee2791bcf8dccf6f901caacc450e",
    "frozenContract": "7db53853739ac7ac7a73675f954d3f3cf83344c9585964d68a954b3d525d4756",
    "binaries": {
        "base": "95c0d9f5090939c60ef8e3d12653d1ec2a0f8323913a2f474c6fec5d41290b04",
        "head": "5b95013c44327bb6b293922f4413808bfdc6ea900c10be9d5fe6768803f7bf7d",
        "candidate": "8b3999dc0bd089a1a37a313b79462b17566623c13bbabe24b0197fa35bd9dd0c",
    },
}
CODEC = "github.com/light-speak/luxo/pkg/lux/codec."
SELECT = re.escape(CODEC) + (
    r"(?:(?:BenchmarkElementPayload|benchmarkPayloadRead|payloadReaderCases(?:Before)?)(?:\.func.*)?"
    r"|Read(?:Nullable|Fixed64|Svarint|Uvarint)"
    r"|\(\*ColumnarReader\)\.(?:readColumn(?:Int|Float)PtrBeforePayload|ReadColumn(?:Int|Float)Ptr))$"
)
REQUIRED = [CODEC + name for name in (
    "benchmarkPayloadRead",
    "payloadReaderCasesBefore.func7", "payloadReaderCasesBefore.func8",
    "(*ColumnarReader).readColumnIntPtrBeforePayload",
    "(*ColumnarReader).readColumnFloatPtrBeforePayload",
    "(*ColumnarReader).ReadColumnIntPtr", "(*ColumnarReader).ReadColumnFloatPtr",
)]
INSTRUCTION = re.compile(r"^\s+\S+\s+0x([0-9a-f]+)\s+([0-9a-f]+)\s+(.+?)\s*$")
IP_MEMORY = re.compile(r"(?P<offset>[-+]?(?:0x[0-9a-f]+|[0-9]+))\((?:IP|RIP)\)")
NAMED_MEMORY = re.compile(r"(?P<name>[^\s,]+)\(SB\)")


@dataclass(frozen=True)
class Instruction:
    address: int
    raw: bytes
    asm: str


class Symbols:
    def __init__(self, text):
        self.by_address = defaultdict(list)
        self.text_sizes = {}
        self.data_addresses = defaultdict(set)
        for line in text.splitlines():
            fields = line.split(maxsplit=3)
            if len(fields) != 4:
                continue
            address, size, kind, name = fields
            if re.fullmatch("[0-9a-f]+", address) and size.isdecimal():
                address, size = int(address, 16), int(size)
                self.by_address[address].append((name, size, kind))
                if kind in ("T", "t"):
                    self.text_sizes[(address, name)] = size
                else:
                    self.data_addresses[name].add(address)
        self.starts = sorted(self.by_address)

    def resolve(self, address, text):
        index = bisect_right(self.starts, address) - 1
        if index < 0:
            return None
        start = self.starts[index]
        offset = address - start
        names = sorted(name for name, size, kind in self.by_address[start]
                       if (kind in ("T", "t")) == text
                       and (offset < size or (offset == 0 and size == 0)))
        if len(names) == 1:
            return f"{names[0]}+0x{offset:x}"
        return None


def direct_relative(ins):
    # Preserve original opcode/encoding when only the displacement is relocated.
    op = ins.asm.split()[0]
    if op != "CALL" and not op.startswith(("J", "LOOP")):
        return None
    raw = ins.raw
    first = raw[0]
    if len(raw) == 5 and first in (0xE8, 0xE9):
        width = 4
    elif len(raw) == 6 and raw[0] == 0x0F and 0x80 <= raw[1] <= 0x8F:
        width = 4
    elif len(raw) == 2 and (0x70 <= first <= 0x7F or first in (0xE0, 0xE1, 0xE2, 0xE3, 0xEB)):
        width = 1
    else:
        return None
    prefix = raw[:-width]
    address = ins.address + len(raw) + int.from_bytes(raw[-width:], "little", signed=True)
    return address, prefix.hex(), width


def normalized(ins, start, offsets, symbols):
    original = (ins.raw.hex(), ins.asm)
    op, _, operand = ins.asm.partition(" ")
    branch = direct_relative(ins)
    if branch is not None:
        address, encoding, width = branch
        if re.fullmatch(r"0x[0-9a-f]+", operand) and int(operand, 16) != address:
            raise ValueError(f"relocated operand differs from machine code at {ins.address:x}")
        offset = address - start
        target = f"local+0x{offset:x}" if offset in offsets else symbols.resolve(address, text=True)
        if target is None:
            return original, ("unresolved-direct", address)
        return (f"{encoding}[rel{8 * width}]", f"{op} {target}"), ("direct", address, target)
    if (op == "CALL" or op.startswith(("J", "LOOP"))) and re.fullmatch(r"0x[0-9a-f]+", operand):
        return original, ("unresolved-branch-encoding",)
    if any(op in ins.asm for op in ("(IP)", "(RIP)", "(SB)")):
        return normalize_memory(ins, symbols)
    if ins.asm.startswith(("CALL ", "JMP ")):
        return original, ("indirect",)
    return original, None


def normalize_memory(ins, symbols):
    original = (ins.raw.hex(), ins.asm)
    explicit = list(IP_MEMORY.finditer(ins.asm))
    symbolic = list(NAMED_MEMORY.finditer(ins.asm))
    next_address = ins.address + len(ins.raw)
    if len(explicit) == 1 and not symbolic:
        ref = explicit[0]
        address = next_address + int(ref["offset"], 0)
    elif len(symbolic) == 1 and not explicit:
        ref = symbolic[0]
        addresses = symbols.data_addresses.get(ref["name"], set())
        if len(addresses) != 1:
            return original, ("unresolved-address-operand",)
        address = next(iter(addresses))
    else:
        return original, ("unresolved-address-operand",)
    target = symbols.resolve(address, text=False)
    displacement = address - next_address
    if target is not None and -(1 << 31) <= displacement < (1 << 31):
        pattern = pack("<i", displacement)
        # RIP-relative addressing has ModRM mod=00, r/m=101 immediately before disp32.
        matches = [i for i in range(1, len(ins.raw)-3)
                   if ins.raw[i-1] & 0xC7 == 5 and ins.raw[i:i+4] == pattern]
        if len(matches) == 1:
            pos = matches[0]
            encoding = ins.raw[:pos].hex() + "[rip32]" + ins.raw[pos+4:].hex()
            operand = ins.asm[:ref.start()] + f"{target}(IP)" + ins.asm[ref.end():]
            return (encoding, operand), ("data", address, target)
    return original, ("unresolved-rip", address)


def parse_disassembly(text):
    blocks = {}
    current = None
    for line in text.splitlines():
        if not line.strip():
            continue
        if line.startswith("TEXT "):
            name = line.split()[1].removesuffix("(SB)")
            if name in blocks:
                raise ValueError(f"duplicate disassembly symbol: {name}")
            current = blocks[name] = []
            continue
        instr = INSTRUCTION.fullmatch(line)
        if instr is None or current is None:
            raise ValueError(f"unparsed disassembly line: {line}")
        address, raw, asm = instr.groups()
        current.append(Instruction(int(address, 16), bytes.fromhex(raw), asm))
    if not blocks or any(not block for block in blocks.values()):
        raise ValueError("empty disassembly")
    return blocks


def layout(address):
    return {"address": hex(address), "mod16": address % 16, "mod32": address % 32,
            "mod64": address % 64, "mod4096": address % 4096}


def inspect_function(name, code, symbols):
    start = code[0].address
    size = symbols.text_sizes[(start, name)]
    offsets = {ins.address - start for ins in code}
    if len(offsets) != len(code) or code[-1].address + len(code[-1].raw) != start + size:
        raise ValueError(f"incomplete symbol size: {name}")
    for left, right in zip(code, code[1:]):
        if left.address + len(left.raw) != right.address:
            raise ValueError(f"incomplete instruction stream: {name}")
    instructions, edges, unresolved = [], [], []
    for ins in code:
        row, ref = normalized(ins, start, offsets, symbols)
        offset = ins.address - start
        instructions.append([offset, *row])
        if ref is not None:
            entry = {"from": layout(ins.address), "offset": offset, "kind": ref[0]}
            if len(ref) > 1:
                entry["to"] = layout(ref[1])
            if len(ref) > 2:
                entry["target"] = ref[2]
                entry["localBackedge"] = ref[2].startswith("local+") and ref[1] < ins.address
            (unresolved if ref[0].startswith("unresolved") else edges).append(entry)
    return {"entry": layout(start), "size": size, "instructions": instructions, "edges": edges,
            "unresolvedReferences": unresolved}


def output_of(*cmd):
    return subprocess.check_output(cmd, text=True)


def inspect_role(binary, evidence, role):
    nm = output_of("go", "tool", "nm", "-size", "-sort", "address", binary)
    disassembly = output_of("go", "tool", "objdump", "-s", SELECT, binary)
    (evidence / f"{role}.nm.txt").write_text(nm)
    (evidence / f"{role}.objdump.txt").write_text(disassembly)
    (evidence / f"{role}.elf.txt").write_text(output_of("readelf", "-h", "-lW", binary))
    symbols = Symbols(nm)
    observed = parse_disassembly(disassembly)
    for name in REQUIRED:
        if name not in observed:
            raise SystemExit(f"missing compiled hot symbol {role}: {name}")
    return {name: inspect_function(name, block, symbols) for name, block in observed.items()}


def fixture_contract(path):
    receipt = json.loads(path.read_text())
    fields = ("Physical", "Baseline", "HeadNative", "FixtureSHA", "DataSHA")
    obj = {key: receipt[key] for key in ("Logical", "GoEnvironment", "Complete")}
    obj["Packages"] = [{k: pkg[k] for k in fields} for pkg in receipt["Packages"]]
    digest = sha256(json.dumps(obj, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    return digest, receipt["Packages"][0]["BinarySHA"]


def bind(evidence, binaries):
    contracts = {}
    actual = {role: sha256(binary.read_bytes()).hexdigest() for role, binary in binaries.items()}
    for role in ("head", "candidate"):
        name = "pkg-lux-codec" + (".candidate" if role == "candidate" else "") + ".json"
        digest, measured = fixture_contract(evidence / "pairs" / name)
        contracts[role] = {"fixtureContract": digest, "reportedELF": measured}
        if measured != [actual["base"], actual[role]]:
            raise SystemExit(f"compiled artifact differs from its build manifest: {role}")
    binding = {"prior": PRIOR, "actualBinaries": actual, "actualContracts": contracts}
    (evidence / "prior-artifact-binding.json").write_text(json.dumps(binding, indent=2)+"\n")
    if actual != PRIOR["binaries"] or any(row["fixtureContract"] != PRIOR["frozenContract"] for row in contracts.values()):
        raise SystemExit("native reconstruction differs from the 45357102586 artifact; layout inference withheld")


def compare_functions(a, b):
    left = {row[0]: row[1:] for row in a["instructions"]}
    right = {row[0]: row[1:] for row in b["instructions"]}
    changed = [offset for offset in sorted(left.keys() | right.keys()) if left.get(offset) != right.get(offset)]
    unresolved = a["unresolvedReferences"] + b["unresolvedReferences"]
    unknown_offsets = {edge["offset"] for edge in unresolved}
    edges = [
        [(edge["offset"], edge["target"]) for edge in side["edges"] if edge["kind"] == "direct"]
        for side in (a,b)
    ]
    return {
        "normalizedIdentical": not changed,
        "unresolvedCount": len(unresolved),
        "resolvedInstructionMatch": not changed and not unresolved,
        "resolvedDirectEdgesIdentical": edges[0] == edges[1],
        "unresolvedControlFlow": sum(edge["kind"] in ("unresolved-direct", "unresolved-branch-encoding")
                                     for edge in unresolved),
        "differences": [{"offset": offset, "left": left.get(offset), "right": right.get(offset),
                         "containsUnresolvedReference": offset in unknown_offsets} for offset in changed],
        "indirectSites": sum(edge["kind"] == "indirect" for side in (a,b) for edge in side["edges"]),
    }


def compare(evidence, roles):
    comparisons = {}
    for left, right in (("base", "head"), ("base", "candidate"), ("head", "candidate")):
        data = {}
        for name in sorted(roles[left].keys() | roles[right].keys()):
            a, b = roles[left].get(name), roles[right].get(name)
            if a is None or b is None:
                data[name] = {"missing": left if a is None else right}
                continue
            data[name] = compare_functions(a,b)
            data[name]["entry"] = {left: a["entry"], right: b["entry"]}
        comparisons[left + "-" + right] = data
    (evidence / "layout-comparison.json").write_text(json.dumps(comparisons, indent=2)+"\n")


def self_test():
    a, b = Symbols("100 4 T target\n300 8 R stableData"), Symbols("200 4 T target\n400 8 R stableData")
    old = Instruction(0x10, bytes.fromhex("e8eb000000"), "CALL target(SB)")
    new = Instruction(0x20, bytes.fromhex("e8db010000"), "CALL target(SB)")
    assert normalized(old, 0x10, {0}, a)[0] == normalized(new, 0x20, {0}, b)[0]
    change = Symbols("200 4 T other")
    assert normalized(old, 0x10, {0}, a)[0] != normalized(new, 0x20, {0}, change)[0]
    loop = Instruction(0x50, bytes.fromhex("75ee"), "JNE 0x40")
    assert normalized(loop, 0x40, {0,16}, a)[1] == ("direct", 0x40, "local+0x0")
    unknown = Instruction(0x50, bytes.fromhex("75ee"), "JNE 0x40")
    assert normalized(unknown, 0x50, {0}, a)[1] == ("unresolved-direct", 0x40)
    rip = Instruction(0x100, bytes.fromhex("488d05f9010000"), "LEAQ 0x1f9(IP), AX")
    rw, ref = normalized(rip, 0x100, {0}, a)
    assert ref == ("data", 0x300, "stableData+0x0") and "[rip32]" in rw[0]
    assert normalized(rip, 0x100, {0}, Symbols(""))[1] == ("unresolved-rip", 0x300)
    named = Instruction(rip.address, rip.raw, "LEAQ stableData(SB), AX")
    assert normalized(named, 0x100, {0}, a)[0] == rw
    non_rip = Instruction(rip.address, bytes.fromhex("488d04f9010000"), rip.asm)
    assert normalized(non_rip, 0x100, {0}, a)[1] == ("unresolved-rip", 0x300)
    indirect = Instruction(0x100, bytes.fromhex("ffd0"), "CALL AX")
    assert normalized(indirect, 0x100, {0}, a)[1] == ("indirect",)
    prefixed = Instruction(0x50, bytes.fromhex("2e7500"), "JNE 0x53")
    assert normalized(prefixed, 0x50, {0}, a)[1] == ("unresolved-branch-encoding",)
    print("relocated call, different target, local branch, resolved/unresolved RIP, indirect, and unsupported encoding controls passed")


if __name__ == "__main__":
    if sys.argv[1:] == ["--self-test"]:
        self_test()
    else:
        evidence, directory = map(Path, sys.argv[1:])
        binaries = {role: directory / role / "pkg-lux-codec.test" for role in PRIOR["binaries"]}
        bind(evidence, binaries)
        roles = {role: inspect_role(binary, evidence, role) for role, binary in binaries.items()}
        (evidence / "normalized-instructions.json").write_text(json.dumps(roles, indent=2)+"\n")
        compare(evidence, roles)
