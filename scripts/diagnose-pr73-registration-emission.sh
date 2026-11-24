#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
source "$script_dir/diagnose-hosted-perf.sh"
mode="${1:-run}"
[[ $# -le 1 && ( "$mode" == run || "$mode" == --plan-only ) ]]
base=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
deploy=d8b686b516ed5ab89e185093ca836756901fb56c
deploy_tree=29b1694bd33a63cc70e13df2e9bce347782046bc
emission_tree=944c2384588c7a0f85aeb98a300e73d2bb722147
package=pkg/codegen
filter='^BenchmarkAPIParameterMetadata$/^(Int|State)$|^BenchmarkRegistrationIdentifierWidth$/^(32|128|512)$'

mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/pr73-registration-emission.XXXXXXXX")
evidence="$repo/.tmp/hosted-pr73-registration-emission"
if [[ "$mode" == --plan-only ]]; then evidence="$scratch/evidence"; fi
clean_emission_probe() {
  local status=$?
  trap - EXIT
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/pr73-registration-emission.* ]] || exit 99
  rm -rf -- "$scratch"
  exit "$status"
}
trap clean_emission_probe EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$evidence"

python3 - "$repo" "$evidence" <<'PYTHON'
from pathlib import Path
import base64
from hashlib import sha256
import json
import sys

repo, dest = map(Path, sys.argv[1:])
patch = base64.b64decode((repo / ".github/qualification/pr73-registration-emission.patch.b64").read_bytes())
if sha256(patch).hexdigest() != "442555cab7776d39a2294b7e482e4285461dde707dc8b92c5f1a70cea75c42dc":
    raise SystemExit("emission patch differs from the locally verified candidate")
(dest / "emission.patch").write_bytes(patch)
names = ["BenchmarkAPIParameterMetadata/" + kind for kind in ("Int", "State")]
names += ["BenchmarkRegistrationIdentifierWidth/" + str(width) for width in (32, 128, 512)]
expected = [{"Package": "pkg/codegen", "Name": name + "-2"} for name in names]
(dest / "expected.json").write_text(json.dumps({"Base": expected, "Head": expected}, indent=2) + "\n")
PYTHON

store="$scratch/objects.git"
git init --bare --quiet "$store"
git -C "$repo" rev-parse --path-format=absolute --git-path objects > "$store/objects/info/alternates"
a=$(git --git-dir="$store" rev-parse "$base^{tree}")
test "$a" = ba252c7cace8908cdfcd7c203bc9dc11bb2ba746
test "$(git --git-dir="$store" merge-tree --write-tree "$base" "$deploy")" = "$deploy_tree"
git --git-dir="$store" read-tree "$deploy_tree"
git --git-dir="$store" apply --cached --check --whitespace=error "$evidence/emission.patch"
git --git-dir="$store" apply --cached --whitespace=error "$evidence/emission.patch"
test "$(git --git-dir="$store" write-tree)" = "$emission_tree"
git --git-dir="$store" apply --cached --reverse --whitespace=error "$evidence/emission.patch"
test "$(git --git-dir="$store" write-tree)" = "$deploy_tree"
printf 'role\ttree\nbase\t%s\ndeploy\t%s\nhead\t%s\n' "$a" "$deploy_tree" "$emission_tree" > "$evidence/roles.tsv"

if [[ "$mode" == --plan-only ]]; then
  cat "$evidence/roles.tsv" "$evidence/expected.json"
  exit 0
fi

