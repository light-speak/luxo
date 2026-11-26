#!/usr/bin/env python3
import argparse
import fcntl
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import secrets
import signal
import subprocess
import sys
import tempfile

HERE = Path(__file__).resolve().parent
FIXTURE = HERE / "advisory_pair_test.go"
A = "89c6db247d43e47064d66b5be08393ca778b2153"
INTERMEDIATE = "d3734a814fc878c00026dcdc16e95fdefad9a2e4"
B = "69574111195c825ac6a85996089f3e20b58853b8"
PATCHES = (
    (HERE.parent / "pg-migration-publication.dmTrPWuk" / "advisory-after-order.patch",
     "f944519ea14b37b706cebe703b3b9f1c547a7873a0d246fba187e8de3da09569", INTERMEDIATE),
    (HERE.parent / "pg-holder-bound.e3Dvjn8Q" / "holder-bound.patch",
     "955feccee04009d4a1acc6cff14025a8849bcfe46de4c70fac11ff77dab34969", B),
)
PATCH_IDENTITIES = [{"file": patch.name, "sha256": digest, "outputTree": tree} for patch, digest, tree in PATCHES]
SHARED = HERE.parent / "pg-migration-timing-3bb.JJy34b3F" / "pair.py"
SHARED_SHA = "9c891834df8486f0b648af3ae418b64c6827f29c792f7739fe43d18781e293da"
LEAVES = {"open-close", "first-lock", "warm-lock", "ordinary-up", "up-no-pending"}
BENCH = re.compile(r"^BenchmarkAdvisoryPair/(\S+?)(?:-\d+)?\s+(\d+)\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op$", re.MULTILINE)


def load_shared_driver():
    if hashlib.sha256(SHARED.read_bytes()).hexdigest() != SHARED_SHA:
        raise RuntimeError("verified role/runner helper digest changed")
    spec = importlib.util.spec_from_file_location("migration_pair_driver", SHARED)
    shared = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(shared)
    for patch, digest, _ in PATCHES:
        if shared.sha(patch) != digest:
            raise RuntimeError(f"release patch digest changed for {patch.name}")
    shared.ROOT = HERE.parent / "pg-migration-publication.dmTrPWuk" / "source"
    shared.HERE = HERE
    shared.FIXTURE = FIXTURE
    shared.LEAVES = LEAVES
    return shared


def prepare(shared):
    env = {**shared.BUILD_ENV, "GOWORK": "off", "GOFLAGS": "-p=1", "GOMAXPROCS": "2", "CGO_ENABLED": "0"}
    version = shared.capture("go", "version", env=env)
    if not version.startswith("go version go1.27.1 "):
        raise RuntimeError(f"unexpected Go toolchain: {version}")
    env["GOOS"], env["GOARCH"] = shared.capture("go", "env", "GOHOSTOS", "GOHOSTARCH", env=env).splitlines()
    if shared.git("cat-file", "-t", A) != "tree":
        raise RuntimeError("exact migration-policy base tree is unavailable")
    alternates = Path(shared.git("rev-parse", "--path-format=absolute", "--git-common-dir")) / "objects"

    with tempfile.TemporaryDirectory(dir=HERE, prefix="prepare-") as scratch:
        temp = Path(scratch)
        private = temp / "objects.git"
        shared.checked("git", "init", "--bare", "-q", str(private))
        git = ("git", "--git-dir", str(private))
        git_env = {**os.environ, "GIT_ALTERNATE_OBJECT_DIRECTORIES": str(alternates)}
        shared.checked(*git, "read-tree", A, env=git_env)
        for patch, _, tree in PATCHES:
            shared.checked(*git, "apply", "--cached", str(patch), env=git_env)
            if shared.capture(*git, "write-tree", env=git_env) != tree:
                raise RuntimeError(f"invalid projection after {patch.name}")
        tmp = temp / "go"
        tmp.mkdir()
        env["TMPDIR"] = str(tmp)
        env["GOTMPDIR"] = str(tmp)

        def unpack(tree, output):
            with subprocess.Popen([*git, "archive", tree], env=git_env, stdout=subprocess.PIPE) as archive:
                with archive.stdout as stream:
                    shared.checked("tar", "-xf", "-", "-C", str(output), stdin=stream)
                if archive.wait():
                    raise RuntimeError(f"cannot archive tree {tree}")

        shared.unpack = unpack
        arms = {label: shared.prepare_arm(label, tree, env) for label, tree in (("A", A), ("B", B))}
    shared.write_json(HERE / "prepared.json", {
        "baseTree": A, "preparedAt": shared.now(), "go": version, "cgo": 0, "trimpath": True,
        "platform": env["GOOS"] + "/" + env["GOARCH"], "fixtureSHA256": shared.sha(FIXTURE),
        "driverSHA256": shared.sha(Path(__file__)), "helperSHA256": SHARED_SHA, "patches": PATCH_IDENTITIES,
        "arms": arms,
    })


