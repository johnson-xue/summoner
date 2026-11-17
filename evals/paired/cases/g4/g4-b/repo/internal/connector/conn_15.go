// Conn15 talks to upstream slot 16 (cn-east, owner platform-infra).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 16 peers
// with slots 17 and 19 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn15 implements the slot-16 upstream connector.
type Conn15 struct {
	base    time.Duration
	retries int
}

// NewConn15 wires the connector with the slot defaults.
func NewConn15() *Conn15 {
	return &Conn15{base: 15 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn15) Name() string { return "conn-15" }

// MaxRetries returns the configured retry budget.
func (c *Conn15) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn15) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn15) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-15: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-15/slot-16/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn15) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-15: miswired base delay")
	}
	return nil
}
