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
| `checkpoints` | enum | yes | `after_each`, `after_merge`, or `none`. |

### Workflow Chain Phase Names

Phases in a chain can be:
- A key in `phases` (uses project skill)
- `fix` — freeform fix phase (no skill mapping; user makes changes manually between checkpoints)

## Example

See the my-project `summoner.yaml` for a complete example.
