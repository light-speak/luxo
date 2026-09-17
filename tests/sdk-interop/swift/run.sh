#!/usr/bin/env bash
set -euo pipefail

: "${LUXO_INTEROP_SCRATCH:?Run this consumer through go run ./tests/sdk-interop -run}"
export LUXO_SWIFT_PATH="$(cd "${LUXO_SWIFT_PATH:-../luxo-swift}" && pwd)"

swift run --package-path tests/sdk-interop/swift \
  --scratch-path "${LUXO_INTEROP_SCRATCH}/swift-bootstrap" \
  -Xswiftc -warnings-as-errors -Xswiftc -strict-concurrency=complete LuxoInteropGenerator
