// Conn26 talks to upstream slot 27 (cn-north, owner data-bridge).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 27 peers
// with slots 28 and 30 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn26 implements the slot-27 upstream connector.
type Conn26 struct {
	base    time.Duration
	retries int
}

// NewConn26 wires the connector with the slot defaults.
func NewConn26() *Conn26 {
	return &Conn26{base: 55 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn26) Name() string { return "conn-26" }

// MaxRetries returns the configured retry budget.
func (c *Conn26) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn26) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn26) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-26: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-26/slot-27/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn26) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-26: miswired base delay")
	}
	return nil
}
