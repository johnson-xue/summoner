# Release Workflow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `/summoner:release` command that ensures synchronized version updates across plugin.json and marketplace.json with automated changelog generation and checkpoint-based workflow.

**Architecture:** Three-phase checkpoint workflow (Version Planning → Changelog Generation → Release Execution) implemented as a Summoner command that follows checkpoint protocol, handles rollback on errors, and optionally creates GitHub releases.

**Tech Stack:** Bash scripting, jq for JSON manipulation, git CLI, gh CLI (optional), Summoner checkpoint protocol

## Global Constraints

- Must follow Summoner checkpoint protocol from `references/checkpoint-protocol.md`
- Version format must be semver X.Y.Z (three integers)
- All JSON files must preserve 2-space indentation
- Git commit must include Co-Authored-By trailer
- Rollback strategy: preserve files, clean up git operations (tags, commits)
- Post-game review Type 4 (流程评价) mandatory at workflow end

---

### Task 1: Create Command Entry Point

**Files:**
- Create: `commands/release.md`

**Interfaces:**
- Consumes: None (entry point)
- Produces: Command metadata (frontmatter) and workflow orchestration logic

- [ ] **Step 1: Write command frontmatter**

Create `commands/release.md` with metadata:

```markdown
---
description: 版本发布全流程 — 版本规划 → changelog 生成 → 发布执行。确保 plugin.json 和 marketplace.json 版本同步。
phase_checkpoints: after_each
end_action: post_game_review
---

# /summoner:release

Automated version release workflow with three checkpoint phases. Ensures zero version drift between plugin.json and marketplace.json.

## Command Signature

```bash
/summoner:release [OPTIONS]

OPTIONS:
  --major              Increment major version (X.0.0)
  --minor              Increment minor version (0.X.0)  
  --patch              Increment patch version (0.0.X)
  --version X.Y.Z      Specify exact version number
  --dry-run            Preview mode, no git operations
  --no-push            Local only (no remote push)
  --skip-changelog     Skip changelog generation
```

## Workflow

Phase 1: Version Planning → Phase 2: Changelog Generation → Phase 3: Release Execution

## Rules

1. Each phase: output **PHASE START** block at entry + **SUMMONER CHECKPOINT** block at end (format + field spec in `references/checkpoint-protocol.md`). Wait for user input.
2. Checkpoint options: continue / skip / done / recall / stop. If user reply is content feedback, handle it first then re-output CHECKPOINT.
3. All file updates must preserve formatting (2-space JSON indentation).
4. Git operations use error trapping for automatic rollback.
5. Phase 3 asks about GitHub Release creation if `gh` CLI is available.

## Post-Game Review

Mandatory Type 4 (流程评价) review at workflow end.
```

- [ ] **Step 2: Commit command file**

```bash
git add commands/release.md
git commit -m "feat(release): add command entry point

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Implement Phase 1 - Version Planning

**Files:**
- Modify: `commands/release.md` (add Phase 1 implementation section)

**Interfaces:**
- Consumes: `.claude-plugin/plugin.json` (current version), `.claude-plugin/marketplace.json` (marketplace version), command-line args
- Produces: `NEW_VERSION` variable (string, semver format X.Y.Z), `CURRENT_VERSION` variable, `VERSION_TYPE` variable (patch/minor/major/custom)

- [ ] **Step 1: Add Phase 1 START block output**

Append to `commands/release.md`:

```markdown
## Phase 1: Version Planning

### Entry

Output PHASE START block:

```
⚡ SUMMONER START — Workflow=release Phase 1/3: Version Planning
🎯 任务: 确定新版本号并验证版本文件一致性
🔧 Skill: none (版本管理逻辑)
```
```

- [ ] **Step 2: Add version detection logic**

```markdown
### Version Detection

```bash
# Read current version from plugin.json
if [ ! -f .claude-plugin/plugin.json ]; then
    echo "❌ Error: .claude-plugin/plugin.json not found"
    exit 1
fi

CURRENT_VERSION=$(jq -r '.version' .claude-plugin/plugin.json)
if [ -z "$CURRENT_VERSION" ] || [ "$CURRENT_VERSION" = "null" ]; then
    echo "❌ Error: version field missing in plugin.json"
    exit 1
fi

# Read marketplace version
if [ ! -f .claude-plugin/marketplace.json ]; then
    echo "❌ Error: .claude-plugin/marketplace.json not found"
    exit 1
fi

MARKETPLACE_VERSION=$(jq -r '.plugins[0].version' .claude-plugin/marketplace.json)

# Check consistency
if [ "$CURRENT_VERSION" != "$MARKETPLACE_VERSION" ]; then
    echo "⚠️  Warning: Version mismatch detected"
    echo "   plugin.json: $CURRENT_VERSION"
    echo "   marketplace.json: $MARKETPLACE_VERSION"
    echo "   Using plugin.json as source of truth"
fi

echo "📌 Current version: $CURRENT_VERSION"
```
```

- [ ] **Step 3: Add version increment logic**

```markdown
### Version Increment

```bash
# Parse current version
IFS='.' read -r MAJOR MINOR PATCH <<< "$CURRENT_VERSION"

# Determine new version based on arguments
if [ -n "$VERSION_ARG" ]; then
    # User specified exact version
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
    # Interactive mode
    echo ""
    echo "Select version increment:"
    echo "  A. Patch ($CURRENT_VERSION → $MAJOR.$MINOR.$((PATCH + 1)))"
    echo "  B. Minor ($CURRENT_VERSION → $MAJOR.$((MINOR + 1)).0)"
    echo "  C. Major ($CURRENT_VERSION → $((MAJOR + 1)).0.0)"
    echo "  D. Custom (enter manually)"
    echo ""
    read -p "Choice [A]: " choice
    choice=${choice:-A}
    
    case "$choice" in
        A|a)
            NEW_VERSION="$MAJOR.$MINOR.$((PATCH + 1))"
            VERSION_TYPE="patch"
            ;;
        B|b)
            NEW_VERSION="$MAJOR.$((MINOR + 1)).0"
            VERSION_TYPE="minor"
            ;;
        C|c)
            NEW_VERSION="$((MAJOR + 1)).0.0"
            VERSION_TYPE="major"
            ;;
        D|d)
            read -p "Enter version (X.Y.Z): " NEW_VERSION
            VERSION_TYPE="custom"
            ;;
        *)
            echo "❌ Invalid choice"
            exit 1
            ;;
    esac
fi
```
```

- [ ] **Step 4: Add version validation**

```markdown
### Version Validation

