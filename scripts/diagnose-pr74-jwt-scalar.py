#!/usr/bin/env python3

import csv
import hashlib
import itertools
import json
import re
import subprocess
import sys
from decimal import Decimal
from pathlib import Path

ROLES = ("base", "jwt", "scalar", "combined")
COMPARISONS = ("aa", "aj", "as", "ac")
GROUPS = ("primary", "confirmation")
HEAD = dict(zip(COMPARISONS, ROLES))
PIN_FILE = Path(__file__).with_name("diagnose-pr74-jwt-scalar") / "pins.json"
PINS = json.loads(PIN_FILE.read_text())
FIELDS = ("group", "pair", "comparison", "case", "side", "revision", "variant", "raw")
UNITS = ("ns/op", "B/op", "allocs/op")
MODULE = "github.com/light-speak/luxo"


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")


def check_digest(path, expected):
    actual = hashlib.sha256(path.read_bytes()).hexdigest()
    if actual != expected:
        raise SystemExit(f"SHA-256 mismatch: {path}: {actual} != {expected}")


def git(repo, *args):
    return subprocess.check_output(["git", "-C", str(repo), *map(str, args)], text=True).strip()


def private_git(store, *args):
    return subprocess.check_output(["git", f"--git-dir={store}", *map(str, args)], text=True).strip()


def check_historical_fixtures(store):
    revisions = dict(PINS["historicalSources"])
    for label, projection in PINS["historicalProjections"].items():
        tree = private_git(store, "merge-tree", "--write-tree", *projection["parents"])
        if tree != projection["tree"]:
            raise SystemExit(f"unexpected historical {label} tree: {tree}")
        revisions[f"{label}SampleHead"] = tree
    for revision in revisions.values():
        for path, expected in PINS["fixtures"].items():
            data = subprocess.check_output(["git", f"--git-dir={store}", "show", f"{revision}:{path}"])
            if hashlib.sha256(data).hexdigest() != expected:
                raise SystemExit(f"historical benchmark source differs: {revision}:{path}")


def prepare(repo, scratch, evidence):
    for path, digest in (PINS["sampler"] | PINS["archivedControl"]).items():
        check_digest(repo / path, digest)
    (evidence / "pins.json").write_bytes(PIN_FILE.read_bytes())

    base = PINS["base"]
    if git(repo, "show", "-s", "--format=%T", base) != PINS["trees"]["base"]:
        raise SystemExit("wrong base tree")
    store = scratch / "objects.git"
    git(repo, "init", "--bare", "--quiet", store)
    alternates = store / "objects/info/alternates"
    alternates.write_text(git(repo, "rev-parse", "--path-format=absolute", "--git-path", "objects") + "\n")

    patch_files = {}
    for name, patch in PINS["patches"].items():
        path = evidence / f"{name}.patch"
        path.write_bytes(patch["text"].encode())
        check_digest(path, patch["sha256"])
        patch_files[name] = path

    check_historical_fixtures(store)
    prepare_roles(store, scratch, evidence, patch_files)
    copy_fixture_sources(repo, evidence)
    verify_sources(scratch / "source")


def prepare_roles(store, scratch, evidence, patch_files):
    base = PINS["base"]
    sources = scratch / "source"
    sources.mkdir()
    role_paths = {"base": (), "jwt": ("jwt",), "scalar": ("scalar",), "combined": ("jwt", "scalar")}
    projection = private_git(store, "merge-tree", "--write-tree", base, PINS["originalPR"])
    if projection != PINS["trees"]["combined"]:
        raise SystemExit(f"combined tree differs from the original PR projection: {projection}")

    with (evidence / "roles.tsv").open("w") as stream:
        stream.write("role\tbase_commit\tarchive_tree\n")
        for role, overlays in role_paths.items():
            private_git(store, "read-tree", base)
            for name in overlays:
                private_git(store, "apply", "--cached", "--check", "--whitespace=error", patch_files[name])
                private_git(store, "apply", "--cached", "--whitespace=error", patch_files[name])
            tree = private_git(store, "write-tree")
            if tree != PINS["trees"][role]:
                raise SystemExit(f"unexpected {role} tree: {tree}")
            dest = sources / role
            dest.mkdir()
            subprocess.run(
                ["bash", "-o", "pipefail", "-c", 'git --git-dir="$1" archive "$2" | tar -xf - -C "$3"',
                 "archive-role", str(store), tree, str(dest)],
                check=True,
            )
            for name in reversed(overlays):
                private_git(store, "apply", "--cached", "--reverse", "--whitespace=error", patch_files[name])
            if private_git(store, "write-tree") != PINS["trees"]["base"]:
                raise SystemExit(f"failed to reverse {role} patches")
            stream.write(f"{role}\t{base}\t{tree}\n")


