"""Smoke or collect paired wall/child-CPU launch samples for exact Dart trees."""

import argparse
import ctypes
from base64 import b64decode
from collections import defaultdict
from statistics import median
from types import SimpleNamespace
import hashlib
import json
import os
import platform
import resource
import shutil
import signal
import subprocess
import tempfile
import time
from pathlib import Path
from urllib.parse import quote, unquote, urlsplit

HERE = Path(__file__).resolve().parent
BASE = "e101cf7699acb358305fd373934e8813a98087bd"
PREVIOUS_DIAGNOSTIC = "9867b149940595ab7b2aa48267c6908e46bb40fb"
PRODUCT_HEAD = "7dffd0b3d937c429b7e8d10b893a6701a5f21803"
TREES = {
    "A": "377fdb5bbe6815a45930c53e10797f08cf07c917",
    "B": "789e77c7459163d137cf04c5547a8778b06cc8be",
}
PATCH_SHA = "4bc513a2daad984b8b1444b570d4a3353aa9f19edbc666510df466c10420b760"
LOCK_SHA = "5f0a75b758b8e33d4d3352fc0416de253c55a04ebf040684601591af10de4cc7"
PUBSPEC_SHA = "294368c6b2cc61a62c83880cdca56d1137de356ef9a7682a6396d4fcce9dc5f9"
FILES_CHANGED = {
    ".github/workflows/dart-release-windows.yml", "packages/dart/bin/build.dart",
    "packages/dart/lib/src/builder/dart_program.dart", "packages/dart/test/dart_launcher_source_test.dart",
}
SDK_INPUTS = (
    "bin/dart", "bin/dartaotruntime", "bin/utils/gen_snapshot",
    "bin/snapshots/frontend_server_aot.dart.snapshot",
    "lib/_internal/vm_platform_strong.dill", "lib/_internal/vm_platform_product.dill",
)
REVISION = "b530c21f7de367b94fb04787bfed9d8e989d75e8"
ARGS = ["--argument", "two words", "literal % and 中文"]
MODES = ("jit", "aot")
COMP = ("AA", "AB", "BB")

APP = r"""
import 'dart:convert';
import 'dart:io';
import '../lib/selected.dart' if (dart.library.io) '../lib/native.dart';

Future<void> main(List<String> args) async {
  final payload = await File('payload.txt').readAsString();
  print(jsonEncode({
    'args': args,
    'branch': branch,
    'payload': payload,
    'cwd': Directory.current.path,
    'script': Platform.script.toString(),
    'product': const bool.fromEnvironment('dart.vm.product'),
  }));
}
"""

FILES = {
    "bin/app.dart": APP,
    "lib/native.dart": "const branch = 'native';\n",
    "lib/selected.dart": "const branch = 'fallback';\n",
    "payload.txt": "fixture payload\n",
    "pubspec.yaml": "name: launcher_fixture\nversion: 0.0.0\nenvironment:\n  sdk: '>=3.13.0 <4.0.0'\n",
    ".dart_tool/package_config.json": json.dumps({"configVersion": 2, "packages": [
        {"name": "launcher_fixture", "rootUri": "../", "packageUri": "lib/", "languageVersion": "3.13"},
    ]}),
    ".dart_tool/package_graph.json": json.dumps({"configVersion": 1, "roots": ["launcher_fixture"],
        "packages": [{"name": "launcher_fixture", "version": "0.0.0", "dependencies": [], "devDependencies": []}]}),
}


UNICODE_NO_MAIN = "no_main_中文.g.dart"
REJECTIONS = {
    "no-main": ("lib/no_main.g.dart", {"lib/no_main.g.dart": "int selected() => 1;\n"}),
    "unicode-no-main": (f"lib/{UNICODE_NO_MAIN}", {f"lib/{UNICODE_NO_MAIN}": "int selected() => 1;\n"}),
    "syntax-error": ("lib/syntax_error.g.dart", {
        "lib/syntax_error.g.dart": "void main() { final int value = ; } // 中文\n",
    }),
    "missing-import": ("lib/missing_import.g.dart", {
        "lib/missing_import.g.dart": "import 'missing.dart'; void main() {}\n",
    }),
    "marked": ("lib/ambiguous.g.dart", {
        "lib/ambiguous.g.dart": (
            "import 'choice.g.dart' if (dart.library.io) '%EF%BB%BFchoice.g.dart';\n"
            "void main() => print(branch);\n"
        ),
        "lib/choice.g.dart": "const branch = 'ordinary sibling';\n",
        "lib/\ufeffchoice.g.dart": "const branch = 'marked sibling';\n",
    }),
}
PROJECT_METADATA = {name: contents for name, contents in FILES.items()
                    if name == "pubspec.yaml" or name.startswith(".dart_tool/")}