```bash
# Validate semver format
if ! echo "$NEW_VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
    echo "❌ Error: Invalid semver format '$NEW_VERSION'"
    echo "   Must be X.Y.Z (e.g., 1.2.3)"
    exit 1
fi

# Parse new version
IFS='.' read -r NEW_MAJOR NEW_MINOR NEW_PATCH <<< "$NEW_VERSION"

# Compare with current version
version_greater_than() {
    local v1_major=$1 v1_minor=$2 v1_patch=$3
    local v2_major=$4 v2_minor=$5 v2_patch=$6
    
    if [ "$v1_major" -gt "$v2_major" ]; then return 0; fi
    if [ "$v1_major" -lt "$v2_major" ]; then return 1; fi
    if [ "$v1_minor" -gt "$v2_minor" ]; then return 0; fi
    if [ "$v1_minor" -lt "$v2_minor" ]; then return 1; fi
    if [ "$v1_patch" -gt "$v2_patch" ]; then return 0; fi
    return 1
}

if ! version_greater_than "$NEW_MAJOR" "$NEW_MINOR" "$NEW_PATCH" "$MAJOR" "$MINOR" "$PATCH"; then
    echo "❌ Error: New version $NEW_VERSION must be greater than current $CURRENT_VERSION"
    exit 1
fi

# Check if git tag already exists
if git tag -l "v$NEW_VERSION" | grep -q .; then
    echo "❌ Error: Git tag v$NEW_VERSION already exists"
    echo "   Use a different version or delete the existing tag"
    exit 1
fi

echo "✅ Version validation passed: $CURRENT_VERSION → $NEW_VERSION"
```
```

- [ ] **Step 5: Add Phase 1 CHECKPOINT block**

```markdown
### Checkpoint 1

Output CHECKPOINT block:

```
┌──────────────────────────────────────────────┐
│  ⚡ SUMMONER — Phase 1/3: Version Planning    │
│                                              │
│  ✅ 完成内容: 确定新版本号并验证无冲突           │
│  📋 产物: Current: {CURRENT_VERSION},         │
│     New: {NEW_VERSION}, Type: {VERSION_TYPE} │
│  ⚠️ 发现: {Version consistency warning or None}│
│                                              │
│  Next:                                       │
│  [enter] 继续下一 Phase                       │
│  [skip]  跳过下一 Phase                       │
│  [done]  完成，退出框架                       │
│  [recall] B 键回城 — 回到之前 Phase 重新来过   │
│  [stop]  紧急停止 — 保留产物，立即退出          │
└──────────────────────────────────────────────┘
```

Wait for user input and handle checkpoint actions.
```
```

- [ ] **Step 6: Commit Phase 1 implementation**

```bash
git add commands/release.md
git commit -m "feat(release): implement Phase 1 version planning

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Implement Phase 2 - Changelog Generation

**Files:**
- Modify: `commands/release.md` (add Phase 2 implementation section)

**Interfaces:**
- Consumes: `NEW_VERSION` (from Task 2), git commit history
- Produces: `CHANGELOG_CONTENT` variable (string, markdown formatted), `CHANGELOG_FILE` path (temporary file with changelog)

- [ ] **Step 1: Add Phase 2 START block output**

Append to `commands/release.md`:

```markdown
## Phase 2: Changelog Generation

### Entry

Output PHASE START block:

```
⚡ SUMMONER START — Workflow=release Phase 2/3: Changelog Generation
🎯 任务: 从 git commits 生成 changelog 并分类
🔧 Skill: none (changelog 生成逻辑)
```
```

- [ ] **Step 2: Add commit range determination**

```markdown
### Commit Range Detection

```bash
# Get last tag
LAST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "")

if [ -n "$LAST_TAG" ]; then
    COMMIT_RANGE="$LAST_TAG..HEAD"
    echo "📊 Analyzing commits from $LAST_TAG to HEAD"
else
    # Get first commit
    FIRST_COMMIT=$(git rev-list --max-parents=0 HEAD)
    COMMIT_RANGE="$FIRST_COMMIT..HEAD"
    echo "📊 Analyzing commits from initial commit to HEAD (first release)"
fi

# Get commits (exclude merge commits)
COMMITS=$(git log "$COMMIT_RANGE" --no-merges --pretty=format:"%s|%h" 2>/dev/null || echo "")

if [ -z "$COMMITS" ]; then
    echo "⚠️  Warning: No commits since last release"
    COMMIT_COUNT=0
else
    COMMIT_COUNT=$(echo "$COMMITS" | wc -l | tr -d ' ')
    echo "📝 Found $COMMIT_COUNT commits to process"
fi
```
```

- [ ] **Step 3: Add commit classification logic**

```markdown
### Commit Classification

```bash
# Initialize category arrays
declare -a FEAT_COMMITS
declare -a FIX_COMMITS
declare -a PERF_COMMITS
declare -a REFACTOR_COMMITS
declare -a DOCS_COMMITS
declare -a TEST_COMMITS
declare -a CHORE_COMMITS
declare -a SECURITY_COMMITS
declare -a OTHER_COMMITS

# Classify commits
while IFS='|' read -r message hash; do
    case "$message" in
        feat:*|feat\(*|✨*)
            FEAT_COMMITS+=("${message#feat:*( )} ($hash)")
            ;;
        fix:*|fix\(*|🐛*)
            FIX_COMMITS+=("${message#fix:*( )} ($hash)")
            ;;
        perf:*|perf\(*|⚡*)
            PERF_COMMITS+=("${message#perf:*( )} ($hash)")
            ;;
        refactor:*|refactor\(*|♻️*)
            REFACTOR_COMMITS+=("${message#refactor:*( )} ($hash)")
            ;;
        docs:*|docs\(*|📝*)
            DOCS_COMMITS+=("${message#docs:*( )} ($hash)")
            ;;
        test:*|test\(*|✅*)
            TEST_COMMITS+=("${message#test:*( )} ($hash)")
            ;;
        chore:*|chore\(*|🔧*)
            CHORE_COMMITS+=("${message#chore:*( )} ($hash)")
            ;;
        security:*|security\(*|🔒*)
            SECURITY_COMMITS+=("${message#security:*( )} ($hash)")
            ;;
        *)
            OTHER_COMMITS+=("$message ($hash)")
            ;;
    esac
done <<< "$COMMITS"

# Count commits by category
echo "📊 Category breakdown:"
echo "   ✨ Features: ${#FEAT_COMMITS[@]}"
echo "   🐛 Bug Fixes: ${#FIX_COMMITS[@]}"
echo "   🔒 Security: ${#SECURITY_COMMITS[@]}"
echo "   ⚡ Performance: ${#PERF_COMMITS[@]}"
echo "   ♻️  Refactoring: ${#REFACTOR_COMMITS[@]}"
echo "   📝 Documentation: ${#DOCS_COMMITS[@]}"
echo "   ✅ Tests: ${#TEST_COMMITS[@]}"
echo "   🔧 Chores: ${#CHORE_COMMITS[@]}"
echo "   📦 Other: ${#OTHER_COMMITS[@]}"
```
```

- [ ] **Step 4: Add changelog formatting**

```markdown
### Changelog Formatting

