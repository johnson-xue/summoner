// Conn22 talks to upstream slot 23 (ap-southeast, owner fulfillment-core).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 23 peers
// with slots 24 and 26 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn22 implements the slot-23 upstream connector.
type Conn22 struct {
	base    time.Duration
	retries int
}

// NewConn22 wires the connector with the slot defaults.
func NewConn22() *Conn22 {
	return &Conn22{base: 15 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn22) Name() string { return "conn-22" }

// MaxRetries returns the configured retry budget.
func (c *Conn22) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn22) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn22) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-22: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-22/slot-23/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn22) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-22: miswired base delay")
	}
	return nil
}
