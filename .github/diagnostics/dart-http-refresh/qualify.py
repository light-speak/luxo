"""Gate equivalent Dart HttpTransport work with separate AOT and VM samples."""

import argparse
from base64 import b64decode
from collections import defaultdict
import ctypes
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import platform
import resource
import shutil
import signal
from statistics import median
import subprocess
import tempfile
import time
from types import SimpleNamespace
from urllib.parse import unquote, urlsplit

HERE = Path(__file__).resolve().parent
BASE = "77b6cdf20d424213f4c1dc90534f924ac6b176d0"
PREVIOUS_DIAGNOSTIC = "363248532f038736753f6945d5ad6300faadcfc7"
OPTIMIZED_DIAGNOSTIC = "b274e92be61e5c0e96ef917fb48bacfff248c5a1"
TREES = {"A": "9014a2b7a92f5b2abbb5aca10a6a9a7d0978955f", "B": "02ef80cad47fe91a6537b79da850bba377dffcab"}
PACKAGES = {"A": "bf0dcf2da32afc0fcff9fb3ee82550b5e6cf0795", "B": "cec75af701a414aac5f2d911ea74a09cf5d07bb8"}
PACKAGE = "packages/dart"
PATHS = {"lib/src/transport.dart", "test/http_protocol_test.dart"}
PATCH = "66dd189d5703dd7f6d8f5248be14c572e540abe8dfb0dcb81e9834aa1d29db02"
PUBSPEC = "294368c6b2cc61a62c83880cdca56d1137de356ef9a7682a6396d4fcce9dc5f9"
LOCK_SHA = "0d107eca2444e58cd48cb37abc48bf7115ed04f5101e6ba753ab13dea7e3f8bc"
TRANSPORT = "8f5b4c0a2bdfda1a548e94503159365a577fcdc131753f468fecb168dfbe0deb"
PREVIOUS_NORMALIZED_CONFIG = "7024bc299c7dfa13991b87ac0a8247ec428fa91d583cdb0bc6daf99ad3d49120"
REVISION = "b530c21f7de367b94fb04787bfed9d8e989d75e8"
BENCHSTAT = "golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70"
FIXTURES = {
    "pubspec.lock": LOCK_SHA,
    "probe.dart": "3910cf89a598727eb88da4ce5b893593d99e4ec1991a060e84a410414f27aff9",
    "observer.dart": "60589b6946f93450dd29e1d2df597817e31735601428eb8688e69067e9e25fd5",
}
SDK_INPUTS = (
    "bin/dart", "bin/dartaotruntime", "bin/utils/gen_snapshot",
    "bin/snapshots/frontend_server_aot.dart.snapshot",
    "lib/_internal/vm_platform_strong.dill", "lib/_internal/vm_platform_product.dill",
)
SCENARIOS = ("noHook", "withHook", "refresh")
CODECS = ("json", "binary")
COMPARISONS = ("AA", "AB", "BB")
ENTRY = "test/_audit_http_refresh.dart"
PROPS = {"elapsedNs": "ns/op", "managedBytes": "B/op", "managedObjects": "allocs/op"}


def git(repo, *args, index=None, data=None):
    env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
    env.update(GIT_NO_LAZY_FETCH="1", GIT_OPTIONAL_LOCKS="0")
    if index is not None:
        env["GIT_INDEX_FILE"] = str(index)
    return subprocess.check_output(["git", "--no-replace-objects", "-c", "core.autocrlf=false", "-c", "core.eol=lf",
                                    "-C", str(repo), *map(str, args)], env=env, input=data)


