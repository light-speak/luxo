#!/usr/bin/env bash
set -euo pipefail

pr73_overlay_digest() {
  sha256sum "$1" | cut -d' ' -f1
}

pr73_apply_relation_overlay() {
  local source="$1" patch="$evidence/relation-emission.patch"
  local resolver=pkg/codegen/relation_resolver.go
  local tests=pkg/codegen/relation_resolver_test.go
  local benchmark=pkg/codegen/dataloader_nullable_bench_test.go
  printf '%s\n' "$pr73_runner_revision" > "$evidence/overlay-runner-revision.txt"
  printf '%s\n' "$pr73_derived_runner" > "$evidence/overlay-runner.sh"
  python3 - "$pr73_overlay_payload" "$patch" <<'PYTHON'
from hashlib import sha256
import json
from pathlib import Path
import re
import sys

bundle, output = map(Path, sys.argv[1:])
patch = json.loads(bundle.read_text())["patch"].encode()
if sha256(patch).hexdigest() != "b2389755c84ee4dd6bff520119c586d87ff62a18284ead635ef0ad3666964742":
    raise SystemExit("PR 73 relation emission patch SHA-256 mismatch")
paths = re.findall(rb"^diff --git a/(\S+) b/(\S+)$", patch, re.M)
expected = [b"pkg/codegen/relation_resolver.go", b"pkg/codegen/relation_resolver_test.go"]
if paths != [(path, path) for path in expected]:
    raise SystemExit("PR 73 relation emission patch path mismatch")
output.write_bytes(patch)
PYTHON
  sha256sum "$patch" > "$evidence/overlay-patch.sha256"
  (
    cd "$source"
    sha256sum "$resolver" "$tests" "$benchmark"
  ) > "$evidence/overlay-source-before.sha256"
  [[ "$(pr73_overlay_digest "$source/$resolver")" == 1028d26ef2040639c10eed77d25adcddd60cddc3203388e0e9ed60ad83017c89 ]]
  [[ "$(pr73_overlay_digest "$source/$tests")" == 4807834caa7047fb36d5544e1e11225890c10dcb5301e6abda3eeae81e267f8a ]]
  (
    cd "$source"
    export GIT_CEILING_DIRECTORIES="${source%/*}"
    git apply --check --whitespace=error "$patch"
    git apply --whitespace=error "$patch"
    git apply -R --check "$patch"
  )
  (
    cd "$source"
    sha256sum "$resolver" "$tests" "$benchmark"
  ) > "$evidence/overlay-source-after.sha256"
  [[ "$(pr73_overlay_digest "$source/$resolver")" == fd549b1fec7c70c67b36fdf90cc083ca0c4c607b634911eb7b192d9a24c7358a ]]
  [[ "$(pr73_overlay_digest "$source/$tests")" == 746258db42c2093ea317e2aa5d63ce60d916c5cd8383190eafb598a412d94993 ]]
  [[ "$(pr73_overlay_digest "$source/$benchmark")" == 4f58c514dc0f2ac9c310a3142e961c15cbaffa47b2dc745192ea3b30be95729e ]]
}

pr73_relation_emission_main() {
  local repository
  local pr73_runner_revision=29a9760671c1e60fb48e266eef0c649a15fdf5a3
  local pr73_overlay_payload pr73_derived_runner
  repository="$(git rev-parse --show-toplevel)"
  pr73_overlay_payload="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)/diagnose-pr73-relation-emission.json"
  # Adapt the pinned harness at the archive hook before it hashes or builds head.
  pr73_derived_runner="$(
    git -C "$repository" show "$pr73_runner_revision:scripts/diagnose-pr73-codegen-layout.sh" |
      python3 -c '
from hashlib import sha256
import sys

source = sys.stdin.buffer.read()
if sha256(source).hexdigest() != "759c8809da2cb67cf2fac4493523d7e995c0acb97a504ca30dcd12599f59b9c8":
    raise SystemExit("PR 73 base harness SHA-256 mismatch")
script = source.decode()
replacements = (
    ("evidence=\"$launch/.tmp/hosted-pr73-codegen-layout\"",
     "evidence=\"$launch/.tmp/hosted-pr73-relation-emission\""),
    ("  git -C \"$repo\" archive \"$commit\" | tar -xf - -C \"$source\"\n",
     "  git -C \"$repo\" archive \"$commit\" | tar -xf - -C \"$source\"\n"
     "  if [[ \"$revision\" == head ]]; then pr73_apply_relation_overlay \"$source\"; fi\n"),
    ("A/A uses the base binary on both sides. A/B uses the base and head binaries.",
     "A/A uses the base binary on both sides. A/B uses base and head with the verified two-file overlay."),
)
for old, new in replacements:
    if script.count(old) != 1:
        raise SystemExit("PR 73 base harness archive, evidence, or protocol marker changed")
    script = script.replace(old, new)
print(script, end="")
'
  )"
  local TMPDIR
  TMPDIR="$(pwd -P)/.tmp"
  mkdir -p "$TMPDIR"
  test ! -L "$TMPDIR"
  export TMPDIR
  source /dev/stdin <<< "$pr73_derived_runner"
  pr73_main "$@"
}

pr73_relation_emission_main "$@"
