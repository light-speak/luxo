#!/usr/bin/env python3
"""Prepare an exact-tree native readiness benchmark and replay its paired lanes."""
import argparse
import hashlib
import importlib.util
import json
import math
import os
from pathlib import Path
import shutil
import subprocess
import sys
from tempfile import TemporaryDirectory

sys.dont_write_bytecode = True

ROOT = Path(__file__).resolve().parent
PREP = ROOT / "prepared"
ARTIFACTS = ROOT.parent
INDEX = ARTIFACTS / "readiness-7ba.Yduv1mND/index.git"
SAMPLER = ARTIFACTS / "pr73-deploy-pairs-3bb.2dXtAO4J/deployment-pairs.py"
BENCHSTAT = ROOT.parents[2] / ".tmp/pr64-perf.Y5HLWX4K/benchstat"
BASE = "e101cf7699acb358305fd373934e8813a98087bd"
BASE_TREE = "377fdb5bbe6815a45930c53e10797f08cf07c917"
EQUIVALENT = "ffd90b349d9d3a116af737d0caae1d24749d24a2"
FINAL = "bc832b5339d797ec1152fbbecc44d7c9a85ad5ec"
BENCHSTAT_SHA = "b5004127165b217b1e9393aadf91fce40d1ae29c0b56e71bc3d897fbdfbf9697"
PATCHES = {
    EQUIVALENT: ("readiness-7ba.Yduv1mND/combined.patch",
                 "7e00b6603dbfcbd9d2a9c7870780a088cba7aadf9af9335aadb80f73b0181757"),
    FINAL: ("readiness-optimized-hosted.nU4rhQsa/stateless-product-e101.patch",
            "18fb02f3dc31c90346073fd04cf5158fed41792689e9b887ead5b62d3c243f45"),
}
PACKAGE = "pkg/lux/luvia"
BENCH_BINARY = "pkg-lux-luvia.test"
COMMON = {"BenchmarkAuditGateway" + name + "/" + engine + "-2"
          for name in ("FirstHealth", "WarmUnary") for engine in ("standard", "luxo")}
PROBES = {"BenchmarkAuditGatewayReadiness/" + leaf + "-2"
          for leaf in ("zero-stateless", "one-cold", "one-cached", "eight-cold", "eight-cached", "eight-cached-503")}
LANES = {
    "ordinary": (BASE, BASE_TREE, FINAL, COMMON,
                 r"^(BenchmarkAuditGatewayFirstHealth|BenchmarkAuditGatewayWarmUnary)$/^(standard|luxo)$"),
    "probes": (EQUIVALENT, EQUIVALENT, FINAL, PROBES, r"^BenchmarkAuditGatewayReadiness$"),
}
ENV = {key: value for key, value in os.environ.items()
       if not key.startswith(("APP_", "LUXO_", "DATABASE_", "JWT_", "GIT_", "CGO_", "GO"))
       and key not in {"CORS_ORIGIN", "DEPLOY_MODE", "GRACEFUL_TIMEOUT", "INTROSPECTION_KEY", "GODEBUG",
                       "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"}}
# Keep the resolved Go cache configuration and the user's normal host environment.
ENV.update({key: os.environ[key] for key in ("GOPATH", "GOENV", "GOMODCACHE", "GOCACHE") if key in os.environ})
ENV.update(GOTOOLCHAIN="local", GOFLAGS="-p=1", GOWORK="off", GOMAXPROCS="2", GOPROXY="off", LC_ALL="C",
           GIT_CEILING_DIRECTORIES=str(ROOT))


def run(command, cwd=ROOT, env=ENV):
    done = subprocess.run(command, cwd=cwd, env=env, capture_output=True, timeout=120)
    if done.returncode:
        raise RuntimeError(f"{command[0]} exited {done.returncode} for {cwd}\n"
                           f"{done.stdout.decode()}\n{done.stderr.decode()}")
    return done.stdout