def smoke_semantics(log, arm):
    rounds = re.findall(r"session-round=(\d+) active=\[([\d ]+)\] holding-PID=(\d+) released-PID=(\d+)", log)
    ordinary = re.findall(r"ordinary-writer-PID=(\d+)", log)
    standalone = re.findall(r"standalone-writer-PID=(\d+) standalone-lock-PID=(\d+)", log)
    contention = re.findall(r"contention=([a-z-]+)", log)
    required = "recursive-session-acquired" if arm == "A" else "waiter-deadline"
    if len(rounds) != 3 or len(ordinary) != 1 or len(standalone) != 1 or contention != [required]:
        raise RuntimeError(f"required session or contention evidence missing from {arm}")
    writer, lock = map(int, standalone[0])
    if writer <= 0 or writer != lock:
        raise RuntimeError(f"standalone writer did not hold advisory lock in {arm}")
    return {"rounds": [{"round": int(number), "sessions": [int(pid) for pid in pids.split()],
                        "heldPID": int(held), "releasedPID": int(released)}
                       for number, pids, held, released in rounds],
            "ordinaryWriterPID": int(ordinary[0]),
            "standaloneWriterPID": writer, "standaloneLockPID": lock, "contention": contention[0]}


def verified_smoke(shared, prepared, server):
    for path in sorted(HERE.glob("smoke-*/run.json"), reverse=True):
        result = json.loads(path.read_text())
        if (result.get("mode"), result.get("status"), result.get("prepared"), result.get("server"),
            result.get("clean")) != ("smoke", "pass", prepared, server, {"a": "0|0", "b": "0|0"}):
            continue
        rows = result["measurements"]
        if [row["arm"] for row in rows] != ["A", "B"]:
            raise RuntimeError("smoke arms are incomplete")
        for row in rows:
            artifact = path.parent / f"1x-{row['arm']}.log"
            if (shared.sha(artifact) != row["logSHA256"]
                or smoke_semantics(artifact.read_text(), row["arm"]) != row["sessions"]):
                raise RuntimeError("smoke lifecycle evidence no longer matches its raw log")
        return {"path": str(path.relative_to(HERE)), "sha256": shared.sha(path)}
    raise RuntimeError("run the exact holder smoke before formal measurement")


