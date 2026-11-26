"""Qualify two exact FetchTransport builds on one native Linux runner."""

import argparse
from base64 import b64decode
from collections import Counter, defaultdict
from datetime import datetime, timezone
from hashlib import sha256
import json
import os
from pathlib import Path
import platform
import shutil
import signal
from statistics import median
import subprocess
import tempfile
import time

HERE = Path(__file__).resolve().parent
BASE = "77b6cdf20d424213f4c1dc90534f924ac6b176d0"
PREVIOUS_DIAGNOSTIC = "aa0b9310049d71759a7782d11f2a91329d420ac7"
BASE_TREE = "9014a2b7a92f5b2abbb5aca10a6a9a7d0978955f"
CANDIDATE_TREE = "5ff7b34b3726c0f30a33be879997031c8904c4e1"
PACKAGE = "packages/client"
PACKAGES = {"A": "7ea95baf91932060edad9d0dc86709ecabed060c", "B": "e021c74132f2ef910dd8579791562af3319a641b"}
CHANGED = {"src/transport.ts", "src/transport.test.ts", "src/node-http2.test.ts"}
LOCK_SHA = "5d79ad0e70739d9440004e80bc4217b7bc7a948dd29cd908ef4bb4e01761df6f"
PACKAGE_SHA = "618e8dbef6fc1cedcb9fc7bfaf5f780a4437cc883f8a0692b2919313b492d409"
PATCH_SHA = "53773091cdb2772d23e30e7fffeb3b5ca0e8ffc564bdc6334a65b8f4ce283472"
WORKER_SHAS = {
    "timing": "5832fbac2ea245a41d286c94235583a6a395cf49b3b588d47893e1009d3ed9ef",
    "allocation": "d6f326f266c8d97db89474c4b40de3894deebaf59016a074a3ea78cddc065e4f",
}
RUNTIME = {"node": "26.9.0", "v8": "14.6.202.34-node.32", "uv": "1.52.1", "platform": "linux", "arch": "x64"}
PNPM = "11.9.0"
LEAVES = [(mode, leaf) for mode in ("json", "binary") for leaf in ("no-callback", "configured", "one-401")]
COMPARISONS = {"AA": ("A", "A"), "AB": ("A", "B")}
GROUPS, PAIRS = 2, 10
WARMUP_BATCHES, ITERATIONS = 12, 2000
LONG = {
    label: "eyJhbGciOiJIUzI1NiJ9." + "YWI" * 640 + label + "." + "a" * 43
    for label in ("expired", "fresh")
}


def digest(path):
    result = sha256()
    with path.open("rb") as data:
        for chunk in iter(lambda: data.read(256 * 1024), b""):
            result.update(chunk)
    return result.hexdigest()


def git(repo, *args, index=None, data=None):
    env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
    env.update(GIT_NO_LAZY_FETCH="1", GIT_OPTIONAL_LOCKS="0")
    if index is not None:
        env["GIT_INDEX_FILE"] = str(index)
    return subprocess.check_output(["git", "--no-replace-objects", "-c", "core.autocrlf=false",
                                    "-C", str(repo), *map(str, args)], env=env, input=data)


def verify_checkout(repo):
    assert git(repo, "rev-parse", "HEAD").decode().strip() == os.environ["GITHUB_SHA"]
    assert git(repo, "rev-parse", "HEAD^").decode().strip() == PREVIOUS_DIAGNOSTIC
    assert git(repo, "rev-parse", "HEAD^^").decode().strip() == BASE
    relative = HERE.relative_to(repo)
    delta = git(repo, "diff", "--name-status", PREVIOUS_DIAGNOSTIC, "HEAD").decode().splitlines()
    expected = {f"M\t{relative / name}" for name in ("qualify.py", "product.patch.b64")}
    expected.add("M\t.github/workflows/sdk-ts-refresh-cost.yml")
    assert set(delta) == expected, delta
    names = {str(HERE.relative_to(repo) / name) for name in
             ("qualify.py", "timing-worker.mjs", "allocation-worker.mjs", "product.patch.b64")}
    names.add(".github/workflows/sdk-ts-refresh-cost.yml")
    changes = git(repo, "diff", "--name-status", BASE, "HEAD").decode().splitlines()
    assert set(changes) == {f"A\t{name}" for name in names}, changes
    assert all((repo / name).read_bytes() == git(repo, "show", f"HEAD:{name}") for name in names)


