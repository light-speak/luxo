#!/usr/bin/env bash
set -euo pipefail
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
python3 - "$repo" <<'PYTHON'
import hashlib
from pathlib import Path
import sys
root = Path(sys.argv[1])
pins = {
  "scripts/diagnose-hosted-perf.sh": "9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072",
  "scripts/benchrun/main.go": "5fadbbd59556c291f9fdd5d8705647fec6fbe7156e3637635f085f4e306a0c68",
  "scripts/benchrun/pair.go": "a9e024af7035e6e6e5e9610df9c4e97215b57700b8f1a290e1cc6d86f99b5a4e",
  "internal/benchlayout/layout.go": "2531f2d279bfe4978b38a4fdffdc74561a613cfd81711f885eeeea2901e61984"
}
for name, digest in pins.items():
    assert hashlib.sha256((root / name).read_bytes()).hexdigest() == digest, name
PYTHON
source "$script_dir/diagnose-hosted-perf.sh"
base=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
head=ec041a07b105c9caf9e327a2cbf73de772582d00
product=b0b7d9b7c51aa67f14bf8545f05e2339ae26f187
mode="${1:---plan-only}"
[[ $# -le 1 && ( "$mode" == run || "$mode" == --plan-only ) ]]
mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/pr71-all-confirmed.XXXXXXXX")
evidence="$repo/.tmp/hosted-pr71-all-confirmed"
if [[ "$mode" == --plan-only ]]; then evidence="$scratch/evidence"; fi
cleanup_pr71() {
  local status=$?
  trap - EXIT
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/pr71-all-confirmed.* ]] || exit 99
  rm -rf -- "$scratch"
  if [[ "$mode" == run && -d "$evidence" ]]; then
    printf 'exit=%s\nowned_scratch_removed=true\n' "$status" > "$evidence/cleanup.txt"
  fi
  exit "$status"
}
trap cleanup_pr71 EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$evidence"
test "$(git -C "$repo" merge-tree --write-tree "$base" "$head")" = "$product"
printf 'base=%s\nhead=%s\nproduct_tree=%s\nofficial_tested_merge=%s\n' \
  "$base" "$head" "$product" 12fc97a91421082200f0fbbf9c9914fb1a68ca8d > "$evidence/revisions.txt"
