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


BASE = "03eb1dce2ed5f7b2d1796f08d934cb8860fb4f82"
BASE_TREE = "a05f7ba50a876937b5764a0e45e78f5307a69d0d"
CANDIDATE_TREE = "87684119c182f5295e7e998f7ae4b60b8f8d54ed"
DARWIN_TREE = "6c6fc5e950301df427758eca62bcc7bf6fa401d4"
PATCH_SHA = "eb5b72c871b115668ccd53931a0dbcf61d911adbf0c4bef1b5e10085919b04f8"
DART_VERSION = "3.13.4"
RELEASE_WORKFLOW = ".github/workflows/dart-release-windows.yml"
DIAGNOSTIC_FILES = {
    ".github/workflows/qualify-dart-snapshot-native.yml",
    ".github/qualification/dart-snapshot-native.py",
    ".github/qualification/dart-snapshot-product.patch.b64",
}
PRODUCT_FILES = {
    RELEASE_WORKFLOW: "9987bb1e84fcc60179d86fc60d5ae64cb094abe1ba707c3934776d778f2a09f8",
    "packages/dart/bin/build.dart": "6459d88ca789c0839fa1250074d4527c80d577a732b8d868603a40b3437f771e",
    "packages/dart/bin/flutter.dart": "050468180d1d143d8b43a4a676c052fd02b2f8fabe10f8e5bbef393ab0360add",
    "packages/dart/lib/src/builder/artifact_publish.dart": "081b7122a34a0d29eab7391c73b050d5678c0c9c769e0ad6269ea22b5549b2fa",
    "packages/dart/lib/src/builder/selection_build.dart": "01f27d83af297789b41af6af955c66abba99d2f4d89792cd75f279a38b05bc6f",
    "packages/dart/lib/src/builder/snapshot_directory.dart": "50370422d15e1696661b0a0f69f52ae7079bf95d33c5dfbc721bc259a6a4cfda",
    "packages/dart/lib/src/builder/snapshot_posix.dart": "c32e8a585d3c2ecea91cbf8ffc17dae01a3f737ace695203e7c3292619e986cf",
    "packages/dart/lib/src/builder/snapshot_windows.dart": "339a85a18eb62092ed4126d75aad294a4d7882f34e737914e4ca4ea5df098344",
    "packages/dart/test/selection_build_test.dart": "3c98902d8a9f10edc26be167507ed8dedf991afb5559281cb18c97035d6d431c",
}
SUITES = [
    "test/artifact_publish_test.dart",
    "test/selection_build_test.dart",
    "test/flutter_commands_test.dart",
]
COMMON = {
    "prepared publication preserves the missing artifact error",
    "prepared publication creates ordinary parents and keeps file bytes",
    "prepared publication preserves ordinary in-project directory aliases",
    "two prepared publishers never replace the first completed artifact",
    "closed prepared output rejects publication without changing the artifact",
    "prepared output retains normal Unicode and long directory names",
    "prepared output accepts a root-level file and normalized relative names",
    "prepared output preserves a project selected through its directory alias",
    "prepared output keeps an existing ordinary file unchanged",
    "snapshot refresh retires an already absent managed file",
    "snapshot refresh can retry after source graph preparation fails",
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
LINUX = COMMON | {
    "shared project accepts its private build scratch directory",
    "snapshot scratch accepts a sticky shared directory",
    "trusted project retains a shared scratch workspace",
    "shared scratch restores retirement after an ordinary permission error",
    "failed initial move clears only its empty private staging directory",
    "snapshot rejects a project and scratch without a trusted anchor",
    "Flutter CLI cleans its snapshot after EOF or native exit null",
    "Flutter CLI cleans its snapshot after EOF or native exit 0",
    "Flutter CLI cleans its snapshot after EOF or native exit 7",
}
MAC_ONLY = {
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

    args = [dart, "test", "--concurrency=1", "--reporter=json", *SUITES]
    isolated_selection = expected == "windows"
    if isolated_selection:
        args += ["--name", "^(?:" + "|".join(sorted(COMMON)) + ")$"]
    receipt = capture(package, evidence, "tests", args, 720)
    required = COMMON if isolated_selection else LINUX
    skips = set() if isolated_selection else MAC_ONLY
    verdict = verify_events(receipt, required, skips, isolated_selection)
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
            "diagnosticHead": git(repo, "rev-parse", "HEAD"),
            "runner": platform.platform(),
            "run": os.environ.get("GITHUB_RUN_ID"),
            "attempt": os.environ.get("GITHUB_RUN_ATTEMPT"),
        }
        print(json.dumps(provenance, indent=2, sort_keys=True))
        if plan:
            return

        require(provenance["diagnosticHead"] == os.environ["GITHUB_SHA"], "unexpected workflow head")
        files = set(git(repo, "diff", "--name-only", BASE, "HEAD").splitlines())
        require(files == DIAGNOSTIC_FILES, "diagnostic branch changed unrelated paths")
        evidence = owned / "dart-snapshot-native-evidence"
        evidence.mkdir()
        write_json(evidence / "source.json", provenance)
        verdict = qualify(source, evidence, os.environ["DART_NATIVE_PLATFORM"])
    write_json(evidence / "verdict.json", {**verdict, "ownedScratchCleaned": True})
    print(f"Verified {len(verdict['passed'])} native tests and removed the isolated work directory.")


if __name__ == "__main__":
    main()
