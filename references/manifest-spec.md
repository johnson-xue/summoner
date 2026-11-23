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
| `version` | yes | The **string** `"2"`. `"1"` loads through zero-touch compat (below); anything else is rejected. |
| `project.name` | yes | Portable memory namespace: starts with a letter/digit, then letters/digits/dot/underscore/hyphen; no `..`. |
| `phases` | yes | Non-empty map of phase id to capability. |
| `check_registry` | no | Array of registered mechanical check definitions. |
| `tool_scope` | no | Declared read-only tool bounds; `allowed_tools` names MCP tools the gate admits as reads during a live run (below). |
| `suggested_budgets` | no | Positive-integer run limits, applied at run start without a confirmation question (0.6.1); omitted → unlimited. |

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
- Provider resolution follows the four-layer order and precedence rules in
  `references/capability-map.md` (explicit manifest binding authoritative;
  the layered order is a discovery preference for unbound capabilities only).
- Providers run inline in the main session. A skill cannot create a second
  orchestration loop, fork context or authorize its own Agent batch. Genuine
  independent work uses the reducer's approved delegation protocol.

The framework routes' phase ids take these capabilities (map order is
irrelevant; binding a capability never inserts, reorders or repeats a phase —
the routes live in `hooks/shared/workflow.go`):

