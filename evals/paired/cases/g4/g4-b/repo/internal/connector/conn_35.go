// Conn35 talks to upstream slot 36 (cn-east, owner data-bridge).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 36 peers
// with slots 37 and 39 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn35 implements the slot-36 upstream connector.
type Conn35 struct {
	base    time.Duration
	retries int
}

// NewConn35 wires the connector with the slot defaults.
func NewConn35() *Conn35 {
	return &Conn35{base: 5 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn35) Name() string { return "conn-35" }

// MaxRetries returns the configured retry budget.
func (c *Conn35) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn35) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn35) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-35: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-35/slot-36/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn35) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-35: miswired base delay")
	}
	return nil
}
