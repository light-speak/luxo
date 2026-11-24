"""Qualify and audit the two test-only linkage conditions for PR 71."""

from collections import Counter
import csv
from hashlib import sha256
import json
from pathlib import Path
import re
import sys


API = "api"
LUVIA = "luvia"
PACKS = (API, LUVIA)
PACKAGE = {name: f"pkg/lux/{name}" for name in PACKS}
CONDITIONS = ("default", "isolated")
CPU = "AMD EPYC 7763 64-Core Processor"
API_HISTORICAL = (
    "BinaryListPayload/Decimal/count=1/version=0",
    "BinaryListPayload/Duration/count=1/version=0",
    "ModuleColorWarm",
    "NullableRootJSON/int/plain",
    "NullableRootJSON/int/present",
    "NullableRootJSON/zero/plain",
    "NullableRootJSON/zero/present",
    "RawJSONArrayPool/empty/encode",
    "RawJSONArrayPool/precision/encode",
    "RawJSONArrayUnicode/empty/strict",
    "RegistrySteadyRequest/params=1/json",
)
API_IDENTITY = ("IdentityConditionValidity/member/EnumChecked", "IdentitySubjectVisibility/pointer/subject")
KEY_HISTORICAL = (
    "PrimaryKeys/row-small/keyLast=false/baseline=true",
    "PrimaryKeys/row-small/keyLast=true/baseline=true",
    "PrimaryKeys/row-wide/keyLast=false/baseline=true",
)
TRACE_HISTORICAL = "TraceEncodingComparison/true/reservation-candidate"
LUVIA_CONTROLS = ("TraceEncodingComparison/true/static-reference", "PrimaryKeys/row-small/keyLast=false/baseline=false")
HELPER = "metrics_retention_test.go"
API_FIXTURES = {
    "identity_reader_bench_test.go": "20d45a1c2a71419d6c2c44e33b49e691cf15f3522f6e1041541dbce5097a3c5f",
    "identity_reader_fuzz_test.go": "f4e765934e382ad361aedac0dea15864efcdeb553ef9e6556adb66b65c0765a2",
    "identity_reader_test.go": "600116795f36c98c7268237d2df5fefabb9d5708cf25ba9219ea4b1dcf21cf4c",
    "identity_value_test.go": "619664ee4f2a8c715c97bc3a661b9a845d1dbd8411bb575a7ac672ec4ee3d560",
}
LUVIA_FIXTURES = {
    "metrics_json_reference_test.go": "bcdbe0ba5a4865086a4c7397fcd5a3a383051d13e3b30d7317c006fdd0d3feb9",
    "metrics_json_test.go": "3956d8a85e34e50d5cf063966b1280b66816c0b5e7f942453d91ee8e765e5632",
    HELPER: "6ac3715808ae4dd3794adc4e3e95bb3c09b8db5c118912a50d25b1787cc8a3c9",
    "primary_keys_baseline_test.go": "807170edfaaa9c102bdea728871aec8ab4caacbbd964e27364ee78d351c999c0",
    "primary_keys_bench_test.go": "199cbc477c1255c98d5c5f1c9c8665d159f234af1049f39d9d7eabcdc03d7108",
}
IDENTITY = re.compile(r"^BenchmarkIdentity(ConditionValidity|EnumArray|SubjectVisibility)/")
API_INVENTORY = {
    "default": (1010, "a280083c39ec5b38e6cbf31270c09867071e29da9fb519f1a19294b558dab330"),
    "identity": (31, "974a52714718340b241af1d9746d48db6e33ec70f4972de278aea37d5cca53ea"),
    "isolated": (979, "02c1401e26206cf3f5c9d69ab50539129bfc5118b1513ffbd00cfd4573891a05"),
}


def full_names(benchmarks):
    return [f"Benchmark{name}-2" for name in benchmarks]


def exact_filter(names):
    return "|".join(
        "/".join(f"^{re.escape(segment)}$" for segment in item.removesuffix("-2").split("/"))
        for item in names
    )


def put_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")


def plan(root):
    historical = {API: API_HISTORICAL, LUVIA: KEY_HISTORICAL}
    supplementary = {API: (), LUVIA: (TRACE_HISTORICAL,)}
    controls = {API: API_IDENTITY, LUVIA: LUVIA_CONTROLS}
    put_json(root / "planned-scopes.json", {
        pack: {"historicalReproducedPreviously": list(historical[pack]),
               "historicalTraceSignalPreviouslyUnconfirmedOn7763": list(supplementary[pack]),
               "controls": list(controls[pack])}
        for pack in PACKS
    })
    for pack in PACKS:
        for condition in CONDITIONS:
            current = root / pack / condition
            current.mkdir(parents=True)
            include = controls[pack] if pack == LUVIA or condition == "default" else ()
            names = full_names(historical[pack] + supplementary[pack] + include)
            assert len(names) == len(set(names))
            (current / "names.txt").write_text("\n".join(names) + "\n")
            (current / "filter.txt").write_text(exact_filter(names) + "\n")
            expected = [{"Package": PACKAGE[pack], "Name": name} for name in names]
            put_json(current / "expected.json", {"Base": expected, "Head": expected})


