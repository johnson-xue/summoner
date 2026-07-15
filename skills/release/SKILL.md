---
description: Version release automation with checkpoint-based workflow
dependencies:
  - jq (JSON processor)
  - git
  - gh (optional, for GitHub releases)
---

# Release Skill

Automates the complete version release workflow with three checkpoint phases.

## Purpose

This skill solves the critical problem of version drift between `plugin.json` and `marketplace.json` by providing a single, reliable release process that:
- Ensures both files are always synchronized
- Generates changelog automatically from conventional commits
- Provides safety through checkpoints and rollback
- Integrates with summoner-ctx for context persistence

## Usage

The AI should execute this skill when the user invokes `/summoner:release`.

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

```bash
INCREMENT_MAJOR=false
INCREMENT_MINOR=false
INCREMENT_PATCH=false
VERSION_ARG=""
DRY_RUN=false
NO_PUSH=false
SKIP_CHANGELOG=false

# Parse from user's command
# Example: /summoner:release --patch --dry-run
```

### Phase 1: Version Planning

**START Block:**
```
⚡ SUMMONER START — Workflow=release Phase 1/3: Version Planning
🎯 任务: 确定新版本号并验证版本文件一致性
🔧 Skill: none (版本管理逻辑)
```

**Core Logic:**

```bash
# Read versions
CURRENT_VERSION=$(jq -r '.version' .claude-plugin/plugin.json)
MARKETPLACE_VERSION=$(jq -r '.plugins[0].version' .claude-plugin/marketplace.json)

# Check consistency
if [ "$CURRENT_VERSION" != "$MARKETPLACE_VERSION" ]; then
    echo "⚠️  Version mismatch: plugin.json=$CURRENT_VERSION, marketplace.json=$MARKETPLACE_VERSION"
    echo "   Using plugin.json as source of truth"
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

**CHECKPOINT Block:**
```
┌──────────────────────────────────────────────┐
│  ⚡ SUMMONER — Phase 1/3: Version Planning    │
│                                              │
│  ✅ 完成内容: 确定新版本号并验证无冲突           │
│  📋 产物: Current: {CURRENT_VERSION},         │
│     New: {NEW_VERSION}, Type: {VERSION_TYPE} │
│  ⚠️ 发现: {consistency warning or None}      │
│                                              │
│  Next:                                       │
│  [enter] 继续下一 Phase                       │
│  [skip]  跳过下一 Phase                       │
│  [done]  完成，退出框架                       │
│  [recall] B 键回城 — 回到之前 Phase 重新来过   │
│  [stop]  紧急停止 — 保留产物，立即退出          │
└──────────────────────────────────────────────┘
```

### Phase 2: Changelog Generation

**START Block:**
```
⚡ SUMMONER START — Workflow=release Phase 2/3: Changelog Generation
🎯 任务: 从 git commits 生成 changelog 并分类
🔧 Skill: none (changelog 生成逻辑)
```

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

**CHECKPOINT Block:**
```
┌──────────────────────────────────────────────┐
│  ⚡ SUMMONER — Phase 2/3: Changelog Generation│
│                                              │
│  ✅ 完成内容: 分析 {COMMIT_COUNT} 个 commits   │
│  📋 产物: {category breakdown}               │
│  ⚠️ 发现: {No commits warning or None}      │
│                                              │
│  Next:                                       │
│  [enter] 继续下一 Phase                       │
│  [skip]  跳过下一 Phase                       │
│  [done]  完成，退出框架                       │
│  [recall] B 键回城 — 回到之前 Phase 重新来过   │
│  [stop]  紧急停止 — 保留产物，立即退出          │
└──────────────────────────────────────────────┘
```

### Phase 3: Release Execution

**START Block:**
```
⚡ SUMMONER START — Workflow=release Phase 3/3: Release Execution
🎯 任务: 更新版本文件、CHANGELOG 并执行 git 操作
🔧 Skill: none (文件更新和 git 操作)
```

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

# Update files
jq --arg v "$NEW_VERSION" '.version = $v' .claude-plugin/plugin.json > /tmp/p.json
mv /tmp/p.json .claude-plugin/plugin.json
echo "✅ Updated plugin.json"

jq --arg v "$NEW_VERSION" '.plugins[0].version = $v' .claude-plugin/marketplace.json > /tmp/m.json
mv /tmp/m.json .claude-plugin/marketplace.json
echo "✅ Updated marketplace.json"

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
git add .claude-plugin/plugin.json .claude-plugin/marketplace.json CHANGELOG.md

git commit -m "release: v$NEW_VERSION

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
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

**CHECKPOINT Block:**
```
┌──────────────────────────────────────────────┐
│  ⚡ SUMMONER — Phase 3/3: Release Execution   │
│                                              │
│  ✅ 完成内容: 完成所有发布操作                  │
│  📋 产物: commit {COMMIT_HASH}, tag          │
│     v{NEW_VERSION}, {push status}            │
│  ⚠️ 发现: None                                │
│                                              │
│  Next:                                       │
│  [enter] 继续下一 Phase                       │
│  [skip]  跳过下一 Phase                       │
│  [done]  完成，退出框架                       │
│  [recall] B 键回城 — 回到之前 Phase 重新来过   │
│  [stop]  紧急停止 — 保留产物，立即退出          │
└──────────────────────────────────────────────┘

✅ Release v{NEW_VERSION} completed!

📊 Summary:
   • Version: {CURRENT_VERSION} → {NEW_VERSION}
   • Files: plugin.json, marketplace.json, CHANGELOG.md
   • Commit: {COMMIT_HASH}
   • Tag: v{NEW_VERSION}
   • Pushed: {yes/no}
```

### Integration with summoner-ctx

After successful release, save to context database:

```bash
if command -v summoner-ctx >/dev/null 2>&1; then
    summoner-ctx save \
        --project summoner \
        --workflow "release-v$NEW_VERSION" \
        --phase "execution" \
        --skill "release" \
        --input <(cat <<EOF
Version: $NEW_VERSION
Type: $VERSION_TYPE
Commits: $COMMIT_COUNT
Changelog:
$CHANGELOG_CONTENT
EOF
)
fi
```

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

Test scenarios:
1. `--patch --dry-run` - preview without executing
2. `--minor` - actual minor release
3. `--version 2.0.0` - custom version
4. Interrupted during Phase 3 - verify rollback works
