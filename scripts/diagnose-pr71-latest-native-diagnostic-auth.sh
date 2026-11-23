#!/usr/bin/env bash
set -euo pipefail

pr71_write_plan() {
  python3 - "$evidence" <<'PYTHON'
import json
from pathlib import Path
import sys

evidence = Path(sys.argv[1])
cache_names = [
    f"BenchmarkCacheOriginBudget/{kind}/hit=false/after-2"
    for kind in ("background", "cancelable", "deadline")
]
cache_filter = "|".join(
    f"^BenchmarkCacheOriginBudget$/^{kind}$/^hit=false$/^after$"
    for kind in ("background", "cancelable", "deadline")
)
identity = "BenchmarkIdentityConditionValidity/wrong-kind/EnumReference-2"
identity_filter = "^BenchmarkIdentityConditionValidity$/^wrong-kind$/^EnumReference$"
ws = "BenchmarkWSUnaryInvocationCompletion/business-error-2"
ws_filter = "^BenchmarkWSUnaryInvocationCompletion$/^business-error$"
cases = [
    ("full", "api", "default", cache_filter, cache_names),
    ("full", "api", "default", f"{identity_filter}|{ws_filter}", [identity, ws]),
    ("full", "luvia", "default", "^BenchmarkPrimaryKeys$/^row-wide$/^keyLast=true$/^baseline=true$",
     ["BenchmarkPrimaryKeys/row-wide/keyLast=true/baseline=true-2"]),
    ("full", "luvia", "default", "^BenchmarkMergeDestination$/^columnar=true$/^rows=1$/^direct=true$",
     ["BenchmarkMergeDestination/columnar=true/rows=1/direct=true-2"]),
    ("ws-alone", "api", "default", ws_filter, [ws]),
    ("lean", "api", "lean", cache_filter, cache_names),
    ("lean", "api", "lean", ws_filter, [ws]),
    ("jwt", "auth", "default", "^BenchmarkStaticJWT$/^Sign$/^After$", ["BenchmarkStaticJWT/Sign/After-2"]),
    ("jwt", "auth", "default", r"^BenchmarkJWTDurationConfig$/^(7d|0\.5d|30m)$",
     [f"BenchmarkJWTDurationConfig/{duration}-2" for duration in ("7d", "0.5d", "30m")]),
]
rows = []
expected = {}
for index, (context, name, variant, filter, leaves) in enumerate(cases, 1):
    package = f"pkg/lux/{name}"
    row = {"id": f"{index:02}", "context": context, "package": package,
           "variant": variant, "filter": filter, "leaves": leaves}
    rows.append(row)
    expected.setdefault(context, []).extend({"Package": package, "Name": leaf} for leaf in leaves)

if {mode: len(leaves) for mode, leaves in expected.items()} != {
    "full": 7, "ws-alone": 1, "lean": 4, "jwt": 4,
}:
    raise SystemExit("unexpected #71 benchmark scope")
for leaves in expected.values():
    if len(leaves) != len({(r["Package"], r["Name"]) for r in leaves}):
        raise SystemExit("benchmark leaf repeated in one gate context")
for filename, value in (("cases.json", rows), ("expected-by-context.json", expected)):
    (evidence / filename).write_text(json.dumps(value, indent=2) + "\n")
(evidence / "cases.tsv").write_text("".join(
    "\t".join((r["id"], r["package"], r["variant"], r["leaves"][0], r["filter"])) + "\n" for r in rows
))
PYTHON
  (
    cd "$evidence"
    printf '%s  %s\n' \
      bb7bf4cbf1ef2622b9f3c6bc26156438f16e2b499abc34da0fe4c7f5b8b2b551 cases.tsv \
      6a4388bc60c39724f8e36b1bdd44eef52c1cb0b09629cedb204f83832ef63827 cases.json \
      d30c0fea4505d79f443b722eb7c3535bf5864c530e898945601068ec82afa312 expected-by-context.json > scope.sha256
    sha256sum -c scope.sha256
  )
}

