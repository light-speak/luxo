"""Verify source-bound #74 benchmark plans and loaded native ELF contents."""

from collections import Counter
import csv
import hashlib
import json
import math
from pathlib import Path
import struct
import sys
from zipfile import ZipFile

BASE = "a2c411b7b83ca233881ace0c5bf3bec0b9646e1d"
HEAD = "a1faf2f6d2aa4b733ee09959387ff433917d98b0"
TESTED = "c50446aaf51d439a105ce674bdddd93407270a5d"
API = "pkg/lux/api"
LUVIA = "pkg/lux/luvia"
CASES = {
    "9v45": (
        "AMD EPYC 9V45 96-Core Processor", "05", 14957031828, "4ba02e6851b6a3cdf60f0960201774adb34edfb839c885ff6123ff0608c0a349",
        (
            (API, "api-non-identity", ("JSONListMetadata/filters/0/request",), ("JSONListMetadata/filters/0/metadata",)),
            (LUVIA, "default", ("HeartbeatEncodingComparison/0/current",), ("HeartbeatEncodingComparison/0/previous",)),
        ),
    ),
    "9v74": (
        "AMD EPYC 9V74 80-Core Processor", "06", 14956143248, "5e5dc2e28cd0f9db902c0a2d512d3c9ca66a4f214645920a438ecdbb68ebd693",
        (
            (API, "api-non-identity", ("BinarySelection/width=1/selected=1/branches=2/request",
                                        "BinarySelection/width=1/selected=1/branches=2/tree"), ()),
        ),
    ),
    "8573c": (
        "INTEL(R) XEON(R) PLATINUM 8573C", "07", 14957157120, "91de5ae838b9a7571b174de5afda30cece07f5df9e870c46ec952ef311914ef4",
        (
            (LUVIA, "default", ("MergeDestination/columnar=true/rows=1/direct=true",),
                               ("MergeDestination/columnar=true/rows=1/direct=false",)),
        ),
    ),
}
ROLES = {"aa": ("base", "base"), "ah": ("base", "head"), "aj": ("base", "auth-only"), "hj": ("head", "auth-only")}
FIELDS = ("group", "pair", "case", "package", "profile", "comparison", "side", "role", "filter", "raw")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def names(paths):
    return sorted(f"Benchmark{path}-2" for path in paths)


def batch_filter(leaves):
    return "|".join("/".join(f"^{part}$" for part in leaf[:-2].split("/")) for leaf in leaves)


def schedule(cases):
    for offset, group in enumerate(("primary", "confirmation")):
        for pair in range(1, 11):
            rotation = pair + offset
            for slot in range(len(cases)):
                index = (slot + rotation) % len(cases)
                pkg, profile, failed, controls = cases[index]
                comparisons = tuple(ROLES) if pkg == LUVIA else ("aa", "ah")
                pattern = batch_filter(names(failed + controls))
                for position in range(len(comparisons)):
                    comparison = comparisons[(position + rotation) % len(comparisons)]
                    sides = tuple(zip(("left", "right"), ROLES[comparison]))
                    if rotation % 2 == 0:
                        sides = sides[::-1]
                    for side, role in sides:
                        raw = f"raw/{index}.{comparison}.{group}.{pair:02}.{side}.txt"
                        yield (group, f"{pair:02}", str(index), pkg, profile, comparison, side, role, pattern, raw)


def measurements(text, pkg, cpu, expected, count, *, formal=False):
    values = {name: [] for name in expected}
    headers = {key: set() for key in ("goos", "goarch", "cpu", "pkg")}
    active_package = ""
    for line in text.splitlines():
        key, sep, value = line.partition(":")
        if sep and key in headers:
            headers[key].add(value.strip())
            if key == "pkg":
                active_package = value.strip()
        if not line.startswith("Benchmark"):
            continue
        leaf, *fields = line.split()
        if formal and (leaf not in values or active_package != pkg):
            continue
        require(leaf in values and active_package == pkg, f"unexpected benchmark {active_package} {leaf}")
        metrics = dict(zip(fields[2::2], fields[1::2]))
        require(set(metrics) >= {"ns/op", "B/op", "allocs/op"}, f"missing units for {leaf}")
        delta = float(metrics["ns/op"])
        allocated, allocations = int(metrics["B/op"]), int(metrics["allocs/op"])
        require(int(fields[0]) > 0 and math.isfinite(delta) and delta > 0 and min(allocated, allocations) >= 0,
                f"invalid measurements for {leaf}")
        values[leaf].append((delta, allocated, allocations))
    for key, value in (("goos", "linux"), ("goarch", "amd64"), ("cpu", cpu)):
        require(headers[key] == {value}, f"unexpected {key}: {sorted(headers[key])}")
    require(pkg in headers["pkg"] and (formal or headers["pkg"] == {pkg}), "unexpected package")
    require(all(len(samples) == count for samples in values.values()), "unexpected measurement counts")
    return values


