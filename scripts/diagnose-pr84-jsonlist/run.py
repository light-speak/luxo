#!/usr/bin/env python3
"""Recheck the remaining PR 84 JSONList signal with a signed test-only projection."""

from hashlib import sha256
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
PARENT = "fedb4b09381246500d916356f0eec56b573352b7"
REF = "refs/heads/diagnostics/pr84-jsonlist-7763"
CPU = "AMD EPYC 7763 64-Core Processor"
KEY = "json-list"
SLOTS = set(range(1, 5))
PATHS = [
    "A\t.github/workflows/diagnose-pr84-jsonlist-7763.yml",
    *["A\tscripts/diagnose-pr84-jsonlist/" + name
      for name in ("capture.py", "fixture.json", "fixture.py", "run.py")],
]
FIXTURE_SHA = "fd9f95ab5d78c11fa10a2170d65a812d4c66d9f91628dc822e219dae0a1b65da"


def bind(driver):
    active = driver.git(ROOT, "rev-parse", "HEAD")
    slot = int(os.environ["SLOT"])
    driver.require(slot in SLOTS and os.environ.get("RUNNER_KIND") == "github-hosted" and
                   os.environ.get("GITHUB_REPOSITORY") == "light-speak/luxo" and
                   os.environ.get("GITHUB_EVENT_NAME") == "push" and os.environ.get("GITHUB_REF") == REF and
                   os.environ.get("GITHUB_SHA") == active, "unexpected JSONList diagnostic event")
    driver.require(driver.git(ROOT, "show", "-s", "--format=%P", active) == PARENT and
                   driver.git(ROOT, "diff", "--name-status", PARENT, active).splitlines() == PATHS,
                   "unexpected JSONList diagnostic parent or paths")
    return active, slot


def inputs():
    earlier = ROOT/"scripts/diagnose-pr84-after171/run.py"
    driver = runpy.run_path(str(earlier))["load_driver"]()
    signed = INPUT/"fixture.json"
    driver.require(sha256(signed.read_bytes()).hexdigest() == FIXTURE_SHA, "changed JSONList signature")
    manifest = json.loads(signed.read_bytes())
    config = driver.CONFIG
    case = config["case"][KEY]
    case["hot"] += r"|parseListObjects|metadataEnd|jsontext\.\(\*(Decoder|decoderState)\)\.(ReadToken|PeekKind)|jsonutil\.BorrowReader"
    driver.require(len(config["inventory"][KEY]) == 26 and len(case["timed"]) == 5 and
                   len(case["confirmed"]) == 5 and config["sourceSHA256"][manifest["path"]] == manifest["sourceSHA256"],
                   "unexpected original JSONList sample scope")
    driver.CONFIG = {**config, "routes": {CPU:[KEY]}, "case": {KEY:case}, "inventory": {KEY:config["inventory"][KEY]}}
    driver.OUT = ROOT/".tmp/pr84-jsonlist-native-evidence"
    driver.CHECKOUT = ROOT/".tmp/pr84-jsonlist-native-inputs"
    driver.TARGET_CASES = {KEY}
    driver.SLOTS = SLOTS
    driver.bind_event = lambda: bind(driver)
    return driver, case, manifest


def build_isolated(driver, capture, runtime, layout, go, source, case, manifest, work, evidence):
    evidence.mkdir(parents=True)
    package = case["package"]
    scratch = work/("isolated-" + evidence.name)
    scratch.mkdir()
    overlay, expected = runpy.run_path(str(INPUT/"fixture.py"))["overlay"](source, package, scratch, manifest)
    wrapper = work/"wrapper/go"
    environment = dict(os.environ, LUXO_WITNESS_REAL_GO=go, LUXO_WITNESS_PROFILE_LOG=str(evidence),
                       GOWORK=str(source/"go.work"), GOFLAGS="-x")
    target = scratch/"api-isolated.test"
    command = (wrapper, "test", "-trimpath", "-c", "-overlay="+str(overlay), "./"+package, "-o", target)
    trace = scratch/"go-build.trace"
    with trace.open("xb") as stream:
        result = subprocess.run(command, cwd=source, env=environment, stderr=stream)
    if result.returncode != 0:
        raise RuntimeError(f"isolated compilation failed: {trace.read_text(errors='replace')[-4000:]}")
    linker = Path(driver.output(go, "env", "GOTOOLDIR").decode().strip())/"link"
    driver.save(evidence/"link-command.json", layout["link_invocation"](trace, linker, "api.test", False))
    trace.unlink()
    runtime.assert_overlay(evidence, target.stem, expected)
    listed = driver.output(target, "-test.list=.*", cwd=source/package).decode().splitlines()
    driver.require(listed == ["BenchmarkJSONListMetadata"], "isolated JSONList acquired a sibling test or benchmark")
    raw = runtime.sample(target, source, package, "^BenchmarkJSONListMetadata$", "1x")
    (evidence/"inventory.raw").write_bytes(raw)
    runtime.inventory(raw, driver.CONFIG["inventory"][KEY], runtime.CONFIG["cpu"], package, one_iteration=True)
    runtime.inspect_binary(go, target, evidence, case["hot"], work)
    driver.save(evidence/"linked-scope.json", capture(target, evidence))
    return target


