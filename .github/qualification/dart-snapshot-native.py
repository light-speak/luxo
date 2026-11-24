"""Verify the public Dart snapshot candidate on native CI runners."""
import base64
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import shutil
import stat
import subprocess
import sys
import tempfile
import zipfile
from urllib.parse import unquote


DIAGNOSTIC_BASE = "03eb1dce2ed5f7b2d1796f08d934cb8860fb4f82"
BASE = "32a10c114ac3c282b4ce46189b646d28eb767b1b"
BASE_TREE = "3ddf747cccd83c2110b3dbeb5ce6c099c4de7c8e"
CANDIDATE_TREE = "9b8bbe722150fd0830c358900bbc383c248e70e1"
DARWIN_TREE = "048b848d6dac5985cabe6b4efe3004aa4fa09520"
PATCH_SHA = "b6054acd6c776c9334050dcf3ffcdc0d4af1cb4635442e501c8c21d1aaaa5c56"
DART_VERSION = "3.13.4"
RELEASE_WORKFLOW = ".github/workflows/dart-release-windows.yml"
DIAGNOSTIC_FILES = {
    ".github/workflows/qualify-dart-snapshot-native.yml",
    ".github/qualification/dart-snapshot-native.py",
    ".github/qualification/dart-snapshot-product.patch.b64",
}
PRODUCT_FILES = {
    RELEASE_WORKFLOW: "a9f056801f074b364bc4991cd7a17e6dc34011e35c41d77a65cfcf9624d7c19f",
    "packages/dart/bin/build.dart": "6459d88ca789c0839fa1250074d4527c80d577a732b8d868603a40b3437f771e",
    "packages/dart/bin/flutter.dart": "050468180d1d143d8b43a4a676c052fd02b2f8fabe10f8e5bbef393ab0360add",
    "packages/dart/lib/src/builder/artifact_publish.dart": "081b7122a34a0d29eab7391c73b050d5678c0c9c769e0ad6269ea22b5549b2fa",
    "packages/dart/lib/src/builder/selection_build.dart": "01f27d83af297789b41af6af955c66abba99d2f4d89792cd75f279a38b05bc6f",
    "packages/dart/lib/src/builder/snapshot_directory.dart": "50370422d15e1696661b0a0f69f52ae7079bf95d33c5dfbc721bc259a6a4cfda",
    "packages/dart/lib/src/builder/snapshot_posix.dart": "963467186dfadfb56a30376481c182ae141809322b78587de1744ee555bff5fb",
    "packages/dart/lib/src/builder/snapshot_windows.dart": "339a85a18eb62092ed4126d75aad294a4d7882f34e737914e4ca4ea5df098344",
    "packages/dart/test/selection_build_test.dart": "4ece4a28dca5382d273f55db8b4121f226e84b2c8e13aeab2a2555e71772a94e",
}
SUITES = [
    "test/artifact_publish_test.dart",
    "test/selection_build_test.dart",
    "test/flutter_commands_test.dart",
]
COMMON = {
    "a missing Darwin snapshot ACL requires ENOENT",
    "Darwin ACL policy refuses unknown tags, flag values and grants",
    "failed prepare cleans its snapshot and can retry",
    "prepared publication preserves the missing artifact error",
    "prepared publication creates ordinary parents and keeps file bytes",
    "prepared publication preserves ordinary in-project directory aliases",
    "two prepared publishers never replace the first completed artifact",
    "closed prepared output rejects publication without changing the artifact",
    "prepared output retains normal Unicode and long directory names",
    "prepared output accepts a root-level file and normalized relative names",
    "prepared output preserves a project selected through its directory alias",
    "prepared output keeps an existing ordinary file unchanged",
    "snapshot refresh retires a present managed file",
    "snapshot refresh retires an already absent managed file",
    "snapshot refresh retires a file whose staging parent is absent",
    "snapshot refresh can retry after source graph preparation fails",
    "snapshot refresh retains a zero-length asset",
    "snapshot publishes its own staged artifact",
    "release output rejects overwrites and escapes before creating directories",
    "build publication accepts an ordinary in-project temporary alias",
    "build close releases an unconsumed prepared output",
    "closing build rejects an in-flight output preparation",
    "release compilation failure publishes no artifact and cleans the snapshot",
    "build ownership accepts a trailing directory separator",
    "release publication preserves an output created after validation",
    "release publication reports a missing artifact error",
    "AOT artifact retains call-site selections after snapshot cleanup",
    "restart waits for transformation; other commands pass through",
    "failed refresh rejects restart without killing the session",
}
WINDOWS_ONLY = {
    "snapshot refresh can retry after a read-only staged file rejects replacement",
}
WINDOWS = COMMON | WINDOWS_ONLY
LINUX = COMMON | {
    "failed prepare keeps both errors when scratch cleanup is blocked",
    "shared project accepts its private build scratch directory",
    "snapshot scratch accepts a sticky shared directory",
    "trusted project retains a shared scratch workspace",
    "shared scratch restores retirement after an ordinary permission error",
    "failed initial move clears only its empty private staging directory",
    "snapshot refresh retries retirement after a staging permission error",
    "snapshot prepare retries after initial scratch creation is denied",
    "snapshot rejects a project and scratch without a trusted anchor",
    "Flutter CLI cleans its snapshot after EOF or native exit null",
    "Flutter CLI cleans its snapshot after EOF or native exit 0",
    "Flutter CLI cleans its snapshot after EOF or native exit 7",
}
MAC_ONLY = {
    "macOS snapshot creation rejects deferred ACL inheritance and can retry",
    "0700 scratch accepts read-only and inherited deny ACLs",
    "0770 scratch accepts read-only and inherited deny ACLs",
    "macOS ACL trust rejects possible mutating and inherited traversal grants",
}


