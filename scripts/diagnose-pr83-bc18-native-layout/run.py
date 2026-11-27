#!/usr/bin/env python3
"""Recheck the current PR 83 timing on the original 9V45 CPU."""
from hashlib import sha256
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import runpy
import shutil
import subprocess
import sys
import tarfile
from tempfile import TemporaryDirectory

INPUT = Path(__file__).resolve().parent
ROOT = INPUT.parents[1]
OUT = ROOT / ".tmp/pr83-bc18-native-evidence"
CHECKOUT = ROOT / ".tmp/pr83-bc18-native-inputs"
HARNESS = ROOT / ".tmp/pr83-native-harness/scripts/diagnose-pr83-amd"
HARNESS_FILES = {
    "run.py": "c01ed27ff3b3a361e86495611507a7f298bbda1fdbbcb9fa7144a2309e722eea",
    "go-capture.py": "b93cb28f2b092f63a07543a71cd39d46f3f28044d73f3547aaf16078c2d07db9",
    "manifest.json": "0c12129c702cf833c79f9e8d3269d1cfb7171d7cd54b1b6255c63a0598bc09a0",
}
PARENT = "b5de982bbd569511d9047a3273ff94e012dd16e5"
HARNESS_REVISION = "5376367706e217b18a676dfe932004a0f5419a04"
REF = "refs/heads/diagnostics/pr83-b5-native-9v45"
MANIFEST = INPUT / "manifest.json"
MANIFEST_SHA = "779496ec6c48fdd6b87dee17f27b3a0e2285f1d4dbb286d6b07607b4b77359c3"
CONFIG = json.loads(MANIFEST.read_bytes())
SLOTS = set(range(1, 4))
TARGET_CASES = {"api-non-identity", "rpc-handler-panic"}
PAIRS = {f"{arm}-{group}.{direction}" for arm in ("AA", "HH")
         for group in (1, 2) for direction in ("forward", "reverse")}


def require(ok, problem):
    if not ok:
        raise RuntimeError(problem)


def output(*args, cwd=ROOT):
    return subprocess.check_output(args, cwd=cwd)


def git(directory, *args):
    return output("git", "--no-replace-objects", "-C", directory, *args).decode().strip()


def save(path, value):
    with path.open("x") as stream:
        json.dump(value, stream, indent=2, sort_keys=True)
        stream.write("\n")


def sha(path):
    return sha256(path.read_bytes()).hexdigest()


def bind_event():
    active = git(ROOT, "rev-parse", "HEAD")
    slot = int(os.environ["SLOT"])
    require(slot in SLOTS and os.environ.get("RUNNER_KIND") == "github-hosted" and
            os.environ.get("GITHUB_REPOSITORY") == "light-speak/luxo" and
            os.environ.get("GITHUB_EVENT_NAME") == "push" and os.environ.get("GITHUB_REF") == REF and
            os.environ.get("GITHUB_SHA") == active, "unexpected diagnostic event")
    paths = [".github/workflows/diagnose-pr83-bc18-native-layout.yml",
             *(str((INPUT / file).relative_to(ROOT)) for file in ("manifest.json", "run.py"))]
    require(git(ROOT, "show", "-s", "--format=%P", active) == PARENT and
            git(ROOT, "diff", "--name-status", PARENT, active).splitlines() ==
            ["M\t" + path for path in sorted(paths)], "unexpected diagnostic parent or paths")
    return active, slot


def target_cases(cpu):
    return [key for key in CONFIG["routes"].get(cpu, []) if key in TARGET_CASES]


def cpu_cases(cpuinfo):
    models = {line.split(":", 1)[1].strip() for line in cpuinfo.splitlines() if line.startswith("model name")}
    require(len(models) == 1, "unknown or mixed CPU model")
    cpu = models.pop()
    return cpu, target_cases(cpu)


def route():
    active, slot = bind_event()
    cpu, cases = cpu_cases(Path("/proc/cpuinfo").read_text())
    if cases:
        require(output("uname", "-sm").strip() == b"Linux x86_64" and
                'VERSION_ID="24.04"' in Path("/etc/os-release").read_text().splitlines() and
                output("getconf", "_NPROCESSORS_ONLN").strip() == b"4", "unexpected hosted environment")
    result = {"slot": slot, "cpu": cpu, "cases": cases, "diagnostic": active}
    OUT.mkdir(parents=True, exist_ok=True)
    receipt = OUT / "route.json"
    if receipt.exists():
        require(json.loads(receipt.read_bytes()) == result, "CPU or checkout changed after routing")
    else:
        save(receipt, result)
    return result


