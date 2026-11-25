"""Compare the current PR 72 with its unchanged production code and former tests."""

from hashlib import sha256
import importlib.util
import json
import os
from pathlib import Path
import re
import sys
from tempfile import TemporaryDirectory
from zipfile import ZipFile


BASE = "5f8f2fcb75eafd2419fab881625b95cfb424b474"
HEAD = "9a22fa8f4e139d372c8516e84344a7bf90c58b99"
TREES = {BASE: "118478837edd37cfa206865a629bfb50ae6e7327",
         HEAD: "89367c56fd6aac0916c1c520ca975e1eb2632790"}
HPROD_TREE = "487639ff1696ef38fa82a2a95b3e173906c182fc"
RUN = 45527815866
CPU = "Intel(R) Xeon(R) 6975P-C"
SHARDS = ("07", "11")
ARTIFACTS = {
    "plan": (14984055331, 344051, "1b6f3cc479e47942e20b5f69127d611b9539c03eb3f8d37103ec9e18ac15655f"),
    "07": (14984773283, 159320, "9a992de6f881f05d72d6f43bd80baf050dcfed94ab09f040e7f7f2767906991d"),
    "11": (14985393563, 155515, "6970374f9f54e6c8888760480790b5c65f457c1c510346b84577f6fbc4f355fd"),
}
TESTS = ("pkg/lux/codec/selectionmask_test.go", "pkg/lux/luvia/planner_test.go")
PRODUCT = ("pkg/lux/codec/selectionmask.go", "pkg/lux/luvia/planner.go")
PLAN_FIXTURE = "e02019c7a03c55b5beac7183395d5d4df78117011013edf2747b2acd34b77cb7"
PLAN_TARGET = "pkg/lux/luvia/diagnostic_exact_plan_selection_test.go"
GUARD_ONLY = ("pkg/lux/rpc", "BenchmarkRPCNilContextGuard/Verified/GuardOnly-2")
SYMBOLS = {
    "pkg/lux/codec": "ColumnarRegisterValidation|appendColumn|DateTimePair|AppendDateTime|AppendSvarint|SplitSelectionMask|selectionMaskParts|validSelectionChildren",
    "pkg/lux/h2transport": "TaskDeadlineReplacement|replaceTaskDeadline|refreshDeadline",
    "pkg/lux/rpc": "RPCNilContextGuard|beforeNilContextGuard|beforeAuthenticationRecovery|requestContext|verifyRequestContext",
    "pkg/lux/luvia": r"BenchmarkExactPlanSelection|luvia\.Plan$|SplitSelectionMask|selectionMaskParts|validSelectionChildren",
}


def load_replay():
    path = Path(__file__).with_name("diagnose-pr72-strict-replay.py")
    if sha256(path.read_bytes()).hexdigest() != "191dd9509d1dc0c213ee54f5639cdab4272aaa7ac60a2c44480ad7dda77d34ea":
        raise ValueError("unexpected shared replay driver bytes")
    spec = importlib.util.spec_from_file_location("original_strict_replay", path)
    tool = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(tool)
    tool.BASE, tool.HEAD = BASE, HEAD
    tool.CPU_SHARDS = {CPU.upper(): SHARDS}
    tool.SYMBOLS = SYMBOLS
    return tool


original = load_replay()
require, digest = original.require, original.digest
capture = original.capture


def artifact_name(key):
    return "benchmark-plan-1" if key == "plan" else f"performance-evidence-{key}-1"


def checked_zip(directory, key):
    path = directory/(artifact_name(key) + ".zip")
    _, size, checksum = ARTIFACTS[key]
    data = path.read_bytes()
    require(len(data) == size and digest(data) == checksum, f"{key}: strict artifact bytes differ")
    return ZipFile(path)


