"""Pin PR 91 source trees and verify the original native benchmark process plan."""

from csv import DictReader
from hashlib import sha1, sha256
import base64
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import zipfile

ASSETS = Path(__file__).resolve().parent / "diagnose-pr91"
PREFIX = "github.com/light-speak/luxo/"
GROUPS = ("primary", "confirmation")
CASES = {
    "01": ("codegen", "^BenchmarkGlobalReceiverName$",
           ["BenchmarkGlobalReceiverName/" + name for name in ("User", "_Record", "用户")]),
    "02": ("codegen", "^BenchmarkCompileDecimalIntegerLiterals$",
           ["BenchmarkCompileDecimalIntegerLiterals/" + name for name in
            ("int_plain", "int_leading_zero", "duration_plain", "duration_leading_zero")]),
    "03": ("semantic", "^BenchmarkScopeLookup(ReadOnly)?$",
           [f"Benchmark{root}/depth{depth}/{name}" for root in ("ScopeLookup", "ScopeLookupReadOnly")
            for depth in (0, 4, 32) for name in ("local", "parent", "missing")]),
    "04": ("semantic", "^BenchmarkFindClosestString$",
           ["BenchmarkFindClosestString/" + name for name in
            ("same-length", "mixed", "long-target", "exact-first")]),
    "05": ("codegen", "^BenchmarkGenerateVariableBindings$/^scalar-locals$",
           ["BenchmarkGenerateVariableBindings/scalar-locals"]),
    "06": ("codegen", "^BenchmarkGenerateModelReferences$/^model-await$",
           ["BenchmarkGenerateModelReferences/model-await"]),
    "07": ("codegen", "^BenchmarkGenerateStandaloneAwaitBindings$/^independent$",
           ["BenchmarkGenerateStandaloneAwaitBindings/independent"]),
    "08": ("codegen", "^BenchmarkSelectedQueryGeneration$/^scalar_map$/^1$",
           ["BenchmarkSelectedQueryGeneration/scalar_map/1"]),
    "09": ("codegen", "^BenchmarkSelectedQueryObjectGeneration$/^copy$/^1$",
           ["BenchmarkSelectedQueryObjectGeneration/copy/1"]),
    "10": ("codegen", "^BenchmarkPaginationCandidate$/^object-copy$/^(Analyze|Generate)$",
           ["BenchmarkPaginationCandidate/object-copy/" + part for part in ("Analyze", "Generate")]),
    "11": ("codegen", "^BenchmarkPrimaryKeyLoaderMetadata$/^128$/^loaders$",
           ["BenchmarkPrimaryKeyLoaderMetadata/128/loaders"]),
}
REQUIRED = {
    "BenchmarkGlobalReceiverName/_Record", "BenchmarkCompileDecimalIntegerLiterals/int_plain",
    "BenchmarkCompileDecimalIntegerLiterals/int_leading_zero", "BenchmarkScopeLookup/depth32/missing",
    "BenchmarkScopeLookup/depth32/parent", "BenchmarkScopeLookup/depth4/missing",
    "BenchmarkFindClosestString/long-target",
}
ORDER_KEYS = ("group", "pair", "comparison", "case", "side", "revision", "variant", "raw")
SCHEDULE_KEYS = (*ORDER_KEYS, "package", "filter")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")


def write_tsv(path, rows):
    path.write_text("".join("\t".join(map(str, row)) + "\n" for row in rows))


def pins():
    value = json.loads((ASSETS / "pins.json").read_text())
    encoded = (ASSETS / "tested-merge.patch.b64").read_bytes()
    require(sha256(encoded).hexdigest() == value["mergePatchBase64SHA256"], "base64 payload drift")
    decoded = base64.b64decode(b"".join(encoded.split()), validate=True)
    require(sha256(decoded).hexdigest() == value["mergePatchSHA256"], "decoded patch drift")
    return value


def comparisons(suite):
    require(suite in ("codegen", "semantic"), "unknown suite")
    result = [("full-aa", "base", "base"), ("full-ab", "base", "head")]
    if suite == "codegen":
        result += [("product-aa", "base-product", "base-product"),
                   ("product-ab", "base-product", "head-product")]
    return result


def cases(suite):
    return [key for key, value in CASES.items() if value[0] == suite]


def inventory(suite):
    roles = sorted({role for _, left, right in comparisons(suite) for role in (left, right)})
    return [(role, case) for role in roles for case in cases(suite)]


def schedule(suite):
    choices = comparisons(suite)
    for offset, group in enumerate(GROUPS):
        for pair in range(1, 11):
            position = ((pair - 1) + offset) % len(choices)
            for comparison, left, right in choices[position:] + choices[:position]:
                roles = [("left", left), ("right", right)]
                if (pair + offset) % 2 == 0:
                    roles.reverse()
                for case in cases(suite):
                    _, pattern, _ = CASES[case]
                    for side, role in roles:
                        raw = f"raw/{group}.{pair:02}.{comparison}.{case}.{side}.txt"
                        yield dict(zip(SCHEDULE_KEYS, (
                            group, f"{pair:02}", comparison, case, side, role, "default", raw,
                            "pkg/" + suite, pattern,
                        )))


