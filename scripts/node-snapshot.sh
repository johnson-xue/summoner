#!/bin/bash
# node-snapshot.sh — ⓪ working-tree snapshot/restore for idempotent retry (H2).
# Owner = SKILL.md. The walker signals snapshot/restore flags via the RUN_NODE
# directive (§10.1, M2); SKILL.md runs THIS helper. The walker never touches the tree.
#
# Usage:
#   node-snapshot.sh save [paths...]   # git stash --include-untracked (-u)
#   node-snapshot.sh restore            # git stash pop
#
# -u covers new files the reproduce/fix nodes write (M2 fix).

set -o pipefail

ACTION="${1:-}"

if [[ "$ACTION" == "save" ]]; then
  shift
  # stash everything including untracked; if paths given, the caller has already
  # scoped the working set — we still stash -u so NEW files are captured.
  if ! git stash push --include-untracked -m "summoner-node-snapshot-$(date +%s)" "$@"; then
    echo "Error: git stash save failed" >&2
    exit 1
  fi
  echo "OK: snapshot saved (git stash --include-untracked)"
  exit 0
fi

if [[ "$ACTION" == "restore" ]]; then
  if ! git stash pop; then
    echo "Error: git stash pop failed (no snapshot to restore?)" >&2
    exit 1
  fi
  echo "OK: snapshot restored (git stash pop)"
  exit 0
fi

echo "Usage: node-snapshot.sh save [paths...] | restore"
exit 2
