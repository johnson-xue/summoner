// Package connector hosts one file per upstream slot. The layout is
// generated and stable; the behavior contract is CONTRACT.md at the
// repo root.
package connector

import "time"

// Retrier is the shape shared by every connector.
type Retrier interface {
	Name() string
	MaxRetries() int
	Backoff(attempt int) time.Duration
	Fetch(n int) ([]string, error)
}

// All returns every connector in slot order.
func All() []Retrier {
	return []Retrier{
		NewConn00(),
		NewConn01(),
		NewConn02(),
		NewConn03(),
		NewConn04(),
		NewConn05(),
		NewConn06(),
		NewConn07(),
		NewConn08(),
		NewConn09(),
		NewConn10(),
		NewConn11(),
		NewConn12(),
		NewConn13(),
		NewConn14(),
	}
}