def native_harness(directory, cpu):
    for name, digest in HARNESS_FILES.items():
        require(sha(directory / name) == digest, f"native harness changed: {name}")
    spec = importlib.util.spec_from_file_location("pr83_native_harness", directory / "run.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    module.ROOT, module.OUT, module.CHECKOUT = ROOT, OUT, CHECKOUT
    module.CONFIG = {**CONFIG, "cpu": cpu}
    return module


def arm_sources(go, work):
    checkouts = {role: CHECKOUT / role for role in ("A", "H")}
    for role, checkout in checkouts.items():
        exact = CONFIG[role]
        require(git(checkout, "rev-parse", "HEAD") == exact["commit"] and
                git(checkout, "rev-parse", "HEAD^{tree}") == exact["tree"] and
                git(checkout, "rev-parse", "--show-toplevel") == str(checkout) and
                not git(checkout, "status", "--porcelain"), f"wrong checkout for {role}")
        require(not any(Path(name).name == ".gitattributes"
                        for name in git(checkout, "ls-tree", "-r", "--name-only", "HEAD").splitlines()),
                "archive attributes changed benchmark source")
        for name, digest in (CONFIG["sourceSHA256"] | exact["sourceSHA256"]).items():
            require(sha(checkout / name) == digest, f"changed hot-path fixture {name}")
    a, h = checkouts["A"], checkouts["H"]
    require(git(h, "show", "-s", "--format=%P", "HEAD").split() == CONFIG["H"]["parents"] and
            sha256(output("git", "--no-replace-objects", "-C", h, "diff", "--name-status",
                          CONFIG["A"]["commit"], "HEAD")).hexdigest() == CONFIG["diffSHA256"],
            "unexpected PR provenance")
    fields = ("GOVERSION", "GOHOSTOS", "GOHOSTARCH", "GOOS", "GOARCH", "GOAMD64", "CGO_ENABLED", "GOFLAGS", "GOTOOLCHAIN")
    compiler = output(go, "env", *fields)
    require(compiler == b"go1.27.1\nlinux\namd64\nlinux\namd64\nv1\n1\n\nlocal\n", "wrong compiler or flags")
    archived_h = work / "source-H"
    archived_h.mkdir()
    archive = output("git", "--no-replace-objects", "-C", h, "archive", "HEAD")
    with tarfile.open(fileobj=io.BytesIO(archive)) as files:
        files.extractall(archived_h, filter="data")
    for arm in (a, archived_h):
        (arm / ".tmp").mkdir()
        output("sh", archived_h / "scripts/setup-modules.sh", arm)
    require((a / "go.work").read_bytes() == (archived_h / "go.work").read_bytes(), "Go workspace differs")
    save(OUT / "binding.json", {"input": CONFIG, "sourceMode": "A checkout; H Git archive",
                               "go": dict(zip(fields, compiler.decode().splitlines())), "goSHA256": sha(Path(go)),
                               "bootID": Path("/proc/sys/kernel/random/boot_id").read_text().strip()})
    (OUT / "lscpu.json").write_bytes(output("lscpu", "--json"))
    return {"A": a, "H": archived_h}


def plan_route(tools, directory, case):
    package, profile, names = case["package"], case["profile"], case["timed"]
    leaves = [{"Package": package, "Name": name} for name in names]
    expected, targets, batch, confirmed = (directory / p for p in ("expected.json", "targets.tsv", "batch.tsv", "confirmation.json"))
    save(expected, {"Base": leaves, "Head": leaves})
    targets.write_text("".join(f"{package}\t{name}\n" for name in names))
    output(tools / "benchplan", "--confirmation-plan", expected, targets, batch, confirmed)
    output(tools / "benchplan", "--verify-profile-batch", "confirmation", confirmed, batch)
    require(batch.read_text().splitlines() == [f"{package}\t{profile}\t{case['selector']}"], "wrong archived selector/profile")
    return expected


def expected_overlay(source, package, profile):
    if profile == "api-non-identity":
        return {f"{package}/{name}": "" for name in ("identity_reader_bench_test.go",
                "identity_reader_fuzz_test.go", "identity_reader_test.go", "identity_value_test.go")}
    files = sorted((source / package).glob("*_test.go"))
    require(profile == "rpc-handler-panic", "unknown compile profile")
    return {f"{package}/{path.name}": "" for path in files if path.name != "handler_panic_bench_test.go"}


def compile_binary(runtime, driver, go, tools, source, role, case, mode, work, evidence):
    package, profile = case["package"], case["profile"]
    evidence.mkdir(parents=True)
    binary_root = work / ("binary-" + evidence.parent.parent.name + "-" + mode + "-" + role)
    binary_root.mkdir()
    wrapper = work / "wrapper/go"
    environment = dict(os.environ, PATH=str(wrapper.parent)+os.pathsep+os.environ["PATH"],
                       LUXO_WITNESS_REAL_GO=go, LUXO_WITNESS_PROFILE_LOG=str(evidence),
                       GOWORK=str(source/"go.work"),
                       GOFLAGS="-x -ldflags=-funcalign=64" if mode == "aligned" else "-x")
    command = "compile-performance-baseline" if role == "A" else "compile-performance"
    trace = binary_root / "go-build.trace"
    args = (tools/"benchrun", command, source, binary_root, package, profile)
    with trace.open("xb") as stream:
        result = subprocess.run(args, cwd=ROOT, env=environment, stderr=stream)
    if result.returncode:
        raise RuntimeError(f"benchmark compilation failed: {trace.read_text(errors='replace')[-4000:]}")
    linker = Path(output(go, "env", "GOTOOLDIR").decode().strip()) / "link"
    invocation = driver["link_invocation"](trace, linker, Path(package).name + ".test", mode == "aligned")
    save(evidence/"link-command.json", invocation)
    trace.unlink()
    stem = package.replace("/", "-") + "-" + profile
    binary = binary_root / (stem + ".test")
    overlay = expected_overlay(source, package, profile)
    runtime.assert_overlay(evidence, stem, overlay)
    inventory = CONFIG["inventory"][case["inventory"]]
    roots = sorted({name.removesuffix("-2").split("/")[0] for name in inventory})
    if profile != "api-non-identity":
        require(sorted(output(binary, "-test.list=.*", cwd=source/package).decode().splitlines()) == roots,
                "isolated profile acquired another root")
    selector = "^(" + "|".join(roots) + ")$"
    raw = runtime.sample(binary, source, package, selector, "1x")
    (evidence/"inventory.raw").write_bytes(raw)
    runtime.inventory(raw, inventory, runtime.CONFIG["cpu"], package, one_iteration=True)
    runtime.inspect_binary(go, binary, evidence, case["hot"], work)
    return binary


def measure_mode(runtime, arms, binaries, case, directory):
    processes = 0
    with (directory / "order.tsv").open("x", buffering=1) as order:
        order.write("process\tblock\tpair\tcomparison\tside\tarm\n")
        for block, index, comparison, matching in runtime.schedule():
            for side in matching:
                label, role = ("left", "right")[side], comparison[side]
                processes += 1
                line = (processes, block, index, comparison, label, role)
                order.write("\t".join(map(str, line)) + "\n")
                raw = runtime.sample(binaries[role], arms[role], case["package"], case["selector"], "200ms")
                runtime.inventory(raw, case["timed"], runtime.CONFIG["cpu"], case["package"])
                with (directory / f"{comparison}-{block}.{label}.raw").open("ab") as sink:
                    sink.write(raw)
    require(processes == 120, "invalid scheduled sample count")
    return processes


def classify(case, modes, layout):
    require(set(modes) in ({"stock"}, {"stock", "aligned"}), "unexpected compiler arms")
    prefix = "github.com/light-speak/luxo/" + case["package"] + "/"
    known = {prefix + name.removeprefix("Benchmark") for name in case["timed"]}
    required = {(name, "sec/op") for name in case["confirmed"]}
    for row in modes.values():
        gates = row["gates"]
        confirmed = [tuple(item) for item in row["confirmed"]]
        require(row["processes"] == 120 and gates.keys() == PAIRS | {"AH-1.forward"} and
                all(type(code) is int and code in (0, 1) for code in gates.values()) and
                bool(confirmed) == bool(gates["AH-1.forward"]) and len(set(confirmed)) == len(confirmed) and
                all(name in known and unit in ("sec/op", "B/op", "allocs/op") for name, unit in confirmed),
                "invalid gate receipt")
    original = {tuple(item) for item in modes["stock"]["confirmed"]}
    controls = all(mode["gates"][name] == 0 for mode in modes.values() for name in PAIRS)
    require(("aligned" in modes) == (layout is not None), "layout evidence disagrees with measurement")
    if layout is not None:
        require(required & original, "alignment requires a reproduced stock target")
        symbols = set().union(*(set(arm["required"]) for arm in layout["arms"].values()))
        same_hot = bool(symbols) and all(layout["sameStockHotInstructions"].get(name) is True for name in symbols)
        shifted = layout["linkOnly"] is True
        aligned_exit = modes["aligned"]["gates"]["AH-1.forward"]
    else:
        same_hot, shifted, aligned_exit = False, False, None
    return {
        "controlsGreen": controls,
        "reproducedTargets": sorted(name for name, _ in required & original),
        "unexpectedConfirmed": sorted([list(item) for item in original - required]),
        "sameStockHotInstructions": same_hot,
        "linkOnly": shifted,
        "alignedExit": aligned_exit,
        "qualifiedLayout": controls and original == required and same_hot and shifted and aligned_exit == 0,
    }


def summarize(directory, diagnostic):
    receipts = sorted(directory.glob("*/status.json"))
    require(len(receipts) == len(SLOTS), "missing slot receipt")
    results, seen = {}, set()
    for path in receipts:
        status = json.loads(path.read_bytes())
        cases = target_cases(status["cpu"])
        slot = status["slot"]
        require(slot in SLOTS-seen and status["diagnostic"] == diagnostic and status["cases"] == cases and
                status["results"].keys() == set(cases), "invalid route receipt")
        seen.add(slot)
        outcomes, processes = {}, 0
        for key, item in status["results"].items():
            layout_path = path.parent / key / "alignment.json"
            layout = json.loads(layout_path.read_bytes()) if layout_path.is_file() else None
            outcome = classify(CONFIG["case"][key], item["comparisons"], layout)
            require(outcome == item["outcome"], "unstable gate outcome")
            outcomes[key] = outcome
            processes += sum(mode["processes"] for mode in item["comparisons"].values())
        require(status["processes"] == processes, "wrong per-slot process totals")
        results[slot] = outcomes
    print(json.dumps(results, sort_keys=True))
    require(any(set(items) == TARGET_CASES and all(value["qualifiedLayout"] for value in items.values())
                for items in results.values()),
            "no exact 9V45 slot reproduced and isolated all original targets")


def run_case(runtime, driver, go, tools, arms, key, work):
    case = CONFIG["case"][key]
    directory = OUT / key
    directory.mkdir()
    expected = plan_route(tools, directory, case)

    def evaluate(mode):
        target = directory / mode
        binaries = {role: compile_binary(runtime, driver, go, tools, source, role, case, mode,
                                         work, target / role) for role, source in arms.items()}
        if mode == "aligned":
            layout = driver["compare"](directory, case, cache=False)
            save(directory / "alignment.json", layout)
        count = measure_mode(runtime, arms, binaries, case, target)
        gates = runtime.gate(tools, expected, arms["H"], target, case["package"], case["timed"])
        transcript = (target / "AH-1.forward.gate.txt").read_text()
        found = re.findall(r"^confirmed performance regression: (\S+) (\S+) ", transcript, re.M)
        return {"processes": count, "gates": gates, "confirmed": [list(item) for item in found]}

    modes = {"stock": evaluate("stock")}
    stock = classify(case, modes, None)
    layout = None
    if stock["controlsGreen"] and stock["reproducedTargets"] and not stock["unexpectedConfirmed"]:
        modes["aligned"] = evaluate("aligned")
        layout = json.loads((directory / "alignment.json").read_bytes())
    return {"comparisons": modes, "outcome": classify(case, modes, layout)}


def main():
    require(sha(MANIFEST) == MANIFEST_SHA, "signed native manifest changed")
    args = sys.argv[1:]
    if len(args) == 3 and args[0] == "--summarize":
        summarize(Path(args[1]), args[2])
        return
    host = route()
    observed = {"results": {}, "processes": 0, **host}
    if args == ["--probe"]:
        with Path(os.environ["GITHUB_OUTPUT"]).open("a") as log:
            log.write("matched=" + str(bool(host["cases"])).lower() + "\n")
        if not host["cases"]:
            save(OUT/"status.json", observed)
        return
    require(not args and host["cases"], "CPU does not match the original scope")
    require(git(HARNESS,"rev-parse","HEAD") == HARNESS_REVISION, "wrong native harness checkout")
    runtime = native_harness(HARNESS, host["cpu"])
    driver = runpy.run_path(str(INPUT/"layout.py"))
    go = shutil.which("go")
    require(go, "Go not found")
    with TemporaryDirectory(prefix="pr83-bc18-native.", dir=OUT.parent) as folder:
        work = Path(folder)
        arms = arm_sources(go, work)
        try:
            tools = runtime.build_tools(go, arms["H"], work)
            for key in host["cases"]:
                result = run_case(runtime, driver, go, tools, arms, key, work)
                observed["results"][key] = result
                observed["processes"] += sum(item["processes"] for item in result["comparisons"].values())
        finally:
            for name in ("go.work", "go.work.sum"):
                (arms["A"]/name).unlink(missing_ok=True)
            (arms["A"]/".tmp").rmdir()
    save(OUT/"status.json", observed)
    require(all(i["outcome"]["controlsGreen"] for i in observed["results"].values()), "same-arm controls failed")


if __name__ == "__main__":
    main()
