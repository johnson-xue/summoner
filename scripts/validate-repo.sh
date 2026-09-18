#!/bin/bash
# validate-repo.sh — P1 mechanical invariants for the Summoner repo.
#
# Every check here maps to a defect that actually shipped:
#   - version drift        (root plugin.json was once stuck at 0.1.5 while the
#                           other two manifests moved on)
#   - manifest omissions   (auto-setup-summoner + review-agent missing from
#                           manifests; name≠dirname for skills/release)
#   - dead references      (scripts/cleanup-traces.sh was documented but never existed)
#   - hardcoded names      (antia-* routing bindings in commands/debug.md)
#   - duplicate hook trees (hooks/hooks/ — a 33MB diverged fork of hooks/)
#
# Scope rules (deliberate, see the 2026-09 adversarial-panel roadmap):
#   - The project-name denylist is enforced on ROUTING layers
#     (commands/, agents/, scripts/, .gemini/commands/ — case-insensitive).
#     Example/illustration usage in skills/*/SKILL.md, references/ protocol
#     samples and Go test fixtures is exempt — the defect class is "binding
#     instructions", not "examples".
#   - Tag-existence is enforced on the default branch (master) only, so
#     release/* branches with older manifest versions don't fail spuriously.
#   - Canonical-marker counting is scoped to live layers (commands/, skills/,
#     references/, agents/) so documenting the convention in CHANGELOG or
#     CLAUDE.md cannot break the build.
#
# Audit provenance: the hostile canary audit (2026-09-17) mutated 20+ defects
# through this script; every scope seam it found (version-key crash → green,
# references/ confinement gap, .gemini/casing denylist gap, depth-1 banned
# names, output self-contradiction after ✗ lines) is closed below.
#
# Exit 0 = green, 1 = violations. Pure repo layer — zero context-window cost.

set -u

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(dirname "$SCRIPT_DIR")"
cd "$ROOT"

ERRORS=0
fail() { echo "✗ $1"; ERRORS=$((ERRORS + 1)); }
ok()   { echo "✓ $1"; }

# ---------------------------------------------------------------- 1. versions
VERSIONS=$(python3 - <<'EOF'
import json, sys
v = []
for p in ["plugin.json", ".claude-plugin/plugin.json", ".claude-plugin/marketplace.json"]:
    try:
        d = json.load(open(p))
    except Exception as e:
        print(f"✗ {p}: does not parse ({e})")
        sys.exit(1)
    ver = d.get("version")
    if ver is None and isinstance(d.get("plugins"), list) and d["plugins"]:
        ver = d["plugins"][0].get("version")
    if not ver:
        print(f"✗ {p}: no version key")
        sys.exit(1)
    v.append(ver)
print("\n".join(v))
EOF
)
EXTRACT_RC=$?
if [[ $EXTRACT_RC -ne 0 ]]; then
  fail "version extraction failed (malformed manifest or missing version key)"
elif [[ "$(sort -u <<< "$VERSIONS" | wc -l | tr -d ' ')" == "1" ]]; then
  VERSION="$(head -1 <<< "$VERSIONS")"
  ok "version consistent across 3 manifests: $VERSION"
else
  fail "version drift across manifests: $(tr '\n' ' ' <<< "$VERSIONS")"
fi

# tag v{version} must exist when on the default branch
if [[ "${VERSION:-}" != "" ]]; then
  BRANCH="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)"
  if [[ "$BRANCH" == "master" ]]; then
    if git rev-parse -q --verify "refs/tags/v$VERSION" >/dev/null 2>&1; then
      ok "tag v$VERSION exists (on default branch)"
    else
      fail "on master but tag v$VERSION does not exist"
    fi
  fi
fi

# ------------------------------------------------- 2. manifest ↔ filesystem
# (if-python-then-ok form: a plain invocation + $? would report ✓ after ✗ —
# the `||` assignment resets $?, found in review)
if python3 - <<'EOF'
import json, os, re, sys

fails = []
root = json.load(open("plugin.json"))

# root plugin.json skills[]: path exists, name == dirname, frontmatter name == name
listed = set()
for s in root.get("skills", []):
    listed.add(s["name"])
    if not os.path.exists(s["path"]):
        fails.append(f"skill path missing: {s['path']}")
        continue
    dirname = os.path.basename(os.path.dirname(s["path"]))
    if s["name"] != dirname:
        fails.append(f"skill name '{s['name']}' != dirname '{dirname}' ({s['path']})")
    fm = open(s["path"], encoding="utf-8").read(2048)
    m = re.search(r"^name:\s*(\S+)", fm, re.M)
    if not m or m.group(1) != s["name"]:
        got = m.group(1) if m else "(none)"
        fails.append(f"SKILL.md frontmatter name '{got}' != manifest name '{s['name']}'")

# every skills/*/ dir with SKILL.md must be listed
on_disk = {d for d in os.listdir("skills")
           if os.path.isdir(os.path.join("skills", d))
           and os.path.exists(os.path.join("skills", d, "SKILL.md"))}
for d in sorted(on_disk - listed):
    fails.append(f"skill on disk but not in root plugin.json: {d}")

