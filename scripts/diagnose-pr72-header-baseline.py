#!/usr/bin/env python3

import csv
import hashlib
import json
import re
import subprocess
import sys
from decimal import Decimal
from pathlib import Path

BASE = "a75a0acce23b30e3f99d44b0c47f746901b82bb8"
HEAD = "5e4c9d1b3813cc95f98d4088a10c242dca47b8de"
MERGE = "056bdab72888c733b84aeb6f0d169a7458e17bf8"
TREES = {
    "base": "d119706aff9f4b3f0564f3725fa491953408a124",
    "test": "68a0c25a02b57827b729888d8254a5a0cbbcbb26",
    "head": "4e0c5bc89439be2f1374db2f306ec261bdccc382",
}
COMPARISONS = {"aa": ("base", "base"), "ab": ("base", "head"),
               "at": ("base", "test"), "tb": ("test", "head")}
FIELDS = ("group", "pair", "comparison", "case", "side", "revision", "variant", "raw")
UNITS = ("ns/op", "B/op", "allocs/op")
CODEC = "pkg/lux/codec"
LUVIA = "pkg/lux/luvia"
BYTES_TEST = f"{CODEC}/bytes_test.go"
PLANNER_TEST = f"{LUVIA}/planner_test.go"
PLANNER_OVERLAY = f"{LUVIA}/planner_selection_diagnostic_test.go"
PLANNER_SHA = "0228ad8f89907ac82d204724e61692047ebb35bb32743bf17db4935b37905d65"
INPUTS = {
    "scripts/diagnose-hosted-perf.sh": "9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072",
    "scripts/diagnose-pr72-rpc-context-inline.sh": "2a3330be68e16ffa98958a48f4feb79197ed173350a2b07d36c0d004ce65dd86",
}
FILES = {
    f"{CODEC}/array_write_bench_test.go": "14807acdbef627443873f3c0899becbd924e7533a122c63f7486957f6ea900e2",
    f"{CODEC}/payload_bench_test.go": "b254012969b1c2d19d626217f3bb53c571325f03c3bccffef7ea5a871303d76b",
    f"{CODEC}/metadata_bench_test.go": "cfcd24ce859d2cb52b196d967cf6150bfb6524f02c2439cb85ef563f7e7cc0b1",
    f"{CODEC}/selectionmask_bench_test.go": "6a295b52e4800e3a65d5f61b072b8e84aae6f0c05188f2ebae36c1f4aa0dc87a",
    f"{CODEC}/encoder.go": "aadf8176a9486f6161c2be882781bb54a8257df459c9300346edde1cdea40942",
    f"{CODEC}/decoder.go": "3b85c1d1d0361e2d0701a1018a82a9b40a743bc5923bcd0c487cf92a387156ff",
    f"{CODEC}/columnar.go": "4c19604d506fdf3e5e8aa32e8728c7d84ab3dc61c4a38e5840a2c8dad979f6e1",
    "go.mod": "4c338424c45e5c076fc86297d61cc4db847acf2850a233016188ae1f1aa39687",
    "go.sum": "1b169fcbace676d76ec5bf05a68474f05720916e38ca5c5bef67e5eafc724cd2",
    "scripts/setup-modules.sh": "b65bf113bc00602cf04c43f7678031cd9286b6d618b2a5a73878d1017d711dcb",
}
CHANGED = {
    BYTES_TEST: ("cee978ab5bc5b16d81a8d53d99b6a27653b94a6bf765cc5c62e26da69a173443",
                 "f6fb8e29aa3648581ffec29ba5a281a197b33de3f94c920aaeb53f30d4d35065"),
    f"{CODEC}/array_write_test.go": ("63c1e136a50371db768100ba8965612de9921081d087179c4ab253a091ab25b8",
                                    "cf469513e09fff2ab49211c17476d4a6ee5e6d960b9184ce8403d2335b939e36"),
    f"{CODEC}/wire.go": ("cac2dd90bdf4e61ebd4c83cbe5df5bfc31e10df9e0a5f7a2b8e9d027cff8562c",
                       "c8cab050daf5809e968b196888fed1f459fb9575392b44e08faa430ed88c9c54"),
    f"{CODEC}/selectionmask.go": ("fcc3e1323ebef2522aa55c7d20325ff834cfe8376edf7d716527f27a12087be4",
                                "85b89c473e4f814c015d86b840d64cc995e7e41a15c4b0e7c57c7e1f4968bd17"),
    f"{LUVIA}/planner.go": ("f5b2c645ca51c949767be42763800783a19f69cbc14fb0c47aedbca29ed03096",
                          "39bb1a13d77e0355c6538469a5e96259ed85f809588699ea6778e45f5d36b66d"),
    PLANNER_TEST: ("5ff89937cd98359dfab84ee886c3c9e5d454652c06f1b879338686084dfffe84",
                   "b6c167a7901aad28d8535ec431ea9f138dc21ea31a990a6c3567c7d8592d3481"),
}
CASES = (
    ("01", CODEC, "04", tuple(f"ArrayHeaderWrite/{count}" for count in (0, 128, 1000000, 1000001, -1))),
    ("02", CODEC, "01", tuple(f"ElementPayload/{name}/column=true/count={count}/version={version}"
                               for name, count in (("bool-nullable", 0), ("float-nullable", 128), ("int-nullable", 128))
                               for version in range(2))),
    ("03", CODEC, "09", ("SelectionMaskLimits/empty/before", "SelectionMaskLimits/empty/after")),
    ("04", CODEC, "09", tuple(f"WireReadOffsetBounds/{name}"
                               for name in ("nullable", "float", "varint-short", "varint-long"))),
    ("05", LUVIA, "11", ("PlanSelection/all", "PlanSelection/nested")),
)


