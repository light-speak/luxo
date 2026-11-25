#!/usr/bin/env python3
import argparse
import fcntl
import hashlib
import json
import os
from datetime import datetime, timezone
from pathlib import Path
import platform
import re
import runpy
import secrets
import shutil
import signal
import subprocess
import tempfile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent / "objects.git"
BASE = "e101cf7699acb358305fd373934e8813a98087bd"
ARMS = {
    "A": "377fdb5bbe6815a45930c53e10797f08cf07c917",
    "B": "500638ea202b7b696de2fc80dde97f90e52a21e2",
}
DEV_PG = "131fdc870d7ff104c1e18c2bb9caeec3408f2a780153c8bca2cd23ecdd0d5611"
DEV_PG_IMAGE = "sha256:252a443e2936039b83dd8da1373d01e59e932d1054fa6adf1bc061f1d56ae60a"
APPLICATION = "luxo_migperf_writer"
FIXTURE = HERE / "migration_pair_test.go"
STAGE_FIXTURE = HERE / "migration_stage_trace_test.go"
STAGE_PARSER = HERE / "stage_parser.py"
TRACE = {
    "A": ("init,checksum,timestamp", "init,checksum,timestamp"),
    "B": ("catalog,checksum,timestamp,index-guard", "catalog,init"),
}
LEAVES = ("concurrent/up-existing-index", "transaction/up-missing-history")
BENCH = re.compile(r"^BenchmarkMigrationPair/(\S+?)(?:-\d+)?\s+(\d+)\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op$", re.MULTILINE)
METRICS = ("ns/op", "B/op", "allocs/op")
BUILD_ENV = {**os.environ, "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off"}


def capture(*args, **kwargs):
    return subprocess.check_output(args, text=True, **kwargs).strip()


