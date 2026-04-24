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

echo "==> gocyclo (complexity ≤ 25)"
if command -v gocyclo &> /dev/null; then
    COMPLEX=$(gocyclo -over 25 pkg/ 2>/dev/null)
    if [ -n "$COMPLEX" ]; then
        echo "❌ 圈复杂度超过 15 / Cyclomatic complexity > 25:"
        echo "$COMPLEX"
        exit 1
    fi
    echo "✅ gocyclo OK"
else
    echo "⚠️  gocyclo 未安装，跳过 / gocyclo not installed, skipping"
    echo "   安装 / Install: go install github.com/fzipp/gocyclo/cmd/gocyclo@latest"
fi

echo "==> go test"
go test ./pkg/... -cover -timeout 60s
echo "✅ tests OK"

echo ""
echo "==> 全部通过，可以提交 / All checks passed, ready to commit"
