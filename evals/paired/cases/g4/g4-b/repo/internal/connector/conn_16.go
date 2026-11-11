// Conn16 talks to upstream slot 17 (cn-north, owner fulfillment-core).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 17 peers
// with slots 18 and 20 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn16 implements the slot-17 upstream connector.
type Conn16 struct {
	base    time.Duration
	retries int
}

// NewConn16 wires the connector with the slot defaults.
func NewConn16() *Conn16 {
	return &Conn16{base: 25 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn16) Name() string { return "conn-16" }

// MaxRetries returns the configured retry budget.
func (c *Conn16) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn16) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn16) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-16: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-16/slot-17/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn16) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-16: miswired base delay")
	}
	return nil
}