def git(repo, *args, index=None):
    env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
    env.update(GIT_NO_LAZY_FETCH="1", GIT_OPTIONAL_LOCKS="0")
    if index is not None:
        env["GIT_INDEX_FILE"] = str(index)
    return subprocess.check_output(["git", "--no-replace-objects", "-c", "core.autocrlf=false", "-c", "core.eol=lf",
                                    "-C", str(repo), *map(str, args)], env=env)


def stage_objects(repo, work):
    lock = (HERE / "pubspec.lock").read_bytes()
    patch = b64decode(b"".join((HERE / "product.patch.b64").read_bytes().split()), validate=True)
    assert hashlib.sha256(lock).hexdigest() == LOCK_SHA and hashlib.sha256(patch).hexdigest() == PATCH_SHA
    assert git(repo, "rev-parse", f"{BASE}^{{tree}}").decode().strip() == TREES["A"]
    if os.environ.get("GITHUB_ACTIONS") == "true":
        assert git(repo, "rev-parse", "HEAD").decode().strip() == os.environ["GITHUB_SHA"]
        assert git(repo, "rev-parse", "HEAD^").decode().strip() == PREVIOUS_DIAGNOSTIC
        assert git(repo, "rev-parse", "HEAD^^^").decode().strip() == BASE
        relative = HERE.relative_to(repo)
        delta = git(repo, "diff", "--name-status", PREVIOUS_DIAGNOSTIC, "HEAD", "--").decode().splitlines()
        expected_update = {f"M\t{relative / 'cost.py'}", "M\t.github/workflows/dart-launcher-e101-cost.yml"}
        assert set(delta) == expected_update, delta
        expected = {str(relative / name) for name in ("cost.py", "product.patch.b64", "pubspec.lock")}
        expected.add(".github/workflows/dart-launcher-e101-cost.yml")
        changed = git(repo, "diff", "--name-status", BASE, "HEAD", "--").decode().splitlines()
        assert set(changed) == {"A\t" + name for name in expected}
        for name in expected:
            assert (repo / name).read_bytes() == git(repo, "show", f"HEAD:{name}")
    common = Path(git(repo, "rev-parse", "--path-format=absolute", "--git-common-dir").decode().strip())
    private = work / "objects.git"
    private.mkdir()
    git(private, "init", "--quiet", "--bare")
    (private / "objects/info/alternates").write_text(str(common / "objects") + "\n")
    index = work / "candidate.index"
    patch_path = work / "product.patch"
    patch_path.write_bytes(patch)
    git(private, "read-tree", BASE, index=index)
    git(private, "apply", "--cached", "--check", "--whitespace=error-all", patch_path, index=index)
    git(private, "apply", "--cached", "--whitespace=error-all", patch_path, index=index)
    assert git(private, "write-tree", index=index).decode().strip() == TREES["B"]
    assert set(git(private, "diff", "--name-only", TREES["A"], TREES["B"]).decode().splitlines()) == FILES_CHANGED
    return private, lock


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
    assert len(profile) == len({row[0] for row in profile}) == 54
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


def prepare_sources(repo, work, sdk, env, online, verify_only):
    objects, lock = stage_objects(repo, work)
    roots, stamps = {}, {}
    for arm, tree in TREES.items():
        scope = work / arm
        scope.mkdir()
        archive = git(objects, "archive", tree, "packages/dart")
        subprocess.run(["tar", "-xf", "-", "-C", scope], input=archive, check=True)
        package = scope / "packages/dart"
        assert sha(package / "pubspec.yaml") == PUBSPEC_SHA
        (package / "pubspec.lock").write_bytes(lock)
        roots[arm] = package
    if verify_only:
        return roots, stamps
    profiles = {}
    if online:
        seed = work / "seed"
        seed.mkdir()
        (seed / "pubspec.yaml").write_bytes((roots["A"] / "pubspec.yaml").read_bytes())
        (seed / "pubspec.lock").write_bytes(lock)
        profiles["seed"] = locked_pub(sdk, seed, env, "seed", offline=False)
    for arm, package in roots.items():
        assert not (package / ".dart_tool").exists(), arm
        profiles[arm] = locked_pub(sdk, package, env, arm, offline=True)
        stamps[arm] = {name: sha(package / name) for name in
                       ("pubspec.lock", ".dart_tool/package_config.json", ".dart_tool/package_graph.json")}
    if online:
        compare_resolution("seed", "A", profiles)
    assert compare_resolution("A", "B", profiles), "the exact locked resolution differs between offline arms"
    return roots, stamps


