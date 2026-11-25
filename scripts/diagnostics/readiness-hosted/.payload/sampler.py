"""Build, verify and measure the four PR 73 deployment generator samples."""
from hashlib import sha256
import json
import math
import os
from pathlib import Path
import re
import shutil
import statistics
import subprocess
import sys

ROOT = Path(__file__).resolve().parent
REPO = ROOT.parents[2]
WORK = ROOT / "build"
BASE = "3bb59ed140dfc959e4cb4419397a009536ea3f4c"
HEAD = "a53e376de76eb655a2f16527fa79ff1340dd9357"
PACKAGE = "pkg/codegen"
PROFILE = "default"
FILTER = r"^(BenchmarkGenerateComposeFiles|BenchmarkGenerateHelmFiles)$/^(1|16)$"
NAMES = {f"BenchmarkGenerate{target}Files/{size}-2" for target in ("Compose", "Helm") for size in (1, 16)}
ENV = os.environ | {
    "GOTOOLCHAIN": "local",
    "GOMAXPROCS": "2",
    "GOWORK": "off",
    "GOFLAGS": os.environ.get("GOFLAGS", "") + " -p=1",
    "LC_ALL": "C",
    "TMPDIR": str(WORK / "temp"),
    "GIT_CEILING_DIRECTORIES": str(ROOT),
}


def execute(args, cwd=ROOT, env=ENV, data=None):
    run = subprocess.run(args, cwd=cwd, env=env, input=data, capture_output=True)
    if run.returncode:
        raise RuntimeError(f"{args[0]} exited {run.returncode}\n{run.stdout.decode()}\n{run.stderr.decode()}")
    return run.stdout


def sha(path):
    with path.open("rb") as handle:
        return sha256(handle.read()).hexdigest()


def script(name):
    return str(WORK / "tools" / name)


def command(arm, action, *flags):
    return [script("benchrun"), action, str(WORK / arm), str(WORK / f"bin-{arm}"), PACKAGE, PROFILE, *flags]


def bench(arm, duration):
    return execute(command(arm, "run-performance", "-test.run=^$", f"-test.bench={FILTER}",
                           "-test.benchmem", f"-test.benchtime={duration}", "-test.count=1", "-test.cpu=2"))


def parse_process(text):
    lines = text.decode().splitlines()
    for value in ("goos: darwin", "goarch: arm64", "pkg: github.com/light-speak/luxo/pkg/codegen", "PASS"):
        assert lines.count(value) == 1, (value, lines)
    cpu, = [line.removeprefix("cpu: ") for line in lines if line.startswith("cpu: ")]
    metrics = {}
    for line in lines:
        if not line.startswith("Benchmark"):
            continue
        name, iterations, ns, nsunit, byte_count, byteunit, allocs, allocunit = line.split()
        values = [float(ns), int(byte_count), int(allocs)]
        assert name not in metrics and int(iterations) > 0, line
        assert (nsunit, byteunit, allocunit) == ("ns/op", "B/op", "allocs/op"), line
        assert all(math.isfinite(v) and v >= 0 for v in values), line
        metrics[name] = values
    assert metrics.keys() == NAMES, metrics
    return cpu, metrics


def pack_archive(rev, target):
    target.mkdir()
    with subprocess.Popen(["git", "archive", rev], cwd=REPO, stdout=subprocess.PIPE, env=ENV) as archive:
        subprocess.run(["tar", "-xf", "-", "-C", str(target)], stdin=archive.stdout, env=ENV, check=True)
        assert archive.wait() == 0, rev
    execute(["sh", str(target / "scripts/setup-modules.sh"), str(target)])
    (target / ".tmp").mkdir()
    shutil.copyfile(ROOT / "fixture.go.txt", target / PACKAGE / "deployment_audit_bench_test.go")


