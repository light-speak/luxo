"""Verify the PR 99 benchmark plan, source archives and collected processes."""

from collections import Counter
from hashlib import sha256
from pathlib import Path
import base64
import csv
import json
import os
import re
import runpy
import shutil
import subprocess
import sys

SCRIPTS = Path(__file__).resolve().parent
ASSETS = SCRIPTS / "diagnose-pr99-codegen"
BASE = "578f507518b3fc9fede3c17930fa4b6cf9af7ea4"
HEAD = "285f59662512be8a6c33e608e8782611641e9f6a"
MERGE = "13b45287dbca1580ece6a3c01023b901824c2d6b"
TREES = {"base": "730e2e26e4d7870fb1d823f80be2bc466a80d717",
         "head": "c7c390f9ac07e19598c3aaf834b81601b91394f2"}
PACKAGE = "pkg/codegen"
TARGET = "BenchmarkMigrationStateValidation/empty/validate-2"
KEYS = ("group", "pair", "comparison", "case", "side", "revision", "variant", "raw")
FILTERS = ("primary", "confirmation")
FIXTURES = ("pkg/codegen/migrate_state.go", "pkg/codegen/migrate_state_bench_test.go")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def selections():
    result = {}
    for group, suffix in (("primary", ""), ("confirmation", "-confirmation")):
        batch = (ASSETS / ("benchmark" + suffix + "-batch.tsv")).read_text().splitlines()
        require(len(batch) == 1, "expected one original process filter")
        package, expression = batch[0].split("\t")
        require(package == PACKAGE and expression, "original process filter drift")
        expected = json.loads((ASSETS / ("benchmark" + suffix + "-expected.json")).read_text())
        require(expected["Base"] == expected["Head"], "baseline and tested inventories differ")
        names = {case["Name"] for case in expected["Base"] if case["Package"] == PACKAGE}
        require(len(names) == len(expected["Base"]) == (52 if group == "primary" else 1),
                "original benchmark inventory drift")
        result[group] = (expression, names)
    require(TARGET in result["primary"][1] and result["confirmation"][1] == {TARGET},
            "original confirmed leaf drift")
    require((ASSETS / "benchmark-confirmation-targets.tsv").read_text() == PACKAGE + "\t" + TARGET + "\n",
            "original confirmation targets drift")
    return result


def schedule():
    groups = selections()
    for offset, group in enumerate(FILTERS):
        expression, _ = groups[group]
        for pair in range(1, 11):
            order = ("aa", "ab") if (pair + offset) % 2 == 1 else ("ab", "aa")
            sides = ("left", "right") if pair % 2 == 1 else ("right", "left")
            for comparison in order:
                for side in sides:
                    revision = "head" if comparison == "ab" and side == "right" else "base"
                    raw = f"raw/{group}.{pair:02}.{comparison}.05.{side}.txt"
                    yield (group, f"{pair:02}", comparison, "05", side, revision, "default", raw,
                           PACKAGE, expression)


def plan(evidence):
    rows = list(schedule())
    require(len(rows) == 80 and len({row[7] for row in rows}) == 80, "sample schedule count drift")
    counts = Counter((row[0], row[2], row[4], row[5]) for row in rows)
    require(len(counts) == 8 and set(counts.values()) == {10}, "unbalanced sample schedule")
    (evidence / "schedule.tsv").write_text("".join("\t".join(row) + "\n" for row in rows))
    print("PR 99 plan: original primary 52 leaves, confirmation 1 leaf; 80 timed process arms")


def source_receipt(source, role):
    helper = runpy.run_path(str(SCRIPTS / "diagnose-pr93-p1.py"))
    tree_hash = helper["tree_hash"]
    tree_hash.root = source
    tree = tree_hash(source).hex()
    require(tree == TREES[role], f"{role} archive tree drift: {tree}")
    return {"tree": tree, "fixtureSHA256": {name: sha256((source / name).read_bytes()).hexdigest()
                                            for name in FIXTURES}}


