# FFI Boundary: AI Orchestration ↔ Go Infrastructure

**Version:** 1.0  
**Last Updated:** 2026-07-14  
**Status:** Draft

---

## Overview

This document defines the integration contract between Summoner's AI orchestration layer (skills, commands, agents) and the Go infrastructure layer (context management, validation, database).

### Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  AI Orchestration Layer (Markdown-based)                    │
│  • skills/summoner/SKILL.md                                  │
│  • commands/*.md                                             │
│  • agents/*.md                                               │
└────────────────┬────────────────────────────────────────────┘
                 │
                 │ Bash tool calls
                 ↓
┌─────────────────────────────────────────────────────────────┐
│  Integration Layer (Hooks + CLI)                             │
│  • hooks/pretooluse-skill/main.go → State tracking          │
│  • hooks/session-start/main.go → Context initialization     │
│  • hooks/validate-manifest/main.go → Manifest validation    │
│  • cmd/summoner-ctx/main.go → CLI for context management    │
└────────────────┬────────────────────────────────────────────┘
                 │
                 │ Function calls
                 ↓
┌─────────────────────────────────────────────────────────────┐
│  Go Infrastructure Layer (Business Logic)                    │
│  • internal/context/memory.go → Context & LLM extraction    │
│  • internal/database/db.go → SQLite persistence             │
│  • internal/llm/client.go → LLM API integration             │
└─────────────────────────────────────────────────────────────┘
```

---

## Integration Points

### 1. Phase Execution → Context Save

**When:** After each workflow phase completes  
**AI Layer:** `skills/summoner/SKILL.md` (Phase execution loop)  
**Go Layer:** `cmd/summoner-ctx save`

#### Current Status: ❌ NOT IMPLEMENTED

**Required Implementation:**

After each phase in SKILL.md (around line 150-200), add:

```markdown
## After phase execution:

1. Extract summary from phase output
2. Call context save tool:

```bash
summoner-ctx save \
  --project "${PROJECT_NAME}" \
  --workflow "${WORKFLOW_NAME}" \
  --phase "${PHASE_NAME}" \
  --skill "${SKILL_NAME}" \
  --input phase_output.txt
```
```

#### Command Signature

```bash
summoner-ctx save [flags]

Required Flags:
  --project string    Project name (validated, no path traversal)
  --workflow string   Workflow name (fix/new/ship/debug)
  --phase string      Phase identifier (diagnose/reproduce/fix/verify)
  --skill string      Skill that executed this phase
  --input string      Path to phase output file (validated for path traversal)

Optional Flags:
  --guide string      Path to project guide file (e.g., CLAUDE.md)
```

#### Data Contract

**Input:** Phase output as plain text file
- Format: Unstructured text (skill output)
- Max size: 100MB (enforced in Go layer)
- Encoding: UTF-8

**Output:** JSON to stdout
```json
{
  "phase_id": 123,
  "summary": "Found null pointer in auth handler line 42",
  "summary_quality": 4,
  "token_cost": 1250,
  "chunks_stored": 3
}
```

**Exit Codes:**
- 0: Success
- 1: Validation error (invalid project name, path traversal)
- 2: Database error
- 3: LLM extraction failed (fallback used)

---

### 2. Phase 0 → Context Retrieval

**When:** Start of each workflow (Phase 0: Memory recall)  
**AI Layer:** `skills/summoner/SKILL.md` (Phase 0 logic)  
**Go Layer:** `cmd/summoner-ctx get-context-bundle`

#### Current Status: ❌ NOT IMPLEMENTED

**Required Implementation:**

In SKILL.md Phase 0 (around line 36-95):

```markdown
## Phase 0: Recall previous context

```bash
summoner-ctx get-context-bundle \
  --project "${PROJECT_NAME}" \
  --workflow "${WORKFLOW_NAME}" \
  --max-tokens 1500 \
  --format markdown
```

Parse output and inject into context.
```

#### Command Signature

```bash
summoner-ctx get-context-bundle [flags]

Required Flags:
  --project string     Project name
  --workflow string    Workflow name

Optional Flags:
  --max-tokens int     Token budget (default: 1500, hard cap)
  --format string      Output format: markdown|json (default: markdown)
  --phases strings     Filter specific phases (comma-separated)
```

#### Data Contract

**Output (markdown format):**
```markdown
## Previous Context: ${WORKFLOW_NAME}

### Phase: diagnose (2026-07-13 14:23)
**Skill:** debug-root-cause  
**Quality:** ⭐⭐⭐⭐ (4/5)  
**Summary:** Found null pointer in auth handler line 42. Root cause: missing validation on token refresh path.

### Phase: fix (2026-07-13 14:45)
**Skill:** code-fixer  
**Quality:** ⭐⭐⭐⭐⭐ (5/5)  
**Summary:** Added null check and test coverage. All tests passing.

---
**Token Budget:** 1500 | **Used:** 847 | **Remaining:** 653
```

**Output (json format):**
```json
{
  "project": "my-app",
  "workflow": "fix",
  "phases": [
    {
      "phase_id": 123,
      "phase": "diagnose",
      "skill": "debug-root-cause",
      "timestamp": "2026-07-13T14:23:00Z",
      "summary": "Found null pointer in auth handler line 42",
      "quality": 4,
      "token_cost": 450
    }
  ],
  "token_budget": 1500,
  "tokens_used": 847,
  "tokens_remaining": 653
}
```

#### Token Budget Enforcement

**Degradation Levels** (as per SKILL.md spec):
1. **Normal (≤1200 tokens):** Full summaries, all phases
2. **Level 1 (≤700 tokens):** Truncated summaries, last 3 phases
3. **Level 2 (≤300 tokens):** Phase names only, last 3 phases
4. **Skip (>1500 tokens):** Empty context, log warning

---

### 3. Checkpoint Display → Interactive UI

**When:** After each phase, display checkpoint for user decision  
**AI Layer:** `skills/summoner/SKILL.md` (Checkpoint output)  
**Go Layer:** `cmd/summoner-ctx checkpoint` (optional)

#### Current Status: ⚠️ PARTIAL (Spec defined, but formats diverged)

**Current Behavior:** AI layer outputs text-based checkpoint block (as per `references/checkpoint-protocol.md`)

**Optional Enhancement:** Use Go CLI for interactive checkpoint

```bash
summoner-ctx checkpoint \
  --phase-id 123 \
  --mode interactive
```

#### Two Modes

**Text Mode (current, mandatory):**
```
┌──────────────────────────────────────────────┐
│  ⚡ SUMMONER — Phase 1/5: diagnose           │
│  ✅ 完成内容: Found null pointer in auth.go   │
│  📋 产物: Diagnosis report, stack trace       │
│  ⚠️ 发现: No test coverage for this path      │
│  Next: [enter] [skip] [done] [recall] [stop] │
└──────────────────────────────────────────────┘
```

**Interactive Mode (optional, Go CLI):**
```
│ ✓ Phase 1 (diagnose) — Complete
│ 📋 摘要质量: ⭐⭐⭐⭐ (4/5)
│ 📂 完整输出: phase1_output.txt (3 chunks)
│ 💰 Token 消耗: 1250
│ [continue] [edit] [view] [skip] [stop]
```

**Decision:** Support both modes, specify with `--mode` flag

---

### 4. Manifest Validation → Hook

**When:** Session start, before any workflow execution  
**AI Layer:** Automatic (hook-based)  
**Go Layer:** `hooks/validate-manifest/main.go`

#### Current Status: ✅ IMPLEMENTED

**Integration:** Git hook calls Go binary automatically

```bash
# Called by Claude harness on session start
hooks/validate-manifest/bin/validate-manifest summoner.yaml
```

#### Output Format

**Success:**
```
✓ VALID path=summoner.yaml project=my-app phases=5 workflows=3 skills_check=skipped
```

**Failure:**
```
✗ INVALID path=summoner.yaml
  - phase 'diagnose' references undefined skill 'debug-agent'
  - workflow 'fix' missing required phase1 definition
```

---

## Error Handling Conventions

### Exit Codes

| Code | Meaning | Retry? |
|------|---------|--------|
| 0 | Success | N/A |
| 1 | Validation error (user input) | No - fix input |
| 2 | System error (disk full, permissions) | Maybe - check system |
| 3 | Transient error (LLM API timeout) | Yes - retry |

### Error Output Format

All errors go to stderr in structured format:

```
ERROR: <category>: <message>
DETAIL: <technical details>
FIX: <suggested remediation>
```

**Example:**
```
ERROR: path_traversal: Invalid input file path
DETAIL: Path contains '..' traversal attempt: ../../../etc/passwd
FIX: Provide a path within the project directory
```

---

## Security Considerations

### Input Validation (All Commands)

1. **Project Names:**
   - Max length: 255 characters
   - No path separators: `/`, `\`
   - No null bytes: `\x00`
   - No newlines: `\n`, `\r`

2. **File Paths (`--input`, `--guide`):**
   - Must not contain `..` (path traversal)
   - Max file size: 100MB
   - Must be regular files (not symlinks, devices)

3. **Environment Variables:**
   - `SUMMONER_DB_PATH`: Must be absolute, no traversal
   - `EDITOR`: Must be in allowed list or absolute path

### Secrets Handling

- **Never log API keys** (LLM client suppresses keys in logs)
- **Never echo secret values** in checkpoint summaries
- **Store secrets in environment variables only**

---

## Versioning Strategy

### Protocol Version

Every command supports `--version` flag:

```bash
summoner-ctx --version
# Output: summoner-ctx v0.1.4 (protocol: v1.0)
```

### Compatibility

**Breaking changes** require protocol version bump:
- Change in CLI flag names
- Change in JSON output schema
- Change in exit code meanings

**Non-breaking changes** keep protocol version:
- New optional flags
- Additional JSON fields (append only)
- Performance improvements

---

## Testing Requirements

### Integration Tests

Every FFI boundary must have integration tests:

```bash
# Test Phase Execution → Context Save
test_phase_save() {
  echo "Test output" > phase_output.txt
  summoner-ctx save --project test --workflow fix --phase diagnose --skill debug --input phase_output.txt
  assert_exit_code 0
  assert_json_field "phase_id" "> 0"
}

# Test Path Traversal Protection
test_path_traversal() {
  echo "Malicious" > /tmp/test.txt
  summoner-ctx save --project "../../../etc" --workflow fix --phase p1 --skill s1 --input /tmp/test.txt
  assert_exit_code 1
  assert_stderr_contains "path_traversal"
}
```

### Required Test Coverage

- ✅ Happy path (all commands with valid input)
- ✅ Input validation (path traversal, invalid names)
- ✅ Error paths (disk full, permissions, LLM timeout)
- ✅ Token budget enforcement (degradation levels)
- ✅ Concurrent access (multiple sessions, race conditions)

---

## Implementation Checklist

### P0: Critical (v0.1.5)

- [x] Fix S1: Path traversal in `getDatabasePath()` ✅ DONE
- [x] Fix S2: Command injection in `editInEditor()` ✅ DONE  
- [x] Fix S3: Vendor memory-validator dependency ✅ DONE
- [ ] Add `summoner-ctx save` call to SKILL.md after each phase
- [ ] Add `summoner-ctx get-context-bundle` call to SKILL.md Phase 0
- [ ] Document checkpoint protocol modes in this file

### P1: Important (v0.2.0)

- [ ] Implement token budget enforcement in `get-context-bundle`
- [ ] Add integration tests for all FFI commands
- [ ] Add `--mode` flag to checkpoint command
- [ ] Update checkpoint-protocol.md to reference this document

### P2: Nice to Have (v0.3.0)

- [ ] Add distributed tracing (correlation IDs across AI → Go calls)
- [ ] Add metrics collection (phase duration, LLM latency)
- [ ] Add structured logging (JSON logs for parsing)

---

## References

- **Checkpoint Protocol Spec:** `references/checkpoint-protocol.md`
- **Manifest Schema:** `references/manifest-schema.md`
- **Security Audit:** `MULTI_PERSPECTIVE_REVIEW_v0.1.4.md` (Section 2)
- **Architecture Review:** `MULTI_PERSPECTIVE_REVIEW_v0.1.4.md` (Section 1)

---

## Changelog

- **2026-07-14:** Initial version (v1.0)
  - Defined 4 integration points
  - Specified data contracts and error handling
  - Documented security requirements
  - Created implementation checklist