def verify_checkout(repo):
    assert git(repo, "rev-parse", "HEAD").decode().strip() == os.environ["GITHUB_SHA"]
    assert git(repo, "rev-parse", "HEAD^").decode().strip() == OPTIMIZED_DIAGNOSTIC
    assert git(repo, "rev-parse", "HEAD^^").decode().strip() == PREVIOUS_DIAGNOSTIC
    assert git(repo, "rev-parse", "HEAD^^^").decode().strip() == BASE
    paths = {str(HERE.relative_to(repo) / name) for name in (*FIXTURES, "qualify.py", "product.patch.b64")}
    paths.add(".github/workflows/dart-http-refresh-cost.yml")
    changes = git(repo, "diff", "--name-status", BASE, "HEAD").decode().splitlines()
    assert set(changes) == {f"A\t{path}" for path in paths}, changes
    assert all((repo / path).read_bytes() == git(repo, "show", f"HEAD:{path}") for path in paths)


def verify_package(package, manifest):
    assert sha(package / "pubspec.yaml") == PUBSPEC
    assert sha(package / "pubspec.lock") == LOCK_SHA
    assert sha(package / ENTRY) == FIXTURES["probe.dart"]
    assert {path.relative_to(package).as_posix() for path in (package / "lib").rglob("*") if path.is_file()} == {
        name for name in manifest if name.startswith("lib/")}
    assert all(sha(package / name) == fingerprint for name, fingerprint in manifest.items())


def stage_sources(repo, work):
    patch = b64decode(b"".join((HERE / "product.patch.b64").read_bytes().split()), validate=True)
    assert hashlib.sha256(patch).hexdigest() == PATCH
    assert all(sha(HERE / name) == checksum for name, checksum in FIXTURES.items())
    assert git(repo, "rev-parse", f"{BASE}^{{tree}}").decode().strip() == TREES["A"]
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
    assert git(private, "write-tree", index=index).decode().strip() == TREES["B"]
    assert git(private, "rev-parse", f"{TREES['B']}:{PACKAGE}").decode().strip() == PACKAGES["B"]
    assert set(git(private, "diff", "--cached", "--name-only", BASE, index=index).decode().splitlines()) == {
        f"{PACKAGE}/{name}" for name in PATHS}

    roots, sources = {}, {}
    for role, tree in PACKAGES.items():
        package = work / role
        package.mkdir()
        subprocess.run(["tar", "-xf", "-", "-C", package], input=git(private, "archive", tree), check=True)
        manifest = {}
        for row in filter(None, git(private, "ls-tree", "-rz", tree).split(b"\0")):
            meta, name = row.decode().split("\t", 1)
            mode, kind, obj = meta.split()
            assert mode in ("100644", "100755") and kind == "blob", row
            manifest[name] = hashlib.sha256(git(private, "cat-file", "blob", obj)).hexdigest()
        assert not any((package / name).exists() for name in (ENTRY, "pubspec.lock", ".dart_tool"))
        shutil.copyfile(HERE / "pubspec.lock", package / "pubspec.lock")
        shutil.copyfile(HERE / "probe.dart", package / ENTRY)
        verify_package(package, manifest)
        roots[role], sources[role] = package, manifest
    assert set(sources["A"]) == set(sources["B"])
    assert {name for name, fingerprint in sources["A"].items() if sources["B"][name] != fingerprint} == PATHS
    assert sources["B"]["lib/src/transport.dart"] == TRANSPORT
    return roots, sources


def sha(path):
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for data in iter(lambda: stream.read(128 * 1024), b""):
            digest.update(data)
    return digest.hexdigest()


def emit(record):
    print(json.dumps(record, sort_keys=True, ensure_ascii=True), flush=True)


def dependency_profile(package, cache):
    config = json.loads((package / ".dart_tool/package_config.json").read_text())
    graph = json.loads((package / ".dart_tool/package_graph.json").read_text())
    assert config["configVersion"] == 2
    profile = []
    for dep in config["packages"]:
        uri = urlsplit(dep["rootUri"])
        if dep["name"] == "luxo_client":
            assert dep["rootUri"] == "../" and dep["packageUri"] == "lib/"
            root = "<package>"
        else:
            assert uri.scheme == "file" and uri.netloc == "", dep["name"]
            root = Path(unquote(uri.path)).resolve(strict=True).relative_to(cache)
        profile.append((dep["name"], str(root), dep["packageUri"], dep.get("languageVersion")))
    assert len(profile) == len({row[0] for row in profile}) == 52
    return sorted(profile), graph


