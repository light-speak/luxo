#!/usr/bin/env python3
"""Bind the archived CanonicalJSON failure and the exact isolated inventory."""

import io
import json
import shutil
import sys
from hashlib import sha256
from itertools import product
from pathlib import Path
from zipfile import ZipFile

from shards import AMD_9V74, API, HOT, PROFILES, SHARDS, filter_for, smoke, verify_original


ARTIFACT_DIGEST = "9be901054bf61386e5ee6f6f86f5119ea78f73fceb0c1111d9665093b772bca2"
COMPARED = sorted(SHARDS["03"][-1] + SHARDS["07"][-1])
WITNESS_NAMES = ("CanonicalJSONTyped/boolean/1", "CanonicalJSONValue/object/after")
COUNTS = ("-1", "0", "1", "128")
SIDES = ("before", "after")
CASES = {
    "Structured": [(count,) for count in COUNTS],
    "Typed": [*product(("string", "escaped", "boolean", "date", "uuid", "decimal", "enum", "unique", "long1KB"), COUNTS),
              ("chinese72KB", "-1")],
    "Bytes": list(product(("empty", "small", "escaped", "long72KB", "escaped4096", "list0", "list1", "list128", "escapedList128"),
                          ("decode", "canonical"))),
    "Float": list(product(("0", "1", "16", "128"), SIDES)),
    "Value": list(product(("integer", "object", "list0", "integerList1", "stringList1", "list1", "list16", "list128"), SIDES)),
    "Integer": list(product(("0", "1", "16", "128"), SIDES)),
}
FULL = sorted(f"CanonicalJSON{kind}/" + "/".join(leaf) for kind, leaves in CASES.items() for leaf in leaves)
assert len(FULL) == len(set(FULL)) == 91 and len(COMPARED) == 6 and set(COMPARED) <= set(FULL)


def header_cpu(cpu):
    if cpu != AMD_9V74:
        print(f"wrong-cpu: {cpu}", file=sys.stderr)
        raise SystemExit(3)


def expected(names):
    entries = [{"Package": API, "Name": f"Benchmark{name}-2"} for name in sorted(names)]
    return {"Base": entries, "Head": entries}


def write_targets(destination, names):
    destination.write_text(json.dumps(expected(names), indent=2) + "\n")


def correlated_gate(text):
    confirmed = [line for line in text.splitlines() if line.startswith("confirmed performance regression:")]
    return all(any(f"/{name}-2 sec/op " in line for line in confirmed) for name in WITNESS_NAMES)


def bind(cpu, directory):
    header_cpu(cpu)
    raw = (directory / "witness.zip").read_bytes()
    assert len(raw) == 529448 and sha256(raw).hexdigest() == ARTIFACT_DIGEST
    profiles = set()
    with ZipFile(io.BytesIO(raw)) as witness:
        assert witness.testzip() is None
        for shard in ("03", "07"):
            _, package, ident, size, digest, confirmed, leaves = SHARDS[shard]
            artifact = witness.read(f"{ident}.zip")
            assert len(artifact) == size and sha256(artifact).hexdigest() == digest
            with ZipFile(io.BytesIO(artifact)) as original:
                batch, targets, verdict, profile = verify_original(
                    lambda file: original.read(file).decode(), cpu, package, leaves, confirmed)
            profiles.add(profile)
            (directory / f"shard-{shard}.batch.tsv").write_text(batch)
            (directory / f"shard-{shard}.expected.json").write_text(targets)
            (directory / f"shard-{shard}.gate.txt").write_text(verdict)
        profile, = profiles
        assert witness.read("profile.txt").decode() == profile
        assert witness.read("selection.txt").decode().strip() == cpu
        assert witness.read("shards/verdict.txt") == b"control_failed=false\ncomparison_exit=1\n"
        assert correlated_gate(witness.read("shards/AB.gate.txt").decode())
        for group in ("AA", "BB"):
            for suffix in ("", ".reverse"):
                assert witness.read(f"shards/{group}{suffix}.gate.txt").startswith(b"performance gate passed:")
    (directory / "original-profile.txt").write_text(profile)
    write_targets(directory / "shards.expected.json", COMPARED)
    write_targets(directory / "canonical-all.expected.json", FULL)
    batch = f"{API}\t{PROFILES[API]}\t{filter_for(COMPARED)}"
    (directory / "shards.batch.tsv").write_text(batch + "\n")
    print(batch + "\t" + HOT[AMD_9V74])


def attach_projector(directory):
    main = directory / "main.go"
    source = main.read_bytes()
    entry = b"func main() {"
    assert source.count(entry) == 1
    main.write_bytes(source.replace(entry, b"func ordinaryBenchrun() {"))
    shutil.copyfile(Path(__file__).with_name("canonical-project.go.txt"), directory / "diagnostic_canonical.go")


if __name__ == "__main__":
    mode, *args = sys.argv[1:]
    if mode == "bind":
        bind(args[0], Path(args[1]))
    elif mode == "all-smoke":
        header_cpu(args[0])
        directory = Path(args[1])
        smoke(args[0], directory, Path(args[2]), directory / "canonical-all.expected.json")
    elif mode == "attach-projector":
        attach_projector(Path(args[0]))
    elif mode == "witness-confirmed":
        if not correlated_gate(Path(args[0]).read_text()):
            raise SystemExit(4)
    else:
        raise SystemExit("use bind, all-smoke, attach-projector or witness-confirmed")
