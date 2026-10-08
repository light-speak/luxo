#!/bin/sh
# Configure only an explicit checkout; never inherit a parent workspace.
set -eu
cd "${1:?usage: setup-modules.sh checkout}"
export GOWORK="$PWD/go.work"
if [ -e "$GOWORK" ]; then
    printf 'Refusing to overwrite %s\n' "$GOWORK" >&2
    exit 1
fi
go work init .
version="v$(tr -d '\r\n' < VERSION)"
go work edit "-replace=github.com/light-speak/luxo@${version}=."
for module in cmd/luxo pkg/lux/pg pkg/lux/storage/s3 pkg/lux/cache/redis pkg/lux/nats; do
    if [ -f "$module/go.mod" ]; then
        go work use "./$module"
        go work edit "-replace=github.com/light-speak/luxo/${module}@${version}=./${module}"
    fi
done
