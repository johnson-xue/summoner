#!/bin/bash
# test-summoner-optimizations.sh — Comprehensive test suite for Summoner optimizations
# Tests all features added in PR #8

set -o pipefail

# Colors
GREEN='\033[32m'
RED='\033[31m'
YELLOW='\033[33m'
CYAN='\033[36m'
BOLD='\033[1m'
NC='\033[0m'

# Counters
PASS_COUNT=0
FAIL_COUNT=0
SKIP_COUNT=0

# Helper functions
test_start() {
    printf "\n${CYAN}${BOLD}=== $1 ===${NC}\n\n"
}

test_pass() {
    PASS_COUNT=$((PASS_COUNT + 1))
    printf "${GREEN}✓${NC} $1\n"
}

test_fail() {
    FAIL_COUNT=$((FAIL_COUNT + 1))
    printf "${RED}✗${NC} $1\n"
}

test_skip() {
    SKIP_COUNT=$((SKIP_COUNT + 1))
    printf "${YELLOW}⊘${NC} $1\n"
}

# Setup
SUMMONER_ROOT="/Users/admin/summoner"
TEST_DIR="/tmp/summoner-test-$$"
mkdir -p "$TEST_DIR"
cd "$TEST_DIR"

printf "${BOLD}Summoner Optimization Test Suite${NC}\n"
printf "==================================\n"
printf "Test Directory: $TEST_DIR\n"
printf "Summoner Root: $SUMMONER_ROOT\n\n"

# Test 1: Check if all scripts exist
test_start "Test 1: Script Availability"

if [ -x "$SUMMONER_ROOT/scripts/summoner-setup.sh" ]; then
    test_pass "summoner-setup.sh exists and is executable"
else
    test_fail "summoner-setup.sh missing or not executable"
fi

if [ -x "$SUMMONER_ROOT/scripts/score-trace.sh" ]; then
    test_pass "score-trace.sh exists and is executable"
else
    test_fail "score-trace.sh missing or not executable"
fi

if [ -x "$SUMMONER_ROOT/scripts/create-baseline.sh" ]; then
    test_pass "create-baseline.sh exists and is executable"
else
    test_fail "create-baseline.sh missing or not executable"
fi

if [ -x "$SUMMONER_ROOT/scripts/regression-test.sh" ]; then
    test_pass "regression-test.sh exists and is executable"
else
    test_fail "regression-test.sh missing or not executable"
fi

if [ -x "$SUMMONER_ROOT/scripts/stability-test.sh" ]; then
    test_pass "stability-test.sh exists and is executable"
else
    test_fail "stability-test.sh missing or not executable"
fi

# Test 2: Deterministic Scorers
test_start "Test 2: Deterministic Scorers"

for scorer in iron-law-check build-check test-pass-rate lint-check; do
    if [ -x "$SUMMONER_ROOT/scorers/deterministic/${scorer}.sh" ]; then
        test_pass "${scorer}.sh exists and is executable"
    else
        test_fail "${scorer}.sh missing or not executable"
    fi
done

# Test 3: Documentation
test_start "Test 3: Documentation"

docs=(
    "trace-protocol.md"
    "scoring-system.md"
    "PROPOSAL-trace-and-scoring.md"
    "CODE_REVIEW_PR8.md"
    "BASELINE_REGRESSION_GUIDE.md"
    "INIT_OPTIMIZATION_PROPOSAL.md"
)

for doc in "${docs[@]}"; do
    if [ -f "$SUMMONER_ROOT/docs/$doc" ] || [ -f "$SUMMONER_ROOT/references/$doc" ]; then
        test_pass "$doc exists"
    else
        test_fail "$doc missing"
    fi
done

# Test 4: Trace Format Validation
test_start "Test 4: Trace Format Validation"

cat > test-trace.jsonl << 'EOF'
{"type":"session_start","timestamp":"2026-06-24T10:00:00Z","workflow":"fix","session_id":"test-001","project":"test-project","model":"claude-opus-4-8"}
{"type":"phase_start","timestamp":"2026-06-24T10:00:05Z","phase":1,"name":"diagnose","skill":"debug"}
{"type":"tool_call","timestamp":"2026-06-24T10:00:10Z","tool":"Read","args":{"file_path":"test.go"},"result":"success","duration_ms":100}
{"type":"phase_end","timestamp":"2026-06-24T10:00:20Z","phase":1,"status":"completed","artifacts":["root_cause"]}
{"type":"session_end","timestamp":"2026-06-24T10:01:00Z","status":"completed","total_duration_ms":60000}
EOF

if [ -f test-trace.jsonl ]; then
    # Validate JSON format
    if python3 -c "import json; [json.loads(line) for line in open('test-trace.jsonl')]" 2>/dev/null; then
        test_pass "Test trace file is valid JSONL"
    else
        test_fail "Test trace file has invalid JSON"
    fi

    # Check required fields
    if grep -q '"type":"session_start"' test-trace.jsonl && \
       grep -q '"project":"test-project"' test-trace.jsonl; then
        test_pass "Test trace has required fields"
    else
        test_fail "Test trace missing required fields"
    fi