def sha(path):
    digest = hashlib.sha256()
    with path.open("rb") as content:
        for chunk in iter(lambda: content.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def toolchain():
    actual = run(["go", "env", "GOVERSION", "GOHOSTOS", "GOHOSTARCH"]).decode().splitlines()
    if actual != ["go1.27.1", "darwin", "arm64"]:
        raise RuntimeError(f"expected native Go 1.27.1 Darwin arm64, got {actual}")
    return actual


def bind_tree(git, expected):
    run([*git, "read-tree", BASE])
    patch = PATCHES.get(expected)
    if patch is not None:
        name, digest = patch
        path = ARTIFACTS / name
        if sha(path) != digest:
            raise RuntimeError(f"unexpected product patch digest {path.name}")
        run([*git, "apply", "--cached", "--whitespace=error", path])
    actual = run([*git, "write-tree"]).decode().strip()
    if actual != expected:
        raise RuntimeError(f"projection is {actual}, expected {expected}")


def archive(git, tree, target, env):
    target.mkdir()
    with subprocess.Popen([*git, "archive", tree], stdout=subprocess.PIPE, env=env) as source:
        assert source.stdout is not None
        subprocess.run(["tar", "-xf", "-", "-C", target], stdin=source.stdout, env=env, check=True, timeout=45)
        if source.wait(timeout=15):
            raise RuntimeError(f"archive failed for {tree}")
    run(["sh", target / "scripts/setup-modules.sh", target], env=env)
    (target / ".tmp").mkdir()


def parse(raw, names):
    lines = raw.decode().splitlines()
    want_headers = ["goos: darwin", "goarch: arm64", "pkg: github.com/light-speak/luxo/pkg/lux/luvia", "PASS"]
    if any(lines.count(header) != 1 for header in want_headers):
        raise ValueError(f"benchmark process metadata or PASS missing: {lines}")
    cpu, = [line.removeprefix("cpu: ").strip() for line in lines if line.startswith("cpu: ")]
    rows = {}
    for line in lines:
        if not line.startswith("Benchmark"):
            continue
        name, iterations, ns, time_unit, size, size_unit, alloc, alloc_unit = line.split()
        values = float(ns), int(size), int(alloc)
        if name in rows or int(iterations) <= 0 or not all(math.isfinite(x) and x >= 0 for x in values):
            raise ValueError(f"invalid measurement {line}")
        if (time_unit, size_unit, alloc_unit) != ("ns/op", "B/op", "allocs/op"):
            raise ValueError(f"invalid units in {line}")
        rows[name] = values
    if rows.keys() != names or not cpu:
        raise ValueError(f"inventory differs: {rows.keys()}, CPU {cpu!r}")
    return cpu, rows


def sample(stage, lane, arm, duration, env):
    _, _, _, names, selector = LANES[lane]
    binary = stage / lane / f"bin-{arm}" / BENCH_BINARY
    flags = ["-test.run=^$", f"-test.bench={selector}", "-test.benchmem", f"-test.benchtime={duration}",
             "-test.count=1", "-test.cpu=2", "-test.timeout=60s"]
    output = run([binary, *flags], cwd=stage / "run-directory", env=env)
    cpu, _ = parse(output, names)
    return output, cpu


def probe_fixture(arm):
    return "readiness-final.go.txt" if arm == "head" else "readiness.go.txt"


def build(stage, source, lane, arm, env):
    fixture_dir = source / PACKAGE
    fixture = fixture_dir / "readiness_audit_gateway_bench_test.go"
    if fixture.exists():
        raise RuntimeError("audit fixture path already exists")
    shutil.copyfile(ROOT / "gateway.go.txt", fixture)
    extra = fixture_dir / "readiness_audit_probe_bench_test.go"
    if lane == "probes":
        if extra.exists():
            raise RuntimeError("probe audit fixture path already exists")
        shutil.copyfile(ROOT / probe_fixture(arm), extra)
    target = stage / lane / f"bin-{arm}"
    action = "compile-performance-baseline" if arm == "base" else "compile-performance"
    try:
        run([stage / "tools/benchrun", action, source, target, PACKAGE, "default"], env=env)
    finally:
        fixture.unlink()
        if lane == "probes":
            extra.unlink()
    raw, cpu = sample(stage, lane, arm, "1x", env)
    (stage / lane / f"{arm}.smoke.txt").write_bytes(raw)
    return cpu


def prepare():
    if PREP.exists():
        raise FileExistsError(PREP)
    host = toolchain()
    if sha(BENCHSTAT) != BENCHSTAT_SHA:
        raise RuntimeError("benchmark statistics binary differs from prior local gate")
    with TemporaryDirectory(prefix="prepare-", dir=ROOT) as temporary:
        scratch = Path(temporary)
        (scratch / "tmp").mkdir()
        env = ENV | {"TMPDIR": str(scratch / "tmp")}
        stage = scratch / "prepared"
        for dir_name in ("tools", "run-directory", "ordinary", "probes"):
            (stage / dir_name).mkdir(parents=True)
        repository = scratch / "objects"
        run(["git", "init", "-q", repository])
        (repository / ".git/objects/info/alternates").write_text(str(INDEX.resolve() / "objects") + "\n")
        git = ("git", "-C", repository)
        stages = {}
        for tree in (BASE_TREE, EQUIVALENT, FINAL):
            bind_tree(git, tree)
            stages[tree] = scratch / tree
            archive(git, tree, stages[tree], env)
        tool_source = stages[FINAL]
        for name in ("benchrun", "benchplan", "benchgate"):
            run(["go", "build", "-trimpath", "-o", stage / "tools" / name, f"./scripts/{name}"], cwd=tool_source, env=env)
        shutil.copy2(BENCHSTAT, stage / "tools/benchstat")
        shutil.copyfile(tool_source / "scripts/benchgate/approved-costs.json", stage / "approved-costs.json")
        cpus = set()
        for lane, (_, left, right, _, _) in LANES.items():
            for arm, tree in (("base", left), ("head", right)):
                cpus.add(build(stage, stages[tree], lane, arm, env))
        if len(cpus) != 1:
            raise RuntimeError("smoke samples have different CPUs")
        native_profile = run([stage / "tools/benchrun", "performance-profile"]).decode().strip()
        files = [*stage.glob("*/bin-*/*.test"), *(stage / "tools").iterdir(), stage / "approved-costs.json"]
        manifest = {
            "source_trees": {"base": BASE_TREE, "prior_readiness": EQUIVALENT, "final": FINAL},
            "base_commit": BASE, "host": host, "cpu": cpus.pop(), "profile": native_profile,
            "fixtures": {name: sha(ROOT / name) for name in
                         ("gateway.go.txt", "readiness.go.txt", "readiness-final.go.txt", "run.py")},
            "sampler": sha(SAMPLER), "prepared": {str(path.relative_to(stage)): sha(path) for path in files},
            "inventory_only": True,
        }
        (stage / "preparation.json").write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
        stage.rename(PREP)
    print(PREP / "preparation.json")


def compare(lane):
    prep = json.loads((PREP / "preparation.json").read_text())
    if prep["host"] != toolchain() or sha(SAMPLER) != prep["sampler"]:
        raise RuntimeError("benchmark host/toolchain or historical sampler drifted")
    for path, expected in prep["fixtures"].items():
        if sha(ROOT / path) != expected:
            raise RuntimeError(f"benchmark fixture drift: {path}")
    for path, expected in prep["prepared"].items():
        if sha(PREP / path) != expected:
            raise RuntimeError(f"prepared binary or tool drift: {path}")
    spec = importlib.util.spec_from_file_location("deployment_pairs", SAMPLER)
    timer = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(timer)
    base, _, _, names, _ = LANES[lane]
    timer.ROOT, timer.WORK, timer.NAMES = PREP, PREP, names
    timer.parse_process = lambda raw: parse(raw, names)
    result = ROOT / f"timing-{lane}"
    result.mkdir()
    with TemporaryDirectory(prefix="timing-scratch-", dir=ROOT) as scratch:
        timer.ENV = ENV | {"TMPDIR": scratch}
        timer.bench = lambda arm, duration: sample(PREP, lane, arm, duration, timer.ENV)[0]
        (result / "host-before.txt").write_bytes(run(["uptime"]))
        controls = {
            "aa-base": timer.sample_group(result, "aa-base", ("base", "base"), 0, prep["cpu"]),
        }
        first = timer.sample_group(result, "first", ("base", "head"), 0, prep["cpu"])
        second = timer.sample_group(result, "second", ("base", "head"), 1, prep["cpu"])
        controls["aa-head"] = timer.sample_group(result, "aa-head", ("head", "head"), 1, prep["cpu"])
        checks = []
        for name, pair in controls.items():
            for direction, ordered in (("forward", pair), ("reverse", pair[::-1])):
                label = f"{name}-{direction}"
                checks.append(timer.gate(result, label, timer.stat_pair(result, label, *ordered)))
        scope = ["--approved-costs", PREP / "approved-costs.json", "--base-sha", base] if lane == "ordinary" else []
        checks.append(timer.gate(result, "comparison", *scope,
                                 timer.stat_pair(result, "first", *first), timer.stat_pair(result, "second", *second)))
        (result / "host-after.txt").write_bytes(run(["uptime"]))
        (result / "outcome.json").write_text(json.dumps({"valid": all(checks), "decisions": checks}, indent=2) + "\n")
        if not all(checks):
            raise RuntimeError(f"performance gate did not qualify {lane}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("prepare", "ordinary", "probes"))
    args = parser.parse_args()
    if args.action == "prepare":
        prepare()
    else:
        compare(args.action)
