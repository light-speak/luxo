#!/usr/bin/env bash
set -euo pipefail

pr73_apply_numeric_overlay() {
  local source="$1" patch="$evidence/numeric-emission.patch"
  local handler=pkg/codegen/handler.go
  local tests=pkg/codegen/numeric_range_test.go
  local benchmark=pkg/codegen/numeric_range_bench_test.go
  printf '%s\n' "$pr73_relation_wrapper" > "$evidence/numeric-wrapper-revision.txt"
  printf '%s\n' "$pr73_combined_wrapper" > "$evidence/combined-wrapper.sh"
  python3 - "$pr73_combined_dir/diagnose-pr73-combined-emission.json" "$patch" <<'PYTHON'
from hashlib import sha256
import json
from pathlib import Path
import re
import sys

bundle, output = map(Path, sys.argv[1:])
patch = json.loads(bundle.read_text())["patch"].encode()
if sha256(patch).hexdigest() != "d27ec33e5625686e42c374cf809a4db85dd355b86d25570af6d53d3fbd5748fb":
    raise SystemExit("PR 73 numeric emission patch SHA-256 mismatch")
paths = re.findall(rb"^diff --git a/(\S+) b/(\S+)$", patch, re.M)
expected = [b"pkg/codegen/handler.go", b"pkg/codegen/numeric_range_test.go"]
if paths != [(path, path) for path in expected]:
    raise SystemExit("PR 73 numeric emission patch path mismatch")
output.write_bytes(patch)
PYTHON
  sha256sum "$patch" > "$evidence/numeric-patch.sha256"
  (cd "$source" && sha256sum "$handler" "$tests" "$benchmark") > "$evidence/numeric-source-before.sha256"
  [[ "$(pr73_overlay_digest "$source/$handler")" == b820a91148a8dc0502bff4b398b66684587c276e996eb214a5d15382ff0e5e76 ]]
  [[ "$(pr73_overlay_digest "$source/$tests")" == 09e5dfbd3fb69f647280eec65933d990c67cd6262003328d294659b61a8e9e29 ]]
  (
    cd "$source"
    export GIT_CEILING_DIRECTORIES="${source%/*}"
    git apply --check --whitespace=error "$patch"
    git apply --whitespace=error "$patch"
    git apply -R --check "$patch"
  )
  (cd "$source" && sha256sum "$handler" "$tests" "$benchmark") > "$evidence/numeric-source-after.sha256"
  [[ "$(pr73_overlay_digest "$source/$handler")" == cea600bd0343f772d6561e9759638e2333bf6c9ba7530c8998105d28a8e9a059 ]]
  [[ "$(pr73_overlay_digest "$source/$tests")" == 9f341a6d8b88333756c883fb31c842e4e143045baa7d4dc963d305baea91c112 ]]
  [[ "$(pr73_overlay_digest "$source/$benchmark")" == e5068c469e3d1a2dd2472dc048ed2a6b19f2cfbecb7a351677e34229a2bda666 ]]
}

pr73_combined_main() {
  local repository pr73_combined_dir pr73_combined_wrapper TMPDIR
  local pr73_relation_wrapper=d985c37a173653d53a1f9687f33c1a0fcf166b8b
  repository="$(git rev-parse --show-toplevel)"
  pr73_combined_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
  pr73_combined_wrapper="$(
    git -C "$repository" show "$pr73_relation_wrapper:scripts/diagnose-pr73-relation-emission.sh" |
      python3 -c '
from hashlib import sha256
import sys

source = sys.stdin.buffer.read()
if sha256(source).hexdigest() != "1d24816c5714eb3e4804165f65c4c2ab91b1d7e6799064100e9adcc75c86fc2a":
    raise SystemExit("PR 73 relation wrapper SHA-256 mismatch")
script = source.decode()
marker = "  [[ \"$(pr73_overlay_digest \"$source/$benchmark\")\" == 4f58c514dc0f2ac9c310a3142e961c15cbaffa47b2dc745192ea3b30be95729e ]]\n"
replacements = (
    (marker, marker + "  pr73_apply_numeric_overlay \"$source\"\n"),
    ("  pr73_overlay_payload=\"$(cd -- \"$(dirname -- \"${BASH_SOURCE[0]}\")\" && pwd -P)/diagnose-pr73-relation-emission.json\"",
     "  pr73_overlay_payload=\"$pr73_combined_dir/diagnose-pr73-relation-emission.json\""),
    ("hosted-pr73-relation-emission", "hosted-pr73-combined-emission"),
    ("with the verified two-file overlay", "with the verified relation and numeric overlays"),
)
for old, new in replacements:
    if script.count(old) != 1:
        raise SystemExit("PR 73 relation wrapper marker changed")
    script = script.replace(old, new)
print(script, end="")
'
  )"
  TMPDIR="$(pwd -P)/.tmp"
  mkdir -p "$TMPDIR"
  test ! -L "$TMPDIR"
  export TMPDIR
  source /dev/stdin "$@" <<< "$pr73_combined_wrapper"
}

pr73_combined_main "$@"
