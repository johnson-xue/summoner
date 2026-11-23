# Summoner Checkpoint Protocol (single-loop v2)

## Purpose

Every phase ends at a checkpoint — the run's ONLY advancement surface.
The reducer (Go hooks) owns the state machine: a phase closes into WAIT_USER,
and a decision recorded from the platform's AskUserQuestion tool_response
moves the run on. One recorded exception exists (0.7.0): the **auto-pass**
(below) — the framework itself accepts a checkpoint that needs no judgment,
under fixed conditions, and traces it as `checkpoint.auto`. A skill starting,
the model claiming "done", or a reviewer PASS never authorizes anything. This file is the CANONICAL
render + decision-channel spec; commands and skills point here and never
inline a variant.

## The render contract (machine-rendered, pasted verbatim)

The render comes from the binary — run it and paste its output VERBATIM:

```
"${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status" <<<'{"view":"checkpoint"}'
```

That view is EXACTLY the canonical block. The default view (`<<< '{}'`)
ends with the same block but prints the status lines and the detail block
before it — paste the trailing block alone, not the whole default view:
the user decides on the checkpoint, and the mechanism belongs to the detail.

(or `/summoner:status`, which does the same). NEVER hand-write, summarize, or
reformat a status or checkpoint render. Display verification (Stop hook)
requires the assistant message to CONTAIN the pending checkpoint's canonical
block — an explanation may precede it and the tail line may differ; pasting
the binary's output satisfies it by construction, and the reducer separately
binds the decision to the checkpoint's content digest — a stale or altered
render can never produce a valid decision.

## PHASE START (entering a phase)

One lightweight line, no frame:

```
⚡ Phase {phase-id} — {one sentence: what this phase will do} — skill: {skill | freeform | none}
```

## CHECKPOINT block (canonical render; the status output ends with it when a checkpoint is pending)

<!-- canonical:checkpoint-format — the ONLY in-repo copy of the rendered
     checkpoint template. Commands and skills reference this section; inlining
     a variant anywhere else is a lint error (scripts/validate-repo.sh,
     exactly-once + signature confinement + nonce-field rule).
     hooks/shared/render_template_test.go holds this block to the binary:
     with its {placeholders} read as wildcards it must match, line for line,
     the render of the widest shape. A renderer change that is not written
     here fails that test. -->

Left-rail structured frame (ruling 2026-11-19): the rail is left-only by
design — no right edge, no CJK width alignment, no width dependency — so
the bytes stay deterministic while the checkpoint reads as a table.

**The render is the decision surface and it leads with the conclusion**
(0.7.0). It answers, in order: what happened before (经过), what this phase
established and on what evidence (结论), and what the user is asked to
decide (请决定). It never carries the mechanism — criteria tables, artifact
digests, requirement snapshot, environment, budget keys, the page
projection, the recall-target list. Those live in the status **detail view**
(below). The whole message stays ≤ 42 lines on the largest legal submission.

The template is the **widest shape**: every optional section present, one
line per list. `{…}` names a field or a list. What each line may instead
say — and when a section is absent — is in the tables after it.

```
╔═ ⚡ SUMMONER CHECKPOINT {id} — nonce {nonce} — {kind} ══════════
║ run: {run} · workflow: {workflow} · phase: {phase-id | —} · requirements v{n}
║
║ ▶ 经过
║   {settled visit}
║
║ ▶ 结论 — {已确认 | 未达成 | 未查清}
║   {conclusion.summary}
║   证据:
║     - {evidence item}
║   怎么查的: {conclusion.approach}
║   没查清 · 需要你判断:
║     - {open question}
║   影响: {conclusion.impact}
║
║ 预算已耗尽 · 本次裁决可顺带追加: {extend label} / {hold label}（追加 {axis +delta, …}）
║ review retry: review {id} attempt {id} — resume {target} · retry="{label}" skip="{label}"
╠═ 请决定 — AskUserQuestion（选项须与本页完全一致）
║   {page 1 of the menu, verbatim, one label per line}
║ nonce {nonce} · {navigation hint}
╚════════════════════════════════════
```

Field rules:

