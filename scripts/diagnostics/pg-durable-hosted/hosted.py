#!/usr/bin/env python3
"""Compare a new holder’s first lock and subsequent operations with the same database protocol."""
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
PARENT = "89d217edc211f8c1c8bd52b77d86ea965e814e33"
BASE = "5b10a26eef09248d77520ce1f118273793f30fc5"
BASE_TREE = "18ec2a84fcd20299a4bb8f3759fbb58b73574176"
ARMS = {"P": "7bc5b40d440439e2f5233d0f0f2c358056ea65f6", "Q": "8a2f2fe01062cfe550c4391ad6b9c376f8b8a008"}
PATCHES = {
    "P": (BASE_TREE, "61d5b5c63674710ed674613e457340b505507e11ba12fdd134778e11e08bc797"),
    "Q": (ARMS["P"], "d20fe94ccca626144f3cdb9f352b1819366fff5f1b2b7147406c2370e38a4c56"),
}
LEAVES = {
    "first-query": {"cold-open-ping-up", "already-used-pool-up", "warm-read-only-holder-up", "used-holder-first-reuse-up"},
    "standalone": {"ordinary/up", "ordinary/up-existing", "concurrent/up", "concurrent/down"},
}
METRICS = ("ns/op", "B/op", "allocs/op")
BENCHMARKS = {"first-query": "BenchmarkDurableFirstQuery", "standalone": "BenchmarkMigrationPair"}
SMOKE_TESTS = {"first-query": ("TestDurableFirstQuerySessions", "TestAdvisoryPairStandaloneWriterHoldsLock"),
               "standalone": ("TestMigrationPairEmptyPreview",)}
SELECTORS = {
    "first-query": "^BenchmarkDurableFirstQuery$",
    "standalone": "^BenchmarkMigrationPair$/^ordinary$/^(up|up-existing)$|^BenchmarkMigrationPair$/^concurrent$/^(up|down)$",
}
CPU = "Neoverse-N2"



