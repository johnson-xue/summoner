package store

import (
	"reflect"
	"testing"
)

// TestPublicMethodSmoke keeps the reflect-based scripting bridge in
// package api honest: every exported method named here must stay
// resolvable BY NAME. These strings are DATA, not Go references — the
// compiler does not see them, and a rename that forgets this list
// breaks the bridge at runtime only.
func TestPublicMethodSmoke(t *testing.T) {
	st := New()
	for _, name := range []string{"Put", "GetByID", "List", "Delete", "BatchDelete"} {
		m := reflect.ValueOf(st).MethodByName(name)
		if !m.IsValid() {
			t.Fatalf("store method %q missing — scripting aliases are stale", name)
		}
	}
}
