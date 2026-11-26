#!/usr/bin/env python3
"""Run the original readiness sampler on a verified hosted ARM64 machine."""
import argparse
import base64
import gzip
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import re
import runpy
import shutil
import signal
import subprocess
import tempfile
import time

HERE = Path(__file__).resolve().parent
PAYLOAD = HERE / ".payload"
INPUTS = {
    "gateway.go.txt", "readiness.go.txt", "readiness-final.go.txt", "native.py", "sampler.py",
    "prior.patch.gz.b64", "final.patch.gz.b64",
}
BASE = "e101cf7699acb358305fd373934e8813a98087bd"
BASE_TREE = "377fdb5bbe6815a45930c53e10797f08cf07c917"
PRIOR = "ffd90b349d9d3a116af737d0caae1d24749d24a2"
FINAL = "4090c9136f4895857305f2ec1fc20e8d88aba546"
PATCHES = {
    PRIOR: ("prior.patch", "7e00b6603dbfcbd9d2a9c7870780a088cba7aadf9af9335aadb80f73b0181757"),
    FINAL: ("final.patch", "5f6b00529702d1ec685b0956006dde3af885c4c777eb9babc7593b3ee931b7f8"),
}
CPU = "Neoverse-N2"
PERF_VERSION = "v0.0.0-20260825160852-19be9d8e6c70"
PERF_SUM = "h1:M+kM03JKR23KkrutTue5KEQYDSbZTZTJ4FJ1v5zYrHM="


def sha(data):
    return hashlib.sha256(data).hexdigest()


def verify_payload():
    found = {}
    for line in (PAYLOAD / "inputs.sha256").read_text().splitlines():
        digest, path = line.split("  ", 1)
        if path not in INPUTS or path in found or not re.fullmatch(r"[0-9a-f]{64}", digest):
            raise RuntimeError("unexpected readiness payload entry")
        source = PAYLOAD / path
        if not source.is_file() or source.is_symlink() or sha(source.read_bytes()) != digest:
            raise RuntimeError(f"payload differs: {path}")
        found[path] = digest
    if found.keys() != INPUTS:
        raise RuntimeError("incomplete readiness payload")
    for name, digest in PATCHES.values():
        if sha(product_patch(name)) != digest:
            raise RuntimeError(f"unexpected original product patch: {name}")
    return found


def product_patch(name):
    data = (PAYLOAD / (name + ".gz.b64")).read_bytes()
    return gzip.decompress(base64.b64decode(b"".join(data.split()), validate=True))


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def run(*command, cwd=HERE, env=None):
    return subprocess.check_output(command, cwd=cwd, env=env)


def host_identity():
    values = json.loads(run("lscpu", "-J"))
    labels = {v["field"].strip().rstrip(":"): v.get("data") for v in values["lscpu"]}
    affinity = sorted(os.sched_getaffinity(0))
    if labels.get("Architecture") != "aarch64" or labels.get("Model name") != CPU or len(affinity) < 2:
        raise RuntimeError(f"unexpected hosted CPU or capacity: {labels.get('Model name')}, {affinity}")
    return {
        "model": CPU, "affinity": affinity, "os": platform.platform(),
        "imageVersion": os.environ.get("ImageVersion"), "lscpu": values,
    }


def admit(evidence, lane, affinity):
    observations = []
    for attempt in range(13):
        instance = host_identity()
        if instance["affinity"] != affinity:
            raise RuntimeError("runner CPU affinity changed during preparation")
        load = os.getloadavg()
        observations.append({"unixTime": time.time(), "load": load, "affinity": affinity})
        write_json(evidence / f"{lane}-admission.json", observations)
        if load[0] <= 0.5 * len(affinity):
            return
        if attempt < 12:
            time.sleep(10)
    raise RuntimeError(f"no exclusive quiet window for {lane}")


def parse(raw, names):
    lines = raw.decode().splitlines()
    headers = ["goos: linux", "goarch: arm64", "pkg: github.com/light-speak/luxo/pkg/lux/luvia", "PASS"]
    if any(lines.count(header) != 1 for header in headers):
        raise ValueError(f"benchmark process metadata or PASS missing: {lines}")
    labels = [line.removeprefix("cpu: ").strip() for line in lines if line.startswith("cpu: ")]
    if len(labels) > 1 or labels and CPU not in labels[0]:
        raise ValueError(f"unexpected CPU label in native Go output: {labels}")
    rows = {}
    for line in lines:
        if not line.startswith("Benchmark"):
            continue
        name, iterations, ns, time_unit, size, size_unit, alloc, alloc_unit = line.split()
        values = float(ns), int(size), int(alloc)
        if (name in rows or int(iterations) <= 0 or not all(math.isfinite(x) and x >= 0 for x in values)
                or (time_unit, size_unit, alloc_unit) != ("ns/op", "B/op", "allocs/op")):
            raise ValueError(f"invalid measurement {line}")
        rows[name] = values
    if rows.keys() != names:
        raise ValueError(f"benchmark inventory differs: {rows.keys()}")
    return CPU, rows


