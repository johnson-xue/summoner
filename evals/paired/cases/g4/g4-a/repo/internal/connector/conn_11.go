// Conn11 talks to upstream slot 12 (cn-north, owner data-bridge).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 12 peers
// with slots 13 and 15 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn11 implements the slot-12 upstream connector.
type Conn11 struct {
	base    time.Duration
	retries int
}

// NewConn11 wires the connector with the slot defaults.
func NewConn11() *Conn11 {
	return &Conn11{base: 45 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn11) Name() string { return "conn-11" }

// MaxRetries returns the configured retry budget.
func (c *Conn11) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn11) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn11) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-11: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-11/slot-12/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn11) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-11: miswired base delay")
	}
	return nil
}
