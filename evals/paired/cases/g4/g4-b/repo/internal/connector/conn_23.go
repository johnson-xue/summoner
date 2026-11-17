// Conn23 talks to upstream slot 24 (eu-west, owner data-bridge).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 24 peers
// with slots 25 and 27 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn23 implements the slot-24 upstream connector.
type Conn23 struct {
	base    time.Duration
	retries int
}

// NewConn23 wires the connector with the slot defaults.
func NewConn23() *Conn23 {
	return &Conn23{base: 25 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn23) Name() string { return "conn-23" }

// MaxRetries returns the configured retry budget.
func (c *Conn23) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn23) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn23) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-23: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-23/slot-24/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn23) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-23: miswired base delay")
	}
	return nil
}
