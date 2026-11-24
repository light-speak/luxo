"""Verify exact PR93 A/H and symmetric-test P1 native comparisons."""

from csv import DictReader, reader
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

ASSETS = Path(__file__).resolve().parent / "diagnose-pr93-p1"
PREFIX = "github.com/light-speak/luxo/"
GROUPS = ("primary", "confirmation")
CASES = {
 "01": ("codegen", "^BenchmarkGlobalReceiverName$/^_Record$", ["BenchmarkGlobalReceiverName/_Record"]),
 "02": ("codegen", "^BenchmarkWhereCondition$/^text$", ["BenchmarkWhereCondition/text"]),
 "03": ("semantic", "^BenchmarkAnalyzeSealedPayloads$/^many_variants$", ["BenchmarkAnalyzeSealedPayloads/many_variants"]),
 "04": ("semantic", "^BenchmarkCollectEventEmits$/^direct_1$/^recursive=true$", ["BenchmarkCollectEventEmits/direct_1/recursive=true"]),
 "05": ("semantic", "^BenchmarkErrorDeclarationValidation$/^fields_1$", ["BenchmarkErrorDeclarationValidation/fields_1"]),
 "06": ("semantic", "^BenchmarkFindClosestString$/^long-target$", ["BenchmarkFindClosestString/long-target"]),
 "07": ("semantic", "^BenchmarkReachableEventEmits$/^helpers_1$", ["BenchmarkReachableEventEmits/helpers_1"]),
 "08": ("semantic", "^BenchmarkScopeLookup$/^depth32$/^missing$", ["BenchmarkScopeLookup/depth32/missing"]),
 "09": ("semantic", "^BenchmarkScopeLookup$/^depth32$/^parent$", ["BenchmarkScopeLookup/depth32/parent"]),
}
DIRECT = ("03", "05")
ORDER_KEYS = ("group", "pair", "comparison", "case", "side", "revision", "variant", "raw")
SCHEDULE_KEYS = (*ORDER_KEYS, "package", "filter")

def require(condition, message):
    if not condition:
        raise ValueError(message)



def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")



def write_tsv(path, rows):
    path.write_text("".join("\t".join(map(str, row)) + "\n" for row in rows))



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



def archive(repo, revision, destination):
    data = subprocess.check_output(["git", "-C", str(repo), "archive", revision])
    with tarfile.open(fileobj=io.BytesIO(data)) as stream:
        for member in stream.getmembers():
            require(not member.name.startswith("/") and ".." not in Path(member.name).parts,
                    "archive path escapes the owned source directory")
        stream.extractall(destination)



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


def pins():
    value = json.loads((ASSETS / "pins.json").read_text())
    for name, digests in value["payloads"].items():
        encoded = (ASSETS / (name + ".patch.b64")).read_bytes()
        require(sha256(encoded).hexdigest() == digests["encodedSHA256"], name + " encoded payload drift")
        decoded = base64.b64decode(b"".join(encoded.split()), validate=True)
        require(sha256(decoded).hexdigest() == digests["decodedSHA256"], name + " decoded payload drift")
    return value


def comparisons(suite):
    require(suite in ("codegen", "semantic"), "unknown suite")
    return [("original-aa", "base", "base"), ("official-ah", "base", "official-head"),
            ("p1-aa", "p1-control", "p1-control"), ("p1-ab", "p1-control", "p1-head")]


def cases(suite):
    return [key for key, value in CASES.items() if value[0] == suite]


def inventory(suite):
    return [(role, case) for role in pins()["roles"] for case in cases(suite)]


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
                    for side, role in roles:
                        raw = f"raw/{group}.{pair:02}.{comparison}.{case}.{side}.txt"
                        yield dict(zip(SCHEDULE_KEYS, (group, f"{pair:02}", comparison, case, side,
                                                      role, "default", raw, "pkg/" + suite, CASES[case][1])))


def expected(suite, selected):
    values = [{"Package": "pkg/" + suite, "Name": leaf + "-2"}
              for case in selected for leaf in CASES[case][2]]
    return {"Base": values, "Head": values}


