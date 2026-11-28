"""Compare linked hot symbols and retain the relocation-aware instruction hashes."""
from hashlib import sha256
import json
import re


def relative_instruction(encoding, mnemonic):
    raw = bytes.fromhex(encoding)
    if raw[:1] in (b"\xe8", b"\xe9") and len(raw) == 5 and mnemonic.endswith("(SB)"):
        return [raw[:1].hex(), mnemonic]
    if raw[:2] == b"\x83\x3d" and mnemonic.startswith("CMPL runtime.writeBarrier(SB),"):
        return [(raw[:2] + raw[-1:]).hex(), mnemonic]
    if mnemonic.startswith("LEAQ ") and "(IP), " in mnemonic and len(raw) == 7:
        return [raw[:-4].hex(), mnemonic.split("(IP), ", 1)[1]]
    if mnemonic.startswith("LEAQ ") and "(SB), " in mnemonic and len(raw) == 7:
        return [raw[:-4].hex(), mnemonic]
    return [encoding]


def linked_code(directory, case):
    symbols = {}
    for row in (directory / "symbols.txt").read_text().splitlines():
        address, size, kind, name = row.split(maxsplit=3)
        if kind in ("T", "t") and int(size) > 0:
            if name in symbols:
                raise ValueError(f"duplicate symbol {name}")
            symbols[name] = int(address, 16), int(size)

    required = {name for name in symbols for pattern in case["required"] if re.search(pattern, name)}
    if not all(any(re.search(pattern, name) for name in required) for pattern in case["required"]):
        raise ValueError("missing benchmark or production hot symbol")

    result = {}
    for section in (directory / "hot.txt").read_text().split("TEXT ")[1:]:
        title, *rows = section.splitlines()
        name, separator, _ = title.partition("(SB) ")
        if not separator:
            raise ValueError("malformed objdump text header")
        if name not in symbols:
            continue
        if name in result:
            raise ValueError(f"duplicate hot disassembly {name}")
        start, size = symbols[name]
        instructions = []
        for line in filter(str.strip, rows):
            _, address, encoding, instruction = line.strip().split(maxsplit=3)
            instructions.append([int(address, 16) - start, *relative_instruction(encoding, instruction.strip())])
        if not instructions or instructions[0][0] != 0 or instructions[-1][0] >= size:
            raise ValueError(f"invalid hot disassembly {name}")
        digest = sha256(json.dumps(instructions, separators=(",", ":")).encode()).hexdigest()
        result[name] = {"entry": start, "mod64": start % 64, "size": size, "normalizedSHA256": digest}

    if not required <= result.keys():
        raise ValueError("missing required disassembly")
    return result, required


def compare(directory, case):
    flags = "\tbuild\t-ldflags=-funcalign=64"
    report = {"arms": {}, "linkOnly": True}
    for role in ("A", "H"):
        stock, aligned = (directory / mode / role for mode in ("stock", "aligned"))
        original, shifted = (json.loads((arm / "elf.json").read_bytes()) for arm in (stock, aligned))
        base_build, aligned_build = ((arm / "buildinfo.txt").read_text().splitlines()[1:]
                                     for arm in (stock, aligned))
        before, required_before = linked_code(stock, case)
        after, required_after = linked_code(aligned, case)
        same_flags = aligned_build.count(flags) == 1 and [line for line in aligned_build if line != flags] == base_build
        same_required = required_before == required_after
        shape = {name: before[name]["normalizedSHA256"] == after[name]["normalizedSHA256"]
                 for name in sorted(before.keys() & after.keys())}
        moved = original["text"] != shifted["text"]
        verified = same_flags and same_required and moved and all(
            after[name]["mod64"] == 0 and shape[name] for name in required_before)
        report["linkOnly"] = report["linkOnly"] and verified
        report["arms"][role] = {"buildFlagsVerified": same_flags, "changedExecutableText": moved,
                                "required": sorted(required_before | required_after),
                                "sameNormalizedSymbols": shape, "stock": before, "aligned": after}

    a, h = (report["arms"][role]["stock"] for role in ("A", "H"))
    report["sameStockHotInstructions"] = {name: a[name]["normalizedSHA256"] == h[name]["normalizedSHA256"]
                                           for name in sorted(a.keys() & h.keys())}
    return report