def prepare_fixture(fixture, files):
    for name, contents in files.items():
        path = fixture / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(contents, encoding="utf-8")
    return fixture


def child_cpu():
    usage = resource.getrusage(resource.RUSAGE_CHILDREN)
    return round((usage.ru_utime + usage.ru_stime) * 1_000_000_000)


def command(sdk, package, fixture, mode, entry, binary):
    launcher = [
        sdk / "bin/dart",
        f"--packages={package / '.dart_tool/package_config.json'}",
        package / "bin/build.dart",
        "--project", fixture, "--entry", quote(entry, safe="/"),
    ]
    if mode == "aot":
        return launcher + ["--compile", "exe", "--output", binary.relative_to(fixture)]
    return launcher + ["--", *ARGS]


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


def script_path(value):
    uri = urlsplit(value)
    assert uri.scheme == "file" and uri.netloc == "", value
    return Path(unquote(uri.path))


def verify_application(result, fixture, binary, mode):
    assert result.returncode == 0, (result.returncode, result.stdout, result.stderr)
    payload = json.loads(result.stdout)
    path, cwd = script_path(payload.pop("script")), Path(payload.pop("cwd"))
    if mode == "jit":
        assert cwd.parent == fixture / ".tmp" and cwd.name.startswith("luxo-selection-"), cwd
        assert path == cwd / "bin/app.dart", path
        layout = {"cwd": "selection snapshot", "script": "bin/app.dart"}
    else:
        assert path == binary and cwd == fixture, (path, cwd)
        layout = {"cwd": "original fixture", "script": "dist/verified.exe"}
    expected = {"args": ARGS, "branch": "native", "payload": FILES["payload.txt"], "product": mode == "aot"}
    assert payload == expected, payload
    return {**payload, **layout}


def sample(sdk, package, fixture, env, mode, case="ordinary", measure=False):
    entry = "bin/app.dart" if case == "ordinary" else REJECTIONS[case][0]
    binary = fixture / "dist/verified.exe"
    assert not binary.exists()
    result = run_checked(command(sdk, package, fixture, mode, entry, binary), fixture, env, measure=measure,
                         phase=f"launcher:{mode}:{case}")
    assert not list((fixture / ".tmp").glob("luxo-selection-*")), "left unmanaged selection snapshot"
    try:
        if case != "ordinary":
            record = {"exit": result.returncode, "output": result.stdout.strip(), "error": result.stderr.strip(),
                      "artifact": binary.is_file(), "launcherPid": result.pid, "launcherGroup": result.group_check,
                      "launcherReaped": result.reaped}
            if binary.is_file():
                output = run_checked([binary, *ARGS], fixture, env, phase=f"executable:{mode}:{case}")
                record.update(artifactExit=output.returncode, artifactOutput=output.stdout.strip(),
                              executionPid=output.pid, executionGroup=output.group_check, executionReaped=output.reaped)
            return record
        artifact = None
        observed = {**result.measured, "launcherPid": result.pid, "launcherGroup": result.group_check,
                    "launcherReaped": result.reaped}
        if mode == "aot":
            assert result.returncode == 0, (result.returncode, result.stdout, result.stderr)
            assert binary.is_file() and os.access(binary, os.X_OK), binary
            artifact = {"size": binary.stat().st_size}
            if not measure:
                artifact["sha256"] = sha(binary)
            result = run_checked([binary, *ARGS], fixture, env, measure=measure, phase=f"executable:{mode}:{case}")
            observed.update(executionPid=result.pid, executionGroup=result.group_check,
                            executionReaped=result.reaped, aotExecution=result.measured)
        contract = verify_application(result, fixture, binary, mode)
        return {**observed, "contract": contract, "artifact": artifact}
    finally:
        binary.unlink(missing_ok=True)


def smoke(sdk, arms, fixture, env, quick=False):
    cases = (("ordinary", "no-main", "marked") if quick else
             ("ordinary", "no-main", "unicode-no-main", "syntax-error", "missing-import", "marked"))
    for case in cases:
        project = fixture
        if case != "ordinary":
            _, sources = REJECTIONS[case]
            project = prepare_fixture(fixture.parent / f"rejection-{case}", {**PROJECT_METADATA, **sources})
            if case == "marked":
                assert not (project / "lib/choice.g.dart").samefile(project / "lib/\ufeffchoice.g.dart")
        for mode in MODES:
            left, right = (sample(sdk, arms[arm], project, env, mode, case) for arm in ("A", "B"))
            if case == "ordinary":
                assert left["contract"] == right["contract"], (left, right)
            elif case == "marked":
                actual = left["artifactOutput"] if mode == "aot" else left["output"]
                assert left["exit"] == 0 and actual == "ordinary sibling", left
                assert mode != "aot" or left["artifactExit"] == 0, left
                assert right["exit"] != 0 and not right["artifact"], right
                assert "%EF%BB%BFchoice.g.dart" in right["error"] and not right["output"], right
            else:
                verify_compiler_exit(case, mode, left, right)
            emit({"stage": "smoke", "mode": mode, "case": case, "A": left, "B": right})


