// Package store keeps inventory items in an in-memory, concurrency-safe
// table. It is the single source of truth for inventoryd; report and api
// read through it and never cache raw maps.
package store

import (
	"errors"
	"sort"
	"sync"
)

// Item is one inventory record. Monetary amounts are int64 cents —
// never float (REVIEW-CRITERIA.md C4). OwnerEmail is internal contact
// metadata only; C3 forbids it appearing in logs, reports or exports.
type Item struct {
	ID         string
	Name       string
	PriceCents int64
	Stock      int
	OwnerEmail string
}

// ErrNotFound is returned by GetByID and Delete when the id is absent.
var ErrNotFound = errors.New("store: item not found")

// ErrInvalid is returned by Put when a record fails validation.
var ErrInvalid = errors.New("store: invalid item")

// BatchError reports a BatchDelete that stopped early. Done lists the
// ids removed before the failure; Failed is the id that stopped the
// batch; Err is the underlying error. Partial state is real: the ids in
// Done ARE deleted and must not be blindly retried.
type BatchError struct {
	Done   []string
	Failed string
	Err    error
}

func (e *BatchError) Error() string {
	return "store: batch delete stopped at " + e.Failed + ": " + e.Err.Error()
}

// Store is safe for concurrent use by multiple goroutines.
type Store struct {
	mu    sync.RWMutex
	items map[string]Item
}

// New returns an empty store.
func New() *Store { return &Store{items: make(map[string]Item)} }

// Put validates and inserts (or overwrites) an item. Valid items need a
// non-empty ID, a non-empty name and a non-negative price.
func (s *Store) Put(it Item) error {
	if it.ID == "" || it.Name == "" || it.PriceCents < 0 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[it.ID] = it
	return nil
}

// GetByID returns a copy of the item with the given id.
func (s *Store) GetByID(id string) (Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	it, ok := s.items[id]
	if !ok {
		return Item{}, ErrNotFound
	}
	return it, nil
}

// List returns all items ordered by ID (ascending). The ordering is
// part of the contract; insertion order is unspecified and must not be
// relied upon (README 行为契约).
func (s *Store) List() []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Item, 0, len(s.items))
	for _, it := range s.items {
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Delete removes an item.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return ErrNotFound
	}
	delete(s.items, id)
	return nil
}

// BatchDelete deletes ids in order and FAILS FAST: the first error stops
// the batch and is returned wrapped in *BatchError with the partial
// state. Callers needing best-effort semantics must loop Delete
// themselves (REVIEW-CRITERIA.md C2).
func (s *Store) BatchDelete(ids []string) error {
	var done []string
	for _, id := range ids {
		if err := s.Delete(id); err != nil {
			return &BatchError{Done: done, Failed: id, Err: err}
		}
		done = append(done, id)
	}
	return nil
}
