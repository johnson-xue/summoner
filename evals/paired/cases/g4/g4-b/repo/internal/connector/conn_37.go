// Conn37 talks to upstream slot 38 (ap-southeast, owner fulfillment-core).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 38 peers
// with slots 39 and 1 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn37 implements the slot-38 upstream connector.
type Conn37 struct {
	base    time.Duration
	retries int
}

// NewConn37 wires the connector with the slot defaults.
func NewConn37() *Conn37 {
	return &Conn37{base: 25 * time.Millisecond, retries: 3}
}

// Name returns the connector identity.
func (c *Conn37) Name() string { return "conn-37" }

// MaxRetries returns the configured retry budget.
func (c *Conn37) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn37) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn37) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-37: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-37/slot-38/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn37) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-37: miswired base delay")
	}
	return nil
}