def download(repo, directory):
    endpoint = "repos/light-speak/luxo/actions"
    listing = json.loads(capture(["gh", "api", f"{endpoint}/runs/{RUN}/artifacts?per_page=100"], repo))
    directory.mkdir()
    for key, (identifier, size, checksum) in ARTIFACTS.items():
        name = artifact_name(key)
        entries = [x for x in listing["artifacts"] if x["name"] == name]
        require(len(entries) == 1, f"expected one {name} artifact")
        entry, = entries
        require(entry["id"] == identifier and entry["size_in_bytes"] == size and not entry["expired"],
                f"{name}: unexpected ID, size or expiry")
        require(entry["workflow_run"]["id"] == RUN and entry["workflow_run"]["head_sha"] == HEAD,
                f"{name}: unexpected run or head")
        require(entry["digest"] == "sha256:" + checksum, f"{name}: API SHA mismatch")
        body = capture(["gh", "api", f"{endpoint}/artifacts/{identifier}/zip"], repo)
        (directory/(name + ".zip")).write_bytes(body)
        (directory/(name + ".json")).write_text(json.dumps(entry, indent=2) + "\n")


def validate_inputs(directory):
    scopes = {}
    with checked_zip(directory, "plan") as plan:
        require(set(plan.read("changed-files.txt").decode().splitlines()) == set(PRODUCT + TESTS),
                "the formal product scope changed")
        for shard in SHARDS:
            with checked_zip(directory, shard) as artifact:
                environment = artifact.read("benchmark-environment.txt").decode()
                models = re.findall(r"^Model name:\s*([^\n]+)$", environment, re.MULTILINE)
                require(models == [CPU] and re.search(r'"GOVERSION": "go1\.27\.1"', environment)
                        and re.search(r'"GOAMD64": "v1"', environment) and re.search(r'"CGO_ENABLED": "1"', environment),
                        f"{shard}: origin CPU or toolchain differs")
                for direction in ("control", "control-reversed"):
                    require(artifact.read(f"benchmark-{direction}-gate.txt").startswith(b"performance gate passed:"),
                            f"{shard}: official A/A did not pass")
                official_results = artifact.read("benchmark-gate.txt").decode().splitlines()
                require(sum(row.startswith("confirmed performance regression: ") for row in official_results)
                        == {"07": 5, "11": 6}[shard], f"{shard}: official results changed")
                expected, batch = original.frozen_scope(plan, artifact, shard)
                leaves = [(item["Package"], item["Name"]) for item in json.loads(expected)["Base"]]
                require(len(leaves) == {"07": 5, "11": 8}[shard] and (shard != "11" or GUARD_ONLY in leaves),
                        f"{shard}: count or frozen GuardOnly changed")
                scopes[shard] = expected, batch
        return scopes, plan.read("benchmark-compile-profile.txt")


def source_receipt(repo, session):
    for sha in (BASE, HEAD):
        require(capture(["git", "rev-parse", sha+"^{tree}"], repo).decode().strip() == TREES[sha],
                f"{sha}: exact source tree unavailable")
    changes = capture(["git", "diff", "--name-only", BASE, HEAD], repo).decode().splitlines()
    require(set(changes) == set(TESTS + PRODUCT), "non-selection files differ")
    for role, sha in (("A", BASE), ("H", HEAD), ("Hprod", HEAD)):
        original.snapshot(repo, sha, session.source(role))
    binding = {}
    for path in TESTS + PRODUCT:
        base = (session.source("A")/path).read_bytes()
        head = (session.source("H")/path).read_bytes()
        expected = base if path in TESTS else head
        dest = session.source("Hprod")/path
        require(base != head and dest.read_bytes() == head, f"wrong source preimage: {path}")
        if path in TESTS:
            dest.write_bytes(base)
        require(dest.read_bytes() == expected, f"wrong restored source: {path}")
        binding[path] = {"base": digest(base), "head": digest(head), "Hprod": digest(expected)}
    roots = {path: capture(["git", "rev-parse", f"{BASE}:{path}"], repo).decode().strip() for path in
             ("scripts/benchrun", "scripts/benchplan", "scripts/benchgate", "internal/benchlayout")}
    (session.evidence/"source.json").write_text(json.dumps({"base": BASE, "head": HEAD, "trees": TREES,
                                                            "HprodProjection": HPROD_TREE, "filesSHA256": binding,
                                                            "toolsTrees": roots}, indent=2)+"\n")


