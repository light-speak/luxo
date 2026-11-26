#!/usr/bin/env python3
"""Measure the one-shot migration intent on the original PostgreSQL protocol."""
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
PARENT = '545261164d4a7c7819f420ffa9533400754662fb'
BASE = '15765dee6705d39de0b5a505d3039d59ebf43584'
BASE_TREE = '75aebe5bd2fa6b45a1c614ace1a816ebc63e4c0a'
PROJECTION = {
    "main": "5017b587f4cf71d30ebba8b542267aed597bb573",
    "mainTree": "f4b13c19316c9fd4cbb0be56d9ba31eab3f98e7f",
    "reviewHead": "411917fe2460a89b60233db8ab85fd7c83ab3955",
    "reviewTree": "cbdacf807d633ba5383b680330be612d7d3588d1",
    "mergeBase": "f83f9c78f9ee03a5d73b7d0a41bae5280a250bfd",
    "mergeBaseTree": "0f97d642325769e2882fd0235c1575af71e3fc3c",
    "mergeTree": "32ec7a87d7797d1017ae338946540f039f0a9efa",
    "priorNativeRun": 45857286205,
    "priorNativeArtifact": 15136741248,
    "priorNativeSHA256": "d68266e78585d107a2371a52f94b5736f35ce73fbaa6ba3132a7341412f24dc8",
}
HISTORICAL = {"run": 45879856292, "attempt": 1, "head": PARENT, "artifact": 15146123973,
              "artifactSHA256": "49f7b4b2a0f28ea4b06d89655982f1375f4b9fbd71c19007293f50358bb8590d"}
ARMS = {'P': BASE_TREE, 'Q': '13a0159134235571a8a1a431d02d6afd362b4718',
        'R': '40e9e4c4aefe41bb6c9fd2d02d5d49f6c7b0909f'}
