import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tempfile
from zipfile import ZipFile

A = "eaf2aac9e920135babd24b58f2a2e800ebdc44e8"
AT = "971b9c83c4f764f1f49e9ad32fa2b12f41d5e008"
C = "a6c37087ee90a7ed247067f23c57df0f2ef735d4"
CAPSULE = "b5b268440b63f9fc5ccc3e33ba4b3204be3247c9152e8838adc6d99fe5b81a9e"
FILES = {"candidate.patch", "corrected.luxo", "emitted.json", "equivalent.luxo", "history.log", "pair.py",
         "qualification_fixture_test.go", "runtime_corrected_test.go", "runtime_equivalent_test.go", "shape.json"}
UNITS = ("sec/op", "B/op", "allocs/op")
TOOLS = ("benchplan", "benchgate", "benchstat")


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def run(*args, cwd=None, env=None):
    return subprocess.check_output(args, cwd=cwd, env=env, text=True).strip()


def bind(root, driver_path, work):
    archive = driver_path.with_name("diagnose-valued-await-inputs.zip")
    if digest(archive) != CAPSULE:
        raise RuntimeError("unexpected compiler fixture archive")
    with ZipFile(archive) as zipped:
        if len(zipped.namelist()) != len(FILES) or set(zipped.namelist()) != FILES or zipped.testzip():
            raise RuntimeError("unexpected compiler fixture archive inventory")
        zipped.extractall(work)
    git = ("git", "--no-replace-objects", "-C", str(root))
    if run(*git, "rev-parse", "HEAD", "HEAD^{tree}").splitlines() != [A, AT] or run(*git, "status", "--porcelain", "-uno"):
        raise RuntimeError("baseline revision differs")
    env = {**os.environ, "GIT_INDEX_FILE": str(work / "candidate.index")}
    run(*git, "read-tree", A, env=env)
    run(*git, "apply", "--cached", str(work / "candidate.patch"), env=env)
    if run(*git, "write-tree", env=env) != C:
        raise RuntimeError("candidate reconstruction differs")
    spec = importlib.util.spec_from_file_location("await_pair", work / "pair.py")
    driver = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(driver)
    driver.ROOT, driver.HERE, driver.HISTORY = root, work, work / "history.log"
    driver.A, driver.A_TREE, driver.B = A, AT, C
    return driver


def require_platform():
    machine = platform.system(), platform.machine()
    expected = ["go1.27.1", "linux", "amd64", "v1", "0"]
    if machine != ("Linux", "x86_64") or run("go", "env", "GOVERSION", "GOOS", "GOARCH", "GOAMD64", "CGO_ENABLED").splitlines() != expected:
        raise RuntimeError("unexpected hosted toolchain")
    description = run("lscpu")
    processors = re.findall(r"^Model name:\s*(.+)$", description, re.M)
    if len(processors) != 1:
        raise RuntimeError("missing unique runner CPU identity")
    return processors[0].strip(), description


def compare_prepared(work):
    receipt = json.loads((work / "prepared.json").read_bytes())
    leaves = receipt["split"]["pairedCompiler"]
    old, new = receipt["arms"]["A"], receipt["arms"]["B"]
    shape = json.loads((work / "shape.json").read_bytes())
    if len(leaves) != 35 or set(leaves) != shape.keys() or receipt["split"]["changedCompilerShape"]:
        raise RuntimeError("expected 35 exact compiler benchmark leaves")
    if any(arm["compiler"]["shape"] != shape for arm in (old, new)):
        raise RuntimeError("compiler shape differs from original A/C fixture")
    if {name: data["emitted"] for name, data in new["runtime"].items()} != json.loads((work / "emitted.json").read_bytes()):
        raise RuntimeError("candidate runtime output shape differs")
    if old["runtime"]["equivalent"]["binary"]["sha256"] != new["runtime"]["equivalent"]["binary"]["sha256"]:
        raise RuntimeError("equivalent runtime binary changed between arms")
    boundary = old["runtime"]["corrected"]
    if boundary["compileExit"] or not boundary["smoke"]["exit"]:
        raise RuntimeError("candidate-only runtime edge did not distinguish baseline")
    log = (work / boundary["smoke"]["log"]).read_text()
    if "--- FAIL: BenchmarkAwaitRuntimeCorrected/early" not in log or "after <nil>" not in log:
        raise RuntimeError("baseline rejected early-return fixture for a different reason")
    return receipt, leaves


