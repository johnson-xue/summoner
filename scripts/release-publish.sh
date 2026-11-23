#!/usr/bin/env bash
# release-publish.sh — release phase 3 (publish), the ONLY mutating step.
# Writes all THREE version manifests (root plugin.json, .claude-plugin/
# plugin.json, .claude-plugin/marketplace.json), inserts the changelog
# section, commits, tags, pushes. Rollback trap on ERR deletes the tag and
# resets the commit, preserving files. NEVER prompts: gh-release creation
# is opt-in via --gh-release (the publish checkpoint's scope decision).
# Usage: release-publish.sh --version X.Y.Z --changelog-file <path>
#                          [--dry-run] [--no-push] [--skip-changelog]
#                          [--gh-release]
# Env: PROJECT_NAME (optional) — manifest-resolved project name for
# summoner-ctx persistence; resolved by the CALLER (the skill), never
# parsed here (jq is JSON-only).
set -u

VERSION="" CHANGELOG_FILE="" DRY_RUN=false NO_PUSH=false SKIP_CHANGELOG=false GH_RELEASE=false
while [[ $# -gt 0 ]]; do
    case "$1" in
        --version) shift; VERSION="${1:-}" ;;
        --changelog-file) shift; CHANGELOG_FILE="${1:-}" ;;
        --dry-run) DRY_RUN=true ;;
        --no-push) NO_PUSH=true ;;
        --skip-changelog) SKIP_CHANGELOG=true ;;
        --gh-release) GH_RELEASE=true ;;
        *) echo "❌ unknown flag: $1" >&2; exit 1 ;;
    esac
    shift
done
[ -n "$VERSION" ] || { echo "❌ --version X.Y.Z is required" >&2; exit 1; }
echo "$VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$' || { echo "❌ Invalid semver: $VERSION" >&2; exit 1; }
git rev-parse --git-dir >/dev/null 2>&1 || { echo "❌ Not a git repository" >&2; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "❌ jq not installed" >&2; exit 1; }
if [ "$SKIP_CHANGELOG" != "true" ] && { [ -z "$CHANGELOG_FILE" ] || [ ! -f "$CHANGELOG_FILE" ]; }; then
    echo "❌ --changelog-file <path> is required unless --skip-changelog" >&2; exit 1
fi
git tag -l "v$VERSION" | grep -q . && { echo "❌ Tag v$VERSION already exists" >&2; exit 1; }

# Dry-run: preview, stop before mutation
if [ "$DRY_RUN" = "true" ]; then
    echo "🔍 DRY RUN — would update 3 manifests + CHANGELOG.md and create v$VERSION"
    exit 0
fi

TAG_CREATED=false COMMIT_CREATED=false RELEASE_STARTED=true
rollback_release() {
    local exit_code=$?
    echo "❌ Error occurred (exit code: $exit_code); rolling back..." >&2
    if [ "$TAG_CREATED" = "true" ] && git tag -l "v$VERSION" | grep -q .; then
        git tag -d "v$VERSION" 2>/dev/null && echo "   ✅ Deleted tag v$VERSION" >&2
    fi
    if [ "$COMMIT_CREATED" = "true" ] && \
       git log -1 --pretty=%B 2>/dev/null | grep -q "release: v$VERSION"; then
        git reset HEAD~1 2>/dev/null && echo "   ✅ Reset commit" >&2
    fi
    echo "   📁 Files preserved for inspection" >&2
    exit "$exit_code"
}
trap rollback_release ERR

# ALL THREE manifests (root plugin.json included)
update_version() {  # $1=file, $2=jq version path
    jq --arg v "$VERSION" "$2 = \$v" "$1" > /tmp/rel.json && \
        mv /tmp/rel.json "$1" && echo "✅ Updated $1"
}
update_version .claude-plugin/plugin.json '.version'
update_version .claude-plugin/marketplace.json '.plugins[0].version'
update_version plugin.json '.version'

# CHANGELOG.md: insert below header (create if absent)
if [ "$SKIP_CHANGELOG" != "true" ]; then
    CHANGELOG_CONTENT=$(cat "$CHANGELOG_FILE")
    if [ -f CHANGELOG.md ]; then
        NEW_CHANGELOG="$(head -n 1 CHANGELOG.md)"$'\n\n'"$CHANGELOG_CONTENT"$'\n'"$(tail -n +2 CHANGELOG.md)"
    else
        NEW_CHANGELOG="# Changelog"$'\n\n'"$CHANGELOG_CONTENT"
    fi
    printf '%s\n' "$NEW_CHANGELOG" > CHANGELOG.md
    echo "✅ Updated CHANGELOG.md"
fi

# Git operations — explicit paths only (never directory-wide adds).
git add plugin.json .claude-plugin/plugin.json .claude-plugin/marketplace.json
[ "$SKIP_CHANGELOG" != "true" ] && git add CHANGELOG.md

# Mutating calls are STANDALONE — failures left of && never fire the ERR trap.
git commit -m "release: v$VERSION

Co-Authored-By: Claude Code <noreply@anthropic.com>"
COMMIT_CREATED=true
COMMIT_HASH=$(git rev-parse --short HEAD)
echo "✅ Committed $COMMIT_HASH"

git tag -a "v$VERSION" -m "Release v$VERSION

$(head -n 10 "$CHANGELOG_FILE" 2>/dev/null || echo "Release v$VERSION")"
TAG_CREATED=true
echo "✅ Tagged v$VERSION"

# Push unless --no-push — standalone, trap fires on failure
if [ "$NO_PUSH" != "true" ]; then
    BRANCH=$(git rev-parse --abbrev-r HEAD)
    git push origin "$BRANCH"
    git push origin "v$VERSION"
    echo "✅ Pushed to remote"
fi
if [ "$GH_RELEASE" = "true" ] && command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    gh release create "v$VERSION" --title "v$VERSION" --notes-file "$CHANGELOG_FILE"
    echo "✅ Created GitHub Release"
fi

trap - ERR

# --- summoner-ctx persistence (optional; every path warns and continues)
if ! command -v summoner-ctx >/dev/null 2>&1; then
    echo "ℹ️ summoner-ctx not installed — skipping release persistence"
elif [ ! -f summoner.yaml ]; then
    echo "⚠️ manifest (summoner.yaml) missing — skipping summoner-ctx save"
elif [ -z "${PROJECT_NAME:-}" ]; then
    echo "⚠️ PROJECT_NAME unresolved — skipping summoner-ctx save"
elif summoner-ctx save --project "$PROJECT_NAME" --workflow "release-v$VERSION" --phase execution --skill release --input <(printf 'Version: %s\n' "$VERSION"); then
    echo "✅ Saved release context"
else
    echo "⚠️ summoner-ctx save failed — the release itself is unaffected"
fi

echo "✅ Release v$VERSION completed"