PATCHES = {
    'Q': (PROJECTION["mergeTree"], 'a2c84ab8db37a7965c63f0a87befc0c5ab3875da2fb49235cc448b8975d8dccd'),
    'R': (ARMS['Q'], 'd46682d848a5640e894f55a6a9ac402f4fc5f356aadd6a0939998b07a07c9355'),
}
DIAGNOSTIC_PATHS = (
    "M\t.github/workflows/diagnose-pg-durable.yml",
    "A\tscripts/diagnostics/pg-durable-hosted/.payload/arm-R.patch.gz.b64",
    "M\tscripts/diagnostics/pg-durable-hosted/.payload/inputs.sha256",
    "M\tscripts/diagnostics/pg-durable-hosted/hosted.py",
)
MODULE_PINS = ("github.com/jackc/pgx/v5 v5.10.0", "golang.org/x/net v0.58.0")
LEAVES = {
    "standalone": {"ordinary/up", "ordinary/up-existing", "concurrent/up", "concurrent/down"},
    "recovery": {"first-journal-and-create-index", "retry-after-invalid-index-repair"},
}
METRICS = ("ns/op", "B/op", "allocs/op")
BENCHMARKS = {"standalone": "BenchmarkMigrationPair", "recovery": "BenchmarkDurableRecovery"}
SMOKE_TESTS = {"standalone": ("TestMigrationPairEmptyPreview",), "recovery": ()}
SELECTORS = {
    "standalone": "^BenchmarkMigrationPair$/^ordinary$/^(up|up-existing)$|^BenchmarkMigrationPair$/^concurrent$/^(up|down)$",
    "recovery": "^BenchmarkDurableRecovery$/^(first-journal-and-create-index|retry-after-invalid-index-repair)$",
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
             "durable_reset_test.go", "used_holder_reuse_test.go", "capacity_public_test.go", "recovery_test.go",
             "arm-Q.patch.gz.b64", "arm-R.patch.gz.b64"}
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
    expected = {BASE: BASE_TREE, PROJECTION["main"]: PROJECTION["mainTree"],
                PROJECTION["reviewHead"]: PROJECTION["reviewTree"],
                PROJECTION["mergeBase"]: PROJECTION["mergeBaseTree"]}
    for commit, tree in expected.items():
        if run(*checkout, "rev-parse", "--verify", commit + "^{tree}", text=True).strip() != tree:
            raise RuntimeError(f"unexpected source tree for {commit}")
    objects = run(*checkout, "rev-parse", "--path-format=absolute", "--git-path", "objects", text=True).strip()
    bare = root / "objects.git"
    run("git", "init", "--bare", "--quiet", str(bare))
    (bare / "objects/info/alternates").write_text(objects + "\n")
    git = ("git", "--no-replace-objects", f"--git-dir={bare}")
    merged = run(*git, "merge-tree", "--write-tree", "--merge-base=" + PROJECTION["mergeBase"],
                 PROJECTION["reviewHead"], PROJECTION["main"], text=True).strip()
    if merged != PROJECTION["mergeTree"]:
        raise RuntimeError("unexpected #151 merge projection")
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
        if not (src / "go.mod").read_text().splitlines().count("go 1.27.1") == 1:
            raise RuntimeError(f"unexpected Go module directive for {arm}")
        modules = run("go", "list", "-mod=readonly", "-m", *(entry.split()[0] for entry in MODULE_PINS),
                      cwd=src, env=workspace, text=True).splitlines()
        if tuple(modules) != MODULE_PINS:
            raise RuntimeError(f"unexpected effective modules for {arm}: {modules}")
        lanes = {"standalone": "migratepair", "recovery": "recoverypair"}
        for lane, folder in lanes.items():
            target = src / "pkg/lux/pg" / folder
            target.mkdir()
            if lane == "standalone":
                (target / "migration_pair_test.go").write_text(standalone_fixture())
                shutil.copyfile(PAYLOAD / "durable_reset_test.go", target / "durable_reset_test.go")
            else:
                for name in ("advisory_pair_test.go", "recovery_test.go"):
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
                                        "arms": result, "modulePins": modules,
                                        "standaloneFixtureSHA256": sha(standalone_fixture().encode())})
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
    comparisons, controls = ("QR", "PR"), ("PP", "QQ", "RR")
    control_count = {"standalone": 5, "recovery": 10}[lane]
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
    changed = run("git", "-C", str(repo), "diff", "--name-status", PARENT, "HEAD", text=True).splitlines()
    if changed != list(DIAGNOSTIC_PATHS):
        raise RuntimeError("unexpected diagnostic append scope")
    cpu = json.loads(run("lscpu", "-J"))
    put(evidence / "host-cpu.json", cpu)
    require_cpu(cpu)
    env = {key: value for key, value in os.environ.items() if not key.startswith(("LUXO_MIGPAIR_", "LUXO_ADVISORY_", "LUXO_DURABLE_", "LUXO_CAPACITY_"))}
    env |= {"GOTOOLCHAIN": "local", "GOWORK": "off", "GOFLAGS": "-p=1", "GOMAXPROCS": "2", "CGO_ENABLED": "0",
            "GOENV": "off", "GOARM64": "v8.0", "GOCACHE": str(root / "cache"), "GOMODCACHE": str(root / "modules"),
            "TMPDIR": str(root), "GOTMPDIR": str(root), "GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org"}
    if run("go", "version", env=env, text=True).strip() != "go version go1.27.1 linux/arm64":
        raise RuntimeError("unexpected Go toolchain")
    put(evidence / "inputs.json", {"base": BASE, "baseTree": BASE_TREE, "products": ARMS,
                                  "patches": PATCHES, "payload": entries, "go": "go1.27.1 linux/arm64",
                                  "projection": PROJECTION, "historical": HISTORICAL, "modulePins": MODULE_PINS,
                                  "comparisons": {"QR": "one-shot intent only", "PR": "remaining product costs"}})
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
