#!/usr/bin/env python3
import argparse
import base64
from collections import Counter
import fcntl
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import runpy
import shutil
import signal
import subprocess
import sys
import tempfile
import time

HERE = Path(__file__).resolve().parent
PAYLOAD_ROOT = HERE / ".payload"
SERVER = runpy.run_path(str(HERE / "server.py"))
PREVIOUS = "bd37e678bfe3f280d522ee146cef1b6b8dfeb8af"
PUBLIC_SOURCE = "77b6cdf20d424213f4c1dc90534f924ac6b176d0"
PUBLIC_TREE = "9014a2b7a92f5b2abbb5aca10a6a9a7d0978955f"
BASE = "6c743d11add26d7d1b8e1f40b0b7bcba21e08291"
BASE_TREE = "92887a0ef2a7f875b5024cf5ecfa099ced96e2ad"
ORDER_TREE = "0b46b3f1017cea25f6e6573c048ab11e623be836"
CPU_MODEL = "Neoverse-N2"
COMPRESSED = {"baseline.patch", "order.patch"}
TRACE = {
    "A": ("catalog,checksum,timestamp,index-guard", "catalog,init"),
    "B": ("catalog-session,checksum,timestamp,index-guard", "catalog-session,init"),
}
PAYLOAD = {
    "source-e101.txt", "baseline.commit", *COMPRESSED, "formal/pair.py", "formal/migration_pair_test.go",
    "trace/pair.py", "trace/migration_pair_test.go", "trace/migration_stage_trace_test.go", "trace/stage_parser.py",
}


def capture(*args, **kwargs):
    return subprocess.check_output(args, text=True, **kwargs).strip()


def payload_bytes(path):
    name = path + ".gz.b64" if path in COMPRESSED else path
    source = PAYLOAD_ROOT / name
    if not source.is_file() or source.is_symlink():
        raise RuntimeError(f"missing local hosted payload: {path}")
    data = source.read_bytes()
    if path in COMPRESSED:
        return gzip.decompress(base64.b64decode(b"".join(data.split()), validate=True))
    return data


def verify_inputs():
    entries = {}
    for row in (PAYLOAD_ROOT / "inputs.sha256").read_text().splitlines():
        digest, path = row.split("  ", 1)
        if path not in PAYLOAD or path in entries or not re.fullmatch(r"[a-f0-9]{64}", digest):
            raise RuntimeError("unexpected hosted payload manifest entry")
        if hashlib.sha256(payload_bytes(path)).hexdigest() != digest:
            raise RuntimeError(f"hosted payload hash mismatch: {path}")
        entries[path] = digest
    if set(entries) != PAYLOAD:
        raise RuntimeError("incomplete hosted payload manifest")
    return entries


def write_json(path, data):
    path.write_text(json.dumps(data, indent=2) + "\n")


def create_objects(repo, root):
    source = ["git", "-C", str(repo)]
    if capture(*source, "rev-parse", f"{PUBLIC_SOURCE}^{{tree}}") != PUBLIC_TREE:
        raise RuntimeError("unexpected public source required for PostgreSQL baseline")
    object_path = Path(capture(*source, "rev-parse", "--path-format=absolute", "--git-path", "objects"))
    bare = root / "objects.git"
    subprocess.run(["git", "init", "--bare", "--quiet", str(bare)], check=True)
    (bare / "objects/info/alternates").write_text(str(object_path) + "\n")
    env = {**os.environ, "GIT_INDEX_FILE": str(root / "order.index")}
    git = ["git", f"--git-dir={bare}"]
    subprocess.run([*git, "read-tree", PUBLIC_TREE], env=env, check=True)
    for patch, tree in (("baseline.patch", BASE_TREE), ("order.patch", ORDER_TREE)):
        cmd = [*git, "apply", "--cached", "--binary", "--whitespace=error-all"]
        subprocess.run([*cmd, "--check", str(root / patch)], env=env, check=True)
        subprocess.run([*cmd, str(root / patch)], env=env, check=True)
        if capture(*git, "write-tree", env=env) != tree:
            raise RuntimeError(f"{patch} did not produce its exact pinned tree")
    restored = subprocess.check_output(
        [*git, "hash-object", "-t", "commit", "-w", "--stdin"], input=(root / "baseline.commit").read_bytes(),
    ).decode().strip()
    if restored != BASE or capture(*git, "rev-parse", f"{BASE}^{{tree}}", f"{BASE}^").splitlines() != [BASE_TREE, PUBLIC_SOURCE]:
        raise RuntimeError("reconstructed baseline does not match the published pull request head")
    module_inputs = ["go.mod", "go.sum", ":(glob)**/go.mod", ":(glob)**/go.sum", "VERSION", "scripts/setup-modules.sh"]
    subprocess.run([*git, "diff", "--quiet", BASE_TREE, ORDER_TREE, "--", *module_inputs], check=True)
    return git


