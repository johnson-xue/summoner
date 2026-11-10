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
```

Field rules:

| Field | Rule |
|-------|------|
| `{id}` / `{nonce}` | Mandatory, from the render. The nonce binds every decision to THIS checkpoint; a question without it is rejected. |
| `{kind}` | `PHASE` \| `CHANGE_CONFIRM` \| `RECOVERY` \| `REVIEW_RESULT`. |
| `note` | The submitted phase summary (or, for REVIEW_RESULT, `COMPLETED PASS` / `COMPLETED NEEDS-FIX` / `BLOCKED {reason}` / `FAILED {problem}`). |
| `criteria` | The exact criterion list the user is accepting or rejecting — never trimmed, never invented. |
| `artifacts` | The checkpoint-frozen versions; artifacts changing after this invalidate reviews of them. |
| `menu` | Exactly the legal options for the kind (below), in the binary's order. |

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
```

## The decision channel (AskUserQuestion)

After pasting the render, ask ONE question:

- **Question text carries the nonce**, e.g.
  `Checkpoint cp-2 (nonce 1a2b3c4d): 如何推进？`
- **Options = the rendered menu verbatim.** Every label STARTS with the exact
  decision keyword — the platform recorder maps the lowercased leading word;
  any other leading word means the decision is REJECTED:

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

## CHANGE_CONFIRM checkpoint (requirement-change close)

A requirement change (CHANGE_OPEN) closes through ONE confirm checkpoint. Its
render carries the impact assessment: what changed (delta), requirements
version bump, newly-stale criteria, preserved results — and the menu is
exactly `accept: 接受变更并恢复开发` / `stop: 停止——保留状态退出`. Choosing
`accept:` restores the frozen development position and resumes in place — no
phase-by-phase reopen, no re-ask of untouched phases. If the change was
entered from a checkpoint, that checkpoint was superseded and its re-rendered
successor asks the interrupted question again after the confirm.

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
