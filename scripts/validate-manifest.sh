#!/bin/bash
# validate-manifest.sh — Fail-closed launcher for the v2 Go validator.
# All arguments are forwarded, including --skills-dir and --project-name.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BIN_DIR="$SCRIPT_DIR/../hooks/bin"
SUFFIX=""
case "$(uname -s)-$(uname -m)" in
    Darwin-x86_64) SUFFIX=darwin-amd64 ;;
    Darwin-arm64) SUFFIX=darwin-arm64 ;;
    Linux-x86_64) SUFFIX=linux-amd64 ;;
esac
if [ -n "$SUFFIX" ] && [ -x "$BIN_DIR/validate-manifest-$SUFFIX" ]; then
    exec "$BIN_DIR/validate-manifest-$SUFFIX" "$@"
fi
if [ -x "$BIN_DIR/validate-manifest" ]; then
    exec "$BIN_DIR/validate-manifest" "$@"
fi
printf '%s\n' 'ERROR: v2 validate-manifest binary unavailable; reinstall the plugin or run make build in hooks/. No fallback validation is allowed.' >&2
exit 1
