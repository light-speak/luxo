"""Run the independent statement-construction diagnostic on a matched native Linux runner."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import runpy
import subprocess
import tempfile
from zipfile import ZipFile

HERE = Path(__file__).resolve().parent
PARENT = "9f93dc8d1f8d51577aee22ce002a165765c5e2fb"
PARENT_TREE = "9f41729d49359a315efab2dffda02338e88a98ff"
PATHS = (
    ".github/workflows/diagnose-pg-intent-latency.yml",
    "scripts/diagnostics/pg-intent-latency/hosted.py",
    "scripts/diagnostics/pg-intent-latency/inputs.zip",
)
INPUTS_SHA = "7f50d7b7277c5fcb39f123b29382bd2e3896dd6dcf772d7963f0ea0362c3d659"
FILE_SHAS = {
    "test-first.patch": "ffdb85cf3cd623c9c0d18a3b29f405c3cb7bff0bcdd5b12e5202f212a2d8b49f",
    "static-intent.patch": "adac7ccffcb5a52ae2d9c15996fdf42895c1360c7c84f70b5ca640dba17c813e",
    "migration_attempt_sql_bench_test.go.txt": "cbc72b1652bb4e7601911d23db90adaddcbaa0115f5b2a9bcf56d88a40dd3e77",
    "pair.py": "6960d23fdd736081952c87df08ef8365a084f9d9735596e0e002c7bfaf2da25d",
}
GO_VERSION = "go version go1.27.1 linux/arm64"
PACKAGE = "pkg/lux/pg/migrate"
GATE_PACKAGE = "pkg/lux/migrate"
BENCHSTAT_VERSION = "v0.0.0-20260825160852-19be9d8e6c70"


def sha(data):
    return hashlib.sha256(data).hexdigest()


def output(*cmd, **kwargs):
    return subprocess.check_output(cmd, **kwargs)


def put(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def git(repo, *args):
    return output("git", "--no-replace-objects", "-C", str(repo), *args).decode().strip()


def check_commit(repo):
    if HERE != repo / "scripts/diagnostics/pg-intent-latency":
        raise RuntimeError("diagnostic must run inside its checkout")
    if git(repo, "rev-list", "--parents", "-n", "1", "HEAD").split() != [os.environ["GITHUB_SHA"], PARENT]:
        raise RuntimeError("unexpected diagnostic head or parent")
    change = git(repo, "diff", "--name-status", PARENT, "HEAD").splitlines()
    if change != ["A\t" + name for name in PATHS] or git(repo, "diff", "--name-only", "HEAD"):
        raise RuntimeError("unexpected diagnostic worktree scope")


def unpack(work):
    source = HERE / "inputs.zip"
    if sha(source.read_bytes()) != INPUTS_SHA:
        raise RuntimeError("unexpected frozen diagnostic inputs")
    with ZipFile(source) as capsule:
        if len(capsule.namelist()) != len(FILE_SHAS) or set(capsule.namelist()) != FILE_SHAS.keys() or capsule.testzip():
            raise RuntimeError("unexpected frozen input inventory")
        for name, expected in FILE_SHAS.items():
            data = capsule.read(name)
            if sha(data) != expected:
                raise RuntimeError(f"unexpected input digest: {name}")
            (work / name).write_bytes(data)
    return runpy.run_path(str(work / "pair.py"))


def build_trees(repo, work, plan):
    if git(repo, "rev-parse", PARENT + "^{tree}") != PARENT_TREE:
        raise RuntimeError("unexpected original diagnostic tree")
    legacy = runpy.run_path(str(repo / "scripts/diagnostics/pg-durable-hosted/hosted.py"))
    receipt = legacy["payload"]()
    if legacy["ARMS"]["Q"] != plan["PRODUCT"]["A"]:
        raise RuntimeError("the previous native run no longer binds the comparison baseline")
    args = legacy["trees"](repo, work)
    output(*args, "read-tree", plan["PRODUCT"]["A"])
    for patch, arm in (("test-first.patch", "A"), ("static-intent.patch", "B")):
        output(*args, "apply", "--cached", "--whitespace=error", str(work / patch))
        if output(*args, "write-tree", text=True).strip() != plan["COMPILED"][arm]:
            raise RuntimeError(f"source projection differs for {arm}")
    return args, legacy, receipt


def native_env(work):
    env = {key: value for key, value in os.environ.items()
           if key not in ("GOOS", "GOARCH", "GOAMD64") and not key.startswith(("LUXO_", "DATABASE_"))}
    return env | {"GOTOOLCHAIN": "local", "GOWORK": "off", "GOFLAGS": "-p=1", "GOMAXPROCS": "2",
                  "CGO_ENABLED": "0", "GOENV": "off", "GOARM64": "v8.0", "GOCACHE": str(work / "cache"),
                  "GOMODCACHE": str(work / "modules"), "TMPDIR": str(work), "GOTMPDIR": str(work),
                  "GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org"}


def compile_arms(args, legacy, work, evidence, plan, env):
    tools = work / "bin"
    tools.mkdir()
    inputs = {}
    for arm, tree in plan["COMPILED"].items():
        src = work / f"source-{arm}"
        legacy["archive_source"](args, src, tree)
        fixture = work / "migration_attempt_sql_bench_test.go.txt"
        with (src / PACKAGE / "migration_attempt_sql_bench_test.go").open("xb") as target:
            target.write(fixture.read_bytes())
        with (evidence / f"compile-{arm}.log").open("w") as log:
            subprocess.run(["sh", "scripts/setup-modules.sh", str(src)], cwd=src, env=env, stdout=log, stderr=log, check=True)
            workspace = env | {"GOWORK": str(src / "go.work")}
            graph_args = ("go", "list", "-mod=readonly", "-p=1", "-deps", "-test", "./" + PACKAGE)
            online = output(*graph_args, cwd=src, env=workspace, stderr=log)
            verified = output("go", "mod", "verify", cwd=src, env=workspace, stderr=log, text=True).strip()
            offline = workspace | {"GOPROXY": "off", "GOSUMDB": "off"}
            if verified != "all modules verified" or output(*graph_args, cwd=src, env=offline, stderr=log) != online:
                raise RuntimeError("could not verify the pinned fixture module graph")
            binary = tools / f"arm-{arm}.test"
            subprocess.run(["go", "test", "-mod=readonly", "-c", "-trimpath", "-o", str(binary), "./" + PACKAGE],
                           cwd=src, env=offline, stdout=log, stderr=log, check=True)
        inputs[arm] = {
            "binary": binary.name, "binarySHA256": sha(binary.read_bytes()), "goos": "linux", "goarch": "arm64",
            "product": plan["PRODUCT"][arm], "tested": tree, "dependencyGraphSHA256": sha(online),
            "goBuild": output("go", "version", "-m", str(binary), env=env, text=True).splitlines(),
        }
    if inputs["A"]["dependencyGraphSHA256"] != inputs["B"]["dependencyGraphSHA256"]:
        raise RuntimeError("benchmark dependency graph differs between arms")
    return tools, inputs


def compile_gate(args, legacy, work, tools, evidence, env):
    source = work / "gate-source"
    legacy["archive_source"](args, source, legacy["BASE_TREE"])
    with (evidence / "compile-gates.log").open("w") as log:
        subprocess.run(["go", "build", "-mod=readonly", "-buildvcs=false", "-o", str(tools) + "/",
                        "./scripts/benchplan", "./scripts/benchgate"], cwd=source, env=env, stdout=log, stderr=log, check=True)
        subprocess.run(["go", "install", "golang.org/x/perf/cmd/benchstat@" + BENCHSTAT_VERSION],
                       cwd=source, env=env | {"GOBIN": str(tools)}, stdout=log, stderr=log, check=True)
    return {name: sha((tools / name).read_bytes()) for name in ("benchplan", "benchgate", "benchstat")}


def host(legacy, evidence, tag):
    cpu = json.loads(output("lscpu", "-J"))
    affinity = sorted(os.sched_getaffinity(0))
    put(evidence / f"host-{tag}.json", {"cpu": cpu, "affinity": affinity, "load": os.getloadavg()})
    legacy["require_cpu"](cpu)
    if affinity != [0, 1, 2, 3]:
        raise RuntimeError("runner CPU affinity differs from the original qualification")


def screen(tools, work, evidence, plan, verified_cpu):
    # These are diagnostic controls and a single primary comparison.
    samples = {(comp, side): [] for comp in plan["COMPARISONS"] for side in (0, 1)}
    for line in (evidence / "raw.jsonl").read_text().splitlines():
        item = json.loads(line)
        if item["exit"]:
            raise RuntimeError("unexpected unsuccessful benchmark process")
        samples[item["comparison"], item["side"]].append(item["output"])
    keys = [{"Package": GATE_PACKAGE, "Name": f"{plan['BENCHMARK']}/{leaf}-2"} for leaf in plan["LEAVES"]]
    expected = work / "expected.json"
    put(expected, {"Base": keys, "Head": keys})
    statuses = {}
    for comp in plan["COMPARISONS"]:
        normalized = []
        for side in (0, 1):
            raw = work / f"{comp}-{side}.raw"
            # The Go runtime omits its CPU label on this ARM64 runner. Only the
            # derived gate input uses the already verified lscpu label.
            raw.write_text(f"cpu: {verified_cpu}\n" + "".join(samples[comp, side]))
            path = raw.with_suffix(".txt")
            output(tools / "benchplan", "--normalize", raw, path)
            normalized.append(path)
        output(tools / "benchplan", "--verify", expected, *normalized)
        directions = (False, True) if comp != "AB" else (False,)
        for reverse in directions:
            label = f"{comp}-reverse" if reverse else comp
            left, right = reversed(normalized) if reverse else normalized
            csv_path = evidence / f"{label}.csv"
            with csv_path.open("w") as csv, (evidence / f"{label}.warnings").open("w") as warnings:
                subprocess.run([tools / "benchstat", "-alpha", "0.01", "-filter", ".unit:(sec/op OR B/op OR allocs/op)",
                                "-format", "csv", f"base={left}", f"head={right}"], stdout=csv, stderr=warnings, check=True)
            with (evidence / f"{label}.gate.txt").open("w") as log:
                statuses[label] = subprocess.run([tools / "benchgate", csv_path], stdout=log, stderr=subprocess.STDOUT).returncode
    put(evidence / "screen.json", statuses)
    if any(statuses.values()):
        raise RuntimeError("native control or primary comparison was rejected")


def measure(work, evidence, plan, args, legacy, inherited):
    host(legacy, evidence, "admission")
    env = native_env(work)
    if output("go", "version", env=env, text=True).strip() != GO_VERSION:
        raise RuntimeError("unexpected native Go toolchain")
    tools, inputs = compile_arms(args, legacy, work, evidence, plan, env)
    gates = compile_gate(args, legacy, work, tools, evidence, env)
    put(evidence / "binding.json", {
        "nativeVersion": GO_VERSION, "previousDiagnostic": PARENT, "diagnostic": os.environ["GITHUB_SHA"],
        "inputArchiveSHA256": INPUTS_SHA, "payloadSHA256": FILE_SHAS, "inheritedPayload": inherited,
        "gateSource": legacy["BASE"], "gateToolsSHA256": gates, "arms": inputs,
        "schedule": list(plan["schedule"]()), "processCount": 2 * len(list(plan["schedule"]())),
    })
    host(legacy, evidence, "before")
    try:
        cpu, raw = plan["collect"](tools, inputs, evidence, verified_cpu=legacy["CPU"])
    finally:
        host(legacy, evidence, "after")
    if cpu != legacy["CPU"]:
        raise RuntimeError("benchmark CPU binding changed")
    for binding in inputs.values():
        if sha((tools / binding["binary"]).read_bytes()) != binding["binarySHA256"]:
            raise RuntimeError("benchmark executable changed during sampling")
    put(evidence / "paired.json", plan["summary"](raw))
    screen(tools, work, evidence, plan, cpu)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("check", "run"))
    parser.add_argument("repo", type=Path)
    parsed = parser.parse_args()
    os.umask(0o077)
    repo = parsed.repo.resolve(strict=True)
    if parsed.mode == "run":
        check_commit(repo)
        base = repo / ".tmp"
        if base.is_symlink():
            raise RuntimeError("diagnostic scratch must stay in its owned checkout")
    else:
        base = HERE
    base.mkdir(exist_ok=True)
    evidence = base / "pg-intent-latency-evidence"
    if parsed.mode == "run":
        evidence.mkdir()
    work = None
    try:
        with tempfile.TemporaryDirectory(prefix="intent-native.", dir=base) as tmp:
            work = Path(tmp)
            plan = unpack(work)
            args, legacy, inherited = build_trees(repo, work, plan)
            if parsed.mode == "check":
                print(json.dumps({"product": plan["PRODUCT"], "compiled": plan["COMPILED"],
                                  "scheduledPairs": len(list(plan["schedule"]())), "inputArchiveSHA256": INPUTS_SHA}))
            else:
                measure(work, evidence, plan, args, legacy, inherited)
    finally:
        if parsed.mode == "run":
            put(evidence / "cleanup.json", {"ownedScratchRemaining": work is not None and work.exists()})


if __name__ == "__main__":
    main()
