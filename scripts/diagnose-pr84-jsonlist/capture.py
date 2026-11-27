"""Record native branch addresses and the bytes referenced by the observed RIP load."""

from hashlib import sha256
import re


def disassembly(path):
    result = {}
    for section in path.read_text().split("TEXT ")[1:]:
        header, *lines = section.splitlines()
        symbol, separator, _ = header.partition("(SB) ")
        if not separator or symbol in result:
            raise ValueError("invalid hot symbol list")
        rows = []
        for line in filter(str.strip, lines):
            location, address, encoding, mnemonic = line.split(maxsplit=3)
            rows.append((int(address, 16), location, bytes.fromhex(encoding), mnemonic.strip()))
        if not rows:
            raise ValueError(f"missing disassembly for {symbol}")
        result[symbol] = rows
    return result


def branch_layout(rows):
    branch = re.compile(r"^(J[A-Z]+) (0x[0-9a-fA-F]+)$")
    entry = rows[0][0]
    locations = {pc for pc, _, _, _ in rows}
    result = []
    for pc, location, data, mnemonic in rows:
        if (match := branch.fullmatch(mnemonic)) is None:
            continue
        destination = int(match.group(2), 16)
        if destination in locations:
            result.append({"source": location, "branch": match.group(1), "pc": pc, "target": destination,
                           "pcMod64": pc%64, "targetMod64": destination%64,
                           "offset": pc-entry, "targetOffset": destination-entry,
                           "fallthroughMod64": (pc+len(data))%64, "backedge": destination<pc})
    return result


def type_location(readelf, target):
    for line in readelf.splitlines():
        if not re.match(r" *\[ *\d+\]", line):
            continue
        name, kind, address, offset, size, *_ = line.split("]", 1)[1].split()
        if name != ".go.type":
            continue
        start, file_offset, length = (int(value, 16) for value in (address, offset, size))
        if kind != "PROGBITS" or not start <= target <= start+length-4:
            break
        return start, file_offset, length
    raise ValueError("RIP target is outside the exact .go.type section")


def type_read(binary, readelf, rows):
    candidates = [(pc, data) for pc, location, data, mnemonic in rows
                  if location == "io.go:683" and len(data) == 6 and data[:2] == b"\x8b\x35" and
                  re.fullmatch(r"MOVL 0x[0-9a-fA-F]+\(IP\), SI", mnemonic)]
    if len(candidates) != 1:
        raise ValueError("expected one signed io.NopCloser type load")
    pc, data = candidates[0]
    target = pc + len(data) + int.from_bytes(data[2:], "little", signed=True)
    start, offset, size = type_location(readelf, target)
    with binary.open("rb") as handle:
        handle.seek(offset)
        section = handle.read(size)
    if len(section) != size:
        raise ValueError("truncated .go.type section")
    relative = target-start
    return {"pc": pc, "target": target, "targetMod64": target%64,
            "sectionOffset": relative, "code": data.hex(), "targetBytes": section[relative:relative+4].hex(),
            "goTypeSHA256": sha256(section).hexdigest()}


def inspect(binary, evidence):
    functions = disassembly(evidence/"hot.txt")
    request_fixture = [rows for name, rows in functions.items() if name.endswith(".benchmarkListMetadata.func2")]
    if len(request_fixture) != 1:
        raise ValueError("required JSONList request fixture symbol missing")
    type_reference = type_read(binary, (evidence/"sections.txt").read_text(), request_fixture[0])
    branches = {name: {"entry": rows[0][0], "entryMod64": rows[0][0]%64, "branches": branch_layout(rows)}
                for name, rows in functions.items()}
    filter_loop = [name for name in branches if "parseListObjects[" in name and "Operator string" in name
                   and name.endswith("]")]
    if len(filter_loop) != 1 or not branches[filter_loop[0]]["branches"]:
        raise ValueError("missing native Filter list branch evidence")
    for key in ("benchmarkListMetadata.func1", "ReadToken", "PeekKind"):
        if not any(key in name and value["branches"] for name, value in branches.items()):
            raise ValueError(f"missing native branch evidence for {key}")
    return {"numericTypeRead": type_reference, "functions": branches}
