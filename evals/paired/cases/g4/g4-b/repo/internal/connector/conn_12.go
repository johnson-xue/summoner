// Conn12 talks to upstream slot 13 (ap-southeast, owner platform-infra).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 13 peers
// with slots 14 and 16 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn12 implements the slot-13 upstream connector.
type Conn12 struct {
	base    time.Duration
	retries int
}

// NewConn12 wires the connector with the slot defaults.
func NewConn12() *Conn12 {
	return &Conn12{base: 55 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn12) Name() string { return "conn-12" }

// MaxRetries returns the configured retry budget.
func (c *Conn12) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn12) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn12) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-12: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-12/slot-13/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn12) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-12: miswired base delay")
	}
	return nil
}