class LayoutReplay(original.Replay):
    def compile(self, role, batch):
        previous = self.built.copy()
        super().compile(role, batch)
        for _, package, profile in sorted(self.built - previous):
            stem = package.replace("/", "-") + (f"-{profile}" if profile != "default" else "")
            binary = self.binaries(role)/(stem + ".test")
            directory = self.evidence/"ELF"/role/stem
            (directory/"sections.txt").write_bytes(capture(["readelf", "-W", "-S", str(binary)], directory))


def normalize_assembly(raw):
    name = None
    sections = {}
    for line in raw.splitlines():
        if line.startswith("TEXT "):
            name = line.split()[1].removesuffix("(SB)")
            require(name not in sections, f"duplicate disassembly: {name}")
            sections[name] = []
            continue
        if not line.strip():
            continue
        row = re.fullmatch(r"\s+\S+\s+(0x[0-9a-f]+)\s+([0-9a-f]+)\s+(.*)", line)
        require(name is not None and row is not None, "unexpected go tool objdump output")
        pc, _, op = row.groups()
        sections[name].append((int(pc, 16), op.strip()))
    require(sections, "missing disassembly")
    result = {}
    for symbol, rows in sections.items():
        require(rows, f"missing instructions: {symbol}")
        first = rows[0][0]
        addresses = {pc for pc, _ in rows}
        ops = []
        for pc, op in rows:
            branch = re.fullmatch(r"((?:J[A-Z]+|CALL)\s+)(0x[0-9a-f]+)", op)
            if branch and int(branch[2], 16) in addresses:
                op = f"{branch[1]}+0x{int(branch[2], 16)-first:x}"
            op = re.sub(r"(?<![\w])(?:[+-]?0x[0-9a-f]+|[+-]?\d+)\(IP\)", "<PCREL>(IP)", op)
            ops.append(f"+{pc-first:04x}\t{op}")
        result[symbol] = ops
    return result


def record_layout(evidence):
    layouts = {}
    for directory in sorted((evidence/"ELF").glob("*/*")):
        assembly = directory/"disassembly.txt"
        if not assembly.is_file():
            continue
        instructions = normalize_assembly(assembly.read_text())
        sizes = {}
        for line in (directory/"symbols.txt").read_text().splitlines():
            address, size, kind, symbol = line.split(maxsplit=3)
            start = int(address, 16)
            sizes[symbol] = {"start": start, "size": int(size), "kind": kind, "mod64": start % 64, "mod4096": start % 4096}
        (directory/"layout.json").write_text(json.dumps(sizes, indent=2, sort_keys=True)+"\n")
        (directory/"normalized-disassembly.json").write_text(json.dumps(instructions, indent=2, sort_keys=True)+"\n")
        layouts[(directory.parent.name, directory.name)] = sizes, instructions
    comparisons = {}
    for left, right in (("A", "H"), ("H", "Hprod"), ("Aplan", "Hplan")):
        for role, binary in sorted(layouts):
            if role != left or (right, binary) not in layouts:
                continue
            left_sizes, left_ops = layouts[(left, binary)]
            right_sizes, right_ops = layouts[(right, binary)]
            common = left_sizes.keys() & right_sizes.keys() & left_ops.keys() & right_ops.keys()
            comparisons[f"{left}/{right}/{binary}"] = {
                name: {
                    "addressDelta": right_sizes[name]["start"] - left_sizes[name]["start"],
                    "sizeDelta": right_sizes[name]["size"] - left_sizes[name]["size"],
                    "sameInstructionShape": left_ops[name] == right_ops[name],
                } for name in sorted(common)
            }
    (evidence/"elf-comparisons.json").write_text(json.dumps(comparisons, indent=2)+"\n")


