#!/usr/bin/env bash
set -euo pipefail

# Read git diff --raw --no-renames --no-abbrev -z for the tested merge.
full_scope() {
  printf '%s\n' full
  exit 0
}

header_pattern='^:([0-9]{6}) ([0-9]{6}) [0-9a-f]{40} [0-9a-f]{40} ([ADM])$'
tool_source='^scripts/bench(gate|plan|run|scope)/[A-Za-z0-9_-]+\.go$'
changed=false
header=
while IFS= read -r -d '' header; do
  [[ "$header" =~ $header_pattern ]] || full_scope
  old_mode="${BASH_REMATCH[1]}"
  new_mode="${BASH_REMATCH[2]}"
  status="${BASH_REMATCH[3]}"
  case "$status:$old_mode:$new_mode" in
    A:000000:100644|D:100644:000000|M:100644:100644) ;;
    *) full_scope ;;
  esac

  IFS= read -r -d '' changed_path || full_scope
  if [[ "$changed_path" != scripts/performance_test.go && ! "$changed_path" =~ $tool_source ]]; then
    full_scope
  fi
  changed=true
done

[[ "$changed" == true && -z "$header" ]] || full_scope
printf '%s\n' benchmark-only
