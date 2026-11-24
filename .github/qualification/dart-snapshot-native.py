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


DIAGNOSTIC_BASE = "307d602b6c0588d636eefb70c627cab7177784d8"
DIAGNOSTIC_REF = "refs/heads/diagnostics/dart-snapshot-native"
DIAGNOSTIC_FILES = {
    ".github/workflows/qualify-dart-snapshot-native.yml",
    ".github/qualification/dart-snapshot-native.py",
    ".github/qualification/dart-snapshot-product.patch.b64",
}
BASE = "dd6405a02c653c8f2da90ff8ea81795a6781033b"
BASE_TREE = "5cb2421d34fc1addd094a0a68ad6671414230c13"
CANDIDATE_TREE = "8fc81745ae2a70d1a07ace8f36f923cb3a732293"
DART_TREE = "222a4d8fdc8a567c2cbda2c5f422dd703ee1ce9b"
PATCH_SHA = "7f20d348a174125033eea6170138b328ff98d2640987be64a1ef0d9f66a87d84"
DART_VERSION = "3.13.4"
VERIFIER = ".github/qualification/dart-snapshot-native.py"
PRODUCT_FILES = {
    VERIFIER: "b894c0424964c4049366a6a3d7c3f10623082658e64a89f9e1c4346366bf8803",
    ".github/workflows/dart-release-windows.yml": "9b4c502a8798ba5a35e8dfe82805b0dd58c5fc3fda55c9d3585c41c00e1c22fa",
    ".github/workflows/test.yml": "7e186b43c3c1da1bcd630a89e87a14eeb2cb1186c96515d446e76776307da1a4",
    "packages/dart/bin/build.dart": "6459d88ca789c0839fa1250074d4527c80d577a732b8d868603a40b3437f771e",
    "packages/dart/bin/flutter.dart": "050468180d1d143d8b43a4a676c052fd02b2f8fabe10f8e5bbef393ab0360add",
    "packages/dart/lib/src/builder/artifact_publish.dart": "081b7122a34a0d29eab7391c73b050d5678c0c9c769e0ad6269ea22b5549b2fa",
    "packages/dart/lib/src/builder/selection_build.dart": "01f27d83af297789b41af6af955c66abba99d2f4d89792cd75f279a38b05bc6f",
    "packages/dart/lib/src/builder/snapshot_directory.dart": "50370422d15e1696661b0a0f69f52ae7079bf95d33c5dfbc721bc259a6a4cfda",
    "packages/dart/lib/src/builder/snapshot_posix.dart": "963467186dfadfb56a30376481c182ae141809322b78587de1744ee555bff5fb",
    "packages/dart/lib/src/builder/snapshot_windows.dart": "339a85a18eb62092ed4126d75aad294a4d7882f34e737914e4ca4ea5df098344",
    "packages/dart/test/select_fixture.dart": "8928cc9983c72cd48743b42fd61e1154fddc7a9b5c12524177658d0ae8b75a3f",
    "packages/dart/test/selection_build_test.dart": "c6994dfd6857298dd9d468e72f17b3692c30df4f5c04e7ede8d2b334300e15c2",
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
            "the diagnostic head must extend the pinned successful branch head")
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

    store = scratch / "git"
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

    extras = sorted(path for path in PRODUCT_FILES if path.startswith(".github/"))
    archive_path = scratch / "source.zip"
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
        before = set(root.glob("dart-select-build-*"))
        done, messages, coverage = native["run_test"](source, evidence, dart, name)
        require(set(root.glob("dart-select-build-*")) == before, "Dart snapshot fixture leaked")
        outcome = native["windows_rejection"](messages, coverage)

    require(list(coverage.rglob("*.vm.json")), "Dart VM coverage reports are missing")
    require(all(digest(source / path) == sha for path, sha in PRODUCT_FILES.items()), "product source changed during the run")
    return {"dart": version, "test": name, "event": done, "outcome": outcome}


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
