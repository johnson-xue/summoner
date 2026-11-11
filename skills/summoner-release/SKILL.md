---
name: summoner-release
description: Version release automation — version planning, changelog generation, release execution. Keeps the three version manifests synchronized (plugin.json, .claude-plugin/plugin.json, .claude-plugin/marketplace.json). Use when user invokes /summoner:release.
when_to_use:
  - User invokes /summoner:release
  - User says "发版", "publish 0.x.y", or wants version bump + manifests/changelog/tag sync
allowed-tools:
  - Bash
  - Read
  - Edit
  - Write
dependencies:
  - jq
  - git
  - gh (optional)
---

# Release Skill

Eliminates version drift between the three version manifests (paths in frontmatter): version planning, auto-changelog, checkpoint safety, rollback, summoner-ctx persistence.

## Route and manifest mapping

`/summoner:release` runs on the fixed framework route **planning → changelog → publish** (`hooks/shared/workflow.go`) — commands define topology; the manifest never does. `phases.release` in `summoner.yaml` maps the CAPABILITY: a project provider replaces this skill (default implementation); no entry → this skill, disclosed「未配置项目专属能力」. Gates, trace, budgets and post-game run through the same single-loop reducer — no second execution chain.

### Pre-Flight Validation

Verify first:

```bash
# git repo + jq must exist
git rev-parse --git-dir >/dev/null 2>&1 || { echo "❌ Not a git repository"; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "❌ jq not installed (brew install jq)"; exit 1; }

# uncommitted changes: warn only, preserve them
if [ -n "$(git status --porcelain)" ]; then
    echo "⚠️  Uncommitted changes (preserved)."
    read -p "Continue? [y/N]: " response
    [[ ! "$response" =~ ^[Yy]$ ]] && exit 0
fi
```

### Argument Parsing

Init `INCREMENT_MAJOR/MINOR/PATCH=false VERSION_ARG="" DRY_RUN=false NO_PUSH=false SKIP_CHANGELOG=false`, then parse the user's flags (`--major/--minor/--patch/--version X.Y.Z/--dry-run/--no-push/--skip-changelog`) into them.

### Phase 1: Version Planning

**START Block:** `⚡ SUMMONER START — Workflow=release Phase 1/3: Version Planning`（🎯 确定新版本号并验证三处版本一致；🔧 none）

**Core Logic:**

```bash
# Read versions — ALL THREE manifests (a root plugin.json drift once shipped; scripts/validate-repo.sh enforces this)
CURRENT_VERSION=$(jq -r '.version' .claude-plugin/plugin.json)
MARKETPLACE_VERSION=$(jq -r '.plugins[0].version' .claude-plugin/marketplace.json)
ROOT_VERSION=$(jq -r '.version' plugin.json)

if [ "$CURRENT_VERSION" != "$MARKETPLACE_VERSION" ] || [ "$CURRENT_VERSION" != "$ROOT_VERSION" ]; then
    echo "⚠️  Version mismatch ($CURRENT_VERSION / $MARKETPLACE_VERSION / $ROOT_VERSION) — using .claude-plugin/plugin.json as source of truth"
fi

IFS='.' read -r MAJOR MINOR PATCH <<< "$CURRENT_VERSION"

# New version
if [ -n "$VERSION_ARG" ]; then NEW_VERSION="$VERSION_ARG"; VERSION_TYPE="custom"
elif [ "$INCREMENT_MAJOR" = "true" ]; then NEW_VERSION="$((MAJOR+1)).0.0"; VERSION_TYPE="major"
elif [ "$INCREMENT_MINOR" = "true" ]; then NEW_VERSION="$MAJOR.$((MINOR+1)).0"; VERSION_TYPE="minor"
elif [ "$INCREMENT_PATCH" = "true" ]; then NEW_VERSION="$MAJOR.$MINOR.$((PATCH+1))"; VERSION_TYPE="patch"
else
    # A=patch (default), B=minor, C=major, D=custom
    read -p "Increment? [A]patch / B minor / C major / D custom: " choice
    case "${choice:-A}" in
        A|a) NEW_VERSION="$MAJOR.$MINOR.$((PATCH+1))"; VERSION_TYPE="patch" ;;
        B|b) NEW_VERSION="$MAJOR.$((MINOR+1)).0"; VERSION_TYPE="minor" ;;
        C|c) NEW_VERSION="$((MAJOR+1)).0.0"; VERSION_TYPE="major" ;;
        D|d) read -p "Enter version: " NEW_VERSION; VERSION_TYPE="custom" ;;
        *) echo "Invalid choice"; exit 1 ;;
    esac
fi

# Validate semver; refuse existing tag
echo "$NEW_VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$' || { echo "❌ Invalid semver format: $NEW_VERSION"; exit 1; }
git tag -l "v$NEW_VERSION" | grep -q . && { echo "❌ Tag v$NEW_VERSION already exists"; exit 1; }

echo "✅ Version: $CURRENT_VERSION → $NEW_VERSION ($VERSION_TYPE)"
```