def first_difference(left, right, path):
    if left == right:
        return None
    if isinstance(left, dict) and isinstance(right, dict):
        for key in sorted(left.keys() | right.keys()):
            field = [*path, key]
            if key not in left or key not in right:
                return {"path": field, "present": [key in left, key in right]}
            difference = first_difference(left[key], right[key], field)
            if difference is not None:
                return difference
    if isinstance(left, (list, tuple)) and isinstance(right, (list, tuple)):
        for index, (old, new) in enumerate(zip(left, right)):
            difference = first_difference(old, new, [*path, index])
            if difference is not None:
                return difference
        return {"path": path, "lengths": [len(left), len(right)]}
    return {"path": path, "values": [left, right]}


def compare_resolution(left_name, right_name, profiles):
    left, right = profiles[left_name], profiles[right_name]
    differences, digests = {}, {}
    for index, section in enumerate(("packageConfig", "packageGraph")):
        old, new = left[index], right[index]
        difference = first_difference(old, new, [])
        if difference is not None:
            differences[section] = difference
        digests[section] = [hashlib.sha256(json.dumps(item, sort_keys=True).encode()).hexdigest()
                            for item in (old, new)]
    emit({"stage": "setup", "phase": "resolution-compare", "arms": [left_name, right_name],
          "state": "different" if differences else "equal", "differences": differences, "sha256": digests})
    return left == right


def locked_pub(sdk, package, env, name, *, offline):
    command = [sdk / "bin/dart", "pub", "get", "--enforce-lockfile"]
    if offline:
        command.append("--offline")
    phase = f"pub-get:{name}:{'offline' if offline else 'locked-download'}"
    emit({"stage": "setup", "phase": phase, "state": "started"})
    result = run_checked(command, package, env, timeout=300, phase=phase)
    emit({"stage": "setup", "phase": phase, "state": "exited", "exit": result.returncode,
          "pid": result.pid, "group": result.group_check, "reaped": result.reaped})
    assert result.returncode == 0, (name, result.stdout, result.stderr)
    assert sha(package / "pubspec.lock") == LOCK_SHA
    return dependency_profile(package, Path(env["PUB_CACHE"]).resolve(strict=True))


def child_cpu():
    usage = resource.getrusage(resource.RUSAGE_CHILDREN)
    return round((usage.ru_utime + usage.ru_stime) * 1_000_000_000)


def kill_group(pid):
    try:
        os.killpg(pid, signal.SIGKILL)
    except ProcessLookupError:
        return


LINUX_SUBREAPER = False


def enable_linux_subreaper():
    global LINUX_SUBREAPER
    libc = ctypes.CDLL(None, use_errno=True)
    # PR_SET_CHILD_SUBREAPER / PR_GET_CHILD_SUBREAPER are stable Linux prctl operations.
    zero = ctypes.c_ulong(0)
    if libc.prctl(36, ctypes.c_ulong(1), zero, zero, zero):
        error = ctypes.get_errno()
        raise OSError(error, os.strerror(error))
    state = ctypes.c_int(0)
    if libc.prctl(37, ctypes.byref(state), zero, zero, zero) or state.value != 1:
        raise RuntimeError("Linux did not enable scoped descendant reaping")
    LINUX_SUBREAPER = True


def related_processes(group):
    records = []
    for entry in Path("/proc").iterdir():
        if not entry.name.isdigit():
            continue
        try:
            stat = (entry / "stat").read_bytes().rsplit(b") ", 1)[1].split()
        except (FileNotFoundError, ProcessLookupError):
            continue
        owner, actual_group = int(stat[1]), int(stat[2])
        if owner == os.getpid() or actual_group == group:
            records.append({"pid": int(entry.name), "ppid": owner, "pgrp": actual_group, "state": stat[0].decode("ascii")})
    return records


