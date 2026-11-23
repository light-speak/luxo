#!/usr/bin/env python3
"""Run explicit normal-input candidate gates, without an explicit dependency installation step."""
import argparse
import hashlib
import json
import os
import platform
import re
import signal
import subprocess
import time
from pathlib import Path

BASE = Path(__file__).resolve().parent
SOURCE = BASE / "source"
PACKAGE = SOURCE / "packages/dart"
FILES = [
    "lib/src/builder/artifact_publish.dart",
    "lib/src/builder/selection_build.dart",
    "bin/build.dart",
    "test/artifact_publish_test.dart",
    "test/selection_build_test.dart",
]
ARTIFACT_ROOTS = [
    "prepared publication preserves the missing artifact error",
    "prepared publication creates ordinary parents and keeps file bytes",
    "prepared publication preserves ordinary in-project directory aliases",
    "two prepared publishers never replace the first completed artifact",
    "closed prepared output rejects publication without changing the artifact",
    "prepared output retains normal Unicode and long directory names",
    "prepared output accepts a root-level file and normalized relative names",
    "prepared output preserves a project selected through its directory alias",
    "prepared output keeps an existing ordinary file unchanged",
]
BUILDER_ROOTS = [
    "release output rejects overwrites and escapes before creating directories",
    "build ownership accepts a trailing directory separator",
    "release publication preserves an output created after validation",
    "build publication accepts an ordinary in-project temporary alias",
    "build close releases an unconsumed prepared output",
    "closing build rejects an in-flight output preparation",
    "release publication reports a missing artifact error",
    "release compilation failure publishes no artifact and cleans the snapshot",
    "AOT artifact retains call-site selections after snapshot cleanup",
]


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def sources():
    paths = [p for p in (PACKAGE / "lib").rglob("*.dart")]
    paths += [p for p in (PACKAGE / "bin").rglob("*.dart")]
    paths += [p for p in (PACKAGE / "test").rglob("*") if p.is_file()]
    paths += [PACKAGE / "pubspec.yaml", PACKAGE / "pubspec.lock"]
    paths += [SOURCE / ".github/workflows/dart-release-windows.yml"]
    return {p.relative_to(SOURCE).as_posix(): digest(p) for p in sorted(paths)}


def run(command):
    options = {"start_new_session": True} if os.name != "nt" else {"creationflags": subprocess.CREATE_NEW_PROCESS_GROUP}
    process = subprocess.Popen(command, cwd=PACKAGE, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, **options)
    forced = False
    try:
        output, _ = process.communicate(timeout=900)
    except (subprocess.TimeoutExpired, KeyboardInterrupt):
        forced = True
        if os.name == "nt":
            subprocess.run(["taskkill", "/PID", str(process.pid), "/T", "/F"], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False)
        else:
            try:
                os.killpg(process.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
        try:
            output, _ = process.communicate(timeout=15)
        except subprocess.TimeoutExpired:
            if os.name != "nt":
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
            process.kill()
            output, _ = process.communicate(timeout=15)
    return process.returncode, output, process.pid, forced


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dart", required=True, help="Already installed Dart SDK executable")
    parser.add_argument("--mode", required=True, choices=("light", "native", "full"))
    parser.add_argument("--label", required=True)
    parser.add_argument("--keep-going", action="store_true", help="Collect later diagnostics after a failed step; the overall gate still fails")
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9_.-]+", args.label):
        parser.error("label must be a single simple filename")
    dart = str(Path(args.dart).resolve(strict=True))
    if not (PACKAGE / ".dart_tool/package_config.json").is_file() or not (PACKAGE / "pubspec.lock").is_file():
        parser.error("existing package configuration and runtime lock are required; this script does not install dependencies")
    evidence = BASE / "evidence" / args.label
    evidence.mkdir(exist_ok=False)
    before = sources()
    (evidence / "sources.before.json").write_text(json.dumps(before, indent=2) + "\n")
    expected = ARTIFACT_ROOTS if args.mode == "light" else ARTIFACT_ROOTS + BUILDER_ROOTS
    selection = "^(" + "|".join(re.escape(name) for name in expected) + ")$"
    steps = [
        ("version", [dart, "--version"]),
        ("format", [dart, "format", "--output=none", "--set-exit-if-changed", *FILES]),
        ("analyze", [dart, "analyze", *FILES]),
    ]
    if args.mode == "full":
        steps.append(("tests", [dart, "test", "--concurrency=1", "--reporter=json"]))
    else:
        tests = ["test/artifact_publish_test.dart"]
        if args.mode == "native":
            tests.append("test/selection_build_test.dart")
        steps.append(("tests", [dart, "test", "--concurrency=1", "--reporter=json", *tests, "--name", selection]))
    results = []
    for name, command in steps:
        start = time.time()
        status, output, pid, forced = run(command)
        (evidence / (name + ".log")).write_text(output)
        results.append({"name": name, "command": command, "cwd": str(PACKAGE), "pid": pid, "exit": status, "reaped": True, "forcedTermination": forced, "elapsedSeconds": time.time() - start})
        print(name, status, flush=True)
        if (status or forced) and not args.keep_going:
            break
    after = sources()
    (evidence / "sources.after.json").write_text(json.dumps(after, indent=2) + "\n")
    passed, failed, skipped = [], [], []
    starts = {}
    log = evidence / "tests.log"
    if log.exists():
        for line in log.read_text().splitlines():
            try:
                event = json.loads(line)
            except ValueError:
                continue
            if event.get("type") == "testStart":
                starts[event["test"]["id"]] = event["test"]["name"]
            if event.get("type") == "testDone" and not event.get("hidden", False):
                name = starts.get(event["testID"], str(event["testID"]))
                (skipped if event.get("skipped") else passed if event.get("result") == "success" else failed).append(name)
    missing = [] if args.mode == "full" else sorted(set(expected) - set(passed))
    receipt = {
        "mode": args.mode, "platform": platform.platform(), "machine": platform.machine(),
        "dart": dart, "dartSha256": digest(Path(dart)), "steps": results,
        "sourceDrift": before != after, "passed": passed, "failed": failed,
        "skipped": skipped, "missingExpectedRoots": missing,
        "explicitDependencyResolution": False, "formalPerformance": False,
    }
    (evidence / "receipt.json").write_text(json.dumps(receipt, indent=2) + "\n")
    raise SystemExit(0 if len(results) == len(steps) and all(r["exit"] == 0 and not r["forcedTermination"] for r in results) and before == after and not failed and not skipped and not missing else 1)


if __name__ == "__main__":
    main()