def schedule(group):
    rows = []
    order = [("A", "A"), ("A", "C"), ("C", "C")]
    for round_number in range(10):
        for offset in range(len(order)):
            i = (round_number + group + offset) % len(order)
            left, right = order[i]
            if left == right and round_number >= 5:
                continue
            reverse = (round_number + group) % 2 == 1
            rows.append((left + right, round_number + 1, left, right, reverse))
    return rows


def read_samples(driver, sample, leaves, cpu):
    environment = {
        "goos": ["goos: linux"], "goarch": ["goarch: amd64"],
        "pkg": ["pkg: github.com/light-speak/luxo/pkg/codegen"], "cpu": [f"cpu: {cpu}"],
    }
    for key, expected in environment.items():
        actual = [line.rstrip() for line in sample.splitlines() if line.startswith(key + ": ")]
        if actual != expected:
            raise RuntimeError(f"benchmark process environment differs: {actual}")
    observations = driver.BENCH_ROW.findall(sample)
    if len(observations) != len(leaves) or {row[0] for row in observations} != set(leaves):
        raise RuntimeError("benchmark process emitted missing/duplicate leaves")
    if any(int(row[1]) < 1 for row in observations) or sample.splitlines()[-1] != "PASS":
        raise RuntimeError("an actual benchmark process did not pass")
    return {leaf: [float(ns), int(size), int(count)] for leaf, _, ns, size, count in observations}