def reap_exited_children():
    records = []
    while True:
        try:
            child, status = os.waitpid(-1, os.WNOHANG)
        except ChildProcessError:
            return records, False
        if child == 0:
            return records, True
        records.append({"pid": child, "exit": os.waitstatus_to_exitcode(status)})


def group_exists(group):
    try:
        os.killpg(group, 0)
    except ProcessLookupError:
        return False
    return True


def terminate_adopted(group):
    deadline = time.monotonic() + 2
    cleanup = []
    while True:
        kill_group(group)
        for member in related_processes(group):
            if member["ppid"] == os.getpid() and member["state"] not in ("Z", "X"):
                try:
                    os.kill(member["pid"], signal.SIGKILL)
                except ProcessLookupError:
                    pass
        ended, active = reap_exited_children()
        cleanup.extend(ended)
        if not active and not group_exists(group):
            return cleanup, []
        if time.monotonic() >= deadline:
            return cleanup, related_processes(group)
        time.sleep(0.01)


def finish_linux_process(process, phase):
    ended, active = reap_exited_children()
    if not active and not group_exists(process.pid):
        return ended
    remaining = related_processes(process.pid)
    cleanup, missed = terminate_adopted(process.pid)
    emit({"stage": "process-failure", "phase": phase, "pid": process.pid, "exit": process.returncode,
          "hadUnfinishedChild": active, "remaining": remaining, "reapedBeforeFailure": ended,
          "reapedDuringCleanup": cleanup, "remainingAfterCleanup": missed})
    raise RuntimeError(f"{phase} left an unfinished child or unreaped process group")


def finish_posix_group(group, env):
    try:
        if not group_exists(group):
            return "gone"
    except PermissionError:
        if platform.system() != "Darwin" or env.get("LUXO_DART_MACOS_REAPED_ONLY") != "1":
            raise
        return "sandbox-denied; direct child reaped and stdio drained"
    kill_group(group)
    raise RuntimeError("launcher left a subprocess in its process group")


def run_checked(args, fixture, env, timeout=60, measure=False, phase="command"):
    linux = platform.system() == "Linux"
    if linux and not LINUX_SUBREAPER:
        raise RuntimeError("Linux requires scoped descendant reaping before launch")
    if measure:
        cpu_start, start = child_cpu(), time.perf_counter_ns()
    with subprocess.Popen(args, cwd=fixture, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                          text=True, encoding="utf-8", start_new_session=True) as process:
        try:
            out, err = process.communicate(timeout=timeout)
        except BaseException:
            kill_group(process.pid)
            if linux:
                # Reap the direct Popen child before touching adopted children.
                # A detached child holding stdout open must be stopped before draining the pipes.
                process.wait(timeout=2)
                ended, missed = terminate_adopted(process.pid)
                emit({"stage": "process-interrupted", "phase": phase, "pid": process.pid,
                      "reapedDuringCleanup": ended, "remainingAfterCleanup": missed})
            process.communicate(timeout=2 if linux else None)
            raise
        elapsed = time.perf_counter_ns() - start if measure else None
        if linux:
            reaped = finish_linux_process(process, phase)
            group_check = "orphan-zombies-reaped" if reaped else "gone"
        else:
            reaped, group_check = [], finish_posix_group(process.pid, env)
        measured = {"wallNs": elapsed, "childCpuNs": child_cpu() - cpu_start} if measure else {}
        return SimpleNamespace(returncode=process.returncode, stdout=out, stderr=err, pid=process.pid,
                               measured=measured, group_check=group_check, reaped=reaped)


