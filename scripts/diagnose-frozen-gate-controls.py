"""Build controlled source inputs and verify the native gate experiment."""

import base64
import csv
from decimal import Decimal
import hashlib
import json
from pathlib import Path
import re
import sys


PACKAGE = "pkg/lux/sample"
IMPORT = "github.com/light-speak/luxo/" + PACKAGE
PROBE = "BenchmarkProbe-2"
ONE_SIDED = "BenchmarkOneSided-2"
GROUPS = ("primary", "confirmation")
MODES = ("native", "frozen")
ROLES = ("aa", "fixture", "time", "alloc")
UNITS = ("ns/op", "B/op", "allocs/op")
BENCHMARK_SHA = "62e4cfe9562302b75bdf226a853fd79614698d489f93514614d2fe98ed6b2f85"
INVENTORY_SHA = "d9c10648067e5eb911e29cc69b7b365534b15752971099c5d86f41d4c261319d"

PRODUCT = """package sample

import "math/bits"

var seed uint64 = 0xa2ddad601ab9f421
var payload = []byte{13, 21, 34}
%s
func probe() []byte {
	value := seed
	for i := range 511 {
		value = bits.RotateLeft64(value^uint64(i), 13)
	}
%s	if value != 0xa10d16ed6b00d430 {
		return nil
	}
%s	return payload
}
"""
EXTRA_ROUNDS = """	for i := 511; i < 4096; i++ {
		value = bits.RotateLeft64(value^uint64(i), 13)
	}
	for i := 4096; i > 511; i-- {
		value = bits.RotateLeft64(value, -13) ^ uint64(i-1)
	}
"""
HEAP_ALLOC = """	block := new([256]byte)
	block[0] = payload[0]
	retained = block
"""
TEST = """package sample

import "testing"

func consume() []byte {
	got := probe()
%s	return got
}

func TestControlResult(t *testing.T) {
	for _, got := range [][]byte{probe(), consume()} {
		if len(got) != 3 || got[0] != 13 || got[1] != 21 || got[2] != 34 {
			t.Fatalf("unexpected control payload: %%v", got)
		}
	}
}
"""
EXTRA_FIXTURE = """	for range 8 {
		repeated := probe()
		if len(repeated) != 3 || repeated[0] != 13 {
			return nil
		}
	}
"""
ADDED_FIXTURE = """package sample

import "testing"

func BenchmarkOneSided(b *testing.B) {
	for b.Loop() {
		result = consume()
	}
}
"""


def require(ok, message):
    if not ok:
        raise SystemExit(message)


def digest(value):
    return hashlib.sha256(value).hexdigest()


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def sample_key(name):
    return {"Package": PACKAGE, "Name": name}


def stage(source, evidence, old_test, patch):
    delimiter = bytes([96])
    match = re.search(
        rb"(?m)^const fixtureBenchmark = " + delimiter + rb"([^" + delimiter + rb"]+)" + delimiter,
        old_test.read_bytes(),
    )
    require(match and digest(match[1]) == BENCHMARK_SHA, "known BenchmarkProbe source changed")
    inventory = base64.b64decode(b"".join(patch.read_bytes().split()), validate=True)
    require(digest(inventory) == INVENTORY_SHA, "approved four-inventory implementation changed")
    (evidence / "frozen-inventory.patch").write_bytes(inventory)
    source_shas = {}
    for role in ("base", "fixture", "time", "alloc"):
        root = source / role
        (root / PACKAGE).mkdir(parents=True)
        (root / "go.mod").write_text("module github.com/light-speak/luxo\n\ngo 1.27.1\n")
        (root / "go.work").write_text("go 1.27.1\n\nuse .\n")
        package = root / PACKAGE
        product = {
            "time": ("", EXTRA_ROUNDS, ""),
            "alloc": ("\nvar retained *[256]byte\n", "", HEAP_ALLOC),
        }.get(role, ("", "", ""))
        (package / "probe.go").write_text(PRODUCT % product)
        (package / "probe_test.go").write_bytes(match[1])
        (package / "helper_test.go").write_text(TEST % (EXTRA_FIXTURE if role == "fixture" else ""))
        if role == "fixture":
            (package / "added_test.go").write_text(ADDED_FIXTURE)
        source_shas[role] = {
            str(file.relative_to(root)): digest(file.read_bytes())
            for file in sorted(root.rglob("*"))
            if file.is_file()
        }
    expected_changes = {
        "fixture": {f"{PACKAGE}/added_test.go", f"{PACKAGE}/helper_test.go"},
        "time": {f"{PACKAGE}/probe.go"},
        "alloc": {f"{PACKAGE}/probe.go"},
    }
    base = source_shas["base"]
    for role, expected in expected_changes.items():
        head = source_shas[role]
        actual = {name for name in base.keys() | head.keys() if base.get(name) != head.get(name)}
        require(actual == expected, f"wrong source changes for {role}: {actual}")
    write_json(evidence / "source-sha256.json", source_shas)
    write_json(evidence / "expected.json", {side: [sample_key(PROBE)] for side in ("Base", "Head")})