**CHECKPOINT Block:** render the canonical box from `references/checkpoint-protocol.md` §CHECKPOINT Block (5 mandatory fields, fixed `Next:` grammar — never inline a variant). Field values — 完成内容: 确定新版本号并验证无冲突；产物: Current: {CURRENT_VERSION}, New: {NEW_VERSION}, Type: {VERSION_TYPE}；发现: {consistency warning or None}.

### Phase 2: Changelog Generation

**START Block:** `⚡ SUMMONER START — Workflow=release Phase 2/3: Changelog Generation`（🎯 从 git commits 生成并分类 changelog；🔧 none）

**Core Logic:**

```bash
# Range: last tag..HEAD (else first commit..HEAD)
LAST_TAG=$(git describe --tags --abbrev=0 2>/dev/null)
COMMIT_RANGE="${LAST_TAG:-$(git rev-list --max-parents=0 HEAD)}..HEAD"
COMMITS=$(git log "$COMMIT_RANGE" --no-merges --pretty=format:"%s|%h")
COMMIT_COUNT=$(echo "$COMMITS" | wc -l | tr -d ' ')

# Classify by leading type or emoji; non-empty sections only (bash-3.2:
# indexed arrays).
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
        [ "${SECTIONS[$i]}" = "$1" ] && { BULLETS[$i]+=$'\n'"$2"; return; }
    done
    SECTIONS+=("$1"); BULLETS+=("$2")
}
while IFS='|' read -r message hash; do
    [[ -z "$message" ]] && continue
    case "$message" in
        "🔒"*) type_=security ;; "⚡"*) type_=perf ;;
        "♻️"*) type_=refactor ;; "✅"*) type_=test ;;
        *) type_="${message%%[:,(]*}" ;;
    esac
    add_bullet "$(section_for "$type_")" "- ${message} (${hash})"
done <<< "$COMMITS"
CURRENT_DATE=$(date +%Y-%m-%d)
CHANGELOG_CONTENT="## [$NEW_VERSION] - $CURRENT_DATE"
for i in "${!SECTIONS[@]}"; do
    CHANGELOG_CONTENT+=$'\n\n### '"${SECTIONS[$i]}"$'\n'"${BULLETS[$i]}"
done

echo "$CHANGELOG_CONTENT"
```

**CHECKPOINT Block:** canonical box (Phase 1 spec). Field values — 完成内容: 分析 {COMMIT_COUNT} 个 commits；产物: {category breakdown}；发现: {No commits warning or None}.

### Phase 3: Release Execution

**START Block:** `⚡ SUMMONER START — Workflow=release Phase 3/3: Release Execution`（🎯 更新版本文件、CHANGELOG 并执行 git 操作；🔧 none）

**Error Handling Setup:** init flags false, arm the trap (delete tag, reset commit, preserve files):

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

# ALL THREE manifests (root plugin.json included)
update_version() {  # $1=file, $2=jq version path
    jq --arg v "$NEW_VERSION" "$2 = \$v" "$1" > /tmp/rel.json && \
        mv /tmp/rel.json "$1" && echo "✅ Updated $1"
}
update_version .claude-plugin/plugin.json '.version'
update_version .claude-plugin/marketplace.json '.plugins[0].version'
update_version plugin.json '.version'

# CHANGELOG.md: insert below header (create if absent)
if [ -f CHANGELOG.md ]; then
    NEW_CHANGELOG="$(head -n 1 CHANGELOG.md)"$'\n\n'"$CHANGELOG_CONTENT"$'\n'"$(tail -n +2 CHANGELOG.md)"
else
    NEW_CHANGELOG="# Changelog"$'\n\n'"$CHANGELOG_CONTENT"
fi
printf '%s\n' "$NEW_CHANGELOG" > CHANGELOG.md
echo "✅ Updated CHANGELOG.md"

# Git operations
git add plugin.json .claude-plugin/plugin.json .claude-plugin/marketplace.json CHANGELOG.md

# Mutating calls are STANDALONE — failures left of && never fire the ERR trap.
git commit -m "release: v$NEW_VERSION

Co-Authored-By: Claude <noreply@anthropic.com>"
COMMIT_CREATED=true
COMMIT_HASH=$(git rev-parse --short HEAD)
echo "✅ Committed $COMMIT_HASH"

git tag -a "v$NEW_VERSION" -m "Release v$NEW_VERSION

$(echo "$CHANGELOG_CONTENT" | head -n 10)"
TAG_CREATED=true
echo "✅ Tagged v$NEW_VERSION"

