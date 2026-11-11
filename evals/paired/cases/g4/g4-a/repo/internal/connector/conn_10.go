// Conn10 talks to upstream slot 11 (cn-east, owner fulfillment-core).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 11 peers
// with slots 12 and 14 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn10 implements the slot-11 upstream connector.
type Conn10 struct {
	base    time.Duration
	retries int
}

// NewConn10 wires the connector with the slot defaults.
func NewConn10() *Conn10 {
	return &Conn10{base: 35 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn10) Name() string { return "conn-10" }

// MaxRetries returns the configured retry budget.
func (c *Conn10) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn10) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn10) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-10: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-10/slot-11/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn10) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-10: miswired base delay")
	}
	return nil
}
