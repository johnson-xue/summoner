package shared

import "os"

// Hook enforcement is OPT-IN (0.7.1).
//
// Incident 2026-11-24. With enforcement on by default, a session was gated
// for as long as any of its runs stayed non-CLOSED — in practice
// indefinitely: six of the seven runs on the reporting machine never
// terminated and one stayed live for 27 hours. For all that time the gate
// denied the session's everyday tools (EnterWorktree, SendMessage,
// TaskCreate, TaskStop, MCP lookups, Bash during a diagnosis) and denied
// fail-closed for every tool it had no model of and whenever the run state
// was unreadable. A hook that runs on EVERY tool call of EVERY session
// cannot be fail-closed by default: one gap in its model is the whole
// session.
//
// So the hooks are DORMANT until the project opts in:
//
//	SUMMONER_HOOKS_ENFORCE=1   enforce: runs, checkpoints, gates, traces
//	SUMMONER_HOOKS_DISABLE=1   the un-brick hatch — outranks the opt-in
//
// With neither set (the default) every hook returns before it reads stdin:
// nothing is parsed, recorded, rendered, appended or denied.

const (
	// EnvHooksEnforce arms the hooks. Only the exact value "1" arms them.
	EnvHooksEnforce = "SUMMONER_HOOKS_ENFORCE"
	// EnvHooksDisable is the un-brick hatch (spec §5). Only the exact value
	// "1" disarms them; it outranks EnvHooksEnforce.
	EnvHooksDisable = "SUMMONER_HOOKS_DISABLE"
)

// HooksDormant reports whether the hooks must do nothing at all because
// enforcement was not opted into.
//
// It is false when the hatch is set: the hatch is not dormancy but its own
// contract (a live run records hooks.disabled), which each hook handles
// after it has parsed its input.
func HooksDormant() bool {
	if os.Getenv(EnvHooksDisable) == "1" {
		return false
	}
	return os.Getenv(EnvHooksEnforce) != "1"
}
