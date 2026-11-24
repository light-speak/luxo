#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
source "$script_dir/diagnose-hosted-perf.sh"
base=1117eb9ffcc0beef977abceec656a351b0988389
base_tree=97e7de78aafc023063c8ad6e44ecfaa3dab04d35
head=95555312e542300f7abb51bb0310f5d535b9fe4b
tested=0c28eb29e5e55ba5db86d236218825448f98a832
head_tree=7a9483fa27dfc7d6a4557347d421e70178eb6826
artifact=14928967579
cpu='INTEL(R) XEON(R) PLATINUM 8573C'
package=pkg/lux/luvia
filter='^BenchmarkGatewayIdentity$/^(anonymous|authenticated|typed-nil)$/^(before|after)$'
evidence="$repo/.tmp/hosted-pr74-gateway-identity"

matches_cpu() {
  local model
  model=$(LC_ALL=C lscpu | awk -F: '/^Model name:/ { sub(/^ +/, "", $2); print toupper($2) }')
  [[ "$(uname -s):$(uname -m)" == Linux:x86_64 && "$model" == "$cpu" ]]
}

write_scope() {
  python3 - "$1" "$evidence" "$base" "$tested" "$cpu" "$package" "$filter" <<'PYTHON'
import hashlib
import json
from pathlib import Path
import sys
import zipfile

archive, output = map(Path, sys.argv[1:3])
base, tested, cpu, package, pattern = sys.argv[3:]
digest = hashlib.sha256(archive.read_bytes()).hexdigest()
if digest != "1d3e94811fea5108524cc479e4810f2b6b21b383c0d02b019a69be9b82b9b3ed":
    raise SystemExit("unexpected formal artifact SHA-256")
names = {f"BenchmarkGatewayIdentity/{case}/{stage}-2"
         for case in ("anonymous", "authenticated", "typed-nil") for stage in ("before", "after")}
with zipfile.ZipFile(archive) as data:
    if data.read("benchmark-revisions.txt").decode() != f"base_sha={base}\ntested_sha={tested}\n":
        raise SystemExit("unexpected formal revisions")
    for item in ("benchmark-expected.json", "benchmark-confirmation-expected.json"):
        for keys in json.loads(data.read(item)).values():
            found = {entry["Name"] for entry in keys if entry["Package"] == package
                     and entry["Name"].startswith("BenchmarkGatewayIdentity/")}
            expected = names if item == "benchmark-expected.json" else names - {"BenchmarkGatewayIdentity/anonymous/after-2"}
            if found != expected:
                raise SystemExit(f"unexpected formal inventory in {item}")
    rows = data.read("benchmark-confirmation-batch.tsv").decode().splitlines()
    if len(rows) != 1 or rows[0].split("\t")[:2] != [package, "default"]:
        raise SystemExit("unexpected formal compilation profile")
    for group in ("primary", "confirmation"):
        for role in ("base", "head"):
            raw = data.read(f"benchmark-raw-{group}-{role}.txt").decode().splitlines()
            if {line for line in raw if line.startswith("cpu: ")} != {f"cpu: {cpu}"}:
                raise SystemExit("unexpected formal raw sample CPU")
    for item in ("benchmark-compile-profile.txt", "benchmark-confirmation-targets.tsv", "benchstat-primary.csv",
                 "benchstat-confirmation.csv"):
        (output / f"formal-{item}").write_bytes(data.read(item))
(output / "formal-archive.sha256").write_text(f"{digest}\n")
keys = [{"Package": package, "Name": name} for name in sorted(names)]
(output / "names.txt").write_text("\n".join(sorted(names)) + "\n")
(output / "expected.json").write_text(json.dumps({"Base": keys, "Head": keys}) + "\n")
(output / "cases.tsv").write_text(f"01\t{package}\tdefault\tordinary\t{pattern}\n"
                                 f"02\t{package}\tidentity-only\tisolated\t{pattern}\n")
PYTHON
}