def git(repo, *args):
    return subprocess.check_output(["git", *args], cwd=repo, text=True).strip()


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_json(destination, data):
    destination.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def restore(repo, scratch):
    require(git(repo, "rev-parse", f"{BASE}^{{tree}}") == BASE_TREE, "base tree changed")
    payload = Path(__file__).with_name("dart-snapshot-product.patch.b64")
    patch = scratch / "product.patch"
    patch.write_bytes(base64.b64decode(payload.read_bytes()))
    require(digest(patch) == PATCH_SHA, "product patch digest changed")

    store = scratch / "git"
    git(repo, "init", "--quiet", "--bare", str(store))
    alternates = store / "objects/info/alternates"
    objects = git(repo, "rev-parse", "--path-format=absolute", "--git-path", "objects")
    alternates.write_bytes((Path(objects).as_posix() + "\n").encode("utf-8"))
    binding = ("--git-dir", str(store))
    git(repo, *binding, "read-tree", BASE)
    git(repo, *binding, "apply", "--cached", "--whitespace=error", str(patch))
    actual = git(repo, *binding, "write-tree")
    require(actual == CANDIDATE_TREE, "product tree changed")
    changed = git(repo, *binding, "diff", "--cached", "--name-only", BASE).splitlines()
    require(set(changed) == PRODUCT_FILES.keys(), "unexpected product paths")
    git(repo, *binding, "apply", "--cached", "--reverse", "--whitespace=error", str(patch))
    require(git(repo, *binding, "write-tree") == BASE_TREE, "reverse projection failed")

    archive_path = scratch / "source.zip"
    # Git archive can apply the runner's worktree line-ending settings.
    git(repo, "-c", "core.autocrlf=false", "-c", "core.eol=lf", *binding,
        "archive", "--format=zip", "-o", str(archive_path),
        actual, "--", "packages/dart", RELEASE_WORKFLOW)
    source = scratch / "src"
    with zipfile.ZipFile(archive_path) as archive:
        for entry in archive.infolist():
            path = PurePosixPath(entry.filename)
            require(
                not path.is_absolute() and ".." not in path.parts
                and "\\" not in entry.filename
                and not stat.S_ISLNK(entry.external_attr >> 16)
                and (path.parts[:2] == ("packages", "dart") or entry.filename == RELEASE_WORKFLOW
                     or entry.filename in {"packages/", ".github/", ".github/workflows/"}),
                f"unexpected archive entry: {entry.filename}",
            )
        archive.extractall(source)
    require(all(digest(source / path) == sha for path, sha in PRODUCT_FILES.items()),
            "extracted product bytes changed")
    product_subtree = git(repo, *binding, "rev-parse", f"{actual}:packages/dart")
    return source, product_subtree


def capture(package, evidence, name, args, timeout):
    stdout = evidence / (name + ".log")
    stderr = evidence / (name + ".stderr")
    with stdout.open("wb") as out, stderr.open("wb") as err:
        result = subprocess.run(args, cwd=package, stdout=out, stderr=err, timeout=timeout)
    if result.returncode:
        print(stdout.read_text(encoding="utf-8", errors="replace")[-18000:])
        print(stderr.read_text(encoding="utf-8", errors="replace")[-6000:])
        raise RuntimeError(f"{name} failed with exit {result.returncode}")
    return stdout


