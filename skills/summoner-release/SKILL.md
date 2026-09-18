---
name: summoner-release
description: Version release automation with checkpoint-based workflow — version planning, changelog generation, release execution. Keeps ALL THREE version manifests synchronized (root plugin.json, .claude-plugin/plugin.json, .claude-plugin/marketplace.json). Use when user invokes /summoner:release.
when_to_use:
  - User invokes /summoner:release
  - User says "发版", "release a new version", "publish 0.x.y"
  - User wants to bump version and synchronize manifests + changelog + git tag
allowed-tools:
  - Bash
  - Read
  - Edit
  - Write
dependencies:
  - jq (JSON processor)
  - git
  - gh (optional, for GitHub releases)
---

# Release Skill

Automates the complete version release workflow with three checkpoint phases.

## Purpose

Single reliable release process that eliminates version drift between the three manifests (`plugin.json`, `.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json`): auto-changelog from conventional commits, checkpoint safety, rollback, summoner-ctx persistence. Executes on `/summoner:release`.

## Implementation Guide

### Pre-Flight Validation

Before starting any phase, verify:

```bash
# 1. Check git repository
git rev-parse --git-dir >/dev/null 2>&1 || {
    echo "❌ Not a git repository"
    exit 1
}

# 2. Check jq installation
command -v jq >/dev/null 2>&1 || {
    echo "❌ jq not installed. Install: brew install jq"
    exit 1
}

# 3. Check for uncommitted changes (warning only)
if [ -n "$(git status --porcelain)" ]; then
    echo "⚠️  You have uncommitted changes. They will be preserved."
    read -p "Continue? [y/N]: " response
    [[ ! "$response" =~ ^[Yy]$ ]] && exit 0
fi
```

### Argument Parsing Template

Init `INCREMENT_MAJOR/MINOR/PATCH=false VERSION_ARG="" DRY_RUN=false NO_PUSH=false SKIP_CHANGELOG=false`, then parse the user's flags (e.g. `/summoner:release --patch --dry-run`) into them.

### Phase 1: Version Planning

**START Block:** `⚡ SUMMONER START — Workflow=release Phase 1/3: Version Planning` / `🎯 任务: 确定新版本号并验证版本文件一致性` / `🔧 Skill: none (版本管理逻辑)`

**Core Logic:**

```bash
# Read versions — ALL THREE manifests (the root plugin.json was historically
# skipped by this skill and drifted to 0.1.5 while the others moved on;
# scripts/validate-repo.sh enforces the same invariant in CI)
CURRENT_VERSION=$(jq -r '.version' .claude-plugin/plugin.json)
MARKETPLACE_VERSION=$(jq -r '.plugins[0].version' .claude-plugin/marketplace.json)
ROOT_VERSION=$(jq -r '.version' plugin.json)

# Check consistency
if [ "$CURRENT_VERSION" != "$MARKETPLACE_VERSION" ] || [ "$CURRENT_VERSION" != "$ROOT_VERSION" ]; then
    echo "⚠️  Version mismatch: .claude-plugin/plugin.json=$CURRENT_VERSION, marketplace.json=$MARKETPLACE_VERSION, root plugin.json=$ROOT_VERSION"
    echo "   Using .claude-plugin/plugin.json as source of truth"
fi

# Parse current version
IFS='.' read -r MAJOR MINOR PATCH <<< "$CURRENT_VERSION"

# Determine new version
if [ -n "$VERSION_ARG" ]; then
    NEW_VERSION="$VERSION_ARG"
    VERSION_TYPE="custom"
elif [ "$INCREMENT_MAJOR" = "true" ]; then
    NEW_VERSION="$((MAJOR + 1)).0.0"
    VERSION_TYPE="major"
elif [ "$INCREMENT_MINOR" = "true" ]; then
    NEW_VERSION="$MAJOR.$((MINOR + 1)).0"
    VERSION_TYPE="minor"
elif [ "$INCREMENT_PATCH" = "true" ]; then
    NEW_VERSION="$MAJOR.$MINOR.$((PATCH + 1))"
    VERSION_TYPE="patch"
else
    # Interactive prompt
    echo "Select version increment:"
    echo "  A. Patch ($CURRENT_VERSION → $MAJOR.$MINOR.$((PATCH + 1)))"
    echo "  B. Minor ($CURRENT_VERSION → $MAJOR.$((MINOR + 1)).0)"
    echo "  C. Major ($CURRENT_VERSION → $((MAJOR + 1)).0.0)"
    echo "  D. Custom"
    read -p "Choice [A]: " choice
    choice=${choice:-A}
    
    case "$choice" in
        A|a) NEW_VERSION="$MAJOR.$MINOR.$((PATCH + 1))"; VERSION_TYPE="patch" ;;
        B|b) NEW_VERSION="$MAJOR.$((MINOR + 1)).0"; VERSION_TYPE="minor" ;;
        C|c) NEW_VERSION="$((MAJOR + 1)).0.0"; VERSION_TYPE="major" ;;
        D|d) read -p "Enter version: " NEW_VERSION; VERSION_TYPE="custom" ;;
        *) echo "Invalid choice"; exit 1 ;;
    esac
fi

# Validate semver
if ! echo "$NEW_VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
    echo "❌ Invalid semver format: $NEW_VERSION"
    exit 1
fi

# Check tag doesn't exist
if git tag -l "v$NEW_VERSION" | grep -q .; then
    echo "❌ Tag v$NEW_VERSION already exists"
    exit 1
fi

echo "✅ Version: $CURRENT_VERSION → $NEW_VERSION ($VERSION_TYPE)"
```