def verify_package(path, sources):
    assert digest(path / "pnpm-lock.yaml") == LOCK_SHA
    assert digest(path / "package.json") == PACKAGE_SHA
    assert {file.relative_to(path).as_posix(): digest(file)
            for file in (path / "src").rglob("*") if file.is_file()} == {
                name: fingerprint for name, fingerprint in sources.items() if name.startswith("src/")}
    assert all(digest(path / name) == fingerprint for name, fingerprint in sources.items())


def stage_sources(repo, work):
    patch = b64decode(b"".join((HERE / "product.patch.b64").read_bytes().split()), validate=True)
    assert sha256(patch).hexdigest() == PATCH_SHA
    assert git(repo, "rev-parse", f"{BASE}^{{tree}}").decode().strip() == BASE_TREE
    assert git(repo, "rev-parse", f"{BASE}:{PACKAGE}").decode().strip() == PACKAGES["A"]
    common = Path(git(repo, "rev-parse", "--path-format=absolute", "--git-common-dir").decode().strip())
    private = work / "objects.git"
    private.mkdir()
    git(private, "init", "--quiet", "--bare")
    (private / "objects/info/alternates").write_text(str(common / "objects") + "\n")
    index = work / "index"
    git(private, "read-tree", BASE, index=index)
    git(private, "apply", "--cached", "--check", "--whitespace=error-all", "-", index=index, data=patch)
    git(private, "apply", "--cached", "--whitespace=error-all", "-", index=index, data=patch)
    changed = set(git(private, "diff", "--cached", "--name-only", BASE, index=index).decode().splitlines())
    assert changed == {f"{PACKAGE}/{name}" for name in CHANGED}, changed
    assert git(private, "write-tree", index=index).decode().strip() == CANDIDATE_TREE
    assert git(private, "rev-parse", f"{CANDIDATE_TREE}:{PACKAGE}").decode().strip() == PACKAGES["B"]

    packages, manifest = {}, {}
    for role, tree in PACKAGES.items():
        path = work / role
        path.mkdir()
        subprocess.run(["tar", "-xf", "-", "-C", path], input=git(private, "archive", tree), check=True)
        sources = {}
        for row in filter(None, git(private, "ls-tree", "-rz", tree).split(b"\0")):
            meta, name = row.decode().split("\t", 1)
            mode, kind, git_id = meta.split()
            assert mode in ("100644", "100755") and kind == "blob", row
            sources[name] = sha256(git(private, "cat-file", "blob", git_id)).hexdigest()
        verify_package(path, sources)
        packages[role], manifest[role] = path, sources
    assert set(manifest["A"]) == set(manifest["B"])
    assert {name for name, value in manifest["A"].items() if manifest["B"][name] != value} == CHANGED
    return packages, manifest


def workers(work):
    probes = {}
    for metric, checksum in WORKER_SHAS.items():
        short = HERE / f"{metric}-worker.mjs"
        assert digest(short) == checksum
        source = short.read_text(encoding="utf-8")
        substitutions = {
            "'fresh'": json.dumps(LONG["fresh"]),
            "transport.setToken('expired')": f"transport.setToken({json.dumps(LONG['expired'])})",
            "return request.headers.authorization === 'Bearer expired' ? unauthorized : success": (
                f"if (request.headers.authorization === {json.dumps('Bearer ' + LONG['expired'])}) return unauthorized\n"
                f"      ok(request.headers.authorization === {json.dumps('Bearer ' + LONG['fresh'])})\n"
                "      return success"
            ),
        }
        for old, new in substitutions.items():
            assert source.count(old) == (2 if old == "'fresh'" else 1), old
            source = source.replace(old, new)
        long = work / f"{metric}-long.mjs"
        long.write_text(source, encoding="utf-8")
        probes[metric] = {"short": short, "long": long}
    return probes


