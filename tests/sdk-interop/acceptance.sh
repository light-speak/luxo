#!/usr/bin/env bash
set -euo pipefail

# Functional acceptance only; do not interpret elapsed build time as a benchmark.
suite=${1:-all}
case "$suite" in
  --list) printf '%s\n' 'all ts dart kotlin swift'; exit 0 ;;
  all|ts|dart|kotlin|swift) ;;
  *) printf 'Usage: bash tests/sdk-interop/acceptance.sh [all|ts|dart|kotlin|swift|--list]\n' >&2; exit 2 ;;
esac
if (( $# > 1 )); then printf 'Unexpected arguments\n' >&2; exit 2; fi
core_dir=$(cd "$(dirname "$0")/../.." && pwd)
swift_dir=${LUXO_SWIFT_PATH:-"$core_dir/../luxo-swift"}
if [[ -L "$core_dir/.tmp" ]]; then printf 'Refusing a symlinked .tmp directory\n' >&2; exit 2; fi
mkdir -p "$core_dir/.tmp"
evidence_dir=$(mktemp -d "$core_dir/.tmp/sdk-acceptance.XXXXXX")
printf 'Logs: %s\n' "$evidence_dir"
git -C "$core_dir" rev-parse HEAD > "$evidence_dir/core-head.txt"
git -C "$core_dir" status --short > "$evidence_dir/core-worktree.txt"
printf 'stage\tresult\n' > "$evidence_dir/results.tsv"

run() {
  local stage=$1 directory=$2
  shift 2
  printf '\nRunning %s\n' "$stage"
  if (cd "$directory" && "$@") 2>&1 | tee "$evidence_dir/$stage.log"; then
    printf '%s\tPASS\n' "$stage" >> "$evidence_dir/results.tsv"
  else
    local result=$?
    printf '%s\tFAIL (%s)\n' "$stage" "$result" >> "$evidence_dir/results.tsv"
    return "$result"
  fi
}

if [[ "$suite" == all || "$suite" == ts ]]; then
  run ts-build "$core_dir/packages/client" pnpm build
  run ts-types "$core_dir/packages/client" pnpm typecheck
  run ts-runtime "$core_dir/packages/client" pnpm test
  run ts-plugin-types "$core_dir/packages/vite-plugin" pnpm typecheck
  run ts-generated "$core_dir/packages/vite-plugin" pnpm exec vitest run
fi
if [[ "$suite" == all || "$suite" == dart ]]; then
  run dart-analysis "$core_dir/packages/dart" dart analyze
  run dart-generated "$core_dir/packages/dart" dart test \
    test/generated_request_test.dart test/query_codegen_test.dart test/selection_build_test.dart \
    test/go_interop_test.dart --reporter expanded --concurrency=1
fi
if [[ "$suite" == all || "$suite" == kotlin ]]; then
  run kotlin-generated "$core_dir/packages/kotlin" ./gradlew :test \
    --tests com.luxo.client.GeneratedRequestTest --tests com.luxo.client.QueryCodegenTest \
    --tests com.luxo.client.SelectionPluginTest --no-daemon --warning-mode fail
  run kotlin-gradle "$core_dir/packages/kotlin" ./gradlew :gradle-plugin:test --no-daemon --warning-mode fail
  run kotlin-direct "$core_dir" go run ./tests/sdk-interop -run -- ./packages/kotlin/gradlew \
    -p tests/sdk-interop/kotlin build --project-cache-dir .tmp/gradle-cache --no-daemon --warning-mode fail
  run kotlin-gateway "$core_dir" go run ./tests/sdk-interop -run -gateway -- ./packages/kotlin/gradlew \
    -p tests/sdk-interop/kotlin build --project-cache-dir .tmp/gradle-cache --no-daemon --warning-mode fail
fi
if [[ "$suite" == all || "$suite" == swift ]]; then
  swift_dir=$(cd "$swift_dir" && pwd)
  git -C "$swift_dir" rev-parse HEAD > "$evidence_dir/swift-head.txt"
  git -C "$swift_dir" status --short > "$evidence_dir/swift-worktree.txt"
  run swift-generated "$swift_dir" env LUXO_IOS_TYPECHECK=1 swift test \
    --filter 'GeneratedSelectionBuildTests|GeneratedQueryBuildTests|SelectionXcodeInvocationTests'
  run swift-direct "$core_dir" env LUXO_SWIFT_PATH="$swift_dir" go run ./tests/sdk-interop \
    -run -- bash tests/sdk-interop/swift/run.sh
  run swift-gateway "$core_dir" env LUXO_SWIFT_PATH="$swift_dir" go run ./tests/sdk-interop \
    -run -gateway -- bash tests/sdk-interop/swift/run.sh
fi
printf '\nSelected functional suite passed. Performance/release approval is separate.\nResults: %s/results.tsv\n' "$evidence_dir"
