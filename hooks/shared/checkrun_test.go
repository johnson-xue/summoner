package shared

// checkrun_test.go — T19 suite, shared half: the check trust ladder's pure
// cores (L1 adapter ops with bounds, L2 exact-match + L3 network classifiers,
// the anchored sandbox-probe invocation shape, the adapter wrapper parser) and
// the trace scans the crash-recovery and probe-evidence joins run on.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// useCheckrunEnv points the project dir at a fresh temp tree.
func useCheckrunEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old, had := os.LookupEnv("CLAUDE_PROJECT_DIR")
	if err := os.Setenv("CLAUDE_PROJECT_DIR", dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			os.Setenv("CLAUDE_PROJECT_DIR", old)
		} else {
			os.Unsetenv("CLAUDE_PROJECT_DIR")
		}
	})
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- check refs ------------------------------------------------------------------

func TestParseCheckRef(t *testing.T) {
	for _, tc := range []struct {
		ref  string
		want string
		err  bool
	}{
		{"check://go-test", "go-test", false},
		{"check://go-test/tu-9abc", "go-test", false}, // pointer tail tolerated, never authority
		{"go-test", "", true},
		{"check://", "", true},
		{"check:///x", "", true},
		{"check://bad id", "", true},
		{"check://ok@digest", "", true}, // digest freezing belongs to envelopes, not refs
	} {
		got, err := ParseCheckRef(tc.ref)
		if tc.err {
			if err == nil {
				t.Errorf("ParseCheckRef(%q) = %q, want error", tc.ref, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("ParseCheckRef(%q) = (%q, %v), want (%q, nil)", tc.ref, got, err, tc.want)
		}
	}
}

// --- L1 adapter ops --------------------------------------------------------------

func l1Def(command string, args ...string) CheckDef {
	return CheckDef{CheckID: "c1", Command: command, Args: args, Trust: "framework"}
}

func TestL1FileContains(t *testing.T) {
	dir := useCheckrunEnv(t)
	writeFile(t, filepath.Join(dir, "report.md"), "alpha\nbeta\n")
	if got := RunL1Check(l1Def(CheckOpFileContains, "report.md", "beta"), time.Now().Add(time.Hour)); got.Status != "pass" {
		t.Errorf("file-contains hit = %+v, want pass", got)
	}
	if got := RunL1Check(l1Def(CheckOpFileContains, "report.md", "gamma"), time.Now().Add(time.Hour)); got.Status != "fail" {
		t.Errorf("file-contains miss = %+v, want fail", got)
	}
	// cwd resolution: def.Cwd is relative to the project dir.
	writeFile(t, filepath.Join(dir, "sub", "x.txt"), "needle")
	if got := RunL1Check(CheckDef{CheckID: "c1", Command: CheckOpFileContains, Args: []string{"x.txt", "needle"}, Cwd: "sub", Trust: "framework"}, time.Now().Add(time.Hour)); got.Status != "pass" {
		t.Errorf("file-contains via cwd = %+v, want pass", got)
	}
	if got := RunL1Check(l1Def(CheckOpFileContains, "absent.md", "x"), time.Now().Add(time.Hour)); got.Status != "blocked" {
		t.Errorf("file-contains absent file = %+v, want blocked (never a fabricated fail)", got)
	}
	if got := RunL1Check(l1Def(CheckOpFileContains, "report.md"), time.Now().Add(time.Hour)); got.Status != "blocked" {
		t.Errorf("file-contains wrong arity = %+v, want blocked", got)
	}
}

func TestL1DigestMatch(t *testing.T) {
	dir := useCheckrunEnv(t)
	content := "artifact bytes\n"
	writeFile(t, filepath.Join(dir, "a.bin"), content)
	sum := sha256.Sum256([]byte(content))
	good := hex.EncodeToString(sum[:])
	if got := RunL1Check(l1Def(CheckOpDigestMatch, "a.bin", good), time.Now().Add(time.Hour)); got.Status != "pass" {
		t.Errorf("digest-match = %+v, want pass", got)
	}
	bad := strings.Repeat("0", 64)
	if got := RunL1Check(l1Def(CheckOpDigestMatch, "a.bin", bad), time.Now().Add(time.Hour)); got.Status != "fail" {
		t.Errorf("digest-match mismatch = %+v, want fail", got)
	}
	if got := RunL1Check(l1Def(CheckOpDigestMatch, "a.bin", "nothex"), time.Now().Add(time.Hour)); got.Status != "blocked" {
		t.Errorf("digest-match malformed digest = %+v, want blocked", got)
	}
}

func TestL1GrepCount(t *testing.T) {
	dir := useCheckrunEnv(t)
	writeFile(t, filepath.Join(dir, "log.txt"), "hit one\nmiss\nhit two\n")
	if got := RunL1Check(l1Def(CheckOpGrepCount, "log.txt", "hit", "2"), time.Now().Add(time.Hour)); got.Status != "pass" {
		t.Errorf("grep-count exact = %+v, want pass", got)
	}
	if got := RunL1Check(l1Def(CheckOpGrepCount, "log.txt", "hit", "3"), time.Now().Add(time.Hour)); got.Status != "fail" {
		t.Errorf("grep-count wrong = %+v, want fail", got)
	}
	if got := RunL1Check(l1Def(CheckOpGrepCount, "log.txt", "hit[", "2"), time.Now().Add(time.Hour)); got.Status != "blocked" {
		t.Errorf("grep-count bad pattern = %+v, want blocked", got)
	}
	if got := RunL1Check(l1Def(CheckOpGrepCount, "log.txt", "hit", "two"), time.Now().Add(time.Hour)); got.Status != "blocked" {
		t.Errorf("grep-count bad want = %+v, want blocked", got)
	}
}

func TestL1NeverExecsUnknownCommandAndBounds(t *testing.T) {
	useCheckrunEnv(t)
	// An unknown adapter op is BLOCKED — never executed, never exec'd.
	if got := RunL1Check(l1Def("go", "test", "./..."), time.Now().Add(time.Hour)); got.Status != "blocked" || !strings.Contains(got.Reason, "l1") {
		t.Errorf("unknown op = %+v, want blocked with an l1 reason (framework adapters never exec registry commands)", got)
	}
	// Aggregate deadline already exhausted → blocked without touching the file.
	if got := RunL1Check(l1Def(CheckOpFileContains, "x", "y"), time.Now().Add(-time.Minute)); got.Status != "blocked" {
		t.Errorf("expired deadline = %+v, want blocked", got)
	}
	// Read cap: a file beyond L1MaxFileBytes is blocked, not silently truncated.
	dir := useCheckrunEnv(t)
	big := filepath.Join(dir, "big.bin")
	f, err := os.OpenFile(big, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(make([]byte, L1MaxFileBytes+1)); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if got := RunL1Check(l1Def(CheckOpFileContains, "big.bin", "x"), time.Now().Add(time.Hour)); got.Status != "blocked" {
		t.Errorf("oversized file = %+v, want blocked read cap", got)
	}
}

// --- L2 exact match + L3 network class -------------------------------------------

func TestMatchProjectCheckExactOnly(t *testing.T) {
	registry := []CheckDef{
		{CheckID: "unit", Command: "go", Args: []string{"test", "./..."}, Trust: "project"},
		{CheckID: "lint", Command: "gofmt", Args: []string{"-l", "."}, Trust: "project"},
		{CheckID: "contains", Command: CheckOpFileContains, Args: []string{"a", "b"}, Trust: "framework"},
	}
	for _, tc := range []struct {
		cmd  string
		want string
	}{
		{"go test ./...", "unit"},
		{"gofmt -l .", "lint"},
		{"go test ./... ", ""},           // trailing space: NOT the exact registered line
		{"go  test ./...", ""},           // inner spacing differs
		{"go test ./... && echo ok", ""}, // chained: never admitted as the check
		{"file-contains a b", ""},        // framework trust is not L2-admitted
		{"cat README.md", ""},            // unregistered
	} {
		def, ok := MatchProjectCheck(tc.cmd, registry)
		if tc.want == "" {
			if ok {
				t.Errorf("MatchProjectCheck(%q) matched %s, want none", tc.cmd, def.CheckID)
			}
			continue
		}
		if !ok || def.CheckID != tc.want {
			t.Errorf("MatchProjectCheck(%q) = (%v, %v), want %s", tc.cmd, def.CheckID, ok, tc.want)
		}
	}
}

func TestIsNetworkCheckCommand(t *testing.T) {
	if !IsNetworkCheckCommand(CheckDef{Command: "curl", Trust: "project"}) {
		t.Error("curl must classify L3 (network egress beyond sandbox isolation)")
	}
	if !IsNetworkCheckCommand(CheckDef{Command: "/usr/bin/wget", Args: []string{"-q", "https://x"}, Trust: "project"}) {
		t.Error("wget (absolute path) must classify L3")
	}
	if IsNetworkCheckCommand(CheckDef{Command: "go", Args: []string{"test"}, Trust: "project"}) {
		t.Error("go test is not network-class")
	}
}

// --- sandbox-probe invocation + wrapper ------------------------------------------

func TestIsSandboxProbeInvocationAnchoredOnly(t *testing.T) {
	for _, ok := range []string{
		`"/root/hooks/bin/summoner-sandbox-probe"`,
		"${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-sandbox-probe",
	} {
		if !IsSandboxProbeInvocation(ok) {
			t.Errorf("IsSandboxProbeInvocation(%q) = false, want true (anchored plugin-bin form)", ok)
		}
	}
	for _, bad := range []string{
		"summoner-sandbox-probe",                       // bare: PATH-shadowable, never the probe channel
		"./summoner-sandbox-probe",                     // relative shadow
		"/usr/local/bin/summoner-sandbox-probe",        // foreign directory prefix
		"/root/hooks/bin/summoner-sandbox-probe extra", // operands break the fixed shape
		"/root/hooks/bin/summoner-sandbox-probe; id",
		"/root/hooks/bin/summoner-sandbox-probe > out", // a redirect is not the probe channel
	} {
		if IsSandboxProbeInvocation(bad) {
			t.Errorf("IsSandboxProbeInvocation(%q) = true, want false", bad)
		}
	}
}

func TestParseSandboxProbeWrapper(t *testing.T) {
	effect, ok := ParseSandboxProbeWrapper(`{"probe":"summoner-sandbox-probe/v1","effect":"refused","detail":"EPERM"}`)
	if !ok || effect != SandboxProbeRefused {
		t.Fatalf("wrapper parse = (%q, %v), want (refused, true)", effect, ok)
	}
	if _, ok := ParseSandboxProbeWrapper("ordinary command output"); ok {
		t.Error("plain output must not parse as a probe wrapper")
	}
	// Last wrapper line wins.
	effect, _ = ParseSandboxProbeWrapper("noise\n" + `{"probe":"summoner-sandbox-probe/v1","effect":"writable"}` + "\n" + `{"probe":"summoner-sandbox-probe/v1","effect":"unproven"}`)
	if effect != SandboxProbeUnproven {
		t.Errorf("last wrapper wins: effect = %q, want unproven", effect)
	}
}

// --- trace scans ------------------------------------------------------------------

func scanEvent(typ, toolUseID string, seq int, payload map[string]interface{}) TraceEvent {
	return TraceEvent{Type: typ, ToolUseID: toolUseID, Seq: seq, Payload: payload}
}

func TestMutatingDanglingIntents(t *testing.T) {
	evs := []TraceEvent{
		scanEvent(EventWorkflowStarted, "", 1, map[string]interface{}{"workflow": "fix"}),
		// Dangling write-class Bash → crash candidate.
		scanEvent(EventToolStarted, "tu-w1", 2, map[string]interface{}{"tool_name": "Bash"}),
		// Dangling read-class Bash → ignored (no uncertain external mutation).
		scanEvent(EventToolStarted, "tu-r1", 3, map[string]interface{}{"tool_name": "Bash", "read_class": true}),
		scanEvent(EventToolResult, "tu-r1", 4, nil),
		// Dangling Edit → crash candidate.
		scanEvent(EventToolStarted, "tu-e1", 5, map[string]interface{}{"tool_name": "Edit"}),
		// A resolved Write → not dangling.
		scanEvent(EventToolStarted, "tu-w2", 6, map[string]interface{}{"tool_name": "Write"}),
		scanEvent(EventToolResult, "tu-w2", 7, nil),
		// A failed write-class Bash → resolved by its failure.
		scanEvent(EventToolStarted, "tu-w3", 8, map[string]interface{}{"tool_name": "Bash"}),
		scanEvent(EventToolFailed, "tu-w3", 9, nil),
		// Skill bookkeeping (phase-submit channel) → ignored.
		scanEvent(EventToolStarted, "tu-s1", 10, map[string]interface{}{"tool_name": "Skill"}),
		// A reviewer delegation still unmatched → NEVER a crash candidate.
		scanEvent(EventDelegationStarted, "tu-a1", 11, map[string]interface{}{"subagent_type": "reviewer"}),
		// Worker delegations: two started, one completed → exactly the second dangles (FIFO).
		scanEvent(EventDelegationStarted, "tu-a2", 12, map[string]interface{}{"subagent_type": "worker"}),
		scanEvent(EventDelegationStarted, "tu-a3", 13, map[string]interface{}{"subagent_type": "summoner:worker"}),
		scanEvent(EventDelegationCompleted, "", 14, map[string]interface{}{"agent_type": "summoner:worker"}),
	}
	got := MutatingDanglingIntents(evs, true)
	want := []DanglingIntent{
		{ToolUseID: "tu-w1", Kind: "tool", ToolName: "Bash"},
		{ToolUseID: "tu-e1", Kind: "tool", ToolName: "Edit"},
		{ToolUseID: "tu-a3", Kind: "worker-delegation", ToolName: "Agent"},
	}
	if len(got) != len(want) {
		t.Fatalf("dangling = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dangling[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	// The Stop variant never converts delegations (a background worker may
	// legitimately still be running at a normal per-turn Stop).
	if got := MutatingDanglingIntents(evs, false); len(got) != 2 {
		t.Errorf("Stop scan (delegations off) = %+v, want only the two tool intents", got)
	}
	// Duplicate scans over the same events are pure — the caller's dedupe is
	// MarkOutcomeUnknown idempotency plus the receipt check.
	if again := MutatingDanglingIntents(evs, true); len(again) != len(got) {
		t.Error("scan must be deterministic")
	}
}

func TestProbeEvidenceJoins(t *testing.T) {
	mark := scanEvent(EventToolOutcomeUnknown, "tu-u1", 10, map[string]interface{}{"tool_use_id": "tu-u1"})
	evs := []TraceEvent{
		scanEvent(EventToolStarted, "tu-p1", 11, map[string]interface{}{"tool_name": "Bash", "read_class": true}),
		scanEvent(EventToolResult, "tu-p1", 12, map[string]interface{}{"tool_name": "Bash"}),
		mark,
		// A probe result recorded BEFORE the marking: prior-episode evidence, stale.
		scanEvent(EventToolStarted, "tu-p0", 5, map[string]interface{}{"tool_name": "Bash", "read_class": true}),
		scanEvent(EventToolResult, "tu-p0", 6, nil),
		// A failed probe: no observation, never evidence.
		scanEvent(EventToolStarted, "tu-pf", 13, map[string]interface{}{"tool_name": "Bash", "read_class": true}),
		scanEvent(EventToolFailed, "tu-pf", 14, nil),
		// A write-class Bash "probe": not the read-class probe channel.
		scanEvent(EventToolStarted, "tu-pw", 15, map[string]interface{}{"tool_name": "Bash"}),
		scanEvent(EventToolResult, "tu-pw", 16, nil),
		// A read-class non-Bash call: not a probe.
		scanEvent(EventToolStarted, "tu-pr", 17, map[string]interface{}{"tool_name": "Read"}),
		scanEvent(EventToolResult, "tu-pr", 18, nil),
	}
	seq, ok := MarkingSeq(evs, "tu-u1")
	if !ok || seq != 10 {
		t.Fatalf("MarkingSeq = (%d, %v), want (10, true)", seq, ok)
	}
	if err := ProbeEvidence(evs, "tu-u1", "tu-p1", seq); err != nil {
		t.Errorf("valid probe evidence rejected: %v", err)
	}
	for _, bad := range []string{"tu-p0", "tu-pf", "tu-pw", "tu-pr", "tu-absent"} {
		if err := ProbeEvidence(evs, "tu-u1", bad, seq); err == nil {
			t.Errorf("probe %q must be rejected as evidence", bad)
		}
	}
	if _, ok := MarkingSeq(evs, "tu-other"); ok {
		t.Error("unknown id must have no marking event")
	}
}

func TestLatestSandboxProbe(t *testing.T) {
	evs := []TraceEvent{
		scanEvent(EventToolResult, "tu-q1", 3, map[string]interface{}{"sandbox_probe": map[string]interface{}{"effect": "refused"}}),
		scanEvent(EventToolResult, "tu-q2", 7, map[string]interface{}{"sandbox_probe": map[string]interface{}{"effect": "writable"}}),
		scanEvent(EventToolResult, "tu-q3", 9, map[string]interface{}{"sandbox_probe": map[string]interface{}{"effect": "refused"}}),
		scanEvent(EventToolResult, "tu-x", 10, nil),
	}
	id, refused, found := LatestSandboxProbe(evs, 8) // bound between q2 and q3
	if !found || id != "tu-q2" || refused {
		t.Fatalf("latest before 8 = (%q, %v, %v), want (tu-q2, writable→false, true)", id, refused, found)
	}
	id, refused, found = LatestSandboxProbe(evs, 0) // unbounded: latest overall
	if !found || id != "tu-q3" || !refused {
		t.Fatalf("latest overall = (%q, %v, %v), want (tu-q3, refused, true)", id, refused, found)
	}
	if _, _, found := LatestSandboxProbe(evs, 2); found {
		t.Error("no observation before seq 2")
	}
}

func TestNewestCheckResultAndL2Binding(t *testing.T) {
	evs := []TraceEvent{
		scanEvent(EventCheckResult, "", 3, map[string]interface{}{"check_id": "unit", "status": "fail", "digest": "d-old"}),
		scanEvent(EventCheckResult, "", 5, map[string]interface{}{"check_id": "unit", "status": "pass", "digest": "d-new"}),
		scanEvent(EventCheckResult, "", 6, map[string]interface{}{"check_id": "lint", "status": "blocked", "reason": "l3_unsupported"}),
	}
	ev, ok := NewestCheckResult(evs, "unit")
	if !ok || ev.Seq != 5 || ev.Payload["digest"] != "d-new" {
		t.Fatalf("newest unit result = %+v, want seq 5/d-new", ev)
	}
	if _, ok := NewestCheckResult(evs, "absent"); ok {
		t.Error("absent check must find nothing")
	}
	// L2 admission binding lookup by the executing call's tool_use_id.
	evs = append(evs,
		scanEvent(EventToolStarted, "tu-l2", 7, map[string]interface{}{
			"tool_name":  "Bash",
			"read_class": true,
			"l2_check":   map[string]interface{}{"check_id": "unit", "digest": "d-new", "cwd": ".", "env_allow": []interface{}{"PATH"}},
		}),
		scanEvent(EventToolResult, "tu-l2", 8, nil),
	)
	binding, seq, ok := L2Admission(evs, "tu-l2")
	if !ok || seq != 7 || binding["check_id"] != "unit" {
		t.Fatalf("L2Admission = (%+v, %d, %v), want unit @7", binding, seq, ok)
	}
	if _, _, ok := L2Admission(evs, "tu-x"); ok {
		t.Error("unadmitted call has no L2 binding")
	}
}
