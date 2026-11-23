from collections import defaultdict
from csv import DictReader
from hashlib import sha256
from itertools import product
import json
from pathlib import Path
import re
import sys


PREFIX = "github.com/light-speak/luxo/"
SUITES = {
    "pr79": {
        "head": "e04c586e491b8b215308d10f7228636e38547f5b",
        "contexts": {2: "product", 3: "lean", 5: "product", 6: "product"},
        "full_cases": 8,
        "historical_count": 10,
        "control_count": 2,
    },
    "pr81": {
        "head": "33ce49477379143a8e3bfcd34a3628ec6a9bc626",
        "contexts": {2: "lean"},
        "full_cases": 4,
        "historical_count": 4,
        "control_count": 1,
    },
}


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")


def digest(data):
    return sha256(data).hexdigest()


def plan(directory, suite):
    original = json.loads((directory / "original-scope.json").read_text())[suite]
    pinned = SUITES[suite]
    historical = {(r["package"], r["leaf"]) for r in original["historical"]}
    controls = {(r["package"], r["leaf"]) for r in original["controls"]}
    full = [(case["package"], leaf) for case in original["cases"] for leaf in case["leaves"]]
    if (original["head"] != pinned["head"] or len(original["cases"]) != pinned["full_cases"]
            or len(historical) != pinned["historical_count"] or len(controls) != pinned["control_count"]
            or len(full) != len(set(full)) or historical & controls or set(full) != historical | controls):
        raise ValueError("original transport scope has changed")
    cases = []

    def append(original_case, context, variant):
        cases.append(dict(original_case, context=context, variant=variant, id=f"{len(cases)+1:02}"))

    for index, case in enumerate(original["cases"], 1):
        append(case, "full", case["variant"])
        if index in pinned["contexts"]:
            context = pinned["contexts"][index]
            append(case, context, context)
    expected = defaultdict(list)
    definitions = []
    for case in cases:
        definitions.append("\t".join([case["id"], case["package"], case["variant"], case["leaves"][0], case["filter"]]))
        expected[case["context"]].extend({"Package": case["package"], "Name": leaf} for leaf in case["leaves"])
    for context, rows in expected.items():
        if len(rows) != len({(r["Package"], r["Name"]) for r in rows}):
            raise ValueError(f"duplicate sample identity in {context}")
    write_json(directory / "cases.json", cases)
    write_json(directory / "scope.json", original)
    write_json(directory / "expected-by-context.json", expected)
    (directory / "cases.tsv").write_text("\n".join(definitions) + "\n")


def sources(directory, role, source):
    scope = json.loads((directory / "scope.json").read_text())
    common = ["go.mod", "go.sum", "scripts/setup-modules.sh", "scripts/benchrun/main.go",
              "scripts/benchgate/approved-costs.json"]
    for category, files in (("unchanged", common + scope["unchanged"]), ("changed", scope["changed"])):
        rows = [f"{digest((source / name).read_bytes())}  {name}\n" for name in files]
        (directory / f"{category}-sources-{role}.sha256").write_text("".join(rows))


def require_hash(path, sha):
    data = path.read_bytes()
    if digest(data) != sha:
        raise ValueError(f"source SHA mismatch: {path}")
    return data


def prune_functions(text, names):
    for name in names:
        pattern = rf"^func {re.escape(name)}\(.*?^}}\n(?:\n)?"
        text, count = re.subn(pattern, "", text, flags=re.M | re.S)
        if count != 1:
            raise ValueError(f"exact function expected for overlay: {name}")
    for imported in ("net", PREFIX + "pkg/lux/rpc"):
        row = f'\t"{imported}"\n'
        if text.count(row) != 1:
            raise ValueError(f"exact import expected for overlay: {imported}")
        text = text.replace(row, "")
    if re.search(r"\b(?:rpc|net)\.", text):
        raise ValueError("removed test imports still referenced")
    return text


