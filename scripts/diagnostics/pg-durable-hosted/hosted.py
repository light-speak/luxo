#!/usr/bin/env python3
"""Replay retained migration samples and measure only supported recovery work."""
import argparse
import base64
import gzip
import hashlib
import json
import os
from decimal import Decimal
from pathlib import Path
import re
import runpy
import secrets
import shutil
import signal
import statistics
import subprocess
import tempfile
import time
from types import SimpleNamespace

HERE = Path(__file__).resolve().parent
PAYLOAD = HERE / ".payload"
SERVER = runpy.run_path(str(HERE / "server.py"))
BASE = "feddadefe3ee05611c075cfa0319a851865584b5"
BASE_TREE = "f30e4c89335c8b14d4269e0df1a5d51e7d471506"
ARMS = {"A": "06d749abc50cd631ca556586a58a07acb9869eda", "B": "631b05aa62508ae122f9a69ad83a80a7eae8bd08"}
PATCHES = {
    "order": ("fbb3bc40bb1cfb8b5e8961f9a17c22ff5931cfffc31bca55184f3aeadd397439", ARMS["A"]),
    "durable": ("5746c8a785473fd05aee2a96eee75f7edfbeccbe342f0423e5d3cfd2d69de2b5", ARMS["B"]),
}
LEAVES = {
    "holder": {"open-close", "first-lock", "warm-lock", "ordinary-up", "up-no-pending"},
    "standalone": {"ordinary/up", "ordinary/down", "ordinary/up-existing",
                   "concurrent/up", "concurrent/down", "concurrent/up-existing-index"},
    "recovery": {"first-journal-and-create-index", "acknowledge-committed-down",
                 "record-applied-after-restart", "retry-after-invalid-index-repair"},
}
METRICS = ("ns/op", "B/op", "allocs/op")
HOLD_ORDER = (("aa", "AA", 4, "ab"), ("h1", "AB", 10, "ab"), ("h2", "AB", 10, "ba"))
RECONCILE = "^BenchmarkDurableRecovery$"
STANDALONE = "^BenchmarkMigrationPair$/^(ordinary|concurrent)$/^(up|down|up-existing|up-existing-index)$"


