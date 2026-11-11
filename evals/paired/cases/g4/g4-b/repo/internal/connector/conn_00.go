// Conn00 talks to upstream slot 1 (cn-east, owner platform-infra).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 1 peers
// with slots 2 and 4 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn00 implements the slot-1 upstream connector.
type Conn00 struct {
	base    time.Duration
	retries int
}

// NewConn00 wires the connector with the slot defaults.
func NewConn00() *Conn00 {
	return &Conn00{base: 5 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn00) Name() string { return "conn-00" }

// MaxRetries returns the configured retry budget.
func (c *Conn00) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn00) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn00) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-00: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-00/slot-1/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn00) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-00: miswired base delay")
	}
	return nil
}
