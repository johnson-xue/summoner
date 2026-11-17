# Summoner Scoring System (v2)

Deterministic contract scoring of v2 traces. The unit of work is ONE trace
file scored by FIVE contract scorers; the result is a derived sidecar next to
the trace. There are no point tiers, no priorities, no skip-credit, no LLM
judges in this layer — behavioral/model evaluation is a separate,
budget-gated surface (spec §7 行为评测放行门, task T15), and nothing here runs
or simulates a model.

## The five contract scorers

Each scorer owns one slice of spec §7 and answers with a STATUS, not points:

| Scorer | Owns |
|---|---|
| `scorers/deterministic/schema-check.sh` | v2 event-set membership (the 38-type allow-list mirrored from `hooks/shared/trace.go`), line shape, `workflow.started` first, terminal exactly-once discipline |
| `scorers/deterministic/review-evidence-check.sh` | reviewer identity, running-review evidence, dangling verdicts, rubber-stamp PASS, evidence-id joins, per-criterion completeness, 同 digest 复用. Reach note: on real runtime traces (thin payloads) the reuse audit's mismatch branch is UNREACHABLE — the content triple is always `(digest, (), ())` because criteria/scope are prepared-side enrichments the runtime does not emit — so the real-trace audit degrades to digest grouping + a reuse note; same-triple enforcement stays registration-side (the reducer's envelope validation) |
| `scorers/deterministic/user-gate-check.sh` | 展示绑定 / 决策绑定 (AskUserQuestion-only decisions) / nonce / 幂等 / stale 渲染 / ride-along binding (criterion drops, budget) |
| `scorers/deterministic/recovery-check.sh` | OUTCOME_UNKNOWN probe resolutions, recovery checkpoints on bad outcomes, review crash recovery without producer re-runs, recall semantics |
| `scorers/deterministic/post-game-check.sh` | CLOSED only after `post_game.completed`; emergency stop stays POST_GAME_PENDING |

Scorers do not verify the hash chain — Go owns crypto (`VerifyChain`); the
scorers audit semantics. Contract failure dominates everything: one FAIL
fails the trace regardless of how much else holds.

## The four statuses (skip ≠ 满分)

| Exit | Status | Meaning |
|---|---|---|
| 0 | `PASS` | the audited surface holds |
| 1 | `FAIL` | contract violation (catchable reason on stdout) |
| 2 | `NA_JUSTIFIED` | the surface does not exist in this trace — the scorer justifies the N/A from trace content (e.g. no review events recorded). **Not a PASS**: it is recorded as its own status and never counts toward "all green" as a pass. |
| 3 | `MISSING_EVIDENCE` | the surface was claimed or begun, but its evidence is absent (e.g. `post_game.started` with no terminal; a PASS verdict with no recorded evidence ids; an `outcome_unknown` never resolved). Required evidence missing FAILS the trace. |

N/A and missing evidence are strictly distinct: absence of a surface can be
legitimate (justified N/A); absence of evidence for a claimed surface never
is. Overall PASS requires every scorer at `PASS` or `NA_JUSTIFIED`.

## The sidecar (derived, never authoritative)

```bash
scripts/score-trace.sh --trace ~/.summoner/traces-v2/<project>/<run>.jsonl
```

Writes `<trace>.score.json` NEXT TO the trace and never appends into the
trace (spec §7 落盘边界):

```json
{
  "schema_version": "2",
  "trace_file": "run-x.jsonl",
  "trace_sha256": "…",
  "generated_at": "2026-11-11T00:00:00Z",
  "scorers": [
    {"name": "schema-check", "status": "PASS", "detail": "PASS — …"},
    {"name": "review-evidence-check", "status": "MISSING_EVIDENCE", "detail": "MISSING — …"}
  ],
  "overall": {"status": "FAIL", "PASS": 3, "NA_JUSTIFIED": 1,
              "MISSING_EVIDENCE": 1, "FAIL": 0}
}
```

Exit code mirrors `overall.status`. Re-running replaces the sidecar
(same inputs → same statuses; the `trace_sha256` pin makes stale sidecars
detectable).

## Baselines and regression

```bash
# pin a baseline from a SCORED trace (the sidecar must exist — required
# score missing = FAIL, never a warning)
scripts/create-baseline.sh --trace <run.jsonl> --name <kebab> --category <bugfix|feature|ops>
# → baselines/<project_root_hash>/<name>.baseline.json
#   {schema_version: "2", trace_sha256, workflow, phases_closed, expected_scores: {…}}

# score the new run, then compare
scripts/score-trace.sh --trace <new.jsonl>
scripts/regression-test.sh --baseline <b.json> --new-trace <new.jsonl>
```

`regression-test.sh` fails when: the new trace has no sidecar (required
score missing), the trace schema version drifted from the pinned one, a
pinned scorer is absent from the sidecar, or any pinned status regressed
(`PASS > NA_JUSTIFIED > MISSING_EVIDENCE > FAIL`). Improvements are noted,
not penalized.

## CI

`scripts/run-trace-fixtures.sh` is the executable contract matrix:
exit-code grid (fixtures × 5 scorers), adversarial message assertions (the
negative fixtures assert the scorer's catch reason), and sidecar/baseline
self-checks (sidecar lands next to the trace; the trace bytes never change;
a missing sidecar fails baseline creation and regression). Run it in CI and
after any scorer/fixture edit; the fixture catalog lives in
`tests/fixtures/traces/` with its expected-outcome table.

## Related

- `trace-protocol.md` — the v2 event vocabulary and write boundary
- `tests/fixtures/traces/README.md` — the fixture catalog
- spec §7 (确定性回归必须覆盖) — the coverage authority
