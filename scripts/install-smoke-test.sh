#!/bin/bash
# install-smoke-test.sh — exercise the install path users actually hit (M-A2).
#
# A broken install is the one failure users experience BEFORE any skill
# quality matters, and until this script existed it was tested by hope.
#
# Key semantic (verified against Claude Code docs + probes, 2026-09): the
# marketplace's origin and each plugin entry's `source` are pinned
# independently — a `github` source installs from GitHub even when the
# marketplace was added from a local checkout. Our marketplace entry
# therefore declares `"source": "./"` (resolves against the marketplace root
# = this checkout), the same pattern as anthropics/skills. That makes this
# test install THE CHECKOUT TREE, which is the point: it is a pre-publish
# gate, not a post-publish observation.
#
# Locally safe: the plugin cache is isolated in a throwaway directory; your
# real ~/.claude is never touched. Requires the `claude` CLI on PATH (CI
# installs it via npm).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(dirname "$SCRIPT_DIR")"

if ! command -v claude >/dev/null 2>&1; then
  echo "✗ claude CLI not found on PATH (CI: npm install -g @anthropic-ai/claude-code)"
  exit 1
fi

# Isolate the plugin cache (documented env; never touch the real ~/.claude)
export CLAUDE_CODE_PLUGIN_CACHE_DIR="${CLAUDE_CODE_PLUGIN_CACHE_DIR:-$(mktemp -d)}"
echo "(isolated plugin cache: $CLAUDE_CODE_PLUGIN_CACHE_DIR)"

# Gate on ERRORS only (plain validate exits non-zero on errors, zero on
# warnings). --strict was dropped 2026-11-17: the repo-root CLAUDE.md advisory
# ("not loaded as project context") is inherent to the dev-repo-is-plugin-repo
# layout and would otherwise fail every run while carrying no install risk —
# the real defects this gate exists for (e.g. skills entries pointing at
# SKILL.md files instead of directories, caught locally on first run) are
# ERRORS and still fail here.
echo "── 1. claude plugin validate ."
(cd "$REPO" && claude plugin validate .)

echo "── 2. marketplace add (from checkout)"
claude plugin marketplace add "$REPO" --scope project

echo "── 3. plugin install"
claude plugin install summoner@summoner-marketplace --scope project

echo "── 4. assert the installed tree IS this checkout (the real gate)"
CACHE_ROOT="$CLAUDE_CODE_PLUGIN_CACHE_DIR/cache/summoner-marketplace/summoner"
if [[ ! -d "$CACHE_ROOT" ]]; then
  echo "✗ installed cache dir not found: $CACHE_ROOT"
  find "$CLAUDE_CODE_PLUGIN_CACHE_DIR" -maxdepth 4 2>/dev/null | head -20
  exit 1
fi
# a reused cache can hold multiple versions — assert exactly one, else the
# pick below is arbitrary (audit: alphabetical `ls | head -1` picked 0.0.1
# over 0.1.9 and produced a false RED)
shopt -s nullglob
version_paths=("$CACHE_ROOT"/*)
VERSIONS_FOUND=${#version_paths[@]}
if [[ "$VERSIONS_FOUND" != "1" ]]; then
  echo "✗ expected exactly 1 installed version, found $VERSIONS_FOUND: ${version_paths[*]}"
  exit 1
fi
INSTALLED="${version_paths[0]}"
VERSION_DIR="${INSTALLED##*/}"
echo "   installed at: $INSTALLED (version $VERSION_DIR)"

# Content identity, not version equality: a published GitHub tag carries the
# same manifest version, so version-match alone can pass while installing the
# remote tree (audit: vacuous-pass window). Byte-compare the manifest and
# one content file instead.
CHECKOUT_VERSION=$(python3 -c 'import json; print(json.load(open("'"$REPO"'/plugin.json"))["version"])')
if [[ "$VERSION_DIR" != "$CHECKOUT_VERSION" ]]; then
  echo "✗ installed version $VERSION_DIR != checkout version $CHECKOUT_VERSION"
  exit 1
fi
if ! cmp -s "$REPO/plugin.json" "$INSTALLED/plugin.json"; then
  echo "✗ installed plugin.json differs from checkout — install did not come from this tree"
  diff "$REPO/plugin.json" "$INSTALLED/plugin.json" | head -5
  exit 1