def prime_modules(git, root, env):
    src = root / "prime"
    src.mkdir()
    with subprocess.Popen([*git, "archive", BASE_TREE], stdout=subprocess.PIPE) as archive:
        with archive.stdout as stream:
            subprocess.run(["tar", "-xf", "-", "-C", str(src)], stdin=stream, check=True)
        if archive.wait() != 0:
            raise RuntimeError("cannot read exact baseline source for module priming")
    subprocess.run(["sh", "scripts/setup-modules.sh", str(src)], cwd=src, env=env | {"GOWORK": "off"}, check=True)
    subprocess.run(
        ["go", "list", "-p=1", "-deps", "-test", "./pkg/lux/pg/migrate"], cwd=src,
        env=env | {"GOWORK": str(src / "go.work")}, stdout=subprocess.DEVNULL, check=True,
    )
    shutil.rmtree(src)


def run_driver(stage, phase):
    _, task, _ = repo_paths()
    if stage.parent.parent != task or not stage.parent.name.startswith("pg-order-hosted-run-"):
        raise RuntimeError("expected an owned hosted driver workspace")
    if stage.name not in {"trace", "formal"} or phase not in {"prepare", "smoke", stage.name}:
        raise RuntimeError("unexpected driver phase")
    module = runpy.run_path(str(stage / "pair.py"))
    namespace = module["execute"].__globals__
    container, _ = SERVER["scope"]()
    # Keep the original timed driver and bind the incremental source and smoke inputs.
    namespace["BASE"] = BASE
    namespace["ARMS"] = {"A": BASE_TREE, "B": ORDER_TREE}
    namespace["TRACE"] = TRACE
    namespace["admin"] = lambda sql: SERVER["admin"](container, sql)
    namespace["verify_server"] = SERVER["verify_server"]
    sys.argv = [str(stage / "pair.py"), phase]
    module["main"]()


def observe():
    current = SERVER["verify_server"]()
    verify_cpu(current["cpu"])
    capacity = os.cpu_count()
    if not capacity or capacity != int(capture("nproc")):
        raise RuntimeError("runner CPU capacity must be visible without a process quota")
    container, _ = SERVER["scope"]()
    guest_count = int(SERVER["docker"]("exec", container, "nproc"))
    if guest_count != capacity:
        raise RuntimeError("PostgreSQL CPU capacity differs from the runner")
    pg = SERVER["admin"](
        container, "SELECT checkpoints_timed, checkpoints_req, checkpoint_write_time, checkpoint_sync_time, "
                   "buffers_checkpoint, buffers_backend_fsync, stats_reset FROM pg_stat_bgwriter;",
    ).split("|")
    if len(pg) != 7 or not pg[-1]:
        raise RuntimeError("missing PostgreSQL checkpoint counters")
    guest_load = SERVER["docker"]("exec", container, "cat", "/proc/loadavg").split()[:3]
    return {
        "hostLoad": os.getloadavg(), "guestLoad": [float(x) for x in guest_load],
        "cpus": capacity, "pgBGWriter": pg, "server": current, "unixTime": time.time(),
    }


def verify_cpu(cpu):
    values = [row["data"] for row in cpu["lscpu"] if row["field"] == "Model name:"]
    if values != [CPU_MODEL]:
        raise RuntimeError(f"expected {CPU_MODEL} from the original performance study, got {values}")


def admit(evidence, label):
    history = []
    for attempt in range(13):
        point = observe()
        history.append(point)
        write_json(evidence / f"before-{label}.json", history)
        if max(point["hostLoad"][0], point["guestLoad"][0]) <= 0.5 * point["cpus"]:
            return point
        if attempt < 12:
            time.sleep(10)
    raise RuntimeError(f"runner did not enter the preset quiet window before {label}")


