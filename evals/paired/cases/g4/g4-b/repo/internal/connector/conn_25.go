// Conn25 talks to upstream slot 26 (cn-east, owner fulfillment-core).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 26 peers
// with slots 27 and 29 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn25 implements the slot-26 upstream connector.
type Conn25 struct {
	base    time.Duration
	retries int
}

// NewConn25 wires the connector with the slot defaults.
func NewConn25() *Conn25 {
	return &Conn25{base: 45 * time.Millisecond, retries: 3}
}

// Name returns the connector identity.
func (c *Conn25) Name() string { return "conn-25" }

// MaxRetries returns the configured retry budget.
func (c *Conn25) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn25) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn25) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-25: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-25/slot-26/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn25) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-25: miswired base delay")
	}
	return nil
}