def verify_events(receipt, required, allowed_skips, exact):
    started = {}
    passed = set()
    skipped = set()
    failed = set()
    final = []
    for line in receipt.read_text(encoding="utf-8").splitlines():
        event = json.loads(line)
        kind = event["type"]
        if kind == "testStart":
            started[event["test"]["id"]] = event["test"]["name"]
        elif kind == "testDone" and not event["hidden"]:
            name = started[event["testID"]]
            if event["skipped"]:
                skipped.add(name)
            elif event["result"] == "success":
                passed.add(name)
            else:
                failed.add(name)
        elif kind == "done":
            final.append(event["success"])
    require(final == [True] and not failed, "Dart JSON reporter did not finish successfully")
    require(required <= passed, f"required tests did not pass: {sorted(required - passed)}")
    require(skipped == allowed_skips, f"unexpected skipped tests: {sorted(skipped)}")
    if exact:
        require(passed == required, f"unexpected selected tests: {sorted(passed - required)}")
    return {"passed": sorted(passed), "skipped": sorted(skipped)}


def verify_hit_positions(tracked, metric, required):
    verified = {}
    for path, positions in required.items():
        counts = tracked[path][metric]
        verified[path] = {}
        for line in positions:
            require(line in counts, f"snapshot {metric} position was not reported: {path}:{line}")
            require(counts[line] > 0, f"snapshot {metric} position was not executed: {path}:{line}")
            verified[path][line] = counts[line]
    return verified


def coverage_receipt(source, raw, expected):
    reports = sorted(raw.rglob("*.vm.json"))
    require(reports, "Dart VM did not write any coverage reports")
    tracked = {
        path: {"hits": {}, "branchHits": {}}
        for path in PRODUCT_FILES
        if path.startswith(("packages/dart/lib/", "packages/dart/bin/"))
    }
    aliases = {}
    for path in tracked:
        aliases[unquote((source / path).as_uri())] = path
        if path.startswith("packages/dart/lib/"):
            aliases["package:luxo_client/" + path.removeprefix("packages/dart/lib/")] = path
    if os.name == "nt":
        aliases = {uri.lower(): path for uri, path in aliases.items()}

    for report in reports:
        data = json.loads(report.read_text(encoding="utf-8"))
        for entry in data["coverage"]:
            uri = unquote(entry["source"])
            path = aliases.get(uri.lower() if os.name == "nt" else uri)
            if path is None:
                continue
            for kind, positions in tracked[path].items():
                values = entry.get(kind, [])
                require(len(values) % 2 == 0, f"invalid {kind} pairs for {path}")
                for line, count in zip(values[::2], values[1::2]):
                    positions[line] = positions.get(line, 0) + count

    observed = {}
    for path, data in tracked.items():
        require(digest(source / path) == PRODUCT_FILES[path],
                f"coverage source changed: {path}")
        observed[path] = {"sha256": PRODUCT_FILES[path]}
        for kind, label in (("hits", "lines"), ("branchHits", "branchSourcePositions")):
            positions = data[kind]
            missed = sorted(position for position, count in positions.items() if count == 0)
            observed[path][label] = {
                "hit": len(positions) - len(missed), "total": len(positions), "missed": missed,
            }
    engine = "windows" if expected == "windows" else "posix"
    host = observed[f"packages/dart/lib/src/builder/snapshot_{engine}.dart"]
    require(host["lines"]["hit"] > 0 and host["branchSourcePositions"]["hit"] > 0,
            "no executed source and branch coverage from the native snapshot implementation")
    builder = "packages/dart/lib/src/builder"
    directory = f"{builder}/snapshot_directory.dart"
    native = f"{builder}/snapshot_{engine}.dart"
    if expected == "windows":
        required_branches = {directory: (148, 157, 165, 173, 389), native: (181,)}
        required_lines = {directory: (158,), native: (175, 198, 200)}
    else:
        required_branches = {directory: (157, 158, 165, 173, 389), native: (224, 336)}
        required_lines = {native: (225, 266)}
    return {
        "scope": "Selected VM-instrumented cases. Zero positions mean no positions were "
                 "reported for that metric. CLI Dart and AOT subprocesses are verified by test events.",
        "rawReportCount": len(reports),
        "suites": SUITES,
        "productSources": observed,
        "requiredOrdinaryBranchHits": verify_hit_positions(tracked, "branchHits", required_branches),
        "requiredOrdinaryLineHits": verify_hit_positions(tracked, "hits", required_lines),
    }


