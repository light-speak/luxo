#!/bin/bash
# Luxo 代码质量检查 / Code quality check
# 提交前运行 / Run before commit
# Standalone checkout: run sh scripts/setup-modules.sh . once before these checks.

set -e

echo "==> gofmt -s"
sh scripts/check-go.sh format pkg/ cmd/ scripts/ internal/
echo "gofmt OK"

echo "==> go vet"
go vet ./pkg/... ./cmd/... ./cmd/luxo/... ./scripts/... ./internal/... ./pkg/lux/pg/... ./pkg/lux/nats/... ./pkg/lux/cache/redis/... ./pkg/lux/storage/s3/...
echo "go vet OK"

echo "==> gocyclo (complexity ≤ 25)"
sh scripts/check-go.sh complexity pkg/ cmd/ scripts/ internal/
echo "gocyclo OK"

echo "==> go test"
go test ./pkg/... ./cmd/... ./scripts/... ./internal/... -cover -timeout 60s
go test ./cmd/luxo/... -timeout 60s
for module in pkg/lux/pg pkg/lux/nats pkg/lux/cache/redis pkg/lux/storage/s3; do
    go test "./$module/..." -cover -timeout 180s
done
echo "tests OK"

echo ""
echo "==> 全部通过，可以提交 / All checks passed, ready to commit"