def run_git(*args):
    return subprocess.check_output(["git", *map(str, args)], text=True).strip()


def digest(data):
    return hashlib.sha256(data).hexdigest()


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")


def check_file(path, wanted):
    actual = digest(path.read_bytes())
    if actual != wanted:
        raise SystemExit(f"source SHA-256 mismatch: {path}: {actual}")


def prepare(repo, scratch, evidence):
    for path, fingerprint in INPUTS.items():
        check_file(repo / path, fingerprint)
    store = scratch / "source.git"
    run_git("-C", repo, "init", "--bare", "--quiet", store)
    objects = run_git("-C", repo, "rev-parse", "--path-format=absolute", "--git-path", "objects")
    (store / "objects/info/alternates").write_text(objects + "\n")
    verify_trees(store)
    source = scratch / "source"
    source.mkdir()
    for role, tree in TREES.items():
        dest = source / role
        dest.mkdir()
        subprocess.run(
            ["bash", "-o", "pipefail", "-c", 'git --git-dir="$1" archive "$2" | tar -xf - -C "$3"',
             "archive-role", str(store), tree, str(dest)],
            check=True,
        )
    write_json(evidence / "revisions.json", {
        "officialRun": 45315178644, "attempt": 1, "base": BASE, "head": HEAD, "synthetic": MERGE, "trees": TREES,
        "singleTestOverlay": BYTES_TEST, "plannerOverlayFor": ["base", "test"], "plannerOverlaySHA256": PLANNER_SHA,
        "historicalSymmetricProbe": {
            "run": 45269791268, "artifact": 14865407539,
            "sha256": "ab4ea5242c41929ac9b4313b0c8dc42ce5f4680b2b0ce712ce782b39a11cda12",
        },
        "historicalOfficialArtifacts": {
            "01": [14885933348, "sha256:3a27e5f869150a3d89f7f76aa98a5d121946f07089dcb02527e9a852ba08717e"],
            "04": [14886277597, "sha256:39027c771f52a4d676344850621e0f9e4849a92263387ac2a309982fe36f658b"],
            "09": [14886488238, "sha256:91940f5a8e8c7da7060dc57e9da94b7ce1735d33fb0a77f11bc0ff2f6b07f949"],
            "11": [14887200535, "sha256:7614df491dc58c080d328ce242cc54eb169d7b7f7ab6d0488690b544d05b9bf5"],
        },
    })


