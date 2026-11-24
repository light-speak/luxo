"""Replay the pinned Dart candidate and exercise one native boundary."""

import argparse
import base64
import hashlib
import json
import os
import platform
import runpy
import shutil
import signal
import stat
import subprocess
import tempfile
import zipfile
from pathlib import Path, PurePosixPath


DIAGNOSTIC_BASE = "fc988f19bf59cd30ae167c9447875def58804975"
DIAGNOSTIC_REF = "refs/heads/diagnostics/dart-snapshot-native"
DIAGNOSTIC_FILES = {
    ".github/workflows/qualify-dart-snapshot-native.yml",
    ".github/qualification/dart-snapshot-native.py",
    ".github/qualification/dart-snapshot-product.patch.b64",
}
BASE = "a2c411b7b83ca233881ace0c5bf3bec0b9646e1d"
BASE_TREE = "782d78fff9fe3346fa7d161960a12a452cf51ff0"
CANDIDATE_TREE = "db05f952649f3a2ca52286634f12235d1bede41c"
DART_TREE = "4aca3ca3c8e903f1079d77e800b078a61e5ed8b8"
PATCH_SHA = "49ba840fecf59426cfe1c77671a1e21185c9cec6e2c9b9816b2f49ecc57ed434"
DART_VERSION = "3.13.4"
COMPONENT_OPEN_TEST = "snapshot refresh rejects a regular file in a directory component and can retry"
VERIFIER = ".github/qualification/dart-snapshot-native.py"
PRODUCT_FILES = {
    VERIFIER: "b894c0424964c4049366a6a3d7c3f10623082658e64a89f9e1c4346366bf8803",
    ".github/workflows/dart-release-windows.yml": "f8ff0b50d52203610c1dc1d5d8c03f002e8da34824d14ce61169259a50dd1fb4",
    ".github/workflows/test.yml": "077ec836bcf04f31131d2bb07811ca114e08692dfd88e491013c7b81166dbe2b",
    "packages/dart/bin/build.dart": "6459d88ca789c0839fa1250074d4527c80d577a732b8d868603a40b3437f771e",
    "packages/dart/bin/flutter.dart": "050468180d1d143d8b43a4a676c052fd02b2f8fabe10f8e5bbef393ab0360add",
    "packages/dart/lib/src/builder/artifact_publish.dart": "081b7122a34a0d29eab7391c73b050d5678c0c9c769e0ad6269ea22b5549b2fa",
    "packages/dart/lib/src/builder/selection_build.dart": "01f27d83af297789b41af6af955c66abba99d2f4d89792cd75f279a38b05bc6f",
    "packages/dart/lib/src/builder/snapshot_directory.dart": "50370422d15e1696661b0a0f69f52ae7079bf95d33c5dfbc721bc259a6a4cfda",
    "packages/dart/lib/src/builder/snapshot_posix.dart": "963467186dfadfb56a30376481c182ae141809322b78587de1744ee555bff5fb",
    "packages/dart/lib/src/builder/snapshot_windows.dart": "339a85a18eb62092ed4126d75aad294a4d7882f34e737914e4ca4ea5df098344",
    "packages/dart/test/select_fixture.dart": "8928cc9983c72cd48743b42fd61e1154fddc7a9b5c12524177658d0ae8b75a3f",
    "packages/dart/test/selection_build_test.dart": "4af911ea49c7a2ec44344f9decc06c72c20807039ef6747cceedeb7095986506",
}


def git(repo, *args):
    return subprocess.check_output(["git", *args], cwd=repo, text=True).strip()


def require(condition, message):
    if not condition:
        raise SystemExit(message)


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def verify_diagnostic_checkout(repo):
    head = git(repo, "rev-parse", "HEAD")
    require(os.environ.get("GITHUB_EVENT_NAME") == "push" and os.environ.get("GITHUB_REF") == DIAGNOSTIC_REF,
            "expected the dedicated diagnostic push")
    require(head == os.environ.get("GITHUB_SHA"), "unexpected diagnostic workflow head")
    require(git(repo, "rev-list", "--parents", "--max-count=1", "HEAD") == f"{head} {DIAGNOSTIC_BASE}",
            "the diagnostic head must extend the pinned Windows cleanup run")
    changed = set(git(repo, "diff", "--name-only", DIAGNOSTIC_BASE, head).splitlines())
    require(changed == DIAGNOSTIC_FILES, "unexpected diagnostic publication paths")
    subprocess.run(["git", "diff", "--quiet", "HEAD", "--"], cwd=repo, check=True)


def extract_candidate(archive_path, source, extras):
    parents = {"packages/", ".github/", ".github/qualification/", ".github/workflows/"}
    seen = set()
    with zipfile.ZipFile(archive_path) as archive:
        for entry in archive.infolist():
            name = entry.filename
            path = PurePosixPath(name)
            require(not path.is_absolute() and ".." not in path.parts and "\\" not in name and ":" not in name
                    and not stat.S_ISLNK(entry.external_attr >> 16) and name.casefold() not in seen
                    and (path.parts[:2] == ("packages", "dart") or name in extras or name in parents),
                    f"unexpected archive entry: {name}")
            seen.add(name.casefold())
        archive.extractall(source)


