"""Verify exact migration-state sources and the scoped native evidence."""

import base64
from collections import Counter
import csv
from hashlib import sha256
import json
import os
from pathlib import Path
import runpy
import subprocess
import sys

SCRIPTS = Path(__file__).resolve().parent
MAIN = "133f54d7722425b0344ec1eda73f954affa5ff5f"
TREES = {
    "main": "ee7b9c9f086bbbaa9eeabe3d68bfb87ef3a1fdae",
    "base": "2d386d50314a7e8a7881bd3c16d0de81a08546ef",
    "head": "a4fee83f04b4202bcef4c297c70a97ebd686076f",
}
PATCH_SHA = "ec80454ea557c69ddd2b0b054ae79da6620755ba479da089bac1d7f031ab99d5"
EXPECTED_SHA = "4a508ff7ee5346bdf48bbb466a56d1ff60eaeef0d29be62749ab23934e6fdbd9"
PRODUCTION = "pkg/codegen/migrate_state.go"
TEST = "pkg/codegen/migrate_state_test.go"
FIXTURE = "pkg/codegen/migrate_state_bench_test.go"
FIXTURE_SHA = "08bf4e4e370167fd7f5d91cc0e420ecb09302576a76dbb9e767202316cf8ce50"
BENCHMARK = "BenchmarkMigrationStateValidation"
GROUPS = ("primary", "confirmation")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")


def names():
    return {f"{BENCHMARK}/{size}/{operation}-2"
            for size in ("empty", "small", "large")
            for operation in ("validate", "load/reference", "load/strict", "save/reference", "save/strict")}


def plan(evidence):
    historical = json.loads((SCRIPTS / "diagnose-pr99-codegen/benchmark-expected.json").read_text())
    selected = {role: [item for item in historical[role] if item["Name"] in names()]
                for role in ("Base", "Head")}
    entries = selected["Base"]
    require(entries == selected["Head"] and len(entries) == 15
            and {item["Name"] for item in entries} == names()
            and all(item["Package"] == "pkg/codegen" for item in entries), "historical leaf scope drift")
    expected = evidence / "expected.json"
    write_json(expected, selected)
    require(sha256(expected.read_bytes()).hexdigest() == EXPECTED_SHA, "15-leaf fixture hash drift")
    (evidence / "cases.tsv").write_text(f"01\tpkg/codegen\tdefault\t{BENCHMARK}\t^{BENCHMARK}$\n")


def fingerprint(role, source, tree_hash):
    tree_hash.root = source
    result = tree_hash(source).hex()
    require(result == TREES[role], f"{role} archive tree drift: {result}")
    files = {path: sha256((source / path).read_bytes()).hexdigest() for path in (PRODUCTION, TEST, FIXTURE)}
    require(files[FIXTURE] == FIXTURE_SHA, f"{role} historical benchmark implementation drift")
    return {"tree": result, "files": files}


def sources(root):
    tree_hash = runpy.run_path(str(SCRIPTS / "diagnose-pr93-p1.py"))["tree_hash"]
    return {role: fingerprint(role, root / role, tree_hash) for role in ("base", "head")}


def prepare(repo, root, evidence):
    git = ["git", "-C", str(repo)]
    require(subprocess.check_output(git + ["rev-parse", MAIN + "^{tree}"], text=True).strip() == TREES["main"],
            "main commit/tree drift")
    helpers = runpy.run_path(str(SCRIPTS / "diagnose-pr93-p1.py"))
    encoded = (SCRIPTS / "diagnose-migration-state-empty.patch.b64").read_bytes()
    patch = base64.b64decode(b"".join(encoded.split()), validate=True)
    require(sha256(patch).hexdigest() == PATCH_SHA, "candidate patch drift")
    payload = root / "candidate.patch"
    payload.write_bytes(patch)
    environment = dict(os.environ, GIT_CEILING_DIRECTORIES=str(root))
    for role in ("base", "head"):
        source = root / role
        source.mkdir()
        helpers["archive"](repo, MAIN, source)
        fingerprint("main", source, helpers["tree_hash"])
        flags = ["--include=" + TEST] if role == "base" else []
        command = ["git", "-C", str(source), "apply", "--whitespace=error", *flags]
        for phase in (["--check"], []):
            subprocess.run(command + phase + [str(payload)], env=environment, check=True)
    actual = sources(root)
    require(actual["base"]["files"][TEST] == actual["head"]["files"][TEST], "asymmetric regression test")
    write_json(evidence / "sources.json", {
        "main": MAIN, "mainTree": TREES["main"], "patchSHA256": PATCH_SHA, **actual,
    })


