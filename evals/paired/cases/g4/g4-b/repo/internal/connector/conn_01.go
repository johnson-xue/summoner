// Conn01 talks to upstream slot 2 (cn-north, owner fulfillment-core).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 2 peers
// with slots 3 and 5 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn01 implements the slot-2 upstream connector.
type Conn01 struct {
	base    time.Duration
	retries int
}

// NewConn01 wires the connector with the slot defaults.
func NewConn01() *Conn01 {
	return &Conn01{base: 15 * time.Millisecond, retries: 3}
}

// Name returns the connector identity.
func (c *Conn01) Name() string { return "conn-01" }

// MaxRetries returns the configured retry budget.
func (c *Conn01) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn01) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn01) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-01: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-01/slot-2/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn01) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-01: miswired base delay")
	}
	return nil
}
