package shared

import "testing"

// checkpoint_auto_test.go — 0.7.0 adaptive checkpoints (D3): a checkpoint the
// framework passes without a user decision is recorded as `checkpoint.auto`.
// It is NEVER a forged `checkpoint.decided` (that event's tool_use_id must be
// a real AskUserQuestion call), so the type needs its own allow-list entry —
// at the writer (S0), and the reducer/gate sites land in S3.

// TestCheckpointAutoEventIsTraceable: the writer accepts `checkpoint.auto`
// (unknown types are rejected at the door) and the line round-trips through
// the strict chain verifier with its payload intact.
func TestCheckpointAutoEventIsTraceable(t *testing.T) {
	useTraceDir(t)
	const run = "run-auto-tr"

	err := AppendEvent(run, "sess-1", TraceEvent{
		Type:      "checkpoint.auto",
		Source:    "pretooluse-gate",
		ToolUseID: "tu-submit-1",
		Payload: map[string]interface{}{
			"checkpoint_id": "cp-1",
			"phase_id":      "context",
			"outcome":       "confirmed",
		},
	})
	if err != nil {
		t.Fatalf("AppendEvent(checkpoint.auto): %v", err)
	}
	if err := VerifyChain(run); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	evs := readTraceLines(t, run)
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1", len(evs))
	}
	if evs[0].Type != "checkpoint.auto" || evs[0].ToolUseID != "tu-submit-1" {
		t.Fatalf("event = type %q tool_use_id %q, want checkpoint.auto / tu-submit-1", evs[0].Type, evs[0].ToolUseID)
	}
	if got := evs[0].Payload["outcome"]; got != "confirmed" {
		t.Fatalf("payload.outcome = %v, want confirmed", got)
	}
}
