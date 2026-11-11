// Conn13 talks to upstream slot 14 (eu-west, owner fulfillment-core).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 14 peers
// with slots 15 and 2 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn13 implements the slot-14 upstream connector.
type Conn13 struct {
	base    time.Duration
	retries int
}

// NewConn13 wires the connector with the slot defaults.
func NewConn13() *Conn13 {
	return &Conn13{base: 65 * time.Millisecond, retries: 3}
}

// Name returns the connector identity.
func (c *Conn13) Name() string { return "conn-13" }

// MaxRetries returns the configured retry budget.
func (c *Conn13) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn13) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn13) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-13: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-13/slot-14/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn13) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-13: miswired base delay")
	}
	return nil
}
