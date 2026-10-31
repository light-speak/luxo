#!/bin/sh
# Verify the actual runtime against the shared, reproducible Node pin.
set -eu

case "$0" in
    */*) node_script_dir=${0%/*} ;;
    *) node_script_dir=. ;;
esac
node_expected=
invalid_node_pin() {
    printf 'Invalid Node version pin: %s\n' "$node_expected" >&2
    exit 1
}
{
    IFS= read -r node_expected || :
    node_extra=
    if IFS= read -r node_extra || [ -n "$node_extra" ]; then
        invalid_node_pin
    fi
} < "$node_script_dir/../.node-version"
case "$node_expected" in
    ''|*[!0-9.]*|.*|*.|*..*|*.*.*.*) invalid_node_pin ;;
    *.*.*) ;;
    *) invalid_node_pin ;;
esac
node_actual=$(node --version)
if [ "$node_actual" != "v$node_expected" ]; then
    printf 'Expected Node v%s; found %s. Update the local toolchain before release validation.\n' "$node_expected" "$node_actual" >&2
    exit 1
fi
printf 'Node %s matches the project pin.\n' "$node_actual"
