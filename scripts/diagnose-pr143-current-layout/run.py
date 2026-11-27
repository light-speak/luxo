#!/usr/bin/env python3
"""Recheck the two gateway identity timing signals on the original Intel CPU."""
from hashlib import sha256
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import runpy
import shlex
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
PARENT = "5a17f908912055c353cb9acd6f4813821c1d3a42"
HARNESS_REVISION = "5376367706e217b18a676dfe932004a0f5419a04"
REF = "refs/heads/diagnostics/pr143-current-layout"
LAYOUT_HELPER_COMMIT = "f9d0454826d1763b46e41eb26ef2a7aecdb86205"
LAYOUT_HELPER_SHA = "f7e1c7b985067778915c4a18a2a9293b6092d98a0dc94801152526aca56b40a0"
LAYOUT_HELPER = ROOT / ".tmp/pr143-layout-helper/scripts/diagnose-pr143-strict-native.py"
PACKAGE = "pkg/lux/luvia"
MANIFEST = INPUT / "manifest.json"
MANIFEST_SHA = "b902e8a90dda5455c6b8b3b5f432923db89992524474c8e50c2eae05b4e06a5d"
CONFIG = json.loads(MANIFEST.read_bytes())
CASE = CONFIG["case"]
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
    paths = [".github/workflows/diagnose-pr143-current-layout.yml", str(Path(__file__).relative_to(ROOT)),
             str(MANIFEST.relative_to(ROOT))]
    require(git(ROOT, "show", "-s", "--format=%P", active) == PARENT and
            git(ROOT, "diff", "--name-status", PARENT, active).splitlines() ==
            ["M\t" + path for path in sorted(paths)], "unexpected diagnostic paths or parent")
    return active, slot


def target_case(cpu):
    return "gateway" if cpu == CONFIG["cpu"] else ""


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
        projection = CONFIG["projectionCompiler"]
        require(sha(checkout / projection["path"]) == projection["sha256"], "changed profile compiler")
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
    require(batch.read_text() == CONFIG["originalBatch"]+"\n" and
            CONFIG["originalBatch"] == f"{PACKAGE}\t{case['profile']}\t{selector}", "wrong archived selector/profile")
    return expected, selector, names


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
    addresses = work / "address-map.txt"
    addresses.write_text("\n".join(row for row in rows if len(row.split()) >= 4 and row.split()[2] in ("T", "t"))+"\n")
    signer["nearby_text_map"](addresses, OUT / (prefix + ".address-map.txt"))


def install_link_capture(work):
    proxy = work / "trace-go"
    proxy.write_text("""#!/usr/bin/env bash
set -eu
if [[ "$1" != test ]]; then
    exec "$TRACE_REAL_GO" "$@"
fi
if ! "$TRACE_REAL_GO" "$1" -x "${@:2}" 2>"$TRACE_FILE"; then
    tail -n 40 "$TRACE_FILE" >&2
    exit 1
fi
""")
    proxy.chmod(0o700)
    return proxy


def actual_link(trace, linker, test_name, aligned):
    commands = []
    with trace.open() as stream:
        for row in stream:
            if str(linker) not in row:
                continue
            args = shlex.split(row)
            if args[:1] == ["GOROOT="]:
                args.pop(0)
            if args[:1] == [str(linker)]:
                commands.append(args)
    require(len(commands) == 1, "expected one actual test link command")
    argv, = commands
    require(argv.count("-o") == argv.count("-importcfg") == 1 and "testing.testBinary=1" in argv and
            Path(argv[argv.index("-o") + 1]).name == test_name and
            Path(argv[argv.index("-importcfg") + 1]).name == "importcfg.link" and
            re.fullmatch(r"_pkg_\.a|[0-9a-f]{64}-d", Path(argv[-1]).name), "unexpected link invocation")
    flags = [arg for arg in argv if arg.startswith(("-funcalign", "--funcalign"))]
    require(flags == (["-funcalign=64"] if aligned else []), "unexpected effective alignment flag")
    volatile = {argv.index("-o")+1, argv.index("-importcfg")+1, len(argv)-1}
    normalized = [arg for index, arg in enumerate(argv) if index not in volatile and
                  not arg.startswith("-buildid=") and arg != "-funcalign=64"]
    return {"argv": argv, "normalized": normalized}


