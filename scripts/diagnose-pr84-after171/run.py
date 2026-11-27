#!/usr/bin/env python3
"""Bind the shared linker witness to the exact PR 84 result."""

from hashlib import sha256
import importlib.util
import json
import os
from pathlib import Path


INPUT = Path(__file__).resolve().parent
ROOT = INPUT.parents[1]
DRIVER = ROOT / "scripts/diagnose-pr83-bc18-native-layout"
MANIFEST = INPUT / "manifest.json"
PARENT = "b5de982bbd569511d9047a3273ff94e012dd16e5"
REF = "refs/heads/diagnostics/pr84-after171-native-layout"
DRIVER_FILES = {
    "run.py": "cd64c21d7861497c3dacaf87e581ee4ef1e8bb796198cf6417d5af5f1377dd00",
    "layout.py": "e4e0f9b7ddf1c8dcfc06e6a7e52bb2daf35c5170534ba6ce8f4749472d502533",
}
MANIFEST_SHA = "430127f6dd112f5f8df4302ab16ad7a2469c74e800e6946953aba7cea5a997a6"
PATHS = [
    "A\t.github/workflows/diagnose-pr84-after171-native-layout.yml",
    "M\tscripts/diagnose-pr83-bc18-native-layout/run.py",
    "A\tscripts/diagnose-pr84-after171/manifest.json",
    "A\tscripts/diagnose-pr84-after171/run.py",
]


def bind(driver):
    active = driver.git(ROOT, "rev-parse", "HEAD")
    slot = int(os.environ["SLOT"])
    driver.require(slot in driver.SLOTS and os.environ.get("RUNNER_KIND") == "github-hosted" and
                   os.environ.get("GITHUB_REPOSITORY") == "light-speak/luxo" and
                   os.environ.get("GITHUB_EVENT_NAME") == "push" and os.environ.get("GITHUB_REF") == REF and
                   os.environ.get("GITHUB_SHA") == active, "unexpected diagnostic event")
    driver.require(driver.git(ROOT, "show", "-s", "--format=%P", active) == PARENT and
                   driver.git(ROOT, "diff", "--name-status", PARENT, active).splitlines() == PATHS,
                   "unexpected diagnostic parent or paths")
    return active, slot


def load_driver():
    for name, digest in DRIVER_FILES.items():
        if sha256((DRIVER / name).read_bytes()).hexdigest() != digest:
            raise RuntimeError(f"shared native witness changed: {name}")
    if sha256(MANIFEST.read_bytes()).hexdigest() != MANIFEST_SHA:
        raise RuntimeError("signed native manifest changed")
    spec = importlib.util.spec_from_file_location("layout_witness", DRIVER / "run.py")
    driver = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(driver)
    driver.MANIFEST = MANIFEST
    driver.MANIFEST_SHA = MANIFEST_SHA
    driver.CONFIG = json.loads(MANIFEST.read_bytes())
    driver.OUT = ROOT / ".tmp/pr84-after171-native-evidence"
    driver.CHECKOUT = ROOT / ".tmp/pr84-after171-native-inputs"
    driver.HARNESS = ROOT / ".tmp/pr84-native-harness/scripts/diagnose-pr83-amd"
    driver.TARGET_CASES = set(driver.CONFIG["case"])
    driver.bind_event = lambda: bind(driver)
    return driver


if __name__ == "__main__":
    load_driver().main()