def plan(directory, suite):
    directory.mkdir(parents=True, exist_ok=True)
    rows = list(schedule(suite))
    leaves = [leaf for key in cases(suite) for leaf in CASES[key][2]]
    require(len(leaves) == (15 if suite == "codegen" else 22), "unexpected case count")
    require(len(rows) == (1440 if suite == "codegen" else 160), "unexpected process count")
    write_tsv(directory / "schedule.tsv", ([row[key] for key in SCHEDULE_KEYS] for row in rows))
    write_tsv(directory / "inventory.tsv", ([role, case, "pkg/" + suite, "default", CASES[case][1]]
                                         for role, case in inventory(suite)))
    write_tsv(directory / "comparisons.tsv", comparisons(suite))
    for comparison, _, _ in comparisons(suite):
        expected = [{"Package": "pkg/" + suite, "Name": leaf + "-2"} for leaf in leaves]
        write_json(directory / f"{comparison}.expected.json", {"Base": expected, "Head": expected})
    write_json(directory / "plan.json", {
        "suite": suite, "processArms": len(rows), "inventoryArms": len(inventory(suite)),
        "leafObservations": sum(len(CASES[row["case"]][2]) for row in rows),
        "leavesPerComparison": len(leaves), "sourcePins": pins(),
    })
    print(f"{suite}: {len(rows)} process arms; {len(leaves)} exact leaves per comparison")


def git_blob(data):
    return sha1(b"blob " + str(len(data)).encode() + b"\0" + data).digest()


def tree_hash(directory):
    entries = []
    for child in directory.iterdir():
        if directory == tree_hash.root and child.name in (".tmp", "go.work", "go.work.sum"):
            continue
        if child.is_symlink():
            mode, digest = b"120000", git_blob(os.readlink(child).encode())
        elif child.is_dir():
            digest = tree_hash(child)
            if digest is None:
                continue
            mode = b"40000"
        else:
            mode = b"100755" if child.stat().st_mode & 0o111 else b"100644"
            digest = git_blob(child.read_bytes())
        name = os.fsencode(child.name)
        entries.append((name + (b"/" if mode == b"40000" else b""), mode + b" " + name + b"\0" + digest))
    if not entries:
        return None
    body = b"".join(row[1] for row in sorted(entries))
    return sha1(b"tree " + str(len(body)).encode() + b"\0" + body).digest()


def source_manifest(source):
    return {str(path.relative_to(source)): sha256(path.read_bytes()).hexdigest()
            for path in sorted(source.rglob("*")) if path.is_file() and not path.is_symlink()
            and path.relative_to(source).parts[0] != ".tmp"
            and str(path.relative_to(source)) not in ("go.work", "go.work.sum")}


def check_source(source, role, metadata, output):
    tree_hash.root = source
    actual = tree_hash(source).hex()
    require(actual == metadata[role]["tree"], f"{role}: source tree drift")
    manifest = source_manifest(source)
    for name, hashes in pins()["fixtureSHA256"].items():
        require(manifest[name] == hashes[pins()["base"]], f"{role}: fixture drift: {name}")
    write_json(output, {"role": role, "tree": actual, "filesSHA256": manifest})


def archive(repo, revision, destination):
    data = subprocess.check_output(["git", "-C", str(repo), "archive", revision])
    with tarfile.open(fileobj=io.BytesIO(data)) as stream:
        for member in stream.getmembers():
            require(not member.name.startswith("/") and ".." not in Path(member.name).parts,
                    "archive path escapes the owned source directory")
        stream.extractall(destination)


def restore_tests(source, baseline):
    for name in pins()["restoreTests"]:
        original, destination = baseline / name, source / name
        if original.exists():
            shutil.copy2(original, destination)
        elif destination.exists():
            destination.unlink()


def validate_test_inputs(source, role):
    value = pins()
    revision = value["base"] if role.startswith("base") else value["prHead"]
    expected = dict(value["allPackageTestSHA256"][revision])
    if role.endswith("-product"):
        for name in value["restoreTests"]:
            original = value["allPackageTestSHA256"][value["base"]].get(name)
            if original is None:
                expected.pop(name, None)
            else:
                expected[name] = original
    actual = {str(path.relative_to(source)): sha256(path.read_bytes()).hexdigest()
              for package in ("codegen", "semantic") for path in (source / "pkg" / package).glob("*_test.go")}
    require(actual == expected, f"{role}: package _test.go closure drift")
    return actual