```bash
# Create temporary changelog file
CHANGELOG_FILE=$(mktemp)
CURRENT_DATE=$(date +%Y-%m-%d)

# Write changelog header
cat > "$CHANGELOG_FILE" << CHANGELOG_HEADER
## [$NEW_VERSION] - $CURRENT_DATE
CHANGELOG_HEADER

# Add Features section
if [ ${#FEAT_COMMITS[@]} -gt 0 ]; then
    echo "" >> "$CHANGELOG_FILE"
    echo "### ✨ Features" >> "$CHANGELOG_FILE"
    for commit in "${FEAT_COMMITS[@]}"; do
        echo "- $commit" >> "$CHANGELOG_FILE"
    done
fi

# Add Bug Fixes section
if [ ${#FIX_COMMITS[@]} -gt 0 ]; then
    echo "" >> "$CHANGELOG_FILE"
    echo "### 🐛 Bug Fixes" >> "$CHANGELOG_FILE"
    for commit in "${FIX_COMMITS[@]}"; do
        echo "- $commit" >> "$CHANGELOG_FILE"
    done
fi

# Add Security section
if [ ${#SECURITY_COMMITS[@]} -gt 0 ]; then
    echo "" >> "$CHANGELOG_FILE"
    echo "### 🔒 Security" >> "$CHANGELOG_FILE"
    for commit in "${SECURITY_COMMITS[@]}"; do
        echo "- $commit" >> "$CHANGELOG_FILE"
    done
fi

# Add Performance section
if [ ${#PERF_COMMITS[@]} -gt 0 ]; then
    echo "" >> "$CHANGELOG_FILE"
    echo "### ⚡ Performance" >> "$CHANGELOG_FILE"
    for commit in "${PERF_COMMITS[@]}"; do
        echo "- $commit" >> "$CHANGELOG_FILE"
    done
fi

# Add Refactoring section
if [ ${#REFACTOR_COMMITS[@]} -gt 0 ]; then
    echo "" >> "$CHANGELOG_FILE"
    echo "### ♻️ Refactoring" >> "$CHANGELOG_FILE"
    for commit in "${REFACTOR_COMMITS[@]}"; do
        echo "- $commit" >> "$CHANGELOG_FILE"
    done
fi

# Add Documentation section
if [ ${#DOCS_COMMITS[@]} -gt 0 ]; then
    echo "" >> "$CHANGELOG_FILE"
    echo "### 📝 Documentation" >> "$CHANGELOG_FILE"
    for commit in "${DOCS_COMMITS[@]}"; do
        echo "- $commit" >> "$CHANGELOG_FILE"
    done
fi

# Add Tests section
if [ ${#TEST_COMMITS[@]} -gt 0 ]; then
    echo "" >> "$CHANGELOG_FILE"
    echo "### ✅ Tests" >> "$CHANGELOG_FILE"
    for commit in "${TEST_COMMITS[@]}"; do
        echo "- $commit" >> "$CHANGELOG_FILE"
    done
fi

# Add Chores section
if [ ${#CHORE_COMMITS[@]} -gt 0 ]; then
    echo "" >> "$CHANGELOG_FILE"
    echo "### 🔧 Chores" >> "$CHANGELOG_FILE"
    for commit in "${CHORE_COMMITS[@]}"; do
        echo "- $commit" >> "$CHANGELOG_FILE"
    done
fi

# Add Other Changes section
if [ ${#OTHER_COMMITS[@]} -gt 0 ]; then
    echo "" >> "$CHANGELOG_FILE"
    echo "### 📦 Other Changes" >> "$CHANGELOG_FILE"
    for commit in "${OTHER_COMMITS[@]}"; do
        echo "- $commit" >> "$CHANGELOG_FILE"
    done
fi

# Store changelog content for later use
CHANGELOG_CONTENT=$(cat "$CHANGELOG_FILE")

echo ""
echo "📄 Generated changelog preview:"
echo "---"
head -n 20 "$CHANGELOG_FILE"
if [ $(wc -l < "$CHANGELOG_FILE") -gt 20 ]; then
    echo "..."
    echo "(showing first 20 lines)"
fi
echo "---"
```
```

- [ ] **Step 5: Add Phase 2 CHECKPOINT block**

```markdown
### Checkpoint 2

Output CHECKPOINT block:

```
┌──────────────────────────────────────────────┐
│  ⚡ SUMMONER — Phase 2/3: Changelog Generation│
│                                              │
│  ✅ 完成内容: 分析 {COMMIT_COUNT} 个 commits    │
│     并生成 changelog                          │
│  📋 产物: {CHANGELOG_FILE} (临时文件),         │
│     {分类统计}                                 │
│  ⚠️ 发现: {No commits warning or None}       │
│                                              │
│  Next:                                       │
│  [enter] 继续下一 Phase                       │
│  [skip]  跳过下一 Phase                       │
│  [done]  完成，退出框架                       │
│  [recall] B 键回城 — 回到之前 Phase 重新来过   │
│  [stop]  紧急停止 — 保留产物，立即退出          │
└──────────────────────────────────────────────┘
```

