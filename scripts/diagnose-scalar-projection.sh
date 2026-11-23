#!/usr/bin/env bash
set -euo pipefail

tool_revision=f44ab00dea4fb2a5af233cdf8f9f07952b69c345
base_revision=65f1892e175487503aa36caa86d1eb8a0805f073
assets="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/diagnose-scalar-projection" && pwd -P)"
repo="$(git rev-parse --show-toplevel)"
launch="$(pwd -P)"
inventory_only=false
if [[ $# -eq 1 && "$1" == --inventory-only ]]; then
  inventory_only=true
elif [[ $# -ne 0 ]]; then
  printf 'usage: %s [--inventory-only]\n' "$0" >&2
  exit 2
fi

export GOTOOLCHAIN=local GOWORK=off
unset GOFLAGS
if [[ "$(go env GOVERSION)" != go1.27.1 ]] ||
   [[ "$(go env GOHOSTOS)/$(go env GOHOSTARCH)" != "$(go env GOOS)/$(go env GOARCH)" ]]; then
  printf 'Use Go 1.27.1 and the native GOOS/GOARCH.\n' >&2
  exit 1
fi
if ! "$inventory_only" && [[ "$(go env GOOS)/$(go env GOARCH)" != linux/amd64 ]]; then
  printf 'Hosted measurements require native Linux amd64.\n' >&2
  exit 1
fi

mkdir -p "$launch/.tmp"
test ! -L "$launch/.tmp"
evidence="$launch/.tmp/hosted-scalar-projection-evidence"
mkdir "$evidence"
work="$(mktemp -d "$launch/.tmp/.hosted-scalar-projection.XXXXXXXX")"
trap 'rm -rf -- "$work"' EXIT
export GITHUB_WORKSPACE="$work"
git -C "$repo" archive "$tool_revision" | tar -x -C "$work"
mkdir -p "$work/.tmp/"{base,head,test-tmp}
export TMPDIR="$work/.tmp/test-tmp"
for side in base head; do
  git -C "$repo" archive "$base_revision" | tar -x -C "$work/.tmp/$side"
done

{
  printf 'tools %s\nbase %s\nbase tree %s\n' "$tool_revision" "$base_revision" "$(git -C "$repo" rev-parse "$base_revision^{tree}")"
  go version
  go env GOOS GOARCH GOAMD64 CGO_ENABLED GOFLAGS
  uname -a
  if command -v lscpu >/dev/null; then lscpu; fi
} > "$evidence/revisions.txt"
python3 "$assets/prepare.py" "$assets" "$work" "$evidence"
cd "$work"

for step in install compile sample; do bash -n ".tmp/replay-$step.sh"; done
# The production compile step needs only one row for each package.
printf 'pkg/codegen\t^$\n' > .tmp/benchmark-batch.tsv
bash .tmp/replay-compile.sh
cp "$assets/benchmark-batch.tsv" .tmp/benchmark-batch.tsv
for side in base head; do
  binary=".tmp/bin/$side/pkg-codegen.test"
  {
    shasum -a 256 "$binary"
    go version -m "$binary"
    go tool buildid "$binary"
  } > "$evidence/binary-$side.txt"
  while IFS=$'\t' read -r package filter; do
    .tmp/bin/benchrun run ".tmp/$side" ".tmp/bin/$side" "$package" \
      -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x \
      -test.count=1 -test.cpu=2
  done < .tmp/benchmark-batch.tsv > "$evidence/inventory-$side.raw.txt"
  go run ./scripts/benchplan --normalize "$evidence/inventory-$side.raw.txt" \
    "$evidence/inventory-$side.txt"
  windows=0
  if [[ "$side" == head ]]; then windows=1; fi
  .tmp/bin/benchrun run ".tmp/$side" ".tmp/bin/$side" pkg/codegen \
    -test.run='^$' -test.bench='^BenchmarkAuditScalarPaginationPlan$/^destructured-query$' \
    -test.benchtime=1x -test.count=1 -test.cpu=2 -test.v > "$evidence/pagination-plan-$side.txt"
  grep -q "pushed windows=$windows$" "$evidence/pagination-plan-$side.txt"
done
python3 "$assets/partition.py" inventory "$evidence"
if "$inventory_only"; then exit 0; fi

GOBIN="$work/.tmp/bin" bash .tmp/replay-install.sh
for group in primary confirmation; do
  # Preserve each arm even if a sample fails before normalization.
  if bash .tmp/replay-sample.sh "$group"; then
    cp .tmp/benchmark-*"$group"-*.txt "$evidence/"
  else
    cp .tmp/benchmark-raw-"$group"-*.txt "$evidence/"
    exit 1
  fi
  go run ./scripts/benchplan --verify "$evidence/benchmark-expected.json" \
    "$evidence/benchmark-$group-base.txt" "$evidence/benchmark-$group-head.txt"
  python3 "$assets/partition.py" sample "$evidence" "$group"
  for category in equivalent projection-repair pagination-repair; do
    stem="$evidence/$category-$group"
    go run ./scripts/benchplan --verify "$evidence/expected-$category.json" "$stem-base.txt" "$stem-head.txt"
    .tmp/bin/benchstat -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
      "base=$stem-base.txt" "head=$stem-head.txt" > "$stem.csv" 2> "$stem-warnings.txt"
  done
done
go run ./scripts/benchgate --confirmation-targets "$evidence/equivalent-primary.csv" \
  > "$evidence/equivalent-confirmation-targets.tsv"
go run ./scripts/benchgate "$evidence/equivalent-primary.csv" "$evidence/equivalent-confirmation.csv" \
  2>&1 | tee "$evidence/equivalent-benchgate.log"