**CHECKPOINT Block:** render the canonical box from `references/checkpoint-protocol.md` §CHECKPOINT Block (5 mandatory fields, fixed `Next:` grammar — never inline a variant). This phase's field values:
- 完成内容: 确定新版本号并验证无冲突
- 产物: Current: {CURRENT_VERSION}, New: {NEW_VERSION}, Type: {VERSION_TYPE}
- 发现: {consistency warning or None}


### Phase 2: Changelog Generation

**START Block:** `⚡ SUMMONER START — Workflow=release Phase 2/3: Changelog Generation` / `🎯 任务: 从 git commits 生成 changelog 并分类` / `🔧 Skill: none (changelog 生成逻辑)`

**Core Logic:**

```bash
# Get commit range
LAST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "")
if [ -n "$LAST_TAG" ]; then
    COMMIT_RANGE="$LAST_TAG..HEAD"
else
    FIRST_COMMIT=$(git rev-list --max-parents=0 HEAD)
    COMMIT_RANGE="$FIRST_COMMIT..HEAD"
fi

# Get commits
COMMITS=$(git log "$COMMIT_RANGE" --no-merges --pretty=format:"%s|%h")
COMMIT_COUNT=$(echo "$COMMITS" | wc -l | tr -d ' ')

# Initialize arrays
declare -a FEAT_COMMITS=()
declare -a FIX_COMMITS=()
declare -a DOCS_COMMITS=()
declare -a CHORE_COMMITS=()
declare -a SECURITY_COMMITS=()
declare -a PERF_COMMITS=()
declare -a REFACTOR_COMMITS=()
declare -a TEST_COMMITS=()
declare -a OTHER_COMMITS=()

# Classify commits
while IFS='|' read -r message hash; do
    [[ -z "$message" ]] && continue
    case "$message" in
        feat:*|feat\(*) FEAT_COMMITS+=("${message#feat:*( )} ($hash)") ;;
        fix:*|fix\(*) FIX_COMMITS+=("${message#fix:*( )} ($hash)") ;;
        docs:*|docs\(*) DOCS_COMMITS+=("${message#docs:*( )} ($hash)") ;;
        chore:*|chore\(*) CHORE_COMMITS+=("${message#chore:*( )} ($hash)") ;;
        security:*|security\(*|🔒*) SECURITY_COMMITS+=("${message#security:*( )} ($hash)") ;;
        perf:*|perf\(*|⚡*) PERF_COMMITS+=("${message#perf:*( )} ($hash)") ;;
        refactor:*|refactor\(*|♻️*) REFACTOR_COMMITS+=("${message#refactor:*( )} ($hash)") ;;
        test:*|test\(*|✅*) TEST_COMMITS+=("${message#test:*( )} ($hash)") ;;
        *) OTHER_COMMITS+=("$message ($hash)") ;;
    esac
done <<< "$COMMITS"

# Generate changelog
CURRENT_DATE=$(date +%Y-%m-%d)
CHANGELOG_CONTENT="## [$NEW_VERSION] - $CURRENT_DATE"

# Add sections (only non-empty ones)
[ ${#FEAT_COMMITS[@]} -gt 0 ] && {
    CHANGELOG_CONTENT+=$'\n\n### ✨ Features'
    for commit in "${FEAT_COMMITS[@]}"; do
        CHANGELOG_CONTENT+=$'\n'"- $commit"
    done
}

[ ${#FIX_COMMITS[@]} -gt 0 ] && {
    CHANGELOG_CONTENT+=$'\n\n### 🐛 Bug Fixes'
    for commit in "${FIX_COMMITS[@]}"; do
        CHANGELOG_CONTENT+=$'\n'"- $commit"
    done
}

[ ${#SECURITY_COMMITS[@]} -gt 0 ] && {
    CHANGELOG_CONTENT+=$'\n\n### 🔒 Security'
    for commit in "${SECURITY_COMMITS[@]}"; do
        CHANGELOG_CONTENT+=$'\n'"- $commit"
    done
}

# ... (add other sections similarly)

echo "$CHANGELOG_CONTENT"
```

