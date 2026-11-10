package connector

import "testing"

// TestSmokeInstantiate pins that every registered connector is usable:
// names resolve, small fetches work, backoffs are non-negative. This is
// a shape smoke test only — contract compliance (CONTRACT.md) is an
// audit task, not a test oracle.
func TestSmokeInstantiate(t *testing.T) {
	for _, c := range All() {
		if c.Name() == "" {
			t.Fatalf("nameless connector in registry")
		}
		recs, err := c.Fetch(2)
		if err != nil || len(recs) != 2 {
			t.Fatalf("%s: fetch = %d, %v", c.Name(), len(recs), err)
		}
		for k := 1; k <= c.MaxRetries(); k++ {
			if c.Backoff(k) < 0 {
				t.Fatalf("%s: negative backoff at attempt %d", c.Name(), k)
			}
		}
	}
}
