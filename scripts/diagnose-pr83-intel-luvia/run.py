#!/usr/bin/env python3
"""Replay the remaining PR 83 timing when the archived CPU and inputs match."""
from hashlib import sha256
import importlib.util
import json
import os
from pathlib import Path
import re
import runpy
import shutil
import subprocess
import sys
from tempfile import TemporaryDirectory

INPUT = Path(__file__).resolve().parent
ROOT = INPUT.parents[1]
OUT = ROOT / ".tmp/pr83-intel-luvia-evidence"
CHECKOUT = ROOT / ".tmp/pr83-intel-luvia-inputs"
HARNESS = ROOT / ".tmp/pr83-native-harness/scripts/diagnose-pr83-amd"
HARNESS_FILES = {
    "run.py": "c01ed27ff3b3a361e86495611507a7f298bbda1fdbbcb9fa7144a2309e722eea",
    "go-capture.py": "b93cb28f2b092f63a07543a71cd39d46f3f28044d73f3547aaf16078c2d07db9",
    "manifest.json": "0c12129c702cf833c79f9e8d3269d1cfb7171d7cd54b1b6255c63a0598bc09a0",
}
PARENT = "2690c4edbc5856f03773db8114188056110c9b57"
ORIGINAL_BASE = "aaf44cbe5911a6f357f601720ac6c4f2d894a0dd"
HARNESS_REVISION = "5376367706e217b18a676dfe932004a0f5419a04"
REF = "refs/heads/diagnostics/pr83-intel-luvia"
MANIFEST = INPUT / "manifest.json"
MANIFEST_SHA = "55bdf490839abf6b5f1a6047512592870c0c47ad6b7c0f415f7dfe8a58a9f223"
CONFIG = json.loads(MANIFEST.read_bytes())
SLOTS = {1, 2, 3}
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


def diagnostic_paths():
    relative = INPUT.relative_to(ROOT)
    paths = [".github/workflows/diagnose-pr83-intel-luvia.yml",
             *(str(relative / name) for name in ("run.py", "project.py", "manifest.json"))]
    for family, spec in CONFIG["families"].items():
        for fixture, binding in spec["keep"].items():
            if binding["projectedSHA256"] is not None:
                paths.append(str(relative / "fixtures" / family / (fixture + ".txt")))
    return sorted(paths)


def bind_event():
    active = git(ROOT, "rev-parse", "HEAD")
    slot = int(os.environ["SLOT"])
    require(slot in SLOTS and os.environ.get("RUNNER_KIND") == "github-hosted" and
            os.environ.get("GITHUB_REPOSITORY") == "light-speak/luxo" and
            os.environ.get("GITHUB_EVENT_NAME") == "push" and os.environ.get("GITHUB_REF") == REF and
            os.environ.get("GITHUB_SHA") == active, "unexpected diagnostic event")
    workflow = ".github/workflows/diagnose-pr83-intel-luvia.yml"
    driver = str((INPUT / "run.py").relative_to(ROOT))
    require(git(ROOT, "show", "-s", "--format=%P", active) == PARENT and
            git(ROOT, "show", "-s", "--format=%P", PARENT) == ORIGINAL_BASE and
            git(ROOT, "diff", "--name-status", ORIGINAL_BASE, active).splitlines() ==
            ["A\t" + path for path in diagnostic_paths()] and
            git(ROOT, "diff", "--name-status", PARENT, active).splitlines() ==
            ["M\t" + path for path in sorted([workflow, driver])], "unexpected diagnostic paths or parent")
    return active, slot


def cpu_families(cpuinfo):
    models = {line.split(":", 1)[1].strip() for line in cpuinfo.splitlines()
              if line.startswith("model name")}
    require(len(models) == 1, "unknown or mixed CPU model")
    cpu = models.pop()
    return cpu, CONFIG["routes"].get(cpu, [])


def route():
    active, slot = bind_event()
    cpu, families = cpu_families(Path("/proc/cpuinfo").read_text())
    if families:
        require(output("uname", "-sm").strip() == b"Linux x86_64" and
                'VERSION_ID="24.04"' in Path("/etc/os-release").read_text().splitlines() and
                output("getconf", "_NPROCESSORS_ONLN").strip() == b"4", "unexpected hosted environment")
    result = {"slot": slot, "cpu": cpu, "families": families, "diagnostic": active}
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


