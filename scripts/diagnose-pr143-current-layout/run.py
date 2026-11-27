#!/usr/bin/env python3
"""Recheck the remaining PR 143 timing on the original CPUs and profiles."""
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
OUT = ROOT / ".tmp/pr143-current-evidence"
CHECKOUT = ROOT / ".tmp/pr143-current-inputs"
HARNESS = ROOT / ".tmp/pr83-native-harness/scripts/diagnose-pr83-amd"
HARNESS_FILES = {
    "run.py": "c01ed27ff3b3a361e86495611507a7f298bbda1fdbbcb9fa7144a2309e722eea",
    "go-capture.py": "b93cb28f2b092f63a07543a71cd39d46f3f28044d73f3547aaf16078c2d07db9",
    "manifest.json": "0c12129c702cf833c79f9e8d3269d1cfb7171d7cd54b1b6255c63a0598bc09a0",
}
PARENT = "91cec71d14e067c1b687115518ca044a8c28e4c1"
HARNESS_REVISION = "5376367706e217b18a676dfe932004a0f5419a04"
REF = "refs/heads/diagnostics/pr143-current-layout"
LAYOUT_HELPER_COMMIT = "f9d0454826d1763b46e41eb26ef2a7aecdb86205"
LAYOUT_HELPER_SHA = "f7e1c7b985067778915c4a18a2a9293b6092d98a0dc94801152526aca56b40a0"
LAYOUT_HELPER = ROOT / ".tmp/pr143-layout-helper/scripts/diagnose-pr143-strict-native.py"
PACKAGE = "pkg/lux/luvia"
MANIFEST = INPUT / "manifest.json"
MANIFEST_SHA = "d033532e920d9324659a32e3f145f95f3b87750dccb7a2507a645704e0ee4d66"
CONFIG = json.loads(MANIFEST.read_bytes())
SLOTS = set(range(1, 6))
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
    paths = [".github/workflows/diagnose-pr143-current-layout.yml", str(Path(__file__).relative_to(ROOT))]
    require(git(ROOT, "show", "-s", "--format=%P", active) == PARENT and
            git(ROOT, "diff", "--name-status", PARENT, active).splitlines() ==
            ["M\t" + path for path in sorted(paths)], "unexpected diagnostic paths or parent")
    return active, slot


def target_case(cpu):
    return "latency" if CONFIG["routes"].get(cpu) == "latency" else ""


def cpu_families(cpuinfo):
    models = {line.split(":", 1)[1].strip() for line in cpuinfo.splitlines()
              if line.startswith("model name")}
    require(len(models) == 1, "unknown or mixed CPU model")
    cpu = models.pop()
    return cpu, target_case(cpu)


def route():
    active, slot = bind_event()
    cpu, case = cpu_families(Path("/proc/cpuinfo").read_text())
    if case:
        require(output("uname", "-sm").strip() == b"Linux x86_64" and
                'VERSION_ID="24.04"' in Path("/etc/os-release").read_text().splitlines() and
                output("getconf", "_NPROCESSORS_ONLN").strip() == b"4", "unexpected hosted environment")
    result = {"slot": slot, "cpu": cpu, "case": case, "diagnostic": active}
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
            require(sha(checkout / PACKAGE / name) == digest, f"changed hot-path fixture {name}")
    a, h = checkouts["A"], checkouts["H"]
    require(git(h, "show", "-s", "--format=%P", "HEAD").split() == CONFIG["H"]["parents"] and
            sha256(output("git", "--no-replace-objects", "-C", h, "diff", "--name-status",
                          CONFIG["A"]["commit"], "HEAD")).hexdigest() == CONFIG["diffSHA256"],
            "unexpected PR provenance")
    fields = ("GOVERSION", "GOHOSTOS", "GOHOSTARCH", "GOOS", "GOARCH", "GOAMD64", "CGO_ENABLED", "GOFLAGS", "GOTOOLCHAIN")
    compiler = output(go, "env", *fields)
    require(compiler == b"go1.27.1\nlinux\namd64\nlinux\namd64\nv1\n1\n\nlocal\n", "wrong compiler or flags")
    archived_h = work / "head"
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