def collect(driver, leaves, cpu, arm, group, evidence):
    env = {key: value for key, value in os.environ.items() if not key.startswith("LUXO_")}
    env.update(GOMAXPROCS="2", GOGC="100", GOMEMLIMIT="off", GODEBUG="")
    pairs = {}
    with (evidence / f"group-{group}.schedule.tsv").open("w") as schedule_log:
        for comparison, number, left, right, reverse in schedule(group):
            print(comparison, number, left, right, int(reverse), sep="\t", file=schedule_log, flush=True)
            pair = [None, None]
            order = ((1, right), (0, left)) if reverse else ((0, left), (1, right))
            for side, name in order:
                filename = evidence / f"{comparison}-{group}-{side}.raw"
                result = subprocess.run(driver.binary_args(arm[name], leaves, "100ms"),
                                        env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
                with filename.open("a") as raw:
                    raw.write(result.stdout)
                result.check_returncode()
                pair[side] = read_samples(driver, result.stdout, leaves, cpu)
            pairs.setdefault(comparison, []).append(pair)
    return pairs


def evaluate(root, work, evidence, leaves):
    gate = work / "bin" / "benchgate"
    stat = work / "bin" / "benchstat"
    plan = work / "bin" / "benchplan"
    keys = [{"Package": "pkg/codegen", "Name": name + "-2"} for name in leaves]
    expected = evidence / "expected.json"
    expected.write_text(json.dumps({"Base": keys, "Head": keys}) + "\n")
    controls = []
    for group in (1, 2):
        for comparison in ("AA", "AC", "CC"):
            left, right = (evidence / f"{comparison}-{group}-{i}.txt" for i in (0, 1))
            for side, file in enumerate((left, right)):
                run(plan, "--normalize", evidence / f"{comparison}-{group}-{side}.raw", file)
            run(plan, "--verify", expected, left, right)
            for reverse in ([False] if comparison == "AC" else [False, True]):
                a, b = (right, left) if reverse else (left, right)
                label = f"{comparison}-{group}" + ("-reverse" if reverse else "")
                with (evidence / f"{label}.csv").open("w") as csv, (evidence / f"{label}.warnings").open("w") as warnings:
                    subprocess.run([stat, "-alpha", "0.01", "-filter", ".unit:(" + " OR ".join(UNITS) + ")",
                                    "-format", "csv", f"base={a}", f"head={b}"], stdout=csv, stderr=warnings, check=True)
                if comparison != "AC":
                    controls.append(label)
    arms = [["--approved-costs", root / "scripts/benchgate/approved-costs.json", "--base-sha", A,
             evidence / "AC-1.csv", evidence / "AC-2.csv"]]
    arms.extend([[evidence / f"{label}.csv"] for label in controls])
    codes = {}
    for name, args in zip(["AC-combined", *controls], arms):
        with (evidence / f"{name}.gate.txt").open("w") as out:
            codes[name] = subprocess.run([gate, *args], stdout=out, stderr=subprocess.STDOUT).returncode
    (evidence / "gates.json").write_text(json.dumps(codes, indent=2) + "\n")
    if any(codes.values()):
        raise RuntimeError("strict gate or same-arm control rejected the hosted timing")


def main():
    root, evidence = map(lambda arg: Path(arg).resolve(), sys.argv[1:3])
    evidence.mkdir(parents=True, exist_ok=False)
    os.umask(0o077)
    os.environ.update(GOTOOLCHAIN="local", GOAMD64="v1", CGO_ENABLED="0", GOMAXPROCS="2", GOFLAGS="-p=1")
    cpu, cpus = require_platform()
    (evidence / "host.txt").write_text(cpus + "\n")
    with tempfile.TemporaryDirectory(dir=evidence.parent, prefix="await-native-work.") as tmp:
        work = Path(tmp)
        driver = bind(root, Path(__file__), work)
        cache = work / "go-cache"
        cache.mkdir()
        os.environ.update(GOCACHE=str(cache), TMPDIR=tmp, GOTMPDIR=tmp)
        env = {**os.environ, "GOWORK": "off"}
        run("sh", "scripts/setup-modules.sh", str(root), cwd=root, env=env)
        workspace = {**env, "GOWORK": str(root / "go.work")}
        run("go", "list", "-deps", "-test", "./pkg/codegen", cwd=root, env=workspace)
        # Generated handlers can use runtime packages the compiler never imports.
        runtime_packages = ("./pkg/lux/api", "./pkg/lux/pg")
        run("go", "list", "-deps", *runtime_packages, cwd=root, env=workspace)
        offline = {**workspace, "GOPROXY": "off", "GOSUMDB": "off"}
        run("go", "list", "-deps", *runtime_packages, cwd=root, env=offline)
        bin_path = work / "bin"
        bin_path.mkdir()
        run("go", "build", "-o", str(bin_path) + "/", "./scripts/benchplan", "./scripts/benchgate", cwd=root, env=env)
        run("go", "install", "golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70",
            cwd=root, env={**env, "GOBIN": str(bin_path)})
        try:
            driver.prepare()
            receipt, leaves = compare_prepared(work)
        finally:
            for path in work.glob("*.log"):
                shutil.copyfile(path, evidence / path.name)
        arm = {"A": work / "compiler-A.test", "C": work / "compiler-B.test"}
        for key, file in arm.items():
            if digest(file) != receipt["arms"]["A" if key == "A" else "B"]["compiler"]["sha256"]:
                raise RuntimeError("prepared compiler binary digest differs")
        (evidence / "binding.json").write_text(json.dumps({
            "base": A, "baseTree": AT, "candidateTree": C, "payload": CAPSULE, "model": cpu,
            "go": run("go", "version"), "commands": {key: driver.binary_args(file, leaves, "100ms") for key, file in arm.items()},
            "sha256": {key: digest(file) for key, file in arm.items()},
            "gateTools": {name: digest(bin_path / name) for name in TOOLS},
        }, indent=2) + "\n")
        shutil.copyfile(work / "prepared.json", evidence / "prepared.json")
        samples = {group: collect(driver, leaves, cpu, arm, group, evidence) for group in (1, 2)}
        (evidence / "samples.json").write_text(json.dumps(samples) + "\n")
        evaluate(root, work, evidence, leaves)


if __name__ == "__main__":
    main()
