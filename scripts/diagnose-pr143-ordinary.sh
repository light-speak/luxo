#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C GOTOOLCHAIN=local
base=5fc47e2a77a5a964e945dc6867ddb7061ce9e9fd
head=c884024aa15c7df09de2c42f70abd616bca8ac1c
root=$(cd "$GITHUB_WORKSPACE" && pwd -P)
work="$root/.tmp"
control="$work/control"
evidence="$work/pr143-ordinary-evidence"
helper="$control/scripts/diagnose-pr143-ordinary.py"
g() { git --no-replace-objects -C "$@"; }
cpu='Intel(R) Xeon(R) 6975P-C'
symbols='BenchmarkGatewayIdentity|luvia\.(New|newGateway|Identity|gatewayIdentity)|context\.\(\*valueCtx\)\.Value'
[[ "$RUNNER_KIND" = github-hosted && "$GITHUB_REPOSITORY" = light-speak/luxo ]]
[[ "$GITHUB_EVENT_NAME" = push && "$GITHUB_REF" = refs/heads/diagnostics/pr143-identity-ordinary ]]
test "$(uname -sm)" = 'Linux x86_64'
test "$(lscpu | sed -n 's/^Model name:[[:space:]]*//p' | sed 's/[[:space:]]*$//')" = "$cpu"
test "$(go env GOVERSION GOHOSTOS GOHOSTARCH GOOS GOARCH GOAMD64 CGO_ENABLED)" = $'go1.27.1\nlinux\namd64\nlinux\namd64\nv1\n1'
test -z "$(go env GOFLAGS)"
test "$(g "$control" rev-list --parents -n 1 HEAD)" = "$GITHUB_SHA $base"
printf '%s\n' .github/workflows/diagnose-pr143-ordinary.yml scripts/diagnose-pr143-ordinary.py scripts/diagnose-pr143-ordinary.sh scripts/diagnose-pr143-ordinary_test.py > "$evidence/allowed-control-paths.txt"
g "$control" diff --name-only "$base" HEAD | cmp - "$evidence/allowed-control-paths.txt"
for arm in base head; do
 checkout="$work/$arm-git"
 rev=$base; [[ "$arm" != head ]] || rev=$head
 test "$(g "$checkout" rev-parse HEAD)" = "$rev"
 g "$checkout" diff --quiet
 test -z "$(g "$checkout" ls-files --others --exclude-standard)"
 test -z "$(g "$checkout" ls-tree -r --name-only HEAD | awk '/(^|\/)\.gitattributes$/')"
 python3 -I -B "$helper" inputs "$checkout" "$rev" "$arm" "$evidence/$arm-go-inputs.txt"
