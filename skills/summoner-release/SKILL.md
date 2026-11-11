---
name: summoner-release
description: Version release automation with checkpoint-based workflow — version planning, changelog generation, release execution. Keeps ALL THREE version manifests synchronized (root plugin.json, .claude-plugin/plugin.json, .claude-plugin/marketplace.json). Use when user invokes /summoner:release.
when_to_use:
  - User invokes /summoner:release
  - User says "发版", "publish 0.x.y", or wants version bump + manifests/changelog/tag sync
allowed-tools:
  - Bash
  - Read
  - Edit
  - Write
dependencies:
  - jq (JSON processor)
  - git
  - gh (optional)
---

# Release Skill

Automates the complete version release workflow with three checkpoint phases.

## Purpose

Eliminates version drift between the three manifests (`plugin.json`, `.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json`): auto-changelog, checkpoint safety, rollback, summoner-ctx persistence.

## Route and manifest mapping (same single loop)

`/summoner:release` runs on the framework route **planning → changelog → publish**, fixed in `hooks/shared/workflow.go` — commands define topology; the manifest never does. `phases.release` in `summoner.yaml` maps the CAPABILITY: a project provider replaces this skill (the default framework implementation); no entry → this skill, disclosed「未配置项目专属能力」. User gates, trace, budgets and post-game run through the same single-loop reducer as every workflow — no second execution chain (`references/checkpoint-protocol.md`).

## Implementation Guide

### Pre-Flight Validation

Verify before any phase:

```bash
# 1-2. git repo + jq must exist
git rev-parse --git-dir >/dev/null 2>&1 || { echo "❌ Not a git repository"; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "❌ jq not installed. Install: brew install jq"; exit 1; }

# 3. Uncommitted changes: warn only, preserve them
if [ -n "$(git status --porcelain)" ]; then
    echo "⚠️  Uncommitted changes (they will be preserved)."
    read -p "Continue? [y/N]: " response
    [[ ! "$response" =~ ^[Yy]$ ]] && exit 0
fi
```

### Argument Parsing

Init `INCREMENT_MAJOR/MINOR/PATCH=false VERSION_ARG="" DRY_RUN=false NO_PUSH=false SKIP_CHANGELOG=false`, then parse the user's flags (`--major/--minor/--patch/--version X.Y.Z/--dry-run/--no-push/--skip-changelog`) into them.

### Phase 1: Version Planning

**START Block:** `⚡ SUMMONER START — Workflow=release Phase 1/3: Version Planning`（🎯 确定新版本号并验证版本文件一致性；🔧 Skill: none — 版本管理逻辑）

**Core Logic:**