def validate_result(result, lane, phase):
    expected = {"a": "0|0"} if phase == "smoke" else {"a": "0|0", "b": "0|0"}
    if result["status"] != "pass" or result["initial"] != expected or result["clean"] != expected:
        raise RuntimeError(f"PostgreSQL test roles or databases survived {lane}-{phase}")
    samples = result["measurements"]
    if phase == "smoke" and [v["arm"] for v in samples] != ["A", "B"]:
        raise RuntimeError("smoke did not verify both SQL controls")
    if phase == "trace" and [(v["arm"], v["slot"]) for v in samples] != [("A", "a"), ("B", "b"), ("B", "a"), ("A", "b")]:
        raise RuntimeError("missing original four trace observations")
    if phase == "formal":
        expected_groups = {f"{group}{slot}": 10 for group in ("aa", "bb", "ab") for slot in (1, 2)}
        if Counter(v["group"] for v in samples) != expected_groups or result["iterations"] != 12:
            raise RuntimeError("missing original 60-pair formal inventory")


def collect_stage(stage, evidence, root, phase, env):
    with (evidence / f"{stage.name}-{phase}.log").open("w") as log:
        result = subprocess.run(
            [sys.executable, "-I", "-B", str(HERE / "hosted.py"), "driver", str(stage), phase],
            cwd=root, env=env, stdout=log, stderr=subprocess.STDOUT,
        )
    if phase == "prepare":
        prepared = json.loads((stage / "prepared.json").read_text()) if result.returncode == 0 else None
        if prepared is not None:
            write_json(evidence / f"{stage.name}-prepared.json", prepared)
    else:
        outputs = list(stage.glob(f"{phase}-[0-9a-f]*"))
        if len(outputs) == 1 and outputs[0].is_dir():
            output = evidence / f"{stage.name}-{outputs[0].name}"
            outputs[0].rename(output)
            if result.returncode == 0:
                validate_result(json.loads((output / "run.json").read_text()), stage.name, phase)
        elif result.returncode == 0:
            raise RuntimeError(f"missing expected result directory for {stage.name}-{phase}")
    result.check_returncode()


def replay(root, evidence, repo, inputs):
    for path in PAYLOAD:
        target = root / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(payload_bytes(path))
    verify_checkout(repo)
    verify_cpu(json.loads(capture("lscpu", "-J")))
    git = create_objects(repo, root)
    workspace = root / "work"
    workspace.mkdir()
    env = {key: value for key, value in os.environ.items() if not key.startswith("LUXO_MIGPAIR_")}
    env |= {
        "GOCACHE": str(workspace / "go-cache"), "GOMODCACHE": str(workspace / "go-mod"),
        "GOTOOLCHAIN": "local", "GOFLAGS": "-p=1", "CGO_ENABLED": "0",
        "GOMAXPROCS": "2", "GOENV": "off", "GOARM64": "v8.0",
        "TMPDIR": str(workspace), "GOTMPDIR": str(workspace),
        "GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org",
        "LUXO_PG_ORDER_EXCLUSIVE": "1",
    }
    if capture("go", "version", env=env) != "go version go1.27.1 linux/arm64":
        raise RuntimeError("expected native Go 1.27.1 linux/arm64")
    write_json(evidence / "inputs.json", {
        "workflowHead": capture("git", "-C", str(repo), "rev-parse", "HEAD"),
        "previousDiagnostic": PREVIOUS, "publicSource": PUBLIC_SOURCE, "cpuModel": CPU_MODEL,
        "base": BASE, "baseTree": BASE_TREE, "orderTree": ORDER_TREE, "payload": inputs,
        "go": "go version go1.27.1 linux/arm64",
        "controller": {name: hashlib.sha256((HERE / name).read_bytes()).hexdigest() for name in ("hosted.py", "server.py")},
    })
    prime_modules(git, root, env)
    env |= {"GOPROXY": "off", "GOSUMDB": "off"}
    for lane in ("trace", "formal"):
        collect_stage(root / lane, evidence, root, "prepare", env)
    write_json(evidence / "server.json", SERVER["start_owned"]())
    for lane in ("trace", "formal"):
        for phase in ("smoke", lane):
            before = admit(evidence, f"{lane}-{phase}")
            try:
                collect_stage(root / lane, evidence, root, phase, env)
            finally:
                after = observe()
                write_json(evidence / f"after-{lane}-{phase}.json", after)
                if after["pgBGWriter"][-1] != before["pgBGWriter"][-1]:
                    raise RuntimeError("PostgreSQL checkpoint statistics were reset during sampling")
    verify_checkout(repo)


