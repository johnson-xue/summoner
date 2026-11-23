package main

// env_export_test.go — 0.7.0 D5.2: SessionStart exports the session id into
// the session's environment file (CLAUDE_ENV_FILE), so a Bash-invoked
// `summoner-status <<< '{}'` can scope to THIS session. The incident
// (2026-11-23): three live runs in one project, a `{}` status payload, and
// a viewer that could only answer "ambiguous" — the model then had no way
// to render its own checkpoint.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sessionStartPayload(sessionID string) string {
	return `{"hook_event_name":"SessionStart","session_id":"` + sessionID + `","source":"startup"}`
}

func runSessionStartOn(t *testing.T, sessionID string) int {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runSessionStart(strings.NewReader(sessionStartPayload(sessionID)), &stdout, &stderr)
	if stdout.Len() == 0 {
		t.Fatal("fixture: SessionStart must still emit its context object")
	}
	return code
}

// TestSessionStartExportsTheSessionID: with an environment file the hook
// appends ONE export line for its session id, after whatever the file
// already holds, and exports the same id again without duplicating the line
// (SessionStart fires on resume and compact too).
func TestSessionStartExportsTheSessionID(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "claude.env")
	if err := os.WriteFile(envFile, []byte("export OTHER=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer setenvS(t, "CLAUDE_ENV_FILE", envFile)()
	defer setenvS(t, "CLAUDE_PROJECT_DIR", dir)()

	if code := runSessionStartOn(t, "sess-abc_123"); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	got, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "export OTHER=1\nexport SUMMONER_SESSION_ID='sess-abc_123'\n"
	if string(got) != want {
		t.Fatalf("environment file = %q, want %q", got, want)
	}

	// A second SessionStart (resume, compact) for the same session changes
	// nothing; for another session it appends that one.
	if code := runSessionStartOn(t, "sess-abc_123"); code != 0 {
		t.Fatalf("re-fire exit = %d, want 0", code)
	}
	if got, _ = os.ReadFile(envFile); string(got) != want {
		t.Fatalf("a re-fire must not duplicate the export line, got %q", got)
	}
	if code := runSessionStartOn(t, "sess-def"); code != 0 {
		t.Fatalf("second session exit = %d, want 0", code)
	}
	if got, _ = os.ReadFile(envFile); !strings.HasSuffix(string(got), "\nexport SUMMONER_SESSION_ID='sess-def'\n") {
		t.Fatalf("a new session must append its own export line, got %q", got)
	}
}

// TestSessionStartWithoutEnvFileExportsNothing: no CLAUDE_ENV_FILE (env
// capture off) means no file to write — the viewer keeps its fallback.
func TestSessionStartWithoutEnvFileExportsNothing(t *testing.T) {
	dir := t.TempDir()
	old, had := os.LookupEnv("CLAUDE_ENV_FILE")
	os.Unsetenv("CLAUDE_ENV_FILE")
	t.Cleanup(func() {
		if had {
			os.Setenv("CLAUDE_ENV_FILE", old)
		}
	})
	defer setenvS(t, "CLAUDE_PROJECT_DIR", dir)()

	if code := runSessionStartOn(t, "sess-1"); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("nothing may be written without an environment file, got %v (%v)", entries, err)
	}
}

// TestSessionStartNeverExportsAnUnsafeSessionId: the export line is sourced
// by the user's shell. A session id outside the safe charset is skipped
// rather than escaped into something that could run.
func TestSessionStartNeverExportsAnUnsafeSessionId(t *testing.T) {
	for _, id := range []string{"", "sess'x", "a;rm -rf /", "a b", "$(id)", "a\nb", "../x"} {
		dir := t.TempDir()
		envFile := filepath.Join(dir, "claude.env")
		if err := os.WriteFile(envFile, []byte("export OTHER=1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		sessions := []func(){
			setenvS(t, "CLAUDE_ENV_FILE", envFile),
			setenvS(t, "CLAUDE_PROJECT_DIR", dir),
		}
		for _, restore := range sessions {
			defer restore()
		}
		if code := runSessionStartOn(t, id); code != 0 {
			t.Fatalf("%q: exit = %d, want 0", id, code)
		}
		if got, _ := os.ReadFile(envFile); string(got) != "export OTHER=1\n" {
			t.Errorf("session id %q must not reach the environment file, got %q", id, got)
		}
	}
}