def result(driver, case, modes):
    checked = driver.classify(case, {"stock":modes["stock"], "aligned":modes["isolated"]})
    original = set(case["confirmed"])
    timed_zero = {name for name in original if name.endswith("/0/metadata-2")}
    confirmed = {name for name, unit in modes["stock"]["confirmed"] if unit == "sec/op"}
    driver.require(len(timed_zero) == 2, "changed signed empty-list cases")
    return {"controlsGreen":checked["controlsGreen"], "isolatedExit":checked["alignedExit"],
            "stockResidualReproduced": timed_zero <= confirmed <= original and
            len(confirmed) == len(modes["stock"]["confirmed"]),
            "stockAllFiveReproduced": checked["originalReproduced"]}


def run_group(driver, case, manifest, runtime, go, tools, arms, work):
    layout = runpy.run_path(str(driver.INPUT/"layout.py"))
    capture = runpy.run_path(str(INPUT/"capture.py"))["inspect"]
    directory = driver.OUT/KEY
    directory.mkdir()
    expected = driver.plan_route(tools, directory, case)
    binaries = {"stock":{}, "isolated":{}}
    for role, source in arms.items():
        stock = directory/"stock"/role
        binary = driver.compile_binary(runtime, layout, go, tools, source, role, case, "stock", work, stock)
        driver.save(stock/"linked-scope.json", capture(binary, stock))
        binaries["stock"][role] = binary
        binaries["isolated"][role] = build_isolated(driver, capture, runtime, layout, go, source, case, manifest,
                                                    work, directory/"isolated"/role)
    modes = {}
    for mode, count in driver.measure_modes(runtime, arms, binaries, case, directory).items():
        path = directory/mode
        gates = runtime.gate(tools, expected, arms["H"], path, case["package"], case["timed"])
        found = re.findall(r"^confirmed performance regression: (\S+) (\S+) ",
                           (path/"AH-1.forward.gate.txt").read_text(), re.M)
        modes[mode] = {"processes":count, "gates":gates, "confirmed":[list(row) for row in found]}
    return {"comparisons":modes, "outcome":result(driver, case, modes)}


def summarize(driver, case, directory, diagnostic):
    seen = set()
    matched = []
    for path in sorted(directory.glob("*/status.json")):
        status = json.loads(path.read_bytes())
        slot = status["slot"]
        cases = driver.target_cases(status["cpu"])
        driver.require(slot in SLOTS-seen and status["diagnostic"] == diagnostic and status["cases"] == cases and
                       status["processes"] == 240*len(cases) and set(status["results"]) == set(cases),
                       "invalid JSONList slot receipt")
        seen.add(slot)
        if cases:
            item = status["results"][KEY]
            outcome = result(driver, case, item["comparisons"])
            driver.require(outcome == item["outcome"], "unstable JSONList result")
            for profile in ("stock", "isolated"):
                for role in ("A", "H"):
                    report = path.parent/KEY/profile/role/"linked-scope.json"
                    capture = json.loads(report.read_bytes())
                    driver.require(capture["numericTypeRead"]["targetBytes"] and capture["functions"],
                                   "missing native type or branch report")
            matched.append({"slot":slot, **outcome})
    print(json.dumps(matched, sort_keys=True))
    driver.require(seen == SLOTS and matched and all(item["controlsGreen"] and item["isolatedExit"] == 0 for item in matched)
                   and any(item["stockResidualReproduced"] for item in matched),
                   "missing EPYC 7763, unstable controls, isolation still fails or residual did not reproduce")


def main(args):
    driver, case, manifest = inputs()
    if len(args) == 3 and args[0] == "--summarize":
        summarize(driver, case, Path(args[1]), args[2])
        return
    host = driver.route()
    report = {"results":{}, "processes":0, **host}
    if args == ["--probe"]:
        with Path(os.environ["GITHUB_OUTPUT"]).open("a") as stream:
            stream.write("matched=" + str(bool(host["cases"])).lower() + "\n")
        if not host["cases"]:
            driver.save(driver.OUT/"status.json", report)
        return
    driver.require(not args and host["cases"] == [KEY], "CPU does not match the original JSONList scope")
    driver.require(driver.git(driver.HARNESS, "rev-parse", "HEAD") == driver.HARNESS_REVISION, "wrong shared native harness")
    runtime = driver.native_harness(driver.HARNESS, host["cpu"])
    go = shutil.which("go")
    driver.require(go, "Go not found")
    with TemporaryDirectory(prefix="pr84-jsonlist-native.", dir=driver.OUT.parent) as folder:
        work = Path(folder)
        arms = driver.arm_sources(go, work)
        try:
            tools = runtime.build_tools(go, arms["H"], work)
            report["results"][KEY] = run_group(driver, case, manifest, runtime, go, tools, arms, work)
            report["processes"] = 240
        finally:
            for name in ("go.work", "go.work.sum"):
                (arms["A"]/name).unlink(missing_ok=True)
            (arms["A"]/".tmp").rmdir()
    driver.save(driver.OUT/"status.json", report)


if __name__ == "__main__":
    main(sys.argv[1:])
