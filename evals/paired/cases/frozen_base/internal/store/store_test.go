package store

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestPutValidation(t *testing.T) {
	st := New()
	for _, it := range []Item{
		{ID: "", Name: "x", PriceCents: 1},
		{ID: "a", Name: "", PriceCents: 1},
		{ID: "a", Name: "x", PriceCents: -1},
	} {
		if err := st.Put(it); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Put(%+v) = %v, want ErrInvalid", it, err)
		}
	}
	if err := st.Put(Item{ID: "a", Name: "freebie", PriceCents: 0}); err != nil {
		t.Fatalf("zero price is valid: %v", err)
	}
}

func TestGetByID(t *testing.T) {
	st := New()
	if err := st.Put(Item{ID: "A", Name: "油壶"}); err != nil {
		t.Fatal(err)
	}
	if it, err := st.GetByID("A"); err != nil || it.Name != "油壶" {
		t.Fatalf("GetByID(A) = %+v, %v", it, err)
	}
	if _, err := st.GetByID("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing id = %v, want ErrNotFound", err)
	}
}

// TestListOrdersByID pins the contract: output ordered by ID ascending,
// insertion order is NOT a contract.
func TestListOrdersByID(t *testing.T) {
	st := New()
	for _, id := range []string{"C", "A", "B"} {
		if err := st.Put(Item{ID: id, Name: "n" + id}); err != nil {
			t.Fatal(err)
		}
	}
	got := st.List()
	want := []string{"A", "B", "C"}
	if len(got) != len(want) {
		t.Fatalf("List() has %d items, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i] {
			t.Fatalf("List()[%d].ID = %q, want %q", i, got[i].ID, want[i])
		}
	}
}

// TestBatchDeleteFailsFast pins C2: the batch stops at the first error
// and later ids remain untouched.
func TestBatchDeleteFailsFast(t *testing.T) {
	st := New()
	for _, id := range []string{"A", "B", "C"} {
		if err := st.Put(Item{ID: id, Name: "n" + id}); err != nil {
			t.Fatal(err)
		}
	}
	err := st.BatchDelete([]string{"A", "MISSING", "C"})
	var be *BatchError
	if !errors.As(err, &be) {
		t.Fatalf("BatchDelete = %v, want *BatchError", err)
	}
	if be.Failed != "MISSING" || len(be.Done) != 1 || be.Done[0] != "A" {
		t.Fatalf("partial state = %+v", *be)
	}
	if _, err := st.GetByID("C"); err != nil {
		t.Fatalf("C must survive fail-fast stop: %v", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	st := New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st.Put(Item{ID: fmt.Sprintf("i-%d", i), Name: "x"})
			st.List()
		}(i)
	}
	wg.Wait()
	if got := len(st.List()); got != 8 {
		t.Fatalf("List() = %d items, want 8", got)
	}
}
