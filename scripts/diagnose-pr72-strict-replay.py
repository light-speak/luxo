"""Replay selected PR 72 strict diagnostics without changing production gates."""

import hashlib
import json
import os
import re
import subprocess
import sys
import tarfile
from pathlib import Path
from tempfile import TemporaryDirectory
from zipfile import ZipFile


BASE = "1ee5a1fff2948bee82b954162eb7378106edeb34"
HEAD = "957994d0361724c96036837d40357bd419d702de"
TREES = {
    BASE: "ea9f9842c5d239605d2fc8e51052112b3cae1e05",
    HEAD: "7b681d8330e28b6078dfe795c1bf223a38ff7fb6",
}
RUN = 45488249043
DIGESTS = {
    "plan": "9072212fd8afeefa1ddd1c640f2f6487798d75f222c514f95f93afb3aa2d9d0e",
    "04": "3187a17cf388e4d9bc32f7bb97cbf558fe65a3c068c19810e1e0618f0ccacb27",
    "06": "ffbc4955b22d95a8bc37921bb8cf19451793b1206e1a1fa60cb9eaa452ed26e7",
    "11": "f0fe7b57e3668a024d19547e8a463dce951577757121f65e06f2208cae245311",
    "12": "67838ff15f3e51f0fb9970226676ffd4d7e8365e3d3b1d858fd2d5f4d1de1c18",
}
CPU_SHARDS = {
    "INTEL(R) XEON(R) 6975P-C": ("04", "06", "11"),
    "AMD EPYC 9V74 80-CORE PROCESSOR": ("12",),
}
WIRE_PATH = "pkg/lux/codec/wire.go"
WIRE_SHA = (
    "cac2dd90bdf4e61ebd4c83cbe5df5bfc31e10df9e0a5f7a2b8e9d027cff8562c",
    "b030007700ab6421548059e60d7be69aaa3641f65ff4a574b81d74b92ece869b",
)
BOXED = "BenchmarkBinaryJSONDocuments/1/boxed-2"
HEADER_LEAVES = [
    ("pkg/lux/api", f"BenchmarkBinaryJSONDocuments/{size}/{kind}-2")
    for size in (0, 1, 128) for kind in ("boxed", "typed")
] + [
    ("pkg/lux/codec", f"BenchmarkArrayHeaderWrite/{count}-2")
    for count in (-1, 0, 128, 1000000, 1000001)
]
RPC_LEAVES = [
    ("pkg/lux/rpc", f"BenchmarkRPCNilContextGuard/{token}/{variant}-2")
    for token in ("NoToken", "Rejected", "Unconfigured", "Verified")
    for variant in ("Before", "After")
]
SYMBOLS = {
    "pkg/lux/api": "BinaryJSONDocuments|EncodeBinaryRequest|encodeBinaryListValue|encodeBinaryBytesList|encodeJSONDocuments|AppendArrayHeader|IsWebSocketUpgrade",
    "pkg/lux/codec": "ArenaPayloadBoundary|readArenaBeforePayload|readHeaderBeforeArenaPayload|benchmarkArena(Row|Column)Header|RawJSONBoundary|benchmarkJSONRead|ReadBytes|ReadVarint|Decoder.*Finish|ColumnarBytesValidation|bytesBeforeValidation|AppendArrayHeader|WriteArrayHeader",
    "pkg/lux/luvia": "HeartbeatEncodingComparison|encodeHeartbeat",
    "pkg/lux/rpc": "LRPCOwnedRequest|callRequest.*prepare|appendCallBody|appendEnvelopePrefix|writePreparedFrame|RPCNilContextGuard|beforeNilContextGuard|beforeAuthenticationRecovery|requestContext|verifyRequestContext",
    "pkg/lux/schema": "PreparedLookup|lookupModelBeforePrebuild|LookupModel",
}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def digest(data):
    return hashlib.sha256(data).hexdigest()


def cpu_route(cpu):
    require(cpu.upper() in CPU_SHARDS, f"unsupported CPU model: {cpu}")
    return CPU_SHARDS[cpu.upper()]


