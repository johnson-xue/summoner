# summoner.yaml Manifest v2

## Purpose

The project-root `summoner.yaml` is a **capability map**, not an execution plan.
The main session selects inline skills; framework commands own phase order; the
same Go reducer owns checkpoints, authorization, evidence and budgets. A plan
can describe tasks, dependencies and acceptance criteria, never executable
manifest topology.

The manifest supplies **capabilities ONLY — never topology**: all seven
framework verbs (`fix`/`new`/`debug`/`review`/`ship`/`ops`/`release`) own
fixed phase routes in `hooks/shared/workflow.go`; a manifest entry binds what
a phase uses, never whether, when or how often it runs.

The schema is `references/summoner.schema.json`. The inline validator in
`hooks/validate-manifest/main.go` additionally checks unique ids, provider
selection, registry references and recomputed check-definition digests.

## Top-level fields

| Field | Required | Meaning |
|---|:---:|---|
| `version` | yes | The **string** `"2"`; other versions are rejected, not adapted. |
| `project.name` | yes | Portable memory namespace: starts with a letter/digit, then letters/digits/dot/underscore/hyphen; no `..`. |
| `phases` | yes | Non-empty map of phase id to capability. |
| `check_registry` | no | Array of registered mechanical check definitions. |
| `tool_scope` | no | Requested tool/read/write bounds, not a permission grant. |
| `suggested_budgets` | no | Positive-integer suggestions presented at the first checkpoint for user confirmation. |

Unknown fields are errors at every level. YAML must be one JSON-compatible
object: string keys, no duplicate keys, aliases, merges, nulls or custom tags.
Keys use lowercase letters/digits/underscore/hyphen and start with a letter.
Phase, check and criterion ids use `^[a-z][a-z0-9_-]*$`. Retired topology keys
are rejected even when nested. Quote strings that look like YAML booleans or
numbers, including `version`.

## Phase capabilities and providers

A normal phase has only `skill`. The short form names the **single default**
provider. The expanded form explicitly distinguishes `default` from `alternate`:

```yaml
version: "2"
project:
  name: sample-project
phases:
  requirements:
    skill: superpowers:brainstorming
  diagnosis:
    skill:
      default: project-diagnosis
      alternate: [superpowers:systematic-debugging]
  ops:
    skill: none
  review:
    verification_spec:
      criteria: []
      check_ids: []
      readable: []
      rubric: plugin:agents/reviewer.md
```

- `skill: name` means exactly `skill: {default: name}`. Expanded bindings
  require one default. Alternate names must be unique, distinct from the
  default and not `none`. `default: none` cannot have alternates.
- Alternates are choices for work **within** a phase, not a sequence, fan-out,
  or automatic fallback when the chosen skill cannot be found.
- Local names and `plugin:skill` names use letters/digits/dot/underscore/hyphen
  (each part starts with a letter/digit). Resolve the selected provider before
  work. An explicitly configured but unavailable skill stops in the main
  session with an explanation; never silently substitute another provider.
- A missing phase uses the disclosed built-in capability. `skill: none` is an
  explicit lack of capability, **not** permission to skip diagnosis or advance
  a checkpoint. A user may choose how to supply the missing capability at the
  existing gate.
- Providers run inline in the main session. A skill cannot create a second
  orchestration loop, fork context or authorize its own Agent batch. Genuine
  independent work uses the reducer's approved delegation protocol.

The framework routes' phase ids take these capabilities (map order is
irrelevant; binding a capability never inserts, reorders or repeats a phase —
the routes live in `hooks/shared/workflow.go`):

| Capability | Built-in inline provider / meaning |
|---|---|
| `requirements` | `superpowers:brainstorming` |
| `plan` | `superpowers:writing-plans` |
| `implement`, `fix` | `superpowers:executing-plans`, under the existing reducer gates |
| `test`, `reproduce` | `superpowers:test-driven-development` |
| `diagnosis` | `superpowers:systematic-debugging` (diagnosis must be accepted before fixes) |
| `verify`, `regression` | `superpowers:verification-before-completion` |
| `ops` | No generic deployment capability; configure a project provider. |
| `review` | `verification_spec`, not a skill or persona roster. |

Other project capability ids are allowed, but declaring one does not insert a
phase into a framework route. For example, old `define`/`debug` bindings should
be moved to `requirements`/`diagnosis` when targeting those fixed phases; the
validator does not guess aliases or migrate them automatically. Release's
project-specific provider can be declared under `phases.release`.