pr71_prepare_sources() {
  python3 - "$scratch/source" "$evidence" <<'PYTHON'
from hashlib import sha256
import json
from pathlib import Path
import re
import sys

sources, evidence = map(Path, sys.argv[1:])
base, head = (sources / role for role in ("base", "head"))
linked_fixtures = {
    "pkg/lux/api/identity_reader_bench_test.go": "20d45a1c2a71419d6c2c44e33b49e691cf15f3522f6e1041541dbce5097a3c5f",
    "pkg/lux/api/identity_reader_fuzz_test.go": "f4e765934e382ad361aedac0dea15864efcdeb553ef9e6556adb66b65c0765a2",
    "pkg/lux/api/identity_reader_test.go": "600116795f36c98c7268237d2df5fefabb9d5708cf25ba9219ea4b1dcf21cf4c",
    "pkg/lux/api/identity_value_test.go": "619664ee4f2a8c715c97bc3a661b9a845d1dbd8411bb575a7ac672ec4ee3d560",
}
unchanged = (
    *linked_fixtures,
    "pkg/lux/api/response_cache_cancellation_bench_test.go",
    "pkg/lux/api/middleware.go",
    "pkg/lux/api/websocket_abnormal_bench_test.go",
    "pkg/lux/api/websocket_call.go",
    "pkg/lux/api/router.go",
    "pkg/lux/api/identity_reader.go",
    "pkg/lux/api/identity_value.go",
    "pkg/lux/luvia/primary_keys_bench_test.go",
    "pkg/lux/luvia/merger_bench_test.go",
    "pkg/lux/luvia/merger.go",
    "pkg/lux/luvia/luvia.go",
    "pkg/lux/auth/jwt_test.go",
)
changed = (
    "pkg/lux/auth/jwt.go",
    "pkg/lux/auth/jwt_duration_test.go",
    "pkg/lux/auth/static_bench_test.go",
)


def function(root, path, name):
    data = (root / path).read_bytes()
    pattern = rb"^func " + name.encode() + rb"\(.*?^}\n"
    matches = re.findall(pattern, data, re.M | re.S)
    if len(matches) != 1:
        raise SystemExit(f"expected one {name} in {path}")
    return matches[0]


for path in unchanged:
    if (base / path).read_bytes() != (head / path).read_bytes():
        raise SystemExit(f"unexpected change to full benchmark source: {path}")
for path in changed:
    if (base / path).read_bytes() == (head / path).read_bytes():
        raise SystemExit(f"missing change to direct JWT source: {path}")

static_file = "pkg/lux/auth/static_bench_test.go"
phase = "benchmarkStaticJWTPhase"
if function(base, static_file, phase) != function(head, static_file, phase):
    raise SystemExit("direct JWT timing implementation differs")
duration_workloads = []
for source in (base, head):
    outer = function(source, static_file, "BenchmarkStaticJWT")
    inputs = (b"cfg := testConfig()", b"writer := testClaims(42, \"ADMIN\")", b"token, err := Sign(cfg, writer)")
    if any(fragment not in outer for fragment in inputs):
        raise SystemExit("missing pinned inputs for StaticJWT/Sign/After")
    duration = function(source, "pkg/lux/auth/jwt_duration_test.go", "BenchmarkJWTDurationConfig")
    loop = re.search(rb"for _, input := range \[\]string\{([^\n]+)\} \{", duration)
    if loop is None or tuple(re.findall(rb'"([^"]+)"', loop[1])[:4]) != (b"7d", b"1d12h", b"0.5d", b"30m"):
        raise SystemExit("common JWT duration inputs or order changed")
    duration_workloads.append(duration[:loop.start(1)] + duration[loop.end(1):])
if duration_workloads[0] != duration_workloads[1]:
    raise SystemExit("direct JWT duration timing implementation changed")

for source in (base, head):
    role = source.name
    replacements = {}
    for path, digest in linked_fixtures.items():
        fixture = source / path
        if not path.endswith("_test.go") or sha256(fixture.read_bytes()).hexdigest() != digest:
            raise SystemExit(f"lean overlay source SHA-256 mismatch: {path}")
        replacements[str(fixture)] = ""
    (evidence / f"lean-{role}-overlay.json").write_text(json.dumps({"Replace": replacements}, indent=2) + "\n")
    manifest = "".join(f"{sha256((source / path).read_bytes()).hexdigest()}  {path}\n"
                       for path in (*unchanged, *changed, "go.mod", "go.sum", "scripts/benchgate/approved-costs.json"))
    (evidence / f"sources-{role}.sha256").write_text(manifest)
PYTHON
}