def prepare_binary(runtime, signer, go, tools, arms, role, mode, work):
    source = arms[role]
    aligned = mode == "aligned"
    side = "base" if role == "A" else "head"
    prefix = side + ("64" if aligned else "")
    built = work / ("binary-" + prefix)
    built.mkdir()
    capture = OUT / "compile" / prefix
    capture.mkdir(parents=True)
    wrapper = work / "wrapper/go"
    trace = work / f"{prefix}.trace"
    env = dict(os.environ, PATH=str(wrapper.parent)+os.pathsep+os.environ["PATH"],
               LUXO_WITNESS_REAL_GO=str(work / "trace-go"), LUXO_WITNESS_PROFILE_LOG=str(capture),
               TRACE_REAL_GO=go, TRACE_FILE=str(trace), GOWORK=str(source/"go.work"),
               GOFLAGS="-ldflags=-funcalign=64" if aligned else "")
    profile = CASE["profile"]
    stem = "pkg-lux-luvia-" + profile
    binary = built / (stem + ".test")
    cmd = "compile-performance-baseline" if role == "A" else "compile-performance"
    runtime.call(tools/"benchrun", cmd, source, built, PACKAGE, profile, env=env)
    linker = Path(output(go, "env", "GOTOOLDIR").decode().strip()) / "link"
    save(capture / "link.json", actual_link(trace, linker, "luvia.test", aligned))
    trace.unlink()
    captured = json.loads((capture/(stem+".overlay.json")).read_bytes())
    expected = {PACKAGE + "/" + path.name: CASE["projectionSHA256"] if path.name == CASE["fixture"] else ""
                for path in (source/PACKAGE).glob("*_test.go")}
    require(captured == expected, "unexpected effective test-only overlay")
    require(output(binary, "-test.list=.*", cwd=source/PACKAGE).decode().splitlines() == [CASE["root"]],
            "isolated binary introduced another test")
    evidence_for(signer, go, binary, CASE, prefix, work)
    raw = runtime.sample(binary, source, PACKAGE, "^"+CASE["root"]+"$", "1x")
    (capture/"full-inventory.raw").write_bytes(raw)
    runtime.inventory(raw, runtime.full_names(CASE, "leaves"), runtime.CONFIG["cpu"], PACKAGE, one_iteration=True)
    return binary


def verify_intervention(signer):
    signer["verify_alignment"](OUT, current_source=True)
    align = json.loads((OUT / "alignment-verification.json").read_bytes())
    before_a, after_a = align["base"]["identityEntryMod64"]
    before_h, after_h = align["head"]["identityEntryMod64"]
    require(align["stockIdentityInstructionsMatch"] and before_a != before_h and after_a == after_h == 0,
            "stock hot instructions or entry displacement do not support alignment attribution")
    for arm in ("base", "head"):
        commands = [json.loads((OUT/"compile"/prefix/"link.json").read_bytes()) for prefix in (arm, arm+"64")]
        require(commands[0]["normalized"] == commands[1]["normalized"], "linker changed more than alignment")


def measure(runtime, tools, arms, binaries, expected, selector, names, mode):
    directory = OUT / mode
    directory.mkdir()
    count = runtime.measure(arms, binaries, PACKAGE, selector, names, directory)
    gates = runtime.gate(tools, expected, arms["H"], directory, PACKAGE, names)
    report = (directory/"AH-1.forward.gate.txt").read_text()
    confirmed = re.findall(r"^confirmed performance regression: (\S+) (\S+) ", report, re.M)
    return {"processes": count, "gates": gates, "confirmed": confirmed}