fi
if ! cmp -s "$REPO/references/single-loop.md" "$INSTALLED/references/single-loop.md"; then
  echo "✗ installed single-loop.md differs from checkout — install did not come from this tree"
  exit 1
fi
echo "   ✓ installed content byte-identical to checkout (version $CHECKOUT_VERSION)"

# hooks present, executable, AND WIRED: `claude plugin validate` does not
# check that hooks.json paths resolve (audit: a typo'd hooks entry passed the
# whole gate and installed with zero active hooks), so assert it here.
# The loop asserts ALL NINE binaries the hooks Makefile builds — including
# summoner-status and summoner-sandbox-probe, which hooks.json does not wire
# (validate-manifest is invoked directly; status/probe are command/contract
# surfaces), so the wiring check below cannot substitute for them.
for b in session-start pretooluse-gate stop validate-manifest posttooluse subagentstop userpromptsubmit summoner-status summoner-sandbox-probe; do
  bin="$INSTALLED/hooks/bin/$b"
  if [[ ! -x "$bin" ]]; then
    echo "✗ hook binary missing or not executable: $bin"
    exit 1
  fi
done
# The plugin-root token below is literal JSON content, not a shell expansion.
# shellcheck disable=SC2016
WIRED=$(python3 -c '
import json, os, sys
hooks = json.load(open(sys.argv[1] + "/hooks/hooks.json"))
missing = []
for event, entries in hooks.get("hooks", {}).items():
    for e in entries:
        for h in e.get("hooks", []):
            cmd = h.get("command", "")
            # strip quoting and the ${CLAUDE_PLUGIN_ROOT} prefix
            path = cmd.strip().strip(chr(34)).replace("${CLAUDE_PLUGIN_ROOT}", sys.argv[1])
            if not (os.path.isfile(path) and os.access(path, os.X_OK)):
                missing.append(f"{event}: {cmd}")
print("\n".join(missing))
' "$INSTALLED")
if [[ -n "$WIRED" ]]; then
  echo "✗ hooks.json references non-executable/missing binaries:"
  echo "$WIRED"
  exit 1
fi
echo "   ✓ hook binaries present + executable + hooks.json wiring resolves"

# commands complete vs repo
repo_command_paths=("$REPO"/commands/*.md)
installed_command_paths=("$INSTALLED"/commands/*.md)
repo_cmds=${#repo_command_paths[@]}
inst_cmds=${#installed_command_paths[@]}
if [[ "$repo_cmds" != "$inst_cmds" ]]; then
  echo "✗ command count mismatch: repo=$repo_cmds installed=$inst_cmds"
  exit 1
fi
echo "   ✓ commands complete ($inst_cmds)"

# all six registered skills present (must match plugin.json skills[])
for s in summoner-setup summoner-release phase-submit change review-submit probe-submit; do
  if [[ ! -f "$INSTALLED/skills/$s/SKILL.md" ]]; then
    echo "✗ skill missing from installed tree: $s"
    exit 1
  fi
done
echo "   ✓ all 6 registered skills present"

# skills[] arrays must equal the actual skills/ directories — both plugin
# manifests, normalized (root uses name/path objects; .claude-plugin uses
# "./skills/x" strings). A 0.6.0 audit found .claude-plugin's list-form
# array had zero validation; a stale entry there ships a dead listing.
ARRAY_DIFF=$(python3 - "$REPO" <<'EOF'
import json, os, sys
repo = sys.argv[1]
on_disk = sorted(d for d in os.listdir(os.path.join(repo, "skills"))
                 if os.path.isdir(os.path.join(repo, "skills", d)))
root = sorted(e["name"] for e in json.load(open(os.path.join(repo, "plugin.json")))["skills"])
cp = json.load(open(os.path.join(repo, ".claude-plugin/plugin.json")))["skills"]
if isinstance(cp, str):
    cp = [cp]
cp = sorted(p.rstrip("/").split("/")[-1] for p in cp)
for name, arr in (("plugin.json", root), (".claude-plugin/plugin.json", cp)):
    if arr != on_disk:
        print(f"{name} skills[] {arr} != skills/ dirs {on_disk}")
EOF
)
if [[ -n "$ARRAY_DIFF" ]]; then
  echo "✗ skills[] array drift: $ARRAY_DIFF"
  exit 1
fi
echo "   ✓ skills[] arrays match skills/ directories (both manifests)"

echo "✓ install smoke test passed — the checkout's distribution path works"
