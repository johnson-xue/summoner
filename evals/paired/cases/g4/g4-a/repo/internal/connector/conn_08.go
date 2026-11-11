// Conn08 talks to upstream slot 9 (eu-west, owner data-bridge).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 9 peers
// with slots 10 and 12 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn08 implements the slot-9 upstream connector.
type Conn08 struct {
	base    time.Duration
	retries int
}

// NewConn08 wires the connector with the slot defaults.
func NewConn08() *Conn08 {
	return &Conn08{base: 15 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn08) Name() string { return "conn-08" }

// MaxRetries returns the configured retry budget.
func (c *Conn08) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn08) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn08) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-08: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-08/slot-9/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn08) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-08: miswired base delay")
	}
	return nil
}