def qualify(source, evidence, expected):
    native = {"linux": "Linux", "windows": "Windows"}[expected]
    require(platform.system() == native and platform.machine().lower() in {"amd64", "x86_64"},
            "the runner is not the required native x64 platform")
    if expected == "linux":
        require(os.geteuid() != 0, "permissions tests require an ordinary Linux runner user")
    dart = shutil.which("dart.exe" if os.name == "nt" else "dart")
    require(dart, "native Dart executable is missing")
    package = source / "packages/dart"
    capture(package, evidence, "dart-version", [dart, "--version"], 30)
    version = "\n".join((evidence / name).read_text(encoding="utf-8")
                        for name in ("dart-version.log", "dart-version.stderr"))
    require(f"Dart SDK version: {DART_VERSION} " in version and f'"{expected}_x64"' in version,
            f"unexpected Dart SDK: {version.strip()}")
    capture(package, evidence, "dependencies", [dart, "pub", "get"], 240)
    shutil.copyfile(package / "pubspec.lock", evidence / "pubspec.lock")
    capture(package, evidence, "analyze", [dart, "analyze", "--fatal-infos"], 240)

    raw_coverage = source.parent / "vm-coverage"
    args = [dart, "test", "--concurrency=1", "--reporter=json",
            "--branch-coverage", f"--coverage={raw_coverage}", *SUITES]
    isolated_selection = expected == "windows"
    if isolated_selection:
        args += ["--name", "^(?:" + "|".join(sorted(WINDOWS)) + ")$"]
    receipt = capture(package, evidence, "tests", args, 720)
    required = WINDOWS if isolated_selection else LINUX
    skips = set() if isolated_selection else MAC_ONLY | WINDOWS_ONLY
    verdict = verify_events(receipt, required, skips, isolated_selection)
    write_json(evidence / "coverage.json", coverage_receipt(source, raw_coverage, expected))
    for directory in (source / ".tmp", package / ".tmp"):
        require(not directory.is_symlink() and (not directory.exists() or not any(directory.iterdir())),
                f"fixture teardown left entries in {directory.relative_to(source)}")
    return verdict


def main():
    options = sys.argv[1:]
    require(options in ([], ["--plan-only"]), "usage: dart-snapshot-native.py [--plan-only]")
    plan = bool(options)
    repo = Path(git(Path.cwd(), "rev-parse", "--show-toplevel")).resolve()
    owned = repo / ".tmp"
    require(not owned.is_symlink(), "qualification scratch cannot be a symlink")
    owned.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="ds-", dir=owned) as temp:
        source, subtree = restore(repo, Path(temp))
        provenance = {
            "base": BASE,
            "baseTree": BASE_TREE,
            "candidateTree": CANDIDATE_TREE,
            "darwinCandidateTree": DARWIN_TREE,
            "dartTree": subtree,
            "productPatchSHA256": PATCH_SHA,
            "productFileSHA256": PRODUCT_FILES,
            "diagnosticBase": DIAGNOSTIC_BASE,
            "diagnosticHead": git(repo, "rev-parse", "HEAD"),
            "runner": platform.platform(),
            "run": os.environ.get("GITHUB_RUN_ID"),
            "attempt": os.environ.get("GITHUB_RUN_ATTEMPT"),
        }
        print(json.dumps(provenance, indent=2, sort_keys=True))
        if plan:
            return

        require(provenance["diagnosticHead"] == os.environ["GITHUB_SHA"], "unexpected workflow head")
        files = set(git(repo, "diff", "--name-only", DIAGNOSTIC_BASE, "HEAD").splitlines())
        require(files == DIAGNOSTIC_FILES, "diagnostic branch changed unrelated paths")
        evidence = owned / "dart-snapshot-native-evidence"
        evidence.mkdir()
        write_json(evidence / "source.json", provenance)
        verdict = qualify(source, evidence, os.environ["DART_NATIVE_PLATFORM"])
    write_json(evidence / "verdict.json", {**verdict, "ownedScratchCleaned": True})
    print(f"Verified {len(verdict['passed'])} native tests and removed the isolated work directory.")


if __name__ == "__main__":
    main()
