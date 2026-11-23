# Summoner Workflow Reference

> Loaded on-demand during workflow execution. Not loaded at startup.

## Workflow Definitions (single-loop phase routes)

Framework-owned sequences — ALL seven verbs (fixed order in
`hooks/shared/workflow.go`; the manifest maps each phase to a project
capability, it never rewrites the order):

- `/summoner:fix`: context → diagnosis(M) → reproduce → fix → regression → review
- `/summoner:new`: context → requirements → plan → implement → verify → review
- `/summoner:debug`: context → diagnosis（止于诊断检查点，不改码）
- `/summoner:review`: context → review（manifest `phases.review` verification_spec; one independent reviewer answers「这份变更有哪些问题」）
- `/summoner:ship`: prepare → review → merge → decide（GO/NO-GO；commands/ship.md steps 1-4 verbatim）
- `/summoner:ops`: context → operate（ops 能力承担领域步骤；operate 单验收门，批次编排是 skill 自己的事）
- `/summoner:release`: planning → changelog → publish（fixed framework route; default implementation `skills/summoner-release/SKILL.md`, replaceable via manifest `phases.release`; publish 是变更阶段 — 只读对账探针先行）

(M) = iron law: in fix/debug, writes are gate-denied until the diagnosis
phase is ACCEPTED at its checkpoint. "Write" is the gate's write-class
verdict on the command text (0.7.0). The ruling names categories; each is
covered by its tools' spellings:
- file and process writers, shells, `source`;
- interpreters with inline code (`python3 -c`, `node -e`, `perl -i`, awk
  `system()`, `sed -i`);
- build and run: `make`, `npx`; `go build` / `install` / `get` / `generate`
  / `fmt` / `run`; `npm`, `pnpm`, `yarn`, `bun`, `deno` run / test / build
  / install forms; `pip`, `pipx`, `poetry`, `uv` installs;
- remote and copy (`ssh`, `rsync`, `scp`); downloads (`curl` when it saves,
  `wget`); unpacking and repacking (`tar` x / c / r / u / A, `unzip`);
  `find -exec`; wrappers that run a command (`timeout`, `nice`, …);
- every git subcommand outside the read-only table (`status`, `log`, `diff`,
  `rev-parse`, `ls-files`, …).

Three rules cut across them. A redirection is judged on its own: to a file
it is a write; to `/dev/null`, `/dev/stdout`, `/dev/stderr`, `/dev/tty` or
`/dev/fd/N` it is not; `2>&1` is a descriptor copy. A command NAME bash
computes when it runs (`"$(echo rm)"`, `$X`, `$'…'`, a substitution behind
a wrapper such as `timeout 5 $(…)`) writes — nothing is known about that
program. An interpreter whose program arrives on stdin or in a process
substitution (`python3 <<'EOF'`, `echo code | node`, `python3 <(echo code)`)
writes, and so does one that loads a module (`python3 -m x`, `perl -Mx`,
`ruby -rx`). A heredoc with a QUOTED delimiter is literal text (nothing in
it runs); an unquoted one's substitutions are live and judged.

Reads that look like writes: `go test` and `go vet` (reproducing a failure
is diagnosis work — their output flags `-o` / `-coverprofile` write), `tar
-O` / `--to-stdout`, a LEADING `bash -n`, `unzip -l` / `-p` / `-t` / `-c`,
`npm audit` (`audit fix` writes), `go tool nm` / `objdump`, `python3 -m pip
list` / `show`, `git config --global --list`. `make` is write-class in
every form, `-n` included: GNU make runs `$(shell …)` while parsing.

Until diagnosis is accepted the executable surface is read-only commands,
LSP and the tools the manifest declares read-only; a Bash denial names the
rule it matched. One consequence for projects: a check registered in
`summoner.yaml` whose command is write-class (`make test`) can no longer be
admitted as an L2 check — re-register a read-class line.

Capability keys ≠ route phase ids: the manifest binds `phases.<capability>`
(the ops capability key is `phases.ops`), never a route's internal phase id
(`operate`, `planning`, … live only in `hooks/shared/workflow.go`). Authority
table: `references/capability-map.md`.

Every phase — including skipped ones — closes at a checkpoint; every arrow is
a recorded decision — the user's, or the framework's own auto-pass
(`checkpoint.auto`, 0.7.0; `references/checkpoint-protocol.md`).

## Auto-Skip Conditions

Phases are offered as skippable at their checkpoints (never auto-skipped —
the user confirms; a skipped phase is marked UNVERIFIED/UNREVIEWED):

| Workflow | Phase | Skip condition |
|----------|-------|---------------|
| fix | reproduce | Pure config fix (data-only, no logic change), or diff < 5 lines with no logic change, or user says "I know the fix" |
| fix | regression | Diff < 5 lines, config-only |
| fix | review | Single file, < 30 lines, no auth/data/config changes |
| new | review | Single file, < 100 lines, no new public API; or user says "不用 review" |

<!-- canonical:auto-skip-table — commands reference this table; inlining a
     variant elsewhere is a lint error (scripts/validate-repo.sh). -->

## Requirement Change vs Recall (canonical boundary table)

Both re-enter earlier ground, but they answer different failures (spec §1
增补: 变更闭环 vs §4 recall). Pick by what went wrong, not by convenience:

| | recall（方向错了回城重做） | requirement change（基线移动增量恢复） |
|---|---|---|
| What went wrong | The work answered the WRONG question — direction itself is wrong | The question MOVED — the baseline changed under correct direction |
| Entry | `recall:` option at any checkpoint (default: latest accepted prior phase) | Free-text change request at any moment (incl. mid-phase), or explicitly at a checkpoint — via `Skill(summoner:change)` (spec §1 增补) |
| Runtime effect | Appends a FRESH visit of the target phase; history kept, downstream accepted criteria go stale | Freezes the development position (task, test progress, pending verifications) → CHANGE_OPEN; delta + impact assessment → single-point confirm `accept:` |
| Recovery shape | Redo the recalled phase and everything downstream (checkpoint per phase) | In-place resume at the frozen position after ONE confirm — no phase-by-phase reopen, untouched accepted results stay valid |
| RequirementsVersion | Unchanged | Bumped by every applied delta |
| Stale propagation | Every later visit's criteria go stale (recursively from the target) | Only the delta's `changed`/`affected` ids go stale — 未受影响的已接受结果保持有效 |
| Budget 口径 | A redone phase gets a fresh phase tool budget (recall = rework) | Change open/apply are control-plane calls on the SAME budget; no rework counter unless a review later returns NEEDS-FIX |
| Risk if confused | Treating a moved baseline as a wrong direction wastes every accepted result (重跑流水线) | Treating a wrong direction as a change silently keeps building on a bad premise (只有影响评估说了算的重新规划会被跳过) |

<!-- canonical:change-vs-recall-table — the boundary table lives here;
     commands and skills point at this section, never inline a variant. -->

## Common Rationalizations

| AI thinks... | Reality |
|-------------|---------|
| "The error code is obvious, skip diagnosis" | Diagnosis is iron law — the gate denies writes until it is accepted. Obvious is often wrong. |
| "User skipped phase 2, I'll skip phase 4 too" | Each phase decides independently at its own checkpoint. |
| "This is too simple for checkpoints" | Checkpoints are the Summoner contract. Recorded as Type 5. |
| "User usually says yes, I'll auto-continue" | Advancement is a recorded AskUserQuestion answer, or the framework's auto-pass under its fixed conditions (which no model can produce); free text never advances. |
| "用户描述得很清楚了，直接写码就行" | 没 spec = 假设当需求。requirements/plan 先行。 |
| "这个功能简单，plan 和 spec 合并为一步就行" | Spec ≠ Plan。跳过 plan = 实现顺序靠猜。 |
| "审查员 PASS 了，直接进下一阶段" | PASS 仍留 WAIT_USER——审查结果检查点不自动通过；推进只来自用户决定。 |

## Red Flags

- ✗ Advancing past a checkpoint without a recorded decision (an AskUserQuestion
  answer, or the framework's `checkpoint.auto` — which no model can produce)
- ✗ Hand-writing a checkpoint/status render instead of pasting the
  summoner-status output (display verification binds the canonical block)
- ✗ AskUserQuestion at a checkpoint whose question text lacks the nonce, whose
  option labels do not LEAD with the decision keyword, or whose offered set
  is not exactly one persisted menu page
- ✗ Asking the decision question in the render turn itself (the protocol is
  two-turn: render ends turn N, ask on turn N+1)
- ✗ Misreading content feedback as CONTINUE (e.g. "方案漏了边界" → follow the
  paths the rejection block names: the read-only investigation it asks for,
  or the user's recall / requirement change / stop; then re-ask)
- ✗ Writes before diagnosis acceptance in fix/debug (the gate's exit-2 deny
  IS the iron law, not an error to work around)
- ✗ Skipping diagnosis, or presenting a skipped phase as verified
- ✗ Hardcoding project names or domain skill names in framework output
- ✗ Skipping post-game review after workflow completion
- ✗ Presenting a BLOCKED review as a quality conclusion, or a NEEDS-FIX as PASS

## Verification Checklist

After workflow completion:
- [ ] Every phase opened, was submitted via summoner:phase-submit with a conclusion, and closed at a checkpoint (rendered for the user, or auto-passed by the framework)
- [ ] Every checkpoint render was the binary's output pasted verbatim (id + nonce present)
- [ ] Every decision came from AskUserQuestion (menu labels keyword-led) or is a recorded auto-pass
- [ ] Content feedback replies were handled (not misread as advancement)
- [ ] Skipped phases are recorded as UNVERIFIED/UNREVIEWED — none presented as PASS
- [ ] Post-game review questionnaire presented and journal entry persisted
- [ ] Phase 0 memory retrieval was attempted (or skipped with no DB, stated)
- [ ] Matched patterns presented to user (if any found)

## Ship Merge Report Format (canonical — commands/ship.md points here)

```markdown
## Ship Decision: GO | NO-GO

### Blockers (must fix before ship)
- [Critical finding + file:line]

### Recommended fixes (should fix before ship)
- [Important finding + file:line]

### Acknowledged risks (shipping anyway)
- [Risk + mitigation]

### Rollback plan
- Trigger conditions: {what signals would prompt rollback}
- Rollback procedure: {exact steps}
- Recovery time objective: {target}

### Review report (full)
- [single reviewer's full report; sensitive changes add criteria, not reviewers]
```

<!-- canonical:ship-merge-template — commands/ship.md references this; inlining
     a variant elsewhere is a lint error (scripts/validate-repo.sh). -->
