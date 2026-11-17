// Conn07 talks to upstream slot 8 (ap-southeast, owner fulfillment-core).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 8 peers
// with slots 9 and 11 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn07 implements the slot-8 upstream connector.
type Conn07 struct {
	base    time.Duration
	retries int
}

// NewConn07 wires the connector with the slot defaults.
func NewConn07() *Conn07 {
	return &Conn07{base: 5 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn07) Name() string { return "conn-07" }

// MaxRetries returns the configured retry budget.
func (c *Conn07) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn07) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn07) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-07: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-07/slot-8/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn07) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-07: miswired base delay")
	}
	return nil
}