def plan(scope, archive, output):
    cpu, shard, artifact, digest, cases = CASES[scope]
    require(sha(archive.read_bytes()) == digest, "original artifact SHA-256 mismatch")
    raw = {}
    with ZipFile(archive) as bundle:
        require(len(bundle.namelist()) == len(set(bundle.namelist())) and bundle.testzip() is None, "invalid formal ZIP")
        def read(n):
            return bundle.read(n).decode()
        require(read("benchmark-revisions.txt") == f"base_sha={BASE}\ntested_sha={TESTED}\n", "formal revisions changed")
        original_targets = set(read("benchmark-confirmation-targets.tsv").splitlines())
        planned_profiles = {tuple(row.split("\t")[:2]) for row in read("benchmark-confirmation-batch.tsv").splitlines()}
        for index, (pkg, profile, failed, controls) in enumerate(cases):
            selected, required = names(failed + controls), names(failed)
            require((pkg, profile) in planned_profiles, "unexpected formal compile profile")
            require({f"{pkg}\t{leaf}" for leaf in required} <= original_targets, "missing original confirmation target")
            keys = [{"Package": pkg, "Name": name} for name in selected]
            (output/f"{index}.expected.json").write_text(json.dumps({"Base": keys, "Head": keys})+"\n")
            for group, reference, expected in (("primary", "benchmark-expected.json", selected),
                                               ("confirmation", "benchmark-confirmation-expected.json", required)):
                expected_config = json.loads(read(reference))
                for role in ("base", "head"):
                    inventory = Counter(row["Name"] for row in expected_config[role.title()] if row["Package"] == pkg)
                    require(all(inventory[name] == 1 for name in expected), "missing or duplicated original leaf")
                    raw[f'{index}/{group}/{role}'] = measurements(
                        read(f"benchmark-raw-{group}-{role}.txt"), f"github.com/light-speak/luxo/{pkg}", cpu, expected, 10, formal=True)
        for filename in ("benchmark-compile-profile.txt", "benchmark-confirmation-targets.tsv",
                         "benchmark-confirmation-batch.tsv", "benchstat-primary.csv", "benchstat-confirmation.csv"):
            (output/f"formal-{filename}").write_bytes(bundle.read(filename))
    (output/"formal.json").write_text(json.dumps({"artifact": artifact, "sha256": digest, "shard": shard,
                                                "run": 45466757941, "cpu": cpu, "selectedRaw": raw}, indent=2)+"\n")
    with (output/"planned-order.tsv").open("w", newline="") as handle:
        writer = csv.writer(handle, delimiter="\t", lineterminator="\n")
        writer.writerow(FIELDS)
        writer.writerows(schedule(cases))
    (output/"cases.tsv").write_text("".join(f"{i}\t{pkg}\t{profile}\t{batch_filter(names(failed + controls))}\n"
                                         for i, (pkg, profile, failed, controls) in enumerate(cases)))


def audit(scope, output):
    cpu, _, _, _, cases = CASES[scope]
    with (output/"order.tsv").open(newline="") as handle:
        rows = list(csv.reader(handle, delimiter="\t"))
    require(rows == [list(FIELDS)] + list(map(list, schedule(cases))), "paired order, sources or profiles changed")
    files = {arm[-1] for arm in rows[1:]}
    require(files == {f.relative_to(output).as_posix() for f in (output/"raw").iterdir()} and len(files) == len(rows)-1,
            "raw files are missing or duplicated")
    count = 0
    for _, _, index, pkg, _, _, _, _, _, filename in rows[1:]:
        raw = (output/filename).read_text()
        require(raw.splitlines()[-1] == "PASS", f"uncompleted benchmark process {filename}")
        _, _, failed, controls = cases[int(index)]
        leaves = names(failed + controls)
        measurements(raw, f"github.com/light-speak/luxo/{pkg}", cpu, leaves, 1)
        count += len(leaves)
    print(json.dumps({"measurementArms": len(files), "benchmarkRows": count, "cpu": cpu}))


def confirmation(source, expected, target):
    keys = json.loads(expected.read_text())
    require(keys["Base"] == keys["Head"] and keys["Base"], "unexpected targeted inventory")
    selected = {row["Name"] for row in keys["Base"]}
    target.write_text("".join(line for line in source.read_text().splitlines(keepends=True)
                              if not line.startswith("Benchmark") or line.split()[0] in selected))