def prepare():
    (WORK / "temp").mkdir(parents=True)
    (WORK / "tools").mkdir()
    assert execute(["go", "env", "GOVERSION", "GOHOSTOS", "GOHOSTARCH"]).decode().splitlines() == [
        "go1.27.1", "darwin", "arm64",
    ]
    for arm, rev in (("base", BASE), ("head", HEAD)):
        pack_archive(rev, WORK / arm)
    for tool in ("benchrun", "benchplan", "benchgate"):
        execute(["go", "build", "-trimpath", "-p=1", "-o", script(tool), f"./scripts/{tool}"], cwd=WORK / "head")
    pinned = REPO / ".tmp/pr64-perf.Y5HLWX4K/benchstat"
    identity = execute(["go", "version", "-m", str(pinned)])
    assert b"v0.0.0-20260825160852-19be9d8e6c70" in identity
    shutil.copyfile(pinned, script("benchstat"))
    Path(script("benchstat")).chmod(0o700)
    (ROOT / "benchstat-version.txt").write_bytes(identity)
    cpus = set()
    binaries = {}
    for arm, action in (("base", "compile-performance-baseline"), ("head", "compile-performance")):
        execute(command(arm, action))
        binary = WORK / f"bin-{arm}" / "pkg-codegen.test"
        binaries[arm] = sha(binary)
        fixture_env = ENV | {"LUXO_DEPLOY_BENCHMARK_OUTPUT": str(WORK / "outputs" / arm)}
        execute(command(arm, "run-performance", "-test.run=^TestDeploymentBenchmarkAuditOutput$", "-test.count=1"),
                env=fixture_env)
        # Two one-iteration invocations of each same binary verify the A/A sample paths.
        for side in ("left", "right"):
            sample = bench(arm, "1x")
            cpu, _ = parse_process(sample)
            cpus.add(cpu)
            (ROOT / f"inventory-{arm}-{side}.txt").write_bytes(sample)
    assert len(cpus) == 1
    (ROOT / "output-equivalence.json").write_bytes(execute([sys.executable, "-B", "validate-output.py"]))
    manifest = {
        "base": BASE, "head": HEAD, "package": PACKAGE, "profile": PROFILE, "binary_sha256": binaries,
        "tools_sha256": {f.name: sha(f) for f in (WORK / "tools").iterdir()},
        "compile_profile": execute([script("benchrun"), "performance-profile"]).decode().strip(),
        "fixture_sha256": sha(ROOT / "fixture.go.txt"), "names": sorted(NAMES), "cpu": cpus.pop(),
        "inventory_only": True,
    }
    (ROOT / "preparation.json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(json.dumps(manifest, indent=2))


def stat_pair(result, name, left, right):
    target = result / f"{name}.csv"
    command = [script("benchstat"), "-alpha", "0.01", "-filter", ".unit:(sec/op OR B/op OR allocs/op)",
               "-format", "csv", f"base={left}", f"head={right}"]
    with target.open("wb") as out, (result / f"{name}.warnings").open("wb") as err:
        subprocess.run(command, stdout=out, stderr=err, env=ENV, check=True)
    return str(target)


def gate(result, name, *args):
    decision = subprocess.run([script("benchgate"), *args], env=ENV, capture_output=True)
    (result / f"{name}.gate.txt").write_bytes(decision.stdout + decision.stderr)
    if decision.returncode:
        print(decision.stdout.decode() + decision.stderr.decode(), file=sys.stderr)
    return decision.returncode == 0


def sample_group(result, group, arms, offset, cpu):
    raw = [result / f"{group}-{side}.raw.txt" for side in ("left", "right")]
    metrics = [[], []]
    with raw[0].open("wb") as left, raw[1].open("wb") as right:
        outs = (left, right)
        for index in range(10):
            for side in ((0, 1) if (index + offset) % 2 == 0 else (1, 0)):
                data = bench(arms[side], "200ms")
                observed, values = parse_process(data)
                assert observed == cpu, (observed, cpu)
                outs[side].write(data)
                metrics[side].append(values)
    deltas = {}
    for name in sorted(NAMES):
        pairs = [[b - a for a, b in zip(left[name], right[name])] for left, right in zip(*metrics)]
        deltas[name] = {unit: {"max": max(delta), "median": statistics.median(delta), "pairs": delta}
                        for unit, delta in zip(("ns/op", "B/op", "allocs/op"), zip(*pairs))}
    (result / f"{group}.paired.json").write_text(json.dumps(deltas, indent=2) + "\n")
    normalized = [p.with_suffix("").with_suffix(".txt") for p in raw]
    for source, out in zip(raw, normalized):
        execute([script("benchplan"), "--normalize", str(source), str(out)])
    return normalized


def timing():
    prep = json.loads((ROOT / "preparation.json").read_text())
    for role, digest in prep["binary_sha256"].items():
        assert sha(WORK / f"bin-{role}" / "pkg-codegen.test") == digest
    for name, digest in prep["tools_sha256"].items():
        assert sha(Path(script(name))) == digest
    result = ROOT / "timing"
    result.mkdir()
    (result / "environment.json").write_bytes(execute(["go", "env", "-json", "GOVERSION", "GOOS", "GOARCH", "CGO_ENABLED"]))
    (result / "host-before.txt").write_bytes(execute(["uptime"]))
    cpu = prep["cpu"]
    first_control = sample_group(result, "aa-base", ("base", "base"), 0, cpu)
    first = sample_group(result, "first", ("base", "head"), 0, cpu)
    second = sample_group(result, "second", ("base", "head"), 1, cpu)
    last_control = sample_group(result, "aa-head", ("head", "head"), 1, cpu)
    verdicts = []
    for name, pair in (("aa-base", first_control), ("aa-head", last_control)):
        for direction, inputs in (("forward", pair), ("reverse", pair[::-1])):
            slot = f"{name}-{direction}"
            verdicts.append(gate(result, slot, stat_pair(result, slot, *inputs)))
    verdicts.append(gate(result, "comparison", "--approved-costs",
                         str(WORK / "head/scripts/benchgate/approved-costs.json"), "--base-sha", BASE,
                         stat_pair(result, "first", *first), stat_pair(result, "second", *second)))
    (result / "host-after.txt").write_bytes(execute(["uptime"]))
    assert all(verdicts), "one or more performance gates failed"


if __name__ == "__main__":
    {"prepare": prepare, "timing": timing}[sys.argv[1]]()