def plan_route(runtime, tools, directory, case):
    package = case["package"]
    names = runtime.full_names(case, "timed")
    require(set(names) <= set(case["expected"]), "timed leaves exceed complete inventory")
    leaves = [{"Package": package, "Name": name} for name in names]
    expected = directory / "expected.json"
    save(expected, {"Base": leaves, "Head": leaves})
    targets, batch, paired = (directory / n for n in ("targets.tsv", "batch.tsv", "confirmation.json"))
    targets.write_text("".join(f"{package}\t{name}\n" for name in names))
    output(tools / "benchplan", "--confirmation-plan", expected, targets, batch, paired)
    output(tools / "benchplan", "--verify-profile-batch", "confirmation", paired, batch)
    selector = "|".join("/".join(f"^{part}$" for part in name[:-2].split("/")) for name in sorted(names))
    require(batch.read_text().splitlines() == [f"{package}\t{case['stockProfile']}\t{selector}"],
            "archived stock profile or selector changed")
    return expected, selector, names


def prepare_binary(runtime, go, tools, mode, role, source, family, case, work, evidence, stock):
    package, profile = case["package"], case["stockProfile"]
    evidence.mkdir()
    wrapper = work / "wrapper/go"
    env = dict(os.environ, PATH=str(wrapper.parent) + os.pathsep + os.environ["PATH"],
               LUXO_WITNESS_REAL_GO=go, LUXO_WITNESS_PROFILE_LOG=str(evidence), GOWORK=str(source / "go.work"))
    stem = package.replace("/", "-")
    if mode == "stock":
        key = (role, package, profile)
        stem += "" if profile == "default" else "-" + profile
        if key in stock:
            binary, previous = stock[key]
            for suffix in (".dependencies.tsv", ".overlay.json"):
                shutil.copyfile(previous / (stem + suffix), evidence / (stem + suffix))
        else:
            built = work / (role + "-stock-" + stem)
            command = "compile-performance-baseline" if role == "A" else "compile-performance"
            runtime.call(tools / "benchrun", command, source, built, package, profile, env=env)
            binary = built / (stem + ".test")
            stock[key] = binary, evidence
        runtime.assert_overlay(evidence, stem, case["stockOverlay"])
    else:
        built = work / (role + "-projected-" + family)
        built.mkdir()
        binary = built / (stem + ".test")
        emit = runpy.run_path(str(INPUT / "project.py"))["project"]
        binding, overlay = emit(source, built / "projection", CONFIG, family)
        require(binding == case["projection"], "reviewed fixture projection differs")
        runtime.call(wrapper, "test", "-trimpath", "-c", "-overlay=" + str(overlay),
                     "./" + package, "-o", binary, cwd=source, env=env)
        roots = output(binary, "-test.list=.*", cwd=source / package).decode().splitlines()
        require(roots == [case["root"]], "projected binary acquired an extra test or benchmark")
        save(evidence / "projection.json", binding)
        observed = json.loads((evidence / (stem + ".overlay.json")).read_bytes())
        want = {package + "/" + Path(path).name: binding["projected"].get(Path(path).name, "")
                for path in json.loads(overlay.read_bytes())["Replace"]}
        require(observed == want and len(observed) == binding["removed"] + len(binding["projected"]), "wrong effective test overlay")
    runtime.inspect_binary(go, binary, evidence, case["hot"], work)
    raw = runtime.sample(binary, source, package, "^" + case["root"] + "$", "1x")
    (evidence / "full-inventory.raw").write_bytes(raw)
    runtime.inventory(raw, case["expected"], runtime.CONFIG["cpu"], package, one_iteration=True)
    return binary


def sample_family(runtime, go, tools, arms, family, work, stock):
    case = CONFIG["families"][family]
    directory = OUT / family
    directory.mkdir()
    expected, selector, names = plan_route(runtime, tools, directory, case)
    comparisons = {}
    roles = {}
    for mode in ("stock", "projected"):
        mode_path = directory / mode
        mode_path.mkdir()
        roles[mode] = {role: prepare_binary(runtime, go, tools, mode, role, source, family,
                                           case, work, mode_path / role, stock) for role, source in arms.items()}
    for mode, binaries in roles.items():
        mode_path = directory / mode
        package = case["package"]
        count = runtime.measure(arms, binaries, package, selector, names, mode_path)
        verdicts = runtime.gate(tools, expected, arms["H"], mode_path, package, names)
        raw = (mode_path / "AH-1.forward.gate.txt").read_text()
        confirmations = re.findall(r"^confirmed performance regression: (\S+) (\S+) ", raw, re.M)
        comparisons[mode] = {"processes": count, "gates": verdicts, "confirmed": confirmations}
    classify(family, comparisons)
    save(directory / "status.json", comparisons)
    return comparisons