def sdk_inputs(sdk):
    assert (platform.system(), platform.machine()) == ("Linux", "x86_64")
    assert (sdk / "version").read_text().strip() == "3.13.4"
    assert (sdk / "revision").read_text().strip() == REVISION
    version = subprocess.check_output([sdk / "bin/dart", "--version"], stderr=subprocess.STDOUT, text=True).strip()
    assert version.startswith("Dart SDK version: 3.13.4 (stable)") and version.endswith('on "linux_x64"'), version
    return {"version": version, "revision": REVISION, "inputs": {path: sha(sdk / path) for path in SDK_INPUTS}}


def dependencies(sdk, work, roots, env, *, online):
    profiles = {}
    if online:
        seed = work / "seed"
        seed.mkdir()
        shutil.copyfile(roots["A"] / "pubspec.yaml", seed / "pubspec.yaml")
        shutil.copyfile(HERE / "pubspec.lock", seed / "pubspec.lock")
        profiles["seed"] = locked_pub(sdk, seed, env, "seed", offline=False)
    for role, package in roots.items():
        assert not (package / ".dart_tool").exists(), role
        profiles[role] = locked_pub(sdk, package, env, role, offline=True)
    if online:
        compare_resolution("seed", "A", profiles)
    assert compare_resolution("A", "B", profiles), "the locked offline arm profiles differ"
    normalized = hashlib.sha256(json.dumps(profiles["A"][0], sort_keys=True).encode()).hexdigest()
    assert normalized == PREVIOUS_NORMALIZED_CONFIG, (normalized, PREVIOUS_NORMALIZED_CONFIG)
    return {role: {name: sha(package / name) for name in (
                    "pubspec.lock", ".dart_tool/package_config.json", ".dart_tool/package_graph.json")}
            for role, package in roots.items()}


def compile_subjects(sdk, work, roots, env, output):
    binaries = {}
    for role, package in roots.items():
        binary = work / f"{role}-probe"
        phase = f"compile:{role}"
        result = run_checked([sdk / "bin/dart", "compile", "exe", package / ENTRY, "-o", binary],
                             package, env, timeout=180, phase=phase)
        (output / f"{phase.replace(':', '-')}.log").write_text(result.stdout + result.stderr)
        emit({"stage": "compile", "role": role, "exit": result.returncode, "pid": result.pid,
              "group": result.group_check, "reaped": result.reaped})
        assert result.returncode == 0 and binary.exists(), phase
        binaries[role] = binary
    return binaries


def build_tools(repo, work, env):
    tool_dir = work / "bin"
    tool_dir.mkdir()
    go_env = {**env, "GOBIN": str(tool_dir), "GOCACHE": str(work / "go-cache"),
              "GOMODCACHE": str(work / "go-mod"), "GOTOOLCHAIN": "local", "GOWORK": "off", "CGO_ENABLED": "0"}
    version = json.loads(subprocess.check_output(["go", "env", "-json", "GOOS", "GOARCH", "GOVERSION"],
                                                 cwd=repo, env=go_env))
    assert version == {"GOOS": "linux", "GOARCH": "amd64", "GOVERSION": "go1.27.1"}, version
    subprocess.run(["go", "install", BENCHSTAT], cwd=repo, env=go_env, timeout=300, check=True)
    subprocess.run(["go", "build", "-o", tool_dir / "benchgate", "./scripts/benchgate"],
                   cwd=repo, env=go_env, timeout=300, check=True)
    tools = {name: tool_dir / name for name in ("benchstat", "benchgate")}
    for name, executable in tools.items():
        build_info = subprocess.check_output(["go", "version", "-m", executable], env=go_env, text=True)
        emit({"stage": "tool", "tool": name, "sha256": sha(executable), "buildInfo": build_info})
    return tools