def schedule():
    rows = []
    for group in range(GROUPS):
        for pair in range(PAIRS):
            offset = (group * PAIRS + pair) % len(LEAVES)
            for mode, leaf in LEAVES[offset:] + LEAVES[:offset]:
                leaf_index = LEAVES.index((mode, leaf))
                reverse = (group + leaf_index + (pair in (2, 3, 6, 7, 9))) % 2
                order = tuple(COMPARISONS)[::-1] if reverse else tuple(COMPARISONS)
                for position, comparison in enumerate(order):
                    first = (pair + group + leaf_index + (comparison == "AB")) % 2
                    rows.append((group + 1, pair + 1, mode, leaf, comparison, first, position))
    counts = Counter((g, m, l, c, first) for g, _, m, l, c, first, _ in rows)
    positions = Counter((g, m, l, c, position) for g, _, m, l, c, _, position in rows)
    expected = {(g, m, l, c, first): PAIRS // 2 for g in range(1, GROUPS + 1)
                for m, l in LEAVES for c in COMPARISONS for first in (0, 1)}
    assert counts == positions == expected
    joint = Counter((g, m, l, c, first, position) for g, _, m, l, c, first, position in rows)
    assert len(joint) == 2 * len(expected) and set(joint.values()) == {2, 3}
    assert len({(g, p, m, l, c) for g, p, m, l, c, _, _ in rows}) == len(rows) == 240
    return rows


def bundle(path):
    files = {name.name: digest(name) for name in (path / "dist").glob("*.mjs")}
    shared = set(files) - {"index.mjs", "node.mjs"}
    assert len(files) == 3 and len(shared) == 1
    entry, = shared
    assert entry.startswith("transport-"), entry
    for name in ("index.mjs", "node.mjs"):
        assert f'from "./{entry}"' in (path / "dist" / name).read_text().splitlines()[0]
    return files


def environment(work):
    assert (platform.system(), platform.machine()) == ("Linux", "x86_64")
    assert not any(os.environ.get(key) for key in ("NODE_OPTIONS", "NODE_V8_COVERAGE", "NODE_COMPILE_CACHE"))
    node, pnpm = (Path(shutil.which(tool)).resolve(strict=True) for tool in ("node", "pnpm"))
    assert subprocess.check_output([pnpm, "--version"], text=True).strip() == PNPM
    payload = "console.log(JSON.stringify({node:process.versions.node,v8:process.versions.v8," \
              "uv:process.versions.uv,platform:process.platform,arch:process.arch}))"
    assert json.loads(subprocess.check_output([node, "-e", payload], text=True)) == RUNTIME
    tools_tmp = work / "tool-tmp"
    tools_tmp.mkdir()
    env = dict(os.environ, TMPDIR=str(tools_tmp), TMP=str(tools_tmp), TEMP=str(tools_tmp),
               XDG_CACHE_HOME=str(work / "xdg-cache"), npm_config_cache=str(work / "npm-cache"))
    return node, pnpm, env


def build(pnpm, packages, work, source, evidence, env):
    results = {}
    for role, package in packages.items():
        store = work / "pnpm-store"
        install = [pnpm, "install", "--frozen-lockfile", "--store-dir", store]
        if role == "B":
            install.append("--offline")
        subprocess.run(install, cwd=package, env=env, timeout=300, check=True)
        subprocess.run([pnpm, "build"], cwd=package, env=env, timeout=120, check=True)
        verify_package(package, source[role])
        results[role] = bundle(package)
        emit(evidence, {"kind": "build", "role": role, "inputs": source[role], "esm": results[role]})
    return results


def emit(stream, row):
    stream.write(json.dumps(row, sort_keys=True, separators=(",", ":")) + "\n")
    stream.flush()


