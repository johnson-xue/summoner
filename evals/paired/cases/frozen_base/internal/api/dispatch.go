package api

import (
	"fmt"
	"reflect"

	"inventoryd/internal/store"
)

// storeMethodAliases maps scripting/export accessor names to STORE
// METHOD NAMES, resolved via reflection at call time. Reflection hides
// these references from the compiler and from textual "find all
// callers" tooling — a store method rename MUST update the alias values
// (TestInvokeAccessor exercises every entry).
var storeMethodAliases = map[string]string{
	"item":   "GetByID",
	"lookup": "GetByID",
}

// InvokeAccessor runs a scripting accessor against the store. arg is
// the item id. Unknown accessors are caller errors; an alias pointing
// at a missing method is a programming error — both surface as errors
// here, never as panics (REVIEW-CRITERIA.md C6).
func (s *Service) InvokeAccessor(accessor, arg string) (string, error) {
	name, ok := storeMethodAliases[accessor]
	if !ok {
		return "", fmt.Errorf("api: unknown accessor %q", accessor)
	}
	m := reflect.ValueOf(s.St).MethodByName(name)
	if !m.IsValid() {
		return "", fmt.Errorf("api: accessor %q points at missing store method %q", accessor, name)
	}
	out := m.Call([]reflect.Value{reflect.ValueOf(arg)})
	if err, ok := out[1].Interface().(error); ok && err != nil {
		return "", err
	}
	item, _ := out[0].Interface().(store.Item)
	return fmt.Sprintf("%s %s %d", item.ID, item.Name, item.Stock), nil
}
