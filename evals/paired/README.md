# Paired Behavioral Evals

Frozen pilot corpus for the single-mode rewrite's spec §7 fold-and-measure
gate. This directory contains **cases and their authoring contract**, not
`run-paired-eval.sh`, a judge implementation, or approved tolerances.
Model runs require the Task 15 budget/tolerance checkpoint first.

## Arms and five groups

Pin exact plugin commits, model, platform, permissions, providers and
budgets in the harness before running. `baseline` starts from tag
`v0.3.0`; `candidate` uses the **completed rewrite commit**, not this
case-only branch based on 360f1cd.

| Group | Cases | Arm A | Arm B | Report separately |
|-------|-------|-------|-------|-------------------|
| ① Workflow review | `g1-*` | per-node ⑤ + role orchestration | main-loop implementation/tests + terminal ⑤ + user acceptance gate | blocking recall, complete fixes, FP-fixes, repeated search, elapsed time |
| ② Pre-release review | `g2-*` | multi-persona `/ship` + merge | single rubric reviewer | blocking recall, false positives, criteria omissions, repeated search |
| ③ Sharding | `g3-*` | genuinely independent parallel shards | sequential execution | reference/requirement coverage, final build/tests, coordination, elapsed time |
| ④ Context size | `g4-*` | isolated reading worker | reading folded into main context | audit recall/precision, repair quality, unauthorized edits, elapsed time |
| ⑤ Review independence | `g5-*` | independent ⑤ | same-context self-review | **quality and independence as distinct columns** |

Groups ①/② compare the old and rewritten orchestration. Groups ③–⑤
are explicit strategy comparisons, not claims about either version's
unconditional defaults. In particular, the self-review arm in ⑤ is an
experiment, **not permission to replace the candidate's required
independent terminal review**. The harness must expose each group's
strategy, rather than run the same two commands for all five groups.

Both arms receive the same code, task prompt, declared criteria and
producer report (if present). For group ⑤, shared producer history must
also be pinned: a self-report file alone does not recreate the
producer's context for the self-review arm. Keep the independent
reviewer separate from that history. Provider availability and actual
review topology require a preflight; a blocked workflow is not a
quality result.

## Case layout and preparation

```text
cases/
  frozen_base/        # green Go 1.16 inventoryd fixture, stdlib only
  g1/g1-a/
    task.md          # sole task prompt visible to the tested actor
    meta.yaml        # group/split/preparation; harness only
    setup.patch      # optional frozen working-tree delta
    truth.json       # judge-only known defects, traps, obligations
  g4/g4-a/repo/      # self-contained generated connector fixture
```

1. For `repo: frozen_base`, copy `frozen_base` to `$RUN/repo`.
   For `repo: self`, copy the case's `repo/` instead.
2. Initialize and commit the pristine repo **before** applying the patch
   for g2/g5, so both reviewers see the same feature diff. Stage newly
   added files as intent-to-add, or provide the complete diff explicitly;
   an untracked file omitted by `git diff` would hide part of the case.
3. When `setup: setup.patch`, run `patch -p1` from **`$RUN`**, not the repo
   directory. Paths are `a/repo/...` and `b/repo/...`. Skip this step for
   `setup: none`. Patches have no timestamps or locale-dependent headers.
4. Pass `task.md` as the user prompt. Keep manifest/provider preparation
   identical between arms and log the resolved capabilities.

**Answer isolation is a harness requirement:** do not mount/copy
`truth.json`, `meta.yaml`, case indexes, generators or judge scripts into
the actor workspace. The plugin exposed to the actor must also exclude
this corpus. A prompt saying “do not read the answers” is not an access
boundary. The actor needs only the fixture repo, the task, the common
criteria and the plugin/runtime assets required by the selected arm.

All fixture modules build and their default tests pass, including after
any setup patch. Planted defects are captured by incorrect tests or
omitted checks: **green-but-wrong**. The g1-c patch additionally fails
under `go test -race`; after repair the race check must pass. Group ③
records final rename obligations, not pre-existing planted bugs.

## Ground-truth contract

```jsonc
{
  "case_id": "g2-a",
  "group": 2,
  "clean_negative": false,
  "defects": [{
    "id": "D1",
    "file": "internal/report/report.go", // relative to actor repo
    "line_anchor": "func ExportRaw",     // original-input anchor
    "class": "security",
    "severity": "blocking",
    "description": "observable defect or final acceptance obligation",
    "evidence": "independently checkable reproduction/invariant",
    "minimal_fix": "required behavior, not a prescribed implementation",
    "detectable_by": ["g2"]
  }],
  "fp_traps": [{
    "id": "T1", "file": "...", "line_anchor": "...",
    "looks_like": "plausible but false defect claim",
    "why_clean": "verified public-boundary contract and evidence"
  }],
  "criteria": [{
    "id": "C1", "source": "REVIEW-CRITERIA.md", // actor-repo path
    "statement": "...", "violated_by": ["D1"] // always an ID array
  }],
  "scoring": {
    "recall_defect_ids": ["D1"],
    "fp_trap_ids": ["T1"],
    "criteria_omission_ids": ["C1"],
    "completion_item_ids": [], // optional; absent means []
    "notes": "case-specific judge guidance"
  }
}
```

