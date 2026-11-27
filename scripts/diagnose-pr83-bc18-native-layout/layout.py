"""Compare linked hot symbols and retain the relocation-aware instruction hashes."""
from hashlib import sha256
import json
import re
import shlex
from pathlib import Path


STOCK = {
    "A": {"text": "d109be5798579f5e9dfe9d6e3e33ad96582ad194b53f88ada46acb907bef1a86",
          "rodata": "d1a8b7a790ee2f0575cf5f945bd8b774cdb3936867c30efd88fdbbb4bfc2c1f4"},
    "H": {"text": "6bf66aa9d91534494ab617cf38266b6b81157b8580b0ffe303d09dae8d39145f",
          "rodata": "c140fa16dcfbecbd014dd32704b383f41e94882871fbc0739d9e27a4b3618000"},
}


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
    if (len(raw) == 7 and raw[0] & 0xf8 == 0x48 and raw[1] == 0x8b and raw[2] & 0xc7 == 0x05 and
            re.fullmatch(r"MOVQ \S+\(SB\), [A-Z0-9]+", mnemonic)):
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


def link_invocation(trace, tool, executable, aligned):
    commands = []
    with trace.open() as stream:
        for raw in stream:
            if str(tool) not in raw:
                continue
            args = shlex.split(raw)
            if args[:1] == ["GOROOT="]:
                args.pop(0)
            if args[:1] == [str(tool)]:
                commands.append(args)
    if len(commands) != 1:
        raise ValueError("expected one actual benchmark test linker invocation")
    args, = commands
    recorded = {"args": args, "testName": executable}
    validate_link_command(recorded, aligned)
    return recorded


def validate_link_command(recorded, aligned):
    argv = recorded["args"]
    if not argv or not Path(argv[0]).is_absolute() or Path(argv[0]).name != "link":
        raise ValueError("unexpected linker executable")
    if argv.count("-o") != 1 or argv.count("-importcfg") != 1 or "testing.testBinary=1" not in argv:
        raise ValueError("linker did not build a Go test binary")
    if Path(argv[argv.index("-o") + 1]).name != recorded["testName"]:
        raise ValueError("wrong linked test binary")
    if Path(argv[argv.index("-importcfg") + 1]).name != "importcfg.link":
        raise ValueError("unexpected Go link import configuration")
    archive = Path(argv[-1]).name
    if archive != "_pkg_.a" and not re.fullmatch(r"[0-9a-f]{64}-d", archive):
        raise ValueError("unexpected Go test link archive")
    actual = [x for x in argv if x.startswith(("-funcalign", "--funcalign"))]
    if actual != (["-funcalign=64"] if aligned else []):
        raise ValueError("unexpected actual linker alignment")


def normalized_link_command(recorded, aligned):
    validate_link_command(recorded, aligned)
    argv = recorded["args"]
    volatile = {argv.index("-o") + 1, argv.index("-importcfg") + 1, len(argv) - 1}
    return [arg for i, arg in enumerate(argv) if i not in volatile and
            not arg.startswith("-buildid=") and arg != "-funcalign=64"]


def compare(directory, case, *, cache):
    report = {"arms": {}, "linkOnly": True}
    if cache:
        report["oldCacheStockELFMatch"] = {}
    for role in ("A", "H"):
        stock, aligned = (directory / mode / role for mode in ("stock", "aligned"))
        original, shifted = (json.loads((arm / "elf.json").read_bytes()) for arm in (stock, aligned))
        base_build, aligned_build = ((arm / "buildinfo.txt").read_text().splitlines()[1:]
                                     for arm in (stock, aligned))
        before, required_before = linked_code(stock, case)
        after, required_after = linked_code(aligned, case)
        stock_link, aligned_link = (json.loads((arm/"link-command.json").read_bytes()) for arm in (stock, aligned))
        same_flags = (base_build == aligned_build and
                      normalized_link_command(stock_link, False) == normalized_link_command(aligned_link, True))
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
        if cache:
            report["oldCacheStockELFMatch"][role] = all(original[key] == digest for key, digest in STOCK[role].items())

    a, h = (report["arms"][role]["stock"] for role in ("A", "H"))
    report["sameStockHotInstructions"] = {name: a[name]["normalizedSHA256"] == h[name]["normalizedSHA256"]
                                           for name in sorted(a.keys() & h.keys())}
    return report
