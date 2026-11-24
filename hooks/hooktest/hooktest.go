// Package hooktest supports the in-process tests of the hook binaries: the
// one place that puts the enforcement switches and the state directories
// into the state a test names.
package hooktest

import (
	"io/fs"
	"os"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// Run arms the opt-in and returns m.Run(). It is every hook package's
// TestMain body:
//
//	func TestMain(m *testing.M) { os.Exit(hooktest.Run(m)) }
//
// The in-process tests exercise the ENFORCING path — the opt-in one — so
// they start with the opt-in armed and the hatch closed, whatever the shell
// that runs `go test` carries (a developer who set the hatch globally must
// not watch every denial test fail). The dormant production default is
// pinned by each package's TestDormantDefault (which calls Dormant) and at
// the binary seam in hooks/contract.
func Run(m *testing.M) int {
	os.Unsetenv(shared.EnvHooksDisable)
	os.Setenv(shared.EnvHooksEnforce, "1")
	return m.Run()
}

// put sets key to value, or unsets it when value is "", and returns the
// function that restores what was there.
func put(key, value string) (restore func()) {
	old, had := os.LookupEnv(key)
	if value == "" {
		os.Unsetenv(key)
	} else {
		os.Setenv(key, value)
	}
	return func() {
		if had {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	}
}

// Put sets key to value ("" unsets it) and returns the function that
// restores what was there — for a test too old for t.Setenv (this module is
// Go 1.16): `t.Cleanup(hooktest.Put("KEY", "value"))`.
func Put(key, value string) (restore func()) {
	return put(key, value)
}

// Dormant puts both switches into the production default — no opt-in, no
// hatch — for the rest of the test.
func Dormant(t *testing.T) {
	t.Helper()
	t.Cleanup(put(shared.EnvHooksEnforce, ""))
	t.Cleanup(put(shared.EnvHooksDisable, ""))
}

// OptIn arms enforcement (and closes the hatch) for the rest of the test.
func OptIn(t *testing.T) {
	t.Helper()
	t.Cleanup(put(shared.EnvHooksEnforce, "1"))
	t.Cleanup(put(shared.EnvHooksDisable, ""))
}

// Hatch opens the un-brick hatch for the rest of the test, leaving the
// opt-in as it is — the hatch outranks it.
func Hatch(t *testing.T) {
	t.Helper()
	t.Cleanup(put(shared.EnvHooksDisable, "1"))
}

// IsolateState points the run state and the trace at fresh temp dirs and
// returns them: a test can assert what was and was not written, and nothing
// ever touches real Summoner state.
func IsolateState(t *testing.T) (stateDir, traceDir string) {
	t.Helper()
	stateDir, traceDir = t.TempDir(), t.TempDir()
	t.Cleanup(put("SUMMONER_STATE_DIR", stateDir))
	t.Cleanup(put("SUMMONER_TRACE_DIR", traceDir))
	return stateDir, traceDir
}

// CountFiles counts the regular files under dir, recursively; a dir that
// does not exist counts 0.
func CountFiles(dir string) int {
	n := 0
	fs.WalkDir(os.DirFS(dir), ".", func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			n++
		}
		return nil // an unreadable subtree counts what was readable
	})
	return n
}