project_fixture() {
  local role="$1"
  mkdir "$scratch/$role"
  python3 - "$scratch/source/$role/$package" "$scratch/$role" "$evidence" "$role" <<'PYTHON'
import hashlib
import json
from pathlib import Path
import sys

package, overlay, output = map(Path, sys.argv[1:4])
role = sys.argv[4]
origin = package / "middleware_test.go"
source = origin.read_bytes()
digest = hashlib.sha256(source).hexdigest()
fixture_sha256 = {
    "base": "a284f6673c86a90ed916099ac82a6ffb0e9b190161c946989957839252b07034",
    "head": "c8d5da22f396349cffd58cea64feabbaff7f57d4f116aaee46954e14aca610a3",
}
if digest != fixture_sha256[role]:
    raise SystemExit("unexpected identity test fixture")
helpers = source[source.index(b"func testCfg("):source.index(b"\nfunc tokenWithTestHeader(")]
benchmark = source[source.index(b"func BenchmarkGatewayIdentity("):
                   source.index(b"\nfunc TestAuthMiddlewareAcceptsWebSocketQueryToken(")]
header = b'''package luvia

import (
	"context"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/auth"
)

'''
replacement = overlay / origin.name
replacement.write_bytes(header + helpers + b"\n" + benchmark)
inventory = sorted(package.glob("*_test.go"))
if len(inventory) != 44 or origin not in inventory:
    raise SystemExit("unexpected Luvia test fixture inventory")
replacements = {str(path): "" for path in inventory}
replacements[str(origin)] = str(replacement)
(overlay / "overlay.json").write_text(json.dumps({"Replace": replacements}, indent=2) + "\n")
(output / f"{role}-overlay-inventory.txt").write_text(
    f"{digest}  {origin.name}\n"
    + "\n".join(f"{'retain' if path == origin else 'omit'} {path.name}" for path in inventory) + "\n"
)
(output / f"{role}-retained.go.txt").write_bytes(replacement.read_bytes())
PYTHON
}

compile_and_inspect() {
  local role="$1" variant="$2" command="$3" bin="$scratch/bin/$1" record="$evidence/$1-$2"
  if [[ "$variant" == identity-only ]]; then
    bin+="-$variant"
    GOFLAGS="-overlay=$scratch/$role/overlay.json" "$scratch/bin/benchrun" compile \
      "$scratch/source/$role" "$bin" "$package"
  else
    "$scratch/bin/benchrun" "$command" "$scratch/source/$role" "$bin" "$package" default
  fi
  record_binaries "${bin##*/}"
  local binary="$bin/pkg-lux-luvia.test"
  local symbols='github\.com/light-speak/luxo/pkg/lux/(luvia\.(BenchmarkGatewayIdentity.*|Identity|New|newGateway.*|internalRequestContext|extractBearerToken)|api\.NewRouter.*|auth\.(LoadConfig.*|loadConfigFromEnv.*))$'
  go tool nm "$binary" | SYMBOL_FILTER="$symbols" awk '$NF ~ ENVIRON["SYMBOL_FILTER"]' > "$record.nm"
  go tool objdump -s "$symbols" "$binary" > "$record.objdump"
  grep -q 'luvia\.BenchmarkGatewayIdentity$' "$record.nm"
  grep -q 'luvia\.BenchmarkGatewayIdentity' "$record.objdump"
  # The symbol can be inlined. Record the actual diagnostic text occurrence too.
  LC_ALL=C grep -aobF 'JWT_SECRET uses the default scaffold placeholder' "$binary" \
    > "$record.placeholder-string.txt" || [[ "$?" == 1 ]]
  "$scratch/bin/benchrun" run "$scratch/source/$role" "$bin" "$package" -test.list='.' > "$record.registry.txt"
  grep -qx BenchmarkGatewayIdentity "$record.registry.txt"
  if [[ "$variant" == identity-only ]]; then
    test "$(cat "$record.registry.txt")" = BenchmarkGatewayIdentity
  else
    grep -qx TestNewGatewayConfiguresStreamIdentityExtraction "$record.registry.txt"
    "$scratch/bin/benchrun" run "$scratch/source/$role" "$bin" "$package" \
      -test.run='^Test(NewGatewayConfiguresStreamIdentityExtraction|ExtractBearerToken.*)$' \
      -test.count=1 -test.timeout=90s > "$record.correctness.txt"
  fi
  "$scratch/bin/benchrun" run "$scratch/source/$role" "$bin" "$package" \
    -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
    -test.timeout=90s > "$record.inventory.txt"
  awk '/^Benchmark/{print $1}' "$record.inventory.txt" | LC_ALL=C sort | cmp "$evidence/names.txt" -
}

