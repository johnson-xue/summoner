// Package api adapts the store into application operations used by the
// CLI and the (planned) HTTP layer.
package api

import (
	"io"
	"log"
	"os"
	"strings"

	"inventoryd/internal/report"
	"inventoryd/internal/store"
)

// Service bundles the inventory store with an audit logger.
type Service struct {
	St    *store.Store
	Audit *log.Logger
}

// NewService wires a service; a nil audit logger falls back to stderr.
func NewService(st *store.Store, audit *log.Logger) *Service {
	if audit == nil {
		audit = log.New(os.Stderr, "audit ", log.LstdFlags)
	}
	return &Service{St: st, Audit: audit}
}

// CreateItem validates and stores one item. Invalid input is rejected
// with store.ErrInvalid and callers must surface it — never assume
// success (REVIEW-CRITERIA.md C2).
func (s *Service) CreateItem(it store.Item) (store.Item, error) {
	if err := s.St.Put(it); err != nil {
		return store.Item{}, err
	}
	s.Audit.Printf("created item %s", it.ID)
	return it, nil
}

// QueryItems returns items whose name contains the case-insensitive
// substring q. An empty q lists everything, ordered by ID (store.List
// contract — insertion order is not a contract).
func (s *Service) QueryItems(q string) ([]store.Item, error) {
	var out []store.Item
	needle := strings.ToLower(q)
	for _, it := range s.St.List() {
		if needle == "" || strings.Contains(strings.ToLower(it.Name), needle) {
			out = append(out, it)
		}
	}
	return out, nil
}

// ExportReport renders and writes a report file under dir, returning
// the written path. All exports must go through report.Export (C1).
func (s *Service) ExportReport(dir, filename string) (string, error) {
	sum := report.Build("inventory export", s.St.List())
	var b strings.Builder
	b.WriteString(sum.Title)
	b.WriteString("\n")
	for _, line := range sum.Display {
		b.WriteString(line)
		b.WriteString("\n")
	}
	return report.Export(dir, filename, b.String())
}

// quietLogger returns a discarding logger for tests and tooling.
func quietLogger() *log.Logger { return log.New(io.Discard, "", 0) }
