# Paired Behavioral Evals

Frozen pilot corpus + the Task 15 harness for the single-mode rewrite's
spec §7 fold-and-measure gate. `cases/` is the frozen case set and its
authoring contract; the harness lives in `harness/` (thin entry:
`scripts/run-paired-eval.sh`). Approved tolerances do NOT exist yet —
`tolerances.yaml.example` ships empty placeholders, and the harness refuses
to run or judge without a user-filled tolerances file plus a budget
statement. Model runs require the Task 15 budget/tolerance checkpoint
first.

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

## Harness (Task 15; adjudicated 2026-11-11, P-MIN-R with grafts)

```text
evals/paired/
  harness/paired_eval.py        one python3-stdlib file: list|prepare|preflight|run|judge|aggregate
  harness/manifests/summoner.v2.yaml   the candidate compat manifest (version "2", project
                                inventoryd, phases.docs.skill none — nothing beyond the frozen
                                v1 file's own comments; NEVER a file inside cases/)
  harness/expected-deltas.yaml  the pinned capability expected-delta allowlist (fairness preflight)
  harness/profile.sbpl          OPTIONAL darwin sandbox-exec tier template (never the mandatory boundary)
  harness/MANIFEST.digest       pins every folded cases/ file at fold-in (peer commit 4af4ed3)
  gate-policy.yaml              gate channels/policies, digest-pinned into every run manifest
  agents/worker.yaml            byte-copy of agents/worker.md (the g4 --agents file is byte-pinned)
  tolerances.yaml.example       EMPTY placeholders, no numeric values anywhere
  results/                      output-only (gitignored; committed .gitkeep)
scripts/run-paired-eval.sh      thin shellcheck-clean entry point
scripts/test-paired-harness.sh  deterministic selftests (no model; wired into validate-repo)
```

`--list`, `--prepare`, `--preflight` and the selftest invoke ZERO claude
calls (PATH-stub tested). `run`/`judge` REFUSE (exit 3) without a
user-filled tolerances file and budget statement.

**Layout per case×arm.** `$CONTROL = evals/paired/results/<run_id>/`
(trusted: truth/meta copies, gate policy, canary, run manifest, pristine
pre-actor tree snapshot, probes, gate records, judge output) and
`$RUN = <tmp>/<case>-<arm>-…/` (actor side: `repo/` fixture+patch+per-arm
manifest, `plugin/` staged copy, `state/{s,t,m}`, `out/`). No actor path
references `$CONTROL`.

**Staging.** Baseline = tag `v0.3.0` (the recorded tag commit — never
silently the current tree), candidate = the recorded harness commit, both
staged by the SAME allowlist copy (`plugin.json, .claude-plugin/, skills/,
commands/, agents/, references/, hooks/ incl. bin/, summoner-ctx/,
scripts/, docs/, settings-recommended.json`; `evals/, scorers/, tests/,
memory/, .git, .superpowers, .claude/` dropped). `scripts/` is REQUIRED —
v0.3.0 skills invoke `${CLAUDE_PLUGIN_ROOT}/scripts/init-memory-db.sh`.
Staging is byte-reproducible (selftest: two stages, equal digests). The
two harness entry scripts (`scripts/run-paired-eval.sh`,
`scripts/test-paired-harness.sh`) live under the allowlisted `scripts/`
path but are eval infrastructure — they are excluded from every staged
plugin (the selftest embeds expected case outcomes; staging it would leak
answers). The
frozen v1 manifest stays byte-frozen for the baseline arm; the candidate
gets the harness-side v2 manifest. Preflight runs EACH arm's own native
`validate-manifest` on its staged fixture plus the candidate validator on
the v2 file; both native validators must pass.

**Preparation order per arm** (identical across arms; every input
digested): fixture copy → per-arm manifest install (BEFORE the pristine
commit, so the actor-visible feature diff never carries harness runtime
config) → `git init` + isolated pristine commit (generic fixture author,
per-command config — the harness never touches user/global/worktree git
config — no templates, no remotes) → `setup.patch` via `patch -p1 -N` from
`$RUN` (`.rej` aborts; `.orig` cleaned) → intent-to-add so `git diff`
shows new files → per-run memory DB pre-init → settings.json
(schema-validated locally AND rebuilt+re-validated at launch, because `-p`
silently drops invalid settings — verified; digest-pinned).