def run(*args, **kwargs):
    return subprocess.check_output(args, **kwargs)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def put(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def payload():
    names = {"holder-driver.py", "shared.py", "advisory_pair_test.go", "migration_pair_test.go",
             "durable_reset_test.go", "recovery_test.go", "order.patch.gz.b64", "durable.patch.gz.b64"}
    entries = {}
    for line in (PAYLOAD / "inputs.sha256").read_text().splitlines():
        digest, name = line.split("  ", 1)
        if name not in names or name in entries or sha((PAYLOAD / name).read_bytes()) != digest:
            raise RuntimeError("unexpected durable diagnostics payload")
        entries[name] = digest
    if entries.keys() != names:
        raise RuntimeError("incomplete durable diagnostics payload")
    return entries


def trees(repo, root):
    checkout = ("git", "-C", str(repo))
    if run(*checkout, "rev-parse", BASE + "^{tree}", text=True).strip() != BASE_TREE:
        raise RuntimeError("unexpected source commit or base tree")
    subprocess.run([*checkout, "merge-base", "--is-ancestor", BASE, "HEAD"], check=True)
    objects = run(*checkout, "rev-parse", "--path-format=absolute", "--git-path", "objects", text=True).strip()
    bare = root / "objects.git"
    run("git", "init", "--bare", "--quiet", str(bare))
    (bare / "objects/info/alternates").write_text(objects + "\n")
    git = ("git", f"--git-dir={bare}")
    run(*git, "read-tree", BASE_TREE)
    for name, (digest, expected) in PATCHES.items():
        encoded = (PAYLOAD / (name + ".patch.gz.b64")).read_bytes()
        patch = gzip.decompress(base64.b64decode(b"".join(encoded.split()), validate=True))
        if sha(patch) != digest:
            raise RuntimeError(f"{name} patch hash changed")
        run(*git, "apply", "--cached", "--whitespace=error", "-", input=patch)
        if run(*git, "write-tree", text=True).strip() != expected:
            raise RuntimeError(f"{name} patch did not produce the expected tree")
    return git


def standalone_fixture():
    source = (PAYLOAD / "migration_pair_test.go").read_text()
    for before, after, count in (
        ("func runPairCase(", "var completeStandalone = func(fixture, scenario) {}\n\nfunc runPairCase(", 1),
        ("\n\tf.verify(sample, before, wantNames, wantSQL, names, sql, err)\n",
         "\n\tf.verify(sample, before, wantNames, wantSQL, names, sql, err)\n\tcompleteStandalone(f, sample)\n", 1),
        ("\n\t\tf.verify(sample, before, wantNames, wantSQL, names, sql, err)\n",
         "\n\t\tf.verify(sample, before, wantNames, wantSQL, names, sql, err)\n\t\tcompleteStandalone(f, sample)\n", 1),
        ("DROP TABLE IF EXISTS public._migrations, public.audit_target",
         "DROP TABLE IF EXISTS public._luxo_migration_attempt, public._migrations, public.audit_target", 1),
    ):
        if source.count(before) != count:
            raise RuntimeError("original standalone fixture preimage changed")
        source = source.replace(before, after)
    return source


def archive_source(git, target, tree):
    target.mkdir()
    with subprocess.Popen([*git, "archive", tree], stdout=subprocess.PIPE) as archive:
        with archive.stdout as stream:
            subprocess.run(["tar", "-xf", "-", "-C", str(target)], stdin=stream, check=True)
        if archive.wait():
            raise RuntimeError(f"could not archive {tree}")


def prepare_arm(git, root, arm, env, evidence):
    with tempfile.TemporaryDirectory(dir=root, prefix=f"source-{arm}-") as temporary:
        src = Path(temporary) / "source"
        archive_source(git, src, ARMS[arm])
        run("sh", "scripts/setup-modules.sh", str(src), cwd=src, env=env)
        workspace = env | {"GOWORK": str(src / "go.work")}
        lanes = {"holder": "advispair", "standalone": "migratepair"}
        if arm == "B":
            lanes["recovery"] = "recoverypair"
        for lane, folder in lanes.items():
            target = src / "pkg/lux/pg" / folder
            target.mkdir()
            if lane == "standalone":
                (target / "migration_pair_test.go").write_text(standalone_fixture())
                if arm == "B":
                    shutil.copyfile(PAYLOAD / "durable_reset_test.go", target / "durable_reset_test.go")
            else:
                for name in ("advisory_pair_test.go", "recovery_test.go"):
                    if lane == "recovery" or name == "advisory_pair_test.go":
                        shutil.copyfile(PAYLOAD / name, target / name)
        packages = ["./pkg/lux/pg/" + folder for folder in lanes.values()]
        graph = run("go", "list", "-mod=readonly", "-p=1", "-deps", "-test", *packages, cwd=src, env=workspace)
        verified = run("go", "mod", "verify", cwd=src, env=workspace, text=True).strip()
        if verified != "all modules verified":
            raise RuntimeError("Go module verification failed")
        offline = workspace | {"GOPROXY": "off", "GOSUMDB": "off"}
        if run("go", "list", "-mod=readonly", "-p=1", "-deps", "-test", *packages, cwd=src, env=offline) != graph:
            raise RuntimeError("offline fixture graph differs")
        result = {}
        for lane, folder in lanes.items():
            path = root / lane / f"runner-{arm}.test"
            path.parent.mkdir(exist_ok=True)
            run("go", "test", "-c", "-trimpath", "-o", str(path), "./pkg/lux/pg/" + folder, cwd=src, env=offline)
            result[lane] = {"tree": ARMS[arm], "binary": path.name, "sha256": sha(path.read_bytes())}
    put(evidence / f"source-{arm}.json", {"tree": ARMS[arm], "graphSHA256": sha(graph), "modules": verified,
                                        "arms": result, "standaloneFixtureSHA256": sha(standalone_fixture().encode())})
    return result


def observation():
    server = SERVER["verify_server"]()
    lscpu = {i["field"].rstrip(":"): i.get("data") for i in server["cpu"]["lscpu"]}
    if lscpu.get("Model name") != "Neoverse-N2":
        raise RuntimeError(f"unexpected hosted model: {lscpu.get('Model name')}")
    scope, _ = SERVER["scope"]()
    checkpoint = SERVER["admin"](scope, "SELECT checkpoints_timed, checkpoints_req, checkpoint_write_time, "
                                 "checkpoint_sync_time, stats_reset FROM pg_stat_bgwriter;").split("|")
    return {"hostLoad": os.getloadavg(), "cpus": sorted(os.sched_getaffinity(0)),
            "checkpoints": checkpoint, "server": server}


def admit(path):
    history = []
    for i in range(13):
        item = observation()
        history.append(item)
        put(path, history)
        if item["hostLoad"][0] <= 0.5 * len(item["cpus"]):
            return item
        if i < 12:
            time.sleep(10)
    raise RuntimeError("no quiet hosted PG window")


def extra_sample(root, lane, name, arm, slots, slot, identities, iterations, evidence):
    database, password = slots[slot]
    binary = root / lane / identities[arm][lane]["binary"]
    selector = STANDALONE if lane == "standalone" else RECONCILE
    env = {**os.environ, "GOMAXPROCS": "1", "GOGC": "100", "GOMEMLIMIT": "off", "GODEBUG": "",
           "TMPDIR": str(root), "LUXO_ADVISORY_MODEL": "holder" if arm == "B" else "pooled",
           "LUXO_MIGPAIR_DATABASE_URL": f"postgres://{database}:{password}@127.0.0.1:5432/{database}?sslmode=disable"}
    proc = subprocess.run([str(binary), "-test.run=^$", f"-test.bench={selector}", "-test.cpu=1",
                           "-test.benchmem", "-test.count=1", "-test.timeout=3m", f"-test.benchtime={iterations}x"],
                          cwd=root, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    raw = proc.stdout.replace(password, "[redacted]")
    (evidence / f"{name}.log").write_text(raw)
    if proc.returncode:
        raise RuntimeError(f"failed to measure {name}; see raw transcript")
    prefix = "BenchmarkMigrationPair" if lane == "standalone" else "BenchmarkDurableRecovery"
    rows = re.findall(rf"^{prefix}/(\S+?)(?:-\d+)?\s+(\d+)\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op$", raw, re.M)
    if len(rows) != len(LEAVES[lane]) or {row[0] for row in rows} != LEAVES[lane] or any(int(r[1]) != iterations for r in rows):
        raise RuntimeError(f"invalid {lane} sample inventory")
    if not re.search(r"^PASS$", raw, re.M):
        raise RuntimeError("missing benchmark PASS")
    return {"arm": arm, "slot": slot, "name": name, "metrics": {leaf: dict(zip(METRICS, values))
             for leaf, _, *values in rows}, "logSHA256": sha(raw.encode())}


def run_lane(root, evidence, helper, holder, lane, identities):
    out = evidence / lane
    out.mkdir()
    order = HOLD_ORDER
    if lane == "standalone":
        order = (("aa", "AA", 10, "ab"), *HOLD_ORDER[1:])
    elif lane == "recovery":
        order = (("bb1", "BB", 10, "ab"), ("bb2", "BB", 10, "ba"))
    slots, runs = {}, []
    state = {"status": "failed", "initial": {}, "clean": {}, "measurements": runs, "iterations": 3}
    token = secrets.token_hex(8)

    def verify_binaries():
        for arm in ("B",) if lane == "recovery" else ("A", "B"):
            expected = identities[arm][lane]
            if sha((root / lane / expected["binary"]).read_bytes()) != expected["sha256"]:
                raise RuntimeError("prepared benchmark binary changed")

    def sample(name, arm, slot, iterations, smoke=False):
        expected = identities[arm][lane]
        if lane == "holder":
            namespace = holder["measure"].__globals__
            namespace["HERE"] = root / lane
            return holder["measure"](helper, out, name, arm, slot, slots, {"arms": {arm: expected}},
                                     iterations, smoke, root)
        return extra_sample(root, lane, name, arm, slots, slot, identities, iterations, out)

    try:
        verify_binaries()
        for key in ("a", "b"):
            helper.create_slot(key, slots, token)
            state["initial"][key] = "0|0"
        before_smoke = admit(out / "before-smoke.json")
        state["smoke"] = [sample(f"smoke-{arm}", arm, slot, 1, True) for arm, slot in
                          ((("B", "b"),) if lane == "recovery" else (("A", "a"), ("B", "b")))]
        verify_binaries()
        before = admit(out / "before-formal.json")
        if before_smoke["cpus"] != before["cpus"]:
            raise RuntimeError("runner CPU scope changed before sampling")
        for group, arms, count, db in order:
            for pair in range(1, count + 1):
                indices = (0, 1) if pair % 2 else (1, 0)
                values = {i: sample(f"{group}-{pair:02d}-{i}", arms[i], db[i], 3) for i in indices}
                difference = {leaf: {metric: str(Decimal(values[1]["metrics"][leaf][metric]) -
                    Decimal(values[0]["metrics"][leaf][metric])) for metric in METRICS} for leaf in LEAVES[lane]}
                runs.append({"group": group, "pair": pair, "order": indices, "arms": list(values.values()), "delta": difference})
        after = observation()
        put(out / "after-formal.json", after)
        if before["cpus"] != after["cpus"] or before["checkpoints"][-1] != after["checkpoints"][-1]:
            raise RuntimeError("CPU scope or checkpoint epoch changed")
        verify_binaries()
        summary = {}
        for group, _, _, _ in order:
            summary[group] = {}
            for leaf in sorted(LEAVES[lane]):
                summary[group][leaf] = {}
                for metric in METRICS:
                    values = [Decimal(row["delta"][leaf][metric]) for row in runs if row["group"] == group]
                    summary[group][leaf][metric] = {"minimum": str(min(values)),
                        "median": str(statistics.median(values)), "maximum": str(max(values)),
                        "maximumPositive": str(max(0, max(values)))}
        put(out / "summary.json", summary)
        state["status"] = "pass"
    finally:
        state["clean"] = helper.clean_slots(slots)
        put(out / "run.json", state)
        if state["clean"] != {"a":"0|0","b":"0|0"}:
            raise RuntimeError(f"incomplete cleanup in {lane}")


def replay(repo, root, evidence, entries):
    env = {key: value for key, value in os.environ.items() if not key.startswith(("LUXO_MIGPAIR_", "LUXO_ADVISORY_"))}
    env |= {"GOTOOLCHAIN": "local", "GOWORK": "off", "GOFLAGS": "-p=1", "GOMAXPROCS": "2", "CGO_ENABLED": "0",
            "GOENV": "off", "GOARM64": "v8.0", "GOCACHE": str(root / "cache"), "GOMODCACHE": str(root / "modules"),
            "TMPDIR": str(root), "GOTMPDIR": str(root), "GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org"}
    if run("go", "version", env=env, text=True).strip() != "go version go1.27.1 linux/arm64":
        raise RuntimeError("unexpected Go toolchain")
    put(evidence / "inputs.json", {"base": BASE, "baseTree": BASE_TREE, "products": ARMS,
                                  "patches": PATCHES, "payload": entries, "go": "go1.27.1 linux/arm64"})
    git = trees(repo, root)
    identities = {arm: prepare_arm(git, root, arm, env, evidence) for arm in ARMS}
    # Only the real PostgreSQL fixture and holder measurement entry points are reused here.
    shared = runpy.run_path(str(PAYLOAD / "shared.py"))
    holder = runpy.run_path(str(PAYLOAD / "holder-driver.py"))
    scope, _ = SERVER["scope"]()
    shared["admin"].__globals__["admin"] = lambda sql: SERVER["admin"](scope, sql)
    helper = SimpleNamespace(**shared)
    put(evidence / "server.json", SERVER["start_owned"]())
    failures = {}
    for lane in LEAVES:
        try:
            run_lane(root, evidence, helper, holder, lane, identities)
        except (RuntimeError, subprocess.SubprocessError) as err:
            failures[lane] = str(err)
    put(evidence / "outcome.json", {"passed": not failures, "failures": failures})
    if failures:
        raise RuntimeError(f"durable PostgreSQL diagnostic failed in {sorted(failures)}")


def interrupted(_signal, _frame):
    raise KeyboardInterrupt


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("check", "run", "cleanup", "remove-evidence"))
    mode = parser.parse_args().mode
    if mode == "check":
        print(json.dumps(payload(), sort_keys=True))
        return
    SERVER["scope"]()
    repo = Path(os.environ["GITHUB_WORKSPACE"]).resolve(strict=True)
    if HERE != repo / "scripts/diagnostics/pg-durable-hosted":
        raise RuntimeError("controller must run in its scoped checkout")
    tmp = repo / ".tmp"
    evidence = tmp / "pg-durable-hosted-evidence"
    if tmp.is_symlink() or evidence.is_symlink():
        raise RuntimeError("scratch paths must be owned by the hosted checkout")
    tmp.mkdir(exist_ok=True)
    if mode == "remove-evidence":
        if evidence.is_dir():
            shutil.rmtree(evidence)
        return
    if mode == "cleanup":
        evidence.mkdir(exist_ok=True)
        put(evidence / "cleanup-confirmed.json", SERVER["remove_owned"]())
        return
    os.umask(0o077)
    signal.signal(signal.SIGTERM, interrupted)
    evidence.mkdir()
    with tempfile.TemporaryDirectory(dir=tmp, prefix="pg-durable-") as temporary:
        try:
            replay(repo, Path(temporary), evidence, payload())
        finally:
            put(evidence / "cleanup.json", SERVER["remove_owned"]())


if __name__ == "__main__":
    main()
