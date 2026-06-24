# Summoner Enhancement Proposal: Trace & Scoring System

## Motivation

Currently, Summoner provides:
- ✅ Checkpoint protocol (captures user decisions)
- ✅ Post-game review (subjective feedback)
- ✅ Memory chain (pattern persistence)

**Missing capabilities:**
- ❌ **Structured execution traces** — detailed log of tool calls, reasoning steps, phase outcomes
- ❌ **Automated quality scoring** — deterministic + LLM-based evaluation
- ❌ **Regression testing** — detect capability degradation after model/prompt changes
- ❌ **Baseline management** — golden references for known-good executions

## Proposal: Add Trace Capture + Scoring System

Based on AI Agent evaluation best practices (see references), this PR introduces:

### 1. Trace Protocol (`references/trace-protocol.md`)

**JSONL-based structured logging** of every workflow execution:
- Session lifecycle events (start/end with metadata)
- Phase boundaries (phase_start/phase_end with artifacts)
- Tool calls (tool name, args, result, duration)
- Reasoning steps (AI's thought process with confidence scores)
- Checkpoints (user decisions: continue/skip/done/recall/stop)
- Memory operations (queries and pattern retrievals)

**Example trace snippet:**
```jsonl
{"type":"session_start","timestamp":"2026-06-23T10:00:00Z","workflow":"fix","project":"antia-server","model":"claude-opus-4-8"}
{"type":"phase_start","timestamp":"2026-06-23T10:00:05Z","phase":1,"name":"diagnose","skill":"antia-debug"}
{"type":"tool_call","timestamp":"2026-06-23T10:00:15Z","tool":"Read","args":{"file_path":"task.go"},"result":"success","duration_ms":120}
{"type":"reasoning","timestamp":"2026-06-23T10:00:20Z","step":"root_cause_analysis","content":"Found nil pointer at line 234","confidence":0.95}
{"type":"phase_end","timestamp":"2026-06-23T10:00:35Z","phase":1,"status":"completed","artifacts":["root_cause: task.go:234"]}
```

**Storage:** `~/.claude/plugins/summoner/traces/{project-name}/{date}-{workflow}-{session-id}.jsonl`

**Privacy:** Local-only, user can delete anytime, 30-day auto-retention

### 2. Scoring System (`references/scoring-system.md`)

**Three-tier evaluation framework:**

#### Tier 1: Deterministic Scorers (Highest Priority)
Script-based checks for objective criteria:
- ✅ `iron-law-check.sh` — Phase 1 must complete in fix/debug workflows (30 pts)
- ✅ `build-check.sh` — Code compiles/builds successfully (20 pts)
- ✅ `test-pass-rate.sh` — All tests pass in Phase 4 (20 pts)
- ✅ `lint-check.sh` — No new lint errors (10 pts)

#### Tier 2: Rubric Scorers (LLM-as-Judge)
Semantic evaluation for subjective criteria:
- 🤖 `error-handling.yaml` — Error checks present, wrapped with context (10 pts)
- 🤖 `edge-case-coverage.yaml` — Nil checks, bounds checks (10 pts)

#### Tier 3: Human Calibration
Post-game review questionnaires (existing) validate scorer accuracy.

**Scoring dimensions (P0 focus):**
- **P0: Functional Correctness + Robustness** (100 pts, ≥80 to pass)
- P1: Process Quality + Efficiency (workflow optimization)
- P2: Experience + Alignment (tone, verbosity)

**Example scoring output:**
```
📊 Scoring Trace: 2026-06-23-fix-abc123.jsonl

✅ iron-law-check: 30/30
✅ build-check: 20/20
✅ test-pass-rate: 20/20
⊘ lint-check: 10/10 (skipped)
🤖 error-handling: 8/10
🤖 edge-case-coverage: 7/10

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
✅ Total: 95/100 (PASS — threshold: 80)
```

### 3. Baseline Management

**Create golden references** for known-good workflows:
```bash
~/.claude/plugins/summoner/scripts/create-baseline.sh \
  --trace traces/antia-server/2026-06-23-fix-abc123.jsonl \
  --name "fix-nil-pointer-in-task" \
  --category "bugfix"
```

Baselines capture:
- Expected phase sequence
- Expected tool call sequence
- Expected quality scores (P0/P1/P2)
- Expected artifacts

**Regression testing:**
```bash
~/.claude/plugins/summoner/scripts/regression-test.sh \
  --baseline baselines/fix-nil-pointer.baseline.json \
  --new-trace traces/2026-06-25-fix-xyz789.jsonl

# Output:
# Phase Coverage: ✅ MATCH
# Tool Sequence:  ✅ MATCH (LCS: 100%)
# P0 Score:       ⚠️  DEGRADED (-10 pts)
# Overall: ⚠️  REGRESSION DETECTED
```

### 4. Stability Testing

Execute workflow **N times** and measure consistency (inspired by article's N=5 recommendation):
```bash
~/.claude/plugins/summoner/scripts/stability-test.sh \
  --workflow fix \
  --input "修复 task.go:234 nil pointer" \
  --runs 5 \
  --tolerance 0

# Output:
# Run 1-5: [95, 92, 95, 88, 95]
# Pass Rate: 100% (5/5)
# ✅ STABLE (meets 0% tolerance for critical workflow)
```

**Tolerance guidelines:**
- Critical workflows (fix/debug): **0% tolerance** (5/5 must pass)
- Auxiliary workflows (review): **≤10% tolerance** (4.5/5)
- Creative workflows (new): **≤40% tolerance** (3/5)

## Benefits

### For Users
1. **Confidence in AI output** — quantitative quality metrics instead of "looks good"
2. **Fast regression detection** — know immediately if model upgrade breaks workflows
3. **Debugging aid** — detailed traces for post-mortem analysis when things go wrong

### For Summoner Maintainers
1. **Data-driven improvements** — identify which phases/skills need tuning
2. **Prevent regressions** — CI integration blocks PRs that degrade quality
3. **Benchmark across models** — compare Opus 4.8 vs 4.9 objectively

### For AI Research
1. **Reproducible evaluation** — standardized trace format enables cross-project comparison
2. **Bad case → test case pipeline** — low-scoring sessions become regression tests
3. **Human-AI collaboration metrics** — track checkpoint decisions, corrections, skips

## Implementation Status

This PR provides:
- ✅ `references/trace-protocol.md` — Complete specification
- ✅ `references/scoring-system.md` — Complete specification
- ✅ `scripts/score-trace.sh` — Scoring orchestrator
- ✅ 4 deterministic scorers in `scorers/deterministic/`
- ⏳ Rubric scorers (requires LLM integration, marked as future work)
- ⏳ Baseline/regression/stability scripts (TODO in follow-up PRs)

## Future Work

1. **Integrate trace capture into SKILL.md** — add trace emission code to Phase execution
2. **Implement rubric scorers** — LLM-as-Judge evaluation for semantic criteria
3. **Build baseline library** — curate 20-30 golden workflows across common tasks
4. **CI integration** — GitHub Actions workflow for regression gating
5. **Dashboard** — Web UI for visualizing score trends over time

## References

Based on the methodology from "AI Agent 测评体系完整实践" (https://zhuanlan.zhihu.com/p/2050893501324441306):
- **Eval = Input → Execution → Trace → Scoring Rules → Score** (core formula)
- **Deterministic > Rubric > Human** (scorer priority)
- **100-point deduction system** with 80-point pass threshold
- **N=5 stability testing** with workflow-specific tolerance
- **Baseline management** for regression detection

## Breaking Changes

None — this PR is purely additive. Existing workflows continue to function without traces/scoring.

## Testing

Manually tested `score-trace.sh` with synthetic JSONL files covering:
- ✅ Iron law pass/fail cases
- ✅ Build success/failure cases
- ✅ Test pass/skip cases
- ✅ Scorer skip logic (exit code 2)

## Checklist

- [x] Documentation complete (`trace-protocol.md`, `scoring-system.md`)
- [x] Scripts executable and error-handling robust
- [x] No breaking changes to existing workflows
- [ ] Rubric scorers implemented (deferred to follow-up)
- [ ] Integration tests with real Claude Code sessions (requires manual validation)

## Request for Feedback

1. **Trace format:** Is JSONL the right choice, or prefer structured logs (e.g., protobuf)?
2. **Scoring weights:** Are P0 weights reasonable (iron law: 30, build: 20, tests: 20)?
3. **Privacy:** Is local-only storage + 30-day retention acceptable, or need opt-out flag?
4. **Rubric scorers:** Should they use Haiku (fast/cheap) or Sonnet (accurate/expensive)?

Looking forward to feedback from the Summoner community!