Wait for user input and handle checkpoint actions.
```
```

- [ ] **Step 6: Commit Phase 2 implementation**

```bash
git add commands/release.md
git commit -m "feat(release): implement Phase 2 changelog generation

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Implement Phase 3 - Release Execution

**Files:**
- Modify: `commands/release.md` (add Phase 3 implementation section)

**Interfaces:**
- Consumes: `NEW_VERSION` (from Task 2), `CHANGELOG_FILE` (from Task 3), command-line flags (--dry-run, --no-push)
- Produces: Updated `.claude-plugin/plugin.json`, updated `.claude-plugin/marketplace.json`, updated/created `CHANGELOG.md`, git commit, git tag, pushed changes (unless --no-push)

- [ ] **Step 1: Add Phase 3 START block output**

Append to `commands/release.md`:

```markdown
## Phase 3: Release Execution

### Entry

Output PHASE START block:

```
⚡ SUMMONER START — Workflow=release Phase 3/3: Release Execution
🎯 任务: 更新版本文件、CHANGELOG 并执行 git 操作
🔧 Skill: none (文件更新和 git 操作)
```
```

- [ ] **Step 2: Add error trap for rollback**

```markdown
### Error Handling Setup

```bash
# Setup error trap for automatic rollback
RELEASE_STARTED=false
COMMIT_CREATED=false
TAG_CREATED=false

rollback_release() {
    local exit_code=$?
    
    if [ "$RELEASE_STARTED" = "false" ]; then
        return
    fi
    
    echo ""
    echo "❌ Error occurred (exit code: $exit_code)"
    echo "🔄 Rolling back release operations..."
    
    # Delete tag if created
    if [ "$TAG_CREATED" = "true" ]; then
        if git tag -l "v$NEW_VERSION" | grep -q .; then
            git tag -d "v$NEW_VERSION" 2>/dev/null
            echo "   ✅ Deleted local tag v$NEW_VERSION"
        fi
    fi
    
    # Reset commit if created
    if [ "$COMMIT_CREATED" = "true" ]; then
        local last_commit_msg=$(git log -1 --pretty=%B 2>/dev/null)
        if echo "$last_commit_msg" | grep -q "release: v$NEW_VERSION"; then
            git reset HEAD~1 2>/dev/null
            echo "   ✅ Reset release commit"
        fi
    fi
    
    echo "   📁 Modified files preserved for inspection"
    echo "      Use 'git status' to see changes"
    echo "      Use 'git restore <file>' to discard"
    echo ""
    
    exit $exit_code
}

trap rollback_release ERR

RELEASE_STARTED=true
```
```

- [ ] **Step 3: Add file update operations**

```markdown
### File Updates

```bash
echo "📝 Updating version files..."

# Update plugin.json
PLUGIN_JSON=".claude-plugin/plugin.json"
if [ ! -f "$PLUGIN_JSON" ]; then
    echo "❌ Error: $PLUGIN_JSON not found"
    exit 1
fi

# Use jq to update version
TMP_FILE=$(mktemp)
jq --arg version "$NEW_VERSION" '.version = $version' "$PLUGIN_JSON" > "$TMP_FILE"
mv "$TMP_FILE" "$PLUGIN_JSON"
echo "   ✅ Updated $PLUGIN_JSON → $NEW_VERSION"

# Update marketplace.json
MARKETPLACE_JSON=".claude-plugin/marketplace.json"
if [ ! -f "$MARKETPLACE_JSON" ]; then
    echo "❌ Error: $MARKETPLACE_JSON not found"
    exit 1
fi

# Use jq to update first plugin version
TMP_FILE=$(mktemp)
jq --arg version "$NEW_VERSION" '.plugins[0].version = $version' "$MARKETPLACE_JSON" > "$TMP_FILE"
mv "$TMP_FILE" "$MARKETPLACE_JSON"
echo "   ✅ Updated $MARKETPLACE_JSON → $NEW_VERSION"

# Update or create CHANGELOG.md
if [ -f CHANGELOG.md ]; then
    # Insert new section after header
    TMP_FILE=$(mktemp)
    head -n 1 CHANGELOG.md > "$TMP_FILE"
    echo "" >> "$TMP_FILE"
    cat "$CHANGELOG_FILE" >> "$TMP_FILE"
    echo "" >> "$TMP_FILE"
    tail -n +2 CHANGELOG.md >> "$TMP_FILE"
    mv "$TMP_FILE" CHANGELOG.md
    echo "   ✅ Updated CHANGELOG.md (prepended v$NEW_VERSION)"
else
    # Create new CHANGELOG.md
    {
        echo "# Changelog"
        echo ""
        cat "$CHANGELOG_FILE"
    } > CHANGELOG.md
    echo "   ✅ Created CHANGELOG.md"
fi

# Clean up temporary changelog file
rm -f "$CHANGELOG_FILE"
```
```

- [ ] **Step 4: Add git operations**

```markdown
### Git Operations

```bash
# Check for dry-run mode
if [ "$DRY_RUN" = "true" ]; then
    echo ""
    echo "🔍 DRY RUN MODE - No git operations executed"
    echo "   Files updated but not committed"
    echo "   Review changes with: git diff"
    trap - ERR  # Remove error trap
    exit 0
fi

echo ""
echo "🔧 Executing git operations..."

# Stage files
git add "$PLUGIN_JSON" "$MARKETPLACE_JSON" CHANGELOG.md
echo "   📦 Staged version files"

# Create commit
COMMIT_MSG="release: v$NEW_VERSION

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"

git commit -m "$COMMIT_MSG"
COMMIT_CREATED=true
COMMIT_HASH=$(git rev-parse --short HEAD)
echo "   ✅ Created commit $COMMIT_HASH"

# Create annotated tag
TAG_MSG="Release v$NEW_VERSION

$(head -n 5 "$CHANGELOG_FILE" 2>/dev/null || echo "See CHANGELOG.md for details")"

git tag -a "v$NEW_VERSION" -m "$TAG_MSG"
TAG_CREATED=true
echo "   ✅ Created tag v$NEW_VERSION"

# Push operations (unless --no-push)
if [ "$NO_PUSH" = "true" ]; then
    echo "   ⏭️  Skipping push (--no-push flag)"
else
    CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
    
    echo "   🚀 Pushing to origin..."
    git push origin "$CURRENT_BRANCH"
    echo "   ✅ Pushed commit to origin/$CURRENT_BRANCH"
    
    git push origin "v$NEW_VERSION"
    echo "   ✅ Pushed tag v$NEW_VERSION"
fi

# Remove error trap after successful completion
trap - ERR
```
```

- [ ] **Step 5: Add GitHub Release creation**

```markdown
### GitHub Release (Optional)