| Field | Rule |
|-------|------|
| `{id}` / `{nonce}` | Mandatory, from the render. The nonce binds every decision to THIS checkpoint; a question without it is rejected. |
| `{kind}` | `PHASE` \| `CHANGE_CONFIRM` \| `RECOVERY` \| `REVIEW_RESULT` \| `REVIEW_DEBT`. |
| `经过` | The run's settled phase visits, oldest first — how each was settled and the phase's **title** (the submission's top-level summary) — so a checkpoint can be read without the conversation. The full structured conclusion of each settled phase is in the detail view's `history conclusions`. Capped at 6 lines; the overflow is counted, never dropped. |
| `结论` | The phase's structured conclusion (below), in the fixed inner order. An omitted outcome reads 未查清; every missing part reads 未提供 — a hole is never hidden by omission. |
| `请决定` | Page 1 of the persisted menu, verbatim — exactly the labels the first decision question offers. The divider above it is an instruction to the model: raise the question through AskUserQuestion with options identical to this page. |
| navigation hint | The last content line: the nonce, how to reach the other option pages, and where everything the render left out lives in full. |

What a section or line may instead say:

| Instead of | The render says | When |
|------------|-----------------|------|
| the whole `经过` section (the blank rail line, the head and its lines) | nothing | no phase visit is settled yet |
| one `经过` line | `{phase \| visit} ✓ 已验收: {…}` | the user accepted the phase |
| | `{phase \| visit} ✓ 自动通过: {…}` | the framework auto-passed it |
| | `{phase \| visit} — 已跳过: {…}` | the user skipped it |
| | `{phase \| visit} ↩ 已回城: {…}` | it was recalled and left behind |
| | `…另 {n} 个阶段，见 summoner-status` (as the first line) | more than 6 visits are settled |
| `结论 — {已确认 \| 未达成 \| 未查清}` and its lines | `║ ▶ 结论 — 未查清` with the phase summary and 未提供 in every part | nothing was claimed: a conclusion-free submission, or a checkpoint written before 0.7.0 |
| | `║ ▶ 结论` and `║   {note}` (see the two frames below) | a checkpoint with no submission: change-confirm, recovery, review-result, review-debt — the note carries the delta, the probe outcomes or the verdict |
| `证据:` and its items | `║   证据: 未提供` | the submission cited no evidence |
| | `║     …另 {n} 条，见 summoner-status` after the fourth item | more than 4 items |
| `怎么查的: {conclusion.approach}` | `怎么做的: {…}` | a building phase: requirements, plan, implement, fix, operate, changelog, publish, merge, decide |
| | `怎么查的: 未提供` (or `怎么做的: 未提供`) | no approach was stated |
| the `没查清 · 需要你判断:` block | nothing | the outcome is 已确认 or 未达成 |
| its items | `║     未提供` | the outcome is 未查清 and no open question was listed — an unlisted hole is still a hole |
| | `║     …另 {n} 条，见 summoner-status` after the third item | more than 3 questions |
| any conclusion text | the text cut after its last complete sentence, ending `…（全文见 summoner-status）` | past 200 runes (summary, approach) or 120 runes (impact, one evidence item, one open question) |
| the blank rail line and the two ride lines | nothing | the budget is not exhausted and no review retry is owed |
| either ride line | only the other | just one is owed |
| `{navigation hint}` | `其余选项（recall / review / 回城目标）回复 more: 翻页 · 被截断的结论、验收标准与产物全文见 summoner-status` | the menu has more than one page |
| | `选项已全部列出 · 验收标准与产物全文见 summoner-status` | the menu fits one page |

A checkpoint with no submission draws its 结论 as one of two frames:

```
║ ▶ 结论
║   {note}
```

```
║ ▶ 结论 — 框架未能完成审查
║   原因：{…}
║   对你的影响：改动尚未被审查
```

The second is a review the framework could not finish (a reviewer child that
died, a protocol error): a failure of the review machinery never reads as a
verdict on the change. The 预算已耗尽 ride appears only on a checkpoint
opened at budget exhaustion — budgets otherwise settle at run start from the
manifest ("Budget: run-start configuration + exhaustion extend"); the
`review retry` ride only on a checkpoint that offers a funded same-envelope
retry.

Example (fix; context was auto-passed, diagnosis submitted with an unclear
conclusion):

```
╔═ ⚡ SUMMONER CHECKPOINT cp-2 — nonce 1a2b3c4d — PHASE ══════════
║ run: 6ac52e1da5f28d0f · workflow: fix · phase: diagnosis · requirements v1
║
║ ▶ 经过
║   context ✓ 自动通过: Go 1.16 monorepo, entry cmd/server
║
║ ▶ 结论 — 未查清
║   任务列表加载时对已完成的子任务解引用，未判空
║   证据:
║     - task/module.go:88 — player.SubTask 直接取值
║     - task/module_test.go:41 — 复现用例
║   怎么查的: 从报错栈回溯调用链，再用现有用例复现
║   没查清 · 需要你判断:
║     - team 模式下英雄禁用规则是否应该作用于子任务
║   影响: 只影响 team 模式的任务列表加载
╠═ 请决定 — AskUserQuestion（选项须与本页完全一致）
║   continue: 继续下一阶段
║   skip: 跳过该阶段（标 UNVERIFIED / UNREVIEWED，不能伪造 PASS）
║   done: 完成——进入收尾（post-game）
║   stop: 停止——保留状态退出
║ nonce 1a2b3c4d · 其余选项（recall / review / 回城目标）回复 more: 翻页 · 被截断的结论、验收标准与产物全文见 summoner-status
╚════════════════════════════════════
```

## The structured conclusion (phase-submit payload)

A phase submission (`skills/phase-submit/SKILL.md`) states what it
established:

```json
{
  "phase_id": "diagnosis",
  "summary": "nil deref in the task loader",
  "outcome": "confirmed | failed | unclear",
  "conclusion": {
    "summary": "TaskModule.handle() dereferences player.SubTask without a nil check",
    "evidence": ["task/module.go:88", "task/module_test.go:41 reproduces"],
    "approach": "read the call chain from the error site, then ran the existing test",
    "open_questions": [],
    "impact": "every task-list load for a player with a completed sub-task"
  }
}
```

- `summary` (top level) is the short title of the acceptance record and of
  the `经过` line in later checkpoints. The checkpoint body shows the
  **conclusion**, not the summary.
- `outcome` — `confirmed` (established, on evidence), `failed` (the phase's
  goal was not met), `unclear` (holes remain). Omitted = `unclear`.
- **Open questions force `unclear`**: a submission that lists
  `open_questions` cannot claim `confirmed` — the stored outcome is
  `unclear` and the checkpoint asks instead of asserting.
- **Diagnosis must state its conclusion**: a diagnosis submission without
  `conclusion.summary` (the root cause, as a conclusion) or
  `conclusion.approach` (how it was established) is rejected before any
  effect and names the missing field. Every other phase may submit
  conclusion-free; the checkpoint then shows what is missing.
- **Caps (render only)**: summary and approach ≤ 200 runes; impact, one
  evidence item and one open question ≤ 120; evidence ≤ 4 items, open
  questions ≤ 3. What a cap rolls off is counted in the render
  (`…另 N 条，见 summoner-status`) and shown in full in the detail view.
- **Display-bound**: the conclusion and outcome are checkpoint content —
  the Stop hook's display binding covers them, so a conclusion edited after
  display never keeps its binding.
- **Across versions**: a checkpoint that carries neither — every
  pre-0.7.0 checkpoint, a conclusion-free submission that claimed nothing —
  hashes exactly as before, so a run parked at a displayed checkpoint
  survives an upgrade to 0.7.0 and a rollback from it unchanged.
  A checkpoint whose outcome is not `unclear`, or that carries a
  conclusion, is decidable under 0.7.x only. Rolling back to 0.6.1 is
  LOSSY for such a checkpoint, and the document must say exactly how:
  - 0.6.1's Stop hook treats a displayed checkpoint as settled, and the
    first 0.6.1 state save (its gate writes the run on every admitted
    call) rewrites the state with 0.6.1's structs — `outcome` and
    `conclusion` are DROPPED, and `display_digest` no longer matches
    what 0.6.1 recomputes. The answer is then rejected silently (exit 0,
    one `checkpoint.decision_rejected`), and the conclusion is gone.
  - Reinstalling 0.7.x does NOT restore the binding (the digest was
    computed over content that no longer exists): the Stop hook demands a
    re-render, a direct ask is rejected "content changed since display",
    and after re-pasting the checkpoint message it is decidable again —
    with every part of the conclusion reading 未提供.
  - So: do not roll back with a live run waiting at a conclusion-bearing
    decision; if a rollback already happened, re-paste the checkpoint
    under 0.7.x before deciding, and re-submit the conclusion if it still
    matters. (A conclusion-free checkpoint rolls back cleanly.)
- An outcome outside the three values is a malformed payload: denied,
  naming the legal values.

## The detail view (what the render leaves out)

`summoner-status` (default view, or `/summoner:status`) prints a
`checkpoint detail (…)` block right before the checkpoint message. It is
the reference view and it is complete and uncapped:

- the conclusion in full — every part, every item;
- `history conclusions`: each settled phase's conclusion as it was
  accepted, oldest first (the render's `经过` line carries only the titles);
- the complete acceptance criteria with their meaning (including the
  default review axes the fix/new/review/ship routes merge into every
  acceptance);
- the frozen artifact versions, the requirement snapshot, the planned scope;
- the verification environment;
- the menu page projection with the `more:` / `back:` labels;
- every recall target; a pending review retry; an exhaustion extend offer;
- `model:` — the one-line instruction for asking the decision question.

The status lines above it keep the live budget (`budget: …`), the route,
the visits and who is awaited.

## The decision channel (AskUserQuestion) — TWO-TURN by construction

The protocol is **two-turn** — and since 0.7.0 the two turns need no user
turn between them:

1. Turn N carries the checkpoint message (an explanation may precede it) and
   **ends**. The Stop hook verifies the display — the message must contain
   the canonical block — binds it (checkpoint id + nonce + content digest;
   that binding is the sole `Displayed` setter), and **blocks with the exact
   question to ask**: the nonce and page 1's labels, verbatim.
2. The platform continues the turn; turn N+1 IS that ask.

A nonce-carrying ask before the display is bound is DENIED by the gate (exit
2, two-turn instruction), and such an answer is always `decision_rejected` —
the display binding does not exist yet. A displayed checkpoint whose question
was never asked gets the same block on every later sign-off until the
question is raised: the user never types "continue" to bring a dialog up.
A request-tied acknowledgment of the user's request ends the turn even at a
displayed checkpoint (`已按你的要求停止`, `Stopped as you asked`, a stop word
next to 按要求/遵照你的要求/as you asked/per your instructions). A bare stop
word does not — once the checkpoint is displayed, `停止` / `stop` alone is
read as content; the user's explicit choice is the `stop:` option (or their
own words to the model, which make the acknowledgment request-tied).

Stopping stays possible at every step — an explicit stop acknowledgment ends
the turn with the state kept.

The order is fixed: display verification must bind BEFORE any answer can,
otherwise nothing the user was shown constrains what they decided.

After the display is bound, ask ONE question:

- **Question text carries the nonce**, e.g.
  `Checkpoint cp-2 (nonce 1a2b3c4d): 如何推进？` — the Stop hook's block
  hands back the nonce and the labels; copy them from there.
- **Options = ONE PAGE of the rendered menu, verbatim** — at most 4 (the
  platform caps 4 options per question). The gate rejects any offered set
  that is not EXACTLY one persisted page (subset, superset, foreign label,
  hand-paraphrased label — all denied). Both pages of a full menu are legal
  to offer; start with page 1.
- **Navigation (`more:` / `back:`) is content feedback, not a decision.**
  When the user asks for the other page, re-ask the decision question with
  that page's labels — nothing is recorded for navigation (no state change,
  no event; the page validation is the enforcement).
