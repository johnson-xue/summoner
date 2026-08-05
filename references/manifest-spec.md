# summoner.yaml Manifest Specification

## Overview

Each project using Summoner must have a `summoner.yaml` at its root. This file declares which skills the project provides for each workflow phase. It is the project's "AI capability manifest" — analogous to a Makefile that maps targets to commands.

## Schema

### Top-Level

| Field | Type | Required | Description |
|-------|------|:--------:|-------------|
| `version` | string | yes | Manifest schema version. Currently `"1"`. |
| `project.name` | string | yes | Human-readable project name for journal entries. |
| `phases` | map | yes | Phase name → skill mapping. |
| `workflows` | map | no | Composite workflow definitions (chains and fan-outs). |

### Phase Entry

| Field | Type | Required | Description |
|-------|------|:--------:|-------------|
| `skill` | string | yes | Skill name to invoke. `"none"` means explicit no-capability. Omit to fall back to superpowers default. |
| `triggers` | list | no | Other phases this phase may trigger during execution. |

### Reserved Phase Names

These verbs are recognized by Summoner commands. Projects map them to their domain skills:

| Phase | Default Skill | Purpose |
|-------|--------------|---------|
| `define` | `superpowers:brainstorming` | Requirements and design |
| `plan` | `superpowers:writing-plans` | Task decomposition |
| `debug` | — | Root cause diagnosis |
| `config` | — | Configuration inspection |
| `test` | — | Test execution |
| `verify` | — | Regression verification (reuses test skill) |
| `reproduce` | — | Write reproduction test (reuses test skill) |
| `ops` | — | Server operations |
| `subsystem` | — | New module creation |
| `rpc` | — | TCP service interface |
| `gmt` | — | Admin/backoffice tools |
| `migrate` | — | Database migrations |
| `review` | `superpowers:requesting-code-review` | Code review |
| `security` | — | Security audit |
| `docs` | — | Documentation generation |
| `worktree` | — | Git worktree management |

### Workflow Entry

| Field | Type | Required | Description |
|-------|------|:--------:|-------------|
| `chain` | list | one of chain/fan_out | Sequential phase names to execute in order. |
| `fan_out` | list | one of chain/fan_out | Parallel persona invocations. |
| `merge` | string | with fan_out | Phase to merge fan-out results. |
| `checkpoints` | enum | yes | `after_each`, `after_merge`, `after_node`, or `none`. |

### Workflow Chain Phase Names

Phases in a chain can be:
- A key in `phases` (uses project skill)
- `fix` — freeform fix phase (no skill mapping; user makes changes manually between checkpoints)

## Node Types

A **node type** IS a phase entry — the framework does not predeclare workflow graphs, only node types (spec §2.6). The `phases` map above doubles as the node-type registry: each entry's `skill` is the closed-loop agent that runs ① Ingest+Validate → ⓪ → ② Work → ③ Test → ④ Handoff for that node. A graph plan references a node type by its phase key (e.g. `diagnose`, `subsystem`, `rpc`) and overrides per-instance fields (`label`, `exit_criteria`, `max_inner_turns`) inline in the `summoner-task-graph` block.

There is no separate "node types" key in the manifest — adding a new node type means adding a phase entry. This keeps the framework project-agnostic: the graph walker reads node ids from the plan block and resolves their `skill` via the same `phases` map the chain mode uses.

## Conditional Routing Rules

Conditional routing is declarative, not computed. A `routing_rules:` map declares rules the walker applies as a pure table lookup (spec §2.6.1 — no model, no script):

```yaml
routing_rules:
  route_by_diagnosis:
    input_field: routing_tag      # field the walker reads from the handoff envelope
    map:
      logic: diagnose_fix         # value → target node id
      rpc: rpc_fix
      subsystem: subsystem_fix
      migrate: migrate_fix
      gmt: gmt_fix
  route_by_function_type:
    input_field: function_type
    map:
      subsystem: subsystem
      rpc: rpc
      gmt: gmt
      migrate: migrate
```

- `input_field`: the handoff-envelope field the walker reads (e.g. `routing_tag`, `function_type`). The producing node sets this field on ④ Handoff.
- `map`: value → target node id. The walker looks up the field's value in this map; the result is the next node. No fuzzy matching, no fallback — an unmapped value is a routing error surfaced to the human (HALT with reason).

This is the only routing mechanism the walker uses for conditional edges. Unconditional edges live in the graph's `edges:` list (declared in the plan block, not the manifest). The manifest declares the rule tables; the plan block declares the graph topology.

## Example

See the my-project `summoner.yaml` for a complete example.
