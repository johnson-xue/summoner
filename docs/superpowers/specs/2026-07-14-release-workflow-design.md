# Release Workflow Design - /summoner:release

**Date:** 2026-07-14  
**Status:** Design Approved  
**Author:** Claude Opus 4.8 + User

---

## Problem Statement

Summoner's version release process is error-prone. The current manual workflow frequently misses updating `.claude-plugin/marketplace.json`, leading to version inconsistencies:

- `plugin.json` gets updated to v0.1.5
- `marketplace.json` remains at v0.1.4
- Git tag is created, but marketplace registration is incomplete

We need a standardized, checkpoint-based release skill that ensures all version-related files are synchronized.

---

## Goals

1. **Zero version drift** - All version files (plugin.json, marketplace.json) stay synchronized
2. **Automated changelog** - Generate changelog from git commits using conventional commits
3. **Safe execution** - Three checkpoints with rollback capability
4. **Flexible versioning** - Support auto-increment (major/minor/patch) or manual specification
5. **Integration ready** - Works standalone or after `/summoner:ship` approval

---

## Design Overview

### Command Signature

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

### Three-Phase Workflow

```
Phase 1: Version Planning
  ├── Read current version from plugin.json
  ├── Validate marketplace.json consistency
  ├── Determine new version (auto-increment or user input)
  ├── Check git tag conflicts
  └── CHECKPOINT 1: Confirm new version

Phase 2: Changelog Generation
  ├── Fetch commits since last tag
  ├── Classify by conventional commits (feat/fix/docs/chore)
  ├── Generate markdown changelog
  └── CHECKPOINT 2: Confirm/edit changelog

Phase 3: Release Execution
  ├── Update plugin.json version
  ├── Update marketplace.json version
  ├── Update/create CHANGELOG.md
  ├── Git commit: "release: vX.Y.Z"
  ├── Git tag: vX.Y.Z (annotated)
  ├── Git push (commit + tag)
  ├── Optional: Create GitHub Release
  └── CHECKPOINT 3: Confirm execution + GitHub Release option
```

---

## Component Design

### 1. Version Management

#### Version Detection Strategy

```
1. Read .claude-plugin/plugin.json → extract version field
2. Read .claude-plugin/marketplace.json → extract plugins[0].version
3. Compare:
   - IF consistent: proceed with current version
   - IF inconsistent: WARN user, use plugin.json as source of truth
```

#### Version Increment Logic

```python
# Pseudo-code
if args.version:
    new_version = args.version
elif args.major:
    new_version = increment_major(current)  # 0.1.5 → 1.0.0
elif args.minor:
    new_version = increment_minor(current)  # 0.1.5 → 0.2.0
elif args.patch:
    new_version = increment_patch(current)  # 0.1.5 → 0.1.6
else:
    # Interactive mode
    prompt_user_choice([
        "A. Patch (0.1.5 → 0.1.6)",
        "B. Minor (0.1.5 → 0.2.0)",
        "C. Major (0.1.5 → 1.0.0)",
        "D. Custom (enter manually)"
    ])
```

#### Version Validation

- Must match semver format: `X.Y.Z` (all integers)
- New version must be greater than current version
- Git tag `vX.Y.Z` must not already exist

---

### 2. Changelog Generation

#### Commit Classification

Based on conventional commits spec:

| Prefix | Category | Emoji | Example |
|--------|----------|-------|---------|
| `feat:` | Features | ✨ | `feat: add user authentication` |
| `fix:` | Bug Fixes | 🐛 | `fix: resolve login timeout` |
| `perf:` | Performance | ⚡ | `perf: optimize query speed` |
| `refactor:` | Refactoring | ♻️ | `refactor: simplify error handling` |
| `docs:` | Documentation | 📝 | `docs: update API guide` |
| `test:` | Tests | ✅ | `test: add unit tests for parser` |
| `chore:` | Chores | 🔧 | `chore: update dependencies` |
| `security:` or `🔒` | Security | 🔒 | `security: fix XSS vulnerability` |
| (other) | Other Changes | 📦 | `merge: PR #42` |

#### Commit Range Determination

```bash
# Get last tag
last_tag=$(git describe --tags --abbrev=0 2>/dev/null)

if [ -n "$last_tag" ]; then
    # From last tag to HEAD
    commit_range="$last_tag..HEAD"
else
    # From first commit to HEAD (initial release)
    commit_range=$(git rev-list --max-parents=0 HEAD)..HEAD
fi

# Get commits (exclude merge commits)
git log $commit_range --no-merges --pretty=format:"%s (%h)"
```