def checked(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def sha(path):
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")


def now():
    return datetime.now(timezone.utc).isoformat()


def git(*args):
    return capture("git", f"--git-dir={ROOT}", *args)


def unpack(tree, output):
    with subprocess.Popen(["git", f"--git-dir={ROOT}", "archive", tree], stdout=subprocess.PIPE) as git_archive:
        with git_archive.stdout as archive:
            checked("tar", "-xf", "-", "-C", str(output), stdin=archive)
        if git_archive.wait():
            raise RuntimeError(f"cannot archive {tree}")


def prepare_arm(label, tree, env):
    with tempfile.TemporaryDirectory(dir=HERE, prefix=f"build-{label}-") as scratch:
        src = Path(scratch)
        unpack(tree, src)
        checked("sh", "scripts/setup-modules.sh", str(src), cwd=src, env=env)
        package = src / "pkg/lux/pg/migrate"
        for fixture in (FIXTURE, STAGE_FIXTURE):
            destination = package / fixture.name
            if destination.exists():
                raise RuntimeError("cannot overwrite a tracked fixture")
            shutil.copyfile(fixture, destination)
        binary = HERE / f"runner-{label}.test"
        checked("go", "test", "-c", "-trimpath", "-o", str(binary), "./pkg/lux/pg/migrate",
                cwd=src, env={**env, "GOWORK": str(src / "go.work")})
    return {"tree": tree, "binary": binary.name, "sha256": sha(binary)}


def prepare():
    if git("rev-parse", f"{BASE}^{{tree}}") != ARMS["A"] or any(git("cat-file", "-t", tree) != "tree" for tree in ARMS.values()):
        raise RuntimeError("the exact base commit and staged candidate tree must be present locally")
    env = {**BUILD_ENV, "GOWORK": "off", "GOFLAGS": "-p=1", "GOMAXPROCS": "2", "CGO_ENABLED": "0"}
    version = capture("go", "version", env=env)
    if not version.startswith("go version go1.27.1 "):
        raise RuntimeError(f"unexpected Go toolchain: {version}")
    env["GOOS"], env["GOARCH"] = capture("go", "env", "GOHOSTOS", "GOHOSTARCH", env=env).splitlines()
    with tempfile.TemporaryDirectory(dir=HERE, prefix="go-tmp-") as tmp:
        env["TMPDIR"] = tmp
        env["GOTMPDIR"] = tmp
        arms = {label: prepare_arm(label, tree, env) for label, tree in ARMS.items()}
    write_json(HERE / "prepared.json", {
        "baseCommit": BASE, "preparedAt": now(), "go": version, "cgo": 0, "trimpath": True,
        "platform": env["GOOS"] + "/" + env["GOARCH"], "fixtureSHA256": sha(FIXTURE),
        "driverSHA256": sha(Path(__file__)), "arms": arms,
        "stageFixtureSHA256": sha(STAGE_FIXTURE), "stageParserSHA256": sha(STAGE_PARSER),
        "sourceBindingSHA256": sha(HERE.parent / "source-e101.txt"),
        "orderPatchSHA256": sha(HERE.parent / "order.patch"),
    })


def docker(*args, **kwargs):
    return capture("docker", *args, **kwargs)


def admin(sql):
    return docker("exec", "-i", DEV_PG, "sh", "-c",
                  'exec psql -X -Atq -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres',
                  input=sql + "\n", stderr=subprocess.PIPE)


def verify_server():
    fmt = "{{.Id}}|{{.Name}}|{{.State.Running}}|{{.Image}}"
    ident, name, running, image = docker("inspect", "--format", fmt, DEV_PG).split("|")
    if (ident, name, running, image) != (DEV_PG, "/dev-pg", "true", DEV_PG_IMAGE):
        raise RuntimeError("unexpected running PostgreSQL container")
    if docker("port", DEV_PG, "5432/tcp") != "127.0.0.1:5432":
        raise RuntimeError("unexpected PostgreSQL loopback port")
    settings = admin("SELECT current_setting('server_version'), current_setting('fsync'), "
                     "current_setting('synchronous_commit'), current_setting('shared_buffers'), "
                     "current_setting('checkpoint_timeout'), current_setting('max_wal_size');").split("|")
    if len(settings) != 6 or settings[:3] != ["16.14", "on", "on"]:
        raise RuntimeError("expected PostgreSQL 16.14 with fsync and synchronous_commit enabled")
    guest_cpu = docker("exec", DEV_PG, "sh", "-c",
                       "awk -F: '/^(model name|Hardware|CPU part)[[:space:]]*:/{print $0; exit}' /proc/cpuinfo")
    host_cpu = platform.processor()
    if platform.system() == "Darwin":
        hardware = capture("/usr/sbin/system_profiler", "-detailLevel", "mini", "SPHardwareDataType",
                           stderr=subprocess.DEVNULL)
        match = re.search(r"^\s*(?:Chip|Processor Name):\s*(.+)$", hardware, re.MULTILINE)
        if match:
            host_cpu = match.group(1)
    if not host_cpu:
        raise RuntimeError("cannot identify benchmark host CPU")
    return {"container": ident, "image": image, "guest": docker("exec", DEV_PG, "uname", "-srm"),
            "guestCPU": guest_cpu, "host": list(platform.uname()), "hostCPU": host_cpu,
            "settings": dict(zip(("version", "fsync", "synchronous_commit", "shared_buffers",
                                  "checkpoint_timeout", "max_wal_size"), settings))}


def counts(role):
    return admin(f"SELECT (SELECT count(*) FROM pg_database WHERE datname='{role}'), "
                 f"(SELECT count(*) FROM pg_roles WHERE rolname='{role}');")


def create_slot(slot, slots, token):
    role = f"luxo_migperf_{token}_{slot}"
    if counts(role) != "0|0":
        raise RuntimeError("random scope conflicts with an existing database or role")
    password = secrets.token_hex(32)
    slots[slot] = role, password
    admin(f"CREATE ROLE {role} LOGIN PASSWORD '{password}' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;")
    admin(f"CREATE DATABASE {role} OWNER {role} TEMPLATE template0;\nREVOKE ALL ON DATABASE {role} FROM PUBLIC;")
    if counts(role) != "1|1":
        raise RuntimeError("isolated database and role did not materialize")


def clean_slots(slots):
    final = {}
    for slot, (role, _) in slots.items():
        try:
            admin(f"DROP DATABASE IF EXISTS {role} WITH (FORCE);\nDROP ROLE IF EXISTS {role};")
            final[slot] = counts(role)
        except (subprocess.CalledProcessError, OSError):
            final[slot] = "cleanup-error"
    return final


def measure(output, name, arm, slot, slots, prepared, iterations, smoke, tmp):
    role, password = slots[slot]
    env = {
        **os.environ, "GOMAXPROCS": "1", "GOGC": "100", "GOMEMLIMIT": "off", "GODEBUG": "",
        "TMPDIR": str(tmp),
        "LUXO_MIGPAIR_DATABASE_URL": f"postgres://{role}:{password}@127.0.0.1:5432/{role}?sslmode=disable",
        "LUXO_MIGPAIR_ARM": arm,
    }
    binary = HERE / prepared["arms"][arm]["binary"]
    args = [str(binary), "-test.bench=^BenchmarkMigrationPair$", "-test.benchmem",
            "-test.cpu=1", "-test.count=1", "-test.timeout=3m", f"-test.benchtime={iterations}x"]
    args += ["-test.run=^TestMigrationPair(SQLControl|HistoryErrorControl)$", "-test.v"] if smoke else ["-test.run=^$"]
    started = now()
    process = subprocess.run(args, cwd=HERE, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    safe = process.stdout.replace(password, "[redacted]")
    (output / f"{name}.log").write_text(safe)
    if process.returncode:
        raise RuntimeError(f"{name} failed, see its log")
    metrics = {}
    for leaf, count, elapsed, memory, allocs in BENCH.findall(safe):
        if int(count) != iterations or leaf in metrics:
            raise RuntimeError(f"bad iteration count or duplicate leaf in {name}: {leaf}")
        metrics[leaf] = dict(zip(METRICS, (elapsed, memory, allocs)))
    if set(metrics) != set(LEAVES) or not re.search(r"^PASS\s*$", safe, re.MULTILINE):
        raise RuntimeError(f"unexpected leaf or terminal result in {name}: {set(metrics)}")
    observed = dict(re.findall(r"pgx-order (\S+) (\S+)", safe))
    errors = re.findall(r"pgx-error legacy-history (\S+)", safe)
    if smoke and (observed != dict(zip(LEAVES, TRACE[arm])) or errors != ["42703"]):
        raise RuntimeError(f"unexpected pgx query or error trace in {name}: {observed} {errors}")
    return {"name": name, "arm": arm, "slot": slot, "startedAt": started,
            "endedAt": now(), "metrics": metrics, "pgxQueryTrace": observed, "pgxErrorTrace": errors}


def trace_arm(output, name, arm, slot, slots, prepared, tmp):
    role, password = slots[slot]
    env = {**os.environ, "GOMAXPROCS": "1", "GOGC": "100", "GOMEMLIMIT": "off", "GODEBUG": "",
           "TMPDIR": str(tmp), "LUXO_MIGPAIR_STAGE_TRACE": "1",
           "LUXO_MIGPAIR_DATABASE_URL": f"postgres://{role}:{password}@127.0.0.1:5432/{role}?sslmode=disable"}
    args = [str(HERE / prepared["arms"][arm]["binary"]), "-test.run=^TestMigrationPairStageTrace$",
            "-test.cpu=1", "-test.count=1", "-test.v", "-test.timeout=3m"]
    started = now()
    proc = subprocess.run(args, cwd=HERE, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    safe = proc.stdout.replace(password, "[redacted]")
    (output / f"{name}.log").write_text(safe)
    if proc.returncode:
        raise RuntimeError(f"{name} failed, see its log")
    validate = runpy.run_path(str(STAGE_PARSER))["validate_stage_trace"]
    return {"name": name, "arm": arm, "slot": slot, "startedAt": started, "endedAt": now(),
            "phases": validate(safe, arm)}


def execute(mode):
    prepared = json.loads((HERE / "prepared.json").read_text())
    evidence = {
        "fixtureSHA256": FIXTURE, "driverSHA256": Path(__file__),
        "stageFixtureSHA256": STAGE_FIXTURE, "stageParserSHA256": STAGE_PARSER,
        "sourceBindingSHA256": HERE.parent / "source-e101.txt", "orderPatchSHA256": HERE.parent / "order.patch",
    }
    if any(prepared[key] != sha(path) for key, path in evidence.items()):
        raise RuntimeError("fixture, driver, or source binding changed since prepare")
    if prepared["baseCommit"] != BASE or set(prepared["arms"]) != set(ARMS):
        raise RuntimeError("base or required source arm changed since prepare")
    for label, tree in ARMS.items():
        arm = prepared["arms"][label]
        if arm["tree"] != tree or arm["binary"] != f"runner-{label}.test" or arm["sha256"] != sha(HERE / arm["binary"]):
            raise RuntimeError("runner source or binary changed since prepare")
    server = verify_server()
    token = secrets.token_hex(8)
    output = HERE / f"{mode}-{token}"
    output.mkdir(mode=0o700)
    print(output, flush=True)
    state = {"mode": mode, "status": "failed", "startedAt": now(), "server": server, "prepared": prepared,
             "iterations": 1 if mode == "smoke" else 12, "gomaxprocs": 1, "gogc": 100,
             "applicationName": APPLICATION, "pgxQueryExecMode": "cache_statement",
             "comparisons": ["A/B stage trace; one LR and one RL"] if mode == "trace" else [],
             "initial": {}, "measurements": []}
    slots = {}
    try:
        for key in ("a",) if mode == "smoke" else ("a", "b"):
            create_slot(key, slots, token)
            state["initial"][key] = "0|0"
        with tempfile.TemporaryDirectory(dir=HERE, prefix="run-tmp-") as scratch:
            tmp = Path(scratch)
            if mode == "smoke":
                for arm in ARMS:
                    state["measurements"].append(measure(output, f"1x-{arm}", arm, "a", slots, prepared, 1, True, tmp))
            else:
                for index, positions in enumerate(((("A", "a"), ("B", "b")), (("B", "a"), ("A", "b"))), 1):
                    for arm, slot in positions:
                        name = f"trace-{index}-{arm}-{slot}"
                        state["measurements"].append(trace_arm(output, name, arm, slot, slots, prepared, tmp))
        state["status"] = "pass"
    finally:
        state["clean"] = clean_slots(slots)
        state["endedAt"] = now()
        bad_cleanup = any(count != "0|0" for count in state["clean"].values())
        if bad_cleanup:
            state["status"] = "cleanup-failed"
        write_json(output / "run.json", state)
        if bad_cleanup:
            raise RuntimeError("isolated PostgreSQL role/database cleanup failed")


def interrupted(_signum, _frame):
    raise KeyboardInterrupt


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("mode", choices=("prepare", "smoke", "trace"))
    args = parser.parse_args()
    if args.mode == "trace" and os.environ.get("LUXO_PG_ORDER_EXCLUSIVE") != "1":
        parser.error("formal sampling requires the coordinated exclusive window")
    if HERE.parent.parent.name != "review-by-file" or HERE.parent.parent.parent.name != ".tmp":
        raise RuntimeError("driver must be under the repository task scratch")
    os.umask(0o077)
    signal.signal(signal.SIGTERM, interrupted)
    with (HERE / ".driver.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if args.mode == "prepare":
            prepare()
        else:
            execute(args.mode)


if __name__ == "__main__":
    main()