def child(node, probe, phase, package, role, mode, scenario, runtime, journal, context):
    entry = package / "dist/index.mjs"
    started = time.monotonic_ns()
    load = os.getloadavg()
    raw = {**context, "kind": "child", "role": role, "mode": mode, "scenario": scenario, "phase": phase}
    try:
        proc = subprocess.run([node, probe, phase, entry, mode, scenario], cwd=probe.parent,
                              capture_output=True, text=True, timeout=45)
    except subprocess.TimeoutExpired as error:
        emit(journal, {**raw, "timeout": 45, "stdout": (error.stdout or b"").decode("utf-8", "replace"),
                       "stderr": (error.stderr or b"").decode("utf-8", "replace")})
        raise
    outer = time.monotonic_ns() - started
    emit(journal, {**raw, "returncode": proc.returncode, "elapsedNs": outer,
                   "loadBefore": load, "loadAfter": os.getloadavg(), "stdout": proc.stdout, "stderr": proc.stderr})
    assert proc.returncode == 0 and not proc.stderr, (role, mode, scenario, proc.returncode)
    row = json.loads(proc.stdout)
    assert all(row.get(name) == value for name, value in {
        **runtime, "phase": phase, "entry": str(entry), "mode": mode, "scenario": scenario,
        "executable": str(node), "validated": True,
    }.items()), row
    count = 1 if phase == "smoke" else 1 + (WARMUP_BATCHES + (phase != "empty")) * ITERATIONS
    expires = scenario == "one-401"
    assert row["counts"] == {"renewals": count if expires else 0, "sends": count * (2 if expires else 1)}, row
    if phase != "smoke":
        assert row["warmupBatches"] == WARMUP_BATCHES and row["iterations"] == ITERATIONS
    if phase == "sample":
        assert 0 < row["wallNs"] < outer and min(row["userCpuUs"], row["systemCpuUs"]) >= 0
        assert row["userCpuUs"] + row["systemCpuUs"] > 0
        return {"wallNs": row["wallNs"], "cpuNs": 1000 * (row["userCpuUs"] + row["systemCpuUs"])}
    if phase in ("allocation", "empty"):
        before, after = row["before"], row["after"]
        assert type(before) is int and type(after) is int and 0 < before <= after < 2**53, row
        assert row["operations"] == (0 if phase == "empty" else ITERATIONS), row
        assert row["allocatedBytes"] == after - before, row
        return {"managedBytes": row["allocatedBytes"]}
    assert row["operations"] == 1


def samples(node, probe, packages, token, metric, journal, runtime):
    rows = [row for row in schedule() if token == "short" or row[3] == "one-401"]
    leaves = LEAVES if token == "short" else [leaf for leaf in LEAVES if leaf[1] == "one-401"]
    totals = defaultdict(list)
    for group in range(1, GROUPS + 1):
        if metric == "allocation":
            for mode, scenario in leaves:
                for role in packages:
                    child(node, probe, "empty", packages[role], role, mode, scenario, runtime, journal,
                          {"group": group, "token": token, "metric": metric})
        for g, pair, mode, scenario, comparison, first, position in rows:
            if group != g:
                continue
            point = {"token": token, "metric": metric, "group": g, "pair": pair, "comparison": comparison,
                     "mode": mode, "scenario": scenario, "first": first, "position": position}
            measured = {}
            for side in (first, 1 - first):
                role = COMPARISONS[comparison][side]
                measured[side] = child(node, probe, "sample" if metric == "timing" else "allocation",
                                       packages[role], role, mode, scenario, runtime, journal, {**point, "side": side})
            delta = {name: (measured[1][name] - value) / ITERATIONS for name, value in measured[0].items()}
            emit(journal, {**point, "kind": "pair", "deltaPerOp": delta,
                           "positivePerOp": {name: max(0, value) for name, value in delta.items()}})
            for name, value in delta.items():
                totals[g, mode, scenario, comparison, name].append(value)
    assert len(rows) == (240 if token == "short" else 80)
    assert len(totals) == GROUPS * len(leaves) * len(COMPARISONS) * (2 if metric == "timing" else 1)
    assert all(len(values) == PAIRS for values in totals.values())
    emit(journal, {"kind": "complete", "token": token, "metric": metric, "pairs": len(rows),
                   "summary": [{"group": g, "mode": mode, "scenario": scenario, "comparison": comparison, "unit": name,
                                "min": min(values), "median": median(values), "max": max(values),
                                "maxPositive": max(0, max(values))}
                               for (g, mode, scenario, comparison, name), values in sorted(totals.items())]})