def sample(sdk, roots, binaries, env, role, scenario, codec, operations, warmup, measure):
    package = roots[role]
    args = [scenario, codec, role, str(operations), str(warmup), measure]
    if measure == "alloc":
        command = [sdk / "bin/dart", "--enable-vm-service=0", package / ENTRY, *args, HERE / "observer.dart"]
    else:
        command = [binaries[role], *args]
    phase = ":".join((measure, role, codec, scenario))
    done = run_checked(command, package, env, timeout=90, phase=phase)
    if done.returncode != 0:
        emit({"stage": "child-failure", "phase": phase, "returncode": done.returncode,
              "stderr": done.stderr, "pid": done.pid, "reaped": done.reaped})
        raise RuntimeError(f"{phase} exited {done.returncode}")
    # Keep the validated worker record. VM stdout also contains an ephemeral localhost service URL.
    reported = [line for line in done.stdout.splitlines() if line.startswith('{"probe":"dart-http-refresh",')]
    assert len(reported) == 1, (role, scenario, codec, measure, done.stderr)
    row = json.loads(reported[0])
    for key, expected in dict(role=role, scenario=scenario, codec=codec, ops=operations, warmup=warmup, measure=measure).items():
        assert row[key] == expected, row
    if scenario == "triplet":
        successes = operations if role == "A" else 3 * operations
        assert row["successes"] == successes and row["sends"] == 3 * operations + successes, row
        assert row["failures"] == 3 * operations - successes and row["unauthorized"] == 3 * operations, row
        assert operations <= row["refreshes"] <= (3 * operations if role == "A" else operations), row
    elif scenario == "refresh":
        assert tuple(row[key] for key in ("successes", "sends", "unauthorized", "refreshes", "failures")) == (
            operations, 2 * operations, operations, operations, 0), row
    else:
        assert tuple(row[key] for key in ("successes", "sends", "unauthorized", "refreshes", "failures")) == (
            operations, operations, 0, 0, 0), row
    if measure == "alloc":
        assert row["managedBytes"] > 0 and row["managedObjects"] > 0 and "elapsedNs" not in row, row
    elif measure == "time":
        assert row["elapsedNs"] > 0 and "managedBytes" not in row, row
    else:
        assert "elapsedNs" not in row and "managedBytes" not in row, row
    return {**row, "process": {"pid": done.pid, "exit": done.returncode, "group": done.group_check,
                               "reaped": done.reaped}, "stderr": done.stderr}


def smoke(run):
    payload = []
    for role in ("A", "B"):
        for scenario in (*SCENARIOS, "triplet"):
            for codec in CODECS:
                kind = "diagnostic" if scenario == "triplet" else "time"
                payload.append(run(role, scenario, codec, 1, 0, kind))
                if kind == "time":
                    payload.append(run(role, scenario, codec, 1, 0, "alloc"))
    assert len(payload) == 28
    return payload


def schedule():
    for codec in CODECS:
        for scenario in SCENARIOS:
            ops = 2000 if scenario == "refresh" else 5000
            warmup = 300 if scenario == "refresh" else 1000
            for group in range(2):
                for pair in range(10):
                    offset = (group + pair) % len(COMPARISONS)
                    order = (1, 0) if (group + pair) % 2 else (0, 1)
                    for comparison in COMPARISONS[offset:] + COMPARISONS[:offset]:
                        yield codec, scenario, ops, warmup, group + 1, pair + 1, order, comparison


def pairs(run, output, measure):
    with (output / f"{measure}-pairs.jsonl").open("x") as sink:
        for codec, scenario, ops, warmup, group, pair, order, comparison in schedule():
            observed = {i: run(comparison[i], scenario, codec, ops, warmup, measure) for i in order}
            keys = ("elapsedNs",) if measure == "time" else ("managedBytes", "managedObjects")
            delta = {key: (observed[1][key] - observed[0][key]) / ops for key in keys}
            row = dict(group=group, pair=pair, codec=codec, scenario=scenario, comparison=comparison, order=order,
                       base=observed[0], head=observed[1], perOperationDelta=delta,
                       positivePerOp={key: max(0, value) for key, value in delta.items()})
            sink.write(json.dumps(row, sort_keys=True) + "\n")
            sink.flush()