export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
mkdir -p "$TMPDIR" "$scratch/bin" "$evidence/fixtures"
[[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]]
[[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
[[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
[[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
collect_host > "$evidence/environment.txt"

cat > "$evidence/fixtures/registration_identifier_width_test.go" <<'GO'
package codegen

import (
	"strconv"
	"strings"
	"testing"
)

func BenchmarkRegistrationIdentifierWidth(b *testing.B) {
	for _, width := range []int{32, 128, 512} {
		b.Run(strconv.Itoa(width), func(b *testing.B) {
			name := "service:echo" + strings.Repeat("x", width)
			field := "value" + strings.Repeat("y", width)
			model := "Model" + strings.Repeat("Z", width)
			generator := mustNewGenerator(b, GeneratorConfig{IDs: StableIDs{
				APIs: map[string]int{name: 1},
				APIParams: map[string]map[string]int{name: {field: 2, "id": 1}},
			}})
			generator.requestParams = requestParamDeclarations(map[string]map[string]string{
				name: {field: "[" + model + "]?", "id": "Int"},
			})
			var expected strings.Builder
			generator.writeAPIRegistration(&expected, name)
			if !strings.Contains(expected.String(), "TypeName: "+strconv.Quote(model)) ||
				!strings.Contains(expected.String(), "Register("+strconv.Quote(name)) {
				b.Fatal("incorrect registration metadata")
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				var output strings.Builder
				generator.writeAPIRegistration(&output, name)
			}
		})
	}
}
GO
gofmt -w "$evidence/fixtures/registration_identifier_width_test.go"
while IFS=$'\t' read -r role tree; do
  source="$scratch/source/$role"
  mkdir -p "$source/.tmp"
  git --git-dir="$store" archive "$tree" | tar -xf - -C "$source"
  cp "$evidence/fixtures/registration_identifier_width_test.go" "$source/pkg/codegen/"
  sh "$source/scripts/setup-modules.sh" "$source"
done < <(tail -n +2 "$evidence/roles.tsv")

(
  cd "$repo"
  go build -o "$scratch/bin/benchrun" ./scripts/benchrun
  cd "$scratch/source/base"
  for tool in benchplan benchgate; do go build -o "$scratch/bin/$tool" "./scripts/$tool"; done
  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  go version -m "$scratch/bin/benchstat"
) > "$evidence/tools.log" 2>&1

for role in deploy head; do
  receipt="$scratch/pair-$role"
  result=0
  "$scratch/bin/benchrun" compile-pair "$scratch/source/base" "$scratch/source/$role" "$receipt" "$package" \
    > "$evidence/$role.compile.log" 2>&1 || result=$?
  if [[ -f "$receipt/fixtures.json" ]]; then cp "$receipt/fixtures.json" "$evidence/$role.fixtures.json"; fi
  if (( result != 0 )); then cat "$evidence/$role.compile.log" >&2; exit "$result"; fi
  ln -s "$receipt/head" "$scratch/bin/$role"
done
cmp "$scratch/pair-deploy/base/pkg-codegen.test" "$scratch/pair-head/base/pkg-codegen.test"
ln -s "$scratch/pair-head/base" "$scratch/bin/base"
for role in base deploy head; do
  record_binaries "$role"
  "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$role" "$package" \
    -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
    -test.timeout=90s > "$evidence/$role.inventory.txt"
  test "$(grep -c '^Benchmark' "$evidence/$role.inventory.txt")" = 5
done

mkdir "$evidence/raw"
printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
comparisons=(aa ad ap)
for group in primary confirmation; do
  offset=0
  if [[ "$group" == confirmation ]]; then offset=1; fi
  for number in {1..10}; do
    printf -v sample '%02d' "$number"
    if (((number + offset) % 2 == 1)); then first=left; else first=right; fi
    for index in 0 1 2; do
      comparison="${comparisons[(index + number + offset) % 3]}"
      case "$comparison" in
        aa) other=base;;
        ad) other=deploy;;
        ap) other=head;;
      esac
      run_pair "$group" "$sample" "$comparison" "$other" "$first" 01 "$package" default "$filter"
    done
  done
done
test "$(find "$evidence/raw" -name '*.txt' | wc -l | tr -d ' ')" = 120
grep -h '^cpu: ' "$evidence"/*.inventory.txt "$evidence"/raw/*.txt | sort -u > "$evidence/cpu-consistency.txt"
test "$(wc -l < "$evidence/cpu-consistency.txt" | tr -d ' ')" = 1
printf 'comparison\tgate_exit\n' > "$evidence/verdicts.tsv"
failed=0
for comparison in "${comparisons[@]}"; do
  normalize_group "$comparison" primary
  normalize_group "$comparison" confirmation
  result=0
  "$scratch/bin/benchgate" "$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv" \
    > "$evidence/$comparison.benchgate.log" 2>&1 || result=$?
  printf '%s\t%s\n' "$comparison" "$result" >> "$evidence/verdicts.tsv"
  if (( result != 0 )); then
    grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/$comparison.benchgate.log" || exit "$result"
    failed=1
  fi
done
cat "$evidence/verdicts.tsv"
exit "$failed"