def verify_compiler_exit(case, mode, left, right):
    missing_main = case in ("no-main", "unicode-no-main")
    expected_exit = 253 if mode == "jit" and missing_main else 254
    for arm, record in (("A", left), ("B", right)):
        assert record["exit"] == expected_exit and not record["artifact"], record
        diagnostics = record["output"] + record["error"]
        if missing_main and mode == "jit" and arm == "A":
            assert "must have a 'main' function defined" in diagnostics, record
        else:
            assert "Error:" in diagnostics, record
    if case == "unicode-no-main":
        name = quote(UNICODE_NO_MAIN, safe="")
        assert f"{name}: Error: No 'main' method found." in right["error"], right
    if case == "syntax-error":
        assert "// 中文" in right["error"], right


def collect(sdk, arms, fixture, env):
    peaks = defaultdict(list)
    for mode in MODES:
        for arm in ("A", "B"):
            sample(sdk, arms[arm], fixture, env, mode)
        for group in range(1, 3):
            for pair in range(1, 11):
                rotation = (group + pair - 2) % len(COMP)
                order = (0, 1) if (group + pair) % 2 == 0 else (1, 0)
                for comparison in COMP[rotation:] + COMP[:rotation]:
                    observed = {side: sample(sdk, arms[comparison[side]], fixture, env, mode, measure=True)
                                for side in order}
                    left, right = observed[0], observed[1]
                    assert left["contract"] == right["contract"], (left, right)
                    phases = {"jit-launch" if mode == "jit" else "aot-compile-publish": (left, right)}
                    if mode == "aot":
                        phases["aot-executable-launch"] = (left["aotExecution"], right["aotExecution"])
                    deltas = {}
                    for phase, (lmeasured, rmeasured) in phases.items():
                        assert min(lmeasured["wallNs"], rmeasured["wallNs"]) > 0
                        assert min(lmeasured["childCpuNs"], rmeasured["childCpuNs"]) >= 0
                        values = {key: rmeasured[key] - lmeasured[key] for key in ("wallNs", "childCpuNs")}
                        deltas[phase] = values
                        for key, value in values.items():
                            peaks[phase, group, comparison, key].append(value)
                    emit({"stage": "sample", "mode": mode, "group": group, "pair": pair,
                          "comparison": comparison, "executionOrder": order, "left": left, "right": right,
                          "delta": deltas})
    assert len(peaks) == 36 and {len(samples) for samples in peaks.values()} == {10}
    for (phase, group, comparison, metric), samples in sorted(peaks.items()):
        emit({"stage": "summary", "phase": phase, "group": group, "comparison": comparison, "metric": metric,
              "samples": len(samples), "min": min(samples), "median": median(samples), "max": max(samples),
              "maxPositive": max(0, max(samples))})


def interrupted(_signum, _frame):
    raise KeyboardInterrupt


def host_cpu():
    if platform.system() == "Linux":
        for row in Path("/proc/cpuinfo").read_text().splitlines():
            name, _, value = row.partition(":")
            if name.strip() == "model name":
                return value.strip()
        raise RuntimeError("cannot identify the native Linux CPU")
    hardware = json.loads(subprocess.check_output(["system_profiler", "-detailLevel", "mini", "SPHardwareDataType", "-json"],
                                                  stderr=subprocess.DEVNULL))
    return hardware["SPHardwareDataType"][0]["chip_type"]


