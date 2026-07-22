#!/bin/bash
set -e

# Summoner Release Verification Tool
# Verifies that a release was correctly published

VERSION=${1:-$(jq -r '.version' .claude-plugin/plugin.json)}

echo "🔍 Summoner Release Verification"
echo "=================================="
echo "Verifying version: v$VERSION"
echo ""

PASSED=0
FAILED=0
WARNINGS=0

check_pass() {
    echo "✅ $1"
    PASSED=$((PASSED + 1))
}

check_fail() {
    echo "❌ $1"
    FAILED=$((FAILED + 1))
}

check_warn() {
    echo "⚠️  $1"
    WARNINGS=$((WARNINGS + 1))
}

# 1. Version Consistency
echo "📋 1. Version Consistency"
echo "-------------------------"

PLUGIN_VERSION=$(jq -r '.version' .claude-plugin/plugin.json)
MARKETPLACE_VERSION=$(jq -r '.plugins[0].version' .claude-plugin/marketplace.json)

if [ "$PLUGIN_VERSION" = "$VERSION" ]; then
    check_pass "plugin.json version matches: $PLUGIN_VERSION"
else
    check_fail "plugin.json version mismatch: expected $VERSION, got $PLUGIN_VERSION"
fi

if [ "$MARKETPLACE_VERSION" = "$VERSION" ]; then
    check_pass "marketplace.json version matches: $MARKETPLACE_VERSION"
else
    check_fail "marketplace.json version mismatch: expected $VERSION, got $MARKETPLACE_VERSION"
fi

if [ "$PLUGIN_VERSION" = "$MARKETPLACE_VERSION" ]; then
    check_pass "Version files are synchronized"
else
    check_fail "Version drift detected: plugin=$PLUGIN_VERSION, marketplace=$MARKETPLACE_VERSION"
fi

echo ""

# 2. Git Tag Verification
echo "📋 2. Git Tag Verification"
echo "--------------------------"

if git tag -l "v$VERSION" | grep -q .; then
    check_pass "Git tag v$VERSION exists"

    # Check if tag is annotated
    if git cat-file -t "v$VERSION" | grep -q "tag"; then
        check_pass "Tag is annotated (recommended)"
    else
        check_warn "Tag is lightweight (annotated preferred)"
    fi

    # Check if tag is pushed
    if git ls-remote --tags origin | grep -q "refs/tags/v$VERSION"; then
        check_pass "Tag v$VERSION pushed to remote"
    else
        check_fail "Tag v$VERSION NOT pushed to remote"
    fi
else
    check_fail "Git tag v$VERSION does not exist"
fi

echo ""

# 3. Commit Verification
echo "📋 3. Commit Verification"
echo "-------------------------"

RELEASE_COMMIT=$(git log --grep="release: v$VERSION" --oneline -1 --pretty=format:"%h %s" || echo "")
if [ -n "$RELEASE_COMMIT" ]; then
    check_pass "Release commit found: $RELEASE_COMMIT"

    # Check Co-Authored-By
    if git log -1 --grep="release: v$VERSION" --pretty=format:"%B" | grep -q "Co-Authored-By:"; then
        check_pass "Commit includes Co-Authored-By trailer"
    else
        check_warn "Commit missing Co-Authored-By trailer"
    fi
else
    check_fail "Release commit for v$VERSION not found"
fi

echo ""

# 4. CHANGELOG Verification
echo "📋 4. CHANGELOG Verification"
echo "----------------------------"

if [ -f CHANGELOG.md ]; then
    check_pass "CHANGELOG.md exists"

    if grep -q "## \[$VERSION\]" CHANGELOG.md; then
        check_pass "CHANGELOG contains v$VERSION entry"

        # Check date format
        if grep "## \[$VERSION\]" CHANGELOG.md | grep -qE "[0-9]{4}-[0-9]{2}-[0-9]{2}"; then
            check_pass "CHANGELOG entry has date"
        else
            check_warn "CHANGELOG entry missing date"
        fi
    else
        check_fail "CHANGELOG missing v$VERSION entry"
    fi
else
    check_fail "CHANGELOG.md does not exist"
fi

echo ""

# 5. Remote Synchronization
echo "📋 5. Remote Synchronization"
echo "----------------------------"

CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
LOCAL_COMMIT=$(git rev-parse HEAD)
REMOTE_COMMIT=$(git rev-parse origin/$CURRENT_BRANCH 2>/dev/null || echo "")

if [ "$LOCAL_COMMIT" = "$REMOTE_COMMIT" ]; then
    check_pass "Local and remote are synchronized"
else
    check_fail "Local branch ahead of remote (not pushed?)"
fi

# Check if release commit is pushed
if [ -n "$RELEASE_COMMIT" ]; then
    RELEASE_HASH=$(echo "$RELEASE_COMMIT" | cut -d' ' -f1)
    if git branch -r --contains "$RELEASE_HASH" | grep -q "origin/$CURRENT_BRANCH"; then
        check_pass "Release commit pushed to remote"
    else
        check_fail "Release commit NOT pushed to remote"
    fi
fi

echo ""

# 6. GitHub Release (if gh available)
echo "📋 6. GitHub Release"
echo "--------------------"

if command -v gh >/dev/null 2>&1; then
    if gh auth status >/dev/null 2>&1; then
        if gh release view "v$VERSION" >/dev/null 2>&1; then
            check_pass "GitHub Release v$VERSION exists"
        else
            check_warn "GitHub Release v$VERSION not created (optional)"
        fi
    else
        check_warn "gh CLI not authenticated (cannot check GitHub Release)"
    fi
else
    check_warn "gh CLI not installed (cannot check GitHub Release)"
fi

echo ""

# 7. Marketplace Configuration
echo "📋 7. Marketplace Configuration"
echo "--------------------------------"

REPO_URL=$(jq -r '.plugins[0].source.repo' .claude-plugin/marketplace.json)
if [ "$REPO_URL" = "johnson-xue/summoner" ]; then
    check_pass "Marketplace repo configured correctly"
else
    check_fail "Marketplace repo misconfigured: $REPO_URL"
fi

MARKETPLACE_SOURCE=$(jq -r '.plugins[0].source.source' .claude-plugin/marketplace.json)
if [ "$MARKETPLACE_SOURCE" = "github" ]; then
    check_pass "Marketplace source is GitHub"
else
    check_warn "Marketplace source is not GitHub: $MARKETPLACE_SOURCE"
fi

echo ""

# Summary
echo "========================================"
echo "📊 Verification Summary"
echo "========================================"
echo "✅ Passed:   $PASSED"
echo "❌ Failed:   $FAILED"
echo "⚠️  Warnings: $WARNINGS"
echo ""

if [ $FAILED -eq 0 ]; then
    echo "🎉 Release v$VERSION verification PASSED!"
    echo ""
    echo "Next steps:"
    echo "  • Wait 5-15 minutes for marketplace cache to refresh"
    echo "  • Try: /plugin install github:johnson-xue/summoner"
    echo "  • Or restart Claude Code client"
    exit 0
else
    echo "❌ Release v$VERSION verification FAILED!"
    echo ""
    echo "Issues found: $FAILED"
    echo "Please fix the failed checks before considering the release complete."
    exit 1
fi