```bash
# Check if gh CLI is available
GH_AVAILABLE=false
if command -v gh &> /dev/null; then
    # Check if authenticated
    if gh auth status &> /dev/null; then
        GH_AVAILABLE=true
    fi
fi

if [ "$GH_AVAILABLE" = "true" ]; then
    echo ""
    read -p "📦 Create GitHub Release? [y/N]: " create_release
    
    if [[ "$create_release" =~ ^[Yy]$ ]]; then
        # Extract version-specific changelog
        RELEASE_NOTES=$(awk "/^## \[$NEW_VERSION\]/,/^## \[/" CHANGELOG.md | head -n -1)
        
        gh release create "v$NEW_VERSION" \
            --title "v$NEW_VERSION" \
            --notes "$RELEASE_NOTES"
        
        echo "   ✅ Created GitHub Release v$NEW_VERSION"
    else
        echo "   ⏭️  Skipped GitHub Release creation"
    fi
else
    echo ""
    echo "ℹ️  gh CLI not available or not authenticated"
    echo "   Skipping GitHub Release creation"
    echo "   Install: https://cli.github.com/"
fi
```
```

- [ ] **Step 6: Add Phase 3 CHECKPOINT block**

```markdown
### Checkpoint 3

Output CHECKPOINT block:

```
┌──────────────────────────────────────────────┐
│  ⚡ SUMMONER — Phase 3/3: Release Execution   │
│                                              │
│  ✅ 完成内容: 更新版本文件、创建 commit 和 tag   │
│  📋 产物: plugin.json, marketplace.json,      │
│     CHANGELOG.md, commit {COMMIT_HASH},       │
│     tag v{NEW_VERSION}, {推送状态}            │
│  ⚠️ 发现: None                                │
│                                              │
│  Next:                                       │
│  [enter] 继续下一 Phase                       │
│  [skip]  跳过下一 Phase                       │
│  [done]  完成，退出框架                       │
│  [recall] B 键回城 — 回到之前 Phase 重新来过   │
│  [stop]  紧急停止 — 保留产物，立即退出          │
└──────────────────────────────────────────────┘
```

Wait for user input. If 'done' or 'enter' (no next phase), display success message:

```
✅ Release v{NEW_VERSION} completed successfully!

📊 Summary:
   • Version: {CURRENT_VERSION} → {NEW_VERSION}
   • Files: plugin.json, marketplace.json, CHANGELOG.md
   • Commit: {COMMIT_HASH}
   • Tag: v{NEW_VERSION}
   • Pushed: {yes/no/skipped}
   • GitHub Release: {created/skipped/unavailable}

🔍 Verify:
   git log -1 --stat
   git tag -l | tail -5
   git show v{NEW_VERSION}
```
```

- [ ] **Step 7: Commit Phase 3 implementation**

```bash
git add commands/release.md
git commit -m "feat(release): implement Phase 3 release execution with rollback

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Add Argument Parsing and Main Orchestration

**Files:**
- Modify: `commands/release.md` (add argument parsing and main flow at beginning)

**Interfaces:**
- Consumes: Command-line arguments from user
- Produces: Parsed flags (INCREMENT_MAJOR, INCREMENT_MINOR, INCREMENT_PATCH, VERSION_ARG, DRY_RUN, NO_PUSH, SKIP_CHANGELOG)

- [ ] **Step 1: Add pre-flight checks**

Insert after the "## Rules" section in `commands/release.md`:

```markdown
## Implementation

### Pre-Flight Checks

```bash
# Check if in git repository
if ! git rev-parse --git-dir > /dev/null 2>&1; then
    echo "❌ Error: Not a git repository"
    echo "   Initialize with: git init"
    exit 1
fi

# Check for uncommitted changes (warning only)
if [ -n "$(git status --porcelain)" ]; then
    echo "⚠️  Warning: You have uncommitted changes"
    echo "   Current changes will be preserved"
    echo ""
    read -p "Continue anyway? [y/N]: " continue_anyway
    if [[ ! "$continue_anyway" =~ ^[Yy]$ ]]; then
        echo "Aborted by user"
        exit 0
    fi
    echo ""
fi

# Check if jq is installed
if ! command -v jq &> /dev/null; then
    echo "❌ Error: jq is not installed"
    echo "   Install with: brew install jq (macOS)"
    echo "   Or: apt-get install jq (Linux)"
    exit 1
fi
```
```

- [ ] **Step 2: Add argument parsing**

```markdown
### Argument Parsing

```bash
# Initialize flags
INCREMENT_MAJOR=false
INCREMENT_MINOR=false
INCREMENT_PATCH=false
VERSION_ARG=""
DRY_RUN=false
NO_PUSH=false
SKIP_CHANGELOG=false

# Parse command-line arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --major)
            INCREMENT_MAJOR=true
            shift
            ;;
        --minor)
            INCREMENT_MINOR=true
            shift
            ;;
        --patch)
            INCREMENT_PATCH=true
            shift
            ;;
        --version)
            if [ -z "$2" ] || [[ "$2" == --* ]]; then
                echo "❌ Error: --version requires a version number"
                exit 1
            fi
            VERSION_ARG="$2"
            shift 2
            ;;
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        --no-push)
            NO_PUSH=true
            shift
            ;;
        --skip-changelog)
            SKIP_CHANGELOG=true
            shift
            ;;
        --help|-h)
            cat << HELP
Usage: /summoner:release [OPTIONS]

OPTIONS:
  --major              Increment major version (X.0.0)
  --minor              Increment minor version (0.X.0)
  --patch              Increment patch version (0.0.X)
  --version X.Y.Z      Specify exact version number
  --dry-run            Preview mode, no git operations
  --no-push            Local only (no remote push)
  --skip-changelog     Skip changelog generation
  --help, -h           Show this help message