def restore(repo, scratch):
    require(git(repo, "rev-parse", f"{BASE}^{{tree}}") == BASE_TREE, "base tree changed")
    payload = Path(__file__).with_name("dart-snapshot-product.patch.b64")
    patch = scratch / "product.patch"
    patch.write_bytes(base64.b64decode(b"".join(payload.read_bytes().splitlines()), validate=True))
    require(digest(patch) == PATCH_SHA, "product patch digest changed")

    extras = sorted(path for path in PRODUCT_FILES if path.startswith(".github/"))
    archive_path = scratch / "source.zip"
    # TemporaryDirectory removes Git's read-only loose objects on Windows.
    with tempfile.TemporaryDirectory(prefix="git-", dir=scratch) as directory:
        store = Path(directory)
        git(repo, "init", "--quiet", "--bare", str(store))
        objects = git(repo, "rev-parse", "--path-format=absolute", "--git-path", "objects")
        (store / "objects/info/alternates").write_bytes((Path(objects).as_posix() + "\n").encode("utf-8"))
        binding = ("--git-dir", str(store))
        git(repo, *binding, "read-tree", BASE)
        git(repo, *binding, "apply", "--cached", "--whitespace=error", str(patch))
        actual = git(repo, *binding, "write-tree")
        require(actual == CANDIDATE_TREE, "candidate tree changed")
        require(git(repo, *binding, "rev-parse", f"{actual}:packages/dart") == DART_TREE, "Dart subtree changed")
        changed = set(git(repo, *binding, "diff", "--cached", "--name-only", BASE).splitlines())
        require(changed == PRODUCT_FILES.keys(), "unexpected product paths")
        git(repo, *binding, "apply", "--cached", "--reverse", "--whitespace=error", str(patch))
        require(git(repo, *binding, "write-tree") == BASE_TREE, "reverse projection failed")
        git(repo, "-c", "core.autocrlf=false", "-c", "core.eol=lf", *binding,
            "archive", "--format=zip", "-o", str(archive_path), actual, "--", "packages/dart", *extras)
    source = scratch / "src"
    extract_candidate(archive_path, source, extras)
    require(all(digest(source / path) == sha for path, sha in PRODUCT_FILES.items()), "extracted product bytes changed")
    return source


def installation(dart, package, evidence):
    with (evidence / "dependencies.log").open("wb") as out, (evidence / "dependencies.stderr").open("wb") as err:
        subprocess.run([dart, "pub", "get"], cwd=package, stdout=out, stderr=err, timeout=240, check=True)
    shutil.copyfile(package / "pubspec.lock", evidence / "pubspec.lock")


def component_open_route(native, coverage):
    tracked = {
        "selection_build.dart": (83, 85, 124),
        "snapshot_directory.dart": (126, 193, 198),
        "snapshot_windows.dart": (71, 78, 79, 80, 102, 103, 106),
        "artifact_publish.dart": (558, 573, 574, 575, 577),
    }
    hits = {name: native["line_hits"](coverage, name, lines) for name, lines in tracked.items()}
    for name, required in {
        "selection_build.dart": (83, 85, 124),
        "snapshot_directory.dart": (126, 193, 198),
        "snapshot_windows.dart": (71, 78, 106),
        "artifact_publish.dart": (558, 574, 577),
    }.items():
        require(all(hits[name][line] > 0 for line in required), f"missing Windows component route in {name}: {hits[name]}")
    require(not (hits["snapshot_windows.dart"][102] or hits["snapshot_windows.dart"][103]),
            "regular-file test entered the junction attributes rejection")
    reports = list(coverage.rglob("*.vm.json"))
    require(len(reports) == 1, "expected one isolated component-open coverage report")
    files = json.loads(reports[0].read_text(encoding="utf-8"))["coverage"]
    source = "package:luxo_client/src/builder/artifact_publish.dart"
    entries = [entry for entry in files if entry["source"] == source]
    require(len(entries) == 1, "expected one native open coverage entry")
    branches = dict(zip(entries[0]["branchHits"][::2], entries[0]["branchHits"][1::2]))
    require(branches.get(573, 0) > 0, "native Windows NTSTATUS rejection branch was not hit")
    return {"lineHits": hits, "branchHits": {"artifact_publish.dart": {573: branches[573]}}}


def verify_windows_case(source, root, evidence, native, dart, name):
    before = set(root.glob("dart-select-build-*"))
    evidence.mkdir()
    done, messages, coverage = native["run_test"](source, evidence, dart, name)
    require(set(root.glob("dart-select-build-*")) == before, f"Dart snapshot fixture leaked: {name}")
    require(len(list(coverage.rglob("*.vm.json"))) == 1, f"expected isolated VM coverage for {name}")
    return done, messages, coverage