def replay(output, workspace, tools, cpu):
    raw, deltas = {}, defaultdict(list)
    expected = {(scenario, codec, comparison, group, pair): (ops, warmup, order)
                for codec, scenario, ops, warmup, group, pair, order, comparison in schedule()}
    assert len(expected) == 360
    for measure in ("time", "alloc"):
        rows = {}
        for line in (output / f"{measure}-pairs.jsonl").read_text().splitlines():
            row = json.loads(line)
            key = tuple(row[field] for field in ("scenario", "codec", "comparison", "group", "pair"))
            assert key in expected and key not in rows, key
            ops, warmup, order = expected[key]
            assert row["order"] == list(order)
            for arm, role in zip(("base", "head"), row["comparison"]):
                assert (row[arm]["ops"], row[arm]["warmup"], row[arm]["role"]) == (ops, warmup, role)
            props = ("elapsedNs",) if measure == "time" else ("managedBytes", "managedObjects")
            delta = {prop: (row["head"][prop] - row["base"][prop]) / ops for prop in props}
            assert row["perOperationDelta"] == delta
            assert row["positivePerOp"] == {prop: max(0, value) for prop, value in delta.items()}
            for prop, value in delta.items():
                deltas[(*key[:-1], prop)].append(value)
            rows[key] = row
        assert rows.keys() == expected.keys()
        raw[measure] = rows
    summary = [{"scenario": s, "codec": c, "comparison": name, "group": group, "metric": PROPS[prop],
                "maxPositive": max(0, max(values)), "medianDelta": median(values), "pairs": len(values)}
               for (s, c, name, group, prop), values in sorted(deltas.items())]
    assert len(summary) == 108 and all(row["pairs"] == 10 for row in summary)
    (output / "paired-summary.json").write_text(json.dumps(summary, indent=2) + "\n")

    statuses = {}
    for comparison in COMPARISONS:
        for reverse in (False, True) if comparison != "AB" else (False,):
            label = f"{comparison}-reverse" if reverse else comparison
            reports = [benchstat(raw, comparison, reverse, group, workspace, output, tools["benchstat"], cpu)
                       for group in (1, 2)]
            done = subprocess.run([tools["benchgate"], *reports], capture_output=True, text=True, check=False)
            (output / f"benchgate-{label}.log").write_text(done.stdout + done.stderr)
            statuses[label] = done.returncode
    (output / "gate.json").write_text(json.dumps(statuses, indent=2) + "\n")
    return statuses


def benchstat(raw, comparison, reverse, group, workspace, output, tool, cpu):
    direction_order = ("head", "base") if reverse else ("base", "head")
    for name, direction in zip(("base", "head"), direction_order):
        with (workspace / f"{name}.txt").open("w") as sink:
            sink.write(f"goos: linux\ngoarch: amd64\npkg: dart-http-refresh\ncpu: {cpu}\n")
            for scenario, codec in sorted((s, c) for s in SCENARIOS for c in CODECS):
                for pair in range(1, 11):
                    key = (scenario, codec, comparison, group, pair)
                    seq = []
                    for prop, unit in PROPS.items():
                        row = raw["time" if prop == "elapsedNs" else "alloc"][key][direction]
                        seq.append(f"{row[prop] / row['ops']:.9f} {unit}")
                    sink.write(f"BenchmarkHttpTransport/{scenario}/{codec} 1 {' '.join(seq)}\n")
    label = f"{comparison}-reverse" if reverse else comparison
    report = output / f"benchstat-{label}-{group}.csv"
    with report.open("w") as sink:
        done = subprocess.run([tool, "-format", "csv", "base.txt", "head.txt"], cwd=workspace, stdout=sink,
                              stderr=subprocess.PIPE, text=True, check=True)
    if done.stderr:
        (output / f"benchstat-{label}-{group}.warnings.txt").write_text(done.stderr)
    return report