**Capability keys are not route phase ids.** A key names a capability; where
a capability serves one of the seven verbs and carries the same name, the key
IS the verb (`phases.fix`, `phases.review`, `phases.ops`, `phases.release`) —
never the route's internal phase id (`operate`, `planning`, `merge`, … belong
only to `hooks/shared/workflow.go`). The ops capability key is `phases.ops`
while the ops route's domain phase id is `operate`; `phases.operate` does not
exist. Authority table: `references/capability-map.md`.

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
closed as authority. The `phases.<verb>.skill` binding loads in BOTH documented
forms (string and `{default, alternate}`) and the parsed binding is retained
on the runtime projection for consumers; anything beyond the two forms
rejects, while provider-name semantics stay the CLI validator's authority.
T11 alignment: two former divergences are closed — the loader rejects a
`default: none` carrying alternates ("skill none cannot have alternate
providers") and an empty or absent `phases` map ("missing_field: phases must
be a non-empty capability map") exactly as the CLI rejects them; the shapes
are pinned as twins in both suites. Tolerated divergences: `goal`/`scope`/
`expected` are not required on criteria, `rubric` and the `check_ids` listing
are not required, and `env_allow` names are not pattern-checked; one shape
away, a bare capability entry (`phases: {requirements: {}}` — an empty
mapping) is `invalid_phase` to the CLI validator while the loader treats it
as unbound (the runtime consumes nothing from such an entry). Do not treat
the alignment class as enumerated beyond this list. A missing
manifest is the no-manifest flow (defaults only); an **unparseable** manifest
fails closed on every path.

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
  is `BLOCKED`, not permission to run outside it. The full ladder rules are the
  section below.
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

## The check trust ladder (L1/L2/L3)

`trust` fixes **who verifies** a check; the runtime enforces the ladder
mechanically (`hooks/shared/checkrun.go` and the gate → posttooluse join;
spec §3). Checks may be cited at phase-submit as `check://<check_id>`.

- **L1 `trust: framework`** — `command` must be one of the three shipped
  adapter ops: `file-contains (path, needle)`, `digest-match (path, sha256)`,
  `grep-count (path, regex, count)`. The gate runs them IN-PROCESS at
  phase-submit when the citation is validated — never `exec`'d, no shell, no
  network. Bounds: 4 MiB file cap, 2^20 match cap, 2s per check / 4s
  aggregate deadlines (well under the hook timeout). Any other op under
  `framework` trust is BLOCKED unexecuted.
- **L2 `trust: project`** — the model runs the EXACT registered line
  (`command` + `args`, byte-exact) as an ordinary Bash call. The gate admits
  that line only when it is read-class AND it can PAIR with a refused in-band
  sandbox probe: strictly one fresh probe per admitted check (the probe must
  be the latest observation and strictly newer than the probe the PREVIOUS L2
  admission consumed — after every admission a fresh probe is required; a
  probe at seq 2 never authorizes a check at seq 1000). `posttooluse`
  re-verifies definition digest, exact line and the admission's OWN probe
  before writing `check.result`. A pass carries the tier
  `L2(basis:paired_probe@<probe-id>, point-in-time)` — sandbox proof is
  observational and time-scoped, never a standing property. No/declined/
  consumed probe → honest BLOCKED `sandbox_unproven`; definition drift →
  BLOCKED `definition_changed`; unobservable exit → BLOCKED. Never a pass
  without the pairing.
- **L3 network-class** — network commands are NEVER run by the framework:
  exit 2 with a traced BLOCKED `l3_unsupported`. A network check can never
  earn citation credit; drop the ref.

The in-band probe is `"${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-sandbox-probe"`.
Admission of the invocation is BYTE EXACT: the full quoted literal above, or
the plugin-root-resolved absolute twin
(`<pluginroot>/hooks/bin/summoner-sandbox-probe`, derived from the hook
binary's own environment or executable location — never from the Bash
input). Any other spelling — a
leading `HOME=…`/`env HOME=…` assignment, a `$HOME`/`~` relative path, a
foreign directory, an operand, a one-byte difference — is judged as ordinary
Bash and can never produce a probe observation. The adapter attempts one
fresh-exclusive write in `$HOME` (O_EXCL: an existing file is never
overwritten or removed), classifies refusal conservatively
(EPERM/EACCES/EROFS), always exits 0, and prints a wrapper line the
posttooluse hook observes ONLY on the gate-marked probe call — a forged
wrapper line in ordinary command output is never observed.

Citation rules at phase-submit: L1 refs re-run in-gate; an L2 ref needs the
NEWEST observed result for that id to be a PASS under the CURRENT recomputed
digest (stale digest, or missing/blocked/failed → no credit). The
submission's own artifacts must not include `summoner.yaml` — a check
definition inside the artifacts under review carries no mechanical authority.
Only `fail` ticks the failed-check budget; BLOCKED never does.

## v1 zero-touch compatibility

A `version: "1"` document is **translated to its v2 capability-map view in
memory** by the runtime loader and by `validate-manifest` — the upgrade costs
a project nothing: update the plugin, keep the file. The on-disk
summoner.yaml is never rewritten; `validate-manifest` prints
`VALID … (v1 compat, file untouched)`; SessionStart renders the translated
route view so the model routes by it.

Deterministic translation rules (no judgment calls):

| v1 | v2 view |
|---|---|
| `phases.debug` + `phases.diagnose` | `phases.diagnosis` (debug's skill = default, diagnose's = alternate) |
| `phases.rpc`/`subsystem`/`gmt`/`migrate` | `phases.implement` (first present in that order = default, rest = alternates) |
| `phases.<id>.triggers` | dropped — the triggered phases stay capability keys, selected at checkpoints |
| top-level `workflows`/`routing_rules` | dropped — every phase capability survives under its own key |
| `phases.review` carrying only `skill` | dropped — review evidence is the `verification_spec`, never a skill |
| an entry already using a v2 route id | wins — a partially-migrated file is never overridden |

Compat lifts the version wall only: the translated document runs through the
SAME v2 validation, so a malformed v1 file still fails closed. `"1"` is the
only translated version — `"3"` and anything else are rejected.

## Tool scope and suggested budgets

`tool_scope` accepts optional `allowed_tools`, `readable`, `writable` arrays of
unique non-empty strings. `allowed_tools` names the project's read-only
workflow tools (e.g. an MCP code-search server, `mcp__server__tool` by exact
name): during an active run the pretooluse gate admits exactly those tools as
observed reads, before any effect — the project's declaration is it taking
responsibility for their read-only behavior. Declarations are PER TOOL, never
per server: a sibling tool on an allowed server stays denied until named
itself. A tool that only rebuilds an external index (an MCP server's own
`index_repository`, say) may be declared too — the declaration's only promise
is that it does not write project files. **The list admits MCP tools only**:
an entry for a harness tool (`SendMessage`, `Monitor`, …) is ignored, so the
list can never launder a denial past the gate.

LSP navigation and a small set of harness tools are admitted without a
declaration — WebFetch, WebSearch, TaskGet, TaskList, TodoWrite,
ScheduleWakeup, ListAgents — each with a schema verified to carry no write,
delegation, or process-control semantics. Every other harness tool stays
denied during a live run, by name: `Monitor` (its `command` runs a shell —
a bypass of the Bash ladder), `TaskStop` (kills background tasks, including
write-class commands, with no OUTCOME_UNKNOWN record), `TaskCreate` /
`TaskUpdate` (assignee semantics unverified — may address another agent),
`SendMessage` (a cross-session channel), and the worktree/plan session
switches. Admission to that set requires a schema review, not a
harmless-sounding name. Since 0.6.1 the hook input carries no tool
annotations, so a host-side `readOnlyHint` cannot substitute for the
declaration. The declaration never widens anything else: modeled tools (Bash,
Edit/Write, Agent, AskUserQuestion, …) route through their own admission
branches and never consult the list, phase gates and the write matrix are
untouched, and a manifest that fails to load narrows admission back to deny
(fail-closed). `readable`/`writable` remain declarations the runtime does not
act on; native permissions and sandbox restrictions are never bypassed.

`suggested_budgets` accepts optional positive integers for `phase_tool_calls`,
`failed_checks`, `run_rework`, `delegations`, `check_timeout_seconds`. Since
0.6.1 these **apply as run-start configuration** — each run starts with
exactly these limits, with no confirmation question (the manifest author is
the user; ruling 2026-11-23). Omit the object to run unlimited. They are not
recommended research-derived numbers. No token budget is supported.
Exhaustion still surfaces an extend offer at the next checkpoint
(`checkpoint-protocol.md` T18). The field does not implement check execution
or new tool-permission plumbing.

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
