#!/bin/bash
# summoner-init.sh — Pick how to configure a v2 capability map.
# All modes delegate to one emitter; no legacy/manual v1 template remains.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if [ "$#" -gt 0 ]; then
    case "$1" in
        1) shift; exec "$SCRIPT_DIR/summoner-bp.sh" "$@" ;;
        2) shift; exec "$SCRIPT_DIR/summoner-bp.sh" --quick "$@" ;;
        3) shift; exec "$SCRIPT_DIR/summoner-bp.sh" --manual "$@" ;;
        *) exec "$SCRIPT_DIR/summoner-bp.sh" "$@" ;;
    esac
fi

printf '%s\n' 'Summoner v2 — project capability setup' \
    '  [1] Choose inline skills (default)' \
    '  [2] Quick defaults (no further input)' \
    '  [3] Enter skill names manually'
while true; do
    read -r -p 'Choose [1-3, default=1]: ' mode || exit 1
    case "${mode:-1}" in
        1) exec "$SCRIPT_DIR/summoner-bp.sh" ;;
        2) exec "$SCRIPT_DIR/summoner-bp.sh" --quick ;;
        3) exec "$SCRIPT_DIR/summoner-bp.sh" --manual ;;
        *) printf '%s\n' 'Choose 1, 2 or 3.' ;;
    esac
done
