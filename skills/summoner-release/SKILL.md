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

Calling contract for the fixed framework route **planning → changelog →
publish** (`../../hooks/shared/workflow.go`). Implementation lives in three
single-purpose scripts (shellcheck-gated in lane CI); this skill owns NO
inline shell programs. `phases.release` in `summoner.yaml` may replace this
skill with a project provider; no entry → this skill, disclosed
「未配置项目专属能力」.

## Scripts (one phase, one script, zero interactivity)

| Phase | Script (under `${CLAUDE_PLUGIN_ROOT}/scripts/`) | Mutates |
|-------|--------------------------------------------------|---------|
| planning | `release-plan.sh [--major\|--minor\|--patch\|--version X.Y.Z]` | no — proposes + validates (semver, tag freshness, manifest drift, uncommitted-state surfacing) |
| changelog | `release-changelog.sh --version X.Y.Z` | no — prints the classified changelog section to stdout |
| publish | `release-publish.sh --version X.Y.Z --changelog-file <path> [--dry-run] [--no-push] [--skip-changelog] [--gh-release]` | **yes** — all three manifests + CHANGELOG.md + commit + tag + push (+ optional gh release) |

**No script ever prompts.** Every user decision rides the checkpoint
channel (two-turn AskUserQuestion per `../../references/checkpoint-protocol.md`):

- Increment choice (patch/minor/major/custom) → the **planning checkpoint's
  decision**; then rerun `release-plan.sh` with the chosen flag.
- Continue with uncommitted changes? → planning checkpoint's first item.
- Create a GitHub Release? → publish scope decision; opt in via
  `--gh-release` (default off).

## Phase flow

1. **planning** — run `release-plan.sh` with the requested increment (no
   flag = patch default). Submit via `summoner:phase-submit` with the
   proposal in the summary (current → new, type, warnings) and the decision
   menu offering the increments. A changed choice reruns the script.
2. **changelog** — run `release-changelog.sh --version <new>`; save the
   emitted section (below the `---8<---` marker) to a temp file. Submit with
   the commit count + category breakdown; empty-range warning goes in
   findings.
3. **publish** — the mutating phase: its capability list MUST declare the
   read-only reconciliation probe set BEFORE the release enters it (below).
   Run `release-publish.sh --version <new> --changelog-file <tmp>`; the ERR
   trap rolls back tag/commit on failure (files preserved; explicit-path
   adds only). `PROJECT_NAME` (optional summoner-ctx persistence) is
   resolved by YOU from this run's `summoner.yaml` and exported
   shell-quoted — the script never parses YAML; persistence is warn-continue.

## 只读对账探针 — publish 阶段的必填能力项

publish (version writes, commit, tag, push) is the mutating phase: 无探针
声明不得进变更阶段 (spec §5). A declared probe is a manifest
`check_registry` entry whose definition is a read-only predicate (L1
framework op) or a read-class Bash line
(`../../references/manifest-spec.md`). The OBSERVED evidence
`summoner:probe-submit` accepts is strictly a read-class Bash tool id. Default set:

- **版本-已一致** — L1 `grep-count (path, pattern, count)` per manifest:
  each of the three manifests carries `"version": "$NEW_VERSION"` once.
- **tag-已存在** — one read-class Bash probe (`git tag -l "v$NEW_VERSION"`)
  per dangling intent at OUTCOME_UNKNOWN recovery.

Probes 对账 not 修复 — READ-ONLY, one probe per unknown, resolved only via
`summoner:probe-submit` (`../probe-submit/SKILL.md`). 不一致/未生效 are
honest bad-outcome verdicts routed to the recovery checkpoint (CONTINUE /
RECALL / STOP) — never a mutating fix, never silently passed, never
auto-replayed. Pinned by `../../tests/fixtures/traces/valid-release-probe-*.jsonl`
+ `../../scorers/deterministic/recovery-check.sh`.

## Error scenarios

Tag exists → planning aborts; no commits in range → changelog warning;
commit/tag/push failure → ERR-trap rollback; summoner-ctx absent/failed →
warn-continue, release unaffected. Testing: `--patch --dry-run` / `--minor`
(real) / `--version 2.0.0` / publish interrupted (rollback) / probe
three-branch after a publish crash.

## Post-Game Review

`done` / emergency `stop` land POST_GAME_PENDING — no release exemption.
CLOSED comes only via the post-game writer (ONE `[pg:q1..q4]` AskUserQuestion
call + the journal at `.summoner/journal/<run_id>.md`); a clean release is
Type 4（流程评价）. Protocol: `../../references/post-game-review.md`.
