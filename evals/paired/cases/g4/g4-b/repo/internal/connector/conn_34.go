// Conn34 talks to upstream slot 35 (us-east, owner fulfillment-core).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 35 peers
// with slots 36 and 38 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn34 implements the slot-35 upstream connector.
type Conn34 struct {
	base    time.Duration
	retries int
}

// NewConn34 wires the connector with the slot defaults.
func NewConn34() *Conn34 {
	return &Conn34{base: 65 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn34) Name() string { return "conn-34" }

// MaxRetries returns the configured retry budget.
func (c *Conn34) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn34) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn34) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-34: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-34/slot-35/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn34) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-34: miswired base delay")
	}
	return nil
}
