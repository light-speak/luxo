#!/usr/bin/env python3
import argparse
import fcntl
import hashlib
import json
import os
from decimal import Decimal
from datetime import datetime, timezone
from pathlib import Path
import platform
import re
import secrets
import shutil
import signal
import subprocess
import tempfile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
BASE = "3bb59ed140dfc959e4cb4419397a009536ea3f4c"
BASE_TREE = "9e93fb1d1b61e495088b345f834c7ed94ca89060"
CANDIDATE = "f59ec9762ba468b4e3b6ee7146330445a185440e"
DEV_PG = "131fdc870d7ff104c1e18c2bb9caeec3408f2a780153c8bca2cd23ecdd0d5611"
APPLICATION = "luxo_migperf_writer"
FIXTURE = HERE / "migration_pair_test.go"
LEAVES = {
    "ordinary/up", "ordinary/down", "ordinary/up-existing",
    "preview/pending", "preview/no-pending",
    "concurrent/up", "concurrent/down", "concurrent/up-existing-index",
}
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
    return capture("git", "-C", str(ROOT), *args)


def unpack(tree, output):
    with subprocess.Popen(["git", "-C", str(ROOT), "archive", tree], stdout=subprocess.PIPE) as git_archive:
        with git_archive.stdout as archive:
            checked("tar", "-xf", "-", "-C", str(output), stdin=archive)
        if git_archive.wait():
            raise RuntimeError(f"cannot archive {tree}")


def prepare_arm(label, tree, env):
    with tempfile.TemporaryDirectory(dir=HERE, prefix=f"build-{label}-") as scratch:
        src = Path(scratch)
        unpack(tree, src)
        checked("sh", "scripts/setup-modules.sh", str(src), cwd=src, env=env)
        package = src / "pkg/lux/pg/migratepair"
        package.mkdir()
        shutil.copyfile(FIXTURE, package / FIXTURE.name)
        binary = HERE / f"runner-{label}.test"
        checked("go", "test", "-c", "-trimpath", "-o", str(binary), "./pkg/lux/pg/migratepair",
                cwd=src, env={**env, "GOWORK": str(src / "go.work")})
    return {"tree": tree, "binary": binary.name, "sha256": sha(binary)}


