"""Verify exact Dart snapshot, release and launcher behavior natively."""

import argparse
from base64 import b64decode
from contextlib import contextmanager
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
PARENT = "321f5268f195f6b3fefed5780d2e79a66d5ddb4c"
PREVIOUS = "52ac87397267602b56cacd926c3370e9cc73bdbc"
ORIGINAL = "4247c57406c05ae0b69c4fa44f96396fb1d0f18d"
BRANCH = "refs/heads/diagnostics/dart-snapshot-5fc"
BASE_TREE = "41ee4dd6f070fac672dbfb0450ad203fd60ed3fd"
TREE = "51d99cf4d2be82ec08dd39fd91b4f1bce6a8671c"
DART_TREE = "af619c494f7c8a5e760b1dfd122f1d0b49c791f1"
PATCH_SHA = "2b373410fdc7bd3697a9dc7e1d075824c9cac4425dbf7740ed8edcac8c5d1545"
LOCK_SHA = "0d107eca2444e58cd48cb37abc48bf7115ed04f5101e6ba753ab13dea7e3f8bc"
PUBSPEC_SHA = "294368c6b2cc61a62c83880cdca56d1137de356ef9a7682a6396d4fcce9dc5f9"
DART_REVISION = "b530c21f7de367b94fb04787bfed9d8e989d75e8"
FIXTURE = ".github/diagnostics/dart-snapshot-5fc"
WORKFLOW = ".github/workflows/dart-snapshot-5fc-native.yml"
PRODUCT_FILES = {
    ".github/workflows/dart-release-windows.yml": "ecda9e19c301004239f102c3f3d87188ddb2f0ef50b7422eeb839dfacd3376df",
    "packages/dart/bin/build.dart": "b86ae7d79cd288b03575ae513eaaac0649325c13ebc4bc78ff50d329a097d997",
    "packages/dart/bin/flutter.dart": "050468180d1d143d8b43a4a676c052fd02b2f8fabe10f8e5bbef393ab0360add",
    "packages/dart/lib/src/builder/artifact_publish.dart": "b3b9e8ff30fdc4358760a974a71b6886af8b1a67611e28e9bed80fbac8751979",
    "packages/dart/lib/src/builder/selection_build.dart": "05d4c526c338b4d07b5639f677ed2da946db012aea0fa13691d043b0a968de4c",
    "packages/dart/lib/src/builder/snapshot_directory.dart": "1722c2625c19828037f8195706cffb7aaf33599dc4029c502859de7a9ceee68c",
    "packages/dart/lib/src/builder/snapshot_posix.dart": "cfd3af930ae1e7dd4f7aad36d8da0719d6444b6b5383402efff40558e7413e34",
    "packages/dart/lib/src/builder/snapshot_topology.dart": "30f6b1bcc2a57f73e87b6016be952252e5a0ab5b22881b16b95b718dbcf1b882",
    "packages/dart/lib/src/builder/snapshot_windows.dart": "a76f85b6abdc7215ea14fa32bbd85831213a05ed55fbfa14d48c412f8055b0b7",
    "packages/dart/test/select_fixture.dart": "8928cc9983c72cd48743b42fd61e1154fddc7a9b5c12524177658d0ae8b75a3f",
    "packages/dart/test/selection_build_test.dart": "f5831d5f2b160a3161671b9540454772eb5be982a9d5cc429a12d430456ff076"
}
COMMON = (
    "build close releases an unconsumed prepared output",
    "build rejects a client outside lib and cleans the snapshot",
    "build can retry after package config omits the project",
    "build cannot close until an active refresh completes",
    "build refuses to clean a replacement workspace",
)
SPLIT_DEVICE = "snapshot rejects cross-device shared scratch and retries privately"
JUNCTION = "snapshot refresh rejects a Windows junction and can retry"
COMPONENT = "build rejects a regular scratch file and can retry"
UNRESOLVABLE = "an unresolvable Dart path leaves the prior snapshot unchanged"
READ_ONLY_CLOSE = "snapshot close retries after a Windows read-only file denies deletion"
SHAPE_RENAME = "snapshot refresh retries a Windows directory rename after its child closes"
FIXTURE_ENV = "LUXO_TEST_POSIX_SPLIT_DEVICE_PROJECT"