#### Changelog Format

```markdown
## [X.Y.Z] - YYYY-MM-DD

### ✨ Features
- Add user authentication system (a1b2c3d)
- Implement real-time notifications (e4f5g6h)

### 🐛 Bug Fixes
- Fix login timeout issue (i7j8k9l)
- Resolve memory leak in cache (m0n1o2p)

### 🔒 Security
- Patch XSS vulnerability in input validation (q3r4s5t)

### 📝 Documentation
- Update API documentation (u6v7w8x)

### 🔧 Chores
- Update dependencies to latest versions (y9z0a1b)
```

#### Edge Cases

- **No commits since last tag**: Generate empty changelog with warning
- **No conventional commit prefix**: Classify under "Other Changes"
- **Multiple categories in one commit**: Use first matching prefix

---

### 3. Checkpoint Protocol

Following `references/checkpoint-protocol.md`:

#### CHECKPOINT 1: Version Planning

```markdown
---
## 🎯 SUMMONER CHECKPOINT

**Workflow:** release
**Phase:** 1 - Version Planning
**Status:** ⏸️ PAUSED

### 📊 Phase Summary

**Current Version:** 0.1.5
**New Version:** 0.1.6
**Version Type:** Patch

**File Status:**
- ✅ plugin.json: 0.1.5
- ⚠️  marketplace.json: 0.1.4 (will be updated)
- ✅ Git tag v0.1.6: not exists (safe to create)

### 🎮 Actions
1. ✅ **continue** - Proceed to changelog generation
2. ⏭️ **skip** - Skip to execution (no changelog)
3. 🔄 **recall** - Re-determine version
4. ❌ **stop** - Cancel release

### 📝 Feedback
(Optional: suggest version change)

---
```

#### CHECKPOINT 2: Changelog Generation

```markdown
---
## 🎯 SUMMONER CHECKPOINT

**Workflow:** release
**Phase:** 2 - Changelog Generation
**Status:** ⏸️ PAUSED

### 📊 Phase Summary

**Commit Range:** v0.1.5..HEAD (15 commits)
**Categories:**
- ✨ 5 features
- 🐛 3 bug fixes
- 📝 2 documentation
- 🔧 5 chores

**Generated Changelog Preview:**
```
## [0.1.6] - 2026-07-14

### ✨ Features
- Add context persistence with SQLite (a1b2c3d)
- Implement LLM fallback strategy (e4f5g6h)
...
```

### 🎮 Actions
1. ✅ **continue** - Accept changelog and proceed
2. ⏭️ **skip** - Skip changelog update
3. 🔄 **recall** - Regenerate changelog
4. ❌ **stop** - Cancel release

### 📝 Feedback
(Optional: edit changelog content)

---
```

#### CHECKPOINT 3: Release Execution

```markdown
---
## 🎯 SUMMONER CHECKPOINT

**Workflow:** release
**Phase:** 3 - Release Execution
**Status:** ⏸️ PAUSED

### 📊 Phase Summary

**About to Execute:**
- ✏️  Update `.claude-plugin/plugin.json` → 0.1.6
- ✏️  Update `.claude-plugin/marketplace.json` → 0.1.6
- 📝 Update `CHANGELOG.md` (prepend new section)
- 💾 Git commit: "release: v0.1.6"
- 🏷️  Git tag: v0.1.6 (annotated)
- 🚀 Git push: origin master + tag
- 📦 GitHub Release: [ASK USER]

**GitHub CLI Status:** ✅ Installed and authenticated

### 🎮 Actions
1. ✅ **continue** - Execute all operations
2. ⏭️ **skip** - Cancel execution
3. ❌ **stop** - Cancel release

### 📝 Additional Options
- Create GitHub Release? (yes/no)

---
```

---

### 4. File Operations

#### plugin.json Update

```javascript
// Read
const plugin = JSON.parse(fs.readFileSync('.claude-plugin/plugin.json'))

// Validate
if (!plugin.version) throw new Error('Missing version field')

// Update
plugin.version = new_version

// Write (preserve formatting: 2-space indent)
fs.writeFileSync('.claude-plugin/plugin.json', 
  JSON.stringify(plugin, null, 2) + '\n')
```

