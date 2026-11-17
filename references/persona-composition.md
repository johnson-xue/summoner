# Summoner Agent Composition Rules

Single-loop design (2026-11-09): the main session runs one loop; only two
agent definitions exist — `agents/reviewer.md` (the ⑤ review agent) and
`agents/worker.md` (restricted worker; on disk, registration pending
behavioral approval). Methodology lives in skills, not in agent personas.

## Composition Rules

1. **Commands are the only orchestrators.** A `/summoner:*` command (or the
   meta-skill routing for it) decides when a delegation happens and what is
   delegated; agents never decide to delegate.
2. **Agents never dispatch other agents.** An agent reports its result and
   returns. Cross-dispatch (an agent deciding another agent is needed) is an
   anti-pattern: it moves orchestration out of the single loop and hides
   routing decisions from the checkpoint protocol.
3. **Agents MAY invoke skills** where their definition says so; the *how*
   lives in skills.

## Dispatch Discipline (anti-pattern)

```
❌ reviewer → "this looks like it needs more test coverage" → dispatches another agent
```

Why this fails:

- The reviewer's job is findings + evidence, not routing. If the reviewer
  believes another pass is needed, that is a finding — the command surfaces
  it to the user at a checkpoint, and the user decides.
- Cross-dispatch replicates work the command and the routing tree already
  own, and produces nested paraphrase hops (information loss at each hop).

The legal delegation shapes are exactly the ones the gate admits:
`summoner:reviewer` (⑤ review) and the approved worker channel — nothing
else.