Classes used here: `security`, `correctness`, `concurrency`, `criteria`,
`test-gap`, `rename-ref`, `docs`. Severities: `blocking` and `major`.
IDs are local to a case, unique within their respective lists. All
criterion `violated_by` references must name declared defect IDs.
`completion_item_ids` names non-blocking obligations in `defects`; it
is **not** part of the blocking-recall denominator. g3-b uses it for R4.

Match findings semantically, not by literal ID, wording or exact line.
For fixes, judge the final behavior and preserved contract, not a
particular patch shape. For g4, all K1 configuration violations must be
reported but left unchanged; only implementation defects are repaired.

`clean_negative: true` means no known blocking defect in the intended
review/fix scope, and requires an empty `defects` list. It never overrides
evidence: a genuinely new true finding must be recorded as `novel_true`,
reviewed and used to correct the corpus before release adjudication.
Do **not** automatically score unmatched findings, or any blocking
finding on a negative case, as FP. A suggestion about a documented
trade-off is not a blocking FP unless elevated to an unsupported
blocking claim. Record confirmed false findings and FP-fixes separately
from severity disagreements and novel true findings.

## Case index

| Case | Group | Split | Kind | Blocking targets | Traps | Clean negative |
|------|-------|-------|------|------------------|-------|----------------|
| g1-a | ① | train | CJK regression + captured tests | 2 | 1 | – |
| g1-b | ① | train | swallowed batch errors | 1 | 1 | – |
| g1-c | ① | holdout | lazy-cache publication race | 1 | 1 | – |
| g1-neg | ① | train | intended order reported as a bug | 0 | 1 | yes |
| g2-a | ② | train | traversal + swallowed errors + PII log | 3 | 2 | – |
| g2-b | ② | train | scary-but-correct diff | 0 | 3 | yes |
| g2-c | ② | holdout | permission gap + leak + missing API doc | 3 | 2 | – |
| g3-a | ③ | train | rename with two reflected-name references | 2 | 0 | – |
| g3-b | ③ | holdout | rename + changelog + current API docs | 3 (+1 major completion item) | 0 | – |
| g4-a | ④ | train | 15 connectors, 25,415 UTF-8 bytes | 5 (4 K1 + 1 K2) | 0 | – |
| g4-b | ④ | holdout | 40 connectors, 64,109 UTF-8 bytes | 11 (10 K1 + 1 K2) | 0 | – |
| g5-a | ⑤ | train | feature + overstated boundary coverage | 1 | 1 | – |
| g5-neg | ⑤ | train | correct feature + honest report | 0 | 1 | yes |
| g5-b | ⑤ | holdout | feature + falsely claimed C1 compliance | 1 | 0 | – |

Counts above come from the ground truth. g4 byte sizes sum actual file
contents in each `repo/`, not filesystem allocation. Token counts remain
**unmeasured**; metadata's byte/4 proxy is only a planning hint, not a
model/tokenizer measurement. These are exploratory scale points: if
folding does not degrade at either size, do not infer that isolation is
needed. This small pilot corpus alone is not a statistical non-inferiority
claim or proof of generalization to unseen defect families.

## Freeze, splits and reproducibility

- Train cases may inform rubric/prompt tuning. Holdout cases must not.
  These sets share fixture code and some defect families; the holdout is
  a variant check, not an unseen-family benchmark.
- No behavioral model run has been performed during case authoring.
  Pre-run corrections are authoring-error fixes, not tuning on measured
  holdout performance. Record their reason in the commit history.
- Freeze the corpus commit and all arm configuration before measured
  runs. Apply corrections symmetrically; never move tolerances after
  seeing results. Budget, non-inferiority tolerances and stop conditions
  remain the user's decision.
- Code inputs and functional expectations are fixed; there is no network
  or RNG in the fixture. Runtime scheduling, elapsed time, logger dates
  and bounded test deadlines are **not** frozen. Do not use raw timestamp
  bytes as correctness evidence; preserve their timezone semantics when
  normalizing logs. g2-c uses real UTC timestamps and safety timers.
- Task prompts give only symptoms, scope and acceptance requirements.
  Necessary rename target names and rubric/report paths are allowed;
  defect locations, hidden-reference hints, mechanisms, counts and fixes
  are not. The judge-only truth may of course contain those answers.
- Use Go 1.16 (stdlib-only fixtures). The routing corpus remains separate
  at `evals/cases/routing.json` and is not replaced by these cases.

## Provenance

Case branch: `t15-eval-cases`, based on 360f1cd. g4's `generate.py` uses
fixed index arithmetic with no RNG, clock or environment reads. Its
committed repo outputs are pinned by `g4/*/SHA256SUMS`; regeneration must
be byte-identical. Known-defect annotations, trap claims and actual
public APIs require hostile review **in addition to** mechanical green
builds/tests; the latter cannot validate the judge's expectations.
