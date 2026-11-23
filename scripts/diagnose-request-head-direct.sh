#!/usr/bin/env bash
set -euo pipefail

mode=${1:-sample}
case "$mode" in sample|--inventory-only|--plan-only) ;; *) exit 2 ;; esac
[[ "$#" -le 1 ]]
launch=$(pwd -P)
repo=$(git rev-parse --show-toplevel)
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
evidence="$launch/.tmp/hosted-request-head-direct"
mkdir -p "$launch/.tmp"
test ! -L "$launch/.tmp"
mkdir "$evidence"

sampler_revision=cbf4cfb383d944ceb38f700e19cf79c4c54d3c58
validator_revision=1eede1c4002de6636575415eef5fff8e3cb572f0
git -C "$repo" show "$sampler_revision:scripts/diagnose-hosted-perf.sh" > "$evidence/sampler.sh"
git -C "$repo" show "$validator_revision:scripts/diagnose-pr83-pr84.sh" > "$evidence/validator.sh"
git -C "$repo" show "$validator_revision:scripts/diagnose-pr78-pr85/request-head.patch.b64" |
  python3 -c 'import base64,sys; sys.stdout.buffer.write(base64.b64decode(sys.stdin.buffer.read()))' > "$evidence/request-head.patch"
cp "$script_dir/diagnose-request-head-direct.go.txt" "$evidence/public-fixture.go.txt"
(
  cd "$evidence"
  sha256sum -c - <<'PINS'
9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072  sampler.sh
8a998e03087873c17f7b5e0675f3880c2dc6e874e61ae579e3e371f9a5242bbc  validator.sh
6d3c631f63ad4c455e00d66c053a108183963e44eb9f7dd7b76f0ca0ea09d365  request-head.patch
PINS
  sha256sum public-fixture.go.txt > fixture.sha256
)
source "$evidence/sampler.sh"
source "$evidence/validator.sh"
base=f853ea49f14a2eb3126f54a831a39d7ea05585db
head="$base"
[[ "$(git -C "$repo" rev-parse "$base^{tree}")" == d4a8ffd9f98ecd00c7eb7c2822f07accce1dab1b ]]
printf 'base %s\nhead %s plus the pinned production-only patch\n' "$base" "$head" > "$evidence/revisions.txt"

cat > "$evidence/leaves.tsv" <<'CASES'
BenchmarkRequestHead-2	^BenchmarkRequestHead$
BenchmarkReceiveUnary4KiB-2	^BenchmarkReceiveUnary4KiB$
BenchmarkValidatedHTTPBridge//-2|BenchmarkValidatedHTTPBridge///a%20b?q=1-2|BenchmarkValidatedHTTPBridge//?-2	^BenchmarkValidatedHTTPBridge$
BenchmarkPriorKnowledgeConnectionLifecycle-2	^BenchmarkPriorKnowledgeConnectionLifecycle$
BenchmarkDiagnosticRequestHeadRegular-2	^BenchmarkDiagnosticRequestHeadRegular$
BenchmarkDiagnosticRequestHeadPublic/h2c-2|BenchmarkDiagnosticRequestHeadPublic/tls-2	^BenchmarkDiagnosticRequestHeadPublic$
CASES
python3 - "$evidence" <<'PYTHON'
import json
from pathlib import Path
import sys

evidence = Path(sys.argv[1])
package = "pkg/lux/h2transport"
scope, batch, expected = [], [], []
for number, row in enumerate((evidence / "leaves.tsv").read_text().splitlines(), 1):
    names, selector = row.split("\t")
    names = names.split("|")
    case = f"{number:02}"
    scope.append({"case": case, "package": package, "leaves": names})
    batch.append("\t".join((case, package, "default", names[0], selector)) + "\n")
    expected.extend({"Package": package, "Name": name} for name in names)
if len(scope) != 6 or len(expected) != 9 or len({row["Name"] for row in expected}) != len(expected):
    raise SystemExit("wrong RequestHead scope")
(evidence / "scope.json").write_text(json.dumps(scope, indent=2) + "\n")
(evidence / "cases.tsv").write_text("".join(batch))
(evidence / "expected.json").write_text(json.dumps({"Base": expected, "Head": expected}, indent=2) + "\n")
PYTHON
if [[ "$mode" == --plan-only ]]; then exit 0; fi

