# In-band artifacts + session narrative — plan rev2 (2026-11-19)

Spec rev2: `docs/superpowers/specs/2026-11-19-inband-narrative-design.md`
(adversarial review round folded in + §10 mattpocock adaptation).
Branch `inband-narrative` (rebase onto 06efefa = master/0.5.2). Every
task: RED first, GREEN, then its battery slice. Fresh binaries
(`make -C hooks build`) before any contract run.

## Task 1 — ResolveProjectPath + Narrative type (shared, pure)
- `hooks/shared/narrative.go`: `ResolveProjectPath(raw) (rel string, err
  error)` (Abs → EvalSymlinks-on-existing-ancestors → component-boundary
  containment → canonical rel; forbidden classes ONLY outside the project;
  planned-vs-IO-error distinction), `Narrative{Background,Actions,Outcome}`
  + prose caps (400r/field, summary 400r, requirement content 600r,
  escaped-byte budgets) + CRLF/trailing normalization.
- RED: resolver table (containment-first, symlink ancestor, dual-spelling
  dedup, outside-forbidden classes, missing-vs-IO).

## Task 2 — Gate admission (resolve-before-hash) + reducer belt + artifact_dirs
- `stagePhaseSemantics`: every submitted path through the resolver BEFORE
  hashing; canonical rel spelling flows through staging/state/freshness;
  IO errors denied with reason; missing → planned scope (unchanged).
- NEW deliverable-class rule (spec §3.5): manifest `artifact_dirs` declared
  → file artifacts must resolve inside a declared dir (one-off reports/
  repro/scratch rejected with 内联 instruction; v1/undeclared = rev3 rules).
- `SubmitPhase` belt re-checks admission; `PhaseSubmission.Narrative`
  persisted onto CheckpointState; `rerenderCheckpoint` copies narrative.
- RED→GREEN: absolute-in-project path no longer silently planned;
  artifact_dirs strict table (inside/outside declared dirs, undeclared
  manifest passthrough); belt table; old payloads pass.

## Task 3 — Journal projection protocol + stop asymmetry + additive writers
- `appendDecisionSection` in the applyDecision CORE: state
  journal_pending(键=run/checkpoint/decision/tool_use_id+期望摘要) →
  O_APPEND write with digest sentinel footer → journal_committed receipt;
  idempotent replay; torn-sentinel recovery; §5 byte format incl. outcome.
- STOP at journal failure → POST_GAME_PENDING + journal_debt receipt
  (surfaced in status render); all other decisions reject with remediation.
- `WriteMinimalExitJournal` + post-game observation writers become
  ADDITIVE (sentinel sections, never whole-file overwrite);
  post-game-review.md clause updated.
- Trace: receipt rides `checkpoint.decided` payload (NO new event type).
- RED→GREEN: section bytes; replay; torn; unwritable-project rejects
  continue but allows stop; overwrite tests inverted; hermetic via
  journalRootOverride.

## Task 4 — Stop-hook precedence + render regrouping + render diet
- stop/main.go: try `VerifyDisplayMessage` BEFORE `stopAck` (exact canonical
  match binds display; stop words inside a legal render are prose, not an
  ack; genuine free-text ack keeps its escape). RED: full-frame fixtures
  with stop words in every narrative field + 至今脉络 lines.
- render.go §2 rev3 diet: DROP verification env line, budget machine keys
  (humanize `预算: 工具调用 a/b · 回工 c/d · 委派 e/f`, only ≥80% or with
  ask), page projection lines, two-turn instruction line, criterion
  scope/check/rubric sub-lines (fold into one line per criterion);
  digests 8ch; 至今脉络 cap 5. Nonce stays (binding contract).
- summoner-status: NEW JSON view `{"view":"pages"}` returning the persisted
  page projection (the model's exact ask sets); skill docs point at it.
- RED→GREEN: render_layout_test extensions (diet asserted: no env line, no
  page lines, no instruction line); status/stop/contract
  contains-assertions shifted.

## Task 5 — Envelope versioning + frozen section references
- Narrative digests as versioned additions (omitempty; legacy no-narrative
  re-mint byte-equal — serialized 0.5.1 PREPARED/RUNNING/result fixtures
  pin it); `[v]`/terminal freeze `(checkpoint_id, section_digest)`
  references of accepted visits only; dispatch/conclusion verify cited
  sentinels; CheckScope.readable += journal path; CheckpointContentDigest
  stays v1 (separate narrative_digest joins display binding, byte-equal
  when absent).
- RED→GREEN: envelope tests; legacy fixtures; review round-trip green.

## Task 5b — method_skills (mattpocock adaptation §10.2)
- v2 manifest `method_skills` (+ v1 translation carries it); gate: method
  invocation never auto-opens, allowed mid-phase (budget-charged,
  `method.use` trace), WAIT_USER still blocks; shipped default set.
- RED→GREEN: gate tests (auto-open suppressed for declared method skills,
  still fires for undeclared skills).

## Task 6 — Skills/docs sync + lints + mattpocock notes
- phase-submit SKILL.md: narrative contract + admission rule; checkpoint-
  protocol.md template sync; manifest-spec: method_skills +
  standards_docs; reviewer.md: section-reference evidence, prose-as-claims;
  SKILL.md routing note (分层 + 入口纪律 §10.5); antia-server reference
  manifest snippet in docs (project applies it itself).
- validate-repo green (canonical template; pointer lint from 0.5.2 holds).

## Task 7 — Battery + release
- Full battery (hooks uncached + race, root, validate-repo incl paired,
  trace 227/38, optimization, install smoke); lane CI BEFORE tagging
  (0.5.2 lesson: hosted shellcheck is the only shellcheck);
  **0.6.0 publish only after user authorization**.
