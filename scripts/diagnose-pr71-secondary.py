"""Bind the two #71 diagnostics to their original formal inputs and samples."""

import csv
import hashlib
import json
import math
from pathlib import Path
import sys
import zipfile


SCOPES = {
    "rpc-trace-local": {
        "cpu": "AMD EPYC 9V74 80-Core Processor",
        "package": "pkg/lux/rpc",
        "profile": "default",
        "fixture": "trace_local_bench_test.go",
        "fixture_sha": "bd60873a66ef965a610d9aecedb15921b7cd60d18889c3245dcacbd71719b8ce",
        "artifact": 14933040707,
        "archive_sha": "1537fc2100f5f36892a7c0eee3c50113a03c586e39c4c348410201443087bfca",
        "root": "BenchmarkRPCTraceLocal",
        "leaves": [f"{traced}/{revision}" for traced in ("false", "true") for revision in ("before", "current")],
        "confirmed": ["false/current"],
    },
    "gateway-identity": {
        "cpu": "Intel(R) Xeon(R) 6975P-C",
        "package": "pkg/lux/luvia",
        "profile": "luvia-gateway-identity",
        "fixture": "middleware_test.go",
        "fixture_sha": "a284f6673c86a90ed916099ac82a6ffb0e9b190161c946989957839252b07034",
        "artifact": 14932193613,
        "archive_sha": "afa3b31913d2c1ddfc04e5d96f6ddfe1deca85e229bc8fc12a4e09934e2e9c03",
        "root": "BenchmarkGatewayIdentity",
        "leaves": [f"{identity}/{revision}" for identity in ("anonymous", "authenticated", "typed-nil") for revision in ("after", "before")],
        "confirmed": ["authenticated/after", "authenticated/before"],
    },
}


def check(condition, message):
    if not condition:
        raise ValueError(message)


def write_json(path, content):
    path.write_text(json.dumps(content, indent=2) + "\n", encoding="utf-8")


def names(scope, key="leaves"):
    return sorted(f"{scope['root']}/{leaf}-2" for leaf in scope[key])


def rows(scope, text, expected, count, *, scoped=False):
    values = {leaf: [] for leaf in expected}
    headers = {"goos": set(), "goarch": set(), "pkg": set(), "cpu": set()}
    package = f"github.com/light-speak/luxo/{scope['package']}"
    active_package = ""
    for line in text.splitlines():
        key, sep, value = line.partition(":")
        if sep and key in headers:
            headers[key].add(value.strip())
            if key == "pkg":
                active_package = value.strip()
        if not line.startswith("Benchmark"):
            continue
        leaf, *fields = line.split()
        if scoped and not leaf.startswith(scope["root"] + "/"):
            continue
        check(active_package == package and leaf in values, f"unexpected benchmark {active_package} {leaf}")
        check(len(fields) == 7 and fields[2::2] == ["ns/op", "B/op", "allocs/op"], f"unexpected sample units for {leaf}")
        iterations, elapsed, allocated, allocs = int(fields[0]), float(fields[1]), int(fields[3]), int(fields[5])
        check(iterations > 0 and math.isfinite(elapsed) and elapsed > 0 and allocated >= 0 and allocs >= 0,
              f"invalid measurement for {leaf}")
        values[leaf].append([iterations, elapsed, allocated, allocs])
    for key, value in (("goos", "linux"), ("goarch", "amd64"), ("cpu", scope["cpu"])):
        check(headers[key] == {value}, f"unexpected sample {key}: {sorted(headers[key])}")
    check(package in headers["pkg"] and (scoped or headers["pkg"] == {package}), "unexpected benchmark package headers")
    check(all(len(leaf) == count for leaf in values.values()), "unexpected sample counts")
    return values


