# Summoner Changelog

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