def classify(family, modes):
    case = CONFIG["families"][family]
    signed = {f"github.com/light-speak/luxo/{case['package']}/{case['root'].removeprefix('Benchmark')}/{x}-2"
              for x in case["timed"]}
    require(modes.keys() == {"stock", "projected"}, "missing compiler arm")
    for value in modes.values():
        confirmed = [tuple(row) for row in value["confirmed"]]
        gates = value["gates"]
        require(value["processes"] == 120 and gates.keys() == PAIRS | {"AH-1.forward"} and
                all(type(code) is int and code in (0, 1) for code in gates.values()) and
                bool(confirmed) == bool(gates["AH-1.forward"]) and len(confirmed) == len(set(confirmed)) and
                all(name in signed and unit in ("sec/op", "B/op", "allocs/op") for name, unit in confirmed),
                "incomplete or invalid strict gate receipt")
    stock, projected = modes["stock"], modes["projected"]
    controls = all(value["gates"][name] == 0 for name in PAIRS for value in modes.values())
    repeated = {name for name, unit in stock["confirmed"] if unit == "sec/op"}
    stock_is_timing_only = all(unit == "sec/op" for _, unit in stock["confirmed"])
    return {"controlsGreen": controls, "stockTiming": sorted(repeated),
            "stockReproducedExact": repeated == signed and stock_is_timing_only,
            "projectedExit": projected["gates"]["AH-1.forward"]}


def summarize(directory, diagnostic):
    receipts = sorted(directory.glob("*/status.json"))
    require(len(receipts) == len(SLOTS), "missing slot receipt")
    results = {family: [] for family in CONFIG["families"]}
    slots = set()
    for path in receipts:
        status = json.loads(path.read_bytes())
        expected = CONFIG["routes"].get(status["cpu"], [])
        slot = status["slot"]
        require(status["diagnostic"] == diagnostic and slot in SLOTS - slots and
                status["families"] == expected and status["processes"] == 240 * len(expected) and
                set(status["comparisons"]) == set(expected), "invalid CPU route or native receipt")
        slots.add(slot)
        for family, result in status["comparisons"].items():
            results[family].append({"slot": slot, **classify(family, result)})
    print(json.dumps(results, sort_keys=True))
    require(all(items and all(x["controlsGreen"] and x["projectedExit"] == 0 for x in items)
                for items in results.values()), "missing matching CPU, failed controls or projected regression")
    require(all(any(item["stockReproducedExact"] for item in items) for items in results.values()),
            "historical stock timing was not reproduced for every family")


def main():
    require(sha(MANIFEST) == MANIFEST_SHA, "signed native manifest changed")
    args = sys.argv[1:]
    if len(args) == 3 and args[0] == "--summarize":
        summarize(Path(args[1]), args[2])
        return
    host = route()
    observed = {"comparisons": {}, "processes": 0, **host}
    if args == ["--probe"]:
        with Path(os.environ["GITHUB_OUTPUT"]).open("a") as output_file:
            output_file.write("matched=" + ("true" if host["families"] else "false") + "\n")
        if not host["families"]:
            save(OUT / "status.json", observed)
        return
    require(not args and host["families"], "refusing to run on an unmatched CPU")
    require(git(HARNESS, "rev-parse", "HEAD") == HARNESS_REVISION, "unexpected reused harness checkout")
    runtime = native_harness(HARNESS, host["cpu"])
    go = shutil.which("go")
    require(go, "Go not found")
    arms = runtime.arm_sources(go)
    try:
        with TemporaryDirectory(prefix="pr83-intel-luvia-work.", dir=OUT.parent) as folder:
            work = Path(folder)
            tools = runtime.build_tools(go, arms["H"], work)
            stock = {}
            for family in host["families"]:
                observed["comparisons"][family] = sample_family(runtime, go, tools, arms, family, work, stock)
                observed["processes"] += 240
    finally:
        for checkout in arms.values():
            for name in ("go.work", "go.work.sum"):
                (checkout / name).unlink(missing_ok=True)
    save(OUT / "status.json", observed)
    results = [classify(family, comparison) for family, comparison in observed["comparisons"].items()]
    require(all(result["controlsGreen"] and result["projectedExit"] == 0 for result in results),
            "failed controls or projected regression")


if __name__ == "__main__":
    main()
