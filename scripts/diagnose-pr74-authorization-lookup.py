#!/usr/bin/env python3

import csv
import hashlib
import itertools
import json
import runpy
import subprocess
import sys
from pathlib import Path

DIRECTORY = Path(__file__).resolve().parent
PEER = DIRECTORY / "diagnose-pr74-jwt-scalar.py"
PEER_PINS = DIRECTORY / "diagnose-pr74-jwt-scalar" / "pins.json"
HEADER = DIRECTORY / "diagnose-pr74-authorization-lookup.json"
BASE = "a75a0acce23b30e3f99d44b0c47f746901b82bb8"
TREES = {
    "base": "d119706aff9f4b3f0564f3725fa491953408a124",
    "jwt": "f17cbe3647afac8511ea03b474e79259a3b4a9c1",
    "control": "3a7a3fa68114bca06499a952cea96366a7131315",
    "product": "71021390d873ddd31139bedc5ea98a660c0aa42e",
}
ROLES = {
    "aa": ("base", "base"), "ap": ("base", "product"),
    "hh": ("control", "control"), "hp": ("control", "product"),
}
KIND = ("anonymous-http", "anonymous-get", "bearer-http", "bearer-get", "bearer-ws", "query-ws")
BENCH = "BenchmarkAuthUpgradeHeaders"
CASE = {"package": "pkg/lux/luvia", "leaves": [f"{BENCH}/{kind}/current-2" for kind in KIND]}
FILTER = f"^{BENCH}$/^({'|'.join(KIND)})$/^current$"
MIDDLEWARE = "pkg/lux/luvia/middleware.go"
FIXTURE = "pkg/lux/luvia/middleware_upgrade_bench_test.go"
PEER_SOURCES = {
    PEER: "5a6aca47a91e2c9890ea53a50fd6c09250ec6421d7a9c4933d5f51c0cdb6eaf7",
    PEER_PINS: "528eee7009ce933696747e71f1563e37a3679a48f42b9cefc0150de4c05ed8c1",
    DIRECTORY / "diagnose-hosted-perf.sh": "9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072",
}


def check_helper_sources():
    for path, expected in PEER_SOURCES.items():
        if hashlib.sha256(path.read_bytes()).hexdigest() != expected:
            raise SystemExit(f"unexpected helper source: {path}")


check_helper_sources()
shared = runpy.run_path(str(PEER))
check_digest = shared["check_digest"]
git = shared["git"]
private_git = shared["private_git"]
read_process = shared["read_process"]
write_json = shared["write_json"]
PINS = shared["PINS"]
HEADER_PATCH = json.loads(HEADER.read_text())


def write_patches(evidence):
    jwt = PINS["patches"]["jwt"]
    patches = [evidence / "jwt-only.patch", evidence / "authorization-lookup.patch"]
    patches[0].write_bytes(jwt["text"].encode())
    patches[1].write_bytes(("\n".join(HEADER_PATCH["lines"]) + "\n").encode())
    check_digest(patches[0], "23fabe521f2cc16d07b156f93ad68162c10f9176bab600e7b33d552415620d6f")
    check_digest(patches[1], "1d088a12cb986b77bf11fd70eccc08b3159232c9e935d34bc98365bc99a2b66c")
    return patches


def verify_checkout(sources):
    for role in ("base", "control", "product"):
        source = sources / role
        check_digest(source / FIXTURE, PINS["fixtures"][FIXTURE])
        for path, digests in PINS["patches"]["jwt"]["paths"].items():
            check_digest(source / path, digests["before" if role == "base" else "after"])
        for path, digests in HEADER_PATCH["paths"].items():
            changed = role == "product" or (role == "control" and path != MIDDLEWARE)
            check_digest(source / path, digests["after" if changed else "before"])