def plan_route(runtime, tools, case):
    names = runtime.full_names(case, "timed")
    leaves = [{"Package": PACKAGE, "Name": name} for name in names]
    expected, targets, batch, confirmed = (OUT / p for p in ("expected.json", "targets.tsv", "batch.tsv", "confirmation.json"))
    save(expected, {"Base": leaves, "Head": leaves})
    targets.write_text("".join(f"{PACKAGE}\t{name}\n" for name in names))
    output(tools / "benchplan", "--confirmation-plan", expected, targets, batch, confirmed)
    output(tools / "benchplan", "--verify-profile-batch", "confirmation", confirmed, batch)
    selector = "|".join("/".join(f"^{part}$" for part in name[:-2].split("/")) for name in sorted(names))
    require(batch.read_text().splitlines() == [f"{PACKAGE}\t{case['profile']}\t{selector}"], "wrong archived selector/profile")
    return expected, selector, names


def latency_projection(source, workspace, case):
    package = source / PACKAGE
    require(sha(package / case["fixture"]) == CONFIG["sourceSHA256"][case["fixture"]], "changed latency source")
    original = (package / case["fixture"]).read_bytes()
    begin = original.index(b"func BenchmarkLatencyPercentiles")
    end = original.index(b"\n\nfunc TestStaticMetricsJSON", begin)
    fixture = INPUT / "latency-fixture.txt"
    content = fixture.read_bytes()
    require(sha(fixture) == case["projectionSHA256"] and
            content == b'package luvia\n\nimport (\n\t"slices"\n\t"sort"\n\t"testing"\n)\n\n' + original[begin:end] + b"\n",
            "projection changed the signed benchmark fixture")
    target = workspace / case["fixture"]
    target.write_bytes(content)
    files = sorted(package.glob("*_test.go"))
    mapping = {}
    for path in files:
        require(path.is_file() and not path.is_symlink() and
                not re.search(rb"(?m)^func\s+(TestMain|init)\s*\(", path.read_bytes()), "unknown Luvia test setup")
        mapping[str(path)] = str(target) if path.name == case["fixture"] else ""
    require(str(package / case["fixture"]) in mapping, "expected latency fixture not emitted")
    overlay = workspace / "overlay.json"
    save(overlay, {"Replace": mapping})
    return overlay


def evidence_for(signer, go, binary, case, prefix, work):
    (OUT / (prefix + ".elf.json")).write_text(json.dumps(signer["executable_fingerprints"](binary))+"\n")
    for name, command in {
        "objdump": (go, "tool", "objdump", "-s", case["hot"]),
        "build": (go, "version", "-m"),
        "buildid": (go, "tool", "buildid"),
        "readelf": ("readelf", "-WhlS"),
    }.items():
        (OUT / (prefix + "." + name + ".txt")).write_bytes(output(*command, binary))
    rows = output(go, "tool", "nm", "-size", "-sort", "address", binary).decode().splitlines()
    hot = [row for row in rows if len(row.split()) >= 4 and row.split()[2] in ("T", "t") and re.search(case["hot"], row)]
    symbols = OUT / (prefix + ".symbols.txt")
    symbols.write_text("\n".join(hot)+"\n")
    save(OUT / (prefix + ".symbols.json"), signer["symbol_fingerprints"](binary, symbols, OUT / (prefix + ".elf.json")))
    if case["root"] == "BenchmarkGatewayIdentity":
        addresses = work / "address-map.txt"
        addresses.write_text("\n".join(row for row in rows if len(row.split()) >= 4 and row.split()[2] in ("T", "t"))+"\n")
        signer["nearby_text_map"](addresses, OUT / (prefix + ".address-map.txt"))


def prepare_binary(runtime, signer, go, tools, arms, role, mode, case, work):
    source = arms[role]
    side = "base" if role == "A" else "head"
    prefix = side + ("64" if mode == "aligned" else "-projected" if mode == "projected" else "")
    built = work / ("binary-" + prefix)
    built.mkdir()
    capture = OUT / "compile" / prefix
    capture.mkdir(parents=True)
    wrapper = work / "wrapper/go"
    env = dict(os.environ, PATH=str(wrapper.parent)+os.pathsep+os.environ["PATH"],
               LUXO_WITNESS_REAL_GO=go, LUXO_WITNESS_PROFILE_LOG=str(capture), GOWORK=str(source/"go.work"),
               GOFLAGS="-ldflags=-funcalign=64" if mode == "aligned" else "")
    profile = case["profile"]
    stem = "pkg-lux-luvia" + ("" if profile == "default" else "-"+profile)
    binary = built / (stem + ".test")
    if mode == "projected":
        overlay = latency_projection(source, built, case)
        runtime.call(wrapper, "test", "-trimpath", "-c", "-overlay="+str(overlay),
                     "./"+PACKAGE, "-o", binary, cwd=source, env=env)
    else:
        cmd = "compile-performance-baseline" if role == "A" else "compile-performance"
        runtime.call(tools/"benchrun", cmd, source, built, PACKAGE, profile, env=env)
    captured = json.loads((capture/(stem+".overlay.json")).read_bytes())
    if mode == "projected" or profile != "default":
        expect = {PACKAGE + "/" + path.name: case["projectionSHA256"] if path.name == case["fixture"] else ""
                  for path in (source/PACKAGE).glob("*_test.go")}
        require(captured == expect, "unexpected effective test-only overlay")
        require(output(binary, "-test.list=.*", cwd=source/PACKAGE).decode().splitlines() == [case["root"]],
                "isolated binary introduced another test")
    else:
        require(captured == {}, "unexpected overlay on the stock Luvia binary")
    evidence_for(signer, go, binary, case, prefix, work)
    raw = runtime.sample(binary, source, PACKAGE, "^"+case["root"]+"$", "1x")
    (capture/"full-inventory.raw").write_bytes(raw)
    runtime.inventory(raw, runtime.full_names(case, "leaves"), runtime.CONFIG["cpu"], PACKAGE, one_iteration=True)
    return binary