def verify_trees(store):
    git_dir = f"--git-dir={store}"
    if run_git(git_dir, "rev-parse", f"{BASE}^{{tree}}") != TREES["base"]:
        raise SystemExit("official baseline tree mismatch")
    projected = run_git(git_dir, "merge-tree", "--write-tree", BASE, HEAD)
    if projected != TREES["head"] or run_git(git_dir, "rev-parse", f"{HEAD}^{{tree}}") != projected:
        raise SystemExit("exact official merge tree mismatch")
    run_git(git_dir, "read-tree", BASE)
    blob = run_git(git_dir, "rev-parse", f"{HEAD}:{BYTES_TEST}")
    run_git(git_dir, "update-index", "--cacheinfo", f"100644,{blob},{BYTES_TEST}")
    if run_git(git_dir, "write-tree") != TREES["test"] or run_git(
        git_dir, "diff-index", "--cached", "--name-only", BASE
    ) != BYTES_TEST:
        raise SystemExit("baseline fixture variant must differ in one test file")


def source_function(path, name):
    raw = path.read_bytes()
    matches = re.findall(rb"^func " + name.encode() + rb"\(.*?^}\n", raw, re.M | re.S)
    if len(matches) != 1:
        raise SystemExit(f"pinned function {name} missing from {path}")
    return matches[0]


def sources(scratch, evidence):
    archive = scratch / "source"
    for role in TREES:
        source = archive / role
        files = dict(FILES)
        for path, (before, after) in CHANGED.items():
            files[path] = after if role == "head" or (role == "test" and path == BYTES_TEST) else before
        if role in ("base", "test"):
            files[PLANNER_OVERLAY] = PLANNER_SHA
        for path, fingerprint in files.items():
            check_file(source / path, fingerprint)
        for function in ("ReadBytes", "ReadVarint", "ReadNullable"):
            reference = archive / "base" / CODEC / "wire.go"
            if source_function(source / CODEC / "wire.go", function) != source_function(reference, function):
                raise SystemExit(f"guard source changed for {function}")
        for helper in ("selectionNode", "userModelWithExtend"):
            if source_function(source / PLANNER_TEST, helper) != source_function(archive / "head" / PLANNER_TEST, helper):
                raise SystemExit(f"planner fixture helper mismatch: {helper}")
        manifest = "".join(f"{value}  {path}\n" for path, value in sorted(files.items()))
        (evidence / f"sources-{role}.sha256").write_text(manifest)


def make_plan(evidence):
    leaves, rows = [], []
    for ident, package, shard, items in CASES:
        names = tuple(f"Benchmark{item}-2" for item in items)
        filters = "|".join("/".join(f"^{re.escape(segment)}$" for segment in f"Benchmark{item}".split("/"))
                           for item in items)
        rows.append((ident, package, "default", shard, filters))
        leaves.extend({"Package": package, "Name": name} for name in names)
    if len(leaves) != 19 or len({(item["Package"], item["Name"]) for item in leaves}) != 19:
        raise SystemExit("expected 19 distinct benchmark leaves")
    (evidence / "cases.tsv").write_text("".join("\t".join(row) + "\n" for row in rows))
    write_json(evidence / "expected.json", {"Base": leaves, "Head": leaves})