pr71_record_layout() {
  local role="$1" package="$2" variant="$3" directory="$scratch/bin/$1"
  local name="$1-$3-$2" binary pattern
  if [[ "$variant" != default ]]; then directory+="-$variant"; fi
  binary="$directory/pkg-lux-$package.test"
  pattern='pkg/lux/(api|auth|luvia)[.].*(CacheOriginBudget|cacheResponse|cacheKeyInNamespace|cacheOrigin|IdentityConditionValidity|WSUnaryInvocationCompletion|completeBenchmarkWSInvocation|wsUnaryCall|callHandler|PrimaryKeys|MergeDestination|AppendMergedColumnar|signWithSubject|[Ss]ign|[Vv]erify|parseDuration|loadConfigFromEnv|StaticJWT|JWTDurationConfig)'
  go tool nm -size "$binary" | awk -v pattern="$pattern" '$4 ~ pattern' > "$evidence/symbols-$name.nm"
  go tool objdump -s "$pattern" "$binary" > "$evidence/symbols-$name.objdump"
  test -s "$evidence/symbols-$name.nm"
  test -s "$evidence/symbols-$name.objdump"
  if [[ "$package" == api || "$package" == luvia ]]; then
    local sign=absent duration=absent load=absent
    if grep -Eq 'pkg/lux/auth[.]signWithSubject$' "$evidence/symbols-$name.nm"; then sign=present; fi
    if grep -Eq 'pkg/lux/auth[.]parseDuration$' "$evidence/symbols-$name.nm"; then duration=present; fi
    if grep -Eq 'pkg/lux/auth[.]loadConfigFromEnv$' "$evidence/symbols-$name.nm"; then load=present; fi
    printf '%s\t%s\t%s\t%s\t%s\n' "$name" "$(sha256sum "$binary" | cut -d' ' -f1)" \
      "$sign" "$duration" "$load" >> "$evidence/jwt-linkage.tsv"
  fi
}

pr71_compile() {
  local role="$1" source="$scratch/source/$1" package
  (
    for package in api luvia auth; do
      "$scratch/bin/benchrun" compile "$source" "$scratch/bin/$role" "pkg/lux/$package"
    done
  ) > "$evidence/compile-$role.log" 2>&1
  GOFLAGS="-overlay=$evidence/lean-$role-overlay.json" "$scratch/bin/benchrun" compile \
    "$source" "$scratch/bin/$role-lean" pkg/lux/api > "$evidence/compile-$role-lean.log" 2>&1
  record_binaries "$role"
  record_binaries "$role-lean"
  for package in api luvia auth; do
    pr71_record_layout "$role" "$package" default
  done
  pr71_record_layout "$role" api lean
}

pr71_inventory() {
  local role id package variant name filter directory
  for role in base head; do
    while IFS=$'\t' read -r id package variant name filter; do
      directory="$scratch/bin/$role"
      if [[ "$variant" != default ]]; then directory+="-$variant"; fi
      "$scratch/bin/benchrun" run "$scratch/source/$role" "$directory" "$package" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 -test.timeout=90s \
        > "$evidence/inventory-$role-$id.txt"
    done < "$evidence/cases.tsv"
  done
  python3 - "$evidence/process-verifier.py" "$evidence" <<'PYTHON'
import json
from pathlib import Path
import runpy
import sys

verifier, evidence = map(Path, sys.argv[1:])
check = runpy.run_path(str(verifier))["verify_process"]
cases = json.loads((evidence / "cases.json").read_text())
cpus = {
    check(case, evidence / f"inventory-{role}-{case['id']}.txt", inventory=True)
    for role in ("base", "head") for case in cases
}
if len(cases) != 9 or len(cpus) != 1:
    raise SystemExit("inventory membership or platform differs")
PYTHON
}

pr71_analyze() {
  local context report="$evidence" status=0
  python3 "$report/process-verifier.py" split "$report" "$scratch/contexts"
  for context in full ws-alone lean jwt; do
    mkdir -p "$report/results/$context"
    (
      evidence="$scratch/contexts/$context"
      analyze_groups
      cp "$evidence/expected.json" "$evidence/verdicts.tsv" "$evidence/"aa.* "$evidence/"ab.* "$report/results/$context/"
    )
  done
  for context in full jwt; do
    if ! grep -Fxq $'aa\t0' "$report/results/$context/verdicts.tsv" ||
       ! grep -Fxq $'ab\t0' "$report/results/$context/verdicts.tsv"; then
      printf '%s did not pass the original thresholds. Inspect its independent A/A and A/B evidence.\n' "$context" >&2
      status=1
    fi
  done
  return "$status"
}