def suites(system):
    require(system in ("linux", "windows"), f"unexpected runner platform: {system}")
    selection_lines = {
        "selection_build.dart": (27, 166, 182),
        "snapshot_directory.dart": (118, 277 if system == "linux" else 243),
    }
    if system == "linux":
        return (("selection", COMMON, selection_lines), ("split-device", (SPLIT_DEVICE,), {"snapshot_directory.dart": (51,)}))
    selection_lines["selection_build.dart"] += (144,)
    return (
        ("selection", COMMON + (UNRESOLVABLE,), selection_lines),
        ("junction", (JUNCTION,), {"snapshot_windows.dart": (114,)}),
        ("component-open", (COMPONENT, READ_ONLY_CLOSE), {
            "artifact_publish.dart": (594,),
            "snapshot_windows.dart": (343,),
        }),
        ("shape-rename", (SHAPE_RENAME,), {"snapshot_windows.dart": (193,)}),
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
                and head == os.environ.get("GITHUB_SHA") and parent == [head, PARENT]
                and git(repo, "rev-parse", PARENT + "^").decode().strip() == PREVIOUS
                and git(repo, "rev-parse", PREVIOUS + "^").decode().strip() == ORIGINAL
                and git(repo, "rev-parse", ORIGINAL + "^").decode().strip() == BASE,
                "expected one diagnostic append to the pinned qualification")
        paths = git(repo, "diff", "--no-renames", "--name-status", BASE, head, "--").decode().splitlines()
        expected = {f"A\t{WORKFLOW}"} | {f"A\t{FIXTURE}/{name}" for name in ("qualify.py", patch_name, "pubspec.lock")}
        require(set(paths) == expected, "the diagnostic branch must contain only its four new files")
        appended = {f"M\t{WORKFLOW}", f"M\t{FIXTURE}/qualify.py", f"M\t{FIXTURE}/{patch_name}"}
        relative = set(git(repo, "diff", "--no-renames", "--name-status", PARENT, head, "--").decode().splitlines())
        require(relative == appended, "unexpected scope in the coverage append")
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


def stat_tuple(path):
    values = path.lstat()
    return [values.st_dev, values.st_ino, values.st_uid, values.st_mode]


def split_device_identity(root, fixture):
    scratch = fixture / '.tmp'
    require(fixture.is_absolute() and fixture == fixture.resolve(strict=True) and fixture.parent == root,
            'Split-device project must be a real direct child of checkout .tmp')
    require(scratch.is_dir() and not scratch.is_symlink(), 'Split-device scratch must be a real directory')
    require(set(fixture.iterdir()) == {scratch} and not list(scratch.iterdir()), 'Split-device fixture is not empty')
    project, mounted = stat_tuple(fixture), stat_tuple(scratch)
    require(project[0] != mounted[0], 'Project and scratch use the same st_dev')
    return [project, mounted]


@contextmanager
def split_device_fixture(root):
    require(os.environ.get('RUNNER_ENVIRONMENT') == 'github-hosted'
            and FIXTURE_ENV not in os.environ, 'Expected an owned GitHub-hosted mount')
    uid, gid = os.geteuid(), os.getegid()
    require(uid != 0, 'Run the Dart test as the unprivileged runner user')
    project = Path(tempfile.mkdtemp(prefix='dart-split-device-', dir=root))
    scratch = project / '.tmp'
    try:
        scratch.mkdir(mode=0o700)
        options = f'size=16m,uid={uid},gid={gid},mode=0770,nosuid,nodev,noexec'
        subprocess.run(['sudo', '-n', 'mount', '-t', 'tmpfs', '-o', options, project.name, str(scratch)], check=True)
        yield project
    finally:
        if scratch.exists():
            mount = subprocess.run(
                ['findmnt', '--noheadings', '--raw', '--output', 'SOURCE,FSTYPE', '--mountpoint', str(scratch)],
                capture_output=True, text=True, check=False,
            )
            if mount.returncode == 0:
                require(mount.stdout.split() == [project.name, 'tmpfs'], 'Unexpected fixture mount identity')
                subprocess.run(['sudo', '-n', 'umount', str(scratch)], check=True)
            else:
                require(mount.returncode == 1 and not mount.stderr.strip(), 'Cannot inspect the fixture mount')
            scratch.rmdir()
        project.rmdir()