def copy_fixture_sources(repo, evidence):
    for source in PINS["archivedControl"]:
        path = repo / source
        filename = "integer.go.txt" if path.name == "diagnose-json-int.go.txt" else path.name
        (evidence / filename).write_bytes(path.read_bytes())


def verify_sources(sources):
    for role in ROLES:
        source = sources / role
        for path, digest in PINS["fixtures"].items():
            check_digest(source / path, digest)
        for name, patch in PINS["patches"].items():
            revision = "after" if role in (name, "combined") else "before"
            for path, hashes in patch["paths"].items():
                check_digest(source / path, hashes[revision])


def make_cases(evidence):
    cases = []
    source_kinds = {}
    leaves = {}
    for index, pin in enumerate(PINS["cases"], 1):
        group = pin["origin"].split("-")[0]
        context = "behavior" if group == "behavior" else "direct" if group in ("direct", "existing") else "historical"
        names = pin["leaves"]
        filter_value = "|".join(
            "/".join(f"^{re.escape(section)}$" for section in name.removesuffix("-2").split("/"))
            for name in names
        )
        if any(not name.startswith("Benchmark") or not name.endswith("-2") for name in names):
            raise SystemExit(f"bad Go CPU2 leaf names: {names}")
        case = dict(pin, id=f"{index:02}", context=context, filter=filter_value)
        cases.append(case)
        source_kinds[pin["origin"]] = source_kinds.get(pin["origin"], 0) + len(names)
        leaves.setdefault(context, []).extend({"Package": pin["package"], "Name": name} for name in names)
    if source_kinds != {"official-repeat": 6, "official-new": 2, "official-auth": 2, "historical-duration": 2,
                        "existing-jwt": 1, "direct-integer": 3, "direct-config": 3, "behavior-config": 1}:
        raise SystemExit(f"wrong evidence scope: {source_kinds}")
    if len({(leaf["Package"], leaf["Name"]) for rows in leaves.values() for leaf in rows}) != 20:
        raise SystemExit("expected 20 distinct timed leaves including the separate behavior probe")
    write_json(evidence / "cases.json", cases)
    (evidence / "cases.tsv").write_text("".join(
        "\t".join((case["id"], case["package"], case["variant"], case["leaves"][0], case["filter"], case["context"])) + "\n"
        for case in cases
    ))
    for context in ("historical", "direct"):
        write_json(evidence / f"{context}.expected.json", {"Base": leaves[context], "Head": leaves[context]})


def schedule(cases):
    for offset, group in enumerate(GROUPS):
        for sample in range(1, 11):
            pair = f"{sample:02}"
            sides = ("left", "right") if (sample + offset) % 2 else ("right", "left")
            for case, side_index in itertools.product(cases, range(4)):
                start = sample + offset + int(case["id"])
                comparison = COMPARISONS[(start + side_index) % len(COMPARISONS)]
                for side in sides:
                    role = "base" if side == "left" else HEAD[comparison]
                    filename = f"raw/{group}.{pair}.{comparison}.{case['id']}.{side}.txt"
                    yield (group, pair, comparison, case["id"], side, role, case["variant"], filename)


