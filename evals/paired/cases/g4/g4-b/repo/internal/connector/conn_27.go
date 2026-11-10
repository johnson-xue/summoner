// Conn27 talks to upstream slot 28 (ap-southeast, owner platform-infra).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 28 peers
// with slots 29 and 31 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn27 implements the slot-28 upstream connector.
type Conn27 struct {
	base    time.Duration
	retries int
}

// NewConn27 wires the connector with the slot defaults.
func NewConn27() *Conn27 {
	return &Conn27{base: 65 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn27) Name() string { return "conn-27" }

// MaxRetries returns the configured retry budget.
func (c *Conn27) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn27) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn27) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-27: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-27/slot-28/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn27) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-27: miswired base delay")
	}
	return nil
}