def sample_order(evidence):
    with (evidence / "order.tsv").open(newline="") as source:
        rows = list(csv.DictReader(source, delimiter="\t"))
    require(len(rows) == 80 and len({row["raw"] for row in rows}) == 80, "wrong sample arm count")
    for index, group in enumerate(GROUPS):
        for pair in range(1, 11):
            offset = index * 40 + (pair - 1) * 4
            legs = rows[offset:offset + 4]
            require(all(row["group"] == group and row["pair"] == f"{pair:02}"
                        and row["case"] == "01" and row["variant"] == "default" for row in legs),
                    "sample batch order drift")
            require(Counter((row["comparison"], row["side"]) for row in legs)
                    == Counter((c, s) for c in ("aa", "ab") for s in ("left", "right")), "missing pair arm")
            sides = ["left", "right"] if (pair + index) % 2 == 1 else ["right", "left"]
            for comparison in ("aa", "ab"):
                sample = [row for row in legs if row["comparison"] == comparison]
                require([row["side"] for row in sample] == sides, "sample sides did not alternate")
            for row in legs:
                newer = row["comparison"] == "ab" and row["side"] == "right"
                require(row["revision"] == ("head" if newer else "base"), "wrong binary in pair")
    return rows


def processes(evidence, timed):
    verify = runpy.run_path(str(SCRIPTS / "diagnose-pr99-codegen.py"))["verify_process"]
    leaves = names()
    cpus = {verify(evidence / f"inventory-{role}.txt", leaves, True) for role in ("base", "head")}
    if timed:
        rows = sample_order(evidence)
        require({str(path.relative_to(evidence)) for path in (evidence / "raw").glob("*.txt")}
                == {row["raw"] for row in rows}, "missing or extra process output")
        cpus.update(verify(evidence / row["raw"], leaves) for row in rows)
    require(len(cpus) == 1, "CPU identity changed in the native run")
    print(next(iter(cpus)))


def observed_empty_time(source):
    unit, matches = "", []
    with source.open(newline="") as stream:
        for row in csv.reader(stream):
            if len(row) >= 7 and not row[0] and row[2] == "CI":
                unit = row[1]
            elif len(row) >= 7 and unit == "sec/op" and row[0] == "MigrationStateValidation/empty/validate-2":
                before, after = float(row[1]), float(row[3])
                matches.append({"baseSeconds": before, "headSeconds": after, "delta": row[5],
                                "significantReduction": row[5].startswith("-") and 0 < after < before})
    require(len(matches) == 1, "missing or duplicate empty validation time")
    return matches[0]


def qualify(evidence):
    with (evidence / "verdicts.tsv").open(newline="") as source:
        verdicts = {row["comparison"]: int(row["benchgate_exit"]) for row in csv.DictReader(source, delimiter="\t")}
    empty = {group: observed_empty_time(evidence / f"ab.{group}.csv") for group in GROUPS}
    accepted = verdicts == {"aa": 0, "ab": 0} and all(item["significantReduction"] for item in empty.values())
    result = {"qualified": accepted, "gateExit": verdicts, "emptyValidation": empty}
    write_json(evidence / "qualification.json", result)
    print(json.dumps(result, indent=2))
    return 0 if accepted else 1


if __name__ == "__main__":
    action, *arguments = sys.argv[1:]
    paths = [Path(value) for value in arguments]
    actions = {"plan": plan, "prepare": prepare, "check-sources": sources, "order": sample_order,
               "inventory": lambda evidence: processes(evidence, False), "samples": lambda evidence: processes(evidence, True),
               "qualify": qualify}
    status = actions[action](*paths)
    sys.exit(status if isinstance(status, int) else 0)