```bash
# Read versions — ALL THREE manifests (root plugin.json once drifted to
# 0.1.5 when skipped; scripts/validate-repo.sh enforces the same invariant)
CURRENT_VERSION=$(jq -r '.version' .claude-plugin/plugin.json)
MARKETPLACE_VERSION=$(jq -r '.plugins[0].version' .claude-plugin/marketplace.json)
ROOT_VERSION=$(jq -r '.version' plugin.json)

if [ "$CURRENT_VERSION" != "$MARKETPLACE_VERSION" ] || [ "$CURRENT_VERSION" != "$ROOT_VERSION" ]; then
    echo "⚠️  Version mismatch ($CURRENT_VERSION / $MARKETPLACE_VERSION / $ROOT_VERSION) — using .claude-plugin/plugin.json as source of truth"
fi

IFS='.' read -r MAJOR MINOR PATCH <<< "$CURRENT_VERSION"

# Determine new version
if [ -n "$VERSION_ARG" ]; then NEW_VERSION="$VERSION_ARG"; VERSION_TYPE="custom"
elif [ "$INCREMENT_MAJOR" = "true" ]; then NEW_VERSION="$((MAJOR+1)).0.0"; VERSION_TYPE="major"
elif [ "$INCREMENT_MINOR" = "true" ]; then NEW_VERSION="$MAJOR.$((MINOR+1)).0"; VERSION_TYPE="minor"
elif [ "$INCREMENT_PATCH" = "true" ]; then NEW_VERSION="$MAJOR.$MINOR.$((PATCH+1))"; VERSION_TYPE="patch"
else
    # Interactive prompt: A=patch (default), B=minor, C=major, D=custom
    read -p "Increment? [A]patch / B minor / C major / D custom: " choice
    case "${choice:-A}" in
        A|a) NEW_VERSION="$MAJOR.$MINOR.$((PATCH+1))"; VERSION_TYPE="patch" ;;
        B|b) NEW_VERSION="$MAJOR.$((MINOR+1)).0"; VERSION_TYPE="minor" ;;
        C|c) NEW_VERSION="$((MAJOR+1)).0.0"; VERSION_TYPE="major" ;;
        D|d) read -p "Enter version: " NEW_VERSION; VERSION_TYPE="custom" ;;
        *) echo "Invalid choice"; exit 1 ;;
    esac
fi

# Validate semver, then refuse an existing tag
echo "$NEW_VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$' || { echo "❌ Invalid semver format: $NEW_VERSION"; exit 1; }
git tag -l "v$NEW_VERSION" | grep -q . && { echo "❌ Tag v$NEW_VERSION already exists"; exit 1; }

echo "✅ Version: $CURRENT_VERSION → $NEW_VERSION ($VERSION_TYPE)"
```

**CHECKPOINT Block:** render the canonical box from `references/checkpoint-protocol.md` §CHECKPOINT Block (5 mandatory fields, fixed `Next:` grammar — never inline a variant). Field values — 完成内容: 确定新版本号并验证无冲突；产物: Current: {CURRENT_VERSION}, New: {NEW_VERSION}, Type: {VERSION_TYPE}；发现: {consistency warning or None}.

### Phase 2: Changelog Generation

**START Block:** `⚡ SUMMONER START — Workflow=release Phase 2/3: Changelog Generation`（🎯 从 git commits 生成 changelog 并分类；🔧 Skill: none — changelog 生成逻辑）

**Core Logic:**

```bash
# Commit range: last tag..HEAD (or first commit..HEAD)
LAST_TAG=$(git describe --tags --abbrev=0 2>/dev/null)
COMMIT_RANGE="${LAST_TAG:-$(git rev-list --max-parents=0 HEAD)}..HEAD"
COMMITS=$(git log "$COMMIT_RANGE" --no-merges --pretty=format:"%s|%h")
COMMIT_COUNT=$(echo "$COMMITS" | wc -l | tr -d ' ')

# Classify by leading type; sections render only when non-empty
# (bash-3.2 safe: indexed arrays only).
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
add_bullet() {  # $1=title $2=bullet (appends to same title)
    local i
    for i in "${!SECTIONS[@]}"; do
        [ "${SECTIONS[$i]}" = "$1" ] && { BULLETS[$i]+=$'\n'"$2"; return; }
    done
    SECTIONS+=("$1"); BULLETS+=("$2")
}
while IFS='|' read -r message hash; do
    [[ -z "$message" ]] && continue
    add_bullet "$(section_for "${message%%[:,(]*}")" "- ${message} (${hash})"
done <<< "$COMMITS"
CURRENT_DATE=$(date +%Y-%m-%d)
CHANGELOG_CONTENT="## [$NEW_VERSION] - $CURRENT_DATE"
for i in "${!SECTIONS[@]}"; do
    CHANGELOG_CONTENT+=$'\n\n### '"${SECTIONS[$i]}"$'\n'"${BULLETS[$i]}"
done

echo "$CHANGELOG_CONTENT"
```

**CHECKPOINT Block:** canonical box per `references/checkpoint-protocol.md` §CHECKPOINT Block. Field values — 完成内容: 分析 {COMMIT_COUNT} 个 commits；产物: {category breakdown}；发现: {No commits warning or None}.

