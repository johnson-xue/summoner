#!/usr/bin/env bash
# release-changelog.sh — release phase 2 (changelog generation), read-only.
# Collects commits since the last tag, classifies them, and prints the
# changelog section for the new version to stdout. NEVER writes files and
# NEVER mutates state: publish (release-publish.sh) owns the CHANGELOG.md
# insert. bash-3.2 safe (indexed arrays only).
# Usage: release-changelog.sh --version X.Y.Z
set -u

VERSION=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --version) shift; VERSION="${1:-}" ;;
        *) echo "❌ unknown flag: $1 (use --version X.Y.Z)" >&2; exit 1 ;;
    esac
    shift
done
[ -n "$VERSION" ] || { echo "❌ --version X.Y.Z is required" >&2; exit 1; }
echo "$VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$' || { echo "❌ Invalid semver: $VERSION" >&2; exit 1; }

git rev-parse --git-dir >/dev/null 2>&1 || { echo "❌ Not a git repository" >&2; exit 1; }

# Range: last tag..HEAD (else first commit..HEAD)
LAST_TAG=$(git describe --tags --abbrev=0 2>/dev/null)
COMMIT_RANGE="${LAST_TAG:-$(git rev-list --max-parents=0 HEAD)}..HEAD"
COMMITS=$(git log "$COMMIT_RANGE" --no-merges --pretty=format:"%s|%h")
COMMIT_COUNT=$(echo "$COMMITS" | wc -l | tr -d ' ')

# Classify by leading type or emoji; non-empty sections only.
section_for() {
    case "$1" in
        feat) echo "✨ Features" ;; fix) echo "🐛 Bug Fixes" ;;
        security) echo "🔒 Security" ;; perf) echo "⚡ Performance" ;;
        refactor) echo "♻️ Refactoring" ;; test) echo "✅ Tests" ;;
        docs) echo "📝 Documentation" ;; chore) echo "🔧 Chores" ;;
        *) echo "Other" ;;
    esac
}
declare -a SECTIONS=() BULLETS=()
add_bullet() {  # $1=title $2=bullet
    local i
    for i in "${!SECTIONS[@]}"; do
        [ "${SECTIONS[$i]}" = "$1" ] && { BULLETS[i]+=$'\n'"$2"; return; }
    done
    SECTIONS+=("$1"); BULLETS+=("$2")
}
while IFS='|' read -r message hash; do
    [ -z "$message" ] && continue
    case "$message" in
        "🔒"*) type_=security ;; "⚡"*) type_=perf ;;
        "♻️"*) type_="refactor" ;; "✅"*) type_="test" ;;
        *) type_="${message%%[:,(]*}" ;;
    esac
    add_bullet "$(section_for "$type_")" "- ${message} (${hash})"
done <<< "$COMMITS"

CURRENT_DATE=$(date +%Y-%m-%d)
CHANGELOG_CONTENT="## [$VERSION] - $CURRENT_DATE"
# bash-3.2 + set -u: expanding an empty array's indices errors — guard first.
if [ "${#SECTIONS[@]}" -gt 0 ]; then
    for i in "${!SECTIONS[@]}"; do
        CHANGELOG_CONTENT+=$'\n\n### '"${SECTIONS[$i]}"$'\n'"${BULLETS[$i]}"
    done
fi

echo "commits: $COMMIT_COUNT (range $COMMIT_RANGE)"
if [ "$COMMIT_COUNT" -eq 0 ] || [ -z "$COMMITS" ]; then
    echo "⚠️  No commits in range — changelog section will be empty"
fi
echo "---8<---"
echo "$CHANGELOG_CONTENT"