pr71_main() {
  [[ $# -eq 0 ]] || { printf 'Usage: %s\n' "$0" >&2; return 2; }
  local role revision helper=cbf4cfb383d944ceb38f700e19cf79c4c54d3c58
  local verifier=eb42833ce74cd9f5822adf5746aaffcdbd56999d
  repo=$(git rev-parse --show-toplevel)
  test "$(pwd -P)" = "$repo"
  mkdir -p "$repo/.tmp"
  test ! -L "$repo/.tmp"
  evidence="$repo/.tmp/hosted-pr71-latest-native-diagnostic-auth"
  mkdir "$evidence"
  git -C "$repo" show "$helper:scripts/diagnose-hosted-perf.sh" > "$evidence/sampler.sh"
  git -C "$repo" show "$verifier:scripts/diagnose-pr79-pr81-attribution.py" > "$evidence/process-verifier.py"
  (
    cd "$evidence"
    printf '%s  %s\n' \
      9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072 sampler.sh \
      b3f5275d56a4a88c0bf34ac82b4dc6bcea1f946b4d5b0638099521654cd80b9e process-verifier.py > helpers.sha256
    sha256sum -c helpers.sha256
  )
  printf 'sampler\t%s\nverifier\t%s\n' "$helper" "$verifier" > "$evidence/helper-revisions.tsv"
  source "$evidence/sampler.sh"
  base=ba94103efe6e623849ba98df20fe6cf14b2d3596
  head=11a1b246b7cfb88926d84bb53424320fd3be3c48
  git -C "$repo" merge-base --is-ancestor "$base" "$head"
  git -C "$repo" diff --exit-code "$base" "$head" -- scripts/benchrun scripts/benchplan scripts/benchgate
  scratch=$(mktemp -d "$repo/.tmp/.pr71-latest-auth.XXXXXX")
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
  mkdir -p "$TMPDIR" "$scratch/bin"
  [[ "$(go env GOVERSION)" == go1.27.1 ]]
  [[ "$(go env GOOS):$(go env GOARCH):$(go env GOAMD64):$(go env CGO_ENABLED)" == linux:amd64:v1:1 ]]
  [[ "$(go env GOHOSTOS):$(go env GOHOSTARCH)" == linux:amd64 ]]
  [[ -z "$(go env GOFLAGS)" && -z "$(go env GOEXPERIMENT)" && -z "${LUXO_WS_INVOKE_BASELINE:-}" ]]
  collect_host > "$evidence/environment.txt"
  cat > "$evidence/protocol.txt" <<'PROTOCOL'
Original #71 run 45237843435 attempt 1 measured merge aeca42f against ba94103.
This diagnostic compiles the two exact archived revisions. ba94103 is an ancestor of 11a1b246.
Full retains the original confirmation selections of shards 03, 06 and 08.
The full context covers seven leaves; the original primary shard inventories remain independent.
Shard 07 stopped during fixture setup and has no confirmed timing result.
The standalone WS context uses the full linked API binary without the identity fixture in that process.
The lean context removes four test-only JWT fixture files symmetrically, while leaving all production code.
Luvia uses JWT configuration in production and retains its full binary.
Direct JWT controls use the common 7d, 0.5d, 30m durations and the valid one-hour StaticJWT Sign/After fixture.
The common inputs produce the same durations. Historical Before and head-only inputs are outside these direct controls.
Default linker, Go 1.27.1 Linux amd64 v1 CGO=1, benchmem, 200ms, CPU=2, benchstat alpha .01, original thresholds.
Each context has two groups of ten A/A and A/B alternating pairs; only each context's comparisons are combined.
A/A and A/B on full and direct JWT controls are judged using the original gate.
A/A must be clean and full must reproduce before attributing a timing difference to fixture linkage.
Check symbol retention and binary hashes before interpreting the lean results. The normal PR gate is independent.
PROTOCOL
  pr71_write_plan
  for role in base head; do
    revision="$base"
    if [[ "$role" == head ]]; then revision="$head"; fi
    mkdir -p "$scratch/source/$role" "$scratch/bin/$role"
    git -C "$repo" archive "$revision" | tar -xf - -C "$scratch/source/$role"
    git -C "$repo" show -s --format="$role %H %T" "$revision" >> "$evidence/revisions.txt"
    sh "$scratch/source/$role/scripts/setup-modules.sh" "$scratch/source/$role"
    cp "$scratch/source/$role/go.work" "$evidence/go-$role.work.txt"
  done
  pr71_prepare_sources
  (
    cd "$scratch/source/base"
    for role in benchrun benchplan benchgate; do
      go build -o "$scratch/bin/$role" "./scripts/$role"
    done
    GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  ) > "$evidence/compile-tools.log" 2>&1
  go version -m "$scratch/bin/benchstat" > "$evidence/benchstat-build.txt"
  printf 'binary\tsha256\tsignWithSubject\tparseDuration\tloadConfigFromEnv\n' > "$evidence/jwt-linkage.tsv"
  for role in base head; do pr71_compile "$role"; done
  pr71_inventory
  date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  sample_groups
  date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  pr71_analyze
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  pr71_main "$@"
fi
