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
		&Conn00{},
		&Conn01{},
		&Conn02{},
		&Conn03{},
		&Conn04{},
		&Conn05{},
		&Conn06{},
		&Conn07{},
		&Conn08{},
		&Conn09{},
		&Conn10{},
		&Conn11{},
		&Conn12{},
		&Conn13{},
		&Conn14{},
	}
}