def verify_sdk(sdk):
    target = (platform.system(), platform.machine())
    abi = {("Darwin", "arm64"): "macos_arm64", ("Linux", "x86_64"): "linux_x64"}[target]
    assert (sdk / "version").read_text().strip() == "3.13.4"
    assert (sdk / "revision").read_text().strip() == REVISION
    reported = subprocess.check_output([sdk / "bin/dart", "--version"], stderr=subprocess.STDOUT).decode().strip()
    assert reported.startswith("Dart SDK version: 3.13.4 (stable)") and reported.endswith(f'on "{abi}"'), reported
    return reported


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=("verify-only", "smoke", "collect"))
    parser.add_argument("--repo", type=Path, required=True)
    parser.add_argument("--scratch", type=Path, required=True)
    parser.add_argument("--sdk", type=Path)
    parser.add_argument("--online", action="store_true", help="populate a task-owned locked cache on a hosted runner")
    parser.add_argument("--quick", action="store_true", help="smoke only the six essential JIT/AOT pairs")
    parser.add_argument("--macos-reaped-only", action="store_true",
                        help="record unavailable process group inspection during local macOS smoke")
    args = parser.parse_args()
    if args.macos_reaped_only and (args.operation != "smoke" or platform.system() != "Darwin"
                                  or os.environ.get("GITHUB_ACTIONS") == "true"):
        parser.error("--macos-reaped-only applies to local macOS smoke only")
    if args.quick and args.operation != "smoke":
        parser.error("--quick applies only to smoke")
    if args.online and os.environ.get("GITHUB_ACTIONS") != "true":
        parser.error("online dependency preparation requires the hosted diagnostic")
    repo, scratch = args.repo.resolve(strict=True), args.scratch.resolve(strict=True)
    assert ".tmp" in scratch.parts and not scratch.is_symlink()
    verify_only = args.operation == "verify-only"
    if args.operation == "collect" and os.environ.get("LUXO_DART_COST_EXCLUSIVE") != "1":
        parser.error("collect requires the coordinated exclusive native host")
    sdk = None
    if not verify_only:
        dart = shutil.which("dart")
        if not (args.sdk or dart):
            parser.error("use --sdk or place the pinned native Dart SDK on PATH")
        sdk = (args.sdk if args.sdk else Path(dart).resolve().parent.parent).resolve(strict=True)
        version = verify_sdk(sdk)
        cpu = host_cpu()
        if platform.system() == "Linux":
            enable_linux_subreaper()
    signal.signal(signal.SIGTERM, interrupted)
    with tempfile.TemporaryDirectory(dir=scratch, prefix="dart-launch-cost-") as tmp:
        work = Path(tmp)
        tools_tmp = work / "sdk-tmp"
        tools_tmp.mkdir()
        pub_cache = work / "pub-cache" if args.online else Path(os.environ.get("PUB_CACHE", Path.home() / ".pub-cache"))
        env = dict(os.environ, TMPDIR=str(tools_tmp), TMP=str(tools_tmp), TEMP=str(tools_tmp),
                   PUB_CACHE=str(pub_cache.resolve()), DART_SUPPRESS_ANALYTICS="true",
                   LUXO_DART_MACOS_REAPED_ONLY="1" if args.macos_reaped_only else "0")
        if not verify_only:
            emit({"stage": "setup", "phase": "prepare", "state": "started", "trees": TREES,
                  "lockSha256": LOCK_SHA, "version": version, "revision": REVISION,
                  "productHead": PRODUCT_HEAD, "linuxSubreaper": LINUX_SUBREAPER})
        roots, dependencies = prepare_sources(repo, work, sdk, env, args.online, verify_only)
        if not verify_only:
            fixture = prepare_fixture(work / "application", FILES)
            emit({"stage": "start", "operation": args.operation, "base": BASE, "trees": TREES,
                  "patchSha256": PATCH_SHA, "lockSha256": LOCK_SHA, "revision": REVISION,
                  "productHead": PRODUCT_HEAD,
                  "version": version, "cpu": cpu, "os": platform.platform(), "loadAverage": os.getloadavg(),
                  "runnerImage": os.environ.get("ImageOS"), "runnerImageVersion": os.environ.get("ImageVersion"),
                  "quickSmoke": args.quick, "macosReapedOnly": args.macos_reaped_only,
                  "resolvedDependencies": dependencies, "sdk": {name: sha(sdk / name) for name in SDK_INPUTS},
                  "fixtures": {path: sha(fixture / path) for path in FILES},
                  "rejectionFixtures": {case: {name: hashlib.sha256(source.encode()).hexdigest()
                                               for name, source in sources.items()}
                                        for case, (_, sources) in REJECTIONS.items()},
                  "driver": sha(Path(__file__))})
            smoke(sdk, roots, fixture, env, quick=args.quick)
            if args.operation == "collect":
                collect(sdk, roots, fixture, env)
            assert all(sha(root / "pubspec.lock") == LOCK_SHA for root in roots.values())
    assert not work.exists(), work
    emit({"stage": "complete", "operation": args.operation, "trees": TREES, "lockSha256": LOCK_SHA,
          "productHead": PRODUCT_HEAD, "scratchRemoved": not work.exists(), "loadAverage": os.getloadavg()})


if __name__ == "__main__":
    main()
