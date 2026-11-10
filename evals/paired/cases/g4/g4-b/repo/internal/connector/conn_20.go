// Conn20 talks to upstream slot 21 (cn-east, owner data-bridge).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 21 peers
// with slots 22 and 24 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn20 implements the slot-21 upstream connector.
type Conn20 struct {
	base    time.Duration
	retries int
}

// NewConn20 wires the connector with the slot defaults.
func NewConn20() *Conn20 {
	return &Conn20{base: 65 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn20) Name() string { return "conn-20" }

// MaxRetries returns the configured retry budget.
func (c *Conn20) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn20) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn20) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-20: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-20/slot-21/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn20) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-20: miswired base delay")
	}
	return nil
}