done
printf 'base=%s\nhead=%s\ncontrol=%s\n' "$base" "$head" "$GITHUB_SHA" > "$evidence/source-mode.txt"
{ uname -a; lscpu; go env -json GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED GOFLAGS; } > "$evidence/environment.txt"
scratch=$(mktemp -d "$work/pr143-ordinary-scratch.XXXXXXXX")
cleanup() {
 local status=$?
 trap - EXIT
 test "${scratch%/*}" = "$work" && test -d "$scratch" && ! test -L "$scratch" && rm -rf -- "$scratch" || status=1
 printf 'exit=%s\nowned_scratch_remaining=%s\n' "$status" "$(test -e "$scratch" && echo 1 || echo 0)" > "$evidence/cleanup.txt"
 exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
tools="$scratch/bin"
mkdir -p "$scratch/tmp" "$tools"
export TMPDIR="$scratch/tmp" GOTMPDIR="$scratch/tmp" GOMAXPROCS=2 GOWORK=off
build_inputs=(go.mod go.sum scripts/setup-modules.sh scripts/benchrun scripts/benchplan scripts/benchgate internal/benchlayout)
for arm in base head; do g "$work/$arm-git" ls-tree HEAD -- "${build_inputs[@]}" > "$evidence/$arm-tool-inputs.txt"; done
cmp "$evidence/base-tool-inputs.txt" "$evidence/head-tool-inputs.txt"
for arm in base head candidate-base candidate-head; do
 source="$scratch/$arm"
 mkdir -p "$source"
 original=${arm#candidate-}
 g "$work/$original-git" archive HEAD | tar -xf - -C "$source"
 mkdir "$source/.tmp"
 if [[ "$arm" = candidate-* ]]; then python3 -I -B "$helper" apply "$source" "$original" "$evidence/$arm-source-proof.json"; fi
 sh "$source/scripts/setup-modules.sh" "$source"
done
(cd "$scratch/head"; go build -o "$tools/" ./scripts/benchrun ./scripts/benchplan ./scripts/benchgate)
GOBIN="$tools" go install golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70
sha256sum "$tools/"{benchrun,benchplan,benchgate,benchstat} > "$evidence/tools.sha256"
go version -m "$tools/benchstat" > "$evidence/benchstat-build.txt"
"$tools/benchrun" performance-profile > "$evidence/rebuilt-profile.txt"
"$tools/benchrun" isolation-profile pkg/lux/luvia > "$evidence/identity-selectors.txt"
grep -Fxq $'luvia-gateway-identity\t^BenchmarkGatewayIdentity$' "$evidence/identity-selectors.txt"
python3 -I -B "$helper" plan "$evidence"
IFS=$'\t' read -r package profile filter < "$evidence/batch.tsv"
"$tools/benchplan" --verify-profile-batch confirmation "$evidence/expected.json" "$evidence/batch.tsv"
record_binary() {
 local path=$1 output=$2
 python3 -I -B "$helper" elf "$path" > "$output.elf.json"
 sha256sum "$path" > "$output.sha256"
 go version -m "$path" > "$output.build.txt"
 go tool buildid "$path" > "$output.buildid.txt"
 readelf -WhlS "$path" > "$output.readelf.txt"
 go tool nm -size -sort address "$path" | awk '$3 ~ /^[Tt]$/' > "$scratch/text-addresses.txt"
 grep -E "$symbols" "$scratch/text-addresses.txt" > "$output.symbols.txt"
 python3 -I -B "$helper" map "$scratch/text-addresses.txt" "$output.address-map.txt"
 go tool objdump -s "$symbols" "$path" > "$output.objdump.txt"
 python3 -I -B "$helper" symbols "$path" "$output.symbols.txt" "$output.elf.json" > "$output.symbols.json"
}
binary=pkg-lux-luvia-luvia-gateway-identity.test
for arm in base head candidate-base candidate-head; do
 source="$scratch/$arm"
 command=compile-performance
 [[ "$arm" != *base ]] || command=compile-performance-baseline
 printf '%s\t%s\tordinary\n' "$arm" "$command" >> "$evidence/link-policy.tsv"
 "$tools/benchrun" "$command" "$source" "$tools/$arm" "$package" "$profile" > "$evidence/$arm.compile.log" 2>&1
 record_binary "$tools/$arm/$binary" "$evidence/$arm"
 "$tools/benchrun" run-performance "$source" "$tools/$arm" "$package" "$profile" -test.list='.*' > "$evidence/$arm.roots.txt"
 test "$(cat "$evidence/$arm.roots.txt")" = BenchmarkGatewayIdentity
 "$tools/benchrun" run-performance "$source" "$tools/$arm" "$package" "$profile" -test.run='^$' -test.bench='^BenchmarkGatewayIdentity$' -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 > "$evidence/$arm.inventory.txt"
 python3 -I -B "$helper" inventory "$evidence/expected.json" "$evidence/$arm.inventory.txt" 1
 mkdir "$source/.tmp/identity-consumer"
 cat > "$source/.tmp/identity-consumer/main.go" <<'GO'
package main
import("context";"fmt";"os";"github.com/light-speak/luxo/pkg/lux/luvia")
func main(){
 gateway:=luvia.New()
 if gateway.Router.IdentityExtractor(context.Background())!=nil {panic("anonymous identity must be nil")}
 if len(os.Args)>1 && os.Args[1]=="--serve" {if err:=gateway.Serve("identity-layout-probe");err!=nil{panic(err)}}
 fmt.Println("ordinary gateway consumer: anonymous identity is nil")
}
GO
 (cd "$source"; GOWORK="$source/go.work" go build -trimpath -o "$tools/$arm/consumer" ./.tmp/identity-consumer)
 "$tools/$arm/consumer" > "$evidence/$arm-consumer.run.txt"
 cp "$source/.tmp/identity-consumer/main.go" "$evidence/$arm-consumer.go.txt"
 record_binary "$tools/$arm/consumer" "$evidence/$arm-consumer"
done
python3 -I -B "$helper" layout "$evidence"
sample() {
 local arm=$1 dest=$2
 "$tools/benchrun" run-performance "$scratch/$arm" "$tools/$arm" "$package" "$profile" -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=200ms -test.count=1 -test.cpu=2 >> "$dest"
}
compare() {
 "$tools/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv "base=$1" "head=$2" > "$3.csv" 2> "$3.warnings.txt"
}
comparisons=(aa:base:base bb:head:head cc:candidate-base:candidate-base dd:candidate-head:candidate-head ab:base:head ac:base:candidate-head cd:candidate-base:candidate-head)
printf 'comparison\tgroup\tpair\torder\tleft\tright\n' > "$evidence/order.tsv"
for group in 1 2; do
 for round in {1..10}; do
  order=left,right; if (( (round + group) % 2 )); then order=right,left; fi
  for (( offset=0; offset < ${#comparisons[@]}; offset++ )); do
   index=$(( (round + (group - 1) * 10 + offset) % ${#comparisons[@]} ))
   IFS=: read -r comparison left right <<< "${comparisons[index]}"
   prefix="$evidence/$comparison.$group"
   printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$comparison" "$group" "$round" "$order" "$left" "$right" >> "$evidence/order.tsv"
   if [[ "$order" = left,right ]]; then sample "$left" "$prefix.left.raw"; sample "$right" "$prefix.right.raw"; else sample "$right" "$prefix.right.raw"; sample "$left" "$prefix.left.raw"; fi
  done
 done
done
for group in 1 2; do
 for scope in "${comparisons[@]}"; do
  IFS=: read -r comparison left right <<< "$scope"
  prefix="$evidence/$comparison.$group"
  for side in left right; do
   python3 -I -B "$helper" inventory "$evidence/expected.json" "$prefix.$side.raw" 10
   "$tools/benchplan" --normalize "$prefix.$side.raw" "$prefix.$side.txt"
  done
  "$tools/benchplan" --verify "$evidence/expected.json" "$prefix.left.txt" "$prefix.right.txt"
  compare "$prefix.left.txt" "$prefix.right.txt" "$prefix"
  if [[ "$left" = "$right" ]]; then compare "$prefix.right.txt" "$prefix.left.txt" "$prefix.reverse"; fi
 done
done
gate() {
 local name=$1 status=0
 shift
 "$tools/benchgate" "$@" > "$evidence/$name.gate.txt" 2>&1 || status=$?
 printf '%s\t%d\n' "$name" "$status" >> "$evidence/gates.tsv"
}
for same in aa bb cc dd; do
 for group in 1 2; do
  for direction in '' .reverse; do name="$same.$group$direction"; gate "$name" "$evidence/$name.csv"; done
 done
done
for label in ab ac cd; do
 gate "$label-combined" --approved-costs "$scratch/head/scripts/benchgate/approved-costs.json" --base-sha "$base" "$evidence/$label.1.csv" "$evidence/$label.2.csv"
done
for arm in base head candidate-base candidate-head; do sha256sum -c "$evidence/$arm.sha256" "$evidence/$arm-consumer.sha256"; done
python3 -I -B "$helper" summarize "$evidence"