target_confirmation() {
  local stem="$1"
  "$scratch/bin/benchgate" --confirmation-targets "$stem.primary.csv" > "$stem.targets.tsv"
  confirmed_args=("$stem.primary.csv")
  [[ -s "$stem.targets.tsv" ]] || return 0
  python3 - "$stem" "$package" <<'PYTHON'
import json
from pathlib import Path
import sys

stem, package = sys.argv[1:]
keys = set()
for entry in Path(stem + ".targets.tsv").read_text().splitlines():
    target_package, name = entry.split("\t")
    if target_package != package or not name.startswith("BenchmarkGatewayIdentity/") or name in keys:
        raise SystemExit("unexpected confirmation target")
    keys.add(name)
expected = [{"Package": package, "Name": key} for key in sorted(keys)]
Path(stem + ".targeted.json").write_text(json.dumps({"Base": expected, "Head": expected}) + "\n")
for side in ("left", "right"):
    raw = Path(f"{stem}.confirmation.{side}.txt").read_text().splitlines(keepends=True)
    selected = [line for line in raw if not line.startswith("Benchmark") or line.split()[0] in keys]
    Path(f"{stem}.targeted.{side}.txt").write_text("".join(selected))
PYTHON
  "$scratch/bin/benchplan" --verify "$stem.targeted.json" "$stem.targeted.left.txt" "$stem.targeted.right.txt"
  "$scratch/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
    base="$stem.targeted.left.txt" head="$stem.targeted.right.txt" > "$stem.targeted.csv" 2> "$stem.targeted.warnings.txt"
  confirmed_args=(--targeted-confirmation "$stem.primary.csv" "$stem.targeted.csv")
}

analyze_profile() {
  local case_id="$1" profile="$2" raw="$evidence/raw"
  local evidence="$evidence/$profile"
  local comparison rc
  local -a confirmed_args policy
  mkdir -p "$evidence/raw"
  cp "$evidence/../expected.json" "$evidence/expected.json"
  ln "$raw/"*".$case_id."*.txt "$evidence/raw/"
  printf 'comparison\tgate_exit\n' > "$evidence/verdicts.tsv"
  for comparison in aa ab; do
    normalize_group "$comparison" primary
    normalize_group "$comparison" confirmation
    policy=()
    if [[ "$comparison" == ab ]]; then
      policy=(--approved-costs "$scratch/source/head/scripts/benchgate/approved-costs.json" --base-sha "$base")
    fi
    target_confirmation "$evidence/$comparison"
    rc=0
    "$scratch/bin/benchgate" "${policy[@]}" "${confirmed_args[@]}" > "$evidence/$comparison.benchgate.log" 2>&1 || rc=$?
    printf '%s\t%s\n' "$comparison" "$rc" >> "$evidence/verdicts.tsv"
    if (( rc != 0 )); then
      grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/$comparison.benchgate.log"
    fi
  done
  rm -- "$evidence/raw/"*.txt
  rmdir "$evidence/raw"
  cat "$evidence/verdicts.tsv"
}

mode="${1:-run}"
mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
case "$mode" in
  --probe)
    mkdir "$evidence"
    LC_ALL=C lscpu | tee "$evidence/runner.txt"
    match=false
    if matches_cpu; then match=true; fi
    printf 'match=%s\n' "$match" | tee -a "$evidence/runner.txt" >> "$GITHUB_OUTPUT"
    exit 0
    ;;
  --fetch)
    matches_cpu
    git -C "$repo" fetch --no-tags origin "$base" "$head" "$tested"
    gh api "repos/light-speak/luxo/actions/artifacts/$artifact/zip" > "$evidence/official.zip"
    exit 0
    ;;
  --check-inputs) test "$#" = 2;;
  run) matches_cpu;;
  *) exit 2;;
esac
scratch=$(mktemp -d "$repo/.tmp/pr74-gateway-identity.XXXXXXXX")
trap '[[ "$scratch" == "$repo"/.tmp/pr74-gateway-identity.* && ! -L "$scratch" ]] && rm -rf -- "$scratch"' EXIT
official="$evidence/official.zip"
source_head="$tested"
test "$(git -C "$repo" rev-parse "$base^{tree}")" = "$base_tree"
test "$(git -C "$repo" rev-parse "$head^{tree}")" = "$head_tree"
git -C "$repo" merge-base --is-ancestor "$base" "$head"
if [[ "$mode" == --check-inputs ]]; then
  official="$2"
  evidence="$scratch/evidence"
  mkdir "$evidence"
  source_head="$head"