def install_benchstat(root, env):
    module = f"golang.org/x/perf@{PERF_VERSION}"
    pkg = f"golang.org/x/perf/cmd/benchstat@{PERF_VERSION}"
    info = json.loads(run("go", "mod", "download", "-json", module, cwd=root, env=env))
    if info["Version"] != PERF_VERSION or info["Sum"] != PERF_SUM:
        raise RuntimeError("benchstat module identity changed")
    run("go", "install", "-trimpath", pkg, cwd=root, env=env | {"GOBIN": str(root)})
    binary = root / "benchstat"
    metadata = run("go", "version", "-m", binary, cwd=root, env=env).decode()
    if f"mod\tgolang.org/x/perf\t{PERF_VERSION}\t{PERF_SUM}" not in metadata:
        raise RuntimeError("compiled benchstat lost the required version and module sum")
    return binary, metadata


def readiness_graph(source, tree, env):
    workspace = env | {"GOWORK": str(source / "go.work")}
    query = ("go", "list", "-mod=readonly", "-p=1", "-deps",
             "-f", "{{.ImportPath}}|{{with .Module}}{{.Path}}@{{.Version}}{{end}}")
    graph = run(*query, "-test", "./pkg/lux/luvia", cwd=source, env=workspace)
    if tree == FINAL:
        graph += run(*query, "./scripts/benchrun", "./scripts/benchplan", "./scripts/benchgate",
                     cwd=source, env=workspace)
    return sorted(set(graph.decode().splitlines()))


def archive_graph(namespace, git, tree, source, env):
    namespace["archive"](git, tree, source, env)
    package = source / "pkg/lux/luvia"
    fixtures = [("gateway.go.txt", "readiness_audit_gateway_bench_test.go")]
    if tree != BASE_TREE:
        arm = "head" if tree == FINAL else "base"
        fixtures.append((namespace["probe_fixture"](arm), "readiness_audit_probe_bench_test.go"))
    for name, target in fixtures:
        destination = package / target
        if destination.exists():
            raise RuntimeError(f"tracked path overlaps the readiness fixture: {target}")
        shutil.copyfile(PAYLOAD / name, destination)
    return readiness_graph(source, tree, env)


def prime_modules(repo, root, env, namespace, evidence):
    if run("git", "rev-parse", "--verify", BASE + "^{tree}", cwd=repo).decode().strip() != BASE_TREE:
        raise RuntimeError("unexpected historical baseline tree")
    subprocess.run(["git", "merge-base", "--is-ancestor", BASE, "HEAD"], cwd=repo, check=True)
    offline = env | {"GOPROXY": "off", "GOSUMDB": "off"}
    verified = {}
    with tempfile.TemporaryDirectory(dir=root, prefix="module-source-") as temporary:
        scratch = Path(temporary)
        index = scratch / "index.git"
        run("git", "init", "--bare", "--quiet", index)
        (index / "objects/info/alternates").write_text(str(namespace["INDEX"] / "objects") + "\n")
        git = ("git", f"--git-dir={index}")
        for label, tree in (("base", BASE_TREE), ("prior", PRIOR), ("final", FINAL)):
            namespace["bind_tree"](git, tree)
            source = scratch / label
            online_graph = archive_graph(namespace, git, tree, source, env)
            workspace = env | {"GOWORK": str(source / "go.work")}
            check = run("go", "mod", "verify", cwd=source, env=workspace).decode().strip()
            if check != "all modules verified":
                raise RuntimeError(f"module verification failed for {label}: {check}")

            # Resolve again from a fresh archive so online workspace sums cannot
            # hide inputs missing from the later offline compilation.
            shutil.rmtree(source)
            offline_graph = archive_graph(namespace, git, tree, source, offline)
            if not offline_graph or offline_graph != online_graph:
                raise RuntimeError(f"offline dependency graph differs for {label}")
            shutil.rmtree(source)
            package_modules = (line.partition("|")[2] for line in offline_graph)
            external = sorted({module for module in package_modules if module and not module.endswith("@")})
            verified[label] = {"tree": tree, "packages": len(offline_graph), "modules": external,
                               "graphSHA256": sha("\n".join(offline_graph).encode()), "verify": check}
            write_json(evidence / "module-graph.json", verified)


