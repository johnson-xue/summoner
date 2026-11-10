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
		&Conn15{},
		&Conn16{},
		&Conn17{},
		&Conn18{},
		&Conn19{},
		&Conn20{},
		&Conn21{},
		&Conn22{},
		&Conn23{},
		&Conn24{},
		&Conn25{},
		&Conn26{},
		&Conn27{},
		&Conn28{},
		&Conn29{},
		&Conn30{},
		&Conn31{},
		&Conn32{},
		&Conn33{},
		&Conn34{},
		&Conn35{},
		&Conn36{},
		&Conn37{},
		&Conn38{},
		&Conn39{},
	}
}
