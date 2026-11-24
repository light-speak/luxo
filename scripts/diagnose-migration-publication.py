#!/usr/bin/env python3
"""Replay the exact migration fixtures on the native runner."""

from collections import Counter
import base64
import csv
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tempfile

BASE = "32a10c114ac3c282b4ce46189b646d28eb767b1b"
BASE_TREE = "3ddf747cccd83c2110b3dbeb5ce6c099c4de7c8e"
PRODUCT_TREE = "4a07e8d512dc34cab9474e77da2e39cad2809060"
PRODUCT_PATCH_SHA256 = "e351028c357eda86f19cf35281b96aee1a0cf2c7bc913409b621f4fb53065aa9"
GO_VERSION = "go1.27.1"
BENCHSTAT = "golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70"
BENCHMARK = "BenchmarkCLIMigrationTransaction"
LEAVES = (
    "diff_small", "squash_small", "diff_large", "squash_large", "diff_linked", "squash_linked",
    "readonly_state", "readonly_parent", "squash_collision", "diff_unchanged", "invalid_name",
)
SAMPLES = 10
BENCHMARK_FLAGS = (
    "-test.run=^$", f"-test.bench=^{BENCHMARK}$", "-test.benchmem", "-test.benchtime=200ms",
    "-test.count=1", "-test.cpu=2",
)
EXPECTED = Counter(f"{BENCHMARK}/{leaf}-2" for leaf in LEAVES)
CLI = (
    "TestMigrationPublicationRecoversThroughDiff", "TestMigrationPublicationKeepsExistingSameContent",
    "TestMigrationPublicationResumesCommittedSquash", "TestMigrationPublicationPreservesGeneratedSQL",
    "TestMigrationConcurrentCommandsDoNotDuplicateSchema", "TestMigrationGenerationLockOwnerExit",
)
LINUX_CLI = (
    "TestMigrationPublicationStateRenameFailureKeepsSQL", "TestMigrationStatePreservesSymlinkTargets",
    "TestMigrationStateReadonlySymlinkParentPreservesProject", "TestMigrationStateWriteFailurePreservesProject",
)
VERIFY = (
    "TestVerifyDefersDuringMigrationPublication", "TestVerifySuccess", "TestVerifyPropagatesPendingErrors",
    "TestVerifyWithPendingMigrations",
)
PKG = "github.com/light-speak/luxo/cmd/luxo"


def read(*args, cwd, env=None):
    return subprocess.check_output(args, cwd=cwd, env=env, text=True).strip()


def log(args, cwd, path, env=None, check=True):
    with path.open("wb") as output:
        result = subprocess.run(args, cwd=cwd, env=env, stdout=output, stderr=subprocess.STDOUT, check=False)
    if check:
        result.check_returncode()
    return result.returncode


def checked_product(repo, evidence, scratch):
    data = base64.b64decode((repo / "scripts/diagnose-migration-publication/product.patch.b64").read_bytes())
    digest = hashlib.sha256(data).hexdigest()
    if digest != PRODUCT_PATCH_SHA256:
        raise ValueError(f"unexpected product patch: {digest}")
    patch = evidence / "product.patch"
    patch.write_bytes(data)
    store = scratch / "git-store"
    git_env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
    git_env["GIT_INDEX_FILE"] = str(scratch / "projection-index")
    subprocess.run(["git", "init", "--bare", "--object-format=sha1", str(store)], cwd=scratch, env=git_env, check=True)
    objects = Path(read("git", "rev-parse", "--path-format=absolute", "--git-path", "objects", cwd=repo))
    (store / "objects/info/alternates").write_bytes((objects.as_posix() + "\n").encode("utf-8"))
    git = ["git", f"--git-dir={store}", "-C", str(scratch)]
    if read(*git, "rev-parse", "--is-bare-repository", cwd=scratch, env=git_env) != "true":
        raise ValueError("release projection needs an isolated bare Git store")
    subprocess.run([*git, "read-tree", BASE], cwd=scratch, env=git_env, check=True)
    if read(*git, "write-tree", cwd=scratch, env=git_env) != BASE_TREE:
        raise ValueError("unexpected original tree in the isolated Git store")
    for mode in ("forward", "reverse"):
        flags = ["--reverse"] if mode == "reverse" else []
        subprocess.run([*git, "apply", "--cached", "--check", *flags, str(patch)], cwd=scratch, env=git_env, check=True)
        subprocess.run([*git, "apply", "--cached", *flags, str(patch)], cwd=scratch, env=git_env, check=True)
        expected = PRODUCT_TREE if mode == "forward" else BASE_TREE
        actual = read(*git, "write-tree", cwd=scratch, env=git_env)
        (evidence / f"{mode}-tree.txt").write_text(actual + "\n", encoding="utf-8")
        if actual != expected:
            raise ValueError(f"unexpected {mode} tree: {actual}")
    return git, git_env


