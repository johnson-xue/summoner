// Package contract hosts the install-contract test suite (contract_test.go —
// the REAL hook binaries driven as subprocesses) plus the sandbox tier
// detection helper this task ships for install-time consumers.
package contract

// detect.go — sandbox tier detection (plan: 沙箱档位检测探针 — 检测到未生效 →
// BLOCKED 而非静默跑). Detection is split in two: a PURE classifier over
// (sandboxConfigured, probe) — fully table-tested — and an injectable probe
// that reports whether the live environment actually behaves sandboxed.
// Real probing is install-time work (later tasks); hermetic tests inject the
// probe result, never the environment.
//
// Verified probe signals (docs verified live 2026-11-10):
//   - `touch ~/sandbox-probe` fails with "Operation not permitted" (macOS) /
//     "Read-only file system" (Linux/WSL2): the home filesystem is not
//     writable → sandboxed;
//   - `curl --noproxy '*' https://example.com` fails with "Could not resolve
//     host": no network egress → sandboxed.

import (
	"os"
	"path/filepath"
)

// Sandbox tier values.
const (
	// TierL2 — the sandbox is in effect (probe confirms).
	TierL2 = "L2"
	// TierL1 — honestly unsandboxed: probe inactive AND no sandbox
	// configuration claimed.
	TierL1 = "L1"
	// TierBlocked — sandbox configuration exists but the probe says it is
	// NOT in effect. NEVER silently degrade to running unsandboxed: the
	// session is marked BLOCKED instead.
	TierBlocked = "BLOCKED"
)

// DetectSandboxTier classifies one install from the configuration claim and
// the probe result:
//
//	probe active            → "L2"      (sandboxed, whatever the config says)
//	probe inactive, no cfg  → "L1"      (honestly unsandboxed)
//	probe inactive, cfg     → "BLOCKED" (configured but not effective)
func DetectSandboxTier(sandboxConfigured, probeSaysSandboxed bool) string {
	if probeSaysSandboxed {
		return TierL2
	}
	if sandboxConfigured {
		return TierBlocked
	}
	return TierL1
}

// SandboxProbe reports whether the live environment behaves sandboxed.
// Injectable: tests and install-time code substitute a canned result; the
// default implements the documented touch signal (cheap, dependency-free).
var SandboxProbe = defaultSandboxProbe

// defaultSandboxProbe attempts the documented home-filesystem write: a
// refused write means the sandbox is in effect. An unresolvable home dir
// reports unsandboxed — an honest "probe inactive", leaving the tier to the
// configuration claim.
func defaultSandboxProbe() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	probe := filepath.Join(home, ".summoner-sandbox-probe")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		return true // write refused → sandbox in effect
	}
	os.Remove(probe)
	return false
}

// ClassifySandbox composes the injectable probe with the pure classifier —
// the one-call form install-time code uses.
func ClassifySandbox(sandboxConfigured bool) string {
	return DetectSandboxTier(sandboxConfigured, SandboxProbe())
}