def collect(repo, work, roots, sources, output):
    verify_checkout(repo)
    sdk = Path(shutil.which("dart")).resolve(strict=True).parent.parent
    runtime = sdk_inputs(sdk)
    enable_linux_subreaper()
    tools_tmp = work / "tools-tmp"
    tools_tmp.mkdir()
    env = dict(os.environ, TMPDIR=str(tools_tmp), TMP=str(tools_tmp), TEMP=str(tools_tmp),
               DART_SUPPRESS_ANALYTICS="true", PUB_CACHE=str(work / "pub-cache"))
    cpu = next(line.partition(":")[2].strip() for line in Path("/proc/cpuinfo").read_text().splitlines()
               if line.startswith("model name"))
    emit({"stage": "prepare", "sha": os.environ["GITHUB_SHA"], "base": BASE, "trees": TREES, "packages": PACKAGES,
          "runtime": runtime, "cpu": cpu, "runnerImage": os.environ.get("ImageVersion"),
          "utc": datetime.now(timezone.utc).isoformat(), "linuxSubreaper": LINUX_SUBREAPER})
    stamps = dependencies(sdk, work, roots, env, online=True)
    binaries = compile_subjects(sdk, work, roots, env, output)
    tools = build_tools(repo, work, env)
    binding = {"driver": sha(Path(__file__)), "lockedFixture": FIXTURES, "payload": PATCH, "sources": sources,
               "dependencies": stamps, "binaries": {role: sha(path) for role, path in binaries.items()},
               "tools": {name: sha(path) for name, path in tools.items()},
               "units": {"time": "native AOT Stopwatch elapsed",
                         "alloc": "separate Dart VM-service managed bytes and accumulated objects"},
               "twoGroupPairsPerMetric": 360}
    (output / "binding.json").write_text(json.dumps(binding, indent=2, sort_keys=True) + "\n")

    def run(*args):
        return sample(sdk, roots, binaries, env, *args)

    (output / "smoke.json").write_text(json.dumps(smoke(run), indent=2) + "\n")
    for measure in ("time", "alloc"):
        pairs(run, output, measure)
        for role, package in roots.items():
            verify_package(package, sources[role])
            assert {name: sha(package / name) for name in stamps[role]} == stamps[role]
            assert sha(binaries[role]) == binding["binaries"][role]
        assert all(sha(HERE / name) == fingerprint for name, fingerprint in FIXTURES.items())
        assert sdk_inputs(sdk) == runtime
    assert {name: sha(path) for name, path in tools.items()} == binding["tools"]
    verify_checkout(repo)
    return replay(output, work, tools, cpu)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=("verify", "collect"))
    parser.add_argument("--repo", type=Path, required=True)
    parser.add_argument("--scratch", type=Path, required=True)
    parser.add_argument("--evidence", type=Path)
    args = parser.parse_args()
    repo, scratch = (path.resolve(strict=True) for path in (args.repo, args.scratch))
    assert ".tmp" in scratch.parts
    assert (args.phase == "collect") == (args.evidence is not None)
    output, statuses = None, {}
    if args.phase == "collect":
        assert os.environ.get("GITHUB_ACTIONS") == "true" and scratch == repo / ".tmp"
        output = args.evidence.resolve(strict=True)
        assert output.parent == scratch
        emit({"stage": "preflight", "sha": os.environ["GITHUB_SHA"], "base": BASE, "trees": TREES,
              "driver": sha(Path(__file__))})
    assert len(list(schedule())) == 360
    with tempfile.TemporaryDirectory(dir=scratch, prefix="dart-http-refresh-") as temp:
        work = Path(temp)
        roots, sources = stage_sources(repo, work)
        if output is not None:
            statuses = collect(repo, work, roots, sources, output)
    assert not work.exists()
    emit({"stage": "complete", "mode": args.phase, "scratchRemoved": True, "status": statuses,
          "base": BASE, "trees": TREES, "pairsPerMetric": 360,
          "gatePassed": not any(statuses.values()) if output is not None else None})
    if any(statuses.values()):
        raise SystemExit(1)


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, signal.getsignal(signal.SIGINT))
    main()
