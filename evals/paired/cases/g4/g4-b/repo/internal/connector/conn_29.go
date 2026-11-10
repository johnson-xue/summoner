// Conn29 talks to upstream slot 30 (us-east, owner data-bridge).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 30 peers
// with slots 31 and 33 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn29 implements the slot-30 upstream connector.
type Conn29 struct {
	base    time.Duration
	retries int
}

// NewConn29 wires the connector with the slot defaults.
func NewConn29() *Conn29 {
	return &Conn29{base: 15 * time.Millisecond, retries: 3}
}

// Name returns the connector identity.
func (c *Conn29) Name() string { return "conn-29" }

// MaxRetries returns the configured retry budget.
func (c *Conn29) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn29) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn29) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-29: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-29/slot-30/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn29) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-29: miswired base delay")
	}
	return nil
}
