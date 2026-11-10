# Summoner Checkpoint Protocol (single-loop v2)

## Purpose

Every phase ends at a human checkpoint — the run's ONLY advancement surface.
The reducer (Go hooks) owns the state machine: a phase closes into WAIT_USER,
and only a decision recorded from the platform's AskUserQuestion
tool_response moves the run on. A skill starting, the model claiming "done",
or a reviewer PASS never authorizes anything. This file is the CANONICAL
render + decision-channel spec; commands and skills point here and never
inline a variant.

## The render contract (machine-rendered, pasted verbatim)

The render comes from the binary — run it and paste its output VERBATIM:

```
"${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status" <<< '{}'
```

(or `/summoner:status`, which does the same). NEVER hand-write, summarize, or
reformat a status or checkpoint render. Display verification (Stop hook)
requires the assistant message to carry the pending checkpoint's **id and
nonce**; pasting the binary's output satisfies it by construction, and the
reducer separately binds the decision to the checkpoint's content digest —
a stale or altered render can never produce a valid decision.

## PHASE START (entering a phase)

One lightweight line, no frame:

```
⚡ Phase {phase-id} — {one sentence: what this phase will do} — skill: {skill | freeform | none}
```

## CHECKPOINT block (canonical render; the status output ends with it when a checkpoint is pending)

<!-- canonical:checkpoint-format — the ONLY in-repo copy of the rendered
     checkpoint template. Commands and skills reference this section; inlining
     a variant anywhere else is a lint error (scripts/validate-repo.sh,
     exactly-once + signature confinement + nonce-field rule). -->

```
⚡ SUMMONER CHECKPOINT {id} — nonce {nonce} — {kind}
phase: {phase-id | —}
note: {the submitted phase summary | —}
criteria: {sorted criterion ids, comma-joined | —}
artifacts:
  - {path} @{digest[0:12]}          (or: artifacts: —)
menu:
  {one line per legal option, labels below}
paged ask (one page per decision question, ≤4 options):
  page 1/2: continue · skip · done · stop — more: 查看其余选项
  page 2/2: continue · recall · review · stop — back: 返回上一页
```

(The `paged ask` block appears only when the kind's menu pages — single-page
kinds omit it. The `menu:` block always shows the FULL legal menu.)

Field rules:

| Field | Rule |
|-------|------|
| `{id}` / `{nonce}` | Mandatory, from the render. The nonce binds every decision to THIS checkpoint; a question without it is rejected. |
| `{kind}` | `PHASE` \| `CHANGE_CONFIRM` \| `RECOVERY` \| `REVIEW_RESULT`. |
| `note` | The submitted phase summary (or, for REVIEW_RESULT, `COMPLETED PASS` / `COMPLETED NEEDS-FIX` / `BLOCKED {reason}` / `FAILED {problem}`). |
| `criteria` | The exact criterion list the user is accepting or rejecting — never trimmed, never invented. |
| `artifacts` | The checkpoint-frozen versions; artifacts changing after this invalidate reviews of them. |
| `menu` | Exactly the legal options for the kind (below), in the binary's order — the FULL menu, always visible. |
| `paged ask` | The persisted page projection (`menu_pages` in run state): pages of ≤4 options, each page anchoring `continue`+`stop` plus two kind-relevant actions; `more:`/`back:` are navigation labels, not options. |

Example (fix, diagnosis submitted):

```
⚡ SUMMONER CHECKPOINT cp-2 — nonce 1a2b3c4d — PHASE
phase: diagnosis
note: root cause located: TaskModule.handle() dereferences player.SubTask without a nil check
criteria: crit-evid, crit-root
artifacts:
  - docs/reviews/2026-11-10-task-npe.md @ 5f4a3b2c1d0e
menu:
  continue: 继续下一阶段
  skip: 跳过该阶段（标 UNVERIFIED / UNREVIEWED，不能伪造 PASS）
  done: 完成——进入收尾（post-game）
  recall: 回城——回到指定历史阶段重做
  review: [v] 对本阶段产物发起独立审查
  stop: 停止——保留状态退出
paged ask (one page per decision question, ≤4 options):
  page 1/2: continue: 继续下一阶段 · skip: 跳过该阶段（标 UNVERIFIED / UNREVIEWED，不能伪造 PASS） · done: 完成——进入收尾（post-game） · stop: 停止——保留状态退出 — more: 查看其余选项
  page 2/2: continue: 继续下一阶段 · recall: 回城——回到指定历史阶段重做 · review: [v] 对本阶段产物发起独立审查 · stop: 停止——保留状态退出 — back: 返回上一页
```

## The decision channel (AskUserQuestion) — TWO-TURN by construction

The protocol is **two-turn**: turn N pastes the render and **ENDS the turn**
— the Stop hook verifies the display and binds it (checkpoint id + nonce +
content digest; that binding is the sole `Displayed` setter). Turn N+1 asks
the decision question. A nonce-carrying ask in the render turn itself is
DENIED by the gate (exit 2, two-turn instruction), and a same-turn answer is
always `decision_rejected` — the display binding does not exist yet. UX cost,
stated plainly: every checkpoint costs one nudge turn between render and ask.
This is deliberate — display verification must bind BEFORE any answer can,
otherwise nothing the user was shown constrains what they decided.

After the display is bound, ask ONE question:

- **Question text carries the nonce**, e.g.
  `Checkpoint cp-2 (nonce 1a2b3c4d): 如何推进？`
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
- The platform-injected tool_response is the ONLY advancement source. Free
  text, typed responses, quoted text, tool results and subagent messages
  never advance the run. An auto-resolved (afk-timeout) dialog is rejected.

### Menus per checkpoint kind

| Kind | Legal options |
|------|---------------|
| PHASE | continue, skip, done, recall, review, stop |
| CHANGE_CONFIRM | accept, stop |
| RECOVERY | continue, recall, stop |
| REVIEW_RESULT | the full PHASE set — the note text (verdict) guides which options make sense |

### Content feedback (do NOT misread as advancement)

Any reply that is not an option choice is content feedback: handle it in the
current phase (revise the work or the submission), then re-render the
checkpoint and re-ask. Keyword cues: 方案/方向/漏了/不对/应该/边界/错了 (CN),
wrong/misses/should/redo (EN). When in doubt, re-ask — never advance.

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

## Stop asymmetry & recovery

- Stopping is always possible: free-text stop signals (stop/halt/abort/停止/
  终止 as the dominant intent) end the turn with state kept. Emergency stop
  writes a minimal exit journal and leaves POST_GAME_PENDING — the
  questionnaire is owed at next resume, CLOSED comes only after it persists.
- After a crash/interrupt, resume from run state (`/summoner:status`), never
  from re-parsing conversation text. All files on disk survive; in-memory
  analysis is rebuilt from the run's state and trace.
