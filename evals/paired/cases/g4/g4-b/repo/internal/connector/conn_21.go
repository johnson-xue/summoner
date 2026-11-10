// Conn21 talks to upstream slot 22 (cn-north, owner platform-infra).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 22 peers
// with slots 23 and 25 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn21 implements the slot-22 upstream connector.
type Conn21 struct {
	base    time.Duration
	retries int
}

// NewConn21 wires the connector with the slot defaults.
func NewConn21() *Conn21 {
	return &Conn21{base: 5 * time.Millisecond, retries: 3}
}

// Name returns the connector identity.
func (c *Conn21) Name() string { return "conn-21" }

// MaxRetries returns the configured retry budget.
func (c *Conn21) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn21) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn21) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-21: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-21/slot-22/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn21) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-21: miswired base delay")
	}
	return nil
}
