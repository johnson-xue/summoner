# memory-validator

Structured validator for `summoner.yaml` manifests. Go library + CLI. Same ecosystem niche as [lane](https://github.com/johnson-xue/lane) — referenced by [summoner](https://github.com/johnson-xue/summoner) via `go.mod` (compile-time binding, not a runtime plugin).

## Why

summoner's old `validate-manifest.sh` used grep fallback (fragile substring matching) and **never checked chain→phase references** — a workflow referencing an undeclared phase silently fell back to superpowers defaults, losing project context. This library provides structured `yaml.v3` parsing + cross-field reference validation with AI-friendly structured errors.

## Install (as library)

```go
import "github.com/johnson-xue/memory-validator"
```

```bash
go get github.com/johnson-xue/memory-validator@v0.1.0
```

## Library API

```go
func Validate(path string, opts ...Option) (errors []ValidationError, m Manifest, ok bool)

func WithSkillsDir(dir string) Option

type ValidationError struct {
    Code   string
    Line   int    // 1-based yaml line; 0 = N/A (file-level)
    Column int    // 1-based yaml column; 0 = N/A. flow sequences share .Line, use Column to disambiguate
    Fields map[string]string
}
func (e ValidationError) Error() string  // "<code> <k>=<v> ... [line=N [column=M]]"
```

`WithSkillsDir` (optional): if set, validates each phase's `skill` (non-`"none"`) exists as a subdirectory under `dir`. If not set, skill-existence check is skipped (portability: no hardcoded antia path).

## CLI

```bash
validate-manifest [--skills-dir <dir>] <path-to-summoner.yaml>
#                ↑ --skills-dir MUST precede path (stdlib flag stops at first non-flag arg)
# exit 0 = valid / 1 = invalid
```

Success output (stable, machine-parseable):
```
✓ VALID path=summoner.yaml project=antia-server phases=15 workflows=2 skills_check=skipped
```
Failure output (one structured error per line):
```
✗ INVALID path=summoner.yaml
  - undeclared_phase workflow=bugfix phase=fix line=22 column=25 index=1
```

## Error Code table (stable contract)

| Code | Trigger | Key Fields |
|------|---------|------------|
| `invalid_version` | version ≠ "1" | `expected=1 actual=<v>` |
| `missing_field` | project.name empty / phases empty / workflow missing checkpoints | `field=<name>` |
| `duplicate_key` | any mapping key declared more than once (top-level or nested) | `key=<name> path=<dotted parent path, empty=root> prev_line=<first> line=<cur> [kind=phases|workflows if under those segments]` |
| `undeclared_phase` | chain references undeclared phase | `workflow=<w> phase=<p> line=<chainline> column=<col> index=<0-based pos in chain>` |
| `empty_chain` | workflow chain is empty | `workflow=<w>` |
| `invalid_enum` | checkpoints not after_each|manual|none | `workflow=<w> field=checkpoints actual=<v> expected=after_each|manual|none` |
| `skill_not_found` | phase skill (non-none) not found in skillsDir | `phase=<p> skill=<s>` |
| `parse_error` | yaml syntax error (file-level) | `detail=<msg>` |

## Validation items

1. Basic fields (version, project.name, phases)
2. Phase/workflow duplicate-key detection + nested field duplicate detection (Stage 1 via `yaml.Node`, recursive — covers top-level version/project and phase/workflow internal fields)
3. Workflow field enum/non-empty (chain, checkpoints)
4. chain→phase reference integrity
5. phase→skill existence (optional, via `WithSkillsDir`)