scratch=$(mktemp -d "$launch/.tmp/.hosted-request-head-direct.XXXXXX")
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
export GOWORK=off GOTOOLCHAIN=local TMPDIR="$scratch/tmp"
mkdir -p "$TMPDIR" "$scratch/bin"
probe="$(go env GOOS):$(go env GOARCH)"
[[ "$(go env GOVERSION)" == go1.27.1 && "$(go env GOHOSTOS):$(go env GOHOSTARCH)" == "$probe" ]]
[[ "$probe:$(go env GOAMD64):$(go env CGO_ENABLED)" == linux:amd64:v1:1 && -z "$(go env GOFLAGS)" ]]
collect_host > "$evidence/environment.txt"
package=pkg/lux/h2transport
packages=("$package")
benchmarks='Benchmark(RequestHead|ReceiveUnary4KiB|ValidatedHTTPBridge|PriorKnowledgeConnectionLifecycle|DiagnosticRequestHeadRegular|DiagnosticRequestHeadPublic)|benchmarkDiagnosticRequestHeadPublic'
for role in base head; do
  source_root="$scratch/source/$role"
  mkdir -p "$source_root" "$scratch/bin/$role"
  git -C "$repo" archive "$base" | tar -xf - -C "$source_root"
  (
    cd "$source_root"
    export GIT_CEILING_DIRECTORIES="$scratch/source"
    cp "$evidence/public-fixture.go.txt" "$package/request_head_direct_diagnostic_test.go"
    git apply --check --whitespace=error --include="$package/request_headers_test.go" "$evidence/request-head.patch"
    git apply --whitespace=error --include="$package/request_headers_test.go" "$evidence/request-head.patch"
    parser_sha=d2bb0b68f56b0b5a647b2b3104a4c8e193f7bda209d82d0ed8f9779620fb9e0e
    if [[ "$role" == head ]]; then
      git apply --check --whitespace=error --include="$package/request_headers.go" "$evidence/request-head.patch"
      git apply --whitespace=error --include="$package/request_headers.go" "$evidence/request-head.patch"
      git apply --reverse --check --include="$package/request_headers.go" "$evidence/request-head.patch"
      parser_sha=3956c63ae1795051b6cc54c94856ec8dfb1149c270242cdb1717e7d6fffca6ca
    fi
    printf '%s  %s\n' "$parser_sha" "$package/request_headers.go" \
      00a36c38c5c9bb0310e8cb4f84c974e08ea1fc35afe60366312b912b5fe7a3d7 "$package/request_headers_test.go" |
      sha256sum -c -
    sha256sum go.mod go.sum "$package/"*.go > "$evidence/sources-$role.sha256"
    sh scripts/setup-modules.sh "$source_root"
    if [[ "$role" == base ]]; then
      for tool in benchrun benchplan benchgate; do
        go build -o "$scratch/bin/$tool" "./scripts/$tool"
      done > "$evidence/compile-tools.log" 2>&1
    fi
  )
  "$scratch/bin/benchrun" compile "$source_root" "$scratch/bin/$role" "$package" > "$evidence/compile-$role.log" 2>&1
  record_binaries "$role"
  "$scratch/bin/benchrun" run "$source_root" "$scratch/bin/$role" "$package" -test.timeout=90s \
    -test.run='^Test(RequestHeaders|RequestHeadPseudoFieldValues|MalformedRequestHeaders|HeaderOwnershipAndLimits|HeaderTargetAndHostCases|ContinuationAndTruncationKeepHPACKState|RejectedHeadersPreserveNextStreamHPACKState|HTTPServerProtocolDispatch|CandidateTLSALPN)$' \
    -test.count=1 -test.v > "$evidence/protocol-$role.log"
done
diff -u "$evidence/sources-base.sha256" "$evidence/sources-head.sha256" > "$evidence/sources.diff" || [[ "$?" == 1 ]]
[[ "$(grep -Ec '^[-+][0-9a-f]{64}  .*/request_headers\.go$' "$evidence/sources.diff")" == 2 ]]
[[ "$(grep -Ec '^[-+][0-9a-f]{64}  ' "$evidence/sources.diff")" == 2 ]]
probe_inventory
if [[ "$mode" == --inventory-only ]]; then exit 0; fi
for role in base head; do record_layout "$role"; done
GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
go version -m "$scratch/bin/benchstat" > "$evidence/benchstat-build.txt"
sample_groups
verify_output samples
analyze_groups
grep -Fxq $'aa\t0' "$evidence/verdicts.tsv"
grep -Fxq $'ab\t0' "$evidence/verdicts.tsv"