# agents: root manifest list vs disk
listed_agents = {a["name"] for a in root.get("agents", [])}
for a in root.get("agents", []):
    if not os.path.exists(a["path"]):
        fails.append(f"agent path missing: {a['path']}")
on_disk_agents = {f[:-3] for f in os.listdir("agents") if f.endswith(".md")}
for a in sorted(on_disk_agents - listed_agents):
    fails.append(f"agent on disk but not in root plugin.json: {a}")

# .claude-plugin manifest: explicit agents list must cover all agents
cp = json.load(open(".claude-plugin/plugin.json"))
cp_agents = {os.path.basename(p) for p in cp.get("agents", [])}
for a in sorted(on_disk_agents - {p[:-3] for p in cp_agents}):
    fails.append(f"agent on disk but not in .claude-plugin/plugin.json: {a}")
for key in ("commands", "skills"):
    p = cp.get(key, "")
    if isinstance(p, str) and p and not os.path.isdir(p):
        fails.append(f".claude-plugin '{key}' dir missing: {p}")

for f in fails:
    print(f"✗ {f}")
sys.exit(1 if fails else 0)
EOF
then
  ok "manifest ↔ filesystem consistent"
else
  ERRORS=$((ERRORS + 1))
fi

# ------------------------------------------------------- 3. dead references
DEAD=0
while IFS= read -r token; do
  [[ -z "$token" ]] && continue
  # strip only TRAILING sentence punctuation captured by the greedy token
  # class ("run scripts/init-memory-db.sh." must test "….sh", not "…sh")
  token="$(sed -E 's/[.,;:!?]+$//' <<< "$token")"
  [[ -z "$token" ]] && continue
  [[ -e "$token" ]] || { fail "dead reference: $token"; DEAD=1; }
done < <(grep -rhoE '(references|scripts|skills|hooks|agents|commands)/[A-Za-z0-9._/-]+' \
           --include="*.md" --include="*.json" \
           commands/ skills/*/SKILL.md references/ hooks/hooks.json 2>/dev/null | sort -u)
[[ $DEAD -eq 0 ]] && ok "no dead path references (commands/, skills/, references/, hooks.json)"

# ------------------------------------------- 4. hardcoded project names
# Routing + script + gemini-pointer layers, case-insensitive. Examples in
# SKILL.md/references/tests are exempt by design (the 2026-09 finds were:
# debug.md routing bindings AND init-memory-db.sh antia-specific seed
# patterns; the audit added .gemini tomls + casing to the seam list).
DENYLIST=("antia")
HITS=0
for marker in "${DENYLIST[@]}"; do
  while IFS=: read -r file lineno _; do
    [[ -z "$file" ]] && continue
    # normalize path form across grep flavors (some emit `dir//file` or `dir\/file`)
    file="${file//\\/}"; file="${file//\/\//\/}"
    fail "hardcoded project name '$marker' at $file:$lineno (routing/script layer)"
    HITS=1
  done < <(grep -rin "$marker" commands/ agents/ scripts/ .gemini/commands/ 2>/dev/null | grep -v "validate-repo\.sh:" || true)
done
[[ $HITS -eq 0 ]] && ok "no hardcoded project names in commands/ + agents/ + scripts/ + .gemini/ (denylist: ${DENYLIST[*]})"

# ------------------------------------------------- 5. hooks-tree uniqueness
if [[ -e "hooks/hooks" ]]; then
  fail "duplicate hooks tree: hooks/hooks/ exists (one canonical tree only)"
else
  ok "single hooks tree"
fi

# --------------------------------------- 6. forbidden documentation names
# DOCUMENTATION_RULES.md: no REPORT/SUMMARY/FINAL/COMPLETED-named .md files.
# Recursive under docs/ (depth-1-only was an audit-found seam) + repo root.
BANNED=$( { find docs -name "*.md" -type f 2>/dev/null; find . -maxdepth 1 -name "*.md" -type f 2>/dev/null; } \
           | grep -iE "(report|summary|final|completed)")
if [[ -n "$BANNED" ]]; then
  while IFS= read -r f; do fail "forbidden doc filename (DOCUMENTATION_RULES): $f"; done <<< "$BANNED"
else
  ok "no banned doc filenames (report/summary/final/completed)"
fi

# ----------------------------------------------------- 7. schema sanity
if python3 - <<'EOF'
import json, sys
try:
    s = json.load(open("references/summoner.schema.json"))
except Exception as e:
    print(f"✗ references/summoner.schema.json does not parse: {e}")
    sys.exit(1)
props = s.get("properties", {})
for key in ("version", "project", "phases"):
    if key not in props:
        print(f"✗ summoner.schema.json missing top-level property: {key}")
        sys.exit(1)
sys.exit(0)
EOF
then
  ok "summoner.schema.json parses with version/project/phases"
else
  ERRORS=$((ERRORS + 1))
fi

# --------------------------------------- 8. canonical-block exactly-once
# Rendered-protocol blocks live exactly once; everything else points at them.
# (How the 5 parallel workflow restatements and 3 inline checkpoint boxes
# happened: nothing failed when a second copy appeared.)
# Counting is scoped to LIVE layers only — documenting the canonical:$m
# convention in CHANGELOG/CLAUDE.md must not break the build (audit trap).
for m in checkpoint-format auto-skip-table ship-merge-template; do
  count=$(grep -rc "canonical:$m" --include="*.md" commands/ skills/ references/ agents/ 2>/dev/null | awk -F: '{s+=$2} END {print s+0}')
  if [[ "$count" -eq 1 ]]; then
    ok "canonical:$m appears exactly once"
  else
    fail "canonical:$m marker count = $count (must be exactly 1)"
  fi
done
# the rendered checkpoint box signature may appear ONLY in the canonical file
# (references/ siblings are user-served too — audit-found confinement gap)
SIG_HITS=$(grep -rl "⚡ SUMMONER — Phase" --include="*.md" commands/ skills/ agents/ references/ 2>/dev/null \
             | grep -v "checkpoint-protocol\.md$" || true)
if [[ -n "$SIG_HITS" ]]; then
  while IFS= read -r f; do
    fail "inline checkpoint-box render in $f (point at references/checkpoint-protocol.md instead)"
  done <<< "$SIG_HITS"
else
  ok "no inline checkpoint-box renders outside canonical file"
fi
# inline auto-skip tables in commands → pointer only
TABLE_HITS=$(grep -rl "^| Phase | Condition |" --include="*.md" commands/ 2>/dev/null)
if [[ -n "$TABLE_HITS" ]]; then
  while IFS= read -r f; do
    fail "inline auto-skip table in $f (point at references/workflow-reference.md instead)"
  done <<< "$TABLE_HITS"
else
  ok "no inline auto-skip tables in commands/"
fi

# ------------------------------------------- 9. resident-text byte ceilings
# M-C2: the repo's product IS context text; nothing bounded it (the
# graph-walker protocol grew into SKILL.md precisely because no ceiling said
# no). Calibration (2026-09, post content-campaign): skills ≤13KB
# (summoner-release binds at ~99%), commands ≤3KB (debug binds at ~95% —
# its triage table is load-bearing anti-misrouting content), SessionStart
# injection ≤300B (enforced by hooks/session-start/main_test.go, which
# renders both branches with a marketplace-length plugin root).
CEIL_SKILL=13312
CEIL_CMD=3072
OVER=0
while IFS= read -r f; do
  size=$(wc -c < "$f" | tr -d ' ')
  if [[ "$size" -gt "$CEIL_SKILL" ]]; then
    fail "$f = ${size}B > ${CEIL_SKILL}B skill ceiling"; OVER=1
  fi
done < <(ls skills/*/SKILL.md 2>/dev/null)
while IFS= read -r f; do
  size=$(wc -c < "$f" | tr -d ' ')
  if [[ "$size" -gt "$CEIL_CMD" ]]; then
    fail "$f = ${size}B > ${CEIL_CMD}B command ceiling"; OVER=1
  fi
