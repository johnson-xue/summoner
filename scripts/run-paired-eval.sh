#!/bin/bash
# run-paired-eval.sh — thin entry point for the paired behavioral eval harness
# (spec §7 fold-and-measure gate; single-mode rewrite Task 15).
#
# All logic lives in evals/paired/harness/paired_eval.py (python3 stdlib, one
# file). This wrapper only translates the documented flag style and never
# invokes any model or claude binary itself.
#
#   scripts/run-paired-eval.sh --list                  # cases × arms + resolved
#                                                     # configs; zero claude calls
#   scripts/run-paired-eval.sh --selftest              # deterministic selftests
#   scripts/run-paired-eval.sh --prepare CASE --arm ARM
#   scripts/run-paired-eval.sh --preflight CASE [--arm ARM]
#   scripts/run-paired-eval.sh run CASE --arm ARM --tolerances F --budget F
#   scripts/run-paired-eval.sh judge RUN_ID --tolerances F --budget F
#   scripts/run-paired-eval.sh aggregate RUN_ID ... [--tolerances F]
#
# Arms: `baseline` = documented checkout of git tag v0.3.0 (the script pins and
# records the tag's commit; it never silently uses the current tree for the
# baseline arm); `candidate` = the recorded harness commit on this branch.
# Groups comparing strategies under the candidate name their arms explicitly
# (worker/folded, independent/self-review) — see --list.
#
# `run`/`judge` REFUSE without a user-filled tolerances file and budget
# statement: actual model runs need the user's budget approval at a real
# checkpoint first. `--list`/`--prepare`/`--preflight`/`--selftest` invoke zero
# claude calls (PATH-stub tested).

set -u

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_ROOT="$(dirname "$SCRIPT_DIR")"
HARNESS="$PLUGIN_ROOT/evals/paired/harness/paired_eval.py"
SELFTEST="$SCRIPT_DIR/test-paired-harness.sh"

usage() {
    sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'
    exit 2
}

if [ ! -f "$HARNESS" ]; then
    printf '%s\n' "run-paired-eval.sh: harness missing: $HARNESS" >&2
    exit 1
fi

MODE=""
CASE=""
ARM=""
TOLERANCES=""
BUDGET=""
REST=()

while [ $# -gt 0 ]; do
    case "$1" in
        --list) MODE="list" ;;
        --selftest) MODE="selftest" ;;
        --prepare) MODE="prepare" ;;
        --preflight) MODE="preflight" ;;
        run) MODE="run" ;;
        judge) MODE="judge" ;;
        aggregate) MODE="aggregate" ;;
        --arm)
            [ $# -ge 2 ] || usage
            ARM="$2"
            shift
            ;;
        --tolerances)
            [ $# -ge 2 ] || usage
            TOLERANCES="$2"
            shift
            ;;
        --budget)
            [ $# -ge 2 ] || usage
            BUDGET="$2"
            shift
            ;;
        --results-dir|--tmp|--run-id|--control)
            [ $# -ge 2 ] || usage
            REST+=("$1" "$2")
            shift
            ;;
        --help|-h) usage ;;
        -*) REST+=("$1") ;;
        *)
            if [ -z "$CASE" ]; then
                CASE="$1"
            else
                REST+=("$1")
            fi
            ;;
    esac
    shift
done

[ -n "$MODE" ] || usage

case "$MODE" in
    list)
        exec python3 "$HARNESS" list ${REST[@]+"${REST[@]}"}
        ;;
    selftest)
        exec bash "$SELFTEST" ${REST[@]+"${REST[@]}"}
        ;;
    prepare)
        [ -n "$CASE" ] && [ -n "$ARM" ] || usage
        exec python3 "$HARNESS" prepare "$CASE" "$ARM" ${REST[@]+"${REST[@]}"}
        ;;
    preflight)
        [ -n "$CASE" ] || usage
        if [ -n "$ARM" ]; then
            exec python3 "$HARNESS" preflight "$CASE" --arm "$ARM" ${REST[@]+"${REST[@]}"}
        fi
        exec python3 "$HARNESS" preflight "$CASE" ${REST[@]+"${REST[@]}"}
        ;;
    run)
        [ -n "$CASE" ] && [ -n "$ARM" ] || usage
        ARGS=()
        [ -n "$TOLERANCES" ] && ARGS+=(--tolerances "$TOLERANCES")
        [ -n "$BUDGET" ] && ARGS+=(--budget "$BUDGET")
        exec python3 "$HARNESS" run "$CASE" "$ARM" ${ARGS[@]+"${ARGS[@]}"} ${REST[@]+"${REST[@]}"}
        ;;
    judge)
        [ -n "$CASE" ] || usage
        ARGS=()
        [ -n "$TOLERANCES" ] && ARGS+=(--tolerances "$TOLERANCES")
        [ -n "$BUDGET" ] && ARGS+=(--budget "$BUDGET")
        exec python3 "$HARNESS" judge "$CASE" ${ARGS[@]+"${ARGS[@]}"} ${REST[@]+"${REST[@]}"}
        ;;
    aggregate)
        ARGS=()
        [ -n "$TOLERANCES" ] && ARGS+=(--tolerances "$TOLERANCES")
        if [ -n "$CASE" ]; then
            exec python3 "$HARNESS" aggregate "$CASE" ${REST[@]+"${REST[@]}"} ${ARGS[@]+"${ARGS[@]}"}
        fi
        exec python3 "$HARNESS" aggregate ${REST[@]+"${REST[@]}"} ${ARGS[@]+"${ARGS[@]}"}
        ;;
    *)
        usage
        ;;
esac
