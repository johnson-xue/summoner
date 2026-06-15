# Summoner Changelog

## 0.1.2 — 2026-06-15

### Security Fixes (P0)
- **Shell Injection in `summoner-init.sh` [High]:** All user inputs sanitized with character whitelist `[a-zA-Z0-9:_-]` to prevent YAML structure corruption
- **Python Path Injection in `validate-manifest.sh` [Medium]:** Replaced `open('$MANIFEST')` with `sys.argv[1]` to prevent arbitrary code execution via command-line parameter
- **Path Traversal in session-start Hook [Medium]:** `projectName` now validated via `filepath.Base()` check before constructing DB file path
- **Unchecked `os.MkdirAll` Error [Bug]:** Directory creation failure now properly logged instead of silently continuing

### Workflow Logic Improvements
- **Checkpoint Protocol:** `VERBOSE` signal added to ambiguity resolution priority chain (`STOP > RECALL > DONE > VERBOSE > SKIP > CONTINUE`)
- **Post-Game Review Collision Rules:** Type 2/3 intersection clarified — when multiple types trigger, only the highest-priority questionnaire is presented
- **No Manifest Handling:** Iron law enforcement — Phase 3 of `/summoner:new` now presents interactive menu rather than silently falling back to generic skills

### Code Quality
- **`ProjectDir()` Fallback:** Handles `os.Getwd()` error by returning stable sentinel path instead of empty string
- **Go Hooks Reliability:** Error handling hardened across all 3 hook binaries (session-start, pretooluse-skill, stop)

### Documentation
- README: Added sqlite3 CLI prerequisite + "Restart Claude Code" instruction after installation
- GitHub Issue templates: bug report and feature request with structured fields and labels

## 0.1.1 — 2026-06-11

### Phase 3 Routing Fix (Issue #1)
- **No Manifest Handling (Hybrid A+B):** When `summoner.yaml` is missing, Phase 3 no longer silently falls back to generic skills. Instead, presents an interactive 3-option menu:
  - [1] Pause to create summoner.yaml (recommended — runs summoner-init.sh)
  - [2] Manually specify a skill name for this session
  - [3] Use generic skill with explicit warning about missing project conventions
- **Phase 3 Routing:** Explicit routing logic for subsystem/RPC/GMT function types when manifest is available
- **Iron Law:** Never silently fall back to generic execution — always surface the choice to the user

### Documentation
- Improved README with "Report Issues" / "Feedback Needed" guidance
- Added GitHub Issue templates (bug report, feature request)
- Updated workflow documentation to reference No Manifest Handling

## 0.1.0 — 2026-06-09

### Core Framework
- 6 slash commands: `/summoner:fix`, `/summoner:new`, `/summoner:ship`, `/summoner:debug`, `/summoner:ops`, `/summoner:review`
- Meta-skill routing hub with `summoner.yaml` manifest resolution
- Checkpoint Protocol: 5 interrupt signals (continue/skip/done/recall/stop) with ambiguity resolution
- Post-Game Review: 5 questionnaire types (direction correction / phase skip / knowledge injection / full completion / verbosity complaint)
- 3 reusable personas: `code-reviewer`, `security-auditor`, `test-engineer`

### Memory Chain
- SQLite-backed cross-session pattern storage with WAL mode
- Phase 0 memory retrieval: feature extraction → Top-5 matching → ≤1500 token budget
- Namespace isolation via `project.name` in summoner.yaml
- Graceful degradation: Normal → Level 1 (Top-3) → Level 2 (Top-1) → Skip
- 5 seed patterns from common AI coding mistakes
- Memory lifecycle: hits-based priority (low → medium → high → candidate → archived)

### Developer Experience
- `summoner-init.sh`: interactive wizard for project onboarding
- `validate-manifest.sh`: YAML + JSON Schema validation (jsonschema fallback to grep)
- JSON Schema (`summoner.schema.json`): structural manifest validation
- Adaptive fan-out thresholds in `/summoner:ship` based on diff size
- `allowed-tools` and `when_to_use` frontmatter for skills
- Complete reference documentation (5 protocol specs)

### Design Quality
- Zero project-specific hardcoded references
- Claude Code plugin marketplace ready (`.claude-plugin/plugin.json`)
- 27 files, MIT license
