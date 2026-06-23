#!/bin/bash
# build-check.sh — Verify build/compile success

TRACE_FILE="$1"

if [[ ! -f "$TRACE_FILE" ]]; then
  echo "Error: Trace file not found"
  exit 1
fi

# Check for successful build commands
if jq -e '
  select(.type=="tool_call" and
         .tool=="Bash" and
         (.args.command | test("(make|go build|go test|npm run build|npm test|cargo build|mvn compile)")) and
         .result=="success")
' "$TRACE_FILE" > /dev/null 2>&1; then
  echo "PASS: Build/test command succeeded"
  exit 0
fi

# Check for Edit operations followed by errors
if jq -e 'select(.type=="tool_call" and .tool=="Edit")' "$TRACE_FILE" > /dev/null 2>&1; then
  if jq -e 'select(.type=="error" and (.message | test("(compile|build|syntax|SyntaxError|ParseError)")))' "$TRACE_FILE" > /dev/null 2>&1; then
    echo "FAIL: Build/syntax errors detected after code changes"
    exit 1
  fi
  echo "PASS: Code changes made, no build errors detected"
  exit 0
fi

echo "SKIP: No build operations or code changes found"
exit 2