def prepare(repo, parent, suite, evidence):
    value = pins()
    require(parent.is_dir() and not any(parent.iterdir()), "source parent must be owned and empty")
    baseline, candidate = parent / "base", parent / "head"
    baseline.mkdir()
    archive(repo, value["base"], baseline)
    shutil.copytree(baseline, candidate, symlinks=True)
    env = dict(os.environ, GIT_CEILING_DIRECTORIES=str(parent))
    evidence.mkdir(parents=True, exist_ok=True)
    patch = evidence / "tested-merge.patch"
    patch.write_bytes(base64.b64decode(b"".join((ASSETS / "tested-merge.patch.b64").read_bytes().split()), validate=True))
    for options in (("--check",), ()):
        subprocess.run(["git", "apply", *options, str(patch)], cwd=candidate, env=env, check=True)
    metadata = {"base": {"tree": value["baseTree"], "revision": value["base"]},
                "head": {"tree": value["testedMergeTree"], "revision": value["testedMerge"]}}
    if suite == "codegen":
        for role, original in (("base-product", baseline), ("head-product", candidate)):
            destination = parent / role
            shutil.copytree(original, destination, symlinks=True)
            restore_tests(destination, baseline)
            tree_hash.root = destination
            metadata[role] = {"tree": tree_hash(destination).hex(), "revision": metadata[role.split("-")[0]]["revision"]}
        require(metadata["base-product"]["tree"] == value["baseTree"], "baseline product changed")
        original_files = source_manifest(candidate)
        product_files = source_manifest(parent / "head-product")
        delta = {name for name in original_files.keys() | product_files.keys()
                 if original_files.get(name) != product_files.get(name)}
        require(delta == set(value["restoreTests"]), "product changed outside four tests")
    for role in metadata:
        check_source(parent / role, role, metadata, evidence / f"source-{role}.before.json")
        write_json(evidence / f"test-inputs-{role}.json", validate_test_inputs(parent / role, role))
    write_json(evidence / "source-roles.json", metadata)
    print("Verified exact tested merge tree and symmetric unchanged original benchmark inputs")


def verify_process(case, path, one_iteration):
    suite, _, leaves = CASES[case]
    lines = path.read_text().splitlines()
    require(len(lines) == len(leaves) + 5, f"{path}: unexpected process output")
    require(lines[:3] == ["goos: linux", "goarch: amd64", "pkg: " + PREFIX + "pkg/" + suite]
            and lines[3].startswith("cpu: ") and lines[-1] == "PASS", f"{path}: invalid process header/verdict")
    names = []
    for row in lines[4:-1]:
        name, iterations, ns, ns_unit, memory, mem_unit, allocations, alloc_unit = row.split()
        require(int(iterations) > 0 and (not one_iteration or int(iterations) == 1)
                and (ns_unit, mem_unit, alloc_unit) == ("ns/op", "B/op", "allocs/op")
                and all(re.fullmatch(r"[0-9]+(?:\.[0-9]+)?", value) for value in (ns, memory, allocations)),
                f"{path}: invalid sample values")
        names.append(name)
    require(names == [leaf + "-2" for leaf in leaves], f"{path}: case identity/order mismatch")
    return lines[3]


def verify_order(actual, suite):
    expected = [{key: row[key] for key in ORDER_KEYS} for row in schedule(suite)]
    require(actual == expected, "process schedule drift")
    return expected


def verify(directory, suite, include_samples):
    cpus = {verify_process(case, directory / f"inventory-{role}-{case}.txt", True)
            for role, case in inventory(suite)}
    if include_samples:
        with (directory / "order.tsv").open(newline="") as stream:
            expected = verify_order(list(DictReader(stream, delimiter="\t")), suite)
        names = {str(path.relative_to(directory)) for path in (directory / "raw").glob("*.txt")}
        require(names == {row["raw"] for row in expected}, "missing/extra process arms")
        cpus.update(verify_process(row["case"], directory / row["raw"], False) for row in expected)
    require(len(cpus) == 1, "all process evidence must use the same CPU")
    print(f"Verified exact {suite} process identity, units, completion, and runner CPU")


def historical_inventory(artifact):
    expected = {"pkg/" + suite: {leaf + "-2" for _, (package, _, leaves) in CASES.items()
                                if package == suite for leaf in leaves} for suite in ("codegen", "semantic")}
    require(REQUIRED <= {leaf for _, _, leaves in CASES.values() for leaf in leaves}, "lost original failure")
    with zipfile.ZipFile(artifact) as bundle:
        for side in ("base", "head"):
            for package, wanted in expected.items():
                raw = bundle.read(f"benchmark-inventory/{side}/{package.replace('/', '-')}.raw.log").decode()
                observed = {line.split()[0] for line in raw.splitlines() if line.startswith("Benchmark")}
                require(wanted <= observed, f"{side}: missing original fixture: {wanted - observed}")
    print("All 37 planned original leaves exist in both historical actual-run inventories; no new timing")


if __name__ == "__main__":
    command, *args = sys.argv[1:]
    if command == "plan":
        plan(Path(args[0]), args[1])
    elif command == "prepare":
        prepare(Path(args[0]).resolve(), Path(args[1]).resolve(), args[2], Path(args[3]).resolve())
    elif command in ("inventory", "samples"):
        verify(Path(args[0]), args[1], command == "samples")
    elif command == "source-check":
        check_source(Path(args[0]).resolve(), args[1], json.loads(Path(args[2]).read_text()), Path(args[3]))
    elif command == "historical-inventory":
        historical_inventory(Path(args[0]))
    else:
        raise SystemExit("unknown diagnostic command")
