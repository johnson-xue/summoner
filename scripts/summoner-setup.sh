#!/bin/bash
# summoner-setup.sh — Create/validate v2 capabilities and initialize memory.
# Run in the project directory. Existing manifests are never silently migrated.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
FORCE=false
PROJECT_ARG=""
ARGS=("$@")
while [ "$#" -gt 0 ]; do
    case "$1" in
        --force|-f) FORCE=true ;;
        --quick|-q|--manual) ;;
        --project)
            if [ "$#" -lt 2 ] || [ -z "$2" ]; then
                printf '%s\n' '--project requires a name' >&2
                exit 2
            fi
            PROJECT_ARG="$2"
            shift
            ;;
        --help|-h) exec "$SCRIPT_DIR/summoner-bp.sh" --help ;;
        *) printf 'Unknown option: %s\n' "$1" >&2; exit 2 ;;
    esac
    shift
done

if [ ! -e summoner.yaml ] || [ "$FORCE" = true ]; then
    # Bash 3.2 with nounset needs this form for a possibly empty argument array.
    "$SCRIPT_DIR/summoner-init.sh" ${ARGS[@]+"${ARGS[@]}"}
fi

# Use the real parser even for an existing or hand-written manifest (including
# YAML flow style). Never parse project.name with grep, or repair invalid v1
# input by retaining its old topology. This output mode STILL fully validates.
if ! PROJECT_NAME="$("$SCRIPT_DIR/validate-manifest.sh" summoner.yaml --project-name)"; then
    printf '%s\n' 'Setup stopped: migrate the manifest to v2, or explicitly use --force to replace it.' >&2
    exit 1
fi
if [ -n "$PROJECT_ARG" ] && [ "$PROJECT_ARG" != "$PROJECT_NAME" ]; then
    printf '%s\n' 'Existing project.name differs from --project; use --force only if replacement is intended.' >&2
    exit 1
fi

# The initializer uses SUMMONER_MEMORY_DIR (default ~/.summoner/memory), performs
# non-destructive legacy DB migration and is safe to repeat. Do not delete a DB
# just because a readiness probe failed.
"$SCRIPT_DIR/init-memory-db.sh" "$PROJECT_NAME"
printf '\n%s\n' 'Setup complete: v2 capabilities validated and memory initialized.' \
    'Use a Summoner command to enter the same checkpoint reducer.' \
    'Project checks remain BLOCKED unless the required sandbox is active.' \
    'Restart the client only if the plugin/hooks were just installed or changed.'