def normalized_latency(signer):
    layouts = {}
    for prefix in ("base", "head", "base-projected", "head-projected"):
        lines = (OUT/(prefix+".objdump.txt")).read_text().split("TEXT ")[1:]
        keys = {}
        for section in lines:
            name, *rows = section.splitlines()
            instructions = []
            for row in filter(str.strip, rows):
                _, address, encoding, decoded = row.split(maxsplit=3)
                addr = int(address, 16)
                instructions.append((addr, signer["instruction_without_relocation"](encoding, decoded.strip())))
            if instructions:
                begin = instructions[0][0]
                symbol = name.split()[0].removesuffix("(SB)")
                require(symbol not in keys, "repeated disassembly symbol")
                keys[symbol] = [[address-begin, *op] for address, op in instructions]
        require(keys, "missing normalized latency instructions")
        layouts[prefix] = keys

    def same(a, h):
        return {key: layouts[a][key] == layouts[h][key] for key in sorted(layouts[a].keys() & layouts[h].keys())}

    save(OUT/"normalized-latency.json", {"instructions": layouts,
        "stockCompare": same("base","head"), "projectedCompare": same("base-projected","head-projected"),
        "AStockToProjected": same("base","base-projected"), "HStockToProjected": same("head","head-projected")})


def measure_modes(runtime, arms, binaries, selector, names):
    modes = tuple(binaries)
    counts = dict.fromkeys(modes, 0)
    for mode in modes:
        (OUT / mode).mkdir()
    with (OUT / "order.tsv").open("x", buffering=1) as order:
        order.write("process\tblock\tpair\tcomparison\tmode\tside\tarm\n")
        for block, index, comparison, matching in runtime.schedule():
            for mode in modes if (block + index) % 2 == 0 else modes[::-1]:
                for side in matching:
                    label, role = ("left", "right")[side], comparison[side]
                    line = (sum(counts.values()) + 1, block, index, comparison, mode, label, role)
                    order.write("\t".join(map(str, line)) + "\n")
                    data = runtime.sample(binaries[mode][role], arms[role], PACKAGE, selector, "200ms")
                    runtime.inventory(data, names, runtime.CONFIG["cpu"], PACKAGE)
                    with (OUT / mode / f"{comparison}-{block}.{label}.raw").open("ab") as sink:
                        sink.write(data)
                    counts[mode] += 1
    require(all(count == 120 for count in counts.values()), "invalid scheduled sample count")
    return counts


def classify(case, modes):
    spec = CONFIG["cases"][case]
    alternative = "aligned" if case == "gateway" else "projected"
    require(modes.keys() == {"stock", alternative}, "missing compiler arms")
    prefix = "github.com/light-speak/luxo/"+PACKAGE+"/"+spec["root"].removeprefix("Benchmark")+"/"
    expected = {prefix+sample+"-2" for sample in spec["confirmed"]}
    known = {prefix+sample+"-2" for sample in spec["timed"]}
    for mode in modes.values():
        gates = mode["gates"]
        confirmed = [tuple(row) for row in mode["confirmed"]]
        require(mode["processes"] == 120 and gates.keys() == PAIRS | {"AH-1.forward"} and
                all(type(code) is int and code in (0,1) for code in gates.values()) and
                bool(confirmed) == bool(gates["AH-1.forward"]) and len(set(confirmed)) == len(confirmed) and
                all(name in known and unit in ("sec/op","B/op","allocs/op") for name,unit in confirmed), "invalid gate receipt")
    stock = modes["stock"]["confirmed"]
    original = {name for name, unit in stock if unit == "sec/op"} == expected and all(row[1] == "sec/op" for row in stock)
    return {"controlsGreen": all(mode["gates"][name] == 0 for mode in modes.values() for name in PAIRS),
            "originalReproduced": original, "alternateExit": modes[alternative]["gates"]["AH-1.forward"]}


