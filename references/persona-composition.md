# Summoner Persona Composition Rules

## Three Layers

| Layer | What | Example | Job |
|-------|------|---------|-----|
| Command | User entry point | `/summoner:ship` | The *when* — orchestrates phases and personas |
| Persona | Role + perspective | `code-reviewer` | The *who* — adopts a viewpoint, produces a report |
| Skill | Workflow + exit criteria | `my-test-skill` | The *how* — step-by-step process |

## Composition Rules

1. **A persona is a single role with a single output format.** If you need a second role, create a second persona.
2. **Personas never call other personas.** Composition is the job of commands.
3. **Personas MAY invoke skills.** The *how* lives in skills.
4. **Commands are the only orchestrators.** They decide which personas to fan out and how to merge results.

## Valid Orchestration: Parallel Fan-Out

`/summoner:ship` is the canonical example:

```
/summoner:ship
  ├── (parallel) code-reviewer    → review report
  ├── (parallel) security-auditor → audit report
  └── (parallel) test-engineer    → coverage report
                  ↓
        merge phase (main agent)
                  ↓
        go/no-go decision + risk summary
```

Why this works:
- Each sub-agent operates on the same diff but produces a DIFFERENT perspective
- No dependencies between sub-agents → genuine parallelism
- Each runs in fresh context → main session stays uncluttered
- Merge step is small and benefits from full context → stays in main agent

## Invalid Orchestration (Anti-Patterns)

### Meta-Orchestrator Persona

```
❌ /work-on-pr → meta-orchestrator persona
                    ↓ (decides "needs review")
                code-reviewer persona
                    ↓ (returns)
                meta-orchestrator (paraphrases result)
```

Why this fails:
- Pure routing layer with no domain value
- Adds two paraphrasing hops → information loss + 2× token cost
- The user already knows they want a review; let them call `/summoner:review` directly
- Replicates work that slash commands and the routing tree already do

### Persona Calling Persona

```
❌ code-reviewer → "this looks like a security issue" → spawns security-auditor
```

Why this fails:
- Personas should report findings, not delegate
- If a review finds security concerns, the REVIEWER notes them; the COMMAND decides whether to fan out security-auditor separately
- On Claude Code: subagents cannot spawn other subagents (hard platform constraint)

## Adding a New Persona

1. Create `agents/<role>.md` with these sections: Role, Scope, Review Dimensions, Output Format, Rules, Composition
2. The Composition section must state: "Invoke directly when / Invoke via / Do NOT invoke from another persona"
3. Add to `plugin.json` agents array
4. If the persona enables a new orchestration pattern, document it here