def prepare(repo, scratch, evidence):
    base_tree = git(repo, "show", "-s", "--format=%T", BASE)
    if base_tree != TREES["base"] or PINS["base"] != BASE or PINS["trees"]["jwt"] != TREES["jwt"]:
        raise SystemExit("unexpected baseline or JWT-only pin")
    jwt, header = write_patches(evidence)
    store = scratch / "objects.git"
    git(repo, "init", "--bare", "--quiet", store)
    objects = git(repo, "rev-parse", "--path-format=absolute", "--git-path", "objects")
    (store / "objects/info/alternates").write_text(objects + "\n")
    private_git(store, "read-tree", BASE)
    for role, patch in (("jwt", jwt), ("product", header)):
        private_git(store, "apply", "--cached", "--check", "--whitespace=error", patch)
        private_git(store, "apply", "--cached", "--whitespace=error", patch)
        if private_git(store, "write-tree") != TREES[role]:
            raise SystemExit(f"unexpected {role} tree")
    mode, kind, blob, path = private_git(store, "ls-tree", TREES["jwt"], "--", MIDDLEWARE).split()
    if kind != "blob" or path != MIDDLEWARE:
        raise SystemExit("missing JWT-only middleware blob")
    private_git(store, "update-index", "--cacheinfo", f"{mode},{blob},{path}")
    if private_git(store, "write-tree") != TREES["control"]:
        raise SystemExit("unexpected matched-test control tree")
    diff = private_git(store, "diff", "--name-only", TREES["control"], TREES["product"])
    if diff != MIDDLEWARE:
        raise SystemExit(f"control differs outside the extractor: {diff}")
    for patch in (header, jwt):
        private_git(store, "read-tree", TREES["product"] if patch == header else TREES["jwt"])
        private_git(store, "apply", "--cached", "--reverse", "--whitespace=error", patch)
        expected = "jwt" if patch == header else "base"
        if private_git(store, "write-tree") != TREES[expected]:
            raise SystemExit(f"cannot reverse overlay to {expected}")

    for role in ("base", "control", "product"):
        destination = scratch / "source" / role
        destination.mkdir(parents=True)
        subprocess.run(
            ["bash", "-o", "pipefail", "-c", 'git --git-dir="$1" archive "$2" | tar -xf - -C "$3"',
             "archive-role", str(store), TREES[role], str(destination)],
            check=True,
        )
    verify_checkout(scratch / "source")
    write_json(evidence / "sources.json", {
        "baseCommit": BASE, "trees": TREES, "comparisons": ROLES, "case": CASE, "filter": FILTER,
        "jwtOnlyPatchSHA256": PINS["patches"]["jwt"]["sha256"],
        "authorizationLookupPatchSHA256": HEADER_PATCH["sha256"],
        "helpers": {str(path.relative_to(DIRECTORY)): checksum for path, checksum in PEER_SOURCES.items()},
        "timedFixture": {FIXTURE: PINS["fixtures"][FIXTURE]},
        "jwtOnlyFiles": PINS["patches"]["jwt"]["paths"], "authorizationLookupFiles": HEADER_PATCH["paths"],
        "priorOfficialRun": PINS["historicalRuns"]["correctedOfficial"], "priorAttributionRun": 45321333172,
    })


def plan(evidence):
    rows = [{"Package": CASE["package"], "Name": name} for name in CASE["leaves"]]
    write_json(evidence / "expected.json", {"Base": rows, "Head": rows})
    with (evidence / "planned-order.tsv").open("w", newline="") as stream:
        table = csv.writer(stream, delimiter="\t", lineterminator="\n")
        table.writerow(shared["FIELDS"])
        comparisons = tuple(ROLES)
        for offset, group in enumerate(shared["GROUPS"]):
            for pair in range(1, 11):
                side_order = ("left", "right") if (pair + offset) % 2 else ("right", "left")
                for index in range(len(comparisons)):
                    comparison = comparisons[(pair + offset + index) % len(comparisons)]
                    roles = dict(zip(("left", "right"), ROLES[comparison]))
                    for side in side_order:
                        raw = f"raw/{group}.{pair:02}.{comparison}.01.{side}.txt"
                        table.writerow((group, f"{pair:02}", comparison, "01", side, roles[side], "default", raw))
    print(FILTER)


def check_samples(evidence):
    with (evidence / "order.tsv").open(newline="") as stream:
        rows = list(csv.DictReader(stream, delimiter="\t"))
    if len(rows) != 160 or len({row["raw"] for row in rows}) != 160:
        raise SystemExit("expected 160 distinct measurement processes")
    cpus = {read_process(evidence / f"inventory-{role}.txt", CASE)[0] for role in ("base", "control", "product")}
    if len(cpus) != 1:
        raise SystemExit(f"multiple inventory CPUs: {cpus}")
    pairs = {}
    for row in rows:
        cpu, values = read_process(evidence / row["raw"], CASE)
        if cpu not in cpus:
            raise SystemExit(f"unexpected sample CPU: {cpu}")
        key = row["group"], row["pair"], row["comparison"]
        pairs.setdefault(key, {})[row["side"]] = values
    with (evidence / "pairs.tsv").open("w", newline="") as stream:
        table = csv.writer(stream, delimiter="\t", lineterminator="\n")
        table.writerow(("group", "pair", "comparison", "leaf", "unit", "base", "measured", "signed_delta"))
        for (group, pair, comparison), sides in pairs.items():
            for leaf, unit in itertools.product(CASE["leaves"], shared["UNITS"]):
                baseline, measured = (sides[side][leaf][unit] for side in ("left", "right"))
                table.writerow((group, pair, comparison, leaf, unit, baseline, measured, measured - baseline))
    print(next(iter(cpus)))


if __name__ == "__main__":
    action, *args = sys.argv[1:]
    actions = {"prepare": prepare, "plan": plan, "sources": verify_checkout, "samples": check_samples}
    actions[action](*map(Path, args))