def prepare(scope, archive, evidence, baseline, tested):
    checksum = hashlib.sha256()
    with archive.open("rb") as handle:
        for chunk in iter(lambda: handle.read(64 * 1024), b""):
            checksum.update(chunk)
    digest = checksum.hexdigest()
    check(digest == scope["archive_sha"], "formal artifact SHA-256 mismatch")
    package, root, profile = (scope[key] for key in ("package", "root", "profile"))
    primary = names(scope)
    confirmed = names(scope, "confirmed")
    formal_samples = {}
    with zipfile.ZipFile(archive) as bundle:
        check(len(set(bundle.namelist())) == len(bundle.namelist()) and bundle.testzip() is None, "invalid formal archive")
        def read(name):
            return bundle.read(name).decode("utf-8")
        check(read("benchmark-revisions.txt") == f"base_sha={baseline}\ntested_sha={tested}\n", "unexpected formal revisions")
        original_profile = read("benchmark-compile-profile.txt")
        check("rpc-boundary-overlay-v2 99658ed681f4110df0db3fc688931fa61cd656f3c62477c04a1ffd4bb567ca8e"
              in original_profile.splitlines(), "unexpected original RPC compilation profile")
        expected_batch = f"{package}\tdefault\t" + "|".join(
            "/".join(f"^{part}$" for part in leaf.removesuffix("-2").split("/")) for leaf in confirmed) + "\n"
        check(read("benchmark-confirmation-batch.tsv") == expected_batch, "unexpected original confirmation batch")
        check(read("benchmark-confirmation-targets.tsv") == "".join(f"{package}\t{leaf}\n" for leaf in confirmed),
              "unexpected original confirmation targets")
        for group, inventory, expected in (("primary", "benchmark-expected.json", primary),
                                           ("confirmation", "benchmark-confirmation-expected.json", confirmed)):
            spec = json.loads(read(inventory))
            check(set(spec) == {"Base", "Head"}, "unexpected original comparison")
            formal_samples[group] = {}
            for side in ("base", "head"):
                registered = [leaf["Name"] for leaf in spec[side.title()] if leaf["Package"] == package
                              and leaf["Name"].startswith(root + "/")]
                check(sorted(registered) == expected, f"unexpected formal inventory: {group}/{side}")
                formal_samples[group][side] = rows(scope, read(f"benchmark-raw-{group}-{side}.txt"), expected, 10, scoped=True)
        for item in ("benchmark-compile-profile.txt", "benchmark-confirmation-targets.tsv", "benchmark-confirmation-batch.tsv",
                     "benchstat-primary.csv", "benchstat-confirmation.csv"):
            (evidence / f"formal-{item}").write_bytes(bundle.read(item))
    write_json(evidence / "formal.json", {"run": 45413131379, "attempt": 1, "archiveSha256": digest,
               "artifact": scope["artifact"], "base": baseline, "tested": tested, "cpu": scope["cpu"], "samples": formal_samples})
    keys = [{"Package": package, "Name": leaf} for leaf in primary]
    write_json(evidence / "expected.json", {"Base": keys, "Head": keys})
    (evidence / "batch.tsv").write_text(f"{package}\t{profile}\t^({root})$\n", encoding="utf-8")
    (evidence / "cases.tsv").write_text(f"01\t{package}\t{profile}\t{root}\t^({root})$\n", encoding="utf-8")


def adapt_sampler(original, output):
    source = original.read_text(encoding="utf-8")
    replacements = {
        '  if [[ "$variant" != default ]]; then\n    binaries+="-$variant"\n  fi\n': "",
        '"$scratch/bin/benchrun" run "$scratch/source/$revision" "$binaries" "$package"':
            '"$scratch/bin/benchrun" run-performance "$scratch/source/$revision" "$binaries" "$package" "$variant"',
    }
    for before, after in replacements.items():
        check(source.count(before) == 1, "shared sampler profile invocation changed")
        source = source.replace(before, after, 1)
    output.write_text(source, encoding="utf-8")


def verify_samples(scope, evidence):
    expected = names(scope)
    for role in ("base", "head"):
        rows(scope, (evidence / f"inventory-{role}.txt").read_text(), expected, 1)
    schedule = []
    for offset, group in enumerate(("primary", "confirmation")):
        for pair in range(1, 11):
            compare = ("ab", "aa") if (pair + offset) % 2 else ("aa", "ab")
            sides = ("left", "right") if (pair + offset) % 2 else ("right", "left")
            for comparison in compare:
                for side in sides:
                    sample = f"{pair:02}"
                    role = "head" if comparison == "ab" and side == "right" else "base"
                    raw = f"raw/{group}.{sample}.{comparison}.01.{side}.txt"
                    schedule.append(dict(group=group, pair=sample, comparison=comparison, case="01", side=side,
                                         revision=role, variant=scope["profile"], raw=raw))
    with (evidence / "order.tsv").open(newline="", encoding="utf-8") as handle:
        order = list(csv.DictReader(handle, delimiter="\t"))
    check(order == schedule, "unexpected paired A/A or A/H execution order")
    raw_paths = {path.relative_to(evidence).as_posix() for path in (evidence / "raw").iterdir()}
    check(raw_paths == {entry["raw"] for entry in schedule}, "missing or additional raw sample file")
    for entry in order:
        rows(scope, (evidence / entry["raw"]).read_text(), expected, 1)


def filter_confirmation(evidence, comparison):
    key_file = evidence / f"{comparison}.targeted.json"
    keys = json.loads(key_file.read_text())
    check(keys["Base"] == keys["Head"] and bool(keys["Base"]), "unexpected targeted confirmation inventory")
    included = {key["Name"] for key in keys["Base"]}
    for side in ("left", "right"):
        source = (evidence / f"{comparison}.confirmation.{side}.txt").read_text().splitlines(keepends=True)
        result = (row for row in source if not row.startswith("Benchmark") or row.split()[0] in included)
        (evidence / f"{comparison}.targeted.{side}.txt").write_text("".join(result))


def main(mode, scope_name, *args):
    if mode == "adapt":
        adapt_sampler(Path(scope_name), Path(args[0]))
        return
    scope = SCOPES[scope_name]
    if mode == "tuple":
        print("\t".join(str(scope[key]) for key in ("cpu", "package", "profile", "fixture", "fixture_sha", "artifact", "archive_sha", "root")))
    elif mode == "prepare":
        prepare(scope, Path(args[0]), Path(args[1]), *args[2:])
    elif mode == "verify-samples":
        verify_samples(scope, Path(args[0]))
    elif mode == "filter":
        filter_confirmation(Path(args[0]), args[1])
    else:
        raise ValueError(f"unknown mode {mode}")


if __name__ == "__main__":
    try:
        main(*sys.argv[1:])
    except (OSError, ValueError, KeyError, zipfile.BadZipFile) as error:
        sys.exit(str(error))
