package api

import (
	"errors"
	"os"
	"strings"
	"testing"

	"inventoryd/internal/store"
)

func TestCreateItemRejectsInvalid(t *testing.T) {
	svc := NewService(store.New(), quietLogger())
	if _, err := svc.CreateItem(store.Item{ID: "", Name: "x"}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("invalid create must surface store.ErrInvalid (C2), got %v", err)
	}
}

func TestQueryItemsFilter(t *testing.T) {
	svc := NewService(store.New(), quietLogger())
	for _, it := range []store.Item{
		{ID: "A", Name: "油壶", PriceCents: 100, Stock: 1},
		{ID: "B", Name: "Turkey Baster", PriceCents: 200, Stock: 2},
	} {
		if _, err := svc.CreateItem(it); err != nil {
			t.Fatal(err)
		}
	}
	got, err := svc.QueryItems("baster")
	if err != nil || len(got) != 1 || got[0].ID != "B" {
		t.Fatalf("QueryItems(baster) = %+v, %v", got, err)
	}
	if all, _ := svc.QueryItems(""); len(all) != 2 {
		t.Fatalf("empty query must list all, got %d", len(all))
	}
}

func TestExportReportSanitized(t *testing.T) {
	svc := NewService(store.New(), quietLogger())
	if _, err := svc.CreateItem(store.Item{ID: "A", Name: "x", PriceCents: 1, Stock: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExportReport(t.TempDir(), "../escape.txt"); err == nil {
		t.Fatalf("traversal filename must be rejected (C1)")
	}
	p, err := svc.ExportReport(t.TempDir(), "daily.txt")
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(p); err != nil || !strings.HasPrefix(string(b), "inventory export") {
		t.Fatalf("export roundtrip = %q, %v", b, err)
	}
}

// TestInvokeAccessor exercises every scripting alias end to end,
// including the reflect resolution (aliases are data, not compiled
// references).
func TestInvokeAccessor(t *testing.T) {
	svc := NewService(store.New(), quietLogger())
	if _, err := svc.CreateItem(store.Item{ID: "A-1", Name: "油壶", PriceCents: 100, Stock: 3}); err != nil {
		t.Fatal(err)
	}
	for _, acc := range []string{"item", "lookup"} {
		got, err := svc.InvokeAccessor(acc, "A-1")
		if err != nil {
			t.Fatalf("%s: %v", acc, err)
		}
		if !strings.Contains(got, "A-1") || !strings.Contains(got, "油壶") {
			t.Fatalf("%s = %q", acc, got)
		}
	}
	if _, err := svc.InvokeAccessor("item", "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing id must map ErrNotFound, got %v", err)
	}
	if _, err := svc.InvokeAccessor("nope", "A-1"); err == nil {
		t.Fatalf("unknown accessor must error (C6)")
	}
}