def compare_plan(repo, session, fixture):
    data = fixture.read_bytes()
    require(digest(data) == PLAN_FIXTURE, "wrong shared PlanSelection fixture")
    names = [("pkg/lux/luvia", f"BenchmarkExactPlanSelection/{name}-2") for name in ("all", "nested")]
    primary = session.evidence/"plan-fixture.expected.json"
    leaves = [{"Package": package, "Name": name} for package, name in names]
    primary.write_text(json.dumps({side: leaves for side in ("Base", "Head")})+"\n")
    for sha, role in ((BASE, "Aplan"), (HEAD, "Hplan")):
        archive = session.source(role)
        original.snapshot(repo, sha, archive)
        target = archive/PLAN_TARGET
        require(not target.exists(), f"unexpected fixture source: {target}")
        target.write_bytes(data)
        require(digest(target.read_bytes()) == PLAN_FIXTURE, "PlanSelection overlay changed during preparation")
    (session.evidence/"plan-fixture.sha256").write_text(PLAN_FIXTURE + "\n")
    expected, batch = session.subset("plan-selection", primary, names)
    return session.compare("Aplan-Hplan", expected, batch, "Aplan", "Hplan")[0]


def replay(repo, output, cpu):
    require(cpu == CPU and sys.platform == "linux" and os.uname().machine == "x86_64", "incorrect Intel Linux runner")
    actual = capture(["lscpu"], repo).decode()
    require(re.findall(r"^Model name:\s*([^\n]+)$", actual, re.MULTILINE) == [CPU], "runner CPU changed")
    require(capture(["git", "rev-parse", "HEAD"], repo).decode().strip() == os.environ["GITHUB_SHA"], "wrong diagnostic revision")
    output.mkdir()
    (output/"lscpu.txt").write_text(actual)
    (output/"uname.txt").write_bytes(capture(["uname", "-a"], repo))
    env = json.loads(capture(["go", "env", "-json", "GOVERSION", "GOOS", "GOARCH", "GOAMD64", "CGO_ENABLED"], repo))
    require(env == {"GOVERSION": "go1.27.1", "GOOS": "linux", "GOARCH": "amd64", "GOAMD64": "v1", "CGO_ENABLED": "1"},
            "incorrect Go qualification toolchain")
    (output/"go-env.json").write_text(json.dumps(env, indent=2)+"\n")
    inputs = output/"origin"
    download(repo, inputs)
    scopes, profile = validate_inputs(inputs)
    with TemporaryDirectory(prefix="pr72-intel-build.", dir=output.parent) as scratch:
        session = LayoutReplay(Path(scratch), output, cpu)
        source_receipt(repo, session)
        session.build_tools()
        require(session.go_command("benchrun", "performance-profile").stdout == profile, "compile profile drift")
        (output/"tool-binaries.json").write_text(json.dumps(
            {name: digest((session.tools/name).read_bytes()) for name in ("benchrun", "benchplan", "benchgate", "benchstat")},
            indent=2)+"\n")
        verdicts = {}
        try:
            for shard in SHARDS:
                expected = output/f"shard-{shard}.expected.json"
                batch = output/f"shard-{shard}.batch.tsv"
                expected.write_bytes(scopes[shard][0])
                batch.write_bytes(scopes[shard][1])
                for left, right in (("A", "H"), ("H", "Hprod")):
                    label = f"{left}-{right}-{shard}"
                    verdicts[label] = session.compare(label, expected, batch, left, right)[0]
            fixture = repo/"scripts/diagnose-pr72-intel-layout/plan-fixture.go.txt"
            verdicts["Aplan-Hplan"] = compare_plan(repo, session, fixture)
        finally:
            (output/"verdicts.json").write_text(json.dumps(verdicts, indent=2)+"\n")
            record_layout(output)
    return int(any(verdicts.values()))


if __name__ == "__main__":
    if len(sys.argv) == 3 and sys.argv[1] == "--check-inputs":
        scopes, profile = validate_inputs(Path(sys.argv[2]).resolve())
        for shard, (expected, _) in scopes.items():
            print(f"shard {shard}: {len(json.loads(expected)['Base'])} original confirmation leaves")
        print(f"profile SHA-256 {digest(profile)}")
    else:
        require(len(sys.argv) == 4, "expected repository, evidence directory and CPU")
        sys.exit(replay(Path(sys.argv[1]).resolve(), Path(sys.argv[2]).resolve(), sys.argv[3]))