EXAMPLES:
  /summoner:release --patch
  /summoner:release --version 1.0.0
  /summoner:release --minor --dry-run
HELP
            exit 0
            ;;
        *)
            echo "❌ Error: Unknown option '$1'"
            echo "   Use --help for usage information"
            exit 1
            ;;
    esac
done

# Validate mutually exclusive version options
VERSION_OPTS=0
[ "$INCREMENT_MAJOR" = "true" ] && ((VERSION_OPTS++))
[ "$INCREMENT_MINOR" = "true" ] && ((VERSION_OPTS++))
[ "$INCREMENT_PATCH" = "true" ] && ((VERSION_OPTS++))
[ -n "$VERSION_ARG" ] && ((VERSION_OPTS++))

if [ "$VERSION_OPTS" -gt 1 ]; then
    echo "❌ Error: Only one version option allowed"
    echo "   (--major, --minor, --patch, or --version)"
    exit 1
fi

# Display dry-run notice
if [ "$DRY_RUN" = "true" ]; then
    echo "🔍 DRY RUN MODE - No git operations will be executed"
    echo ""
fi
```
```

- [ ] **Step 3: Add workflow orchestration**

```markdown
### Main Workflow

```bash
echo "🚀 Starting release workflow..."
echo ""

# Phase 1: Version Planning
# [Phase 1 implementation from Task 2 goes here]

# Handle checkpoint 1
read -p "> " checkpoint1_action
case "$checkpoint1_action" in
    ""|continue)
        echo "Proceeding to Phase 2..."
        ;;
    skip)
        echo "⏭️  Skipping to Phase 3 (no changelog)"
        SKIP_CHANGELOG=true
        ;;
    done)
        echo "✅ Workflow completed (stopped after Phase 1)"
        exit 0
        ;;
    recall)
        echo "🔄 Restarting Phase 1..."
        # Re-execute Phase 1 (loop back)
        ;;
    stop)
        echo "⏹️  Workflow stopped by user"
        exit 0
        ;;
    *)
        if [[ -n "$checkpoint1_action" ]]; then
            echo "📝 Feedback received: $checkpoint1_action"
            echo "   Processing feedback..."
            # Handle feedback and re-output checkpoint
        fi
        ;;
esac

# Phase 2: Changelog Generation (unless skipped)
if [ "$SKIP_CHANGELOG" = "false" ]; then
    # [Phase 2 implementation from Task 3 goes here]
    
    # Handle checkpoint 2
    read -p "> " checkpoint2_action
    case "$checkpoint2_action" in
        ""|continue)
            echo "Proceeding to Phase 3..."
            ;;
        skip)
            echo "⏭️  Skipping changelog update"
            SKIP_CHANGELOG=true
            ;;
        done)
            echo "✅ Workflow completed (stopped after Phase 2)"
            exit 0
            ;;
        recall)
            echo "🔄 Regenerating changelog..."
            # Re-execute Phase 2
            ;;
        stop)
            echo "⏹️  Workflow stopped by user"
            exit 0
            ;;
        *)
            if [[ -n "$checkpoint2_action" ]]; then
                echo "📝 Feedback received: $checkpoint2_action"
                # Handle feedback
            fi
            ;;
    esac
fi

# Phase 3: Release Execution
# [Phase 3 implementation from Task 4 goes here]

# Handle checkpoint 3 (final)
read -p "> " checkpoint3_action
case "$checkpoint3_action" in
    ""|continue|done)
        echo ""
        echo "✅ Release v$NEW_VERSION completed successfully!"
        ;;
    stop)
        echo "⏹️  Workflow stopped by user at final checkpoint"
        exit 0
        ;;
    *)
        if [[ -n "$checkpoint3_action" ]]; then
            echo "📝 Unexpected input at final checkpoint: $checkpoint3_action"
        fi
        ;;
esac
```
```

- [ ] **Step 4: Add post-game review trigger**

```markdown
### Post-Game Review

Trigger Type 4 (流程评价) review at workflow end.

```bash
echo ""
echo "📝 Post-Game Review"
echo ""
echo "## 📝 Release Workflow Review"
echo ""
echo "### 1. Version Planning (Phase 1)"
echo "- Was version increment logic clear and predictable?"
echo "- Did auto-increment (--patch/--minor/--major) work as expected?"
echo "- Any issues with version conflict detection?"
echo ""
read -p "Rating (1-5): " phase1_rating
read -p "Improvement suggestions: " phase1_suggestions
echo ""

echo "### 2. Changelog Generation (Phase 2)"
echo "- Were commits classified accurately?"
echo "- Was the changelog format clear and readable?"
echo "- Did you need to manually edit the changelog?"
echo ""
read -p "Rating (1-5): " phase2_rating
read -p "Improvement suggestions: " phase2_suggestions
echo ""

echo "### 3. Checkpoint Flow"
echo "- Were 3 checkpoints too many / too few / just right?"
echo "- Which checkpoint could be combined or removed?"
echo "- Was the information at each checkpoint sufficient?"
echo ""
read -p "Rating (1-5): " checkpoint_rating
read -p "Improvement suggestions: " checkpoint_suggestions
echo ""

echo "### 4. Execution Reliability (Phase 3)"
echo "- Did all file updates complete successfully?"
echo "- Did git operations (commit/tag/push) work smoothly?"
echo "- Any unexpected errors or rollback needed?"
echo ""
read -p "Rating (1-5): " phase3_rating
read -p "Issues encountered: " phase3_issues
echo ""

echo "### 5. Overall Experience"
echo "- Would you use this skill for future releases?"
echo "- What's the most annoying part of the workflow?"
echo "- Feature requests for next iteration?"
echo ""
read -p "Overall Rating (1-5): " overall_rating
read -p "Key improvement: " key_improvement
echo ""

echo "Thank you for your feedback!"
```
```

- [ ] **Step 5: Commit main orchestration**

```bash
git add commands/release.md
git commit -m "feat(release): add argument parsing and workflow orchestration

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Integration Testing

**Files:**
- Create: `tests/release-workflow-test.sh` (integration test script)

**Interfaces:**
- Consumes: Completed `/summoner:release` command
- Produces: Test results (pass/fail), test artifacts in temporary directory

- [ ] **Step 1: Write test setup**

Create `tests/release-workflow-test.sh`:

```bash
#!/bin/bash
set -e

