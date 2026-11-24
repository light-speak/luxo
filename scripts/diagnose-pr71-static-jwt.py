import csv
import hashlib
import json
from pathlib import Path
import sys
import zipfile


PACKAGE = "pkg/lux/auth"
CPU = "AMD EPYC 9V45 96-Core Processor"
ROOT = "BenchmarkStaticJWT"
COMMON = {f"{ROOT}/{part}-2" for part in ("Identity/Before", "Identity/After", "Sign/After", "Verify/After")}
BASE_NAMES = COMMON | {f"{ROOT}/{part}/Before-2" for part in ("Sign", "Verify")}
HEAD_NAMES = COMMON | {f"{ROOT}/{part}/LegacyHMAC-2" for part in ("Sign", "Verify")}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def expected(base, head):
    return {role: [{"Package": PACKAGE, "Name": name} for name in sorted(names)]
            for role, names in (("Base", base), ("Head", head))}


def write_json(path, data):
    path.write_text(json.dumps(data, indent=2) + "\n")


def read_official(output, archive, base, tested):
    content = archive.read_bytes()
    digest = sha(content)
    if digest != "789c9c7220e6a1400461f35d3bae5b1e922e94a8a8c7cd7105604948bec0f9af":
        raise ValueError("wrong formal artifact digest")
    with zipfile.ZipFile(archive) as data:
        if data.read("benchmark-revisions.txt").decode() != f"base_sha={base}\ntested_sha={tested}\n":
            raise ValueError("wrong formal revisions")
        for stage, base_names, head_names in (
            ("", BASE_NAMES, HEAD_NAMES),
            ("confirmation-", {f"{ROOT}/Identity/Before-2", f"{ROOT}/Identity/After-2"}, None),
        ):
            candidates = json.loads(data.read(f"benchmark-{stage}expected.json"))
            for role, names in (("Base", base_names), ("Head", head_names or base_names)):
                found = {row["Name"] for row in candidates[role]
                         if row["Package"] == PACKAGE and row["Name"].startswith(ROOT + "/")}
                if found != names:
                    raise ValueError(f"wrong formal root inventory for {role}/{stage} in {archive}")
        batch = data.read("benchmark-confirmation-batch.tsv").decode().splitlines()
        if len(batch) != 1 or batch[0].split("\t")[:2] != [PACKAGE, "default"]:
            raise ValueError("wrong formal profile")
        for phase in ("primary", "confirmation"):
            for role in ("base", "head"):
                raw = data.read(f"benchmark-raw-{phase}-{role}.txt").decode().splitlines()
                if {line[5:].strip() for line in raw if line.startswith("cpu: ")} != {CPU}:
                    raise ValueError("wrong formal CPU")
        for item in ("benchmark-compile-profile.txt", "benchstat-primary.csv", "benchstat-confirmation.csv"):
            (output / ("formal-" + item)).write_bytes(data.read(item))
    (output / "formal-archive.sha256").write_text(digest + "\n")


def extract(source, start, end):
    return source[source.index(start):source.index(end)].rstrip() + b"\n"