def unpack(projection, revision, role, scratch):
    git, git_env = projection
    target = scratch / role
    target.mkdir()
    tarball = scratch / f"{role}.tar"
    subprocess.run([*git, "archive", f"--output={tarball}", revision], cwd=scratch, env=git_env, check=True)
    subprocess.run(["tar", "-xf", str(tarball), "-C", str(target)], cwd=scratch, check=True)
    tarball.unlink()
    return target


def workspace(target, native, evidence, role):
    env = dict(os.environ, GOWORK=str(target / "go.work"))
    if native == "linux":
        log(["sh", "scripts/setup-modules.sh", "."], target, evidence / f"workspace-{role}.txt", env)
    else:
        # The native Windows Go process needs an absolute Windows GOWORK path.
        version = "v" + (target / "VERSION").read_text(encoding="utf-8").strip()
        modules = ("cmd/luxo", "pkg/lux/pg", "pkg/lux/storage/s3", "pkg/lux/cache/redis", "pkg/lux/nats")
        command = ["go", "work", "edit", f"-replace=github.com/light-speak/luxo@{version}=."]
        for module in modules:
            command.extend((f"-use=./{module}", f"-replace=github.com/light-speak/luxo/{module}@{version}=./{module}"))
        log(["go", "work", "init", "."], target, evidence / f"workspace-{role}-init.txt", env)
        log(command, target, evidence / f"workspace-{role}.txt", env)
    shutil.copyfile(target / "go.work", evidence / f"workspace-{role}.work")
    return env


def test_suite(source, package, cases, path, env):
    pattern = "^(" + "|".join(cases) + ")$"
    log(["go", "test", "-json", "-count=1", "-p=2", "-run", pattern, package], source, path, env)
    events = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.startswith("{")]
    passed = {event.get("Test") for event in events if event.get("Action") == "pass"}
    if not set(cases).issubset(passed):
        raise ValueError(f"required tests did not pass in {path}: {set(cases) - passed}")


def validate_shot(raw, where):
    seen = Counter()
    metadata = {}
    for line in raw.splitlines():
        if not line.startswith("Benchmark"):
            for key in ("goos", "goarch", "pkg", "cpu"):
                if line.startswith(key + ": "):
                    metadata[key] = line.removeprefix(key + ": ")
            continue
        columns = line.split()
        if len(columns) != 8 or columns[3::2] != ["ns/op", "B/op", "allocs/op"]:
            raise ValueError(f"invalid benchmark metrics in {where}: {line!r}")
        if int(columns[1]) <= 0 or any(not re.fullmatch(r"[0-9]+(?:\.[0-9]+)?", value) for value in columns[2::2]):
            raise ValueError(f"invalid benchmark result in {where}: {line!r}")
        seen[columns[0]] += 1
    if seen != EXPECTED or metadata.get("pkg") != PKG or metadata.get("goos") != "linux" or metadata.get("goarch") != "amd64" or not metadata.get("cpu"):
        raise ValueError(f"different benchmark inventory or native metadata in {where}: {seen}, {metadata}")
    return metadata["cpu"]


def validate_comparison(path):
    expected = Counter((name.removeprefix("Benchmark"), unit) for name in EXPECTED for unit in ("sec/op", "B/op", "allocs/op"))
    seen = Counter()
    unit = None
    with path.open(encoding="utf-8", newline="") as file:
        for row in csv.reader(file):
            if len(row) < 7:
                continue
            if row[0] == "" and row[2] == "CI" and row[4] == "CI" and row[5] == "vs base":
                unit = row[1]
            elif row[0] and row[0] != "geomean":
                seen[(row[0], unit)] += 1
    if seen != expected:
        raise ValueError(f"incomplete benchmark comparison in {path}: {seen}")


def measure(label, arms, sources, binaries, evidence, benchstat):
    raw = evidence / label
    raw.mkdir()
    cpu = None
    for number in range(1, SAMPLES + 1):
        order = ("base", "head") if number % 2 else ("head", "base")
        for arm in order:
            source = arms[arm]
            shot = raw / f"{number:02}-{arm}.txt"
            log([str(binaries[source]), *BENCHMARK_FLAGS], sources[source] / "cmd/luxo", shot)
            observed = validate_shot(shot.read_text(encoding="utf-8"), shot)
            if cpu is not None and observed != cpu:
                raise ValueError(f"CPU changed within {label}: {cpu!r} → {observed!r}")
            cpu = observed
            with (raw / f"{arm}.txt").open("ab") as combined:
                combined.write(shot.read_bytes())
            with (raw / "order.tsv").open("a", encoding="utf-8") as ledger:
                ledger.write(f"{number}\t{arm}\t{source}\t{observed}\n")
    args = [str(benchstat), "-alpha", "0.01", "-filter", ".unit:(sec/op OR B/op OR allocs/op)", "-format", "csv", f"base={raw / 'base.txt'}", f"head={raw / 'head.txt'}"]
    comparison = evidence / f"{label}.csv"
    with comparison.open("wb") as output, (evidence / f"{label}-warnings.txt").open("wb") as warnings:
        subprocess.run(args, cwd=evidence, stdout=output, stderr=warnings, check=True)
    validate_comparison(comparison)
    return comparison, cpu