### Phase 3: Release Execution

**START Block:** `⚡ SUMMONER START — Workflow=release Phase 3/3: Release Execution`（🎯 更新版本文件、CHANGELOG 并执行 git 操作；🔧 Skill: none — 文件更新和 git 操作）

**Error Handling Setup:** init the three flags false, arm the trap (tag deleted, release commit reset, files preserved):

```bash
rollback_release() {
    local exit_code=$?
    [ "$RELEASE_STARTED" = "false" ] && return
    echo "❌ Error occurred (exit code: $exit_code); rolling back..."
    if [ "$TAG_CREATED" = "true" ] && git tag -l "v$NEW_VERSION" | grep -q .; then
        git tag -d "v$NEW_VERSION" 2>/dev/null && echo "   ✅ Deleted tag v$NEW_VERSION"
    fi
    if [ "$COMMIT_CREATED" = "true" ] && \
       git log -1 --pretty=%B 2>/dev/null | grep -q "release: v$NEW_VERSION"; then
        git reset HEAD~1 2>/dev/null && echo "   ✅ Reset commit"
    fi
    echo "   📁 Files preserved for inspection"
    exit $exit_code
}

trap rollback_release ERR
RELEASE_STARTED=true
```

**Core Logic:**

```bash
# Dry-run: preview, stop before mutation
if [ "$DRY_RUN" = "true" ]; then
    echo "🔍 DRY RUN - Would update files and create v$NEW_VERSION"
    trap - ERR
    exit 0
fi

# Update ALL THREE manifests (root plugin.json included)
update_version() {  # $1=file, $2=jq version path
    jq --arg v "$NEW_VERSION" "$2 = \$v" "$1" > /tmp/rel.json && \
        mv /tmp/rel.json "$1" && echo "✅ Updated $1"
}
update_version .claude-plugin/plugin.json '.version'
update_version .claude-plugin/marketplace.json '.plugins[0].version'
update_version plugin.json '.version'

# Update CHANGELOG.md (insert below header; create if absent)
if [ -f CHANGELOG.md ]; then
    NEW_CHANGELOG="$(head -n 1 CHANGELOG.md)"$'\n\n'"$CHANGELOG_CONTENT"$'\n'"$(tail -n +2 CHANGELOG.md)"
else
    NEW_CHANGELOG="# Changelog"$'\n\n'"$CHANGELOG_CONTENT"
fi
printf '%s\n' "$NEW_CHANGELOG" > CHANGELOG.md
echo "✅ Updated CHANGELOG.md"

# Git operations
git add plugin.json .claude-plugin/plugin.json .claude-plugin/marketplace.json CHANGELOG.md

git commit -m "release: v$NEW_VERSION

Co-Authored-By: Claude <noreply@anthropic.com>" && COMMIT_CREATED=true
COMMIT_HASH=$(git rev-parse --short HEAD) && echo "✅ Committed $COMMIT_HASH"

git tag -a "v$NEW_VERSION" -m "Release v$NEW_VERSION

$(echo "$CHANGELOG_CONTENT" | head -n 10)" && TAG_CREATED=true && echo "✅ Tagged v$NEW_VERSION"

# Push (unless --no-push); optional GitHub Release via gh
if [ "$NO_PUSH" != "true" ]; then
    BRANCH=$(git rev-parse --abbrev-ref HEAD)
    git push origin "$BRANCH" && git push origin "v$NEW_VERSION" && echo "✅ Pushed to remote"
fi
if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    read -p "📦 Create GitHub Release? [y/N]: " create_gh
    [[ "$create_gh" =~ ^[Yy]$ ]] && gh release create "v$NEW_VERSION" --title "v$NEW_VERSION" --notes "$CHANGELOG_CONTENT" && echo "✅ Created GitHub Release"
fi

trap - ERR
```

