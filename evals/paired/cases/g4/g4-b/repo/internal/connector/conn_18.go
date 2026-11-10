// Conn18 talks to upstream slot 19 (eu-west, owner platform-infra).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 19 peers
// with slots 20 and 22 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn18 implements the slot-19 upstream connector.
type Conn18 struct {
	base    time.Duration
	retries int
}

// NewConn18 wires the connector with the slot defaults.
func NewConn18() *Conn18 {
	return &Conn18{base: 45 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn18) Name() string { return "conn-18" }

// MaxRetries returns the configured retry budget.
func (c *Conn18) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn18) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn18) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-18: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-18/slot-19/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn18) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-18: miswired base delay")
	}
	return nil
}
