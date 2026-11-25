"""Verify the exact Dart launcher source and native test receipts."""

import argparse
from base64 import b64decode
from hashlib import sha256
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
from zipfile import ZipFile


BASE = "e101cf7699acb358305fd373934e8813a98087bd"
BASE_TREE = "377fdb5bbe6815a45930c53e10797f08cf07c917"
TREE = "8226407033d261b8031c715e86f6fecc327a4db9"
PATCH_SHA = "d952cffb1eeb642d6f5d51aa9d47a626a2aeaa927275edcae39be839047614ad"
LOCK_SHA = "5f0a75b758b8e33d4d3352fc0416de253c55a04ebf040684601591af10de4cc7"
PUBSPEC_SHA = "294368c6b2cc61a62c83880cdca56d1137de356ef9a7682a6396d4fcce9dc5f9"
DART_REVISION = "b530c21f7de367b94fb04787bfed9d8e989d75e8"
FIXTURE = ".github/diagnostics/dart-launcher-e101"
WORKFLOW = ".github/workflows/dart-launcher-e101-native.yml"
PRODUCT_FILES = {
    ".github/workflows/dart-release-windows.yml",
    "packages/dart/bin/build.dart",
    "packages/dart/lib/src/builder/dart_program.dart",
    "packages/dart/test/dart_launcher_source_test.dart",
}
LAUNCHER = tuple(
    f"launcher {mode} {case}"
    for mode in ("JIT", "AOT")
    for case in (
        "preserves compiler exit for no_main.g.dart",
        "preserves compiler exit for syntax_error.g.dart",
        "preserves compiler exit for missing_import.g.dart",
        "preserves a literal escaped entry and cwd",
        "guards a selected marked entry",
        "respects active conditional selection",
        "respects inactive conditional selection",
        "respects overridden conditional selection",
        "respects mirrors conditional selection",
    )
)
DIRECTORY = (
    "native refresh preserves distinct source names beginning with U+FEFF",
    "snapshot source filter preserves escaped native name identity",
    "snapshot workspace uses the exact project folder",
)
SUITES = (
    ("launcher", "test/dart_launcher_source_test.dart", LAUNCHER, 25 * 60),
    ("directory", "test/selection_build_test.dart", DIRECTORY, 4 * 60),
)


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def digest(data):
    return sha256(data).hexdigest()


def git(repo, *args, index=None):
    env = {name: value for name, value in os.environ.items() if not name.startswith("GIT_")}
    env.update(GIT_NO_LAZY_FETCH="1", GIT_OPTIONAL_LOCKS="0")
    if index is not None:
        env["GIT_INDEX_FILE"] = str(index)
    return subprocess.check_output(["git", "--no-replace-objects", "-C", str(repo), *args], env=env)


def diagnostic_assets(repo, offline):
    patch_name = "product.patch.b64"
    if offline:
        source = Path(__file__).resolve().parent
        patch, lock = (source / patch_name).read_bytes(), (source / "pubspec.lock").read_bytes()
    else:
        head = git(repo, "rev-parse", "HEAD").decode().strip()
        require(head == os.environ["GITHUB_SHA"] and git(repo, "rev-parse", "HEAD^").decode().strip() == BASE,
                "the diagnostic commit must be the only commit above the pinned base")
        paths = git(repo, "diff", "--no-renames", "--name-status", BASE, head, "--").decode().splitlines()
        expected = {f"A\t{WORKFLOW}"} | {f"A\t{FIXTURE}/{name}" for name in ("qualify.py", patch_name, "pubspec.lock")}
        require(set(paths) == expected, "the diagnostic commit must contain only its four new files")
        patch, lock = git(repo, "show", f"{head}:{FIXTURE}/{patch_name}"), git(repo, "show", f"{head}:{FIXTURE}/pubspec.lock")
    return b64decode(b"".join(patch.split()), validate=True), lock


def stage(repo, scratch, patch, lock):
    require(git(repo, "rev-parse", BASE + "^{tree}").decode().strip() == BASE_TREE, "pinned base tree mismatch")
    require(digest(patch) == PATCH_SHA and digest(lock) == LOCK_SHA, "diagnostic patch or lock digest mismatch")
    data = scratch / "product.patch"
    data.write_bytes(patch)
    index = scratch / "candidate.index"
    git(repo, "read-tree", BASE, index=index)
    git(repo, "apply", "--cached", "--check", "--binary", str(data), index=index)
    git(repo, "apply", "--cached", "--binary", str(data), index=index)
    actual = git(repo, "write-tree", index=index).decode().strip()
    require(actual == TREE, f"patched source tree differs: {actual}")
    changed = git(repo, "diff", "--name-only", BASE_TREE, actual, "--").decode().splitlines()
    require(set(changed) == PRODUCT_FILES, "unexpected product file in the verified patch")
    archive = scratch / "source.zip"
    git(repo, "archive", "--format=zip", f"--output={archive}", actual, "VERSION", "packages/dart")
    source = scratch / "source"
    with ZipFile(archive) as bundle:
        bundle.extractall(source)
    package = source / "packages/dart"
    require(digest((package / "pubspec.yaml").read_bytes()) == PUBSPEC_SHA, "patched pubspec input differs")
    (package / "pubspec.lock").write_bytes(lock)
    return package