done < <(ls commands/*.md 2>/dev/null)
[[ $OVER -eq 0 ]] && ok "byte ceilings respected (skills ≤${CEIL_SKILL}B, commands ≤${CEIL_CMD}B)"

# ------------------------------------ 10. platform parity tripwire (.gemini)
# P4: .gemini/commands/*.toml are pointers to the canonical files — frozen
# paraphrases are how the Gemini surface went stale 2026-06→2026-08. Two
# tripwires: (a) file-count parity with commands/, (b) each TOML actually
# points at the canonical skill + its own command file (pointer-form, not
# paraphrase), and the GEMINI.md table lists every workflow.
CMD_COUNT=$(ls commands/*.md 2>/dev/null | wc -l | tr -d ' ')
GEM_COUNT=$(ls .gemini/commands/*.toml 2>/dev/null | wc -l | tr -d ' ')
if [[ "$CMD_COUNT" != "$GEM_COUNT" ]]; then
  fail ".gemini/commands has $GEM_COUNT tomls but commands/ has $CMD_COUNT md files (parity broken)"
else
  P4=0
  for f in commands/*.md; do
    name="$(basename "$f" .md)"
    toml=".gemini/commands/summoner-$name.toml"
    if [[ ! -f "$toml" ]]; then
      fail "missing .gemini pointer: $toml"; P4=1; continue
    fi
    if ! grep -q "skills/summoner/SKILL.md" "$toml" || ! grep -q "commands/$name.md" "$toml"; then
      fail "$toml is not pointer-form (must reference skills/summoner/SKILL.md + commands/$name.md)"; P4=1
    fi
    if ! grep -q "summoner:$name" .gemini/GEMINI.md; then
      fail ".gemini/GEMINI.md table missing /summoner:$name"; P4=1
    fi
  done
  [[ $P4 -eq 0 ]] && ok "gemini surface: $GEM_COUNT pointer-form tomls + complete GEMINI.md table"
fi

# ------------------------------------------------------------------ result
echo "─"
if [[ $ERRORS -eq 0 ]]; then
  echo "✓ validate-repo: all mechanical invariants green"
  exit 0
fi
echo "✗ validate-repo: $ERRORS violation(s)"
exit 1
