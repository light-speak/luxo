#!/bin/bash
# Luxo 代码质量检查 / Code quality check
# 提交前运行 / Run before commit

set -e

echo "==> gofmt -s"
UNFMT=$(gofmt -s -l pkg/ cmd/ 2>/dev/null)
if [ -n "$UNFMT" ]; then
    echo "❌ 以下文件未格式化 / Unformatted files:"
    echo "$UNFMT"
    echo "运行 / Run: gofmt -s -w pkg/ cmd/"
    exit 1
fi
echo "✅ gofmt OK"

echo "==> go vet"
go vet ./pkg/... ./cmd/...
echo "✅ go vet OK"

echo "==> go test"
go test ./pkg/... -cover -timeout 60s
echo "✅ tests OK"

echo "==> 全部通过 / All checks passed"