#### marketplace.json Update

```javascript
// Read
const marketplace = JSON.parse(fs.readFileSync('.claude-plugin/marketplace.json'))

// Validate
if (!marketplace.plugins || !Array.isArray(marketplace.plugins)) {
  throw new Error('Invalid marketplace.json structure')
}

// Update first plugin entry
marketplace.plugins[0].version = new_version

// Write (preserve formatting)
fs.writeFileSync('.claude-plugin/marketplace.json',
  JSON.stringify(marketplace, null, 2) + '\n')
```

#### CHANGELOG.md Update

```bash
# If file exists
if [ -f CHANGELOG.md ]; then
    # Insert new section after "# Changelog" header
    # Preserve existing content
    temp_file=$(mktemp)
    head -n 1 CHANGELOG.md > $temp_file  # Keep header
    echo "" >> $temp_file
    cat new_changelog_section.md >> $temp_file
    echo "" >> $temp_file
    tail -n +2 CHANGELOG.md >> $temp_file  # Keep old content
    mv $temp_file CHANGELOG.md
else
    # Create new file
    echo "# Changelog" > CHANGELOG.md
    echo "" >> CHANGELOG.md
    cat new_changelog_section.md >> CHANGELOG.md
fi
```

---

### 5. Git Operations

#### Operation Sequence

```bash
#!/bin/bash
set -e  # Exit on error

# 1. Stage files
git add .claude-plugin/plugin.json
git add .claude-plugin/marketplace.json
git add CHANGELOG.md

# 2. Commit
git commit -m "release: v${NEW_VERSION}

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"

# 3. Create annotated tag
changelog_preview=$(head -n 5 CHANGELOG.md | tail -n 3)
git tag -a "v${NEW_VERSION}" -m "Release v${NEW_VERSION}

${changelog_preview}"

# 4. Push (only if not --no-push)
if [ "$NO_PUSH" != "true" ]; then
    current_branch=$(git rev-parse --abbrev-ref HEAD)
    git push origin "$current_branch"
    git push origin "v${NEW_VERSION}"
fi

# 5. GitHub Release (optional)
if [ "$CREATE_GITHUB_RELEASE" = "true" ]; then
    if command -v gh &> /dev/null; then
        gh release create "v${NEW_VERSION}" \
            --title "v${NEW_VERSION}" \
            --notes-file <(extract_version_changelog CHANGELOG.md "$NEW_VERSION")
    else
        echo "⚠️  gh CLI not found, skipping GitHub Release"
    fi
fi
```

#### Rollback Strategy

| Stage | Completed Operations | Rollback Actions |
|-------|---------------------|------------------|
| **Pre-commit** | File modifications | Keep files (user can inspect) |
| **Post-commit, pre-tag** | Commit + files | `git reset HEAD~1`, keep files |
| **Post-tag, pre-push** | Commit + tag + files | `git tag -d vX.Y.Z`, `git reset HEAD~1`, keep files |
| **Post-push** | All operations | ⚠️ **No auto-rollback** - Manual intervention required |

#### Error Handling

```bash
# Trap errors and rollback
trap 'rollback_on_error $?' ERR

rollback_on_error() {
    exit_code=$1
    echo "❌ Error occurred (exit code: $exit_code)"
    
    # Check if tag exists locally
    if git tag -l "v${NEW_VERSION}" | grep -q .; then
        echo "🔄 Deleting local tag v${NEW_VERSION}"
        git tag -d "v${NEW_VERSION}"
    fi
    
    # Check if commit was made (compare HEAD with previous)
    if [ "$(git log -1 --pretty=%B)" = "release: v${NEW_VERSION}" ]; then
        echo "🔄 Resetting last commit"
        git reset HEAD~1
    fi
    
    echo "📁 Modified files preserved for inspection"
    echo "   Use 'git status' to see changes"
    echo "   Use 'git restore <file>' to discard changes"
    
    exit $exit_code
}
```

---

### 6. Error Scenarios