def classify(mode):
    prefix = "github.com/light-speak/luxo/"+PACKAGE+"/"+CASE["root"].removeprefix("Benchmark")+"/"
    wanted = {(prefix+leaf+"-2", "sec/op") for leaf in CASE["confirmed"]}
    known = {prefix+leaf+"-2" for leaf in CASE["timed"]}
    gates = mode["gates"]
    confirmed = [tuple(row) for row in mode["confirmed"]]
    require(mode["processes"] == 120 and gates.keys() == PAIRS | {"AH-1.forward"} and
            all(type(code) is int and code in (0,1) for code in gates.values()) and
            bool(confirmed) == bool(gates["AH-1.forward"]) and len(set(confirmed)) == len(confirmed) and
            all(name in known and unit in ("sec/op","B/op","allocs/op") for name,unit in confirmed), "invalid gate receipt")
    return {"controlsGreen": all(gates[name] == 0 for name in PAIRS), "expectedReproduced": set(confirmed) == wanted,
            "crossExit": gates["AH-1.forward"]}


def summarize(directory, diagnostic):
    files = sorted(directory.glob("*/status.json"))
    require(len(files) == len(SLOTS), "missing slot receipt")
    results, slots = [], set()
    for path in files:
        status = json.loads(path.read_bytes())
        expected = target_case(status["cpu"])
        slot = status["slot"]
        require(slot in SLOTS-slots and status["diagnostic"] == diagnostic and status["case"] == expected,
                "invalid route receipt")
        slots.add(slot)
        if not expected:
            require(not status["comparisons"] and status["processes"] == 0, "samples from unmatched CPU")
            continue
        modes = status["comparisons"]
        stock = classify(modes["stock"])
        opened = stock["controlsGreen"] and stock["expectedReproduced"]
        require(set(modes) == ({"stock", "aligned"} if opened else {"stock"}) and
                status["processes"] == (240 if opened else 120), "invalid alignment state transition")
        aligned = classify(modes["aligned"]) if opened else None
        require(status["outcome"] == {"stock": stock, "aligned": aligned}, "unstable gate outcome")
        results.append({"slot": slot, "stock": stock, "aligned": aligned})
    print(json.dumps(results, sort_keys=True))
    require(results and all(item["stock"]["controlsGreen"] and
            (item["aligned"] is None and item["stock"]["crossExit"] == 0 or
             item["aligned"] and item["aligned"]["controlsGreen"] and item["aligned"]["crossExit"] == 0)
            for item in results), "matching CPU absent, an unclassified regression or control failed")
    require(any(item["aligned"] for item in results), "stock did not reproduce both original after signals")


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
    outcome = {"stock": None, "aligned": None}
    with TemporaryDirectory(prefix="pr143-current.", dir=OUT.parent) as folder:
        work = Path(folder)
        arms = arm_sources(go, work)
        try:
            tools = runtime.build_tools(go, arms["H"], work)
            install_link_capture(work)
            expected, selector, names = plan_route(runtime, tools, CASE)
            for mode in ("stock", "aligned"):
                binaries = {role: prepare_binary(runtime, signer, go, tools, arms, role, mode, work)
                            for role in ("A", "H")}
                if mode == "aligned":
                    verify_intervention(signer)
                result = measure(runtime, tools, arms, binaries, expected, selector, names, mode)
                observed["comparisons"][mode] = result
                observed["processes"] += result["processes"]
                outcome[mode] = classify(result)
                if mode == "stock":
                    save(OUT/"stock-status.json", {**observed, "outcome": outcome[mode]})
                    if not (outcome[mode]["controlsGreen"] and outcome[mode]["expectedReproduced"]):
                        break
        finally:
            for name in ("go.work","go.work.sum"):
                (arms["A"]/name).unlink(missing_ok=True)
            (arms["A"]/".tmp").rmdir()
    save(OUT/"status.json", {**observed, "outcome": outcome})
    require(outcome["stock"]["controlsGreen"], "stock controls failed")
    if outcome["aligned"] is None:
        require(outcome["stock"]["crossExit"] == 0, "stock reported a different strict regression")
    else:
        require(outcome["aligned"]["controlsGreen"] and outcome["aligned"]["crossExit"] == 0,
                "aligned controls failed or original timing still regressed")


if __name__ == "__main__":
    main()
