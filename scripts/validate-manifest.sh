#!/bin/bash
# validate-manifest.sh — 调用 Go 版 validate-manifest 校验 summoner.yaml
# Usage: validate-manifest.sh <path-to-summoner.yaml>
# Go 版用 yaml.v3 结构化解析 + chain→phase 引用校验; 无 Go 二进制时降级 grep fallback。

set -e

MANIFEST="${1:-summoner.yaml}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
GO_BIN="${SCRIPT_DIR}/../hooks/bin/validate-manifest"

if [ ! -f "$MANIFEST" ]; then
    echo "ERROR: $MANIFEST not found" >&2
    exit 1
fi

# 优先用 Go 二进制（结构化解析 + chain 引用校验）
if [ -x "$GO_BIN" ]; then
    "$GO_BIN" "$MANIFEST"
    exit $?
fi

# 无 Go 二进制时降级到 grep fallback（向后兼容, 无 chain 校验）
echo "  (Go validate-manifest 未构建, 降级 grep fallback — 无 chain→phase 校验)" >&2
ERRORS=0
for field in version project phases; do
    if ! grep -qE "^${field}:" "$MANIFEST"; then
        echo "ERROR: missing required field '$field'" >&2
        ERRORS=$((ERRORS + 1))
    fi
done
if [ "$ERRORS" -eq 0 ]; then
    echo "✓ $MANIFEST is valid (grep fallback, no chain check)"
    exit 0
fi
echo "✗ $MANIFEST has $ERRORS error(s)" >&2
exit 1