def plan(directory, suite):
    directory.mkdir(parents=True, exist_ok=True)
    rows = list(schedule(suite))
    require(len(rows) == (320 if suite == "codegen" else 1120), "process count drift")
    write_tsv(directory / "schedule.tsv", ([row[key] for key in SCHEDULE_KEYS] for row in rows))
    write_tsv(directory / "inventory.tsv", ([role, case, "pkg/" + suite, "default", CASES[case][1]]
                                         for role, case in inventory(suite)))
    write_tsv(directory / "comparisons.tsv", comparisons(suite))
    for comparison, _, _ in comparisons(suite):
        write_json(directory / (comparison + ".expected.json"), expected(suite, cases(suite)))
    if suite == "semantic":
        write_json(directory / "p1-direct.expected.json", expected(suite, DIRECT))
    write_json(directory / "plan.json", {"suite": suite, "processArms": len(rows),
                                        "inventoryArms": len(inventory(suite)),
                                        "leavesPerComparison": len(cases(suite)), "sourcePins": pins()})
    print(f"{suite}: {len(rows)} process arms; {len(cases(suite))} original leaves per comparison")


def check_source(source, role, output):
    value = pins()
    tree_hash.root = source
    actual = tree_hash(source).hex()
    require(actual == value["roles"][role]["tree"], role + " complete source tree drift")
    manifest = source_manifest(source)
    for name, digest in value["fixtureSHA256"].items():
        require(manifest[name] == digest, role + " original benchmark/helper drift: " + name)
    tests = {str(path.relative_to(source)): sha256(path.read_bytes()).hexdigest()
             for package in ("codegen", "semantic") for path in (source / "pkg" / package).glob("*_test.go")}
    require(tests == value["packageTestSHA256"][role], role + " complete test-input closure drift")
    write_json(output, {"role": role, "tree": actual, "filesSHA256": manifest, "testSHA256": tests})


def apply_patch(source, payload, include=None):
    options = [] if include is None else ["--include=" + include]
    env = dict(os.environ, GIT_CEILING_DIRECTORIES=str(source.parent))
    for check in (["--check", "--whitespace=error-all"], []):
        subprocess.run(["git", "apply", *check, *options, str(payload)], cwd=source, env=env, check=True)


def prepare(repo, parent, evidence):
    value = pins()
    require(parent.is_dir() and not any(parent.iterdir()), "owned source parent must be empty")
    evidence.mkdir(parents=True, exist_ok=True)
    payloads = {}
    for name in value["payloads"]:
        payloads[name] = evidence / (name + ".patch")
        encoded = (ASSETS / (name + ".patch.b64")).read_bytes()
        payloads[name].write_bytes(base64.b64decode(b"".join(encoded.split()), validate=True))
    baseline, head, control, candidate = [parent / role for role in value["roles"]]
    baseline.mkdir()
    archive(repo, value["base"], baseline)
    shutil.copytree(baseline, head, symlinks=True)
    apply_patch(head, payloads["official"])
    shutil.copytree(head, control, symlinks=True)
    apply_patch(control, payloads["p1"], value["p1TestPath"])
    shutil.copytree(control, candidate, symlinks=True)
    apply_patch(candidate, payloads["p1"], value["p1ProductionPath"])
    for left, right, path in ((head, control, value["p1TestPath"]),
                              (control, candidate, value["p1ProductionPath"])):
        before, after = source_manifest(left), source_manifest(right)
        delta = {name for name in before.keys() | after.keys() if before.get(name) != after.get(name)}
        require(delta == {path}, "P1 role changed more than its exact one-file scope")
    for role in value["roles"]:
        check_source(parent / role, role, evidence / ("source-" + role + ".before.json"))
    require(value["packageTestSHA256"]["p1-control"] == value["packageTestSHA256"]["p1-head"],
            "P1 arms must have byte-identical tests")
    write_json(evidence / "source-roles.json", value["roles"])
    print("Exact original A/H source trees and symmetric-test C/P1 trees verified")


