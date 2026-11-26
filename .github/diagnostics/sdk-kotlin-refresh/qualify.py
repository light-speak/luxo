#!/usr/bin/env python3
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import statistics
import subprocess
import tempfile

SCOPE = Path(__file__).resolve().parent
ENTRY = "com.luxo.client.HttpSingleFlightBenchmark"
MODES = ("JSON", "BINARY")
CASES = ("no-callback", "callback-ready", "refresh-once")
LEAVES = ((mode, case) for mode in MODES for case in CASES)
LEAF_SET = set(LEAVES)
METRICS = ("wall_ns", "caller_cpu_ns", "dispatcher_cpu_ns", "caller_B", "dispatcher_B")
VM_FLAGS = ("-Xms512m", "-Xmx512m", "-XX:-UsePerfData")


def sha256_file(path):
    digest = hashlib.sha256()
    with path.open("rb") as reader:
        for chunk in iter(lambda: reader.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def binding_for(build):
    metadata = (build / "build-metadata.txt").read_text().splitlines()
    fields = dict(line.split("=", 1) for line in metadata if "=" in line and not line.startswith(" "))
    java = Path(fields["selected_java"])
    fingerprints = {}
    external = None
    for arm in ("base", "candidate"):
        root = build / arm
        entries = [Path(name) for name in (root / "classpath.txt").read_text().strip().split(os.pathsep)]
        libraries = []
        for entry in entries:
            if not entry.is_relative_to(root):
                assert entry.is_file(), entry
                libraries.append((str(entry), sha256_file(entry)))
        if external is None:
            external = libraries
            names = {Path(name).name for name, _ in libraries}
            assert {"okhttp-4.12.0.jar", "kotlinx-coroutines-core-jvm-1.9.0.jar",
                    "kotlinx-serialization-json-jvm-1.7.0.jar"} <= names, names
        else:
            assert libraries == external, "the runtime dependency selections differ"
        sdk = root / "packages/kotlin"
        transport = sdk / "src/main/kotlin/com/luxo/client/Transport.kt"
        assert fields["baseline_transport" if arm == "base" else "candidate_transport"] == sha256_file(transport)
        fixture = sdk / "src/test/kotlin/com/luxo/client/HttpSingleFlightBenchmark.kt"
        assert sha256_file(fixture) == sha256_file(SCOPE / "HttpSingleFlightBenchmark.kt")
        classes = sorted((sdk / "build/classes").rglob("*.class"))
        assert any(path.name == "OkHttpTransport.class" for path in classes)
        assert any(path.name == "HttpSingleFlightBenchmark.class" for path in classes)
        manifest = "".join(f"{sha256_file(path)} {path.relative_to(sdk)}\n" for path in classes)
        fingerprints[arm] = hashlib.sha256(manifest.encode()).hexdigest()
    lock = [tuple(line.split("\t")) for line in (SCOPE / "runtime-sha256.tsv").read_text().splitlines()]
    assert len(lock) == len(external) == 24
    assert sorted(lock) == sorted((Path(name).name, digest) for name, digest in external), "runtime JARs changed"
    return java, {"source": fields, "java_version": metadata[5:8], "class_manifest_sha256": fingerprints,
                  "dependencies": [{"name": Path(name).name, "sha256": digest} for name, digest in external],
                  "jvm_flags": VM_FLAGS}


def run_arm(build, java, arm, mode, iterations, rotation):
    root = build / arm
    classpath = (root / "classpath.txt").read_text().strip()
    command = [str(java), *VM_FLAGS, f"-Djava.io.tmpdir={root / 'system-tmp'}", "-cp", classpath,
               ENTRY, mode, str(iterations), str(rotation)]
    result = subprocess.run(command, cwd=root / "packages/kotlin", text=True, check=False, capture_output=True, timeout=100)
    if result.returncode != 0:
        raise RuntimeError(f"{arm} {mode} exited {result.returncode}\n{result.stdout}\n{result.stderr}")
    rows = {}
    for line in result.stdout.splitlines():
        parts = line.split("\t")
        assert len(parts) == (8 if mode == "smoke" else 13) and parts[0] == mode, line
        leaf = tuple(parts[1:3])
        assert leaf in LEAF_SET and leaf not in rows, line
        counts = tuple(map(int, parts[3:8]))
        if leaf[1] == "refresh-once":
            expected = (iterations, iterations * 2, iterations, iterations, iterations)
        else:
            expected = (iterations, iterations, 0, iterations, 0)
        assert counts == expected, (leaf, counts, expected)
        sample = dict(zip(("operations", "requests", "unauthorized", "successful", "refreshes"), counts))
        if mode == "sample":
            metrics = dict(zip(METRICS, map(float, parts[8:])))
            assert all(math.isfinite(value) and value >= 0 for value in metrics.values())
            metrics["measured_B"] = metrics["caller_B"] + metrics["dispatcher_B"]
            metrics["measured_cpu_ns"] = metrics["caller_cpu_ns"] + metrics["dispatcher_cpu_ns"]
            sample["metrics"] = metrics
        rows[leaf] = sample
    assert set(rows) == LEAF_SET, rows
    result_rows = [{"protocol": leaf[0], "case": leaf[1], **row} for leaf, row in sorted(rows.items())]
    return {"role": arm, "exit": result.returncode, "rows": result_rows, "stdout": result.stdout, "stderr": result.stderr}


def paths_for_sample(pairs):
    for group in (1, 2):
        for index in range(pairs):
            reverse = (group + index) % 2 == 0
            comparisons = ["AA", "AB"] if reverse else ["AB", "AA"]
            comparisons.insert((index + (group - 1) * pairs) % 3, "BB")
            for comparison in comparisons:
                yield group, index + 1, (index + (group - 1) * pairs) % len(LEAF_SET), comparison, reverse


def paired_summary(samples):
    groups = {}
    for pair in samples:
        for base, comparison in zip(pair["arms"]["left"]["rows"], pair["arms"]["right"]["rows"]):
            assert (base["protocol"], base["case"]) == (comparison["protocol"], comparison["case"])
            key = (pair["group"], pair["comparison"], base["protocol"], base["case"])
            entries = groups.setdefault(key, {})
            for name, value in base["metrics"].items():
                entries.setdefault(name, []).append((value, comparison["metrics"][name]))
    result = []
    for key, entries in sorted(groups.items()):
        item = dict(zip(("group", "comparison", "protocol", "case"), key))
        values = {}
        for metric, pairs in entries.items():
            assert len(pairs) == 10
            differences = [right - left for left, right in pairs]
            values[metric] = {"left_median": statistics.median(left for left, _ in pairs),
                              "right_median": statistics.median(right for _, right in pairs),
                              "paired_min": min(differences), "paired_median": statistics.median(differences),
                              "paired_max": max(differences)}
        result.append({**item, "metrics": values})
    return result


def qualify(mode, build, destination):
    java, binding = binding_for(build)
    if mode == "smoke":
        results = [run_arm(build, java, arm, "smoke", 1, 0) for arm in ("base", "candidate")]
        assert results[0]["rows"] == results[1]["rows"]
        receipt = {"binding": binding, "status": "semantically equal", "arms": results}
        destination.write_text(json.dumps(receipt, indent=2) + "\n")
        return
    assert not destination.exists()
    destination.mkdir()
    (destination / "binding.json").write_text(json.dumps(binding, indent=2) + "\n")
    samples = []
    with (destination / "pairs.jsonl").open("x") as output:
        for group, pair, rotation, comparison, reverse in paths_for_sample(10):
            roles = {"left": "candidate" if comparison == "BB" else "base",
                     "right": "base" if comparison == "AA" else "candidate"}
            order = ("right", "left") if reverse else ("left", "right")
            arms = {side: run_arm(build, java, roles[side], "sample", 256, rotation) for side in order}
            item = dict(group=group, pair=pair, comparison=comparison, order=order, rotation=rotation, arms=arms)
            samples.append(item)
            output.write(json.dumps(item) + "\n")
            output.flush()
            print(f"group {group} pair {pair}/10 {comparison} complete", flush=True)
    (destination / "summary.json").write_text(json.dumps(paired_summary(samples), indent=2) + "\n")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("mode", choices=("smoke", "measure"))
    parser.add_argument("--build-dir", type=Path)
    parser.add_argument("--out", required=True, type=Path)
    args = parser.parse_args()
    destination = args.out.resolve()
    assert destination.parent == SCOPE and not destination.exists(), destination
    if args.build_dir:
        build = args.build_dir.resolve()
        assert build.parent == SCOPE and build.name.startswith("run."), build
        qualify(args.mode, build, destination)
    else:
        with tempfile.TemporaryDirectory(prefix="run.", dir=SCOPE) as path:
            build = Path(path)
            subprocess.run([str(SCOPE / "prepare.sh"), str(build)], check=True)
            qualify(args.mode, build, destination)


if __name__ == "__main__":
    main()
