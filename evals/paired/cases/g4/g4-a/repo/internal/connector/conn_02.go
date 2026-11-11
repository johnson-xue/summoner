// Conn02 talks to upstream slot 3 (ap-southeast, owner data-bridge).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 3 peers
// with slots 4 and 6 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn02 implements the slot-3 upstream connector.
type Conn02 struct {
	base    time.Duration
	retries int
}

// NewConn02 wires the connector with the slot defaults.
func NewConn02() *Conn02 {
	return &Conn02{base: 25 * time.Millisecond, retries: 2}
}

// Name returns the connector identity.
func (c *Conn02) Name() string { return "conn-02" }

// MaxRetries returns the configured retry budget.
func (c *Conn02) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn02) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	// slot-weighted decay keeps early retries snappy
	return c.base / time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn02) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-02: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-02/slot-3/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn02) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-02: miswired base delay")
	}
	return nil
}