def derive_direct(directory):
    target = directory / "derived"
    target.mkdir(exist_ok=True)
    for comparison in ("p1-aa", "p1-ab"):
        for group in GROUPS:
            for side in ("left", "right"):
                selected = [row for row in schedule("semantic") if row["comparison"] == comparison
                            and row["group"] == group and row["side"] == side and row["case"] in DIRECT]
                require(len(selected) == 20, "direct process count drift")
                data = b"".join((directory / row["raw"]).read_bytes() for row in selected)
                (target / f"{comparison}.{group}.{side}.raw.txt").write_bytes(data)
    print("Derived direct-path views from the same measured processes; no resampling")


def csv_cells(path):
    package = unit = None
    result = {}
    for row in reader(io.StringIO(path.read_text())):
        if len(row) == 1 and row[0].startswith("pkg: "):
            package, unit = row[0][5:].strip(), None
        if len(row) != 7:
            continue
        if not row[0] and row[1] in ("sec/op", "B/op", "allocs/op"):
            unit = row[1]
        if not row[0] or row[0] == "geomean" or unit is None:
            continue
        require(row[0].endswith("-2") and row[-1].endswith("n=10"), "unexpected CSV sample count")
        key = (package, row[0], unit)
        require(key not in result, "duplicate CSV metric")
        result[key] = {"base": float(row[1]), "head": float(row[3]), "change": row[5], "p": row[6]}
    return result


def direct_outcome(status, observations):
    return status["aa"] == 0 and status["ab"] == 0 and all(
        item["significantImprovementBothGroups"] for item in observations)


def report_direct(directory):
    status = json.loads((directory / "p1-direct-status.json").read_text())
    tables = {group: csv_cells(directory / f"p1-direct-ab.{group}.csv") for group in GROUPS}
    observations = []
    for case in DIRECT:
        leaf = CASES[case][2][0].removeprefix("Benchmark") + "-2"
        groups = {group: tables[group][(PREFIX + "pkg/semantic", leaf, "sec/op")] for group in GROUPS}
        improved = all(cell["head"] < cell["base"] and cell["change"].startswith("-")
                       for cell in groups.values())
        observations.append({"benchmark": "Benchmark" + leaf, "groups": groups,
                             "significantImprovementBothGroups": improved})
    established = direct_outcome(status, observations)
    write_json(directory / "p1-independent-evidence.json", {
        "aaGateExit": status["aa"], "abGateExit": status["ab"], "directObservations": observations,
        "independentImprovementEstablished": established,
        "outcome": "established" if established else "not-established",
        "otherSevenDoNotDecideThisOutcome": True, "historicalOfficialGateStillOpen": True,
        "freshOfficialAHDoesNotEraseOriginalFailures": True,
        "scopeOnlyNotAFullCallableArityOrReleaseAcceptance": True,
        "bothP1ArmsHaveIdenticalAdditionalTests": True,
    })
    print("P1 direct-path improvement established:", established)


def historical_files(directory):
    for side in ("base", "head"):
        for suite in ("codegen", "semantic"):
            text = (directory / "benchmark-inventory" / side / ("pkg-" + suite + ".raw.log")).read_text()
            names = {line.split()[0] for line in text.splitlines() if line.startswith("Benchmark")}
            wanted = {CASES[key][2][0] + "-2" for key in cases(suite)}
            require(wanted <= names, side + " missing original actual benchmark inventory")
    print("All nine exact original benchmark leaves exist on both actual official sides")


if __name__ == "__main__":
    command, *args = sys.argv[1:]
    if command == "plan":
        plan(Path(args[0]), args[1])
    elif command == "prepare":
        prepare(Path(args[0]).resolve(), Path(args[1]).resolve(), Path(args[2]).resolve())
    elif command == "source-check":
        check_source(Path(args[0]).resolve(), args[1], Path(args[2]))
    elif command in ("inventory", "samples"):
        verify(Path(args[0]), args[1], command == "samples")
    elif command == "derive-direct":
        derive_direct(Path(args[0]))
    elif command == "report-direct":
        report_direct(Path(args[0]))
    elif command == "historical-files":
        historical_files(Path(args[0]))
    else:
        raise SystemExit("unknown PR93 P1 diagnostic command")
