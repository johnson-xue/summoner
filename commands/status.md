---
description: 只读状态视图 — 渲染当前 run 的阶段路线/visit 状态/已验产物版本/未满足标准/等待谁/默认 recall/预算余量/沙箱档位；有 pending checkpoint 时附检查点块。只读，不改任何状态。「现在什么状态」「run 到哪了」类意图路由到这里。
allowed-tools:
  - Bash("${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status" <<< '{}')
  - Bash("${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status" <<< '{"session_id":"*"}')
---

# /summoner:status

Read-only viewer. It never starts, advances, or mutates a run — rendering only.
No phase loop applies here (per `${CLAUDE_PLUGIN_ROOT}/references/single-loop.md`); the
shared disciplines still do: render verbatim, decisions only via
AskUserQuestion at a rendered checkpoint.

## Steps

1. Run exactly this and nothing else (read-only; `{}` scopes to this
   session's run — SessionStart exports the session id — and names the
   project's other live runs; without it, the project's single live run):

   ```
   "${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status" <<< '{}'
   ```

   When the session id is known (e.g. quoted from a hook message), pass it:

   ```
   "${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status" <<< '{"session_id":"<id>"}'
   ```

2. **Paste the binary's output VERBATIM into your reply.** Never summarize,
   reformat, or hand-write a status or checkpoint render — the canonical format
   lives in `${CLAUDE_PLUGIN_ROOT}/references/checkpoint-protocol.md`, and a pending checkpoint's
   block (its id + nonce) is what display verification binds to.

3. Interpret for the user, briefly, below the pasted block: what is awaited
   (their decision / a reviewer / reconciliation probes), unmet criteria, and
   budget remaining. If the output names multiple live runs, relay all names —
   the viewer refuses to guess, and so must you.

Exit 2 from the binary means unreadable input only; any state problem is
printed in the output itself. The viewer still renders under
`SUMMONER_HOOKS_DISABLE=1` — the hatch disables the enforcement hooks, not
this diagnostic view (a wedged session must stay inspectable). Nothing here
advances the run: decisions go through AskUserQuestion at a rendered
checkpoint, never through this command.