# Push unless --no-push — standalone, trap fires on failure
if [ "$NO_PUSH" != "true" ]; then
    BRANCH=$(git rev-parse --abbrev-ref HEAD)
    git push origin "$BRANCH"
    git push origin "v$NEW_VERSION"
    echo "✅ Pushed to remote"
fi
if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    read -p "📦 Create GitHub Release? [y/N]: " create_gh
    if [[ "$create_gh" =~ ^[Yy]$ ]]; then
        gh release create "v$NEW_VERSION" --title "v$NEW_VERSION" --notes "$CHANGELOG_CONTENT"
        echo "✅ Created GitHub Release"
    fi
fi

trap - ERR
```

**CHECKPOINT Block:** canonical box (Phase 1 spec). Field values — 完成内容: 完成所有发布操作；产物: commit {COMMIT_HASH}, tag v{NEW_VERSION}, {push status}；发现: None.

✅ Release v{NEW_VERSION} completed — {CURRENT_VERSION} → {NEW_VERSION} (enforce via scripts/verify-release.sh).

## 只读对账探针 — publish 阶段的必填能力项

publish（version writes, commit, tag, push）is the mutating phase: its capability list MUST declare a read-only reconciliation probe set BEFORE the release enters it — 无探针声明不得进变更阶段 (spec §5: 变更性阶段的能力清单必须声明探针). A declared probe is a manifest `check_registry` entry whose definition is a read-only predicate (L1 framework op) or a read-class Bash line (`references/manifest-spec.md`). The OBSERVED evidence `summoner:probe-submit` accepts is strictly a read-class Bash tool id — run the probe as the Bash equivalent and submit that id, never an in-process L1 result. Default set:

- **版本-已一致** — L1 `grep-count (path, pattern, count)` per manifest: each (paths in Phase 1) carries `"version": "$NEW_VERSION"` once.
- **tag-已存在** — not L1-expressible (git query): one read-class Bash probe (`git tag -l "v$NEW_VERSION"`) per dangling intent at OUTCOME_UNKNOWN recovery.

Probes 对账 not 修复 — READ-ONLY, one probe per unknown, resolved only via `summoner:probe-submit` (`skills/probe-submit/SKILL.md`). 不一致 and 未生效 are honest bad-outcome verdicts routed to the recovery checkpoint (user decides CONTINUE / RECALL / STOP) — never a mutating fix, never silently passed, never auto-replayed. Pinned by `tests/fixtures/traces/valid-release-probe-*.jsonl` + `scorers/deterministic/recovery-check.sh`: exists+matches → `accepted`; exists+mismatch → `failed`; missing → `failed`（未生效 — 重试是用户决定）.

### Integration with summoner-ctx

`PROJECT_NAME` is the manifest-resolved `project.name` — resolve it YOURSELF from this run's `summoner.yaml` (Read — already read at routing) and export it shell-quoted as ONE value; the snippet CONSUMES it, never parses YAML (jq is JSON-only), never runs a placeholder or another project's name:

```bash
if ! command -v summoner-ctx >/dev/null 2>&1; then
    echo "ℹ️ summoner-ctx not installed — skipping release persistence"
elif [ ! -f summoner.yaml ]; then
    echo "⚠️ manifest (summoner.yaml) missing — skipping summoner-ctx save"
elif [ -z "${PROJECT_NAME:-}" ]; then
    echo "⚠️ project.name unresolved in summoner.yaml — skipping summoner-ctx save"
elif summoner-ctx save --project "$PROJECT_NAME" --workflow "release-v$NEW_VERSION" --phase execution --skill release --input <(printf 'Version: %s\nType: %s\nCommits: %s\nChangelog:\n%s' "$NEW_VERSION" "$VERSION_TYPE" "$COMMIT_COUNT" "$CHANGELOG_CONTENT"); then
    echo "✅ Saved release context"
else
    echo "⚠️ summoner-ctx save failed — the release itself is unaffected"
fi
```

Persistence is optional — every path warns and continues; it never fails the release.

## Error Scenarios

Handled inline above: tag exists → Phase 1 abort; no new commits → Phase 2 warning; commit/tag/push failure → ERR-trap rollback.

## Post-Game Review

`done` / emergency `stop` land POST_GAME_PENDING — no release exemption, no separate closure. CLOSED comes only via the post-game writer: ONE AskUserQuestion call with the `[pg:q1]..[pg:q4]` questions (text carries the run id), the journal at `.summoner/journal/<run_id>.md`, then `CompletePostGame` emits `post_game.completed` + `workflow.ended` once — CLOSED never reopens. A clean release is Type 4（流程评价）. Protocol: `references/post-game-review.md`.

## Testing

Scenarios: `--patch --dry-run` / `--minor`（真实发布）/ `--version 2.0.0` / Phase 3 中断（回滚）/ publish 崩溃后对账探针三分支.
