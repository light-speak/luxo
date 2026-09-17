#!/bin/bash
# Luxo 代码质量检查 / Code quality check
# 提交前运行 / Run before commit

set -e

echo "==> gofmt -s"
sh scripts/check-go.sh format pkg/ cmd/ scripts/
echo "gofmt OK"

echo "==> go vet"
go vet ./pkg/... ./cmd/... ./scripts/...
echo "go vet OK"

echo "==> gocyclo (complexity ≤ 25)"
sh scripts/check-go.sh complexity pkg/ cmd/ scripts/
echo "gocyclo OK"

echo "==> go test"
go test ./pkg/... ./scripts/... -cover -timeout 60s
go test ./cmd/... -timeout 60s
echo "tests OK"

echo ""
echo "==> 全部通过，可以提交 / All checks passed, ready to commit"
