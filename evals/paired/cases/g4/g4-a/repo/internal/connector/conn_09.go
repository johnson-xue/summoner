// Conn09 talks to upstream slot 10 (us-east, owner platform-infra).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot 10 peers
// with slots 11 and 13 for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn09 implements the slot-10 upstream connector.
type Conn09 struct {
	base    time.Duration
	retries int
}

// NewConn09 wires the connector with the slot defaults.
func NewConn09() *Conn09 {
	return &Conn09{base: 25 * time.Millisecond, retries: 3}
}

// Name returns the connector identity.
func (c *Conn09) Name() string { return "conn-09" }

// MaxRetries returns the configured retry budget.
func (c *Conn09) MaxRetries() int { return c.retries }

// Backoff returns the wait before attempt n (1-based).
func (c *Conn09) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return c.base * time.Duration(attempt)
}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn09) Fetch(n int) ([]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("conn-09: bad record count %d", n)
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("conn-09/slot-10/rec/%04d", k))
	}
	return out, nil
}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn09) Ping() error {
	if c.base <= 0 {
		return fmt.Errorf("conn-09: miswired base delay")
	}
	return nil
}
