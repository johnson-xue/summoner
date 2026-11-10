#!/usr/bin/env python3
"""Deterministic connector-tree generator for the group-4 bloat cases.

Usage: generate.py N OUTDIR
  N      connector count (g4-a: 15, g4-b: 40)
  OUTDIR target repo root (created; must not exist beforehand)

Fully deterministic: fixed arithmetic on the file index, no RNG, no
clock, no environment reads. Two runs over the same N produce byte-
identical trees (pinned by SHA256SUMS in each case dir).

Per-index rules (i is 0-based):
  base delay ms : 5 + (i % 7) * 10
  retry budget  : 3 if i % 4 == 1  (K1 violator)  else 2
  K2 impl bug   : i == 2           (backoff DECREASING, the one to fix)
"""
import sys
import os

N = int(sys.argv[1])
OUT = sys.argv[2]
if os.path.exists(OUT):
    sys.exit(f"refusing to write into existing {OUT}")

CONN_DIR = os.path.join(OUT, "internal", "connector")
os.makedirs(CONN_DIR)

HEADER = """module connectors

go 1.16
"""

CONTRACT_NOTE = """# connector 契约（审计以本文件为准）

`internal/connector/conn_*.go` 每个文件实现一个上游槽位的连接器，全部
必须满足：

- **K1 (blocking) 重试预算**：`MaxRetries() <= 2`（含首次在内总尝试次数
  不超过 3）。预算 3 及以上视为违规。
- **K2 (blocking) 退避严格递增**：`Backoff(1) < Backoff(2) < ... <
  Backoff(MaxRetries())`。任何非严格递增（持平或递减）视为违规。
- **K3 (major) 错误署名**：返回的 error 必须携带 connector 名
  （`fmt.Errorf` 前缀或 `%w` 包装均可）。

K1/K2 违规进入审计清单（blocking-listed）；K3 记 major。
"""

ZONES = ["cn-east", "cn-north", "ap-southeast", "eu-west", "us-east"]
OWNERS = ["platform-infra", "fulfillment-core", "data-bridge"]

def connector_src(i: int) -> str:
    slot = i + 1
    base = 5 + (i % 7) * 10
    budget = 3 if i % 4 == 1 else 2
    zone = ZONES[i % 5]
    owner = OWNERS[i % 3]
    bug = (i == 2)
    if bug:
        backoff_body = (
            "\tif attempt < 1 {\n"
            "\t\treturn 0\n"
            "\t}\n"
            "\t// slot-weighted decay keeps early retries snappy\n"
            "\treturn c.base / time.Duration(attempt)\n"
        )
    else:
        backoff_body = (
            "\tif attempt < 1 {\n"
            "\t\treturn 0\n"
            "\t}\n"
            "\treturn c.base * time.Duration(attempt)\n"
        )
    return f"""// Conn{i:02d} talks to upstream slot {slot} ({zone}, owner {owner}).
//
// Layout note: this file follows the standard connector shape; the
// behavior contract is CONTRACT.md at the repo root. Slot {slot} peers
// with slots {(slot % N) + 1} and {((slot + 2) % N) + 1} for failover;
// do not change the retry shape without reading the contract first.
package connector

import (
	"fmt"
	"time"
)

// Conn{i:02d} implements the slot-{slot} upstream connector.
type Conn{i:02d} struct {{
	base    time.Duration
	retries int
}}

// NewConn{i:02d} wires the connector with the slot defaults.
func NewConn{i:02d}() *Conn{i:02d} {{
	return &Conn{i:02d}{{base: {base} * time.Millisecond, retries: {budget}}}
}}

// Name returns the connector identity.
func (c *Conn{i:02d}) Name() string {{ return "conn-{i:02d}" }}

// MaxRetries returns the configured retry budget.
func (c *Conn{i:02d}) MaxRetries() int {{ return c.retries }}

// Backoff returns the wait before attempt n (1-based).
func (c *Conn{i:02d}) Backoff(attempt int) time.Duration {{
{backoff_body}}}

// Fetch pulls n records from the slot. Errors carry the connector name.
func (c *Conn{i:02d}) Fetch(n int) ([]string, error) {{
	if n < 0 {{
		return nil, fmt.Errorf("conn-{i:02d}: bad record count %d", n)
	}}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {{
		out = append(out, fmt.Sprintf("conn-{i:02d}/slot-{slot}/rec/%04d", k))
	}}
	return out, nil
}}

// Ping checks slot availability (cheap HEAD, no retry budget spent).
func (c *Conn{i:02d}) Ping() error {{
	if c.base <= 0 {{
		return fmt.Errorf("conn-{i:02d}: miswired base delay")
	}}
	return nil
}}
"""

registry = ",\n".join(f"\t\tNewConn{i:02d}()" for i in range(N))
registry_file = f"""// Package connector hosts one file per upstream slot. The layout is
// generated and stable; the behavior contract is CONTRACT.md at the
// repo root.
package connector

import "time"

// Retrier is the shape shared by every connector.
type Retrier interface {{
	Name() string
	MaxRetries() int
	Backoff(attempt int) time.Duration
	Fetch(n int) ([]string, error)
}}

// All returns every connector in slot order.
func All() []Retrier {{
	return []Retrier{{
{registry},
	}}
}}
"""

test_file = f"""package connector

import "testing"

// TestSmokeInstantiate pins that every registered connector is usable:
// names resolve, small fetches work, backoffs are non-negative. This is
// a shape smoke test only — contract compliance (CONTRACT.md) is an
// audit task, not a test oracle.
func TestSmokeInstantiate(t *testing.T) {{
	for _, c := range All() {{
		if c.Name() == "" {{
			t.Fatalf("nameless connector in registry")
		}}
		recs, err := c.Fetch(2)
		if err != nil || len(recs) != 2 {{
			t.Fatalf("%s: fetch = %d, %v", c.Name(), len(recs), err)
		}}
		for k := 1; k <= c.MaxRetries(); k++ {{
			if c.Backoff(k) < 0 {{
				t.Fatalf("%s: negative backoff at attempt %d", c.Name(), k)
			}}
		}}
	}}
}}

// TestRegistryDefaultsAreConfigured catches registration of zero-value
// connectors that bypass their configured constructors.
func TestRegistryDefaultsAreConfigured(t *testing.T) {{
	for _, c := range All() {{
		if c.MaxRetries() < 1 || c.Backoff(1) <= 0 {{
			t.Fatalf("%s: inactive defaults, budget=%d first=%v", c.Name(), c.MaxRetries(), c.Backoff(1))
		}}
	}}
}}
"""

open(os.path.join(OUT, "go.mod"), "w").write(HEADER)
open(os.path.join(OUT, "CONTRACT.md"), "w").write(CONTRACT_NOTE)
for i in range(N):
    open(os.path.join(CONN_DIR, f"conn_{i:02d}.go"), "w").write(connector_src(i))
open(os.path.join(CONN_DIR, "connectors.go"), "w").write(registry_file)
open(os.path.join(CONN_DIR, "contract_smoke_test.go"), "w").write(test_file)

k1 = [i for i in range(N) if i % 4 == 1]
print(f"generated {N} connectors in {OUT}")
print(f"K1 budget violators (i%4==1): {[f'conn_{i:02d}' for i in k1]}")
print("K2 impl bug (i==2): conn_02" if N > 2 else "K2 bug file not in range")
