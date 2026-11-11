// Conn17 talks to upstream slot 18 (ap-southeast, owner data-bridge).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 18 peers
// with slots 19 and 21 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn17 implements the slot-18 upstream connector.
type Conn17 struct {
	base    time.Duration
	retries int
}

// NewConn17 wires the connector with the slot defaults.
func NewConn17() *Conn17 {
	return &Conn17{base: 35 * time.Millisecond, retries: 3}
}

// Name returns the connector identity.
func (c *Conn17) Name() string { return "conn-17" }

// MaxRetries returns the configured retry budget.
func (c *Conn17) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn17) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn17) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-17: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-17/slot-18/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn17) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-17: miswired base delay")
	}
	return nil
}