def overlay(suite, variant, role, source, base, directory):
    source, base, directory = map(Path, (source, base, directory))
    replacements = {}
    manifest = []

    def record(original, replacement):
        replacements[str(original)] = "" if replacement is None else str(replacement)
        manifest.append({"path": str(original.relative_to(source)),
                         "original": digest(original.read_bytes()),
                         "replacement": None if replacement is None else digest(replacement.read_bytes())})

    if (suite, variant, role) == ("pr79", "product", "head"):
        for name, sha in {
            "pkg/lux/h2transport/http_server_test.go": "3f3265aec388b4ce37d699477dd86ed5794d9fe14a9e18ec554438491e33e424",
            "pkg/lux/luvia/http_transport_test.go": "a8dd662ecbd8390c08bccfa3bd435df4829dd43c20ffe52ea16fee06963f5023",
        }.items():
            require_hash(base / name, sha)
            record(source / name, base / name)
    elif suite == "pr79" and variant == "lean":
        path = source / "pkg/lux/rpc/public_boundary_test.go"
        require_hash(path, "99658ed681f4110df0db3fc688931fa61cd656f3c62477c04a1ffd4bb567ca8e")
        record(path, None)
    elif suite == "pr81" and variant == "lean":
        test = source / "pkg/lux/luvia"
        for name, sha, remove in (
            ("lifecycle_signal_test.go", "9a1661e7fc71656cf53e7326a6ef3c06109fe5c22504f6f72f49e7b9665d12ef",
             ("TestGatewayRPCOriginStartup",)),
            ("middleware_test.go", "a284f6673c86a90ed916099ac82a6ffb0e9b190161c946989957839252b07034",
             ("TestJWTHeaderBoundaryHTTPWebSocketAndInternal", "headerBoundaryRPCClient", "checkHeaderRPC")),
            ("middleware_expiry_rpc_test.go", "edf8861c80e4f6272de9c879e8d2e2cb1da153a0745ef135bf957b3c6c69b96e", ()),
        ):
            original = test / name
            data = require_hash(original, sha)
            replacement = None
            if remove:
                replacement = directory / f"{role}-{name}"
                replacement.write_text(prune_functions(data.decode(), remove))
            record(original, replacement)
    else:
        raise ValueError("unexpected test overlay combination")
    if not replacements or any(not path.endswith("_test.go") for path in replacements):
        raise ValueError("overlay must affect only tests")
    write_json(directory / f"{role}-{variant}.json", {"Replace": replacements})
    write_json(directory / f"{role}-{variant}.sha256.json", manifest)


def verify_process(case, path, inventory=False):
    lines = [line.strip() for line in path.read_text().splitlines() if line.strip()]
    if (lines[:3] != ["goos: linux", "goarch: amd64", "pkg: " + PREFIX + case["package"]]
            or len(lines) < 5 or not lines[3].startswith("cpu: ") or lines[-1] != "PASS"):
        raise ValueError(f"invalid process identity: {path}")
    names = []
    for line in lines[4:-1]:
        name, runs, ns, time_unit, memory, mem_unit, count, alloc_unit = line.split()
        if (int(runs) <= 0 or (inventory and int(runs) != 1)
                or (time_unit, mem_unit, alloc_unit) != ("ns/op", "B/op", "allocs/op")
                or any(re.fullmatch(r"[0-9]+(?:\.[0-9]+)?", val) is None for val in (ns,memory,count))):
            raise ValueError(f"invalid measurements: {path}")
        names.append(name)
    if names != case["leaves"]:
        raise ValueError(f"unexpected leaf order or membership: {path}")
    return lines[3]


def split(directory, contexts):
    cases = json.loads((directory / "cases.json").read_text())
    expected = json.loads((directory / "expected-by-context.json").read_text())
    by_id = {row["id"]: row for row in cases}
    original = []
    for group, pair, case in product(("primary", "confirmation"), range(1, 11), cases):
        offset = int(group == "confirmation")
        side_order = ("left", "right") if (pair + offset) % 2 else ("right", "left")
        comparisons = ("aa", "ab") if (pair + int(case["id"]) + offset) % 2 else ("ab", "aa")
        for comparison, side in product(comparisons, side_order):
            revision = "base" if comparison == "aa" or side == "left" else "head"
            case_id = case["id"]
            original.append(dict(group=group, pair=f"{pair:02}", comparison=comparison, case=case_id, side=side,
                                 revision=revision, variant=case["variant"], raw=f"raw/{group}.{pair:02}.{comparison}.{case_id}.{side}.txt"))
    with (directory / "order.tsv").open(newline="") as stream:
        seen = list(DictReader(stream, delimiter="\t"))
    if seen != original or {row["raw"] for row in seen} != {str(p.relative_to(directory)) for p in (directory / "raw").glob("*.txt")}:
        raise ValueError("sample schedule, identity or process count differs")
    cpu = set()
    for case, role in product(cases, ("base", "head")):
        cpu.add(verify_process(case, directory / f"inventory-{role}-{case['id']}.txt", inventory=True))
    for context, leaves in expected.items():
        own = contexts / context
        (own / "raw").mkdir(parents=True)
        write_json(own / "expected.json", {"Base": leaves, "Head": leaves})
    for row in seen:
        case = by_id[row["case"]]
        actual = directory / row["raw"]
        cpu.add(verify_process(case, actual))
        (contexts / case["context"] / row["raw"]).symlink_to(actual.resolve())
    if len(cpu) != 1:
        raise ValueError("inventory and samples must have the same CPU")
    print(f"Verified {len(seen)} original-order sample processes across {len(expected)} attribution contexts")


if __name__ == "__main__":
    command, *args = sys.argv[1:]
    if command == "plan":
        plan(Path(args[0]), args[1])
    elif command == "sources":
        sources(Path(args[0]), args[1], Path(args[2]))
    elif command == "overlay":
        overlay(*args)
    elif command == "split":
        split(*(Path(arg).resolve() for arg in args))
    else:
        raise SystemExit("unknown attribution command")