def frozen_scope(plan, archive, shard):
    for name in ("benchmark-revisions.txt", "benchmark-compile-profile.txt"):
        require(plan.read(name) == archive.read(name), f"shard {shard}: incompatible {name}")
    require(plan.read("benchmark-revisions.txt").decode() == f"base_sha={BASE}\ntested_sha={HEAD}\n", "revisions differ")
    for ext, name in (("tsv", "batch.tsv"), ("json", "expected.json")):
        require(plan.read(f"benchmark-plan/shard-{shard}.{ext}") == archive.read("benchmark-" + name), f"wrong shard {shard}")
    expected = archive.read("benchmark-confirmation-expected.json")
    targets = [tuple(line.split("\t")) for line in archive.read("benchmark-confirmation-targets.tsv").decode().splitlines()]
    original = json.loads(archive.read("benchmark-expected.json"))
    for side, entries in json.loads(expected).items():
        keys = [(item["Package"], item["Name"]) for item in entries]
        require(keys == targets and len(set(keys)) == len(keys), f"unexpected {shard} {side} targets")
        require(set(keys) <= {(item["Package"], item["Name"]) for item in original[side]}, f"unpaired shard {shard} targets")
    return expected, archive.read("benchmark-confirmation-batch.tsv")


def header_bounds(wire):
    marker = b"func AppendArrayHeader(dst []byte, count int) ([]byte, error) {\n"
    require(wire.count(marker) == 1, "missing array header function")
    start = wire.index(marker) + len(marker)
    return start, wire.index(b"\n}\n", start)


def restore_array_header(base, head):
    require((digest(base), digest(head)) == WIRE_SHA, "wrong array header source digest")
    start, end = header_bounds(head)
    base_start, base_end = header_bounds(base)
    return head[:start] + base[base_start:base_end] + head[end:]


def boxed_confirmed(log):
    return any(
        line.startswith("confirmed performance regression: github.com/light-speak/luxo/pkg/lux/api/"
                        + BOXED.removeprefix("Benchmark") + " sec/op ")
        for line in log.splitlines()
    )


def run(args, cwd, *, output=subprocess.PIPE, stderr=None, check=True, extra_env=None, timeout=450):
    env = dict(os.environ)
    if args[0] != "gh":
        env.pop("GH_TOKEN", None)
    env.update(extra_env or {})
    return subprocess.run(args, cwd=cwd, env=env, stdout=output, stderr=stderr, check=check, timeout=timeout)


def capture(args, cwd):
    return run(args, cwd).stdout


def fetch_inputs(repo, scratch, evidence, shards):
    listing = json.loads(capture(["gh", "api", f"repos/light-speak/luxo/actions/runs/{RUN}/artifacts?per_page=100"], repo))
    for key in ("plan", *shards):
        name = "benchmark-plan-1" if key == "plan" else f"performance-evidence-{key}-1"
        matches = [entry for entry in listing["artifacts"] if entry["name"] == name]
        require(len(matches) == 1, f"expected a unique {name} artifact")
        entry = matches[0]
        require(entry["workflow_run"]["id"] == RUN and entry["workflow_run"]["head_sha"] == HEAD,
                f"artifact {name} revision mismatch")
        require(not entry["expired"] and entry["digest"] == "sha256:" + DIGESTS[key], f"bad {name} digest")
        if key == "plan":
            require(entry["id"] == 14964798133, "wrong strict plan artifact")
        content = capture(["gh", "api", f"repos/light-speak/luxo/actions/artifacts/{entry['id']}/zip"], repo)
        require(digest(content) == DIGESTS[key], f"downloaded {name} digest mismatch")
        (scratch/f"official-{key}.zip").write_bytes(content)
        (evidence/f"official-{key}.json").write_text(json.dumps(entry, indent=2) + "\n")


def snapshot(repo, revision, source):
    source.mkdir()
    archive = source.parent/(source.name + ".tar")
    with archive.open("wb") as stream:
        run(["git", "archive", revision], repo, output=stream)
    with tarfile.open(archive) as stream:
        stream.extractall(source, filter="data")
    archive.unlink()
    (source/".tmp").mkdir()
    run(["sh", "scripts/setup-modules.sh", str(source)], source, output=None)