def freeze_setup(output, scratch):
    packages = {role: scratch / "source" / role / PACKAGE for role in ("base", "head")}
    fixtures = {role: (pkg / "static_bench_test.go").read_bytes() for role, pkg in packages.items()}
    if {role: sha(source) for role, source in fixtures.items()} != {
        "base": "34c2a2f551c3a81016e2f6c95b4c970c5db278d06d0271003bcacb0636a553be",
        "head": "801629df1e920d41ab2a2062b10395c78af7b75c89c2b74b841eafbada89dc38",
    }:
        raise ValueError("unexpected native StaticJWT fixture")
    setup = {}
    for role, pkg in packages.items():
        config = extract((pkg / "jwt_test.go").read_bytes(), b"func testConfig(", b"\nfunc TestSignAndVerify(")
        claims = extract((pkg / "claims_test.go").read_bytes(), b"func testClaims(", b"\nfunc TestClaimsPreserveExactNumbers(")
        phase = extract(fixtures[role], b"func benchmarkStaticJWTPhase(", b"\nfunc TestStaticJWTBenchmarkReference(")
        setup[role] = (config, claims, phase)
    if setup["base"] != setup["head"]:
        raise ValueError("test configuration, claims writer or measured phase changed")
    head = fixtures["head"]
    # Keep the new native registration and its explicit frozen historical HMAC implementation.
    header = head[:head.index(b"func BenchmarkVerifyAlternateHeader(")]
    header = header.replace(b'\t"encoding/json"\n', b'\t"encoding/json"\n\t"encoding/json/jsontext"\n')
    header = header.replace(b'\t"fmt"\n', b'\t"fmt"\n\t"strconv"\n')
    root = extract(head, b"func BenchmarkStaticJWT(", b"\nfunc TestStaticJWTBenchmarkReference(")
    if b"token, err := Sign(cfg, writer)" not in root or b"staticIDSink = value.ID()" not in root:
        raise ValueError("expected real signer setup and identity read")
    projection = header + root + b"\n" + b"\n".join(setup["head"][:2])
    projected = scratch / "static-jwt-overlay.go"
    projected.write_bytes(projection)
    (output / "frozen-setup.go.txt").write_bytes(projection)
    write_json(output / "setup-digests.json", {
        "native": {role: sha(source) for role, source in fixtures.items()},
        "common": {name: sha(body) for name, body in zip(("testConfig", "testClaims", "timedPhase"), setup["head"])},
        "isolated": sha(projection),
    })
    for role, pkg in packages.items():
        files = sorted(pkg.glob("*_test.go"))
        native = pkg / "static_bench_test.go"
        if len(files) != 8 or native not in files:
            raise ValueError(f"unexpected auth fixture inventory for {role}")
        replaces = {str(path): "" for path in files}
        replaces[str(native)] = str(projected)
        write_json(scratch / f"{role}-overlay.json", {"Replace": replaces})
        (output / f"{role}-overlay-files.txt").write_text(
            "".join(f"{'project' if path == native else 'omit'} {path.name}\n" for path in files))


def prepare(output, scratch, archive, base, tested, pattern):
    read_official(output, archive, base, tested)
    freeze_setup(output, scratch)
    (output / "cases.tsv").write_text(f"01\t{PACKAGE}\tdefault\tordinary\t{pattern}\n"
                                     f"02\t{PACKAGE}\tstatic-only\tisolated\t{pattern}\n")
    for name, keys in (("base", BASE_NAMES), ("head", HEAD_NAMES)):
        (output / (name + "-names.txt")).write_text("\n".join(sorted(keys)) + "\n")
    for condition, ab, aa in (("ordinary", HEAD_NAMES, BASE_NAMES), ("isolated", HEAD_NAMES, HEAD_NAMES)):
        path = output / condition
        path.mkdir()
        write_json(path / "aa.expected.json", expected(aa, aa))
        write_json(path / "ab.expected.json", expected(aa, ab))
    write_json(output / "matched.expected.json", expected(COMMON, COMMON))


def verify_raw(output):
    with (output / "order.tsv").open() as stream:
        rows = list(csv.DictReader(stream, delimiter="\t"))
    if len(rows) != 160 or len({row["raw"] for row in rows}) != 160:
        raise ValueError("unexpected number of native measurements")
    for row in rows:
        text = (output / row["raw"]).read_text().splitlines()
        names = HEAD_NAMES if row["case"] == "02" or row["revision"] == "head" else BASE_NAMES
        if sorted(line.split()[0] for line in text if line.startswith(ROOT)) != sorted(names):
            raise ValueError(f"unexpected native inventory in {row['raw']}")
        if {line[5:].strip() for line in text if line.startswith("cpu: ")} != {CPU}:
            raise ValueError(f"unexpected runner CPU in {row['raw']}")


def gate_rows(stem, phase):
    names = COMMON
    if phase == "confirmation":
        targets = [line.split("\t") for line in Path(f"{stem}.targets.tsv").read_text().splitlines()]
        names = {name for package, name in targets if package == PACKAGE and name in COMMON}
        if not names or len(names) != len(targets):
            raise ValueError("unexpected confirmation targets")
    write_json(Path(f"{stem}.{phase}.gate.json"), expected(names, names))
    for role in ("left", "right"):
        lines = Path(f"{stem}.{phase}.{role}.txt").read_text().splitlines(keepends=True)
        selected = [line for line in lines if not line.startswith("Benchmark") or line.split()[0] in names]
        Path(f"{stem}.{phase}.gate.{role}.txt").write_text("".join(selected))


if __name__ == "__main__":
    command, *args = sys.argv[1:]
    if command == "prepare":
        prepare(*map(Path, args[:3]), *args[3:])
    elif command == "verify-raw":
        verify_raw(Path(args[0]))
    elif command == "gate-rows":
        gate_rows(*args)
    else:
        sys.exit(f"unknown mode {command}")