def qualify(repo, native, evidence, scratch):
    git = checked_product(repo, evidence, scratch)
    sources = {"head": unpack(git, PRODUCT_TREE, "head", scratch)}
    env = workspace(sources["head"], native, evidence, "head")
    cli = CLI + LINUX_CLI if native == "linux" else CLI
    test_suite(sources["head"], "./cmd/luxo", cli, evidence / "cli-tests.jsonl", env)
    test_suite(sources["head"], "./pkg/lux/pg/migrate", VERIFY, evidence / "pg-verify-tests.jsonl", env)
    if native != "linux":
        return
    if os.geteuid() == 0 or os.environ.get("LUXO_MIGRATION_STATE_BASELINE"):
        raise ValueError("permission cases require an unprivileged user and the default migration fixture")
    log(["lscpu"], repo, evidence / "lscpu.txt")
    sources["base"] = unpack(git, BASE, "base", scratch)
    fixture = Path("cmd/luxo/migrate_bench_test.go")
    if (sources["base"] / fixture).read_bytes() != (sources["head"] / fixture).read_bytes():
        raise ValueError("original CLI transaction benchmark fixture changed")
    binaries = {}
    for role, source in sources.items():
        build_env = env if role == "head" else workspace(source, native, evidence, role)
        binaries[role] = scratch / f"{role}.test"
        log(["go", "test", "-trimpath", "-c", "-o", str(binaries[role]), "./cmd/luxo"], source, evidence / f"compile-{role}.txt", build_env)
    baseline = sources["base"]
    tools_env = dict(os.environ, GOWORK="off", GOBIN=str(scratch))
    log(["go", "install", BENCHSTAT], baseline, evidence / "install-benchstat.txt", tools_env)
    benchstat = scratch / "benchstat"
    control, cpu_a = measure("control", {"base": "base", "head": "base"}, sources, binaries, evidence, benchstat)
    roles = {"base": "base", "head": "head"}
    primary, cpu_b = measure("primary", roles, sources, binaries, evidence, benchstat)
    confirmation, cpu_c = measure("confirmation", roles, sources, binaries, evidence, benchstat)
    if cpu_a != cpu_b or cpu_a != cpu_c:
        raise ValueError(f"CPU changed between groups: {cpu_a!r}, {cpu_b!r}, {cpu_c!r}")
    gate = ["go", "run", "./scripts/benchgate", "--approved-costs", "scripts/benchgate/approved-costs.json", "--base-sha", BASE]
    status = log([*gate, str(control)], baseline, evidence / "control-gate.txt", tools_env, check=False)
    (evidence / "control-status.txt").write_text(f"{status}\n", encoding="utf-8")
    log([*gate, str(primary), str(confirmation)], baseline, evidence / "gate.txt", tools_env)


def main(native):
    repo = Path(read("git", "rev-parse", "--show-toplevel", cwd=Path.cwd())).resolve()
    temporary = repo / ".tmp"
    temporary.mkdir(exist_ok=True)
    if temporary.is_symlink():
        raise ValueError("qualification needs an owned temporary directory")
    evidence = temporary / "migration-publication-native-evidence"
    evidence.mkdir()
    diagnostic = read("git", "rev-parse", "HEAD", cwd=repo)
    diagnostic_parent = read("git", "show", "-s", "--format=%P", "HEAD", cwd=repo)
    go = json.loads(read("go", "env", "-json", "GOOS", "GOARCH", "GOVERSION", "GOAMD64", "CGO_ENABLED", cwd=repo))
    receipt = dict(base=BASE, base_tree=BASE_TREE, product_tree=PRODUCT_TREE, product_patch_sha256=PRODUCT_PATCH_SHA256,
                   diagnostic=diagnostic, diagnostic_parent=diagnostic_parent, system=platform.platform(), go=go,
                   flags=BENCHMARK_FLAGS, leaves=LEAVES, samples_per_arm=SAMPLES)
    (evidence / "scope.json").write_text(json.dumps(receipt, indent=2) + "\n", encoding="utf-8")
    if (go["GOOS"], go["GOARCH"], go["GOVERSION"]) != (native, "amd64", GO_VERSION):
        raise ValueError(f"unexpected native Go environment: {go}")
    if diagnostic_parent != BASE or read("git", "rev-parse", BASE + "^{tree}", cwd=repo) != BASE_TREE:
        raise ValueError("diagnostic must be a single commit on the pinned base")
    if native == "windows":
        log(["pwsh", "-NoProfile", "-Command", "Get-CimInstance Win32_Processor | Select-Object Name,NumberOfCores | ConvertTo-Json"], repo, evidence / "windows-cpu.json")
    with tempfile.TemporaryDirectory(prefix="migration-publication-native-", dir=temporary) as scratch:
        qualify(repo, native, evidence, Path(scratch))


if __name__ == "__main__":
    if sys.argv[1:] not in (["linux"], ["windows"]):
        raise SystemExit("usage: diagnose-migration-publication.py {linux|windows}")
    main(sys.argv[1])