**CHECKPOINT Block:** render the canonical box from `references/checkpoint-protocol.md` §CHECKPOINT Block (5 mandatory fields, fixed `Next:` grammar — never inline a variant). This phase's field values:
- 完成内容: 分析 {COMMIT_COUNT} 个 commits
- 产物: {category breakdown}
- 发现: {No commits warning or None}


### Phase 3: Release Execution

**START Block:** `⚡ SUMMONER START — Workflow=release Phase 3/3: Release Execution` / `🎯 任务: 更新版本文件、CHANGELOG 并执行 git 操作` / `🔧 Skill: none (文件更新和 git 操作)`

**Error Handling Setup:**

```bash
RELEASE_STARTED=false
COMMIT_CREATED=false
TAG_CREATED=false

rollback_release() {
    local exit_code=$?
    [ "$RELEASE_STARTED" = "false" ] && return
    
    echo ""
    echo "❌ Error occurred (exit code: $exit_code)"
    echo "🔄 Rolling back..."
    
    # Delete tag if created
    if [ "$TAG_CREATED" = "true" ] && git tag -l "v$NEW_VERSION" | grep -q .; then
        git tag -d "v$NEW_VERSION" 2>/dev/null
        echo "   ✅ Deleted tag v$NEW_VERSION"
    fi
    
    # Reset commit if created
    if [ "$COMMIT_CREATED" = "true" ]; then
        local last_msg=$(git log -1 --pretty=%B 2>/dev/null)
        if echo "$last_msg" | grep -q "release: v$NEW_VERSION"; then
            git reset HEAD~1 2>/dev/null
            echo "   ✅ Reset commit"
        fi
    fi
    
    echo "   📁 Files preserved for inspection"
    exit $exit_code
}

trap rollback_release ERR
RELEASE_STARTED=true
```

**Core Logic:**