else
  test "$(git -C "$repo" rev-list --parents -n 1 "$tested")" = "$tested $base $head"
  test "$(git -C "$repo" rev-parse "$tested^{tree}")" = "$head_tree"
fi
printf 'base=%s\nbase_tree=%s\nhead=%s\ntested=%s\ntested_tree=%s\nformal_run=45408776744/1\nformal_shard=02\nformal_artifact=%s\n' \
  "$base" "$base_tree" "$head" "$tested" "$head_tree" "$artifact" > "$evidence/revisions.txt"
write_scope "$official"
for role in base head; do
  revision="$base"
  if [[ "$role" == head ]]; then revision="$source_head"; fi
  mkdir -p "$scratch/source/$role"
  paths=()
  if [[ "$mode" == --check-inputs ]]; then paths=("$package"); fi
  git -C "$repo" archive "$revision" "${paths[@]}" | tar -xf - -C "$scratch/source/$role"
  project_fixture "$role"
done
cmp "$evidence/base-retained.go.txt" "$evidence/head-retained.go.txt"
git -C "$repo" diff --exit-code "$base" "$head" -- pkg/lux/luvia/luvia.go
git -C "$repo" diff --no-ext-diff "$base" "$head" -- pkg/lux/luvia/middleware.go pkg/lux/auth/jwt.go +  > "$evidence/product-auth.diff"
if [[ "$mode" == --check-inputs ]]; then
  cat "$evidence/revisions.txt" "$evidence/cases.tsv"
  exit 0
fi

export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
mkdir -p "$TMPDIR" "$scratch/bin"
[[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
[[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
[[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
collect_host > "$evidence/environment.txt"
for role in base head; do
  mkdir "$scratch/source/$role/.tmp"
  sh "$scratch/source/$role/scripts/setup-modules.sh" "$scratch/source/$role"
  cp "$scratch/source/$role/go.work" "$evidence/$role.go.work.txt"
done
(
  cd "$scratch/source/head"
  for tool in benchrun benchplan benchgate; do go build -o "$scratch/bin/$tool" "./scripts/$tool"; done
  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  go version -m "$scratch/bin/benchstat"
) > "$evidence/tools.log" 2>&1
"$scratch/bin/benchrun" performance-profile > "$evidence/compile-profile.txt"
cmp "$evidence/formal-benchmark-compile-profile.txt" "$evidence/compile-profile.txt"
for role in base head; do
  command=compile-performance
  if [[ "$role" == base ]]; then command=compile-performance-baseline; fi
  for variant in default identity-only; do
    compile_and_inspect "$role" "$variant" "$command" > "$evidence/$role-$variant.compile.log" 2>&1
  done
done
sample_groups
test "$(wc -l < "$evidence/order.tsv")" -eq 161
for raw in "$evidence/raw/"*.txt; do grep -qxF "cpu: $cpu" "$raw"; done
analyze_profile 01 ordinary
analyze_profile 02 isolated

ordinary="$evidence/ordinary/ab.benchgate.log"
if ! grep -qx $'aa\t0' "$evidence/ordinary/verdicts.tsv" || ! grep -qx $'aa\t0' "$evidence/isolated/verdicts.tsv"; then
  printf '%s\n' 'A/A control was rejected' >&2
  exit 1
fi
if ! grep -qx 'performance gate rejected 3 regression(s)' "$ordinary"; then
  printf '%s\n' 'Ordinary A/H failed to reproduce the original three gate results' >&2
  exit 1
fi
for leaf in anonymous/before authenticated/after typed-nil/after; do
  if ! grep -qF "/GatewayIdentity/$leaf-2 sec/op" "$ordinary"; then
    printf 'Ordinary A/H failed to reproduce %s\n' "$leaf" >&2
    exit 1
  fi
done
if ! grep -qx $'ab\t0' "$evidence/isolated/verdicts.tsv"; then
  printf '%s\n' 'Isolated A/H was rejected' >&2
  exit 1
fi
