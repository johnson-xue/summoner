#!/bin/bash
set -o pipefail

# create-baseline.sh — Create a baseline from a successful trace
# Usage: create-baseline.sh --trace <file> --name <baseline-name> --category <category>

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_ROOT="$(dirname "$SCRIPT_DIR")"
BASELINES_DIR="$PLUGIN_ROOT/baselines"

# Parse arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --trace)
      TRACE_FILE="$2"
      shift 2
      ;;
    --name)
      BASELINE_NAME="$2"
      shift 2
      ;;
    --category)
      CATEGORY="$2"
      shift 2
      ;;
    *)
      echo "Usage: $0 --trace <file> --name <baseline-name> --category <bugfix|feature|ops>"
      exit 1
      ;;
  esac
done

# Validate arguments
if [[ -z "$TRACE_FILE" ]] || [[ -z "$BASELINE_NAME" ]] || [[ -z "$CATEGORY" ]]; then
  echo "Error: --trace, --name, and --category are required"
  exit 1
fi

if [[ ! -f "$TRACE_FILE" ]]; then
  echo "Error: Trace file not found: $TRACE_FILE"
  exit 1
fi

# Validate category
if [[ "$CATEGORY" != "bugfix" ]] && [[ "$CATEGORY" != "feature" ]] && [[ "$CATEGORY" != "ops" ]]; then
  echo "Error: category must be bugfix, feature, or ops (got: $CATEGORY)"
  exit 1
fi

# Validate baseline name (kebab-case only)
if [[ ! "$BASELINE_NAME" =~ ^[a-z0-9-]+$ ]]; then
  echo "Error: baseline name must be kebab-case (lowercase letters, numbers, hyphens only)"
  exit 1
fi

echo "📊 Creating Baseline: $BASELINE_NAME"
echo "Category: $CATEGORY"
echo "Source: $(basename "$TRACE_FILE")"
echo ""

# Extract project name from trace
PROJECT=$(jq -r 'select(.type=="session_start") | .project' "$TRACE_FILE" 2>/dev/null | head -1)
if [[ -z "$PROJECT" ]]; then
  echo "Error: Cannot extract project name from trace"
  exit 1
fi

# Extract workflow type
WORKFLOW=$(jq -r 'select(.type=="session_start") | .workflow' "$TRACE_FILE" 2>/dev/null | head -1)
if [[ -z "$WORKFLOW" ]]; then
  echo "Error: Cannot extract workflow type from trace"
  exit 1
fi

# Extract model
MODEL=$(jq -r 'select(.type=="session_start") | .model' "$TRACE_FILE" 2>/dev/null | head -1)

# Extract phase sequence
PHASES=$(jq -r 'select(.type=="phase_end") | .phase' "$TRACE_FILE" 2>/dev/null | jq -R -s -c 'split("\n") | map(select(length > 0) | tonumber)')

# Extract tool call sequence
TOOLS=$(jq -r 'select(.type=="tool_call") | .tool' "$TRACE_FILE" 2>/dev/null | jq -R -s -c 'split("\n") | map(select(length > 0))')

# Extract artifacts
ARTIFACTS=$(jq -r 'select(.type=="phase_end") | .artifacts[]' "$TRACE_FILE" 2>/dev/null | jq -R -s -c 'split("\n") | map(select(length > 0))')

# Check for scoring results
SCORING_RESULT=$(jq -c 'select(.type=="scoring_result")' "$TRACE_FILE" 2>/dev/null | tail -1)

if [[ -z "$SCORING_RESULT" ]]; then
  echo "⚠️  Warning: No scoring result found in trace. Run score-trace.sh first?"
  echo ""
  read -p "Continue without scores? [y/N] " -n 1 -r
  echo
  if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Aborted."
    exit 1
  fi
  SCORES='{}'
else
  # Extract scores by priority
  P0_SCORE=$(echo "$SCORING_RESULT" | jq -r 'select(.priority=="P0") | .total_score')
  P0_MAX=$(echo "$SCORING_RESULT" | jq -r 'select(.priority=="P0") | .max_score')
  SCORES="{\"P0\":$P0_SCORE,\"P0_max\":$P0_MAX}"
fi

# Extract session duration
DURATION=$(jq -r 'select(.type=="session_end") | .total_duration_ms' "$TRACE_FILE" 2>/dev/null | head -1)

# Create baseline directory
BASELINE_DIR="$BASELINES_DIR/$PROJECT"
mkdir -p "$BASELINE_DIR"

# Generate baseline file
BASELINE_FILE="$BASELINE_DIR/${BASELINE_NAME}.baseline.json"
TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)

cat > "$BASELINE_FILE" <<EOF
{
  "name": "$BASELINE_NAME",
  "category": "$CATEGORY",
  "workflow": "$WORKFLOW",
  "project": "$PROJECT",
  "model": "$MODEL",
  "expected_phases": $PHASES,
  "expected_tool_sequence": $TOOLS,
  "expected_scores": $SCORES,
  "expected_artifacts": $ARTIFACTS,
  "expected_duration_ms": ${DURATION:-0},
  "trace_reference": "$TRACE_FILE",
  "created_at": "$TIMESTAMP",
  "created_by": "human",
  "approved": true,
  "metadata": {
    "description": "Auto-generated baseline from successful trace",
    "tags": []
  }
}
EOF

echo "✅ Baseline created: $BASELINE_FILE"
echo ""
echo "Baseline summary:"
echo "  - Phases: $(echo "$PHASES" | jq -r '. | join(", ")')"
echo "  - Tools: $(echo "$TOOLS" | jq -r 'length') tool calls"
echo "  - Scores: P0=$P0_SCORE/$P0_MAX"
echo "  - Duration: ${DURATION:-0}ms"
echo ""
echo "To test against this baseline:"
echo "  ./scripts/regression-test.sh --baseline $BASELINE_FILE --new-trace <new-trace.jsonl>"

exit 0
