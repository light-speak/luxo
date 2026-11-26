"""Verify exact Dart snapshot, release and launcher behavior natively."""

import argparse
from base64 import b64decode
from hashlib import sha256
import json
import os
import platform
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import sys
from zipfile import ZipFile


BASE = "5fc47e2a77a5a964e945dc6867ddb7061ce9e9fd"
BRANCH = "refs/heads/diagnostics/dart-snapshot-5fc"
BASE_TREE = "41ee4dd6f070fac672dbfb0450ad203fd60ed3fd"
TREE = "5a7e89b23cc02a0d22af0a532e9efec0ac32bfeb"
DART_TREE = "5abb98216620f6dbbc4e2671d4729b459aaaf407"
PATCH_SHA = "c4aef816a0eb0c447a67ce9d301b6f7d1b5dc1feb9a80745bfb6346dfcd804a1"
LOCK_SHA = "0d107eca2444e58cd48cb37abc48bf7115ed04f5101e6ba753ab13dea7e3f8bc"
PUBSPEC_SHA = "294368c6b2cc61a62c83880cdca56d1137de356ef9a7682a6396d4fcce9dc5f9"
DART_REVISION = "b530c21f7de367b94fb04787bfed9d8e989d75e8"
FIXTURE = ".github/diagnostics/dart-snapshot-5fc"
WORKFLOW = ".github/workflows/dart-snapshot-5fc-native.yml"
PRODUCT_FILES = {
    ".github/workflows/dart-release-windows.yml": "63a237b028166c10e34060b4e4550818054ac17080012522ac7172483ed17430",
    "packages/dart/bin/build.dart": "b86ae7d79cd288b03575ae513eaaac0649325c13ebc4bc78ff50d329a097d997",
    "packages/dart/bin/flutter.dart": "050468180d1d143d8b43a4a676c052fd02b2f8fabe10f8e5bbef393ab0360add",
    "packages/dart/lib/src/builder/artifact_publish.dart": "b3b9e8ff30fdc4358760a974a71b6886af8b1a67611e28e9bed80fbac8751979",
    "packages/dart/lib/src/builder/selection_build.dart": "05d4c526c338b4d07b5639f677ed2da946db012aea0fa13691d043b0a968de4c",
    "packages/dart/lib/src/builder/snapshot_directory.dart": "1722c2625c19828037f8195706cffb7aaf33599dc4029c502859de7a9ceee68c",
    "packages/dart/lib/src/builder/snapshot_posix.dart": "cfd3af930ae1e7dd4f7aad36d8da0719d6444b6b5383402efff40558e7413e34",
    "packages/dart/lib/src/builder/snapshot_topology.dart": "30f6b1bcc2a57f73e87b6016be952252e5a0ab5b22881b16b95b718dbcf1b882",
    "packages/dart/lib/src/builder/snapshot_windows.dart": "a76f85b6abdc7215ea14fa32bbd85831213a05ed55fbfa14d48c412f8055b0b7",
    "packages/dart/test/selection_build_test.dart": "50de54c42b090fb189d6b341103174b8996014a7826514b71b05c3af3538503f",
}
LAUNCHER = tuple(
    f"launcher {mode} {case}"
    for mode in ("JIT", "AOT")
    for case in (
        "preserves compiler exit for no_main.g.dart",
        "preserves compiler exit for no_main_中文.g.dart",
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
COMMON = (
    "native refresh preserves distinct source names beginning with U+FEFF",
    "snapshot source filter preserves escaped native name identity",
    "snapshot workspace uses the exact project folder",
    "snapshot refresh retires a present managed file",
    "snapshot refresh retires an already absent managed file",
    "snapshot refresh retires a file whose staging parent is absent",
    "snapshot refresh replaces a managed file to directory",
    "snapshot refresh replaces a managed directory to file",
    "snapshot refresh resolves actual spelling for file to directory",
    "snapshot refresh resolves actual spelling for directory to file",
    "snapshot refresh retains files under an aliased parent",
    "snapshot refresh recreates a missing shape parent",
    "snapshot refresh recreates absent managed shapes",
    "snapshot refresh replaces an empty managed directory",
    "snapshot refresh preserves an unowned staged file",
    "snapshot refresh preserves an unowned staged directory",
    "snapshot refresh preserves a replaced managed file",
    "snapshot refresh preserves a replaced managed directory",
    "snapshot refresh can retry after source graph preparation fails",
    "snapshot publishes its own staged artifact",
    "release compilation failure publishes no artifact and cleans the snapshot",
    "AOT artifact retains call-site selections after snapshot cleanup",
)
LINUX = (
    "snapshot refresh checks links using the exact marked asset path",
    "trusted project retains a shared scratch workspace",
    "shared project accepts its private build scratch directory",
)
WINDOWS = (
    "snapshot refresh can retry after a read-only staged file rejects replacement",
)
PUBLICATION = (
    "prepared publication preserves an exact escaped destination name",
    "prepared publication moves only the exact native artifact name",
    "prepared publication retains the exact project root",
    "prepared publication preserves the missing artifact error",
    "prepared publication creates ordinary parents and keeps file bytes",
    "prepared publication preserves ordinary in-project directory aliases",
    "two prepared publishers never replace the first completed artifact",
    "closed prepared output rejects publication without changing the artifact",
    "prepared output retains normal Unicode and long directory names",
    "prepared output accepts a root-level file and normalized relative names",
    "prepared output preserves a project selected through its directory alias",
    "prepared output keeps an existing ordinary file unchanged",
)


def suites(system):
    require(system in ("linux", "windows"), f"unexpected runner platform: {system}")
    return (
        ("selection", "test/selection_build_test.dart", COMMON + (LINUX if system == "linux" else WINDOWS), 12 * 60),
        ("publication", "test/artifact_publish_test.dart", PUBLICATION, 5 * 60),
        ("launcher", "test/dart_launcher_source_test.dart", LAUNCHER, 25 * 60),
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
        parent = git(repo, "rev-list", "--parents", "-n", "1", "HEAD").decode().split()
        require(os.environ.get("GITHUB_REF") == BRANCH and os.environ.get("GITHUB_EVENT_NAME") == "push"
                and head == os.environ.get("GITHUB_SHA") and parent == [head, BASE],
                "expected one diagnostic append to the exact main commit")
        paths = git(repo, "diff", "--no-renames", "--name-status", BASE, head, "--").decode().splitlines()
        expected = {f"A\t{WORKFLOW}"} | {f"A\t{FIXTURE}/{name}" for name in ("qualify.py", patch_name, "pubspec.lock")}
        require(set(paths) == expected, "the diagnostic branch must contain only its four new files")
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
    require(set(changed) == set(PRODUCT_FILES), "unexpected product file in the verified patch")
    require(git(repo, "rev-parse", f"{actual}:packages/dart").decode().strip() == DART_TREE,
            "patched Dart subtree differs")
    archive = scratch / "source.zip"
    # Git can otherwise export CRLF on Windows even from the exact LF tree.
    git(repo, "-c", "core.autocrlf=false", "-c", "core.eol=lf",
        "archive", "--format=zip", f"--output={archive}", actual, "VERSION", "packages/dart", ".github/workflows/dart-release-windows.yml")
    source = scratch / "source"
    with ZipFile(archive) as bundle:
        entries = set(bundle.namelist())
        require(entries.issuperset(PRODUCT_FILES), "expected product files missing from the archive")
        bundle.extractall(source)
    for name, expected in PRODUCT_FILES.items():
        require(digest((source / name).read_bytes()) == expected, f"archived source differs: {name}")
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
    require(version.startswith("Dart SDK version: 3.13.4 (stable) ") and version.endswith(f'on "{expected}"'),
            f"unexpected Dart release or ABI: {version}")
    revision = (sdk.parent.parent / "revision").read_text().strip()
    require(revision == DART_REVISION, f"unexpected Dart SDK revision: {revision}")
    return str(sdk), version


def coverage_receipt(raw, system):
    reports = sorted(raw.rglob("*.vm.json"))
    require(reports, "native Dart selection did not export VM coverage")
    engine = "snapshot_posix.dart" if system == "linux" else "snapshot_windows.dart"
    names = ("selection_build.dart", "snapshot_directory.dart", "snapshot_topology.dart", engine)
    counts = {name: {"hits": {}, "branchHits": {}} for name in names}
    for report in reports:
        for entry in json.loads(report.read_text(encoding="utf-8"))["coverage"]:
            name = entry["source"].split("/")[-1]
            if name not in counts:
                continue
            for metric, tally in counts[name].items():
                positions = entry.get(metric, [])
                require(len(positions) % 2 == 0, f"invalid coverage positions for {name}")
                for line, hits in zip(positions[::2], positions[1::2]):
                    tally[line] = tally.get(line, 0) + hits
    for name, metrics in counts.items():
        require(all(any(tally.values()) for tally in metrics.values()), f"missing native line/branch coverage: {name}")
    return {"sources": counts, "rawReportCount": len(reports),
            "scope": "Selected Dart VM statement and branch positions; compiler subprocesses are verified by test events."}


def native_test(package, scratch, output, receipt):
    dart, version = verify_sdk()
    system = "windows" if sys.platform == "win32" else sys.platform
    selected = suites(system)
    if system == "linux":
        require(platform.freedesktop_os_release()["VERSION_ID"] == "24.04", "expected Ubuntu 24.04")
    receipt.update(sdk=version, sdkRevision=DART_REVISION, runnerImage=os.environ.get("ImageOS"),
                   runnerImageVersion=os.environ.get("ImageVersion"), platform=platform.platform(), suites={})
    temp = scratch / "temp"
    temp.mkdir()
    env = dict(os.environ, PUB_CACHE=str(scratch / "pub-cache"), TMP=str(temp), TEMP=str(temp), TMPDIR=str(temp))
    record_command([dart, "pub", "get", "--enforce-lockfile"], package, env, output, "locked-pub-get", 5 * 60)
    lock = package / "pubspec.lock"
    require(digest(lock.read_bytes()) == LOCK_SHA, "Dart dependency resolution altered the lock")
    failures = []
    raw = output / "raw-selection-coverage"
    for tag, file, names, timeout in selected:
        command = [dart, "test", "--concurrency=1", "--reporter=json", file]
        if tag == "selection":
            command += ["--branch-coverage", f"--coverage={raw}"]
            command += ["--name", "^(?:" + "|".join(re.escape(name).replace(r"\ ", " ") for name in names) + ")$"]
        try:
            report = record_command(command, package, env, output, tag, timeout)
            receipt["suites"][tag] = verify_report(report, names)
            if tag == "selection":
                measured = coverage_receipt(raw, system)
                (output / "coverage.json").write_text(json.dumps(measured, indent=2) + "\n", encoding="utf-8")
        except (RuntimeError, OSError, ValueError, subprocess.SubprocessError) as error:
            failures.append(f"{tag}: {error}")
    product_temp = package.parents[1] / ".tmp"
    remaining = sorted(file.name for file in product_temp.iterdir()) if product_temp.exists() else []
    receipt["retainedFixtureDirectories"] = remaining
    receipt["resolvedLockSHA256"] = digest(lock.read_bytes())
    require(not remaining, f"test fixtures were left behind: {remaining}")
    require(receipt["resolvedLockSHA256"] == LOCK_SHA, "Dart tests altered the lock")
    require(not failures, "\n".join(failures))
    record_command([dart, "analyze", "--fatal-infos"], package, env, output, "analyze", 4 * 60)
    require(digest(lock.read_bytes()) == LOCK_SHA, "Dart analysis altered the lock")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verify-only", action="store_true", help="stage the locked tree without invoking Dart")
    parser.add_argument("--repo", type=Path, default=os.environ.get("GITHUB_WORKSPACE"))
    parser.add_argument("--scratch", type=Path, default=os.environ.get("RUNNER_TEMP"))
    args = parser.parse_args()
    require(args.repo and args.scratch, "explicit repository and scratch paths required")
    root = args.scratch.resolve(strict=True)
    receipt = dict(base=BASE, baseTree=BASE_TREE, productTree=TREE, patchSHA256=PATCH_SHA, lockSHA256=LOCK_SHA,
                   dartTree=DART_TREE, sourceSHA256=PRODUCT_FILES,
                   expectedTests={system: {name: len(cases) for name, _, cases, _ in suites(system)}
                                  for system in ("linux", "windows")})
    if not args.verify_only:
        output = root / "dart-snapshot-5fc-evidence"
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