def prepare():
    if git("rev-parse", f"{BASE}^{{tree}}") != BASE_TREE or git("cat-file", "-t", CANDIDATE) != "tree":
        raise RuntimeError("the exact base commit and staged candidate tree must be present locally")
    env = {**BUILD_ENV, "GOWORK": "off", "GOFLAGS": "-p=1", "GOMAXPROCS": "2", "CGO_ENABLED": "0"}
    version = capture("go", "version", env=env)
    if not version.startswith("go version go1.27.1 "):
        raise RuntimeError(f"unexpected Go toolchain: {version}")
    env["GOOS"], env["GOARCH"] = capture("go", "env", "GOHOSTOS", "GOHOSTARCH", env=env).splitlines()
    with tempfile.TemporaryDirectory(dir=HERE, prefix="go-tmp-") as tmp:
        env["TMPDIR"] = tmp
        env["GOTMPDIR"] = tmp
        arms = {label: prepare_arm(label, tree, env) for label, tree in (("A", BASE_TREE), ("B", CANDIDATE))}
    write_json(HERE / "prepared.json", {
        "baseCommit": BASE, "preparedAt": now(), "go": version, "cgo": 0, "trimpath": True,
        "platform": env["GOOS"] + "/" + env["GOARCH"], "fixtureSHA256": sha(FIXTURE),
        "driverSHA256": sha(Path(__file__)), "arms": arms,
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
    if (ident, name, running) != (DEV_PG, "/dev-pg", "true"):
        raise RuntimeError("unexpected running PostgreSQL container")
    if docker("port", DEV_PG, "5432/tcp") != "127.0.0.1:5432":
        raise RuntimeError("unexpected PostgreSQL loopback port")
    settings = admin("SELECT current_setting('server_version'), current_setting('fsync'), "
                     "current_setting('synchronous_commit'), current_setting('shared_buffers'), "
                     "current_setting('checkpoint_timeout'), current_setting('max_wal_size');").split("|")
    if settings[1:3] != ["on", "on"]:
        raise RuntimeError("PostgreSQL fsync and synchronous_commit must both be on")
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
        "LUXO_MIGPAIR_EMPTY_HISTORY_CREATE": "yes" if arm == "A" else "no",
    }
    binary = HERE / prepared["arms"][arm]["binary"]
    args = [str(binary), "-test.bench=^BenchmarkMigrationPair$", "-test.benchmem",
            "-test.cpu=1", "-test.count=1", "-test.timeout=3m", f"-test.benchtime={iterations}x"]
    args += ["-test.run=^TestMigrationPairEmptyPreview$", "-test.v"] if smoke else ["-test.run=^$"]
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
    if set(metrics) != LEAVES or not re.search(r"^PASS\s*$", safe, re.MULTILINE):
        raise RuntimeError(f"unexpected leaf or terminal result in {name}: {set(metrics)}")
    empty = re.findall(r"history created=(true|false)", safe)
    if smoke and empty != (["true"] if arm == "A" else ["false"]):
        raise RuntimeError(f"unexpected empty preview state in {name}: {empty}")
    return {"name": name, "arm": arm, "slot": slot, "startedAt": started,
            "endedAt": now(), "metrics": metrics,
            "emptyHistoryCreated": (empty[0] == "true") if smoke else None}


def paired(output, group, count, mapping, slots, prepared, tmp):
    for pair in range(1, count + 1):
        order = ("A", "B") if pair % 2 else ("B", "A")
        rows = []
        for position in order:
            arm = position if group != "aa" else "A"
            slot = mapping[position]
            name = f"{group}-{pair:02d}-{position}-{slot}"
            rows.append(measure(output, name, arm, slot, slots, prepared, 3, False, tmp))
        by_position = dict(zip(order, rows))
        diff = {
            leaf: {m: str(Decimal(by_position["B"]["metrics"][leaf][m]) -
                          Decimal(by_position["A"]["metrics"][leaf][m])) for m in METRICS}
            for leaf in sorted(LEAVES)
        }
        yield {"group": group, "pair": pair, "order": "".join(order), "arms": rows,
               "signedBminusA": diff}


def write_summary(output, pairs):
    with (output / "summary.tsv").open("w") as file:
        file.write("group\tleaf\tmetric\tmin-signed\tmax-signed\tmax-positive\n")
        for group in ("aa", "h1", "h2"):
            group_pairs = [pair for pair in pairs if pair["group"] == group]
            for leaf in sorted(LEAVES):
                for metric in METRICS:
                    values = [Decimal(pair["signedBminusA"][leaf][metric]) for pair in group_pairs]
                    file.write(f"{group}\t{leaf}\t{metric}\t{min(values)}\t{max(values)}\t{max(0, max(values))}\n")


def execute(mode):
    prepared = json.loads((HERE / "prepared.json").read_text())
    if prepared["fixtureSHA256"] != sha(FIXTURE) or prepared["driverSHA256"] != sha(Path(__file__)):
        raise RuntimeError("fixture or driver changed since prepare")
    for arm in prepared["arms"].values():
        if arm["sha256"] != sha(HERE / arm["binary"]):
            raise RuntimeError("runner binary changed since prepare")
    server = verify_server()
    token = secrets.token_hex(8)
    output = HERE / f"{mode}-{token}"
    output.mkdir(mode=0o700)
    print(output, flush=True)
    state = {"mode": mode, "status": "failed", "startedAt": now(), "server": server, "prepared": prepared,
             "iterations": 1 if mode == "smoke" else 3, "gomaxprocs": 1, "gogc": 100,
             "applicationName": APPLICATION, "measurements": []}
    slots = {}
    try:
        for key in ("a", "b"):
            create_slot(key, slots, token)
        with tempfile.TemporaryDirectory(dir=HERE, prefix="run-tmp-") as scratch:
            tmp = Path(scratch)
            if mode == "smoke":
                for arm, slot in (("A", "a"), ("B", "b")):
                    state["measurements"].append(measure(output, f"1x-{arm}", arm, slot, slots, prepared, 1, True, tmp))
            else:
                for group, count, mapping in (
                    ("aa", 4, {"A": "a", "B": "b"}), ("h1", 10, {"A": "a", "B": "b"}),
                    ("h2", 10, {"A": "b", "B": "a"}),
                ):
                    state["measurements"].extend(paired(output, group, count, mapping, slots, prepared, tmp))
                write_summary(output, state["measurements"])
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
    parser.add_argument("mode", choices=("prepare", "smoke", "formal"))
    args = parser.parse_args()
    if HERE.parent.name != "review-by-file" or HERE.parent.parent.name != ".tmp":
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