python3 - "$evidence" <<'PYTHON'
import json
from pathlib import Path
import re
import sys
rows = [
  {
    "package": "pkg/lux/auth",
    "name": "BenchmarkStaticJWT/Identity/After",
    "role": "official-confirmed"
  },
  {
    "package": "pkg/lux/api",
    "name": "BenchmarkNullableRootJSON/int/plain",
    "role": "official-confirmed"
  },
  {
    "package": "pkg/lux/api",
    "name": "BenchmarkNullableRootJSON/int/present",
    "role": "official-confirmed"
  },
  {
    "package": "pkg/lux/api",
    "name": "BenchmarkNullableRootJSON/zero/plain",
    "role": "official-confirmed"
  },
  {
    "package": "pkg/lux/api",
    "name": "BenchmarkNullableRootJSON/zero/present",
    "role": "official-confirmed"
  },
  {
    "package": "pkg/lux/api",
    "name": "BenchmarkRawJSONArrayUnicode/empty/strict",
    "role": "official-confirmed"
  },
  {
    "package": "pkg/lux/luvia",
    "name": "BenchmarkGatewayIdentity/anonymous/before",
    "role": "official-confirmed"
  },
  {
    "package": "pkg/lux/api",
    "name": "BenchmarkBinaryListPayload/Decimal/count=1/version=0",
    "role": "official-confirmed"
  },
  {
    "package": "pkg/lux/api",
    "name": "BenchmarkBinaryListPayload/Duration/count=1/version=0",
    "role": "official-confirmed"
  },
  {
    "package": "pkg/lux/luvia",
    "name": "BenchmarkTraceEncodingComparison/true/reservation-candidate",
    "role": "official-confirmed"
  },
  {
    "package": "pkg/lux/api",
    "name": "BenchmarkRawJSONArrayPool/empty/encode",
    "role": "official-confirmed"
  },
  {
    "package": "pkg/lux/api",
    "name": "BenchmarkRawJSONArrayPool/precision/encode",
    "role": "official-confirmed"
  },
  {
    "package": "pkg/lux/api",
    "name": "BenchmarkRegistrySteadyRequest/params=1/json",
    "role": "official-confirmed"
  },
  {
    "package": "pkg/lux/rpc",
    "name": "BenchmarkLRPCPoolLimits/serial/after",
    "role": "official-confirmed"
  },
  {
    "package": "pkg/lux/api",
    "name": "BenchmarkModuleColorWarm",
    "role": "official-confirmed"
  },
  {
    "package": "pkg/lux/luvia",
    "name": "BenchmarkPrimaryKeys/row-small/keyLast=false/baseline=true",
    "role": "official-confirmed"
  },
  {
    "package": "pkg/lux/luvia",
    "name": "BenchmarkPrimaryKeys/row-small/keyLast=true/baseline=true",
    "role": "official-confirmed"
  },
  {
    "package": "pkg/lux/luvia",
    "name": "BenchmarkPrimaryKeys/row-wide/keyLast=false/baseline=true",
    "role": "official-confirmed"
  },
  {
    "package": "pkg/lux/rpc",
    "name": "BenchmarkRPCHandlerCompletion/unobserved/error",
    "role": "official-confirmed"
  },
  {
    "package": "pkg/lux/auth",
    "name": "BenchmarkStaticJWT/Identity/Before",
    "role": "previous-shadow-control"
  },
  {
    "package": "pkg/lux/luvia",
    "name": "BenchmarkGatewayIdentity/anonymous/after",
    "role": "previous-shadow-control"
  },
  {
    "package": "pkg/lux/auth",
    "name": "BenchmarkStaticJWT/Sign/Before",
    "role": "direct-sign-control"
  },
  {
    "package": "pkg/lux/auth",
    "name": "BenchmarkStaticJWT/Sign/After",
    "role": "direct-sign-control"
  },
  {
    "package": "pkg/lux/auth",
    "name": "BenchmarkJWTDurationConfig/7d",
    "role": "direct-duration-control"
  },
  {
    "package": "pkg/lux/auth",
    "name": "BenchmarkJWTDurationConfig/1d12h",
    "role": "direct-duration-control"
  },
  {
    "package": "pkg/lux/auth",
    "name": "BenchmarkJWTDurationConfig/0.5d",
    "role": "direct-duration-control"
  },
  {
    "package": "pkg/lux/auth",
    "name": "BenchmarkJWTDurationConfig/30m",
    "role": "direct-duration-control"
  }
]
out = Path(sys.argv[1])
assert len(rows) == 27
assert sum(row["role"] == "official-confirmed" for row in rows) == 19
assert len({(row["package"], row["name"]) for row in rows}) == 27
packages = sorted({row["package"] for row in rows})
cases, expected = [], []
for index, package in enumerate(packages, 1):
    names = [row["name"] for row in rows if row["package"] == package]
    filters = ["/".join("^" + re.escape(part) + "$" for part in name.split("/")) for name in names]
    cases.append(f"{index:02d}\t{package}\tdefault\tpr71-confirmed-and-controls\t" + "|".join(filters))
    expected.extend({"Package": package, "Name": name + "-2"} for name in names)
(out / "cases.tsv").write_text("\n".join(cases) + "\n")
(out / "packages.txt").write_text("\n".join(packages) + "\n")
(out / "expected.json").write_text(json.dumps({"Base": expected, "Head": expected}, indent=2) + "\n")
(out / "scope.json").write_text(json.dumps(rows, indent=2) + "\n")
PYTHON
if [[ "$mode" == --plan-only ]]; then
  cat "$evidence/revisions.txt" "$evidence/cases.tsv"
  printf 'groups=primary,confirmation samples_per_group=10 comparisons=aa,ab expected_leaves=27 official_confirmed=19\n'
  exit 0
