#!/usr/bin/env python3
"""Recheck the remaining PR 83 timing on the original CPUs and profiles."""
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
PARENT = "bc18a70e66da8053deb70d575b4752a4343ef1f8"
HARNESS_REVISION = "5376367706e217b18a676dfe932004a0f5419a04"
REF = "refs/heads/diagnostics/pr83-bc18-native-layout"
MANIFEST = INPUT / "manifest.json"
MANIFEST_SHA = "46e93f3752bb106a44a2e91e52735ca8c890cda44e0303b4c0fb27cdd30f521e"
CONFIG = json.loads(MANIFEST.read_bytes())
SLOTS = set(range(1, 7))
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
             *(str((INPUT / file).relative_to(ROOT)) for file in ("layout.py", "manifest.json", "run.py"))]
    require(git(ROOT, "show", "-s", "--format=%P", active) == PARENT and
            git(ROOT, "diff", "--name-status", PARENT, active).splitlines() ==
            ["A\t" + path for path in sorted(paths)], "unexpected diagnostic parent or paths")
    return active, slot


def cpu_cases(cpuinfo):
    models = {line.split(":", 1)[1].strip() for line in cpuinfo.splitlines() if line.startswith("model name")}
    require(len(models) == 1, "unknown or mixed CPU model")
    cpu = models.pop()
    return cpu, CONFIG["routes"].get(cpu, [])


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
        for name, digest in CONFIG["sourceSHA256"].items():
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
    if profile == "api-handler-panic":
        return {f"{package}/{path.name}": "" for path in files if path.name != "handler_panic_bench_test.go"}
    require(profile == "luvia-gateway-identity", "unknown compile profile")
    return {f"{package}/{path.name}": "ae790127f4acd6df4fe2388d239b2657c999add2e742584284c411bef514e365"
            if path.name == "middleware_test.go" else "" for path in files}


def compile_binary(runtime, go, tools, source, role, case, mode, work, evidence):
    package, profile = case["package"], case["profile"]
    evidence.mkdir(parents=True)
    binary_root = work / ("binary-" + evidence.parent.parent.name + "-" + mode + "-" + role)
    binary_root.mkdir()
    wrapper = work / "wrapper/go"
    environment = dict(os.environ, PATH=str(wrapper.parent)+os.pathsep+os.environ["PATH"],
                       LUXO_WITNESS_REAL_GO=go, LUXO_WITNESS_PROFILE_LOG=str(evidence),
                       GOWORK=str(source/"go.work"), GOFLAGS="-ldflags=-funcalign=64" if mode == "aligned" else "")
    command = "compile-performance-baseline" if role == "A" else "compile-performance"
    runtime.call(tools/"benchrun", command, source, binary_root, package, profile, env=environment)
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


def measure_modes(runtime, arms, binaries, case, directory):
    modes = tuple(binaries)
    counts = dict.fromkeys(modes, 0)
    with (directory / "order.tsv").open("x", buffering=1) as order:
        order.write("process\tblock\tpair\tcomparison\tmode\tside\tarm\n")
        for block, index, comparison, matching in runtime.schedule():
            for mode in modes if (block + index) % 2 == 0 else modes[::-1]:
                for side in matching:
                    label, role = ("left", "right")[side], comparison[side]
                    line = (sum(counts.values()) + 1, block, index, comparison, mode, label, role)
                    order.write("\t".join(map(str, line)) + "\n")
                    raw = runtime.sample(binaries[mode][role], arms[role], case["package"], case["selector"], "200ms")
                    runtime.inventory(raw, case["timed"], runtime.CONFIG["cpu"], case["package"])
                    with (directory / mode / f"{comparison}-{block}.{label}.raw").open("ab") as sink:
                        sink.write(raw)
                    counts[mode] += 1
    require(all(count == 120 for count in counts.values()), "invalid scheduled sample count")
    return counts