def rows(batch):
    return [tuple(line.split("\t")) for line in batch.read_text().splitlines()]


def record_binary(binary, expression, folder):
    folder.mkdir(parents=True, exist_ok=True)
    version = capture(["go", "version", "-m", str(binary)], folder)
    for key, value in (("GOOS","linux"), ("GOARCH","amd64"), ("GOAMD64","v1"), ("CGO_ENABLED","1"), ("-trimpath","true")):
        require(f"{key}={value}\n".encode() in version, f"{binary}: expected {key}={value}")
    (folder/"build.txt").write_bytes(version)
    (folder/"build-id.txt").write_bytes(capture(["go","tool","buildid",str(binary)], folder))
    (folder/"sha256.txt").write_text(digest(binary.read_bytes()) + "\n")
    symbols = capture(["go","tool","nm","-size",str(binary)], folder).splitlines()
    (folder/"symbols.txt").write_bytes(b"\n".join(line for line in symbols if re.search(expression.encode(), line)) + b"\n")
    assembly = capture(["go","tool","objdump","-s",expression,str(binary)], folder)
    require(assembly.startswith(b"TEXT "), f"{binary}: no retained hot symbols")
    (folder/"disassembly.txt").write_bytes(assembly)


class Replay:
    def __init__(self, scratch, evidence, cpu):
        self.scratch = scratch
        self.evidence = evidence
        self.cpu = cpu
        self.tools = scratch/"tools"
        self.tools.mkdir()
        self.built = set()

    def build_tools(self):
        for name in ("benchrun","benchplan","benchgate"):
            run(["go","build","-o",str(self.tools/name),f"./scripts/{name}"], self.source("A"),
                output=None, extra_env={"GOWORK":"off"})
        run(["go","install","golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70"], self.source("A"),
            output=None, extra_env={"GOWORK":"off", "GOBIN":str(self.tools)})

    def source(self, role):
        return self.scratch/role

    def binaries(self, role):
        return self.scratch/"bin"/role

    def compile(self, role, batch):
        for package, profile, _ in rows(batch):
            key = (role,package,profile)
            if key in self.built:
                continue
            mode = "compile-performance-baseline" if role == "A" else "compile-performance"
            run([self.tools/"benchrun",mode,self.source(role),self.binaries(role),package,profile],
                self.scratch, output=None)
            stem = package.replace("/", "-") + (f"-{profile}" if profile != "default" else "")
            record_binary(self.binaries(role)/(stem + ".test"), SYMBOLS[package], self.evidence/"ELF"/role/stem)
            self.built.add(key)

    def go_command(self, name, *args, output=subprocess.PIPE, check=True):
        return run([self.tools/name,*args], self.scratch, output=output, check=check)

    def sample(self, label, expected, batch, left, right, rounds):
        files = []
        for group in range(rounds):
            print(f"Sampling {label} group {group + 1}/{rounds}", flush=True)
            stem = self.evidence/f"{label}-{group + 1}"
            raw = [Path(f"{stem}.{side}.raw") for side in ("base","head")]
            with raw[0].open("wb") as a, raw[1].open("wb") as b:
                for index in range(10):
                    order = (0,1) if (index + group) % 2 == 0 else (1,0)
                    for package, profile, selector in rows(batch):
                        for side in order:
                            role = (left,right)[side]
                            self.go_command(
                                "benchrun","run-performance",self.source(role),self.binaries(role),package,profile,
                                "-test.run=^$",f"-test.bench={selector}","-test.benchmem",
                                "-test.benchtime=200ms","-test.count=1","-test.cpu=2","-test.timeout=90s",
                                output=(a,b)[side],
                            )
            normalized = [Path(f"{stem}.{side}.txt") for side in ("base","head")]
            for raw_file, normalized_file in zip(raw, normalized):
                self.go_command("benchplan","--normalize",raw_file,normalized_file)
                cpus = {line.removeprefix("cpu: ") for line in normalized_file.read_text().splitlines()
                        if line.startswith("cpu: ")}
                require(len(cpus) == 1 and cpu_route(cpus.pop()) == cpu_route(self.cpu),
                        f"{normalized_file}: CPU changed during replay")
            self.go_command("benchplan","--verify",expected,*normalized)
            files.append(normalized)
        return files

    def benchstat(self, label, inputs):
        path = self.evidence/(label + ".csv")
        with path.open("wb") as output:
            self.go_command("benchstat","-alpha","0.01","-filter",".unit:(sec/op OR B/op OR allocs/op)",
                            "-format","csv",f"base={inputs[0]}",f"head={inputs[1]}",output=output)
        return path

    def control(self, label, expected, batch, role):
        (sides,) = self.sample(label,expected,batch,role,role,1)
        statuses = [
            self.gate(label+"-"+direction, [self.benchstat(label+"-"+direction,inputs)])
            for direction, inputs in (("forward",sides),("reverse",sides[::-1]))
        ]
        require(all(status == 0 for status, _ in statuses), "same-binary control did not pass both directions")

    def gate(self, label, csvs, original=False):
        args = [self.tools/"benchgate"]
        if original:
            args.extend(["--approved-costs",self.source("A")/"scripts/benchgate/approved-costs.json","--base-sha",BASE])
        result = run([*args,*csvs], self.scratch, stderr=subprocess.STDOUT, check=False, timeout=30)
        text = result.stdout.decode()
        (self.evidence/(label+".gate.txt")).write_text(text)
        print(text, end="", flush=True)
        if result.returncode:
            kind = "requires confirmation for" if len(csvs) == 1 else "rejected"
            require(result.returncode == 1 and f"performance gate {kind} " in text, "gate failed to parse samples")
        return result.returncode, text

    def compare(self, label, expected, batch, left, right):
        self.go_command("benchplan","--verify-profile-batch","confirmation",expected,batch)
        for role in (left,right):
            self.compile(role,batch)
        self.control(label+"-AA",expected,batch,left)
        paired = self.sample(label,expected,batch,left,right,2)
        csvs = [self.benchstat(f"{label}-{group}", sides) for group, sides in enumerate(paired,1)]
        return self.gate(label,csvs,original=(left,right)==("A","H"))

    def subset(self, label, primary, leaves):
        target = self.evidence/(label+".tsv")
        expected = self.evidence/(label+".expected.json")
        batch = self.evidence/(label+".batch.tsv")
        target.write_text("".join(f"{package}\t{name}\n" for package,name in leaves))
        self.go_command("benchplan","--confirmation-plan",primary,target,batch,expected)
        return expected,batch


