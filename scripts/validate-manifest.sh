#!/bin/bash
set -e

# validate-manifest.sh — Validate a summoner.yaml manifest
# Usage: validate-manifest.sh <path-to-summoner.yaml>

MANIFEST="${1:-summoner.yaml}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SCHEMA_FILE="${SCRIPT_DIR}/../references/summoner.schema.json"

if [ ! -f "$MANIFEST" ]; then
    echo "ERROR: $MANIFEST not found" >&2
    exit 1
fi

ERRORS=0

# --- JSON Schema validation (preferred) ---
if command -v python3 &>/dev/null; then
    # Check if pyyaml is available
    if python3 -c "import yaml, json, sys" 2>/dev/null; then
        echo "→ Schema validation..."

        # Validate YAML can be parsed
        python3 -c "
import yaml, json, sys
try:
    with open('$MANIFEST') as f:
        data = yaml.safe_load(f)
    if data is None:
        print('ERROR: empty or invalid YAML')
        sys.exit(1)
    print('  ✓ YAML syntax valid')
except Exception as e:
    print(f'ERROR: YAML parse failed — {e}')
    sys.exit(1)
"
        YAML_OK=$?
    else
        echo "  (pyyaml not installed — skipping YAML parse check)"
        YAML_OK=0
    fi

    # Validate against JSON Schema if jsonschema is available
    if [ -f "$SCHEMA_FILE" ] && [ $YAML_OK -eq 0 ]; then
        if python3 -c "import jsonschema" 2>/dev/null; then
            python3 -c "
import yaml, json, jsonschema, sys
with open('$MANIFEST') as f:
    data = yaml.safe_load(f)
with open('$SCHEMA_FILE') as f:
    schema = json.load(f)
try:
    jsonschema.validate(data, schema)
    print('  ✓ Schema validation passed')
except jsonschema.ValidationError as e:
    print(f'ERROR: Schema validation failed — {e.message}')
    print(f'  at: {\" → \".join(str(p) for p in e.absolute_path)}')
    sys.exit(1)
"
            SCHEMA_OK=$?
        else
            echo "  (jsonschema not installed — skipping schema validation)"
            SCHEMA_OK=0
        fi
    else
        SCHEMA_OK=0
    fi
else
    echo "→ Basic validation (python3 not available)..."
    YAML_OK=0
    SCHEMA_OK=0
fi

# --- Grep-based fallback checks ---
echo "→ Structural checks..."

# Check required top-level fields
for field in version project phases; do
    if ! grep -qE "^${field}:" "$MANIFEST"; then
        echo "ERROR: missing required field '$field'" >&2
        ERRORS=$((ERRORS + 1))
    else
        echo "  ✓ ${field}"
    fi
done

# Check version
VERSION=$(grep -oE 'version:.*"[^"]*"' "$MANIFEST" | head -1 | sed 's/.*"\(.*\)".*/\1/')
if [ "$VERSION" != "1" ]; then
    echo "ERROR: version must be \"1\", got \"$VERSION\"" >&2
    ERRORS=$((ERRORS + 1))
fi

# Check project.name
if ! grep -qE '^\s+name:' "$MANIFEST"; then
    echo "ERROR: missing required field 'project.name'" >&2
    ERRORS=$((ERRORS + 1))
fi

# Check that each phase has a 'skill' field
PHASE_COUNT=$(grep -cE '^\s+skill:' "$MANIFEST" || true)
if [ "$PHASE_COUNT" -eq 0 ]; then
    echo "ERROR: no phases with 'skill' field found" >&2
    ERRORS=$((ERRORS + 1))
else
    echo "  ✓ ${PHASE_COUNT} phases defined"
fi

# Validate workflows section if present
if grep -qE '^workflows:' "$MANIFEST"; then
    echo "  ✓ workflows section present"
    WORKFLOW_NAMES=$(grep -oE '^\s{2}[a-z-]+:' "$MANIFEST" | grep -A100 '^workflows:' | tail -n +2 | sed 's/://g' | tr -d ' ')
    for wf in $WORKFLOW_NAMES; do
        if ! grep -A20 "^\s\s${wf}:" "$MANIFEST" | grep -q 'checkpoints:'; then
            echo "ERROR: workflow '$wf' missing 'checkpoints' field" >&2
            ERRORS=$((ERRORS + 1))
        fi

        HAS_CHAIN=$(grep -A20 "^\s\s${wf}:" "$MANIFEST" | grep -c 'chain:' || true)
        HAS_FANOUT=$(grep -A20 "^\s\s${wf}:" "$MANIFEST" | grep -c 'fan_out:' || true)
        if [ "$HAS_CHAIN" -eq 0 ] && [ "$HAS_FANOUT" -eq 0 ]; then
            echo "ERROR: workflow '$wf' must have 'chain' or 'fan_out'" >&2
            ERRORS=$((ERRORS + 1))
        fi

        if [ "$HAS_FANOUT" -gt 0 ]; then
            if ! grep -A30 "^\s\s${wf}:" "$MANIFEST" | grep -q 'merge:'; then
                echo "ERROR: workflow '$wf' has fan_out but no 'merge' field" >&2
                ERRORS=$((ERRORS + 1))
            fi
        fi
    done
fi

# Check "none" is lowercase (case-sensitive keyword)
MIXED_NONE=$(grep -E '^\s+skill:\s+' "$MANIFEST" | grep -i 'none' | grep -v 'skill: none$' || true)
if [ -n "$MIXED_NONE" ]; then
    echo "WARNING: 'none' must be lowercase. Found: $MIXED_NONE" >&2
fi

echo ""
if [ "$ERRORS" -eq 0 ]; then
    echo "✓ $MANIFEST is valid"
    exit 0
else
    echo "✗ $MANIFEST has $ERRORS error(s)" >&2
    exit 1
fi