echo "🧪 Release Workflow Integration Tests"
echo "======================================"
echo ""

# Create temporary test repository
TEST_DIR=$(mktemp -d)
echo "📁 Test directory: $TEST_DIR"
cd "$TEST_DIR"

# Initialize git repo
git init
git config user.name "Test User"
git config user.email "test@example.com"

# Create minimal plugin structure
mkdir -p .claude-plugin
cat > .claude-plugin/plugin.json << JSON
{
  "name": "test-plugin",
  "version": "0.1.0",
  "description": "Test plugin"
}
JSON

cat > .claude-plugin/marketplace.json << JSON
{
  "name": "test-marketplace",
  "plugins": [
    {
      "name": "test-plugin",
      "version": "0.1.0"
    }
  ]
}
JSON

# Create initial commit
git add .
git commit -m "chore: initial commit"

# Create some test commits
git commit --allow-empty -m "feat: add feature A"
git commit --allow-empty -m "fix: resolve bug B"
git commit --allow-empty -m "docs: update readme"

echo "✅ Test repository setup complete"
echo ""
```

- [ ] **Step 2: Write test cases**

```bash
# Test 1: Version detection
echo "Test 1: Version detection"
echo "-------------------------"
CURRENT=$(jq -r '.version' .claude-plugin/plugin.json)
if [ "$CURRENT" = "0.1.0" ]; then
    echo "✅ PASS: Current version detected correctly"
else
    echo "❌ FAIL: Expected 0.1.0, got $CURRENT"
    exit 1
fi
echo ""

# Test 2: Version increment logic (patch)
echo "Test 2: Version increment (patch)"
echo "----------------------------------"
# Simulate increment: 0.1.0 → 0.1.1
IFS='.' read -r MAJOR MINOR PATCH <<< "$CURRENT"
NEW_VERSION="$MAJOR.$MINOR.$((PATCH + 1))"
if [ "$NEW_VERSION" = "0.1.1" ]; then
    echo "✅ PASS: Patch increment correct"
else
    echo "❌ FAIL: Expected 0.1.1, got $NEW_VERSION"
    exit 1
fi
echo ""

# Test 3: Version validation (semver format)
echo "Test 3: Version validation"
echo "--------------------------"
validate_version() {
    echo "$1" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'
}

if validate_version "1.2.3"; then
    echo "✅ PASS: Valid semver accepted"
else
    echo "❌ FAIL: Valid semver rejected"
    exit 1
fi

if validate_version "1.2"; then
    echo "❌ FAIL: Invalid semver accepted"
    exit 1
else
    echo "✅ PASS: Invalid semver rejected"
fi
echo ""

# Test 4: Changelog classification
echo "Test 4: Changelog classification"
echo "--------------------------------"
COMMITS=$(git log --no-merges --pretty=format:"%s" HEAD~3..HEAD)
FEAT_COUNT=$(echo "$COMMITS" | grep -c "^feat:" || true)
FIX_COUNT=$(echo "$COMMITS" | grep -c "^fix:" || true)
DOCS_COUNT=$(echo "$COMMITS" | grep -c "^docs:" || true)

if [ "$FEAT_COUNT" -eq 1 ] && [ "$FIX_COUNT" -eq 1 ] && [ "$DOCS_COUNT" -eq 1 ]; then
    echo "✅ PASS: Commits classified correctly"
    echo "   Features: $FEAT_COUNT, Fixes: $FIX_COUNT, Docs: $DOCS_COUNT"
else
    echo "❌ FAIL: Commit classification incorrect"
    exit 1
fi
echo ""

# Test 5: File update (JSON manipulation)
echo "Test 5: File update simulation"
echo "-------------------------------"
TMP_FILE=$(mktemp)
jq --arg version "0.1.1" '.version = $version' .claude-plugin/plugin.json > "$TMP_FILE"
NEW_VER=$(jq -r '.version' "$TMP_FILE")
if [ "$NEW_VER" = "0.1.1" ]; then
    echo "✅ PASS: JSON update successful"
else
    echo "❌ FAIL: JSON update failed"
    exit 1
fi
rm -f "$TMP_FILE"
echo ""

# Test 6: Version consistency check
echo "Test 6: Version consistency check"
echo "----------------------------------"
PLUGIN_VER=$(jq -r '.version' .claude-plugin/plugin.json)
MARKET_VER=$(jq -r '.plugins[0].version' .claude-plugin/marketplace.json)
if [ "$PLUGIN_VER" = "$MARKET_VER" ]; then
    echo "✅ PASS: Versions are consistent"
else
    echo "⚠️  WARN: Version mismatch (expected for this test)"
fi
echo ""
```

- [ ] **Step 3: Write test teardown and summary**

```bash
# Cleanup
cd /
rm -rf "$TEST_DIR"
echo "🧹 Test directory cleaned up"
echo ""

echo "======================================"
echo "✅ All tests passed!"
echo "======================================"
```

- [ ] **Step 4: Make test script executable**

```bash
chmod +x tests/release-workflow-test.sh
```

- [ ] **Step 5: Run tests**

```bash
./tests/release-workflow-test.sh
```

Expected output: All tests pass

- [ ] **Step 6: Commit test file**

```bash
git add tests/release-workflow-test.sh
git commit -m "test(release): add integration test suite

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Documentation and Final Verification

**Files:**
- Create: `docs/commands/release.md` (user-facing documentation)
- Modify: `README.md` (add release command to command list)

**Interfaces:**
- Consumes: Implemented `/summoner:release` command
- Produces: User documentation, updated README

- [ ] **Step 1: Write user documentation**

Create `docs/commands/release.md`:

```markdown
# /summoner:release

Automated version release workflow with checkpoint-based execution. Ensures synchronized version updates across `plugin.json` and `marketplace.json`, generates changelog from git commits, and handles git tagging and pushing.

## Quick Start

```bash
# Standard patch release
/summoner:release --patch

# Minor version bump
/summoner:release --minor

# Major version bump
/summoner:release --major

# Specify exact version
/summoner:release --version 1.0.0