**Launch.** NEVER `--bare` (it skips plugin hooks — both runtimes under
test ARE their hooks). `claude --plugin-dir $RUN/plugin --settings
$RUN/settings.json`, cwd `$RUN/repo`, prompt = task.md. Per-run env:
`SUMMONER_STATE_DIR/TRACE_DIR/MEMORY_DIR=$RUN/state/{s,t,m}` — WRITE via
env, READ DENIED to the actor. Honest caveat: the candidate honors all
three overrides; v0.3.0 honors only `SUMMONER_MEMORY_DIR` (its shared.go),
so baseline hook state is captured by tmp-discovery (keyed by project
hash) and the deny rules cover the shared dir — recorded per arm as
`capture_mode`. Deny rules cover `$CONTROL`, the original checkout, ALL of
`~/.summoner` (memory is the cross-arm leak settings-recommended.json
misses), `~/.claude/projects` (minus the one pinned producer session file
on g5 fork runs — expressed by narrowing the deny entries, since deny
beats allow), sibling runs, and the run's own state dirs. Bash allowlist
is exact-purpose (`git add|commit|status|diff|log`, `go build|test|vet`);
`Bash(git -C:*)` and `Bash(go run:*)` (oracle-read primitives) are DENIED.
Network stays open (model API).

**Isolation probes P1-P9** (per case×arm, recorded in
`$CONTROL/probes.json`; any failure ⇒ BLOCKED, never a score): P1 read of
`$CONTROL` truth FAILS; P2 read of the repo go.mod SUCCEEDS; P3 `go test`
in a scratch fixture SUCCEEDS; P4 zero corpus filenames in actor-visible
roots; P5 hook state write captured per-run; P6 read of the run's own
state dirs FAILS; P7 `claude --help` boot probe SUCCEEDS; P8 (g5 forks)
pinned producer session file readable, siblings DENIED; P9 (execution
tiers) a Bash-CHILD corpus-path read is DENIED under the exact arm launch
flags, probed via the plugin's `hooks/bin/summoner-sandbox-probe`.
Required tier per arm is derived from truth evidence needs (g1, g3, g4
and g5-a's Limit=0 probe REQUIRE the P9-passing tier; no-Bash arms run at
the lower permission tier, disclosed as a metric column). Non-darwin
platforms get P9 BLOCKED-emission, never a silent downgrade. sandbox-exec
(SBPL) is an OPTIONAL extra tier where available. Post-run transcript
scan (canary token, out-of-root tool_use) quarantines CONTAMINATED runs —
detection backstops prevention.

**Gates.** PRIMARY = attended: a named operator (gate-policy.yaml
`operator.name`, recorded in the run manifest) answers every checkpoint
per the digest-pinned gate policy; candidate gates arrive as
AskUserQuestion dialogs (the ONLY decision channel), baseline v0.3.0 gates
are chat-prose — channels declared per arm, "identical mechanical
answering" never claimed. The g1 candidate acceptance gate is ALWAYS
human-decided; a mechanical answer there is a protocol violation and
BLOCKS the case (judge exit 9). SECONDARY (probe-gated, never primary): a
stream-json two-turn mechanical responder for pre-ratified strategy-arm
policies ONLY, admitted after the user-approved micro gate-probe proves
clean AskUserQuestion tool_results; until then headless runs report
BLOCKED-channel (exit 4). Every metric row joins the per-gate record
`{gate_id, arm, channel, answerer, policy_digest, dwell}`.