def checked_order(evidence, cases):
    with (evidence / "order.tsv").open(newline="") as stream:
        table = csv.reader(stream, delimiter="\t")
        if next(table) != list(FIELDS):
            raise SystemExit("invalid measurement order header")
        rows = list(map(tuple, table))
    if rows != list(schedule(cases)):
        raise SystemExit("missing, duplicate or unordered measurement arm")
    print(f"Verified exact schedule: {len(rows)} processes")
    return rows


def read_process(path, case):
    fields = {}
    rows = {}
    for line in path.read_text().splitlines():
        key, _, value = line.partition(":")
        if key in ("goos", "goarch", "pkg", "cpu"):
            if key in fields:
                raise SystemExit(f"duplicate {key} in {path}")
            fields[key] = value.strip()
        elif line.startswith("Benchmark"):
            name, iterations, *elements = line.split()
            if len(elements) != 2 * len(UNITS):
                raise SystemExit(f"invalid benchmark metrics in {path}: {line}")
            metrics = {unit: Decimal(value) for value, unit in zip(elements[::2], elements[1::2])}
            if name in rows or int(iterations) <= 0 or set(metrics) != set(UNITS) or any(
                not value.is_finite() or value < 0 for value in metrics.values()
            ):
                raise SystemExit(f"invalid raw benchmark result in {path}: {line}")
            rows[name] = metrics
    expected_fields = {"goos": "linux", "goarch": "amd64", "pkg": f"{MODULE}/{case['package']}"}
    if any(fields.get(key) != value for key, value in expected_fields.items()) or not fields.get("cpu"):
        raise SystemExit(f"wrong raw process metadata in {path}: {fields}")
    if set(rows) != set(case["leaves"]):
        raise SystemExit(f"unexpected raw leaves in {path}: {list(rows)}")
    return fields["cpu"], rows


def inventories(evidence, cases):
    cpus = {read_process(evidence / f"inventory-{role}-{case['id']}.txt", case)[0]
            for role, case in itertools.product(ROLES, cases)}
    if len(cpus) != 1:
        raise SystemExit(f"multiple inventory CPUs: {cpus}")
    return cpus


def samples(evidence, cases, schedule_rows):
    cpus = inventories(evidence, cases)
    by_id = {case["id"]: case for case in cases}
    pairs = {}
    for group, pair, comparison, case_id, side, _, _, raw in schedule_rows:
        case = by_id[case_id]
        cpu, rows = read_process(evidence / raw, case)
        if cpu not in cpus:
            raise SystemExit(f"unexpected sample CPU: {cpu}")
        pairs.setdefault((group, pair, comparison, case_id), {})[side] = rows
    write_pair_deltas(evidence, by_id, pairs)


def write_pair_deltas(evidence, cases, pairs):
    for context in ("historical", "direct", "behavior"):
        with (evidence / f"{context}.pairs.tsv").open("w", newline="") as stream:
            table = csv.writer(stream, delimiter="\t", lineterminator="\n")
            table.writerow(("group", "pair", "comparison", "package", "leaf", "unit", "base", "measured", "signed_delta"))
            for (group, pair, comparison, case_id), sides in pairs.items():
                case = cases[case_id]
                if case["context"] != context:
                    continue
                for name in case["leaves"]:
                    for unit in UNITS:
                        base = sides["left"][name][unit]
                        head = sides["right"][name][unit]
                        table.writerow((group, pair, comparison, case["package"], name, unit, base, head, head - base))


def main(argv):
    action, *args = argv
    if action == "prepare":
        prepare(*map(Path, args))
        return
    evidence = Path(args[0])
    if action == "plan":
        make_cases(evidence)
        return
    if action == "sources":
        verify_sources(evidence)
        return
    cases = json.loads((evidence / "cases.json").read_text())
    if action == "order":
        checked_order(evidence, cases)
    elif action == "inventory":
        print(next(iter(inventories(evidence, cases))))
    elif action == "samples":
        samples(evidence, cases, checked_order(evidence, cases))
    else:
        raise SystemExit(f"unknown action: {action}")


if __name__ == "__main__":
    main(sys.argv[1:])
