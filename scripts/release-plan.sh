#!/usr/bin/env bash
# release-plan.sh — release phase 1 (version planning), read-only.
# Proposes the next version from the three version manifests; validates
# semver and tag freshness. NEVER mutates anything, NEVER prompts: the
# increment choice is the planning checkpoint's AskUserQuestion decision
# (checkpoint-protocol.md); this script only computes and checks.
# Usage: release-plan.sh [--major|--minor|--patch|--version X.Y.Z]
# Exit: 0 ok (prints PROPOSED line) | 1 invalid input/state.
set -u

INCREMENT_MAJOR=false INCREMENT_MINOR=false INCREMENT_PATCH=false VERSION_ARG=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --major) INCREMENT_MAJOR=true ;;
        --minor) INCREMENT_MINOR=true ;;
        --patch) INCREMENT_PATCH=true ;;
        --version) shift; VERSION_ARG="${1:-}" ;;
        *) echo "❌ unknown flag: $1 (use --major/--minor/--patch/--version X.Y.Z)" >&2; exit 1 ;;
    esac
    shift
done

# --- pre-flight (report-only; the continue/stop call is the checkpoint's)
git rev-parse --git-dir >/dev/null 2>&1 || { echo "❌ Not a git repository"; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "❌ jq not installed (brew install jq)"; exit 1; }
if [ -n "$(git status --porcelain)" ]; then
    echo "⚠️  Uncommitted changes present (surfaced to the planning checkpoint)."
fi

# --- read ALL THREE manifests (a root plugin.json drift once shipped;
# scripts/validate-repo.sh enforces this at CI time)
CURRENT_VERSION=$(jq -r '.version' .claude-plugin/plugin.json) || exit 1
MARKETPLACE_VERSION=$(jq -r '.plugins[0].version' .claude-plugin/marketplace.json) || exit 1
ROOT_VERSION=$(jq -r '.version' plugin.json) || exit 1
if [ "$CURRENT_VERSION" != "$MARKETPLACE_VERSION" ] || [ "$CURRENT_VERSION" != "$ROOT_VERSION" ]; then
    echo "⚠️  Version mismatch ($CURRENT_VERSION / $MARKETPLACE_VERSION / $ROOT_VERSION) — using .claude-plugin/plugin.json as source of truth"
fi

IFS='.' read -r MAJOR MINOR PATCH <<< "$CURRENT_VERSION"

if [ -n "$VERSION_ARG" ]; then
    NEW_VERSION="$VERSION_ARG"; VERSION_TYPE="custom"
elif [ "$INCREMENT_MAJOR" = "true" ]; then
    NEW_VERSION="$((MAJOR+1)).0.0"; VERSION_TYPE="major"
elif [ "$INCREMENT_MINOR" = "true" ]; then
    NEW_VERSION="$MAJOR.$((MINOR+1)).0"; VERSION_TYPE="minor"
else
    NEW_VERSION="$MAJOR.$MINOR.$((PATCH+1))"; VERSION_TYPE="patch"
fi

echo "$NEW_VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$' || { echo "❌ Invalid semver format: $NEW_VERSION"; exit 1; }
git tag -l "v$NEW_VERSION" | grep -q . && { echo "❌ Tag v$NEW_VERSION already exists"; exit 1; }

echo "✅ Version: $CURRENT_VERSION → $NEW_VERSION ($VERSION_TYPE)"
echo "PROPOSED v$NEW_VERSION ($VERSION_TYPE)"