def evidence_inputs(node, pnpm, probes, packages, source, esm):
    assert all(bundle(package) == esm[role] for role, package in packages.items())
    for role, package in packages.items():
        verify_package(package, source[role])
    return {"base": BASE, "baseTree": BASE_TREE, "candidateTree": CANDIDATE_TREE, "packageTrees": PACKAGES,
            "payloadSha256": PATCH_SHA, "driverSha256": digest(Path(__file__)), "lockSha256": LOCK_SHA,
            "esm": esm, "sources": source, "nodeSha256": digest(node), "pnpmSha256": digest(pnpm), "pnpm": PNPM,
            "runtime": RUNTIME, "workers": {metric: {token: digest(path) for token, path in worker.items()}
                                            for metric, worker in probes.items()},
            "supplementalTokenLengths": {label: len(token) for label, token in LONG.items()},
            "groups": GROUPS, "pairsPerComparisonPerLeaf": PAIRS, "iterations": ITERATIONS,
            "warmupBatches": WARMUP_BATCHES, "allocationScope": "cumulative V8-managed bytes"}


def collect(repo, work, probes, packages, source, output):
    verify_checkout(repo)
    node, pnpm, env = environment(work)
    cpu = next(line.partition(":")[2].strip() for line in Path("/proc/cpuinfo").read_text().splitlines()
               if line.startswith("model name"))
    with (output / "setup.jsonl").open("x") as setup:
        emit(setup, {"kind": "setup", "cpu": cpu, "platform": platform.platform(), "runnerImage": os.environ.get("ImageVersion"),
                     "sha": os.environ["GITHUB_SHA"], "started": datetime.now(timezone.utc).isoformat()})
        esm = build(pnpm, packages, work, source, setup, env)
        binding = evidence_inputs(node, pnpm, probes, packages, source, esm)
        emit(setup, {"kind": "binding", **binding})
        for role, package in packages.items():
            for mode, scenario in LEAVES:
                child(node, probes["timing"]["short"], "smoke", package, role, mode, scenario, RUNTIME, setup, {})
            for mode in ("json", "binary"):
                child(node, probes["timing"]["long"], "smoke", package, role, mode, "one-401", RUNTIME, setup, {})
        for token in ("short", "long"):
            for metric in ("timing", "allocation"):
                with (output / f"{token}-{metric}.jsonl").open("x") as journal:
                    emit(journal, {"kind": "header", "token": token, "metric": metric, "cpu": cpu, "binding": binding})
                    try:
                        samples(node, probes[metric][token], packages, token, metric, journal, RUNTIME)
                        assert evidence_inputs(node, pnpm, probes, packages, source, esm) == binding
                    except BaseException as error:
                        emit(journal, {"kind": "failed", "error": str(error)})
                        raise
        verify_checkout(repo)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=("verify", "collect"))
    parser.add_argument("--repo", type=Path, required=True)
    parser.add_argument("--scratch", type=Path, required=True)
    parser.add_argument("--evidence", type=Path)
    args = parser.parse_args()
    repo = args.repo.resolve(strict=True)
    scratch = args.scratch.resolve(strict=True)
    assert ".tmp" in scratch.parts
    assert (args.phase == "collect") == (args.evidence is not None)
    output = None
    if args.phase == "collect":
        assert os.environ.get("GITHUB_ACTIONS") == "true" and scratch == repo / ".tmp"
        output = args.evidence.resolve(strict=True)
        assert output.parent == scratch
        with (output / "preflight.jsonl").open("x") as log:
            emit(log, {"kind": "started", "sha": os.environ["GITHUB_SHA"], "base": BASE,
                       "candidate": CANDIDATE_TREE, "driverSha256": digest(Path(__file__))})
    try:
        with tempfile.TemporaryDirectory(dir=scratch, prefix="ts-refresh-") as tmp:
            work = Path(tmp)
            packages, source = stage_sources(repo, work)
            probes = workers(work)
            assert len([row for row in schedule() if row[3] == "one-401"]) == 80
            if output is not None:
                collect(repo, work, probes, packages, source, output)
    except BaseException as error:
        if output is not None:
            with (output / "preflight.jsonl").open("a") as log:
                emit(log, {"kind": "failed", "type": type(error).__name__, "error": str(error)})
        raise
    assert not work.exists()
    result = {"kind": "complete", "phase": args.phase, "scratchRemoved": True, "base": BASE,
              "candidate": CANDIDATE_TREE, "shortPairsPerMetric": 240, "longPairsPerMetric": 80}
    print(json.dumps(result))
    if output is not None:
        with (output / "complete.jsonl").open("x") as log:
            emit(log, result)


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, signal.getsignal(signal.SIGINT))
    main()
