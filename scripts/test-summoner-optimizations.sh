#!/bin/bash
# test-summoner-optimizations.sh — portable deterministic v2 smoke test.
#
# The pre-single-mode version hardcoded a /Users root, wrote into a fixed
# /tmp path, synthesized a v1 trace and asserted deleted v1 scorers, the
# deleted v1 stability runner and stale documentation names. This rewrite
# asserts the CURRENT survivor surface and exercises the real v2 scorer +
# derived sidecar on an isolated COPY of a fixture:
#   1. every survivor script / v2 scorer / hook binary / fixture exists
#   2. score-trace.sh scores a copied fixture PASS, writes the sidecar NEXT
#      TO the copy, never touches the authoritative fixture bytes and never
#      writes a sidecar into the repo fixtures dir
#   3. a hostile fixture (rubber-stamp review) scores overall FAIL
# Deterministic only — no model, no network, no cd into the checkout; all
# generated files live under a mktemp dir removed on exit.

set -u

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_ROOT="$(dirname "$SCRIPT_DIR")"

TMP="$(mktemp -d "${TMPDIR:-/tmp}/summoner-opt-smoke.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

PASS=0
FAIL=0
pass() { PASS=$((PASS + 1)); printf '✓ %s\n' "$1"; }
fail() { FAIL=$((FAIL + 1)); printf '✗ %s\n' "$1"; }

assert_exec() { # $1 = path — must exist and be executable
  if [[ -f "$1" && -x "$1" ]]; then
    pass "executable: $1"
  else
    fail "missing or not executable: $1"
  fi
}
assert_file() { # $1 = path — must exist
  if [[ -f "$1" ]]; then
    pass "present: $1"
  else
    fail "missing: $1"
  fi
}

echo "── 1. survivor scripts"
for s in \
  cleanup-traces.sh create-baseline.sh init-memory-db.sh install-smoke-test.sh \
  regression-test.sh run-routing-evals.sh run-trace-fixtures.sh score-trace.sh \
  summoner-bp.sh summoner-init.sh summoner-setup.sh validate-manifest.sh \
  validate-repo.sh verify-release.sh; do
  assert_exec "$PLUGIN_ROOT/scripts/$s"
done

echo "── 2. v2 contract scorers (scorers/deterministic/)"
for s in schema-check review-evidence-check user-gate-check recovery-check post-game-check; do
  assert_exec "$PLUGIN_ROOT/scorers/deterministic/$s.sh"
done

echo "── 3. hook binaries (the hooks/Makefile HOOKS list)"
for b in session-start pretooluse-gate stop validate-manifest posttooluse \
  subagentstop userpromptsubmit summoner-status summoner-sandbox-probe; do
  assert_exec "$PLUGIN_ROOT/hooks/bin/$b"
done

echo "── 4. v2 fixtures (post-T12/T13 survivor baseline)"
assert_file "$PLUGIN_ROOT/tests/fixtures/traces/valid-full-run.jsonl"
assert_file "$PLUGIN_ROOT/tests/fixtures/traces/invalid-review-rubber-stamp.jsonl"

echo "── 5. scorer + derived sidecar on an isolated copy"
FIXTURE="$PLUGIN_ROOT/tests/fixtures/traces/valid-full-run.jsonl"
cp "$FIXTURE" "$TMP/run.jsonl"
sha_before="$(shasum -a 256 "$FIXTURE" | cut -d' ' -f1)"
if bash "$PLUGIN_ROOT/scripts/score-trace.sh" --trace "$TMP/run.jsonl" >/dev/null 2>&1; then
  pass "score-trace.sh scores the positive fixture copy PASS"
else
  fail "score-trace.sh did not pass the positive fixture copy"
fi
if [[ -f "$TMP/run.jsonl.score.json" ]]; then
  pass "sidecar written next to the copied trace"
else
  fail "sidecar not written next to the copied trace"
fi
if [[ ! -e "$FIXTURE.score.json" ]]; then
  pass "no sidecar leaked into the repo fixtures dir"
else
  fail "sidecar leaked into the repo fixtures dir"
fi
overall="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["overall"]["status"])' "$TMP/run.jsonl.score.json" 2>/dev/null || true)"
if [[ "$overall" == "PASS" ]]; then
  pass "sidecar overall status = PASS"
else
  fail "sidecar overall status = ${overall:-<none>} (want PASS)"
fi
sha_after="$(shasum -a 256 "$FIXTURE" | cut -d' ' -f1)"
if [[ "$sha_before" == "$sha_after" ]]; then
  pass "authoritative fixture bytes untouched"
else
  fail "authoritative fixture bytes changed"
fi

echo "── 6. a hostile fixture fails overall"
cp "$PLUGIN_ROOT/tests/fixtures/traces/invalid-review-rubber-stamp.jsonl" "$TMP/bad.jsonl"
if bash "$PLUGIN_ROOT/scripts/score-trace.sh" --trace "$TMP/bad.jsonl" >/dev/null 2>&1; then
  fail "rubber-stamp fixture scored overall PASS (must FAIL)"