def replay(repo, root, evidence, inputs):
    host = host_identity()
    write_json(evidence / "host.json", host)
    for name in ("gateway.go.txt", "readiness.go.txt", "readiness-final.go.txt"):
        shutil.copyfile(PAYLOAD / name, root / name)
    for name, _ in PATCHES.values():
        (root / name).write_bytes(product_patch(name))
    shutil.copyfile(PAYLOAD / "native.py", root / "run.py")
    namespace = runpy.run_path(str(root / "run.py"))["prepare"].__globals__
    if (namespace["BASE"], namespace["BASE_TREE"], namespace["EQUIVALENT"], namespace["FINAL"]) != (BASE, BASE_TREE, PRIOR, FINAL):
        raise RuntimeError("original harness source binding changed")
    git_dir = Path(run("git", "rev-parse", "--absolute-git-dir", cwd=repo).decode().strip())
    env = namespace["ENV"] | {
        "GOCACHE": str(root / "cache"),
        "GOMODCACHE": str(root / "modules"), "GOPATH": str(root / "gopath"),
        "GOENV": "off", "GOBIN": str(root), "TMPDIR": str(root),
        "GOARM64": "v8.0", "GOSUMDB": "sum.golang.org", "GOPROXY": "https://proxy.golang.org",
    }

    def toolchain():
        actual = run("go", "env", "GOVERSION", "GOHOSTOS", "GOHOSTARCH", cwd=root, env=env).decode().splitlines()
        if actual != ["go1.27.1", "linux", "arm64"]:
            raise RuntimeError(f"unexpected readiness toolchain {actual}")
        return actual

    write_json(evidence / "inputs.json", {
        "head": run("git", "rev-parse", "HEAD", cwd=repo).decode().strip(), "go": toolchain(),
        "nativeGoEnv": json.loads(run("go", "env", "-json", "CGO_ENABLED", "GOARM64", cwd=root, env=env)),
        "base": BASE, "trees": {"base": BASE_TREE, "prior": PRIOR, "final": FINAL},
        "payload": inputs, "sourcePatches": PATCHES, "controllerSHA256": sha(Path(__file__).read_bytes()),
    })
    namespace.update({"ARTIFACTS": root, "INDEX": git_dir, "PATCHES": PATCHES})
    prime_modules(repo, root, env, namespace, evidence)
    benchstat, metadata = install_benchstat(root, env)
    (evidence / "benchstat-version.txt").write_text(metadata)
    original_sample = namespace["sample"]

    def sample(*args):
        if host_identity()["affinity"] != host["affinity"]:
            raise RuntimeError("benchmark child changed runner CPU affinity")
        return original_sample(*args)

    namespace.update({
        "SAMPLER": PAYLOAD / "sampler.py", "BENCHSTAT": benchstat,
        "BENCHSTAT_SHA": sha(benchstat.read_bytes()), "toolchain": toolchain, "parse": parse, "sample": sample,
        "ENV": env | {"GOPROXY": "off", "GOSUMDB": "off"},
    })
    namespace["prepare"]()
    prepared = root / "prepared"
    for name in ("preparation.json", "approved-costs.json"):
        shutil.copyfile(prepared / name, evidence / name)
    for match in prepared.glob("*/*.smoke.txt"):
        shutil.copyfile(match, evidence / f"{match.parent.name}-{match.name}")
    failures = {}
    for lane in ("ordinary", "probes"):
        try:
            admit(evidence, lane, host["affinity"])
            namespace["compare"](lane)
        except (AssertionError, OSError, RuntimeError, subprocess.SubprocessError, ValueError) as err:
            failures[lane] = str(err)
        finally:
            directory = root / f"timing-{lane}"
            if directory.is_dir():
                directory.rename(evidence / directory.name)
    after = host_identity()
    write_json(evidence / "host-after.json", after)
    if after["affinity"] != host["affinity"]:
        failures["host"] = "runner CPU affinity changed before terminal observation"
    write_json(evidence / "result.json", {"passed": not failures, "failures": failures})
    if failures:
        raise RuntimeError(f"readiness qualification failed in {sorted(failures)}")


def job_paths():
    expected = {
        "GITHUB_ACTIONS": "true", "GITHUB_REPOSITORY": "light-speak/luxo",
        "GITHUB_REF": "refs/heads/diagnostics/hosted-readiness-e101",
        "RUNNER_ENVIRONMENT": "github-hosted", "RUNNER_OS": "Linux", "RUNNER_ARCH": "ARM64",
    }
    if platform.system() != "Linux" or platform.machine() != "aarch64" or any(
        os.environ.get(k) != v for k, v in expected.items()
    ):
        raise RuntimeError("expected the dedicated GitHub-hosted Linux ARM64 readiness job")
    repo = Path(os.environ["GITHUB_WORKSPACE"]).resolve(strict=True)
    if HERE != repo / "scripts/diagnostics/readiness-hosted":
        raise RuntimeError("controller is outside its diagnostic checkout")
    tmp = repo / ".tmp"
    evidence = tmp / "readiness-hosted-evidence"
    if tmp.is_symlink() or evidence.is_symlink():
        raise RuntimeError("task scratch must be local to the diagnostic checkout")
    tmp.mkdir(exist_ok=True)
    return repo, tmp, evidence


def interrupted(_signal, _frame):
    raise KeyboardInterrupt


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("check", "run", "clean"))
    args = parser.parse_args()
    if args.mode == "check":
        print(json.dumps(verify_payload(), sort_keys=True))
        return
    repo, tmp, evidence = job_paths()
    if args.mode == "clean":
        if evidence.is_dir():
            shutil.rmtree(evidence)
        return
    os.umask(0o077)
    signal.signal(signal.SIGTERM, interrupted)
    evidence.mkdir()
    with tempfile.TemporaryDirectory(dir=tmp, prefix="readiness-hosted-run-") as temporary:
        replay(repo, Path(temporary), evidence, verify_payload())


if __name__ == "__main__":
    main()