def measure(shared, output, name, arm, slot, slots, prepared, iterations, smoke, tmp):
    role, password = slots[slot]
    env = {
        **os.environ, "GOMAXPROCS": "1", "GOGC": "100", "GOMEMLIMIT": "off", "GODEBUG": "",
        "TMPDIR": str(tmp), "LUXO_ADVISORY_MODEL": "pooled" if arm == "A" else "holder",
        "LUXO_MIGPAIR_DATABASE_URL": f"postgres://{role}:{password}@127.0.0.1:5432/{role}?sslmode=disable",
    }
    binary = HERE / prepared["arms"][arm]["binary"]
    args = [str(binary), "-test.bench=^BenchmarkAdvisoryPair$", "-test.benchmem",
            "-test.cpu=1", "-test.count=1", "-test.timeout=3m", f"-test.benchtime={iterations}x"]
    args += ["-test.run=^TestAdvisoryPair", "-test.v"] if smoke else ["-test.run=^$"]
    started = shared.now()
    process = subprocess.run(args, cwd=HERE, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    safe = process.stdout.replace(password, "[redacted]")
    raw_log = output / f"{name}.log"
    raw_log.write_text(safe)
    if process.returncode:
        raise RuntimeError(f"{name} failed, see its log")
    metrics = {}
    for leaf, count, elapsed, memory, allocs in BENCH.findall(safe):
        if int(count) != iterations or leaf in metrics:
            raise RuntimeError(f"bad iteration count or duplicate leaf in {name}: {leaf}")
        metrics[leaf] = dict(zip(shared.METRICS, (elapsed, memory, allocs)))
    if set(metrics) != LEAVES or not re.search(r"^PASS\s*$", safe, re.MULTILINE):
        raise RuntimeError(f"unexpected leaf or terminal result in {name}: {set(metrics)}")
    result = {"name": name, "arm": arm, "slot": slot, "startedAt": started,
              "endedAt": shared.now(), "metrics": metrics, "logSHA256": shared.sha(raw_log)}
    if smoke:
        result["sessions"] = smoke_semantics(safe, arm)
    return result


def execute(shared, mode):
    prepared = json.loads((HERE / "prepared.json").read_text())
    if (prepared["fixtureSHA256"] != shared.sha(FIXTURE)
        or prepared["driverSHA256"] != shared.sha(Path(__file__))
        or prepared["helperSHA256"] != SHARED_SHA
        or prepared["patches"] != PATCH_IDENTITIES
        or {arm: entry["tree"] for arm, entry in prepared["arms"].items()} != {"A": A, "B": B}):
        raise RuntimeError("fixture, driver, or source trees changed since prepare")
    for arm in prepared["arms"].values():
        if arm["sha256"] != shared.sha(HERE / arm["binary"]):
            raise RuntimeError("runner binary changed since prepare")
    server = shared.verify_server()
    smoke = verified_smoke(shared, prepared, server) if mode == "formal" else None
    token = secrets.token_hex(8)
    output = HERE / f"{mode}-{token}"
    output.mkdir(mode=0o700)
    print(output, flush=True)
    state = {"mode": mode, "status": "failed", "startedAt": shared.now(), "server": server, "prepared": prepared,
             "iterations": 1 if mode == "smoke" else 3, "gomaxprocs": 1, "gogc": 100, "measurements": []}
    if smoke is not None:
        state["smoke"] = smoke
    slots = {}
    try:
        for key in ("a", "b"):
            shared.create_slot(key, slots, token)
        with tempfile.TemporaryDirectory(dir=HERE, prefix="run-tmp-") as scratch:
            tmp = Path(scratch)

            def run(*args):
                return measure(shared, *args)

            if mode == "smoke":
                for arm, slot in (("A", "a"), ("B", "b")):
                    state["measurements"].append(run(output, f"1x-{arm}", arm, slot, slots, prepared, 1, True, tmp))
            else:
                shared.measure = run
                for group, count, mapping in (
                    ("aa", 4, {"A": "a", "B": "b"}), ("h1", 10, {"A": "a", "B": "b"}),
                    ("h2", 10, {"A": "b", "B": "a"}),
                ):
                    state["measurements"].extend(shared.paired(output, group, count, mapping, slots, prepared, tmp))
                shared.write_summary(output, state["measurements"])
        state["status"] = "pass"
    finally:
        state["clean"] = shared.clean_slots(slots)
        state["endedAt"] = shared.now()
        bad_cleanup = any(count != "0|0" for count in state["clean"].values())
        if bad_cleanup:
            state["status"] = "cleanup-failed"
        shared.write_json(output / "run.json", state)
        if bad_cleanup:
            raise RuntimeError("isolated PostgreSQL role/database cleanup failed")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("mode", choices=("prepare", "smoke", "formal"))
    args = parser.parse_args()
    if HERE.parent.name != "review-by-file" or HERE.parent.parent.name != ".tmp":
        raise RuntimeError("driver must be under the repository task scratch")
    os.umask(0o077)
    sys.dont_write_bytecode = True
    shared = load_shared_driver()
    signal.signal(signal.SIGTERM, shared.interrupted)
    with (HERE / ".driver.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if args.mode == "prepare":
            prepare(shared)
        else:
            execute(shared, args.mode)


if __name__ == "__main__":
    main()