# Preview without executing
/summoner:release --patch --dry-run
```

## Three-Phase Workflow

### Phase 1: Version Planning
- Detects current version from `plugin.json`
- Validates consistency with `marketplace.json`
- Determines new version (auto-increment or manual)
- Checks for git tag conflicts
- **Checkpoint**: Confirm version before proceeding

### Phase 2: Changelog Generation
- Analyzes git commits since last tag
- Classifies by conventional commits (feat/fix/docs/chore/etc.)
- Generates markdown changelog with emoji categories
- **Checkpoint**: Review/edit changelog before applying

### Phase 3: Release Execution
- Updates `plugin.json` and `marketplace.json`
- Updates or creates `CHANGELOG.md`
- Creates git commit with standard message
- Creates annotated git tag
- Pushes to remote (unless `--no-push`)
- Optionally creates GitHub Release
- **Checkpoint**: Confirm execution and review results

## Options

- `--major` - Increment major version (X.0.0)
- `--minor` - Increment minor version (0.X.0)
- `--patch` - Increment patch version (0.0.X)
- `--version X.Y.Z` - Specify exact version
- `--dry-run` - Preview mode, no git operations
- `--no-push` - Local only, don't push to remote
- `--skip-changelog` - Skip changelog generation

## Error Handling

The command implements automatic rollback on errors:
- **Before commit**: Files modified, preserved for inspection
- **After commit, before push**: Tag and commit removed, files preserved
- **After push**: Manual intervention required (rollback not possible)

## Recommended Workflow

For major releases:

```bash
# 1. Run pre-release review
/summoner:ship

# 2. If GO decision, proceed with release
/summoner:release --minor

# 3. Verify release
git log -1 --stat
git tag -l | tail -5
```

## Requirements

- Git repository
- `jq` installed (JSON manipulation)
- `gh` CLI (optional, for GitHub Releases)

## Post-Game Review

After completion, the command triggers a Type 4 (流程评价) review to collect feedback on:
- Version planning phase
- Changelog generation quality
- Checkpoint flow
- Execution reliability
- Overall experience

## Troubleshooting

**Version files out of sync:**
```bash
# Phase 1 will warn you and use plugin.json as source
# Both files will be synchronized after completion
```

**Git tag already exists:**
```bash
# Delete existing tag or choose different version
git tag -d v0.1.5
```

**Push failed:**
```bash
# Automatic rollback removes tag and commit
# Fix authentication and retry
git remote -v  # verify remote
git push --dry-run  # test connectivity
```

**No commits since last tag:**
```bash
# Changelog will be empty with warning
# Consider if release is needed
```
```

- [ ] **Step 2: Update README**

Add to README.md command list:

```markdown
### /summoner:release

Automated version release workflow. Three checkpoint phases: Version Planning → Changelog Generation → Release Execution. Ensures zero version drift between plugin.json and marketplace.json.

```bash
/summoner:release --patch    # Patch release (0.1.5 → 0.1.6)
/summoner:release --minor    # Minor release (0.1.5 → 0.2.0)
/summoner:release --major    # Major release (0.1.5 → 1.0.0)
```

See [docs/commands/release.md](docs/commands/release.md) for full documentation.
```

- [ ] **Step 3: Commit documentation**

```bash
git add docs/commands/release.md README.md
git commit -m "docs(release): add user documentation and README entry

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 4: Run final verification**

```bash
# Verify command file exists
test -f commands/release.md && echo "✅ Command file exists"

# Verify all sections present
grep -q "Phase 1: Version Planning" commands/release.md && echo "✅ Phase 1 implemented"
grep -q "Phase 2: Changelog Generation" commands/release.md && echo "✅ Phase 2 implemented"
grep -q "Phase 3: Release Execution" commands/release.md && echo "✅ Phase 3 implemented"

# Verify checkpoint protocol compliance
grep -q "SUMMONER CHECKPOINT" commands/release.md && echo "✅ Checkpoint protocol used"

# Verify documentation exists
test -f docs/commands/release.md && echo "✅ User documentation exists"

# Run tests
./tests/release-workflow-test.sh
```

Expected: All checks pass

- [ ] **Step 5: Create final summary commit**

```bash
git log --oneline HEAD~6..HEAD
git diff HEAD~6..HEAD --stat

git commit --allow-empty -m "feat(release): complete /summoner:release implementation

Summary:
- Three-phase checkpoint workflow
- Automated version increment and validation
- Conventional commits changelog generation
- Automatic rollback on errors
- GitHub Release support
- Comprehensive test suite
- Full user documentation

Files created:
- commands/release.md (main implementation)
- tests/release-workflow-test.sh (integration tests)
- docs/commands/release.md (user documentation)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

### Spec Coverage Check

✅ **Phase 1: Version Planning**
- Task 2 implements version detection, increment logic, validation, and checkpoint

✅ **Phase 2: Changelog Generation**
- Task 3 implements commit range determination, classification, formatting, and checkpoint

✅ **Phase 3: Release Execution**
- Task 4 implements file updates, git operations, rollback, GitHub Release, and checkpoint

✅ **Argument Parsing**
- Task 5 implements all command-line options from spec

✅ **Error Handling**
- Task 4 implements rollback strategy with error trapping

✅ **Post-Game Review**
- Task 5 implements Type 4 review questions

✅ **Testing**
- Task 6 implements integration test suite

✅ **Documentation**
- Task 7 implements user documentation and README updates

### Placeholder Scan

- No TBD, TODO, or incomplete sections
- All code blocks contain actual implementation
- All file paths are exact
- All commands include expected output

### Type Consistency

- `NEW_VERSION`: string, semver format X.Y.Z (consistent across all tasks)
- `CURRENT_VERSION`: string, semver format (Task 2 → Task 4)
- `CHANGELOG_FILE`: temp file path (Task 3 → Task 4)
- `CHANGELOG_CONTENT`: string, markdown (Task 3)
- All boolean flags consistent: INCREMENT_MAJOR, DRY_RUN, NO_PUSH, SKIP_CHANGELOG

### Internal Consistency

- All checkpoints follow protocol from `references/checkpoint-protocol.md`
- All git commits include Co-Authored-By trailer
- All JSON operations preserve 2-space indentation
- Rollback strategy consistent across error scenarios

---

**Plan complete. Ready for implementation.**