def verify_inventories(evidence):
    source = json.loads((evidence / "source-sha256.json").read_text())
    fixtures = {Path(name).name: sha for name, sha in source["base"].items() if name.endswith("_test.go")}
    shared_base = None
    for role in ROLES[1:]:
        pair = json.loads((evidence / f"compile-pair-{role}.json").read_text())
        require(pair["Complete"] and pair["Logical"] == PACKAGE, f"incomplete paired compile: {role}")
        first, second = pair["GoEnvironment"]
        require(first == second and all(first.get(key) == value for key, value in {
            "GOVERSION": "go1.27.1", "GOOS": "linux", "GOARCH": "amd64", "GOAMD64": "v1",
            "GOFLAGS": "", "GOEXPERIMENT": "", "CGO_ENABLED": "1",
        }.items()), f"unexpected paired build environment: {role}")
        package, = pair["Packages"]
        require(package["Physical"] == PACKAGE and package["FixtureSHA"] == fixtures,
                f"wrong pinned fixture bytes: {role}")
        base_sha, head_sha = package["BinarySHA"]
        if shared_base is None:
            shared_base = base_sha
        require(base_sha == shared_base, f"frozen baseline is not byte-identical: {role}")
        require((base_sha == head_sha) == (role == "fixture"), f"wrong frozen treatment bytes: {role}")
        for side, sha in (("base", base_sha), (role, head_sha)):
            hashes = (evidence / f"binaries-{side}-frozen.txt").read_text().splitlines()[0].split()
            require(hashes[0] == sha, f"sampled binary differs from compile receipt: {side}")
        receipt = json.loads((evidence / f"inventory-{role}.json").read_text())
        probe = [sample_key(PROBE)]
        extra = [sample_key(ONE_SIDED)] if role == "fixture" else []
        require(receipt["Package"] == PACKAGE and receipt["NativeBaseOnly"] == [] and
                receipt["NativeHeadOnly"] == extra and receipt["Native"] == {"Base": probe, "Head": extra + probe} and
                receipt["Frozen"] == {"Base": probe, "Head": probe}, f"unexpected native/frozen leaves: {role}")


def samples(evidence):
    with (evidence / "order.tsv").open(newline="") as stream:
        rows = list(csv.DictReader(stream, delimiter="\t"))
    count = len(GROUPS) * 10 * len(MODES) * len(ROLES) * 2
    require(len(rows) == count, f"expected {count} arms, got {len(rows)}")
    seen = set()
    for left, right in zip(rows[0::2], rows[1::2]):
        group, pair, comparison = (left[key] for key in ("group", "pair", "comparison"))
        mode, role = comparison.split("-")
        key = (group, pair, mode, role)
        require(key not in seen and group in GROUPS and pair in {f"{i:02}" for i in range(1, 11)} and
                mode in MODES and role in ROLES, f"unexpected or repeated pair: {key}")
        seen.add(key)
        left_first = (int(pair) + GROUPS.index(group)) % 2 == 1
        for row, side in zip((left, right), ("left", "right") if left_first else ("right", "left")):
            revision = "base" if side == "left" or role == "aa" else role
            variant = "default" if mode == "native" else "frozen"
            name = f"raw/{group}.{pair}.{comparison}.01.{side}.txt"
            require(row == {"group": group, "pair": pair, "comparison": comparison, "case": "01",
                            "side": side, "revision": revision, "variant": variant, "raw": name},
                    f"wrong order or benchmark binary: {row}")
        yield key, comparison, (left, right) if left_first else (right, left)