```bash
# Skip if dry-run
if [ "$DRY_RUN" = "true" ]; then
    echo "🔍 DRY RUN - Would update files and create v$NEW_VERSION"
    trap - ERR
    exit 0
fi

# Update files — ALL THREE manifests (root plugin.json included; see version-check note above)
jq --arg v "$NEW_VERSION" '.version = $v' .claude-plugin/plugin.json > /tmp/p.json
mv /tmp/p.json .claude-plugin/plugin.json
echo "✅ Updated .claude-plugin/plugin.json"

jq --arg v "$NEW_VERSION" '.plugins[0].version = $v' .claude-plugin/marketplace.json > /tmp/m.json
mv /tmp/m.json .claude-plugin/marketplace.json
echo "✅ Updated .claude-plugin/marketplace.json"

jq --arg v "$NEW_VERSION" '.version = $v' plugin.json > /tmp/root.json
mv /tmp/root.json plugin.json
echo "✅ Updated root plugin.json"

# Update CHANGELOG.md
if [ -f CHANGELOG.md ]; then
    {
        head -n 1 CHANGELOG.md
        echo ""
        echo "$CHANGELOG_CONTENT"
        echo ""
        tail -n +2 CHANGELOG.md
    } > /tmp/changelog.new
    mv /tmp/changelog.new CHANGELOG.md
else
    {
        echo "# Changelog"
        echo ""
        echo "$CHANGELOG_CONTENT"
    } > CHANGELOG.md
fi
echo "✅ Updated CHANGELOG.md"

# Git operations
git add plugin.json .claude-plugin/plugin.json .claude-plugin/marketplace.json CHANGELOG.md

git commit -m "release: v$NEW_VERSION

Co-Authored-By: Claude <noreply@anthropic.com>"
COMMIT_CREATED=true
COMMIT_HASH=$(git rev-parse --short HEAD)
echo "✅ Committed $COMMIT_HASH"

git tag -a "v$NEW_VERSION" -m "Release v$NEW_VERSION

$(echo "$CHANGELOG_CONTENT" | head -n 10)"
TAG_CREATED=true
echo "✅ Tagged v$NEW_VERSION"

# Push (unless --no-push)
if [ "$NO_PUSH" != "true" ]; then
    BRANCH=$(git rev-parse --abbrev-ref HEAD)
    git push origin "$BRANCH"
    git push origin "v$NEW_VERSION"
    echo "✅ Pushed to remote"
fi

# GitHub Release (optional)
if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    read -p "📦 Create GitHub Release? [y/N]: " create_gh
    if [[ "$create_gh" =~ ^[Yy]$ ]]; then
        gh release create "v$NEW_VERSION" --title "v$NEW_VERSION" --notes "$CHANGELOG_CONTENT"
        echo "✅ Created GitHub Release"
    fi
fi

trap - ERR
```

**CHECKPOINT Block:** render the canonical box from `references/checkpoint-protocol.md` §CHECKPOINT Block (5 mandatory fields, fixed `Next:` grammar — never inline a variant). This phase's field values:
- 完成内容: 完成所有发布操作
- 产物: commit {COMMIT_HASH}, tag v{NEW_VERSION}, {push status}
- 发现: None

✅ Release v{NEW_VERSION} completed!

📊 Summary:
   • Version: {CURRENT_VERSION} → {NEW_VERSION}
   • Files: plugin.json, .claude-plugin/plugin.json, .claude-plugin/marketplace.json, CHANGELOG.md (ALL THREE manifests must carry the new version — enforce via scripts/verify-release.sh)
   • Commit: {COMMIT_HASH}
   • Tag: v{NEW_VERSION}
   • Pushed: {yes/no}

### Integration with summoner-ctx

After a successful release (guarded by `command -v summoner-ctx`):
`summoner-ctx save --project summoner --workflow "release-v$NEW_VERSION" --phase execution --skill release --input <(printf 'Version: %s\nType: %s\nCommits: %s\nChangelog:\n%s' "$NEW_VERSION" "$VERSION_TYPE" "$COMMIT_COUNT" "$CHANGELOG_CONTENT")`

## Error Scenarios

| Error | Handling |
|-------|----------|
| jq not installed | Abort with install instructions |
| Not a git repo | Abort early |
| Tag already exists | Abort in Phase 1 |
| Version files missing | Abort in Phase 1 |
| No commits since last tag | Warning in Phase 2, continue |
| Push fails | Rollback commit and tag, preserve files |
| Invalid semver | Reject and prompt again |

## Post-Game Review

Not implemented yet - should ask user about workflow experience.

## Testing

Scenarios: `--patch --dry-run`（预览不执行）/ `--minor`（真实发布）/ `--version 2.0.0`（自定义）/ Phase 3 中断（验证回滚）。
