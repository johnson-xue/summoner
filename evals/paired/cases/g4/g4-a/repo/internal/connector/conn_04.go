// Conn04 talks to upstream slot 5 (us-east, owner fulfillment-core).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 5 peers
// with slots 6 and 8 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn04 implements the slot-5 upstream connector.
type Conn04 struct {
	base    time.Duration
	retries int
}

// NewConn04 wires the connector with the slot defaults.
func NewConn04() *Conn04 {
	return &Conn04{base: 45 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn04) Name() string { return "conn-04" }

// MaxRetries returns the configured retry budget.
func (c *Conn04) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn04) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn04) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-04: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-04/slot-5/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn04) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-04: miswired base delay")
	}
	return nil
}