| Scenario | Detection | Handling |
|----------|-----------|----------|
| **Git tag already exists** | Phase 1: `git tag -l vX.Y.Z` | Block execution, suggest different version |
| **Version files inconsistent** | Phase 1: Compare plugin.json vs marketplace.json | Warn user, proceed with plugin.json as source |
| **No commits since last tag** | Phase 2: `git log --oneline $range` empty | Warn user, generate empty changelog section |
| **Not a git repository** | Pre-flight: `git rev-parse --git-dir` fails | Abort with error message |
| **Uncommitted changes** | Pre-flight: `git status --porcelain` not empty | Warn user, ask to commit/stash first |
| **Push fails (network)** | Phase 3: `git push` returns non-zero | Rollback tag and commit, show error |
| **Push fails (auth)** | Phase 3: Authentication error | Rollback tag and commit, guide user to fix auth |
| **gh CLI not found** | Phase 3: `command -v gh` fails | Skip GitHub Release, inform user |
| **Invalid semver** | Phase 1: Version regex mismatch | Reject version, ask for valid format |
| **New version ≤ current** | Phase 1: Version comparison | Reject version, must be greater |

---

### 7. Post-Game Review

Trigger **Type 4 (流程评价)** review at workflow end.

#### Review Questions

```markdown
## 📝 Release Workflow Review

### 1. Version Planning (Phase 1)
- Was version increment logic clear and predictable?
- Did auto-increment (--patch/--minor/--major) work as expected?
- Any issues with version conflict detection?

**Rating (1-5):** ___
**Improvement suggestions:** ___

### 2. Changelog Generation (Phase 2)
- Were commits classified accurately?
- Was the changelog format clear and readable?
- Did you need to manually edit the changelog?

**Rating (1-5):** ___
**Improvement suggestions:** ___

### 3. Checkpoint Flow
- Were 3 checkpoints too many / too few / just right?
- Which checkpoint could be combined or removed?
- Was the information at each checkpoint sufficient?

**Rating (1-5):** ___
**Improvement suggestions:** ___

### 4. Execution Reliability (Phase 3)
- Did all file updates complete successfully?
- Did git operations (commit/tag/push) work smoothly?
- Any unexpected errors or rollback needed?

**Rating (1-5):** ___
**Issues encountered:** ___

### 5. Overall Experience
- Would you use this skill for future releases?
- What's the most annoying part of the workflow?
- Feature requests for next iteration?

**Overall Rating (1-5):** ___
**Key improvement:** ___
```

---

## Integration Points

### With /summoner:ship

**Recommended workflow for major releases:**

```bash
# 1. Pre-release review
/summoner:ship

# 2. If GO decision, proceed with release
/summoner:release --minor

# 3. Post-release verification
git log -1 --stat
git tag -l | tail -5
```

**The two skills are independent:**
- `/summoner:ship` focuses on **quality gates** (code review, security, tests)
- `/summoner:release` focuses on **version management** (files, changelog, git)

### With summoner-ctx Tool

Optional: Save release metadata to context database

```bash
# After successful release
summoner-ctx save \
  --project summoner \
  --workflow "release-v${NEW_VERSION}" \
  --phase "execution" \
  --skill "release" \
  --input <(cat <<EOF
Version: ${NEW_VERSION}
Changelog: $(extract_version_changelog CHANGELOG.md "$NEW_VERSION")
Commits: $(git log --oneline v${OLD_VERSION}..v${NEW_VERSION})
EOF
)
```

---

## Usage Examples

### Example 1: Standard Patch Release

```bash
$ /summoner:release --patch

# Output:
Phase 1: Version Planning
  Current: 0.1.5 → New: 0.1.6
  CHECKPOINT 1 → User: continue

Phase 2: Changelog Generation
  15 commits analyzed
  CHECKPOINT 2 → User: continue

Phase 3: Release Execution
  ✅ Updated plugin.json
  ✅ Updated marketplace.json
  ✅ Updated CHANGELOG.md
  ✅ Created commit c4d5e6f
  ✅ Created tag v0.1.6
  ✅ Pushed to origin/master
  CHECKPOINT 3 → User: continue (yes to GitHub Release)
  ✅ Created GitHub Release

✅ Release v0.1.6 completed successfully!
```

### Example 2: Major Version with Manual Changelog Edit

```bash
$ /summoner:release --major

# Phase 1
Current: 0.9.5 → New: 1.0.0
CHECKPOINT 1 → continue

# Phase 2
Generated changelog shows 50 commits...
CHECKPOINT 2 → recall (user wants to add breaking changes section)

# Regenerate with custom intro
CHECKPOINT 2 → continue

# Phase 3
Execute all operations...
CHECKPOINT 3 → continue (no GitHub Release)

✅ Release v1.0.0 completed!
```