def prepare(repo, source, evidence):
    git = ["git", "-C", str(repo)]
    require(subprocess.check_output(git + ["rev-parse", BASE + "^{tree}"], text=True).strip() == TREES["base"],
            "baseline commit tree drift")
    base, head = source / "base", source / "head"
    base.mkdir()
    helper = runpy.run_path(str(SCRIPTS / "diagnose-pr93-p1.py"))
    helper["archive"](repo, BASE, base)
    baseline = source_receipt(base, "base")
    shutil.copytree(base, head, symlinks=True)
    patch = ["git", "-C", str(head), "apply", "--whitespace=error"]
    environment = dict(os.environ, GIT_CEILING_DIRECTORIES=str(source))
    payload = source.parent / "official.patch"
    encoded = (ASSETS / "official.patch.b64").read_bytes()
    decoded = base64.b64decode(b"".join(encoded.split()), validate=True)
    require(sha256(decoded).hexdigest() == "f95772025899d509499981cb6dc4f286af8be6a71ae2d2c35d55258f5546fd8c",
            "decoded official patch drift")
    payload.write_bytes(decoded)
    payload = str(payload)
    subprocess.run(patch + ["--check", payload], env=environment, check=True)
    subprocess.run(patch + [payload], env=environment, check=True)
    tested = source_receipt(head, "head")
    require(baseline["fixtureSHA256"] == tested["fixtureSHA256"], "validation code or benchmark source drift")
    subprocess.run(patch + ["--reverse", payload], env=environment, check=True)
    source_receipt(head, "base")
    subprocess.run(patch + [payload], env=environment, check=True)
    source_receipt(head, "head")
    (evidence / "source.json").write_text(json.dumps({
        "base": {"revision": BASE, **baseline},
        "head": {"revision": HEAD, "testedMerge": MERGE, **tested},
        "historicalRun": 45298161121, "historicalAttempt": 1,
        "historicalShardJob": 138124674218,
        "historicalEvidence": {
            "14878037736": "047c7388f823634a77d98d833681b5a997d7c6546374e6771bdd4c75498635f3",
            "14877122932": "5117ed6986e15dba669d16dec39a792bc4e858ea5d1dceb524559415cfce87c4",
        },
    }, indent=2) + "\n")
    print("PR 99 exact baseline and tested Git tree hashes verified, including reverse replay")


def verify_process(path, names, inventory=False):
    lines = path.read_text().splitlines()
    require(lines[:3] == ["goos: linux", "goarch: amd64", "pkg: github.com/light-speak/luxo/" + PACKAGE],
            f"{path}: platform or package drift")
    require(lines[3].startswith("cpu: ") and lines[-1] == "PASS", f"{path}: missing CPU or completion")
    rows = [line.split() for line in lines[4:-1] if line.startswith("Benchmark")]
    require(Counter(row[0] for row in rows) == Counter(names), f"{path}: incomplete or extra leaves")
    for row in rows:
        require(len(row) == 8 and int(row[1]) > 0 and (not inventory or int(row[1]) == 1)
                and tuple(row[3::2]) == ("ns/op", "B/op", "allocs/op")
                and all(re.fullmatch(r"[0-9]+(?:\.[0-9]+)?", sample) for sample in row[2::2]),
                f"{path}: invalid measurement")
    return lines[3]


def check_processes(evidence, timed):
    groups = selections()
    cpus = {verify_process(evidence / f"inventory-{role}-{group}.txt", names, True)
            for role in TREES for group, (_, names) in groups.items()}
    if timed:
        planned = [dict(zip(KEYS, row[:8])) for row in schedule()]
        with (evidence / "order.tsv").open(newline="") as stream:
            observed = list(csv.DictReader(stream, delimiter="\t"))
        require(observed == planned, "recorded process order does not match the plan")
        require({str(path.relative_to(evidence)) for path in (evidence / "raw").glob("*.txt")}
                == {row["raw"] for row in planned}, "missing or extra raw process files")
        cpus.update(verify_process(evidence / row["raw"], groups[row["group"]][1]) for row in planned)
    require(len(cpus) == 1, "processes reported different CPU models")
    print(next(iter(cpus)))


def layout(source, target):
    with target.open("w") as out:
        out.write("address\tsize\tmod16\tmod32\tmod64\tmod4096\tsymbol\n")
        for line in source.read_text().splitlines():
            address, size, _, name = line.split(maxsplit=3)
            offset = int(address, 16)
            out.write("\t".join(map(str, (address, size, offset % 16, offset % 32, offset % 64,
                                          offset % 4096, name))) + "\n")


def main():
    action, *arguments = sys.argv[1:]
    paths = [Path(value) for value in arguments]
    if action == "plan":
        plan(*paths)
    elif action == "prepare":
        prepare(*paths)
    elif action == "check-sources":
        for role in TREES:
            source_receipt(paths[0] / role, role)
    elif action == "inventory":
        check_processes(paths[0], False)
    elif action == "samples":
        check_processes(paths[0], True)
    elif action == "layout":
        layout(*paths)
    else:
        raise ValueError(f"unknown action: {action}")


if __name__ == "__main__":
    main()