def replay_on_host(repo, output, cpu):
    shards = cpu_route(cpu)
    with TemporaryDirectory(prefix="pr72-strict-build.",dir=output.parent) as workspace:
        session = Replay(Path(workspace),output,cpu)
        inputs = session.scratch/"inputs"
        inputs.mkdir()
        fetch_inputs(repo,inputs,output,shards)
        for revision, role in ((BASE,"A"),(HEAD,"H")):
            actual = capture(["git","rev-parse",revision+"^{tree}"],repo).decode().strip()
            require(actual == TREES[revision], f"{role}: wrong tree")
            snapshot(repo,revision,session.source(role))
        session.build_tools()
        results = {}
        with ZipFile(inputs/"official-plan.zip") as plan:
            require(session.go_command("benchrun","performance-profile").stdout == plan.read("benchmark-compile-profile.txt"),
                    "local compile profile differs from original plan")
            for shard in shards:
                with ZipFile(inputs/f"official-{shard}.zip") as archive:
                    environment = archive.read("benchmark-environment.txt").decode()
                    require(cpu.upper() in environment.upper(), "archived shard CPU differs")
                    expected, batch = frozen_scope(plan,archive,shard)
                    expected_file = output/f"shard-{shard}.expected.json"
                    batch_file = output/f"shard-{shard}.batch.tsv"
                    primary_file = output/f"shard-{shard}.primary.json"
                    expected_file.write_bytes(expected)
                    batch_file.write_bytes(batch)
                    primary_file.write_bytes(archive.read("benchmark-expected.json"))
                status, report = session.compare("AH-"+shard,expected_file,batch_file,"A","H")
                results["AH-"+shard] = status
                if shard == "11":
                    expected_file, batch_file = session.subset("bearer-paths",primary_file,RPC_LEAVES)
                    results["AH-bearer-paths"], _ = session.compare("AH-bearer-paths",expected_file,batch_file,"A","H")
                if shard == "12" and status == 1 and boxed_confirmed(report):
                    header_experiment(repo,session,primary_file,results)
    (output/"summaries.json").write_text(json.dumps(results,indent=2) + "\n")
    return int(any(results.values()))


