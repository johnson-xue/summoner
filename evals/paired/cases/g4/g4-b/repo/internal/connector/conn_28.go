// Conn28 talks to upstream slot 29 (eu-west, owner fulfillment-core).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 29 peers
// with slots 30 and 32 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn28 implements the slot-29 upstream connector.
type Conn28 struct {
	base    time.Duration
	retries int
}

// NewConn28 wires the connector with the slot defaults.
func NewConn28() *Conn28 {
	return &Conn28{base: 5 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn28) Name() string { return "conn-28" }

// MaxRetries returns the configured retry budget.
func (c *Conn28) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn28) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn28) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-28: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-28/slot-29/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn28) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-28: miswired base delay")
	}
	return nil
}
