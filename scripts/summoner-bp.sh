#!/bin/bash
# summoner-bp.sh — Select inline capabilities and write a v2 manifest atomically.
# Bash 3.2+; review is a verification_spec, never a skill/persona selection.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
OUTPUT_FILE="summoner.yaml"
QUICK=false
MANUAL=false
FORCE=false
PROJECT_NAME=""

while [ "$#" -gt 0 ]; do
    case "$1" in
        --quick|-q) QUICK=true ;;
        --manual) MANUAL=true ;;
        --force|-f) FORCE=true ;;
        --project)
            if [ "$#" -lt 2 ] || [ -z "$2" ]; then
                printf '%s\n' '--project requires a name' >&2
                exit 2
            fi
            PROJECT_NAME="$2"
            shift
            ;;
        --help|-h)
            printf '%s\n' 'Usage: summoner-bp.sh [--quick | --manual] [--project NAME] [--force]' \
                '  --quick    Use inline defaults without reading stdin' \
                '  --manual   Enter a default skill for each capability' \
                '  --project  Portable project/memory namespace (default: directory name)' \
                '  --force    Replace an existing manifest; no legacy merge or fallback' \
                'Without --quick/--manual, choose a default, custom skill, or none per capability.'
            exit 0
            ;;
        *) printf 'Unknown option: %s\n' "$1" >&2; exit 2 ;;
    esac
    shift
done

if [ "$QUICK" = true ] && [ "$MANUAL" = true ]; then
    printf '%s\n' '--quick and --manual are mutually exclusive' >&2
    exit 2
fi

if [ -d "$OUTPUT_FILE" ]; then
    printf '%s\n' 'summoner.yaml is a directory; refusing to replace it' >&2
    exit 1
fi
if { [ -e "$OUTPUT_FILE" ] || [ -L "$OUTPUT_FILE" ]; } && [ "$FORCE" = false ]; then
    if [ "$QUICK" = true ]; then
        printf '%s\n' 'summoner.yaml already exists; inspect it, then use --force to replace it' >&2
        exit 1
    fi
    printf '%s\n' 'An existing manifest will be REPLACED, not merged. Keep a copy of custom capabilities.'
    read -r -p 'Replace it? [y/N]: ' answer || exit 1
    case "$answer" in y|Y) ;; *) printf '%s\n' 'Cancelled; manifest unchanged.'; exit 1 ;; esac
fi

if [ -z "$PROJECT_NAME" ]; then
    default_name="$(basename "$PWD")"
    if ! [[ "$default_name" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || [[ "$default_name" == *..* ]]; then
        default_name="my-project"
    fi
    PROJECT_NAME="$default_name"
    if [ "$QUICK" = false ]; then
        read -r -p "Project name [$default_name]: " answer || exit 1
        PROJECT_NAME="${answer:-$default_name}"
    fi
fi
if ! [[ "$PROJECT_NAME" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || [[ "$PROJECT_NAME" == *..* ]]; then
    printf '%s\n' 'Invalid project name: use letters/digits, dot, underscore or hyphen; no paths or double dots.' >&2
    exit 2
fi

# Capability names match the fixed reducer phases. These are provider defaults,
# not instructions to execute phases in the order printed here.
PHASES=(requirements plan implement test diagnosis ops)
SKILLS=()
default_skill() {
    case "$1" in
        requirements) printf '%s' 'superpowers:brainstorming' ;;
        plan)         printf '%s' 'superpowers:writing-plans' ;;
        implement)    printf '%s' 'superpowers:executing-plans' ;;
        test)         printf '%s' 'superpowers:test-driven-development' ;;
        diagnosis)    printf '%s' 'superpowers:systematic-debugging' ;;
        ops)          printf '%s' 'none' ;;
    esac
}

for phase in "${PHASES[@]}"; do
    skill="$(default_skill "$phase")"
    if [ "$MANUAL" = true ]; then
        read -r -p "$phase default skill [$skill]: " answer || exit 1
        skill="${answer:-$skill}"
    elif [ "$QUICK" = false ]; then
        if [ "$skill" = none ]; then
            printf '\n%s capability\n  [1] none — no capability (never skip authorization)\n  [2] custom skill\n' "$phase"
            while true; do
                read -r -p 'Choose [1-2, default=1]: ' answer || exit 1
                case "${answer:-1}" in
                    1) break ;;
                    2) read -r -p 'Skill name (local or plugin:skill): ' skill || exit 1; break ;;
                    *) printf '%s\n' 'Choose 1 or 2.' ;;
                esac
            done
        else
            printf '\n%s capability\n  [1] default: %s\n  [2] custom skill\n  [3] none (no capability, not skip authorization)\n' "$phase" "$skill"
            while true; do
                read -r -p 'Choose [1-3, default=1]: ' answer || exit 1
                case "${answer:-1}" in
                    1) break ;;
                    2) read -r -p 'Skill name (local or plugin:skill): ' skill || exit 1; break ;;
                    3) skill=none; break ;;
                    *) printf '%s\n' 'Choose 1, 2 or 3.' ;;
                esac
            done
        fi
    fi
    if ! [[ "$skill" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*(:[A-Za-z0-9][A-Za-z0-9._-]*)?$ ]]; then
        printf 'Invalid skill name for %s: %s\n' "$phase" "$skill" >&2
        exit 2
    fi
    SKILLS+=("$skill")
done

if [ "$QUICK" = false ]; then
    printf '\nReview uses verification_spec (criteria, check_ids, readable, rubric), not a skill roster.\n'
    printf '%s\n' 'Skills run inline; alternates can be declared explicitly after setup.'
    read -r -p 'Write the v2 capability map? [Y/n]: ' answer || exit 1
    case "$answer" in ''|y|Y) ;; *) printf '%s\n' 'Cancelled; manifest unchanged.'; exit 1 ;; esac
fi

# Write in the destination directory and validate BEFORE rename. A validation
# failure, missing binary, interrupted prompt or invalid input leaves the old
# manifest untouched. All three entry points share this sole v2 emitter.
TEMP_FILE="$(mktemp .summoner.yaml.XXXXXX)"
trap 'rm -f "$TEMP_FILE"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
{
    printf '%s\n' '# summoner.yaml — project capabilities, not an execution topology' 'version: "2"' 'project:'
    printf '  name: "%s"\nphases:\n' "$PROJECT_NAME"
    for ((i=0; i<${#PHASES[@]}; i++)); do
        printf '  %s:\n    skill: "%s"\n' "${PHASES[$i]}" "${SKILLS[$i]}"
    done
    printf '  fix:\n    skill: "%s"\n' "${SKILLS[2]}"
    printf '  reproduce:\n    skill: "%s"\n' "${SKILLS[3]}"
    printf '%s\n' \
        '  verify:' '    skill: "superpowers:verification-before-completion"' \
        '  regression:' '    skill: "superpowers:verification-before-completion"' \
        '  review:' '    verification_spec:' '      criteria: []' \
        '      check_ids: []' '      readable: []' '      rubric: plugin:agents/reviewer.md'
} > "$TEMP_FILE"
"$SCRIPT_DIR/validate-manifest.sh" "$TEMP_FILE"
mv -f "$TEMP_FILE" "$OUTPUT_FILE"
printf '%s\n' 'Created summoner.yaml (v2). Review capability bindings before use.' \
    'Tool scopes and budgets are not pre-authorized; confirm suggestions at the first checkpoint.'
printf 'Memory setup: %s/init-memory-db.sh %s\n' "$SCRIPT_DIR" "$PROJECT_NAME"