## Review verification_spec

`phases.review` has **only** `verification_spec`. Projects define **what to
verify**; one independent reviewer determines **how to verify it** in a fresh,
restricted context. Sensitive changes add criteria, not fixed extra reviewers.

| Field | Type | Meaning |
|---|---|---|
| `rubric` | non-empty string, required | Pointer to the default judgment/evidence rubric. |
| `criteria` | array, optional | Additional acceptance criteria; ids must be unique. |
| `check_ids` | array of unique bare ids, optional | Allowed checks, each registered in `check_registry`. |
| `readable` | array of unique non-empty strings, optional | Additional readable artifacts/specification paths. |

Paths in project rubrics and readable scope are project-relative. A rubric
starting with `plugin:` is relative to the installed plugin root; the default
`plugin:agents/reviewer.md` points at the shipped evidence/review contract.
These are declarations for envelope construction, not permission to read
unapproved paths. Empty addition lists do not remove the frozen requirements
or artifact scope.

Each criterion uses the `shared.Criterion` field names: `id`, `goal`, `scope`,
`method`, `expected`, optional `required`, `check_id` and `rubric`. The first five
are required non-empty strings. `method` is `mechanical` or `judgment`.
Set `required: true` explicitly for a blocking criterion (the wire default is
false). A mechanical criterion must reference a check registered **and** listed
in `check_ids`. Judgment cites its own rubric or inherits the spec rubric when
absent/empty; it cannot cite a non-empty `check_id`. `expected` should include
boundary and should-fail cases, not merely a happy-path assertion.

These are **additions**, never replacements for accepted criteria. Freeze them
with the accepted requirement version before review. The envelope's criteria
must equal the frozen set minus recorded, explicit user drops. Changing criteria
after acceptance requires the same versioned change/acceptance mechanism.

**Runtime consumption (T17)**: the hooks read `phases.review.verification_spec.criteria`
ids and `check_registry` directly (`hooks/shared/manifest.go`). Three effects:
(1) on the standalone `/summoner:review` and `/summoner:ship` routes, EVERY
phase submission is machine-checked (fix round: not just the pre-review
context/prepare entry) — its `criteria_ids` must come from the effective
verification_spec (the framework default full-rubric axes PLUS these declared
additions); anything else is a traced `phase_submitted.rejected`. Dev routes
(fix/new/debug) stay unconstrained. (2) `check_registry` is the registry
`summoner:review-submit` validates mechanical envelope criteria against.
(3) the loader enforces the validator's invariants for everything it consumes
(fix round): retired keys, aliases, nulls, duplicate keys, exact CheckDef
entry shape, unique check ids, trust enum, recomputed digests, and the
mechanical/judgment citation split — a validator-invalid manifest fails
closed as authority. Tolerated divergences: `goal`/`scope`/`expected` are not
required on criteria, `rubric` and the `check_ids` listing are not required,
and `env_allow` names are not pattern-checked. A missing manifest is the
no-manifest flow (defaults only); an **unparseable** manifest fails closed on
every path.

## Check registry and digest freeze

Every registry entry uses **exactly** the JSON names of `shared.CheckDef` in
`hooks/shared/envelope.go`:

```yaml
check_registry:
  - check_id: unit
    command: go
    args: [test, ./...]
    cwd: .
    env_allow: [PATH]
    digest: d177246245d54d344e2dc1b623abf85e4137d724d7eff0c2e79a8e7cf68d63d4
    trust: project
```

- `check_id`, `command`, `digest`, `trust` are required. Ids are unique.
- `command` is one executable name/path, not a shell command line. `args` is an
  optional string array. `cwd` is an optional working directory. `env_allow`
  is an optional list of unique environment-variable names, not key/value data.
- `trust` is `framework` or `project`. Declaring `framework` does **not** promote
  an arbitrary project command to L1: hooks authorize only approved, shipped
  adapters. L2 project checks require an active sandbox; unavailable isolation
  is `BLOCKED`, not permission to run outside it.
- `digest` is lowercase SHA-256 from **`shared.CheckDefDigest`**: clear the stored
  digest, marshal the Go struct as JSON (its field order and `omitempty` rules),
  hash those bytes. Do not hash raw YAML or alphabetically sorted JSON. All
  content fields, including `trust`, participate; the digest label does not.
  Validation never executes a check. A missing/stale digest is rejected with
  the recomputed value so the author can review and update the declaration.