def schedule():
    keys = tuple(COMPARISONS)
    for offset, group in enumerate(("primary", "confirmation")):
        for pair in range(1, 11):
            sides = (0, 1) if (pair + offset) % 2 else (1, 0)
            for ident, _, _, _ in CASES:
                start = (pair + offset + int(ident)) % len(keys)
                for key in keys[start:] + keys[:start]:
                    for arm in sides:
                        side = ("left", "right")[arm]
                        role = COMPARISONS[key][arm]
                        raw = f"raw/{group}.{pair:02}.{key}.{ident}.{side}.txt"
                        yield (group, f"{pair:02}", key, ident, side, role, "default", raw)


def verify_order(evidence):
    with (evidence / "order.tsv").open(newline="") as stream:
        rows = csv.reader(stream, delimiter="\t")
        if next(rows) != list(FIELDS):
            raise SystemExit("measurement order header mismatch")
        actual = list(map(tuple, rows))
    if len(actual) != 800 or actual != list(schedule()):
        raise SystemExit("measurement process schedule mismatch")
    print(f"Verified all {len(actual)} planned process arms")
    return actual


def read_process(path, case):
    _, package, _, items = case
    wanted = {f"Benchmark{item}-2" for item in items}
    names, header = {}, {}
    passes = 0
    for line in path.read_text().splitlines():
        key, _, value = line.partition(": ")
        if key in ("goos", "goarch", "pkg", "cpu"):
            if key in header:
                raise SystemExit(f"duplicate process metadata in {path}")
            header[key] = value
        elif line.startswith("Benchmark"):
            fields = line.split()
            if len(fields) != 8 or tuple(fields[3::2]) != UNITS:
                raise SystemExit(f"unexpected benchmark metrics: {path}: {line}")
            name, iterations = fields[:2]
            metrics = tuple(Decimal(number) for number in fields[2::2])
            if name in names or int(iterations) <= 0 or any(not n.is_finite() or n < 0 for n in metrics):
                raise SystemExit(f"invalid benchmark row: {path}: {line}")
            names[name] = metrics
        elif line == "PASS":
            passes += 1
        elif line.strip():
            raise SystemExit(f"unexpected benchmark output: {path}: {line}")
    if passes != 1 or set(names) != wanted or header.get("goos") != "linux" or header.get("goarch") != "amd64":
        raise SystemExit(f"wrong process contents: {path}")
    if header.get("pkg") != f"github.com/light-speak/luxo/{package}" or not header.get("cpu"):
        raise SystemExit(f"wrong process package or CPU: {path}")
    return header["cpu"], names


def read_inventory(evidence):
    cpus = {read_process(evidence / f"inventory-{role}-{case[0]}.txt", case)[0]
            for role in TREES for case in CASES}
    if len(cpus) != 1:
        raise SystemExit(f"inventory used multiple CPUs: {cpus}")
    return cpus.pop()


def samples(evidence):
    cpu = read_inventory(evidence)
    cases = {case[0]: case for case in CASES}
    pairs = {}
    for group, pair, comparison, ident, side, _, _, raw in verify_order(evidence):
        observed_cpu, names = read_process(evidence / raw, cases[ident])
        if observed_cpu != cpu:
            raise SystemExit(f"runner CPU changed during sampling: {observed_cpu}")
        pairs.setdefault((group, pair, comparison, ident), {})[side] = names
    with (evidence / "signed-pairs.tsv").open("w", newline="") as stream:
        table = csv.writer(stream, delimiter="\t", lineterminator="\n")
        table.writerow(("group", "pair", "comparison", "case", "benchmark", "unit", "left", "right", "signed_delta"))
        for keys, sides in pairs.items():
            for benchmark, left in sides["left"].items():
                for unit, a, b in zip(UNITS, left, sides["right"][benchmark]):
                    table.writerow((*keys, benchmark, unit, a, b, b - a))


if __name__ == "__main__":
    command, *arguments = sys.argv[1:]
    actions = {"plan": make_plan, "prepare": prepare, "sources": sources, "order": verify_order,
               "inventory": lambda path: print(read_inventory(path)), "samples": samples}
    actions[command](*map(Path, arguments))