else
  pass "rubber-stamp fixture fails overall"
fi

echo "── 7. scorer selects the host-platform verifier (real canonical verification)"
PLATFORM_PLUGIN="$TMP/platform-plugin"
mkdir -p "$PLATFORM_PLUGIN/scripts" "$PLATFORM_PLUGIN/hooks/bin" "$TMP/platform-path"
cp "$PLUGIN_ROOT/scripts/score-trace.sh" "$PLATFORM_PLUGIN/scripts/score-trace.sh"
cp -R "$PLUGIN_ROOT/scorers" "$PLATFORM_PLUGIN/scorers"
# Select a REAL verifier for this host, then place it under each platform's
# literal fixture filename. Only uname is faked; hashing/scoring stay real.
case "$(uname -s)-$(uname -m)" in
  Darwin-x86_64) NATIVE_VERIFIER="$PLUGIN_ROOT/hooks/bin/summoner-status-darwin-amd64" ;;
  Darwin-arm64) NATIVE_VERIFIER="$PLUGIN_ROOT/hooks/bin/summoner-status-darwin-arm64" ;;
  Linux-x86_64) NATIVE_VERIFIER="$PLUGIN_ROOT/hooks/bin/summoner-status-linux-amd64" ;;
  *) NATIVE_VERIFIER="$PLUGIN_ROOT/hooks/bin/summoner-status" ;;
esac
cat > "$TMP/platform-path/uname" <<'UNAME'
#!/bin/bash
case "$1" in
  -s) printf '%s\n' "$TEST_UNAME_SYSTEM" ;;
  -m) printf '%s\n' "$TEST_UNAME_MACHINE" ;;
  *) exit 1 ;;
esac
UNAME
chmod +x "$TMP/platform-path/uname"
# A deliberately unusable unsuffixed copy proves the suffixed one was used:
# a scorer that still selects the tree binary FAILs on every platform case.
printf '%s\n' '#!/bin/bash' 'printf "wrong unsuffixed verifier selected\n" >&2' \
  'exit 71' > "$PLATFORM_PLUGIN/hooks/bin/summoner-status"
chmod +x "$PLATFORM_PLUGIN/hooks/bin/summoner-status"
for platform in Darwin:x86_64:darwin-amd64 Darwin:arm64:darwin-arm64 Linux:x86_64:linux-amd64; do
  IFS=: read -r test_system test_machine test_suffix <<< "$platform"
  cp "$NATIVE_VERIFIER" "$PLATFORM_PLUGIN/hooks/bin/summoner-status-$test_suffix"
  cp "$FIXTURE" "$TMP/platform-$test_suffix.jsonl"
  if PATH="$TMP/platform-path:$PATH" TEST_UNAME_SYSTEM="$test_system" TEST_UNAME_MACHINE="$test_machine" \
      bash "$PLATFORM_PLUGIN/scripts/score-trace.sh" --trace "$TMP/platform-$test_suffix.jsonl" >/dev/null 2>&1; then
    pass "scorer uses $test_suffix verifier instead of unusable unsuffixed copy"
  else
    fail "scorer failed with $test_suffix verifier available"
  fi
done
# Unknown hosts retain the native unsuffixed fallback; missing verifiers
# remain FAIL rather than certifying a trace without canonical verification.
cp "$NATIVE_VERIFIER" "$PLATFORM_PLUGIN/hooks/bin/summoner-status"
cp "$FIXTURE" "$TMP/platform-fallback.jsonl"
if PATH="$TMP/platform-path:$PATH" TEST_UNAME_SYSTEM=Unknown TEST_UNAME_MACHINE=unknown \
    bash "$PLATFORM_PLUGIN/scripts/score-trace.sh" --trace "$TMP/platform-fallback.jsonl" >/dev/null 2>&1; then
  pass "scorer keeps unsuffixed fallback when no host variant exists"
else
  fail "scorer lost the unsuffixed fallback"
fi
rm "$PLATFORM_PLUGIN/hooks/bin/summoner-status"
cp "$FIXTURE" "$TMP/platform-missing.jsonl"
if PATH="$TMP/platform-path:$PATH" TEST_UNAME_SYSTEM=Unknown TEST_UNAME_MACHINE=unknown \
    bash "$PLATFORM_PLUGIN/scripts/score-trace.sh" --trace "$TMP/platform-missing.jsonl" >/dev/null 2>&1; then
  fail "missing verifier certified a trace"
else
  missing_status=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["chain_verification"]["status"])' \
    "$TMP/platform-missing.jsonl.score.json" 2>/dev/null || true)
  if [[ "$missing_status" == "FAIL" ]]; then
    pass "missing verifier records chain FAIL in the derived sidecar"
  else
    fail "missing verifier did not record chain FAIL (${missing_status:-no sidecar})"
  fi
fi

echo "──"
if [[ $FAIL -eq 0 ]]; then
  echo "✓ optimization smoke green ($PASS assertions)"
  exit 0
fi
echo "✗ optimization smoke RED: $FAIL failed assertion(s), $PASS passed"
exit 1