def coverage_receipt(raw, required):
    reports = sorted(raw.rglob("*.vm.json"))
    require(reports, "native Dart selection did not export VM coverage")
    names = tuple(Path(path).name for path in PRODUCT_FILES if "/lib/src/builder/" in path)
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
    for name, lines in required.items():
        for line in lines:
            require(counts[name]["hits"].get(line, 0) > 0, f"unexecuted native statement: {name}:{line}")
    return {"sources": counts, "rawReportCount": len(reports),
            "scope": "Selected Dart VM statement and branch positions; compiler subprocesses are verified by test events."}


def run_gate(dart, package, output, env, tag, names, required):
    raw = output / f"raw-{tag}-coverage"
    pattern = "^(?:" + "|".join(re.escape(name).replace(r"\ ", " ") for name in names) + ")$"
    command = [dart, "test", "--concurrency=1", "--reporter=json", "--branch-coverage",
               f"--coverage={raw}", "test/selection_build_test.dart", "--name", pattern]
    report = record_command(command, package, env, output, tag, 5 * 60)
    passed = verify_report(report, names)
    measured = coverage_receipt(raw, required)
    if tag == "shape-rename":
        branch = measured["sources"]["snapshot_windows.dart"]["branchHits"].get(192, 0)
        require(branch > 0, "unexecuted native branch: snapshot_windows.dart:192")
    (output / f"coverage-{tag}.json").write_text(json.dumps(measured, indent=2) + "\n", encoding="utf-8")
    return {"passed": passed, "requiredPhysicalLines": required}


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
    if system == "windows":
        volume = record_command(
            ["powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
             "[System.IO.DriveInfo]::new($env:LUXO_TEST_VOLUME).DriveFormat"],
            package, env | {"LUXO_TEST_VOLUME": package.anchor}, output, "fixture-volume", 30,
        ).read_text(encoding="utf-8").strip()
        require(volume == "NTFS", f"native rename witness needs local NTFS, got {volume}")
        receipt["fixtureFilesystem"] = volume
    record_command([dart, "pub", "get", "--enforce-lockfile"], package, env, output, "locked-pub-get", 5 * 60)
    lock = package / "pubspec.lock"
    require(digest(lock.read_bytes()) == LOCK_SHA, "Dart dependency resolution altered the lock")
    failures = []
    product_temp = package.parents[1] / ".tmp"
    product_temp.mkdir()
    for tag, names, required in selected:
        try:
            if tag == "split-device":
                with split_device_fixture(product_temp) as fixture:
                    before = split_device_identity(product_temp, fixture)
                    coverage = run_gate(dart, package, output, env | {FIXTURE_ENV: str(fixture)}, tag, names, required)
                    after = split_device_identity(product_temp, fixture)
                    require(after == before, "owned split-device fixture identity changed")
                coverage["splitDeviceIdentity"] = after
                coverage["ownedMountRemoved"] = not fixture.exists()
                require(coverage["ownedMountRemoved"], "owned split-device mount left materialized")
            else:
                coverage = run_gate(dart, package, output, env, tag, names, required)
            receipt["suites"][tag] = coverage
        except (RuntimeError, OSError, ValueError, subprocess.SubprocessError) as error:
            failures.append(f"{tag}: {error}")
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
                   expectedTests={system: {name: len(cases) for name, cases, _ in suites(system)}
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