def verify_checkout(repo):
    source = ["git", "-C", str(repo)]
    if capture(*source, "rev-parse", "HEAD", "HEAD^").splitlines() != [os.environ["GITHUB_SHA"], PREVIOUS]:
        raise RuntimeError("expected a direct successor of the original PostgreSQL diagnostic")
    relative = HERE.relative_to(repo)
    paths = {relative / "hosted.py", relative / ".payload/inputs.sha256"}
    additions = {relative / ".payload" / name for name in ("baseline.commit", "baseline.patch.gz.b64")}
    edits = paths | {relative / ".payload" / name for name in (
        "formal/migration_pair_test.go", "order.patch.gz.b64", "source-e101.txt",
        "trace/migration_pair_test.go", "trace/migration_stage_trace_test.go", "trace/stage_parser.py",
    )}
    delta = set(capture(*source, "diff", "--name-status", PREVIOUS, "HEAD", "--").splitlines())
    if delta != {f"A\t{path}" for path in additions} | {f"M\t{path}" for path in edits}:
        raise RuntimeError("unexpected diagnostic change list")
    unchanged = {relative / name for name in ("server.py", ".payload/formal/pair.py", ".payload/trace/pair.py")}
    unchanged.add(Path(".github/workflows/diagnose-pg-order.yml"))
    for path in additions | edits | unchanged:
        tracked = subprocess.check_output([*source, "show", f"HEAD:{path}"])
        if tracked != (repo / path).read_bytes():
            raise RuntimeError(f"uncommitted diagnostic input: {path}")


def repo_paths():
    SERVER["scope"]()
    repo = Path(os.environ["GITHUB_WORKSPACE"]).resolve(strict=True)
    if HERE != repo / "scripts/diagnostics/pg-order-hosted":
        raise RuntimeError("hosted controller is outside the current diagnostic checkout")
    tmp = repo / ".tmp"
    task = tmp / "review-by-file"
    if tmp.is_symlink() or task.is_symlink():
        raise RuntimeError("hosted scratch must be local to its checkout")
    task.mkdir(parents=True, exist_ok=True)
    return repo, task, tmp / "pg-order-hosted-evidence"


def on_signal(_signum, _frame):
    raise KeyboardInterrupt


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("mode", choices=("check", "run", "driver", "cleanup", "remove-evidence"))
    parser.add_argument("driver_arguments", nargs="*")
    args = parser.parse_args()
    if args.mode == "driver":
        if len(args.driver_arguments) != 2:
            parser.error("driver requires its stage and phase")
        stage, phase = args.driver_arguments
        run_driver(Path(stage).resolve(strict=True), phase)
        return
    if args.driver_arguments:
        parser.error("only the internal driver accepts extra arguments")
    if args.mode == "check":
        print(json.dumps(verify_inputs(), sort_keys=True))
        return
    os.umask(0o077)
    repo, task, evidence = repo_paths()
    if evidence.is_symlink():
        raise RuntimeError("refusing to operate on symlinked hosted evidence")
    if args.mode == "remove-evidence":
        if evidence.is_dir():
            shutil.rmtree(evidence)
        return
    if args.mode == "cleanup":
        evidence.mkdir(exist_ok=True)
        write_json(evidence / "cleanup-confirmed.json", SERVER["remove_owned"]())
        return
    inputs = verify_inputs()
    evidence.mkdir()
    signal.signal(signal.SIGTERM, on_signal)
    with (evidence / "runner.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        with tempfile.TemporaryDirectory(dir=task, prefix="pg-order-hosted-run-") as tmp:
            try:
                replay(Path(tmp), evidence, repo, inputs)
            finally:
                write_json(evidence / "cleanup.json", SERVER["remove_owned"]())


if __name__ == "__main__":
    main()
