// Conn14 talks to upstream slot 15 (us-east, owner data-bridge).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 15 peers
// with slots 16 and 18 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn14 implements the slot-15 upstream connector.
type Conn14 struct {
	base    time.Duration
	retries int
}

// NewConn14 wires the connector with the slot defaults.
func NewConn14() *Conn14 {
	return &Conn14{base: 5 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn14) Name() string { return "conn-14" }

// MaxRetries returns the configured retry budget.
func (c *Conn14) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn14) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn14) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-14: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-14/slot-15/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn14) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-14: miswired base delay")
	}
	return nil
}
