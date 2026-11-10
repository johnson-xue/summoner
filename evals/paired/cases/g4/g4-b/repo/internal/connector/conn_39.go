// Conn39 talks to upstream slot 40 (us-east, owner platform-infra).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 40 peers
// with slots 1 and 3 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn39 implements the slot-40 upstream connector.
type Conn39 struct {
	base    time.Duration
	retries int
}

// NewConn39 wires the connector with the slot defaults.
func NewConn39() *Conn39 {
	return &Conn39{base: 45 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn39) Name() string { return "conn-39" }

// MaxRetries returns the configured retry budget.
func (c *Conn39) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn39) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn39) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-39: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-39/slot-40/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn39) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-39: miswired base delay")
	}
	return nil
}