def measurement(raw, cpu):
    lines = raw.read_text().splitlines()
    require(lines[-1:] == ["PASS"] and {"goos: linux", "goarch: amd64", f"pkg: {IMPORT}"} <= set(lines),
            f"incomplete or wrong-platform raw sample: {raw.name}")
    require([line for line in lines if line.startswith("cpu: ")] == [cpu], f"CPU metadata changed: {raw.name}")
    values = [line.split() for line in lines if line.startswith("Benchmark")]
    require(len(values) == 1 and len(values[0]) == 8, f"wrong measurement count: {raw.name}")
    name, count, ns, ns_unit, size, size_unit, allocs, alloc_unit = values[0]
    require(name == PROBE and int(count) > 0 and (ns_unit, size_unit, alloc_unit) == UNITS,
            f"wrong measurement or standard metrics: {raw.name}")
    result = tuple(Decimal(number) for number in (ns, size, allocs))
    require(all(number.is_finite() and number >= 0 for number in result) and result[0] > 0,
            f"invalid benchmark measurement: {raw.name}")
    return result


def metrics(evidence, normalized):
    all_pairs = list(samples(evidence))
    first = normalized / all_pairs[0][2][0]["raw"]
    cpu = next((line for line in first.read_text().splitlines() if line.startswith("cpu: ")), None)
    require(cpu is not None, "missing measured CPU")
    with (evidence / "signed-pairs.tsv").open("w", newline="") as stream:
        writer = csv.writer(stream, delimiter="\t", lineterminator="\n")
        writer.writerow(("group", "pair", "comparison", "unit", "base", "head", "signed_delta"))
        for (group, pair, _, role), comparison, rows in all_pairs:
            left, right = (measurement(normalized / row["raw"], cpu) for row in rows)
            for unit, base, head in zip(UNITS, left, right):
                writer.writerow((group, pair, comparison, unit, base, head, head - base))
    with (evidence / "signed-pairs.tsv").open(newline="") as stream:
        for row in csv.DictReader(stream, delimiter="\t"):
            if row["unit"] == "ns/op":
                continue
            delta = int(row["signed_delta"])
            expected = {"B/op": 256, "allocs/op": 1}[row["unit"]] if row["comparison"].endswith("-alloc") else 0
            require(delta == expected, f"unexpected raw allocation delta: {row}")


def verdict(evidence):
    with (evidence / "verdicts.tsv").open(newline="") as stream:
        rows = list(csv.DictReader(stream, delimiter="\t"))
    require([row["comparison"] for row in rows] == [f"{mode}-{role}" for mode in MODES for role in ROLES],
            "missing or wrongly ordered benchgate verdict")
    for row in rows:
        comparison = row["comparison"]
        mode, role = comparison.split("-")
        log = (evidence / f"{comparison}.benchgate.log").read_text()
        expected = (
            {"sec/op"} if role == "time" or (mode, role) == ("native", "fixture")
            else {"B/op", "allocs/op"} if role == "alloc"
            else set()
        )
        rejected = re.findall(rf"(?m)^confirmed performance regression: {PACKAGE}/{PROBE} (\S+) ", log)
        actual = set(rejected)
        require(actual >= expected and (actual == expected or role == "alloc" and actual - expected == {"sec/op"}),
                f"unexpected confirmed metrics for {comparison}: {actual}, require {expected}")
        if expected:
            require(len(rejected) == len(actual) and row["benchgate_exit"] == "1" and
                    f"performance gate rejected {len(actual)} regression(s)" in log.splitlines(),
                    f"unexpected rejection or execution error: {comparison}")
        else:
            require(row["benchgate_exit"] == "0" and
                    "performance gate passed: 1 time and 2 allocation comparisons confirmed across two sample groups"
                    in log.splitlines(), f"clean control did not pass: {comparison}")


def main():
    operation, *paths = sys.argv[1:]
    paths = list(map(Path, paths))
    operations = {
        "stage": stage,
        "inventories": verify_inventories,
        "order": lambda prefix: list(samples(prefix)),
        "metrics": metrics,
        "verdict": verdict,
    }
    operations[operation](*paths)


if __name__ == "__main__":
    main()