def classify(case, modes):
    require(modes.keys() == {"stock", "aligned"}, "missing compiler arms")
    prefix = "github.com/light-speak/luxo/"+case["package"]+"/"
    known = {prefix+name.removeprefix("Benchmark") for name in case["timed"]}
    for row in modes.values():
        gates = row["gates"]
        confirmed = [tuple(item) for item in row["confirmed"]]
        require(row["processes"] == 120 and gates.keys() == PAIRS | {"AH-1.forward"} and
                all(type(code) is int and code in (0, 1) for code in gates.values()) and
                bool(confirmed) == bool(gates["AH-1.forward"]) and len(set(confirmed)) == len(confirmed) and
                all(name in known and unit in ("sec/op", "B/op", "allocs/op") for name, unit in confirmed),
                "invalid gate receipt")
    return {"controlsGreen": all(mode["gates"][name] == 0 for mode in modes.values() for name in PAIRS),
            "originalReproduced": modes["stock"]["confirmed"] == [[case["confirmed"], "sec/op"]],
            "alignedExit": modes["aligned"]["gates"]["AH-1.forward"]}


def summarize(directory, diagnostic):
    receipts = sorted(directory.glob("*/status.json"))
    require(len(receipts) == len(SLOTS), "missing slot receipt")
    results, slots = {case: [] for case in CONFIG["case"]}, set()
    for path in receipts:
        status = json.loads(path.read_bytes())
        cases = CONFIG["routes"].get(status["cpu"], [])
        slot = status["slot"]
        require(slot in SLOTS-slots and status["diagnostic"] == diagnostic and status["cases"] == cases and
                status["processes"] == 240*len(cases) and status["results"].keys() == set(cases), "invalid route receipt")
        slots.add(slot)
        for key, item in status["results"].items():
            outcome = classify(CONFIG["case"][key], item["comparisons"])
            require(outcome == item["outcome"], "unstable gate outcome")
            layout = json.loads((path.parent/key/"alignment.json").read_bytes())
            results[key].append({"slot": slot, "linkOnly": layout["linkOnly"],
                                 "oldCacheStockELFMatch": layout.get("oldCacheStockELFMatch"), **outcome})
    print(json.dumps(results, sort_keys=True))
    require(all(items and all(i["controlsGreen"] and i["alignedExit"] == 0 and i["linkOnly"] for i in items)
                for items in results.values()), "missing exact route, invalid control or unqualified alignment")
    require(all(any(item["originalReproduced"] for item in items) for items in results.values()),
            "original stock signal not reproduced in each case")


def run_case(runtime, driver, go, tools, arms, key, work):
    case = CONFIG["case"][key]
    directory = OUT / key
    directory.mkdir()
    expected = plan_route(tools, directory, case)
    binaries = {mode: {role: compile_binary(runtime, go, tools, source, role, case, mode, work, directory/mode/role)
                       for role, source in arms.items()} for mode in ("stock", "aligned")}
    save(directory/"alignment.json", driver["compare"](directory, case, cache=key=="cache"))
    comparisons = {}
    for mode, count in measure_modes(runtime, arms, binaries, case, directory).items():
        target = directory / mode
        gates = runtime.gate(tools, expected, arms["H"], target, case["package"], case["timed"])
        transcript = (target/"AH-1.forward.gate.txt").read_text()
        found = re.findall(r"^confirmed performance regression: (\S+) (\S+) ", transcript, re.M)
        comparisons[mode] = {"processes": count, "gates": gates, "confirmed": [list(item) for item in found]}
    return {"comparisons": comparisons, "outcome": classify(case, comparisons)}


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
                observed["results"][key] = run_case(runtime, driver, go, tools, arms, key, work)
                observed["processes"] += 240
        finally:
            for name in ("go.work", "go.work.sum"):
                (arms["A"]/name).unlink(missing_ok=True)
            (arms["A"]/".tmp").rmdir()
    save(OUT/"status.json", observed)
    require(all(i["outcome"]["controlsGreen"] and i["outcome"]["alignedExit"] == 0
                for i in observed["results"].values()), "same-arm controls failed or alignment remains red")


if __name__ == "__main__":
    main()
