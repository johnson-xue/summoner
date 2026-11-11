// Conn33 talks to upstream slot 34 (eu-west, owner platform-infra).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 34 peers
// with slots 35 and 37 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn33 implements the slot-34 upstream connector.
type Conn33 struct {
	base    time.Duration
	retries int
}

// NewConn33 wires the connector with the slot defaults.
func NewConn33() *Conn33 {
	return &Conn33{base: 55 * time.Millisecond, retries: 3}
}

// Name returns the connector identity.
func (c *Conn33) Name() string { return "conn-33" }

// MaxRetries returns the configured retry budget.
func (c *Conn33) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn33) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn33) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-33: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-33/slot-34/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn33) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-33: miswired base delay")
	}
	return nil
}
