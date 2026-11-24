package shared

import (
	"os"
	"testing"
)

// setOrUnset puts one switch into the state a case names and restores the
// process environment when the test ends.
func setOrUnset(t *testing.T, key, value string, set bool) {
	t.Helper()
	old, had := os.LookupEnv(key)
	if set {
		os.Setenv(key, value)
	} else {
		os.Unsetenv(key)
	}
	t.Cleanup(func() {
		if had {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})
}

// TestHooksDormant is the whole switch contract in one table. The first row
// is the production default: nobody who merely installed the plugin is
// gated.
func TestHooksDormant(t *testing.T) {
	cases := []struct {
		name        string
		enforce     string
		enforceSet  bool
		disable     string
		disableSet  bool
		wantDormant bool
	}{
		{
			name:        "default — neither switch set: dormant",
			wantDormant: true,
		},
		{
			name:       "the opt-in arms the hooks",
			enforce:    "1",
			enforceSet: true,
		},
		{
			name:        "only the exact value 1 arms them",
			enforce:     "true",
			enforceSet:  true,
			wantDormant: true,
		},
		{
			name:        "an empty opt-in is no opt-in",
			enforceSet:  true,
			wantDormant: true,
		},
		{
			name:       "the hatch alone is not dormancy — its own contract applies",
			disable:    "1",
			disableSet: true,
		},
		{
			name:       "the hatch outranks the opt-in",
			enforce:    "1",
			enforceSet: true,
			disable:    "1",
			disableSet: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setOrUnset(t, EnvHooksEnforce, tc.enforce, tc.enforceSet)
			setOrUnset(t, EnvHooksDisable, tc.disable, tc.disableSet)
			if got := HooksDormant(); got != tc.wantDormant {
				t.Fatalf("HooksDormant() = %v, want %v", got, tc.wantDormant)
			}
		})
	}
}