- Structural schema validation cannot establish digest equality, duplicate
  ids with differing definitions, or cross-references; run the CLI as well.

The manifest's `verification_spec.check_ids: [unit]` is **not** the frozen
carrier. Envelope construction uses the registry digest to produce:

```yaml
verification_scope:
  check_ids:
    - unit@d177246245d54d344e2dc1b623abf85e4137d724d7eff0c2e79a8e7cf68d63d4
```

With a registry present, `shared.ValidateEnvelope` rejects a mechanical check's
bare, missing or malformed reference. Any definition change must upgrade the
envelope; execution rechecks the content digest. A check definition modified in
the reviewed diff cannot certify itself: downgrade that criterion to judgment
unless the reviewer verifies the check against the baseline.

## Tool scope and suggested budgets

`tool_scope` accepts optional `allowed_tools`, `readable`, `writable` arrays of
unique non-empty strings. These describe requested bounds. They do not bypass
phase gates, a reviewer's actual tool allow-list, native permissions or sandbox
restrictions. The validator checks declarations, not effective runtime access.

`suggested_budgets` accepts optional positive integers for `phase_tool_calls`,
`failed_checks`, `run_rework`, `delegations`, `check_timeout_seconds`. These are
**suggestions, surfaced at the first checkpoint for user confirmation**, not
pre-authorized limits or recommended research-derived numbers. Omit the object
to make no suggestion. No token budget is supported. This manifest migration
does not activate them: run-start limits stay zero/unlimited until the separate
runtime confirmation wiring applies user choices (T10+). It also does not
implement check execution or new tool-permission plumbing.

## Setup and validation

Run from the project directory, using the actual installed plugin root:

```bash
"${CLAUDE_PLUGIN_ROOT}/scripts/summoner-setup.sh" --quick --project sample-project
"${CLAUDE_PLUGIN_ROOT}/scripts/validate-manifest.sh" summoner.yaml
"${CLAUDE_PLUGIN_ROOT}/scripts/validate-manifest.sh" summoner.yaml --skills-dir .claude/skills
```

All three generators (`scripts/summoner-init.sh`, `scripts/summoner-bp.sh`,
`scripts/summoner-setup.sh`) share one v2 emitter. `--quick` never reads stdin;
`--project` supplies the namespace (otherwise the directory name is used).
`init` modes 1/2/3 select interactive/quick/manual input. Without quick mode,
EOF cancels rather than granting confirmation. `--force` explicitly replaces
an existing manifest; there is no legacy merge. A candidate is validated before
atomic rename, so failures leave an existing manifest intact. Setup validates
existing files too and initializes the version-independent memory DB.

The CLI exits 0 for valid, 1 for invalid/unreadable, 2 for usage errors.
`--project-name` emits only the validated namespace for setup; it does not skip
any validation. `--skills-dir` requires local providers (including alternates)
to have a regular `SKILL.md` under that directory. Plugin-qualified names are
checked for syntax but resolved at runtime, not proven installed by this check.
The shell launcher forwards all flags and fails closed if no binary is present;
there is no grep-based fallback. No generator has `--dry-run`: test in a temporary
project and redirect `SUMMONER_MEMORY_DIR` there when testing setup.

Golden examples live under `tests/fixtures/manifests/`: minimal, full registry,
generator output, local/plugin skills, and intentionally rejected legacy input.

## Migration from v1

Changing only `version` is insufficient. Remove executable topology and map
capabilities to the fixed phase ids instead:

| Removed field | v2 replacement |
|---|---|
| `workflows`, `chain`, `graph` | `phases` capability map; commands own order, plan documents describe tasks. |
| `fan_out`, review skill/persona selection | `phases.review.verification_spec`; one independent reviewer, additional criteria as needed. |
| `checkpoints` enum | Mandatory reducer checkpoint after every phase. No manifest switch can disable it. |
| `routing_rules`, `triggers`, `mode` | Phase capabilities with one default provider and explicitly selected alternates; no topology or mode selector. |

No v1 parser or generator remains. Missing manifest and invalid manifest are
not equivalent: a missing file uses disclosed built-in capabilities in the
same reducer (`references/no-manifest-flow.md`); an invalid or legacy file must
be corrected, not silently ignored.