**CHECKPOINT Block:** canonical box per `references/checkpoint-protocol.md` §CHECKPOINT Block. Field values — 完成内容: 完成所有发布操作；产物: commit {COMMIT_HASH}, tag v{NEW_VERSION}, {push status}；发现: None.

✅ Release v{NEW_VERSION} completed! — {CURRENT_VERSION} → {NEW_VERSION}; files: plugin.json, .claude-plugin/plugin.json, .claude-plugin/marketplace.json, CHANGELOG.md（ALL THREE manifests must carry the new version — enforce via scripts/verify-release.sh）.

## 只读对账探针 — publish 阶段的必填能力项

publish（version writes, commit, tag, push）is the mutating phase: its capability list MUST declare a read-only reconciliation probe set BEFORE the release enters it — 无探针声明不得进变更阶段 (spec §5: 变更性阶段的能力清单必须声明探针). A declared probe is a manifest `check_registry` entry; read-only means an L1 framework op or a read-class Bash call (`references/manifest-spec.md`, trust ladder). Default set:

- **版本-已一致** — L1 `grep-count (path, pattern, count)` per manifest: each of the three manifests (paths in Phase 1) carries `"version": "$NEW_VERSION"` exactly once.
- **tag-已存在** — not L1-expressible (git query): one read-class Bash probe (`git tag -l "v$NEW_VERSION"`) per dangling intent at the OUTCOME_UNKNOWN recovery path.

Probes 对账 not 修复 — READ-ONLY, one probe per unknown, resolved only via `summoner:probe-submit` (`skills/probe-submit/SKILL.md`). 不一致 and 未生效 are honest bad-outcome verdicts routed to the recovery checkpoint (user decides CONTINUE / RECALL / STOP) — never a mutating fix, never silently passed, never auto-replayed. Pinned by `tests/fixtures/traces/valid-release-probe-*.jsonl` + `scorers/deterministic/recovery-check.sh`: exists+matches → `accepted`（已生效，直接恢复）；exists+mismatch → `failed`（部分落地/漂移）；missing → `failed`（未生效，重试是用户决定）.

### Integration with summoner-ctx

After a successful release (guarded by `command -v summoner-ctx`), save under the CURRENT project — `project.name` from `summoner.yaml`; no manifest → skip (never hardcode a project name):

```bash
PROJECT_NAME=$(jq -r '.project.name // empty' summoner.yaml 2>/dev/null || true)
if [ -n "$PROJECT_NAME" ]; then
    summoner-ctx save --project "$PROJECT_NAME" --workflow "release-v$NEW_VERSION" --phase execution --skill release --input <(printf 'Version: %s\nType: %s\nCommits: %s\nChangelog:\n%s' "$NEW_VERSION" "$VERSION_TYPE" "$COMMIT_COUNT" "$CHANGELOG_CONTENT")
fi
```

## Error Scenarios

| Error | Handling |
|-------|----------|
| Tag already exists | Abort in Phase 1 |
| No commits since last tag | Warning in Phase 2, continue |
| Push fails | Rollback commit and tag, preserve files |

## Post-Game Review

`done` / emergency `stop` land POST_GAME_PENDING — no release exemption, no separate closure. CLOSED comes only via the post-game writer: ONE AskUserQuestion call with the `[pg:q1]..[pg:q4]` questions (each text carries the run id), the journal at `.summoner/journal/<run_id>.md`, then `CompletePostGame` emits `post_game.completed` + `workflow.ended` once — CLOSED never reopens. A clean release is Type 4（流程评价）. Protocol: `references/post-game-review.md`.

## Testing

Scenarios: `--patch --dry-run`（预览不执行）/ `--minor`（真实发布）/ `--version 2.0.0`（自定义）/ Phase 3 中断（验证回滚）/ publish 崩溃后的对账探针三分支（fixtures 见上）.
