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

## Scripts (one phase = one script = zero interactivity)

| Phase | Script (under `${CLAUDE_PLUGIN_ROOT}/scripts/`) | Mutates |
|-------|--------------------------------------------------|---------|
| planning | `release-plan.sh [--major\|--minor\|--patch\|--version X.Y.Z]` | no — proposes + validates (semver, tag, drift, uncommitted) |
| changelog | `release-changelog.sh --version X.Y.Z` | no — classified section to stdout |
| publish | `release-publish.sh --version X.Y.Z --changelog-file <path> [--dry-run] [--no-push] [--skip-changelog] [--gh-release]` | **yes** — 3 manifests + CHANGELOG + commit + tag + push (+ optional gh release) |

**No script ever prompts** — decisions ride their checkpoints (two-turn
AskUserQuestion, `../../references/checkpoint-protocol.md`): increment +
uncommitted-continue → planning (rerun plan.sh with the chosen flag);
GitHub Release → publish scope (`--gh-release`, default off).

## Phase flow

1. **planning** — run `release-plan.sh` with the requested increment (no
   flag = patch default). Submit via `summoner:phase-submit` with the
   proposal in the summary (current → new, type, warnings) and the decision
   menu offering the increments.
2. **changelog** — run `release-changelog.sh --version <new>`; save the
   emitted section (below `---8<---`) to a temp file; submit with commit
   count + category breakdown (empty-range warning in findings).
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

Tag exists → planning aborts; empty range → changelog warning; commit/tag/
push failure → ERR-trap rollback; summoner-ctx → warn-continue. Tests:
`--dry-run` / `--minor` / `--version 2.0.0` / interrupted rollback / probe
three-branch.

## Post-Game Review

`done`/`stop` land POST_GAME_PENDING — no release exemption; CLOSED only
via the post-game writer (ONE `[pg:q1..q4]` ask + the journal at
`.summoner/journal/<run_id>.md`), Type 4（流程评价）—
`../../references/post-game-review.md`.