def run(*args, **kwargs):
    return subprocess.check_output(args, **kwargs)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def put(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def payload():
    names = {"shared.py", "advisory_pair_test.go", "first_query_test.go", "migration_pair_test.go",
             "durable_reset_test.go", "used_holder_reuse_test.go", "arm-P.patch.gz.b64", "arm-Q.patch.gz.b64"}
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
    objects = run(*checkout, "rev-parse", "--path-format=absolute", "--git-path", "objects", text=True).strip()
    bare = root / "objects.git"
    run("git", "init", "--bare", "--quiet", str(bare))
    (bare / "objects/info/alternates").write_text(objects + "\n")
    git = ("git", f"--git-dir={bare}")
    for arm, (preimage, digest) in PATCHES.items():
        run(*git, "read-tree", preimage)
        encoded = (PAYLOAD / f"arm-{arm}.patch.gz.b64").read_bytes()
        patch = gzip.decompress(base64.b64decode(b"".join(encoded.split()), validate=True))
        if sha(patch) != digest:
            raise RuntimeError(f"{arm} patch hash changed")
        run(*git, "apply", "--cached", "--whitespace=error", "-", input=patch)
        if run(*git, "write-tree", text=True).strip() != ARMS[arm]:
            raise RuntimeError(f"{arm} patch did not produce the expected tree")
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
        ('\n\t"testing"\n', '\n\t"testing"\n\t"time"\n', 1),
        ("\n\tgot := f.inspect()\n\tif got.lockHeld || got.historyOID", """
	got := f.inspect()
	// Local connection close does not confirm that PostgreSQL processed Terminate.
	deadline := time.Now().Add(5 * time.Second)
	for got.lockHeld && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		got = f.inspect()
	}
	if got.lockHeld || got.historyOID""", 1),
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
        lanes = {"first-query": "advispair", "standalone": "migratepair"}
        for lane, folder in lanes.items():
            target = src / "pkg/lux/pg" / folder
            target.mkdir()
            if lane == "standalone":
                (target / "migration_pair_test.go").write_text(standalone_fixture())
                shutil.copyfile(PAYLOAD / "durable_reset_test.go", target / "durable_reset_test.go")
            else:
                for name in ("advisory_pair_test.go", "first_query_test.go", "used_holder_reuse_test.go"):
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


def require_cpu(data):
    lscpu = {i["field"].rstrip(":"): i.get("data") for i in data["lscpu"]}
    if lscpu.get("Model name") != CPU or lscpu.get("Architecture") != "aarch64":
        raise RuntimeError(f"unexpected hosted model or architecture: {lscpu.get('Model name')}")


def observation():
    server = SERVER["verify_server"]()
    require_cpu(server["cpu"])
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


def sample_arguments(lane, iterations, smoke):
    tests = SMOKE_TESTS[lane] if smoke else ()
    selected = "^(" + "|".join(tests) + ")$" if tests else "^$"
    args = [f"-test.run={selected}", f"-test.bench={SELECTORS[lane]}", "-test.cpu=1", "-test.benchmem", "-test.count=1",
            "-test.timeout=3m", f"-test.benchtime={iterations}x"]
    if tests:
        args.append("-test.v")
    return args, tests


def sample_metrics(raw, lane, iterations, tests):
    rows = re.findall(rf"^{BENCHMARKS[lane]}/(\S+?)(?:-\d+)?\s+(\d+)\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op$", raw, re.M)
    if len(rows) != len(LEAVES[lane]) or {row[0] for row in rows} != LEAVES[lane] or any(int(row[1]) != iterations for row in rows):
        raise RuntimeError(f"invalid {lane} sample inventory")
    if len(re.findall(r"^PASS$", raw, re.M)) != 1:
        raise RuntimeError("missing benchmark PASS")
    passed = re.findall(r"^--- PASS: (Test\w+) ", raw, re.M)
    if sorted(passed) != sorted(tests) or re.search(r"^\s*--- SKIP:", raw, re.M):
        raise RuntimeError(f"incomplete {lane} smoke tests")
    if tests and lane == "first-query":
        cases = re.findall(r"^ +--- PASS: TestDurableFirstQuerySessions/(\S+) ", raw, re.M)
        if sorted(cases) != ["clean-ping", "used-pool"]:
            raise RuntimeError("missing first-query session smoke case")
    if tests and lane == "standalone" and re.findall(r"history created=(true|false)", raw) != ["false"]:
        raise RuntimeError("empty preview changed migration history")
    return {leaf: dict(zip(METRICS, values)) for leaf, _, *values in rows}


def sample(root, lane, name, arm, slots, slot, identities, iterations, evidence, smoke=False):
    database, password = slots[slot]
    binary = root / lane / identities[arm][lane]["binary"]
    arguments, tests = sample_arguments(lane, iterations, smoke)
    env = {**os.environ, "GOMAXPROCS": "1", "GOGC": "100", "GOMEMLIMIT": "off", "GODEBUG": "",
           "TMPDIR": str(root), "LUXO_ADVISORY_MODEL": "holder",
           "LUXO_DURABLE_ARM": arm, "LUXO_MIGPAIR_EMPTY_HISTORY_CREATE": "no",
           "LUXO_MIGPAIR_DATABASE_URL": f"postgres://{database}:{password}@127.0.0.1:5432/{database}?sslmode=disable"}
    proc = subprocess.run([str(binary), *arguments], cwd=root, env=env, stdout=subprocess.PIPE,
                          stderr=subprocess.STDOUT, text=True)
    raw = proc.stdout.replace(password, "[redacted]")
    (evidence / f"{name}.log").write_text(raw)
    if proc.returncode:
        raise RuntimeError(f"failed to measure {name}; see raw transcript")
    return {"arm": arm, "slot": slot, "name": name, "metrics": sample_metrics(raw, lane, iterations, tests),
            "logSHA256": sha(raw.encode())}


def schedule(lane):
    comparisons, controls = ("PQ",), ("PP", "QQ")
    control_count = 2 if lane == "first-query" else 5
    for group, slots in enumerate(("ab", "ba"), 1):
        for pair in range(1, 11):
            active = comparisons + (controls if pair <= control_count else ())
            rotate = (pair + group - 1) % len(active)
            indices = (0, 1) if (pair + group) % 2 == 0 else (1, 0)
            for arms in active[rotate:] + active[:rotate]:
                yield f"{arms.lower()}{group}", arms, pair, slots, indices


def check_observation(before, after):
    first, last = before["checkpoints"], after["checkpoints"]
    if before["cpus"] != after["cpus"] or first[:2] != last[:2] or first[-1] != last[-1]:
        raise RuntimeError("CPU scope, checkpoint counts or checkpoint epoch changed")


def summarize(runs, lane):
    groups = sorted({row["group"] for row in runs})
    result = {}
    for group in groups:
        result[group] = {}
        group_rows = [row for row in runs if row["group"] == group]
        for leaf in sorted(LEAVES[lane]):
            result[group][leaf] = {}
            for metric in METRICS:
                values = [Decimal(row["delta"][leaf][metric]) for row in group_rows]
                result[group][leaf][metric] = {"minimum": str(min(values)), "median": str(statistics.median(values)),
                    "maximum": str(max(values)), "maximumPositive": str(max(0, max(values)))}
    return result


def run_lane(root, evidence, helper, lane, identities, smoke):
    out = evidence / (lane + "-smoke" if smoke else lane)
    out.mkdir()
    slots, runs = {}, []
    state = {"status": "failed", "initial": {}, "clean": {}, "measurements": runs, "iterations": 3}
    token = secrets.token_hex(8)
    arms_for_lane = tuple(arm for arm, identity in identities.items() if lane in identity)

    def verify_binaries():
        for arm in arms_for_lane:
            expected = identities[arm][lane]
            if sha((root / lane / expected["binary"]).read_bytes()) != expected["sha256"]:
                raise RuntimeError("prepared benchmark binary changed")

    try:
        verify_binaries()
        for key in ("a", "b"):
            helper.create_slot(key, slots, token)
            state["initial"][key] = "0|0"
        if smoke:
            state["iterations"] = 1
            before = admit(out / "before-smoke.json")
            state["smoke"] = [sample(root, lane, f"smoke-{arm}", arm, slots, "ab"[index % 2], identities, 1, out, True)
                              for index, arm in enumerate(arms_for_lane)]
            after = observation()
            put(out / "after-smoke.json", after)
            if before["cpus"] != after["cpus"]:
                raise RuntimeError("runner CPU scope changed during smoke checks")
            verify_binaries()
            state["status"] = "pass"
            return
        before = admit(out / "before-formal.json")
        for group, arms, pair, db, indices in schedule(lane):
            values = {i: sample(root, lane, f"{group}-{pair:02d}-{i}", arms[i], slots, db[i], identities, 3, out)
                      for i in indices}
            difference = {leaf: {metric: str(Decimal(values[1]["metrics"][leaf][metric]) -
                Decimal(values[0]["metrics"][leaf][metric])) for metric in METRICS} for leaf in LEAVES[lane]}
            runs.append({"group": group, "pair": pair, "order": indices, "arms": [values[0], values[1]], "delta": difference})
        after = observation()
        put(out / "after-formal.json", after)
        check_observation(before, after)
        verify_binaries()
        put(out / "summary.json", summarize(runs, lane))
        state["status"] = "pass"
    finally:
        state["clean"] = helper.clean_slots(slots)
        put(out / "run.json", state)
        if state["clean"] != {"a": "0|0", "b": "0|0"}:
            raise RuntimeError(f"incomplete cleanup in {lane}")


def replay(repo, root, evidence, entries):
    current = run("git", "-C", str(repo), "rev-list", "--parents", "-n", "1", "HEAD", text=True).strip().split()
    if current != [os.environ["GITHUB_SHA"], PARENT]:
        raise RuntimeError("unexpected diagnostic commit or parent")
    cpu = json.loads(run("lscpu", "-J"))
    put(evidence / "host-cpu.json", cpu)
    require_cpu(cpu)
    env = {key: value for key, value in os.environ.items() if not key.startswith(("LUXO_MIGPAIR_", "LUXO_ADVISORY_", "LUXO_DURABLE_"))}
    env |= {"GOTOOLCHAIN": "local", "GOWORK": "off", "GOFLAGS": "-p=1", "GOMAXPROCS": "2", "CGO_ENABLED": "0",
            "GOENV": "off", "GOARM64": "v8.0", "GOCACHE": str(root / "cache"), "GOMODCACHE": str(root / "modules"),
            "TMPDIR": str(root), "GOTMPDIR": str(root), "GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org"}
    if run("go", "version", env=env, text=True).strip() != "go version go1.27.1 linux/arm64":
        raise RuntimeError("unexpected Go toolchain")
    put(evidence / "inputs.json", {"base": BASE, "baseTree": BASE_TREE, "products": ARMS,
                                  "patches": PATCHES, "payload": entries, "go": "go1.27.1 linux/arm64"})
    git = trees(repo, root)
    identities = {arm: prepare_arm(git, root, arm, env, evidence) for arm in ARMS}
    shared = runpy.run_path(str(PAYLOAD / "shared.py"))
    scope, _ = SERVER["scope"]()
    shared["admin"].__globals__["admin"] = lambda sql: SERVER["admin"](scope, sql)
    helper = SimpleNamespace(**shared)
    put(evidence / "server.json", SERVER["start_owned"]())
    for phase in ("smoke", "formal"):
        failures = {}
        for lane in LEAVES:
            try:
                run_lane(root, evidence, helper, lane, identities, phase == "smoke")
            except (RuntimeError, subprocess.SubprocessError) as err:
                failures[lane] = str(err)
        put(evidence / "outcome.json", {"passed": not failures and phase == "formal", "phase": phase, "failures": failures})
        if failures:
            raise RuntimeError(f"durable PostgreSQL {phase} failed in {sorted(failures)}")


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
