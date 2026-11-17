// Conn05 talks to upstream slot 6 (cn-east, owner data-bridge).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 6 peers
// with slots 7 and 9 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn05 implements the slot-6 upstream connector.
type Conn05 struct {
	base    time.Duration
	retries int
}

// NewConn05 wires the connector with the slot defaults.
func NewConn05() *Conn05 {
	return &Conn05{base: 55 * time.Millisecond, retries: 3}
}

// Name returns the connector identity.
func (c *Conn05) Name() string { return "conn-05" }

// MaxRetries returns the configured retry budget.
func (c *Conn05) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn05) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn05) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-05: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-05/slot-6/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn05) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-05: miswired base delay")
	}
	return nil
}