def qualify(source, root, evidence, native, expected):
    host = {"linux": "Linux", "windows": "Windows"}.get(expected)
    require(host and platform.system() == host and platform.machine().lower() in {"amd64", "x86_64"},
            "expected a native Linux or Windows x64 runner")
    require(os.environ.get("RUNNER_ENVIRONMENT") == "github-hosted", "expected a GitHub-hosted runner")
    dart = shutil.which("dart.exe" if host == "Windows" else "dart")
    require(dart, "native Dart executable is missing")
    version = native["output"]([dart, "--version"])
    require(version.startswith(f"Dart SDK version: {DART_VERSION} (stable) ") and
            version.split()[-1].strip('"') == f"{expected}_x64", f"unexpected Dart SDK: {version}")
    installation(dart, source / "packages/dart", evidence)

    name = native["TESTS"][host]
    additional = {}
    if host == "Linux":
        require(native["FIXTURE_ENV"] not in os.environ, "diagnostic must own the Linux mount and teardown")
        with native["split_device_fixture"](root, True) as (fixture, mounted):
            require(mounted, "diagnostic did not create the dedicated tmpfs")
            identity = native["split_device_identity"](root, fixture)
            done, _, coverage = native["run_test"](source, evidence, dart, name, fixture)
            require(native["split_device_identity"](root, fixture) == identity, "split-device fixture state changed")
            hits = native["line_hits"](coverage, "snapshot_directory.dart", (48, 49))
            require(any(hits.values()), f"missing physical different-volume rejection: {hits}")
        outcome = {"boundary": "different-volume", "lineHits": hits, "directoryIdentity": identity,
                   "fixtureCleanup": "unmounted-and-removed"}
    else:
        attributes = evidence / "attributes"
        done, messages, coverage = verify_windows_case(source, root, attributes, native, dart, name)
        outcome = native["windows_rejection"](messages, coverage)
        require(outcome["reparseAttributesVerified"], "the Windows junction did not hit the attributes rejection")
        component = evidence / "component-open"
        component_done, _, component_coverage = verify_windows_case(
            source, root, component, native, dart, COMPONENT_OPEN_TEST)
        route = component_open_route(native, component_coverage)
        additional["componentOpen"] = {
            "test": COMPONENT_OPEN_TEST, "event": component_done, "boundary": "component-open",
            "nativeErrorAssertedByPinnedTest": {
                "type": "FileSystemException", "code": 267, "component": "assets",
                "diagnostic": "Cannot prepare output component",
            },
            "bytePreservationAndNormalRetryAssertedByPinnedTest": True,
            "fixtureCleanup": "removed",
            **route,
        }

    require(list(coverage.rglob("*.vm.json")), "Dart VM coverage reports are missing")
    require(all(digest(source / path) == sha for path, sha in PRODUCT_FILES.items()), "product source changed during the run")
    return {"dart": version, "test": name, "event": done, "outcome": outcome, **additional}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--plan-only", action="store_true")
    args = parser.parse_args()
    repo = Path(git(Path.cwd(), "rev-parse", "--show-toplevel")).resolve()
    if not args.plan_only:
        verify_diagnostic_checkout(repo)
    root = repo / ".tmp"
    require(not root.is_symlink(), "qualification scratch cannot be a symlink")
    root.mkdir(exist_ok=True)
    require(root.resolve() == root, "qualification scratch must stay at checkout .tmp")
    scratch = Path(tempfile.mkdtemp(prefix="dart-snapshot-boundary-", dir=root))
    source_root = scratch / "src/.tmp"
    try:
        source = restore(repo, scratch)
        native = runpy.run_path(str(source / VERIFIER))
        provenance = {"base": BASE, "baseTree": BASE_TREE, "candidateTree": CANDIDATE_TREE, "dartTree": DART_TREE,
                      "candidatePatchSHA256": PATCH_SHA, "productFileSHA256": PRODUCT_FILES,
                      "diagnosticBase": DIAGNOSTIC_BASE, "diagnosticHead": git(repo, "rev-parse", "HEAD"),
                      "runner": platform.platform(), "run": os.environ.get("GITHUB_RUN_ID"),
                      "attempt": os.environ.get("GITHUB_RUN_ATTEMPT")}
        if args.plan_only:
            print(json.dumps(provenance, indent=2, sort_keys=True))
            return

        signal.signal(signal.SIGTERM, native["stop"])
        evidence = root / "dart-snapshot-native-evidence"
        evidence.mkdir()
        (evidence / "source.json").write_bytes((json.dumps(provenance, indent=2, sort_keys=True) + "\n").encode("utf-8"))
        source_root.mkdir()
        verdict = qualify(source, source_root, evidence, native, os.environ.get("DART_NATIVE_PLATFORM"))
    finally:
        require(not list(source_root.glob("dart-split-device-*")), "fixture must be unmounted before scratch cleanup")
        shutil.rmtree(scratch)

    native["finish"](evidence, {**provenance, **verdict, "ownedScratchCleaned": not scratch.exists()})


if __name__ == "__main__":
    main()
