package hooktest

import (
	"os"
	"testing"
)

// TestPutRestoresBothOriginStates covers the branch the per-package tests
// cannot see: put() must restore a variable that was ABSENT to absent, not
// to the value the test body left behind.
func TestPutRestoresBothOriginStates(t *testing.T) {
	t.Cleanup(put("HOOKTEST_PROBE", "")) // force the origin to "absent"
	if _, had := os.LookupEnv("HOOKTEST_PROBE"); had {
		t.Skip("HOOKTEST_PROBE is set in the outer environment; cannot test the absent origin here")
	}
	restore := put("HOOKTEST_PROBE", "1")
	os.Setenv("HOOKTEST_PROBE", "touched-by-body")
	restore()
	if _, had := os.LookupEnv("HOOKTEST_PROBE"); had {
		t.Fatalf("restore re-created a variable whose origin was absent (value %q)", os.Getenv("HOOKTEST_PROBE"))
	}

	t.Cleanup(put("HOOKTEST_PROBE", "original")) // force the origin to "present"
	restore = put("HOOKTEST_PROBE", "temporary")
	if got := os.Getenv("HOOKTEST_PROBE"); got != "temporary" {
		t.Fatalf("put set %q, want temporary", got)
	}
	restore()
	if got := os.Getenv("HOOKTEST_PROBE"); got != "original" {
		t.Fatalf("restore left %q, want the original value", got)
	}
}
