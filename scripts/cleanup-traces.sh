#!/bin/bash
# cleanup-traces.sh — enforce the documented trace retention policy.
#
# references/trace-protocol.md §Trace Retention promises: "Last 30 days OR
# last 100 sessions per project (whichever is the larger)", cron-able. Until this
# script existed that promise pointed at nothing (caught by validate-repo.sh's
# dead-reference check).
#
# Layout convention (matches scripts/stability-test.sh globbing):
#   $TRACES_ROOT/<project>/*-<workflow>-*.jsonl
#
# The keep-newest-N step is python3, not `xargs ls -t | tail`: past ARG_MAX,
# xargs re-batches `ls -t` and `tail -n +N` then trims across independently
# sorted batches — deleting NEW traces and keeping OLD ones (found in review).
#
# Usage: cleanup-traces.sh [traces-root]   (default: ~/.claude/plugins/summoner/traces)
# Cron:  0 4 * * * ~/.claude/plugins/summoner/scripts/cleanup-traces.sh

set -u

TRACES_ROOT="${1:-${SUMMONER_TRACES_ROOT:-$HOME/.claude/plugins/summoner/traces}}"
DAYS=30
KEEP=100

if [[ ! -d "$TRACES_ROOT" ]]; then
  echo "no traces dir at $TRACES_ROOT — nothing to clean"
  exit 0
fi

python3 - "$TRACES_ROOT" "$DAYS" "$KEEP" <<'EOF'
import os, sys, time

root, days, keep = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
now = time.time()
cutoff = now - days * 86400  # strictly older than DAYS days

for project in sorted(os.listdir(root)):
    pdir = os.path.join(root, project)
    if not os.path.isdir(pdir):
        continue
    traces = [os.path.join(pdir, f) for f in os.listdir(pdir) if f.endswith(".jsonl")]
    removed_age = 0
    for t in traces:
        try:
            if os.path.getmtime(t) < cutoff:
                os.remove(t)
                removed_age += 1
        except OSError:
            pass
    remaining = [t for t in traces if os.path.exists(t)]
    removed_keep = 0
    if len(remaining) > keep:
        remaining.sort(key=lambda t: os.path.getmtime(t), reverse=True)
        for t in remaining[keep:]:
            try:
                os.remove(t)
                removed_keep += 1
            except OSError:
                pass
        print(f"{project}: trimmed to {keep} newest (had {len(traces)}; {removed_age} expired, {removed_keep} over-quota)")
    else:
        print(f"{project}: {len(traces)} trace(s) within policy ({removed_age} expired removed)")
EOF