def header_experiment(repo, session, shard12_primary, results):
    snapshot(repo,HEAD,session.source("R"))
    read_wire = lambda role: (session.source(role)/WIRE_PATH).read_bytes()
    restored = restore_array_header(read_wire("A"),read_wire("H"))
    target = session.source("R")/WIRE_PATH
    require(target.read_bytes() == read_wire("H"), "wrong H/R preimage")
    target.write_bytes(restored)
    require(digest(target.read_bytes()) == "46f3d7683e9125544866e890cf62d5ae8370751583dcc76cac6dc4a7ea5f49d2",
            "wrong R wire digest")
    (session.evidence/"R-wire-sha256.txt").write_text(digest(restored) + "\n")

    # The API leaves are from shard 12. Obtain the array leaves from the
    # complete original plan because benchmark families occupy separate shards.
    with ZipFile(session.scratch/"inputs/official-plan.zip") as plan:
        api = json.loads(shard12_primary.read_bytes())
        catalog = {arm:{(item["Package"],item["Name"]) for item in api[arm]} for arm in api}
        for name in plan.namelist():
            if re.fullmatch(r"benchmark-plan/shard-[0-9]+\.json",name):
                for arm, entries in json.loads(plan.read(name)).items():
                    catalog[arm].update((item["Package"],item["Name"]) for item in entries)
        require(all(set(HEADER_LEAVES) <= leaves for leaves in catalog.values()), "unpaired array header fixture")
        primary = session.evidence/"HR-original-leaves.json"
        primary.write_text(json.dumps(
            {arm:[{"Package":package,"Name":name} for package,name in HEADER_LEAVES] for arm in catalog}
        )+"\n")
    expected, batch = session.subset("header-body",primary,HEADER_LEAVES)
    results["HR-header-body"], _ = session.compare("HR-header-body",expected,batch,"H","R")


def main():
    require(len(sys.argv) == 4, "expected repository, evidence path and CPU")
    repo, output, selected_cpu = Path(sys.argv[1]).resolve(), Path(sys.argv[2]).resolve(), sys.argv[3]
    require(sys.platform == "linux" and os.uname().machine == "x86_64", "Linux x64 is required")
    actual = capture(["lscpu"],repo).decode()
    names = re.findall(r"^Model name:\s*([^\n]+)$",actual,re.MULTILINE)
    require(len(names) == 1 and names[0].strip().upper() == selected_cpu.upper(), "runner CPU changed")
    require(capture(["git","rev-parse","HEAD"],repo).decode().strip() == os.environ["GITHUB_SHA"], "wrong diagnostic checkout")
    output.mkdir()
    (output/"lscpu.txt").write_text(actual)
    (output/"uname.txt").write_bytes(capture(["uname","-a"],repo))
    go = json.loads(capture(["go","env","-json","GOVERSION","GOOS","GOARCH","GOAMD64","CGO_ENABLED"],repo))
    require(go == {"GOVERSION":"go1.27.1","GOOS":"linux","GOARCH":"amd64","GOAMD64":"v1","CGO_ENABLED":"1"},
            "wrong benchmark toolchain")
    (output/"go-env.json").write_text(json.dumps(go,indent=2) + "\n")
    return replay_on_host(repo,output,selected_cpu)


if __name__ == "__main__":
    sys.exit(main())