fi
export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
mkdir -p "$TMPDIR" "$scratch/source/base" "$scratch/source/head" "$scratch/bin/base" "$scratch/bin/head" "$evidence/pairs"
[[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]]
[[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
[[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
[[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
collect_host > "$evidence/environment.txt"
cp "${BASH_SOURCE[0]}" "$script_dir/diagnose-hosted-perf.sh" "$evidence/"
git -C "$repo" archive "$base" | tar -xf - -C "$scratch/source/base"
git -C "$repo" archive "$product" | tar -xf - -C "$scratch/source/head"
for role in base head; do
  mkdir "$scratch/source/$role/.tmp"
  sh "$scratch/source/$role/scripts/setup-modules.sh" "$scratch/source/$role" > "$evidence/workspace-$role.log" 2>&1
  cp "$scratch/source/$role/go.work" "$evidence/go-$role.work.txt"
done
(
  cd "$repo"
  test -f internal/benchlayout/layout.go
  go build -o "$scratch/bin/benchrun" ./scripts/benchrun
  cd "$scratch/source/base"
  for tool in benchplan benchgate; do go build -o "$scratch/bin/$tool" "./scripts/$tool"; done
  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  go version -m "$scratch/bin/benchstat"
) > "$evidence/tools.log" 2>&1
bind_pr71_sources() {
  python3 - "$scratch/source" "$evidence" "$1" <<'PYTHON'
import hashlib
import json
from pathlib import Path
import sys
source, out, phase = Path(sys.argv[1]), Path(sys.argv[2]), sys.argv[3]
result = {side: {str(path.relative_to(source / side)): hashlib.sha256(path.read_bytes()).hexdigest()
                 for path in sorted((source / side).rglob("*"))
                 if path.is_file() and (path.suffix == ".go" or path.name in ("go.mod", "go.sum"))
                 and ".tmp" not in path.relative_to(source / side).parts}
          for side in ("base", "head")}
if phase == "after":
    assert result == json.loads((out / "sources-before.json").read_text()), "source drift"
(out / ("sources-" + phase + ".json")).write_text(json.dumps(result, indent=2) + "\n")
PYTHON
}
bind_pr71_sources before
while IFS= read -r package; do
  label="${package//\//-}"
  pair="$scratch/pair-$label"
  "$scratch/bin/benchrun" compile-pair "$scratch/source/base" "$scratch/source/head" "$pair" "$package" \
    > "$evidence/pairs/$label.compile.log" 2>&1
  cp "$pair/fixtures.json" "$evidence/pairs/$label.json"
  jq -e '.Complete == true and (.Packages | length) > 0' "$pair/fixtures.json" >/dev/null
  for role in base head; do
    for binary in "$pair/$role/"*.test; do ln -s "$binary" "$scratch/bin/$role/${binary##*/}"; done
  done
done < "$evidence/packages.txt"
for role in base head; do
  record_binaries "$role"
  for binary in "$scratch/bin/$role/"*.test; do
    name="${binary##*/}"
    go tool nm -size "$binary" > "$evidence/symbols-$role-$name.nm"
    go tool objdump -s 'Benchmark(StaticJWT|JWTDurationConfig|NullableRootJSON|BinaryListPayload|RawJSONArrayPool|RawJSONArrayUnicode|RegistrySteadyRequest|ModuleColorWarm|GatewayIdentity|TraceEncodingComparison|PrimaryKeys|LRPCPoolLimits|RPCHandlerCompletion)|[.](signWithSubject|parseDuration|parseExtendedDuration|parseDurationWithMultiplier|scaleDuration|hmacSign)$' \
      "$binary" > "$evidence/symbols-$role-$name.objdump"
    grep -q '^TEXT ' "$evidence/symbols-$role-$name.objdump"
  done
  while IFS=$'\t' read -r case_id package variant name filter; do
    "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$role" "$package" \
      -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
      -test.timeout=90s > "$evidence/inventory-$role-$case_id.txt" 2>> "$evidence/benchmark-stderr.txt"
  done < "$evidence/cases.tsv"
  cat "$evidence"/inventory-"$role"-*.txt > "$scratch/inventory-$role.raw.txt"
  "$scratch/bin/benchplan" --normalize "$scratch/inventory-$role.raw.txt" "$evidence/inventory-$role.txt"
done
python3 - "$evidence" <<'PYTHON'
import collections
import json
from pathlib import Path
import sys
out = Path(sys.argv[1])
expected = json.loads((out / "expected.json").read_text())
cpus = {}
for role, key in (("base", "Base"), ("head", "Head")):
    actual = collections.Counter()
    package = None
    observed_cpus = set()
    for line in (out / ("inventory-" + role + ".txt")).read_text().splitlines():
        if line.startswith("pkg: "):
            package = line[5:].removeprefix("github.com/light-speak/luxo/")
        elif line.startswith("cpu: "):
            observed_cpus.add(line[5:].strip())
        elif line.startswith("Benchmark"):
            fields = line.split()
            assert package and len(fields) == 8 and fields[3::2] == ["ns/op", "B/op", "allocs/op"]
            actual[package, fields[0]] += 1
    want = collections.Counter((row["Package"], row["Name"]) for row in expected[key])
    assert actual == want, (role, actual - want, want - actual)
    assert len(observed_cpus) == 1 and next(iter(observed_cpus))
    cpus[role] = observed_cpus
assert cpus["base"] == cpus["head"], cpus
PYTHON
# These extra head-native benchmarks are observations, not paired historical evidence.
"$scratch/bin/benchrun" compile "$scratch/source/head" "$scratch/bin/head-native" pkg/lux/auth \
  > "$evidence/auth-head-native.compile.log" 2>&1
"$scratch/bin/benchrun" run "$scratch/source/head" "$scratch/bin/head-native" pkg/lux/auth \
  -test.run='^$' -test.bench='^BenchmarkJWTDurationConfig$/^(1e-1\*10s|0x1p-1\*24h)$' \
  -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 -test.timeout=90s > "$evidence/auth-head-only.txt"
test "$(grep -c '^BenchmarkJWTDurationConfig/' "$evidence/auth-head-only.txt")" = 2
record_binaries head-native
sample_groups
bind_pr71_sources after
python3 - "$evidence" <<'PYTHON'
import csv
from collections import Counter
from pathlib import Path
import sys
out = Path(sys.argv[1])
rows = list(csv.DictReader((out / "order.tsv").open(), delimiter="\t"))
cases = [line.split("\t")[0] for line in (out / "cases.tsv").read_text().splitlines()]
expected = {(g, f"{n:02d}", comparison, case, side): 1
            for g in ("primary", "confirmation") for n in range(1, 11)
            for comparison in ("aa", "ab") for case in cases for side in ("left", "right")}
assert Counter((r["group"], r["pair"], r["comparison"], r["case"], r["side"]) for r in rows) == expected
cpus = set()
for row in rows:
    assert row["revision"] == ("head" if row["comparison"] == "ab" and row["side"] == "right" else "base")
    raw = (out / row["raw"]).read_text()
    found = {line[5:].strip() for line in raw.splitlines() if line.startswith("cpu: ")}
    assert len(found) == 1 and next(iter(found))
    cpus.update(found)
assert len(cpus) == 1, cpus
PYTHON
status=0
printf 'comparison\tgate_exit\n' > "$evidence/verdicts.tsv"
for comparison in aa ab; do
  normalize_group "$comparison" primary
  normalize_group "$comparison" confirmation
  args=("$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv")
  if [[ "$comparison" == ab ]]; then
    args=(--approved-costs "$scratch/source/head/scripts/benchgate/approved-costs.json" --base-sha "$base" "${args[@]}")
  fi
  code=0
  "$scratch/bin/benchgate" "${args[@]}" > "$evidence/$comparison.benchgate.log" 2>&1 || code=$?
  printf '%s\t%s\n' "$comparison" "$code" >> "$evidence/verdicts.tsv"
  if (( code != 0 )); then
    grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/$comparison.benchgate.log" || exit "$code"
    status=1
  fi
done
cat "$evidence/verdicts.tsv"
if ! grep -Fxq $'aa\t0' "$evidence/verdicts.tsv"; then
  printf '%s\n' 'Same-binary control crossed the unchanged threshold; attribution is inconclusive.'
fi
exit "$status"
