import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
from textwrap import dedent


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def copy_assets(assets, evidence):
    expected = {
        "scope.json": "26045bb9ad3cf577843ce1ee286be7769f23f5bb655fb79f0697ceabdb33ece2",
        "benchmark-batch.tsv": "780a46565f475789ceccddaec7ea5cc538bd143ee07fa6ec19f37f8baa66f382",
        "benchmark-expected.json": "582b3af8f4bb2debe3193200554ea773bcdb57dfdb699d2130fe7b5920915031",
    }
    for name, digest in expected.items():
        if sha256(assets / name) != digest:
            raise ValueError(f"diagnostic asset hash mismatch: {name}")
        shutil.copyfile(assets / name, evidence / name)
    return json.loads((assets / "scope.json").read_text())


def apply_candidate(assets, work, evidence, scope):
    candidate = work / ".tmp/head"
    sources = scope["candidateFiles"]
    patch_bytes = json.loads((assets / "candidate-patch.json").read_text())["patch"].encode()
    if hashlib.sha256(patch_bytes).hexdigest() != "bbc4eb25c5b6b6ad9036f7da9429ae85a018c577e41660f380f33ec605182b1d":
        raise ValueError("candidate patch hash mismatch")
    patch = evidence / "candidate.patch"
    patch.write_bytes(patch_bytes)
    paths = re.findall(rb"^diff --git a/(\S+) b/\S+$", patch_bytes, re.M)
    if len(paths) != 11 or sorted(path.decode() for path in paths) != sorted(sources):
        raise ValueError("the 11-file candidate path set differs from the verified scope")
    env = dict(os.environ, GIT_CEILING_DIRECTORIES=str(candidate.parent))
    for args in (["--check"], []):
        subprocess.run(["git", "apply", *args, str(patch)], cwd=candidate, env=env, check=True)
    actual = {path: sha256(candidate / path) for path in sorted(sources)}
    combined = b"".join(path.encode() + b"\0" + bytes.fromhex(digest) for path, digest in actual.items())
    overlay = hashlib.sha256(combined).hexdigest()
    if actual != sources or overlay != scope["candidateOverlaySHA256"]:
        raise ValueError("candidate after applying patch differs from the verified scope")
    (evidence / "sources.json").write_text(json.dumps({"files": actual, "overlaySHA256": overlay}, indent=2) + "\n")

    fixture = scope["fixture"]
    source = assets / fixture["source"]
    if sha256(source) != fixture["sha256"]:
        raise ValueError("common Go fixture hash mismatch")
    shutil.copyfile(source, evidence / source.name)
    for side in ("base", "head"):
        destination = work / ".tmp" / side / fixture["copyToBothSides"]
        with destination.open("xb") as file:
            file.write(source.read_bytes())


def extract_steps(work, evidence):
    workflow = (work / ".github/workflows/test.yml").read_text().splitlines(keepends=True)
    steps = {
        "Install pinned benchstat": "install",
        "Compile benchmark binaries": "compile",
        "Run adaptive interleaved benchmark samples": "sample",
    }
    for name, slug in steps.items():
        starts = [i for i, line in enumerate(workflow) if line == f"      - name: {name}\n"]
        if len(starts) != 1:
            raise ValueError(f"expected one production workflow step named {name}")
        start = starts[0] + 1
        while workflow[start] != "        run: |\n":
            if workflow[start].startswith("      - "):
                raise ValueError(f"missing literal run block for {name}")
            start += 1
        start += 1
        end = start
        while end < len(workflow) and (not workflow[end].strip() or workflow[end].startswith("          ")):
            end += 1
        body = dedent("".join(workflow[start:end]))
        if slug == "sample":
            marker = "\nrun_group primary .tmp/benchmark-batch.tsv\n"
            if body.count(marker) != 1:
                raise ValueError("unexpected production sampler entry point")
            body = body.split(marker, 1)[0] + '\nrun_group "$1" .tmp/benchmark-batch.tsv\n'
        if not body.strip():
            raise ValueError(f"empty shell step for {name}")
        (work / ".tmp" / f"replay-{slug}.sh").write_text(body)
        (evidence / f"replay-{slug}.sh").write_text(body)


def main():
    assets, work, evidence = map(Path, sys.argv[1:])
    scope = copy_assets(assets, evidence)
    apply_candidate(assets, work, evidence, scope)
    extract_steps(work, evidence)


if __name__ == "__main__":
    main()