def verify_report(path, expected):
    starts = {}
    passed = []
    done = []
    for line in path.read_text(encoding="utf-8").splitlines():
        row = json.loads(line)
        if row["type"] == "testStart":
            starts[row["test"]["id"]] = row["test"]["name"]
        if row["type"] == "testDone" and not row["hidden"]:
            name = starts[row["testID"]]
            require(row["result"] == "success" and not row["skipped"], f"test failed or skipped: {name}")
            passed.append(name)
        if row["type"] == "done":
            done.append(row["success"])
    require(done == [True] and sorted(passed) == sorted(expected),
            f"incomplete test run: expected {sorted(expected)}, observed {sorted(passed)}")
    return sorted(passed)


def record_command(command, pkg, env, output, tag, timeout):
    stdout, stderr = output / f"{tag}.stdout", output / f"{tag}.stderr"
    with stdout.open("wb") as out, stderr.open("wb") as err:
        subprocess.run(command, cwd=pkg, env=env, stdout=out, stderr=err, check=True, timeout=timeout)
    return stdout


def verify_sdk():
    sdk = Path(shutil.which("dart") or "").resolve()
    require(sdk.is_file(), "dart SDK executable not found")
    version = subprocess.check_output([str(sdk), "--version"], stderr=subprocess.STDOUT).decode().strip()
    expected = os.environ["EXPECTED_DART_ABI"]
    require(version.startswith("Dart SDK version: 3.13.4 ") and version.endswith(f'on "{expected}"'),
            f"unexpected Dart release or ABI: {version}")
    revision = (sdk.parent.parent / "revision").read_text().strip()
    require(revision == DART_REVISION, f"unexpected Dart SDK revision: {revision}")
    return str(sdk), version


def native_test(package, scratch, output, receipt):
    dart, version = verify_sdk()
    receipt.update(sdk=version, sdkRevision=DART_REVISION, runnerImage=os.environ.get("ImageOS"),
                   runnerImageVersion=os.environ.get("ImageVersion"), suites={})
    temp = scratch / "temp"
    temp.mkdir()
    env = dict(os.environ, PUB_CACHE=str(scratch / "pub-cache"), TMP=str(temp), TEMP=str(temp), TMPDIR=str(temp))
    record_command([dart, "pub", "get", "--enforce-lockfile"], package, env, output, "locked-pub-get", 5 * 60)
    lock = package / "pubspec.lock"
    require(digest(lock.read_bytes()) == LOCK_SHA, "Dart dependency resolution altered the lock")
    failures = []
    for tag, file, names, timeout in SUITES:
        command = [dart, "test", "--concurrency=1", "--reporter=json", file]
        if tag == "directory":
            command += ["--name", "^(" + "|".join(re.escape(name).replace(r"\ ", " ") for name in names) + ")$"]
        try:
            report = record_command(command, package, env, output, tag, timeout)
            receipt["suites"][tag] = verify_report(report, names)
        except (RuntimeError, OSError, ValueError, subprocess.SubprocessError) as error:
            failures.append(f"{tag}: {error}")
    product_temp = package.parents[1] / ".tmp"
    remaining = sorted(file.name for file in product_temp.iterdir()) if product_temp.exists() else []
    receipt["retainedFixtureDirectories"] = remaining
    receipt["resolvedLockSHA256"] = digest(lock.read_bytes())
    require(not remaining, f"test fixtures were left behind: {remaining}")
    require(receipt["resolvedLockSHA256"] == LOCK_SHA, "Dart tests altered the lock")
    require(not failures, "\n".join(failures))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verify-only", action="store_true", help="stage the locked tree without invoking Dart")
    parser.add_argument("--repo", type=Path, default=os.environ.get("GITHUB_WORKSPACE"))
    parser.add_argument("--scratch", type=Path, default=os.environ.get("RUNNER_TEMP"))
    args = parser.parse_args()
    require(args.repo and args.scratch, "explicit repository and scratch paths required")
    root = args.scratch.resolve(strict=True)
    receipt = dict(base=BASE, baseTree=BASE_TREE, productTree=TREE, patchSHA256=PATCH_SHA, lockSHA256=LOCK_SHA,
                   expectedTests={name: len(cases) for name, _, cases, _ in SUITES})
    if not args.verify_only:
        output = root / "dart-launcher-evidence"
        output.mkdir()
    try:
        patch, lock = diagnostic_assets(args.repo, args.verify_only)
        with tempfile.TemporaryDirectory(dir=root, prefix="dl-") as directory:
            scratch = Path(directory)
            package = stage(args.repo, scratch, patch, lock)
            if not args.verify_only:
                native_test(package, scratch, output, receipt)
        receipt["scratchRemoved"] = not scratch.exists()
        receipt["status"] = "static-only" if args.verify_only else "passed"
        print(json.dumps(receipt, indent=2))
    except BaseException as error:
        receipt.update(status="failed", failure=str(error))
        raise
    finally:
        if not args.verify_only:
            (output / "receipt.json").write_text(json.dumps(receipt, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