else
    test_fail "Failed to create test trace file"
fi

# Test 5: Scoring System
test_start "Test 5: Scoring System"

if [ -f test-trace.jsonl ]; then
    output=$("$SUMMONER_ROOT/scripts/score-trace.sh" --trace test-trace.jsonl --priority P0 2>&1)

    if echo "$output" | grep -q "📊 Scoring Trace"; then
        test_pass "Scoring script executes successfully"
    else
        test_fail "Scoring script failed to execute"
    fi

    if echo "$output" | grep -q "iron-law-check"; then
        test_pass "Iron law check runs"
    else
        test_fail "Iron law check not executed"
    fi

    if echo "$output" | grep -q "Total:"; then
        test_pass "Score calculation completes"
    else
        test_fail "Score calculation failed"
    fi

    if echo "$output" | grep -q "PASS\|FAIL"; then
        test_pass "Pass/fail determination works"
    else
        test_fail "Pass/fail determination missing"
    fi
else
    test_skip "Scoring test (no trace file)"
fi

# Test 6: Skills Registration
test_start "Test 6: Skills Registration"

if [ -f "$SUMMONER_ROOT/plugin.json" ]; then
    if grep -q '"name": "summoner-setup"' "$SUMMONER_ROOT/plugin.json"; then
        test_pass "summoner-setup skill registered"
    else
        test_fail "summoner-setup skill not registered"
    fi

    if [ -f "$SUMMONER_ROOT/skills/summoner-setup/SKILL.md" ]; then
        test_pass "summoner-setup skill file exists"
    else
        test_fail "summoner-setup skill file missing"
    fi

    if [ -f "$SUMMONER_ROOT/skills/auto-setup-summoner/SKILL.md" ]; then
        test_pass "auto-setup-summoner skill file exists"
    else
        test_fail "auto-setup-summoner skill file missing"
    fi
else
    test_fail "plugin.json not found"
fi

# Test 7: Hooks
test_start "Test 7: Hooks Enhancement"

if [ -f "$SUMMONER_ROOT/hooks/bin/session-start" ]; then
    test_pass "session-start hook binary exists"

    if [ -x "$SUMMONER_ROOT/hooks/bin/session-start" ]; then
        test_pass "session-start hook is executable"
    else
        test_fail "session-start hook not executable"
    fi
else
    test_fail "session-start hook binary missing"
fi

if [ -f "$SUMMONER_ROOT/hooks/session-start/main.go" ]; then
    if grep -q "setup summoner" "$SUMMONER_ROOT/hooks/session-start/main.go"; then
        test_pass "Hook contains friendly setup message"
    else
        test_fail "Hook missing friendly setup message"
    fi
fi

# Test 8: Test Fixtures
test_start "Test 8: Test Fixtures"

if [ -f "$SUMMONER_ROOT/tests/fixtures/traces/valid-fix-workflow.jsonl" ]; then
    test_pass "valid-fix-workflow.jsonl fixture exists"
else
    test_fail "valid-fix-workflow.jsonl fixture missing"
fi

if [ -f "$SUMMONER_ROOT/tests/fixtures/traces/invalid-missing-phase1.jsonl" ]; then
    test_pass "invalid-missing-phase1.jsonl fixture exists"
else
    test_fail "invalid-missing-phase1.jsonl fixture missing"
fi

# Test 9: README Update
test_start "Test 9: README Update"

if [ -f "$SUMMONER_ROOT/README.md" ]; then
    if grep -q "summoner-setup.sh" "$SUMMONER_ROOT/README.md"; then
        test_pass "README mentions summoner-setup.sh"
    else
        test_fail "README missing summoner-setup.sh reference"
    fi

    if ! grep -q "线上报错" "$SUMMONER_ROOT/README.md"; then
        test_pass "README has no mixed language (Chinese/English)"
    else
        test_fail "README still has mixed language"
    fi
else
    test_fail "README.md not found"
fi

# Summary
printf "\n${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n"
printf "${BOLD}Test Results${NC}\n"
printf "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n"
printf "${GREEN}✓ Passed:${NC}  %d\n" "$PASS_COUNT"
printf "${RED}✗ Failed:${NC}  %d\n" "$FAIL_COUNT"
printf "${YELLOW}⊘ Skipped:${NC} %d\n" "$SKIP_COUNT"
printf "\nTotal: %d tests\n" "$((PASS_COUNT + FAIL_COUNT + SKIP_COUNT))"

# Cleanup
cd /
rm -rf "$TEST_DIR"

# Exit code
if [ "$FAIL_COUNT" -gt 0 ]; then
    printf "\n${RED}${BOLD}Some tests failed!${NC}\n"
    exit 1
else
    printf "\n${GREEN}${BOLD}All tests passed!${NC}\n"
    exit 0
fi