- **Exactly ONE nonce-carrying question per call**: two or more fail closed
  (`decision_rejected`, nothing recorded) — the answers map would be
  ambiguous.
- Every label STARTS with the exact decision keyword — the platform recorder
  maps the lowercased leading word; any other leading word means the
  decision is REJECTED:

| Keyword | Decision | Meaning |
|---------|----------|---------|
| `continue` | CONTINUE | Accept this phase's criteria; next phase opens |
| `skip` | SKIP | Skip the settled next phase honestly (UNVERIFIED/UNREVIEWED) |
| `done` | DONE | Route complete → post-game (CLOSED only after journal persists) |
| `recall` | RECALL | Back to a real history label (default: latest accepted prior phase) |
| `review` | REVIEW_ON_DEMAND | [v] commission an independent ⑤ review of this phase's artifacts |
| `stop` | STOP | Emergency stop: minimal exit journal, POST_GAME_PENDING |
| `accept` | ACCEPT_REQUIREMENT_CHANGE | Close a requirement change and resume (change-confirm only) |

- Anti-example: a label like `[v] 独立审查` (decoration first) maps to nothing —
  write `review: [v] 对本阶段产物发起独立审查`.
- The platform-injected tool_response is the ONLY advancement source for a
  decision (the auto-pass above is the framework's own recorded act). Free
  text, typed responses, quoted text, tool results and subagent messages
  never advance the run. An auto-resolved (afk-timeout) dialog is rejected.

### Menus per checkpoint kind

| Kind | Legal options |
|------|---------------|
| PHASE | continue, skip, done, recall, review, stop |
| CHANGE_CONFIRM | accept, stop |
| RECOVERY | continue, recall, stop |
| REVIEW_RESULT | the full PHASE set — the note text (verdict) guides which options make sense |
| REVIEW_DEBT | review ([v]), skip (finish UNREVIEWED), continue, stop — and, with accepted history, the recall-target page |

`continue:` at a REVIEW_DEBT checkpoint means back to work. While earlier
route phases remain unsettled the run resumes there. When every route phase
is already settled there is nothing to resume into, and the run used to sit
ACTIVE with nothing to open and no checkpoint to close from; `continue:`
then re-enters the last accepted DEVELOPMENT phase (the latest accepted one
when every accepted phase is the review) — a recall, recorded and traced as
one, and PostToolUse answers with a block that names the phase it re-opened.
`skip:` there is the explicit UNREVIEWED finish (post-game).

`continue:` at a REVIEW_RESULT checkpoint follows the same rule: while
phases remain it resumes the route; with the route complete it re-enters
development when the review did not conclude PASS (NEEDS-FIX, BLOCKED), and
it IS the finish (post-game) when the review is settled — there `continue`,
`done` and `skip` all lead to the same place.

### Rejected answers are visible (0.7.0) and content feedback

A decision attempt that fails validation is `checkpoint.decision_rejected`
(nothing recorded) — and PostToolUse answers it with a **block** the model
reads: the reason, what the same callback already changed (a recorded drop,
an applied extension — those are not rolled back; never re-ask a settled
extend, it would extend twice), and the legal next step at the checkpoint.
The whole stdout is that one JSON object; a recorded decision writes
nothing — with one exception: a `continue:` that took effect as a recall
(above) answers with a block naming the re-entered phase, because the
effect differs from what the user clicked.

Any reply that is not an option choice is content feedback — the user typed
words. Text never advances the run. While the run waits, the phase is closed
and writes are denied, so only these moves exist, and the block names them:

1. **It is a request** — do the read-only investigation it asks for (Read /
   Grep / LSP / read-only Bash), report the conclusion, then re-ask the
   decision question exactly (nonce + page 1's labels).
2. **The direction is wrong** — the user chooses `recall:` to reopen the
   phase (offered at phase / review-result / review-debt / recovery
   checkpoints).
3. **The requirement itself moved** — `Skill(summoner:change)` (open →
   apply → accept).
4. **None of these** — only the user's `stop:` ends it (state kept).

Keyword cues: 方案/方向/漏了/不对/应该/边界/错了 (CN), wrong/misses/should/redo
(EN). When in doubt, re-ask — never advance.

### Drop ride-along questions (per-id criteria drops)

The decision call may ALSO carry nonce-free **drop questions** — one per
criterion the user might explicitly remove from the acceptance view
(`信封 criteria 集 = 冻结集 − 显式 drop`, spec §3). They ride the SAME
AskUserQuestion call as the nonce decision question; the gate admits the call
untouched (its nonce predicate keys on the decision question), and
PostToolUse records them:

- **Shape**: exactly two options — `keep: <id>` (the default) and
  `drop: <id>` — naming the SAME criterion id; the question text names the id
  too. Question text must NOT carry the nonce (the nonce binds the decision
  question). One criterion id per question; several questions may ride along.
- **Legal ids**: the checkpoint's own pending criteria ∪ the frozen
  acceptance view (dropping a frozen criterion at a LATER checkpoint is the
  channel's purpose). Anything else is rejected.
- **Recording order**: answered `drop:` entries record BEFORE the decision —
  a `continue:` that re-accepts the criterion CLEARS its drop again
  (acceptance governs; the drop ledger is per id per checkpoint). `keep` and
  unanswered questions record nothing. Replay is a no-op per
  (criterion id, tool_use_id): a durable receipt survives the clearing, and a
  call whose tool_use_id already concluded a decision can never record again
  — a re-delivered callback never re-drops a criterion the user just
  re-accepted. One call may still record drops for SEVERAL ids.
- **Trace**: one `criterion.dropped` per newly recorded drop (checkpoint id +
  criterion id + the platform tool_use_id of the answering call).
- **Failure**: a malformed drop question (missing keep, mismatched ids,
  nonce-carrying, foreign id) or a drop answer that is not one of the two
  labels fails the WHOLE callback — `decision_rejected`, nothing recorded.
  A MULTI-NONCE call (two or more questions carrying the current nonce)
  rejects the whole callback with NOTHING recorded, drops included.
- **Partial answers**: a drop answered while the decision question is not
  answered records the drop observation and rejects the decision (the
  checkpoint stays open).
- **`[v]` before any freeze**: a `review:` answer with NOTHING yet frozen is
  refused (`frozen acceptance view is empty`) — it would park the run in an
  unrecoverable REVIEW_PENDING. The checkpoint stays open for `continue:` /
  `skip:`; the `[v]` becomes legal once an acceptance freezes the view.
  (The terminal DEFAULT review needs no `[v]` at all: a plain `continue:` at
  the review phase's checkpoint opens the envelope channel from ACTIVE —
  spec §1 终局⑤默认强制; an explicit `skip:` of that phase closes it as
  UNREVIEWED.)

### Budget: run-start configuration + exhaustion extend (T18, 0.6.1 rev)

Budgets are **manifest-authored** — no checkpoint asks about them at run
start (user ruling 2026-11-23: 不必在工作流开始时询问 budget):

- **Run-start settlement** — the run settles its budget the moment it
  starts: the manifest's `suggested_budgets` applies as configuration when
  declared (the manifest author is the user — no runtime confirmation),
  else unlimited. An unreadable manifest fails the run start CLOSED rather
  than guessing. The status render's budget line shows the live limits and
  counters (`run-start 已生效`).
- **No budget questions** — a `budget:`-keyworded ride-along is rejected at
  the door and at PostToolUse (whole callback, nothing recorded), with a
  reason pointing at `suggested_budgets`. A plain `continue:`/`skip:` never
  owes a budget answer.
- **Digest binding** — the persisted extend OFFER is checkpoint content (a
  changed offer after display → re-render, re-ask). Applied Limits/Counters
  are NOT content: extending or counting never invalidates the display.
- **Extend ask** — a checkpoint opened while the budget reads exhausted
  carries ONE extension offer (delta = the current limit per SET axis; unset
  axes stay unlimited). Exactly two options: `extend: 追加已显示额度` and
  `extend: 不追加`, riding the decision call the same nonce-free way. Applied
  only from the persisted delta, bound to the answering call; a fresh answer
  at a later exhaustion may extend again.
- **Exhaustion surface (fail-open for the human, closed for the work)** — at
  zero headroom every tool call is denied EXCEPT: `summoner:phase-submit`
  (the exhausted phase must reach its checkpoint), the exact read-only
  status invocation — accepted verb spellings are the bare `summoner-status`
  and explicit plugin-bin paths ending `/hooks/bin/summoner-status`; the
  payload is `{}` or a JSON object literal with NO `<`/`>` in it (any flag,
  redirect-carrying payload, other directory prefix, redirect, or pipe is
  judged normally) — the checkpoint ask itself, `summoner:change` (replan),
  and read-class OUTCOME_UNKNOWN probes. POST_GAME_PENDING runs may
  additionally ask their questionnaire and write ONLY the fixed journal
  path. Never try to work around an exhausted budget — present the
  checkpoint and let the user extend, replan, or stop.
- **Malformed extend rides** — an extend question with foreign labels,
  three options, a carried nonce, or no persisted offer fails the whole call
  (gate exit 2 / `decision_rejected`), the decision included. An extend
  ANSWER that is not one of the two persisted labels rejects the whole
  callback too.
- **Trace**: `budget.extended` per applied extension, bound to the answering
  call's tool_use_id. Run-start settlement emits no budget event (it is
  configuration, visible in the run state and the status render);
  `budget.confirmed` appears only in pre-0.6.1 traces.

## Adaptive checkpoints (auto-pass, 0.7.0)

Most checkpoints exist to collect a judgment; the ones that collect none
cost a dialog each. A phase-submit checkpoint therefore **passes without
asking** when ALL of these hold:

| Condition | Rule |
|-----------|------|
| phase | `context`, `reproduce`, `verify`, `regression` — never `diagnosis`, `fix` or any other phase |
| submission | `outcome: confirmed` AND at least one `evidence` item (a listed open question forces `unclear`) |
| checks | no FAIL among the submission's own mechanical checks |
| budget | no extension offer on the checkpoint, and the budget not exhausted |
| evidence & conclusion | at least one non-blank `evidence` item and a non-blank `conclusion.summary` (blank items are not evidence) |
| not yet shown | the checkpoint was never displayed — once the user has seen it, the decision is theirs |
| something follows | another phase is still owed on the route (an auto-pass opens no checkpoint to decide `done` / `stop` from) |

An auto-pass does exactly what a CONTINUE does, with the framework as the
actor: the checkpoint record is kept and marked decided (`AUTO_PASS`, no
tool_use id), the phase is frozen into the acceptance view
(`accepted_by: auto-pass`), the next visit's budget bucket is authorized,
the run returns to ACTIVE, and one `checkpoint.auto` is traced — never a
`checkpoint.decided` (that needs a real AskUserQuestion call) and never a
`checkpoint.displayed`.

A passed phase renders no checkpoint, so the run keeps moving two ways:

- `summoner-status` prints `auto-passed: 阶段 X 已自动通过 → 立即进入 Y`.
- If the turn ends right after the pass, the Stop hook blocks once with the
  phase to enter — at most once per pass. The same one-shot nudge covers
  every decision that returns the run to ACTIVE with nothing rendered: the
  user's own `continue:` / `skip:`, a requirement change's `accept:`, and a
  recovery `continue:` (a `recall:` opens the phase it returns to, so it
  needs no nudge). A sign-off right after one of them
  is answered once with the next step — the user never types "continue" to
  keep the run alive; when the route is complete the nudge says so and the
  run needs closing (post-game or stop).
- A `skip:` that settles the route's LAST unsettled phase IS the explicit
  UNREVIEWED finish: the run enters post-game (it does not stay ACTIVE with
  nothing to open and no checkpoint to close from). A mid-route skip resumes
  the run ACTIVE with the continuation cue.
- The pass stays visible afterwards: the visit reads `accepted (自动通过)`
  and every later checkpoint's `经过` block shows `✓ 自动通过`.

Everything that owes a judgment stays a human checkpoint: an `unclear` or
`failed` submission, a confirmed one without evidence, a FAIL among its
checks, a checkpoint opened at budget exhaustion. Review-result checkpoints
are never auto-passed.

## CHANGE_CONFIRM checkpoint (requirement-change close)

A requirement change (CHANGE_OPEN) closes through ONE confirm checkpoint —
the single-point close of spec §1 增补 (变更闭环). The checkpoint opens when
the delta is applied (`Skill(summoner:change, apply)` — the gate records
`change.delta_applied`); its render presents the impact assessment for the
user to confirm:

- `note:` — the delta statement (what changed this time);
- the status view's change block — the SAVED development position that
  `accept:` restores (`saved position: current_task … · test_progress … ·
  pending_verifications …`) and the bumped `requirements version`;
- the status view's unmet-criteria list — the newly-stale ids (含 stale 标注),
  i.e. what the change invalidated; untouched accepted results stay valid and
  are NOT re-asked.

The menu is exactly `accept: 接受变更并恢复开发` / `stop: 停止——保留状态退出`
— the label LEADS with `accept` (→ ACCEPT_REQUIREMENT_CHANGE). The confirm
question text carries the checkpoint nonce like every decision. Choosing
`accept:` restores the frozen development position and resumes in place —
no phase-by-phase reopen, no re-run of the pipeline (`change.resumed` is
recorded after the decision); when the change touched a public interface or
an architectural premise, the impact assessment's mandated re-planning is
presented AT this confirm point, never left to the user to remember. If the
change was entered from a checkpoint, that checkpoint was superseded and its
re-rendered successor asks the interrupted question again after the confirm.

## RECOVERY checkpoint

An OUTCOME_UNKNOWN episode that resolved failed/indeterminate opens a
recovery checkpoint: the render lists the probes and outcomes, and the user
decides `continue:` (accept the status quo and restore) / `recall:` / `stop:`.
Mutating actions are never auto-replayed.

### How an episode arises and resolves (spec §5)

- **Crash scans** classify dangling intents by evidence, not guesswork:
  SessionStart (after a restart) marks dangling MUTATING tool intents
  (tool.started on the trace, result never landed) plus dead worker
  delegations; Stop (turn end) marks tools only — a live delegation is not
  a crash. Reviewer delegations never convert (the review lifecycle owns
  them). Worker correlation is FIFO-per-type (SubagentStop carries no
  tool_use id): the crash may be attributed to the WRONG worker identity —
  the error direction is UNDER-MARKING a completed worker (truth "A crashed,
  B completed" can mark B), never hiding an episode; the episode count and
  the recovery checkpoint stay correct. Each mark traces
  `tool.outcome_unknown` with `detected_by` (`session-start` / `stop`).
- **During the episode** writes are denied and the crashed action is never
  auto-replayed. Read-class Bash probes stay allowed — they are the evidence
  channel, exempt from budget exhaustion alongside `summoner:probe-submit`.
- **Resolution** goes ONLY through `Skill(summoner:probe-submit, args=<batch>)`
  (`skills/probe-submit/SKILL.md` is the payload contract — `{op:"resolve",
  unknown_tool_use_ids[], probe_tool_use_ids[]}`, NO outcome field). Evidence
  rules: every unknown's mutation declaration is frozen at admission
  (capability, target, preimage, expect); the probe is the EXACT command
  summoner-status serves (`shasum -a 256 '<target>'` for write/edit —
  unsupported capabilities have none and resolve indeterminate-only); the
  first byte-exact call binds a one-shot ticket, and the PostToolUse matcher
  computes every verdict (accepted / failed / indeterminate) mechanically
  from the observed stdout — outcomes are never accepted on the wire, and a
  lost observation resolves indeterminate (never a second free probe). The
  gate validates the WHOLE batch against the run's trace (marking freshness,
  evidence receipts, ticket binding) before any transition — one bad pair
  resolves nothing. All `accepted` → the pre-episode status restores; any
  `failed`/`indeterminate` → this RECOVERY checkpoint. A re-delivered
  identical batch is an idempotent no-op; a different probe for an
  already-resolved id is rejected.

## Stop asymmetry & recovery

- Stopping is always possible: free-text stop signals (stop/halt/abort/停止/
  终止 as the dominant intent) end the turn with state kept. Emergency stop
  writes a minimal exit journal and leaves POST_GAME_PENDING — the
  questionnaire is owed at next resume, CLOSED comes only after it persists.
- After a crash/interrupt, resume from run state (`/summoner:status`), never
  from re-parsing conversation text. All files on disk survive; in-memory
  analysis is rebuilt from the run's state and trace.
