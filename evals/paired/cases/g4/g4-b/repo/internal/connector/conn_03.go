// Conn03 talks to upstream slot 4 (eu-west, owner platform-infra).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 4 peers
// with slots 5 and 7 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn03 implements the slot-4 upstream connector.
type Conn03 struct {
	base    time.Duration
	retries int
}

// NewConn03 wires the connector with the slot defaults.
func NewConn03() *Conn03 {
	return &Conn03{base: 35 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn03) Name() string { return "conn-03" }

// MaxRetries returns the configured retry budget.
func (c *Conn03) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn03) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn03) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-03: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-03/slot-4/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn03) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-03: miswired base delay")
	}
	return nil
}