**Fairness.** Identical TASK inputs (task.md, criteria, feature diff,
report bytes; same prep procedure; every input digested). Resolved
capability tables are logged per arm and compared against the
expected-delta allowlist — exactly the three adjudicated deltas (v0.3.0
`requesting-code-review` ↔ candidate `verification_spec` rubric; persona
fan-out ↔ single reviewer; per-node ⑤ ↔ terminal ⑤); anything else (in
either direction) ⇒ preflight BLOCKED for user adjudication. g4 ships no
manifest: the harness probes v0.3.0's session-start+stop binaries on a
manifest-less temp project (no model); if they do not tolerate it, the
contingency is a symmetric minimal manifest for BOTH arms (logged, never
arm-only).

**Groups.** g1: v0.3.0 `/summoner:fix` vs candidate fix (main loop +
terminal ⑤ + attended acceptance gate). g2: v0.3.0 `/summoner:ship`
personas vs candidate single-rubric verification_spec. g3: the
corpus-designed pairing (baseline arm parallel shards vs candidate arm
sequential; version-entangled, DISCLOSED — never silently rewired; the
truth denominators were designed for it). g3-prime (optional,
user-budget-permitting, separate arm-pair record): candidate-only
delegation-vs-sequential via `--include-optional`. g4: candidate-only
strategy isolation — arm A adds the worker via a session-scoped `--agents`
file BYTE-PINNED to shipped `agents/worker.md` (hooks admit
worker/summoner:worker; NO product registration), arm B denies Agent
(folded); the K1 digest alarm is deterministic. g5: SEEDED producer —
setup.patch applied, workspace digest asserted equal to the frozen patched
state, producer session P gets ONE adoption prompt (feature request +
frozen diff + pinned self-report as its own completed work; NO
verification instruction; only review-turn probes count toward
independence), digest re-asserted, P frozen; arm A = fresh session whose
envelope contains ONLY workspace + task.md + criteria + self-report
(seeding framing NEVER in arm A's context); arm B = `--resume P
--fork-session` (P never appended; repeats fork from P only) with the P8
single-file read allowance. `producer_history: seeded` is recorded.

**Judge** (harness-side only). Deterministic first pass maps actor-cited
evidence (file + function anchor) to truth ids AGAINST the pristine
pre-actor tree in `$CONTROL`; stale anchors route onward, never silently
dropped. Residuals → budget-gated model adjudication with cited evidence
(judge runs use `--no-session-persistence`); unmatched findings are
novel_true candidates (quarantined) — NEVER auto-FP; FP-fixes, severity
disagreements and novel-true stay separate columns; a denied adjudication
budget yields MATCH_PENDING, not scores. Elapsed excludes checkpoint dwell
(candidate trace events); baseline trace-sparse columns are marked
N/A-source, never zero-filled.

**Skill-authoring carry (controller 2026-11-11).** Deterministic
red/green contract tests for SKILL.md files and protocol edits are NOT
proof that a model discovers or follows the skills. The post-budget eval
plan reserves no-guidance/control vs skill-present application/pressure
scenarios — dispatch discovery, malformed payload, two-turn human gate,
exhausted budget, no sandbox, emergency stop — with the same
version/input/oracle isolation as the paired cases. Any actual model
samples, including writing-skills micro/pressure tests, stay behind the
SAME budget gate as everything here.

**Selftests** (`scripts/test-paired-harness.sh`, deterministic, no model,
wired into `scripts/validate-repo.sh`): corpus digests (MANIFEST.digest,
g4 SHA256SUMS, generate.py regeneration), refusal gates (the example ships
empty and digit-free), `--list` snapshot with a PATH-stub claude (zero
invocations), prepare invariants (pristine-commit-before-patch, per-arm
manifest bytes, truth/meta absent from `$RUN`, allowlist corpus-free AND
containing scripts/, patched-tree digest pinned, generic fixture author),
staging reproducibility, settings schema validation, native validators +
expected-delta table (both drift directions trip), the stub-runner
pipeline (fixture transcripts → expected metric columns; empty transcript
⇒ nonzero BLOCKED/EMPTY exit; probe failure ⇒ BLOCKED; canary ⇒
QUARANTINED), P9 plumbing via the plugin's sandbox-probe wrapper, the g4
`--agents` byte-pin, and the g5 producer→fork flow (P8 allowance,
`--resume --fork-session`, never `--bare`).