### Example 3: Dry Run for Testing

```bash
$ /summoner:release --version 0.2.0 --dry-run

# Output:
🔍 DRY RUN MODE - No git operations will be executed

Phase 1: Version Planning
  0.1.5 → 0.2.0 ✅

Phase 2: Changelog Generation
  [Preview shown]

Phase 3: Preview Operations
  Would update: plugin.json, marketplace.json, CHANGELOG.md
  Would commit: "release: v0.2.0"
  Would tag: v0.2.0
  Would push: origin/master + tag

🔍 Dry run complete. Use without --dry-run to execute.
```

### Example 4: Rollback Scenario

```bash
$ /summoner:release --minor

# Phase 1 & 2 complete...
# Phase 3 starts...
  ✅ Updated files
  ✅ Created commit
  ✅ Created tag
  ❌ Push failed: Permission denied (publickey)

# Auto-rollback triggered
🔄 Rolling back...
  ✅ Deleted tag v0.2.0
  ✅ Reset commit
  📁 Files preserved in working directory

⚠️  Release incomplete. Fix git authentication and retry.
```

---

## Non-Goals

**What this skill does NOT do:**

1. **Build/compile artifacts** - No build step, assumes code is ready
2. **Run tests** - Use `/summoner:ship` for pre-release testing
3. **Deployment** - Only handles git/GitHub, not production deploy
4. **Branch management** - Assumes release from current branch
5. **Monorepo support** - Designed for single-package repos
6. **Release notes generation** - Only changelog from commits
7. **Dependency updates** - No automatic dependency bumping

---

## Future Enhancements (Not in v1)

**Potential additions for later iterations:**

- **Smart version inference** - Analyze commits to suggest major/minor/patch
- **Breaking change detection** - Scan for BREAKING CHANGE footers
- **Multi-package support** - Handle monorepo with multiple version files
- **Custom changelog templates** - User-defined changelog format
- **Release scheduling** - Schedule release for specific time
- **Automated testing trigger** - Optional pre-release test run
- **Rollback command** - `/summoner:rollback-release vX.Y.Z`
- **Release branch workflow** - Support release/X.Y.Z branching strategy

---

## Success Criteria

**The skill is successful if:**

1. ✅ **Zero version drift** - plugin.json and marketplace.json always match after release
2. ✅ **User confidence** - 3 checkpoints prevent accidental releases
3. ✅ **Error recovery** - Automatic rollback on failure prevents broken state
4. ✅ **Time savings** - Reduce release time from ~10 min to ~2 min
5. ✅ **Adoption** - Used for 100% of Summoner releases going forward

**Acceptance test:**

```bash
# Before: Version files out of sync
plugin.json:      0.1.5
marketplace.json: 0.1.4

# Run release skill
/summoner:release --patch

# After: Perfect sync
plugin.json:      0.1.6
marketplace.json: 0.1.6
Git tag:          v0.1.6
CHANGELOG.md:     Contains v0.1.6 section
Remote pushed:    ✅
```

---

## Implementation Notes

### Technology Stack

- **Shell scripting** (bash) for git operations
- **JSON manipulation** using `jq` or native read/write
- **Checkpoint protocol** following existing Summoner conventions
- **GitHub CLI** (`gh`) for optional release creation

### Files to Create

```
commands/release.md          # Skill entry point
skills/release/              # (Optional) If logic is complex
  ├── version-manager.sh
  ├── changelog-generator.sh
  └── git-operations.sh
```

### Testing Strategy

1. **Unit tests** - Test each phase independently
2. **Integration test** - Full release workflow on test repo
3. **Rollback test** - Trigger failures at each stage, verify cleanup
4. **Edge case tests** - No commits, version conflicts, network failures

---

## Appendix: Conventional Commits Reference

For changelog classification:

```
<type>(<scope>): <subject>

<body>

<footer>
```

**Types recognized:**
- `feat` - New feature (minor version bump)
- `fix` - Bug fix (patch version bump)
- `perf` - Performance improvement
- `refactor` - Code refactoring
- `docs` - Documentation only
- `test` - Test changes
- `chore` - Build/tooling changes
- `security` - Security fix (patch version bump, high priority)

**Breaking changes:**
- Footer with `BREAKING CHANGE:` (major version bump)
- `!` after type: `feat!: ...`

---

**End of Design Document**