def elf(image):
    binary = image.read_bytes()
    header = struct.unpack_from("<16sHHIQQQIHHHHHH", binary)
    ident, etype, machine, version, entry, phoff, shoff, flags, hsz, phsz, phnum, shsz, shnum, namesidx = header
    require(ident[:7] == b"\x7fELF\x02\x01\x01" and machine == 62 and version == 1 and etype in (2, 3),
            f"expected Linux/amd64 ELF executable: {image}")
    require(hsz == 64 and phsz == 56 and shsz == 64 and 0 < namesidx < shnum and
            phoff+phsz*phnum <= len(binary) and shoff+shsz*shnum <= len(binary), "invalid ELF tables")
    sections = [struct.unpack_from("<IIQQQQIIQQ", binary, shoff+i*shsz) for i in range(shnum)]
    names_off, names_size = sections[namesidx][4:6]
    require(names_off+names_size <= len(binary), "invalid ELF section names")
    string_table = binary[names_off:names_off+names_size]
    masked = bytearray(binary)
    alloc = {}
    all_sections = {}
    build_id = None
    for name_idx, typ, bits, address, offset, size, link, info, align, entsize in sections:
        stop = string_table.find(b"\0", name_idx)
        require(stop >= name_idx and (typ == 8 or offset+size <= len(binary)), "invalid ELF section")
        name = string_table[name_idx:stop].decode()
        require(name not in all_sections, f"duplicate ELF section {name}")
        content = binary[offset:offset+size] if typ != 8 else b""
        if name == ".note.go.buildid":
            namesz, descsz, note_type = struct.unpack_from("<III", content)
            require(typ == 7 and bits & 2 and namesz == 4 and content[12:16] == b"Go\0\0" and note_type == 4
                    and descsz > 0 and 16 + ((descsz + 3) & ~3) == size, "invalid Go build-ID note")
            build_id = content[16:16+descsz].decode("ascii")
            masked[offset+16:offset+16+descsz] = bytes(descsz)
        item = (typ, bits, address, offset, size, link, info, align, entsize, sha(content))
        all_sections[name] = item
        if bits & 2:
            alloc[name] = (*item[:-1], sha(masked[offset:offset+size])) if name == ".note.go.buildid" else item
    program = [struct.unpack_from("<IIQQQQQQ", binary, phoff+i*phsz) for i in range(phnum)]
    load = []
    for typ, f, offset, virtual, physical, file_size, mem_size, align in program:
        require(offset+file_size <= len(binary), "invalid ELF program contents")
        if typ == 1:
            require(mem_size >= file_size, "invalid load segment")
            load.append((f, offset, virtual, physical, file_size, mem_size, align, sha(masked[offset:offset+file_size])))
    require(load and alloc.get(".text"), "missing loadable program")
    return {"elfSha": sha(binary), "buildID": build_id, "onlyGoBuildIDMaskedSha": sha(masked), "entry": entry,
            "program": program, "load": load, "alloc": alloc, "all": all_sections}


def compare_elf(a_name, h_name, a_symbols, h_symbols):
    a, h = elf(a_name), elf(h_name)
    def changed(left, right):
        return sorted(key for key in set(left) | set(right) if left.get(key) != right.get(key))
    allocated = changed(a["alloc"], h["alloc"])
    symbols_match = a_symbols.read_bytes() == h_symbols.read_bytes()
    equivalent = (not allocated and a["entry"] == h["entry"] and a["program"] == h["program"]
                  and a["load"] == h["load"] and symbols_match)
    print(json.dumps({"sameLoadableImageAndSymbolsIgnoringGoBuildID": equivalent,
                      "entireELFAfterMaskingGoBuildIDEqual": a["onlyGoBuildIDMaskedSha"] == h["onlyGoBuildIDMaskedSha"],
                      "sameEntries": a["entry"] == h["entry"], "sameProgramHeaders": a["program"] == h["program"],
                      "sameMaskedLoadSegments": a["load"] == h["load"], "sameSymbolAddressMaps": symbols_match,
                      "changedAllocSectionsIgnoringGoBuildID": allocated,
                      "changedSectionsUnmasked": changed(a["all"], h["all"]),
                      "a": a, "h": h}, indent=2))


if __name__ == "__main__":
    try:
        mode, *args = sys.argv[1:]
        if mode == "tuple":
            print("\t".join(map(str, CASES[args[0]][:4])))
        elif mode == "plan":
            plan(args[0], Path(args[1]), Path(args[2]))
        elif mode == "audit":
            audit(args[0], Path(args[1]))
        elif mode == "filter":
            confirmation(*map(Path, args))
        elif mode == "elf":
            compare_elf(*map(Path, args))
        else:
            raise ValueError("unknown mode")
    except (KeyError, IndexError, ValueError, OSError, struct.error) as error:
        sys.exit(str(error))