def checked_file(path, digest):
    data = path.read_bytes()
    assert sha256(data).hexdigest() == digest, path
    return data


def overlay(scratch, root, pack, role):
    source = scratch / "source" / role
    package_dir = source / PACKAGE[pack]
    pair = scratch / f"pair-{pack}"
    manifest = json.loads((pair / "fixtures.json").read_text())
    assert manifest["Complete"]
    fixture = next(item for item in manifest["Packages"] if item["Physical"] == PACKAGE[pack])
    ordinary = pair / "fixtures" / PACKAGE[pack] / f"{role}.json"
    replace = json.loads(ordinary.read_text())["Replace"]
    pins = API_FIXTURES if pack == API else LUVIA_FIXTURES
    original = {}
    for name, digest in pins.items():
        virtual = package_dir / name
        assert fixture["FixtureSHA"][name] == digest
        original[name] = checked_file(virtual, digest)
        frozen = Path(replace[str(virtual)])
        assert frozen.is_relative_to(pair)
        assert checked_file(frozen, digest) == original[name]
    if pack == API:
        for name in pins:
            replace[str(package_dir / name)] = ""
    else:
        tests = {Path(path) for path in replace if Path(path).parent == package_dir and path.endswith("_test.go")}
        tests.update(package_dir.glob("*_test.go"))
        for path in tests:
            if path.name not in pins:
                replace[str(path)] = ""
        declaration = re.search(
            rb"^func observationTestTrace\(id string\) api\.TraceRecord \{\n[^\n]+\n\}$",
            original[HELPER], re.M,
        )
        assert declaration
        helper = scratch / f"{role}-{pack}-observation-fixture.go"
        imports = b'package luvia\n\nimport (\n\t"net/http"\n\t"time"\n\n\t"github.com/light-speak/luxo/pkg/lux/api"\n)\n\n'
        helper.write_bytes(imports + declaration.group() + b"\n")
        replace[str(package_dir / HELPER)] = str(helper)
    (root / pack / "default" / f"{role}-overlay.json").write_bytes(ordinary.read_bytes())
    path = root / pack / "isolated" / f"{role}-overlay.json"
    put_json(path, {"Replace": dict(sorted(replace.items()))})
    print(path)


def sample_names(path, pack, iterations, cpu=None):
    lines = path.read_text().splitlines()
    assert len(lines) > 4 and lines[2] == f"pkg: github.com/light-speak/luxo/{PACKAGE[pack]}", path
    assert lines[3].startswith("cpu: ") and lines[-1] == "PASS", path
    if cpu is not None:
        assert lines[:2] == ["goos: linux", "goarch: amd64"] and lines[3][5:].strip() == cpu, path
    rows = [line.split() for line in lines[4:-1] if line.startswith("Benchmark")]
    for row in rows:
        assert (row[1] == iterations if iterations is not None else int(row[1]) > 0), (path, row)
        assert row[3] == "ns/op" and row[-3] == "B/op" and row[-1] == "allocs/op", (path, row)
    result = [row[0] for row in rows]
    assert len(result) == len(set(result)), path
    return sorted(result)


def luvia_inventory():
    return full_names(
        [f"TraceEncodingComparison/{size}/{mode}"
         for size in ("false", "true") for mode in ("previous", "static-reference", "reservation-candidate")]
        + [f"PrimaryKeys/{shape}/keyLast={key}/baseline={baseline}"
           for shape in ("row-small", "row-wide", "column-small", "column-batch", "column-many")
           for key in ("false", "true") for baseline in ("false", "true")]
    )


def inventory(root, cpu):
    for role in ("base", "head"):
        api = {}
        for pack in PACKS:
            for condition in CONDITIONS:
                current = root / pack / condition
                names = sample_names(current / f"{role}-inventory.raw", pack, "1", cpu)
                names_path = current / f"{role}-inventory.names"
                names_path.write_text("\n".join(names) + "\n")
                if pack == API:
                    count, digest = API_INVENTORY[condition]
                    assert len(names) == count and sha256(names_path.read_bytes()).hexdigest() == digest
                    api[condition] = names
                else:
                    assert names == sorted(luvia_inventory())
                expected = sorted((current / "names.txt").read_text().splitlines())
                assert set(expected) <= set(names)
                assert sample_names(current / f"{role}-selected.raw", pack, "1", cpu) == expected
                nm = (current / f"{role}-symbols.nm").read_text()
                pattern = r"/auth\.signWithSubject$" if pack == API else r"/auth\.LoadConfig$"
                assert bool(re.search(pattern, nm, re.M)) == (condition == "default"), (pack, role, condition)
        identity = [name for name in api["default"] if IDENTITY.match(name)]
        count, digest = API_INVENTORY["identity"]
        assert len(identity) == count and not (set(identity) & set(api["isolated"]))
        path = root / API / "default" / f"{role}-identity.names"
        path.write_text("\n".join(identity) + "\n")
        assert sha256(path.read_bytes()).hexdigest() == digest
        assert sorted(identity + api["isolated"]) == api["default"]


