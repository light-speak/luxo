#!/bin/sh
# Shared formatting and complexity checks for local hooks, lint and CI.
set -eu

require_clean_output() {
    if check_output=$("$@" 2>&1); then
        check_status=0
    else
        check_status=$?
    fi
    if [ "$check_status" -eq 0 ] && [ -z "$check_output" ]; then
        return 0
    fi
    printf 'ERROR: %s check failed (exit %s).\n' "$1" "$check_status" >&2
    if [ -n "$check_output" ]; then
        printf '%s\n' "$check_output" >&2
    fi
    return 1
}

if [ "$#" -lt 2 ]; then
    printf 'Usage: sh scripts/check-go.sh {format|complexity} path...\n' >&2
    exit 2
fi
check_mode=$1
shift

case "$check_mode" in
    format)
        if ! require_clean_output gofmt -s -l "$@"; then
            printf 'Use the project Go toolchain; fix syntax errors and run gofmt -s -w on the reported files.\n' >&2
            exit 1
        fi
        ;;
    complexity)
        if ! require_clean_output gocyclo -over 25 "$@"; then
            printf 'Complexity limit: 25. A missing tool or a parse error is not a passing check.\n' >&2
            printf 'If needed, rebuild with the project Go toolchain: go install github.com/fzipp/gocyclo/cmd/gocyclo@v0.6.0\n' >&2
            exit 1
        fi
        ;;
    *)
        printf 'Unknown Go check: %s\n' "$check_mode" >&2
        exit 2
        ;;
esac