def summarize(directory, diagnostic):
    files = sorted(directory.glob("*/status.json"))
    require(len(files) == len(SLOTS), "missing slot receipt")
    results, slots = {"latency": []}, set()
    for path in files:
        status = json.loads(path.read_bytes())
        expected = target_case(status["cpu"])
        slot = status["slot"]
        require(slot in SLOTS-slots and status["diagnostic"] == diagnostic and status["case"] == expected and
                status["processes"] == (240 if expected else 0), "invalid route receipt")
        slots.add(slot)
        if expected:
            computed = classify(expected, status["comparisons"])
            require(computed == status["outcome"], "unstable gate outcome")
            results[expected].append({"slot": slot, **computed})
        else:
            require(not status["comparisons"], "samples from unmatched CPU")
    print(json.dumps(results, sort_keys=True))
    require(all(items and all(item["controlsGreen"] and item["alternateExit"] == 0 for item in items)
                for items in results.values()), "matched case missing, controls failed or alternate still regressed")
    require(all(any(item["originalReproduced"] for item in items) for items in results.values()),
            "original timing signal not reproduced in each case")


def main():
    require(sha(MANIFEST) == MANIFEST_SHA, "signed native manifest changed")
    args = sys.argv[1:]
    if len(args) == 3 and args[0] == "--summarize":
        summarize(Path(args[1]), args[2])
        return
    host = route()
    observed = {"comparisons": {}, "processes": 0, **host}
    if args == ["--probe"]:
        matched = bool(host["case"])
        with Path(os.environ["GITHUB_OUTPUT"]).open("a") as log:
            log.write("matched="+str(matched).lower()+"\n")
        if not matched:
            save(OUT/"status.json", observed)
        return
    require(not args and host["case"], "CPU does not match the original scope")
    require(git(HARNESS,"rev-parse","HEAD") == HARNESS_REVISION, "wrong native harness checkout")
    require(git(LAYOUT_HELPER.parent,"rev-parse","HEAD") == LAYOUT_HELPER_COMMIT and
            sha(LAYOUT_HELPER) == LAYOUT_HELPER_SHA, "wrong layout verifier checkout")
    runtime = native_harness(HARNESS, host["cpu"])
    signer = runpy.run_path(str(LAYOUT_HELPER))
    go = shutil.which("go")
    require(go, "Go not found")
    scope = host["case"]
    case = CONFIG["cases"][scope]
    modes = ("stock", "aligned" if scope == "gateway" else "projected")
    with TemporaryDirectory(prefix="pr143-current.", dir=OUT.parent) as folder:
        work = Path(folder)
        arms = arm_sources(go, work)
        try:
            tools = runtime.build_tools(go, arms["H"], work)
            expected, selector, names = plan_route(runtime, tools, case)
            binaries = {mode: {role: prepare_binary(runtime, signer, go, tools, arms, role, mode, case, work)
                               for role in ("A","H")} for mode in modes}
            if scope == "gateway":
                signer["verify_alignment"](OUT, current_source=True)
            else:
                normalized_latency(signer)
            for mode, count in measure_modes(runtime, arms, binaries, selector, names).items():
                directory = OUT/mode
                gates = runtime.gate(tools, expected, arms["H"], directory, PACKAGE, names)
                raw = (directory/"AH-1.forward.gate.txt").read_text()
                confirmed = re.findall(r"^confirmed performance regression: (\S+) (\S+) ", raw, re.M)
                observed["comparisons"][mode] = {"processes": count, "gates": gates, "confirmed": confirmed}
                observed["processes"] += count
        finally:
            for name in ("go.work","go.work.sum"):
                (arms["A"]/name).unlink(missing_ok=True)
            (arms["A"]/".tmp").rmdir()
    outcome = observed["outcome"] = classify(scope, observed["comparisons"])
    save(OUT/"status.json", observed)
    require(outcome["controlsGreen"] and outcome["alternateExit"] == 0,
            "controls failed or alternate still regressed")


if __name__ == "__main__":
    main()