def table(path):
    with path.open() as stream:
        return list(csv.reader(stream, delimiter="\t"))


def audit(root):
    global_expected = []
    wanted = {(pack, condition): [] for pack in PACKS for condition in CONDITIONS}
    for offset, group in enumerate(("primary", "confirmation")):
        for index in range(1, 11):
            pair = f"{index:02d}"
            odd = (index + offset) % 2 == 1
            comparisons = ("ab", "aa") if odd else ("aa", "ab")
            conditions = CONDITIONS if odd else CONDITIONS[::-1]
            packs = PACKS if odd else PACKS[::-1]
            sides = ("left", "right") if odd else ("right", "left")
            for comparison in comparisons:
                for condition in conditions:
                    for pack in packs:
                        global_expected.append([group, pair, comparison, condition, pack])
                        for side in sides:
                            role = "head" if comparison == "ab" and side == "right" else "base"
                            raw = f"raw/{group}.{pair}.{comparison}.01.{side}.txt"
                            wanted[pack, condition].append([group, pair, comparison, "01", side, role, condition, raw])
    header, *observed = table(root / "condition-order.tsv")
    assert header == ["group", "pair", "comparison", "condition", "package"]
    assert observed == global_expected
    counts = Counter()
    for (pack, condition), expected in wanted.items():
        current = root / pack / condition
        header, *observed = table(current / "order.tsv")
        assert header == ["group", "pair", "comparison", "case", "side", "revision", "variant", "raw"]
        assert observed == expected
        assert {str(path.relative_to(current)) for path in (current / "raw").glob("*.txt")} == {r[-1] for r in expected}
        expected_names = sorted((current / "names.txt").read_text().splitlines())
        for row in expected:
            assert sample_names(current / row[-1], pack, None, CPU) == expected_names
            counts["arms"] += 1
            counts["benchmarkRows"] += len(expected_names)
    put_json(root / "raw-inventory.json", dict(counts))


def rejects(path, pack):
    pattern = re.compile(
        rf"^confirmed performance regression: github\.com/light-speak/luxo/{PACKAGE[pack]}/(.+)-2 (sec/op|B/op|allocs/op) "
    )
    return {match.groups() for line in path.read_text().splitlines() if (match := pattern.match(line))}


def summarize(root):
    plan = json.loads((root / "planned-scopes.json").read_text())
    report = {}
    bad_gate, inconclusive = False, False
    for pack in PACKS:
        ordinary, isolated = (root / pack / condition for condition in CONDITIONS)
        bad_aa = any("aa\t0" not in (directory / "verdicts.tsv").read_text().splitlines()
                     for directory in (ordinary, isolated))
        historical = set(plan[pack]["historicalReproducedPreviously"])
        default_rejects, treatment_rejects = (rejects(directory / "ab.benchgate.log", pack)
                                             for directory in (ordinary, isolated))
        allowed = historical | set(plan[pack]["historicalTraceSignalPreviouslyUnconfirmedOn7763"])
        ordinary_control_rejects = {(name, unit) for name, unit in default_rejects if name not in allowed}
        reproduced = {name for name, unit in default_rejects if name in historical and unit == "sec/op"}
        retained = {name for name, unit in treatment_rejects if name in reproduced and unit == "sec/op"}
        bad_gate |= bad_aa or bool(ordinary_control_rejects) or bool(treatment_rejects)
        inconclusive |= not reproduced
        report[pack] = {
            "stableSameBinaryControl": not bad_aa,
            "defaultRejects": sorted(default_rejects),
            "alreadyConfirmedTimingReproduced": sorted(reproduced),
            "alreadyConfirmedTimingNotReproduced": sorted(historical - reproduced),
            "ordinaryControlRejects": sorted(ordinary_control_rejects),
            "isolatedRejects": sorted(treatment_rejects),
            "stillRejectedWhenIsolated": sorted(retained),
            "noLongerRejectedWhenIsolated": sorted(reproduced - retained),
        }
    report["scope"] = "Historical attribution on matched hardware; ordinary benchmarks and the formal gate remain authoritative."
    put_json(root / "diagnosis.json", report)
    print((root / "diagnosis.json").read_text(), end="")
    return 1 if bad_gate else 4 if inconclusive else 0


def main():
    action, root, *args = sys.argv[1:]
    root = Path(root)
    if action == "plan":
        plan(root)
    elif action == "overlay":
        overlay(Path(args[0]), root, args[1], args[2])
    elif action == "inventory":
        inventory(root, args[0] or None)
    elif action == "audit":
        audit(root)
    elif action == "summarize":
        sys.exit(summarize(root))
    else:
        sys.exit(f"unknown action: {action}")


if __name__ == "__main__":
    main()
