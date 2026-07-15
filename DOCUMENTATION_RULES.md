# Documentation Rules for Summoner

## Core Principle

**Only create documentation that serves a specific, ongoing purpose.**

Context preservation should be done through:
- **Code and scripts** (executable, verifiable)
- **Database records** (queryable, structured)
- **Git history** (commits, tags, branches)
- **Configuration files** (machine-readable)

NOT through disposable markdown reports.

---

## ✅ ALLOWED Documentation

### 1. User-Facing Documentation
- `README.md` - Project overview and quick start
- `CONTRIBUTING.md` - Contribution guidelines
- `CHANGELOG.md` - Version history (maintained via release process)
- `docs/commands/*.md` - Command usage guides (if commands exist)

### 2. Persistent Design & Architecture
- `docs/specs/*.md` - Design specifications (only if referenced by code)
- Reference documents in `references/` (protocol specs, schemas)

### 3. Living Documentation
- `CLAUDE.md` - Project instructions for AI agents
- Command definitions in `commands/*.md` (executable by framework)
- Agent definitions in `agents/*.md` (referenced by workflows)

---

## ❌ FORBIDDEN Documentation

### Never Create These:

1. **One-Time Reports**
   - "FINAL_COMPLETION_REPORT.md"
   - "TASK_COMPLETION_SUMMARY.md"
   - "EXECUTION_SUMMARY.md"
   - Any file with "REPORT", "SUMMARY", "FINAL", "COMPLETED" in the name

2. **Version-Specific Reviews**
   - "MULTI_PERSPECTIVE_REVIEW_v0.1.4.md"
   - "RELEASE_REVIEW_v0.1.5.md"
   - "P0_FIXES_COMPLETED.md"
   
3. **Progress Tracking Documents**
   - Use git commits and tags instead
   - Use databases for queryable data
   - Use structured logs for debugging

4. **Meeting Notes / Decision Logs**
   - Decisions go in code comments or ADRs (if you have a process)
   - Not standalone markdown files that rot

---

## Alternative Approaches

### Instead of Markdown Reports, Use:

| Scenario | Bad Approach | Good Approach |
|----------|-------------|---------------|
| Track task completion | `TASK_COMPLETION.md` | Git commits with conventional commit messages |
| Record test results | `TEST_REPORT.md` | CI/CD pipeline output, test databases |
| Document release | `RELEASE_REVIEW_v1.2.3.md` | Git tag annotation + CHANGELOG.md entry |
| Save context between sessions | `SESSION_SUMMARY.md` | SQLite database (e.g., summoner-ctx) |
| Track technical decisions | `DECISION_LOG.md` | Code comments or ADR process if formal |
| Report bugs/issues | `BUG_REPORT.md` | GitHub Issues |

---

## Exception Process

Before creating ANY new `.md` file, ask:

1. **Will this be read more than once?**
   - No → Don't create it
   - Yes → Continue

2. **Will this be maintained when things change?**
   - No → Don't create it
   - Yes → Continue

3. **Can this be replaced by executable code or structured data?**
   - Yes → Use code/data instead
   - No → Consider creating it

4. **Is there an existing place for this information?**
   - Yes → Update existing file
   - No → Create only if passes all above checks

---

## Enforcement

- Any PR adding `.md` files must justify their ongoing value
- Quarterly audit: delete any `.md` file not updated in 6 months (except README, CHANGELOG)
- Default answer to "should I create a report?" is **NO**

---

**Last Updated:** 2026-07-14  
**Rationale:** Information that won't be read, maintained, or used is noise, not documentation.
