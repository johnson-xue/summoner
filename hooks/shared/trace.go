package shared

// trace.go — append-only JSONL trace writer/verifier (schema v2; spec §7
// 单一 trace schema). One self-contained file per run:
// $SUMMONER_TRACE_DIR (or ~/.summoner/traces-v2) / {project_key} / {run_id}
// .jsonl. Events are the durable record: AppendEvent never touches run-state,
// so a lost state file never blocks evidence capture.
//
// Every line is one JSON object: {type, seq, timestamp, source?, prompt_id?,
// tool_use_id?, payload?, prev_hash, hash}. `type` is allow-listed (unknown
// types are rejected at the door). The hash chain makes tampering detectable:
// prev_hash = sha256 of the previous line's canonical bytes (first event:
// sha256 of the empty string); hash = sha256 of this event's canonical bytes
// including prev_hash, excluding the hash field itself. The canonical form is
// pinned to encoding/json's struct-order + sorted-map-keys output — write and
// verify share this one method, so any byte-level edit anywhere breaks either
// that line's own hash or the next line's prev_hash.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// --- event types (allow-list) -------------------------------------------------

const (
	EventWorkflowStarted = "workflow.started"
	EventWorkflowEnded   = "workflow.ended"
	EventWorkflowAborted = "workflow.aborted"

	EventPhaseStarted           = "phase.started"
	EventPhaseClosed            = "phase.closed"
	EventPhaseSubmittedRejected = "phase_submitted.rejected"

	EventCheckpointOpened    = "checkpoint.opened"
	EventCheckpointDisplayed = "checkpoint.displayed"
	EventCheckpointDecided   = "checkpoint.decided"

	EventRecallApplied = "recall.applied"

	EventChangeOpened       = "change.opened"
	EventChangeDeltaApplied = "change.delta_applied"
	EventChangeResumed      = "change.resumed"

	EventToolStarted        = "tool.started"
	EventToolResult         = "tool.result"
	EventToolFailed         = "tool.failed"
	EventToolOutcomeUnknown = "tool.outcome_unknown"
	EventToolResolved       = "tool.resolved"

	EventCheckResult = "check.result"

	EventReviewPrepared   = "review.prepared"
	EventReviewStarted    = "review.started"
	EventReviewVerdict    = "review.verdict"
	EventReviewFailed     = "review.failed"
	EventReviewBlocked    = "review.blocked"
	EventReviewSuperseded = "review.superseded"

	EventDelegationStarted   = "delegation.started"
	EventDelegationCompleted = "delegation.completed"
	EventDelegationFailed    = "delegation.failed"

	EventParallelOpened = "parallel.opened"
	EventParallelJoined = "parallel.joined"

	EventAgentTelemetry = "agent.telemetry"

	EventPostGameStarted   = "post_game.started"
	EventPostGameCompleted = "post_game.completed"
)

// allEventTypes is the single allow-list every AppendEvent is checked against;
// the slice (not the map) fixes a stable order for tests and fixtures.
var allEventTypes = []string{
	EventWorkflowStarted, EventWorkflowEnded, EventWorkflowAborted,
	EventPhaseStarted, EventPhaseClosed, EventPhaseSubmittedRejected,
	EventCheckpointOpened, EventCheckpointDisplayed, EventCheckpointDecided,
	EventRecallApplied,
	EventChangeOpened, EventChangeDeltaApplied, EventChangeResumed,
	EventToolStarted, EventToolResult, EventToolFailed, EventToolOutcomeUnknown, EventToolResolved,
	EventCheckResult,
	EventReviewPrepared, EventReviewStarted, EventReviewVerdict, EventReviewFailed, EventReviewBlocked, EventReviewSuperseded,
	EventDelegationStarted, EventDelegationCompleted, EventDelegationFailed,
	EventParallelOpened, EventParallelJoined,
	EventAgentTelemetry,
	EventPostGameStarted, EventPostGameCompleted,
}

func eventAllowed(typ string) bool {
	for _, t := range allEventTypes {
		if t == typ {
			return true
		}
	}
	return false
}

// --- event shape ---------------------------------------------------------------

// TraceEvent is one trace line. Seq and the hash pair are assigned by
// AppendEvent (caller-supplied values are ignored); a caller-provided
// Timestamp is kept, an empty one is stamped with now UTC (RFC3339).
type TraceEvent struct {
	Type      string                 `json:"type"`
	Seq       int                    `json:"seq"`
	Timestamp string                 `json:"timestamp"`
	Source    string                 `json:"source,omitempty"`
	PromptID  string                 `json:"prompt_id,omitempty"`
	ToolUseID string                 `json:"tool_use_id,omitempty"`
	Payload   map[string]interface{} `json:"payload,omitempty"`
	PrevHash  string                 `json:"prev_hash"`
	Hash      string                 `json:"hash,omitempty"`
}

// canonicalBytes is THE pinned canonical serialization, used for both writing
// and hashing: encoding/json emits struct fields in declaration order and map
// keys sorted, so identical values always yield identical bytes.
func canonicalBytes(ev TraceEvent) ([]byte, error) {
	return json.Marshal(ev)
}

// hashHex is the chain's digest of arbitrary bytes.
func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// eventHash computes an event's `hash`: sha256 over its canonical bytes
// including prev_hash, with the (derived) hash field itself empty.
func eventHash(ev TraceEvent) (string, error) {
	ev.Hash = ""
	b, err := canonicalBytes(ev)
	if err != nil {
		return "", err
	}
	return hashHex(b), nil
}

// --- file resolution ------------------------------------------------------------

// traceDirOverride redirects v2 traces into a test directory (hermetic suite,
// same pattern as runStateDirOverride). Empty in production.
var traceDirOverride string

func traceRoot() (string, error) {
	if traceDirOverride != "" {
		return traceDirOverride, nil
	}
	if d := os.Getenv("SUMMONER_TRACE_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".summoner", "traces-v2"), nil
}

func traceFilePath(runID string) (string, error) {
	if !validID(runID) {
		return "", fmt.Errorf("trace: invalid run id %q", runID)
	}
	root, err := traceRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ProjectKey(), runID+".jsonl"), nil
}

// --- writing ---------------------------------------------------------------------

// AppendEvent appends one event to the run's trace file, assigning seq (last
// seq + 1, starting at 1), timestamp (if absent) and the hash pair. The write
// is a single O_APPEND|O_CREATE Write of the fully-built line, so a reader
// never observes a half-written event (single-process hooks assumption; no
// cross-process locking in v1). Appending onto an unparseable tail fails
// closed rather than chaining onto a broken line.
func AppendEvent(runID string, ev TraceEvent) error {
	p, err := traceFilePath(runID)
	if err != nil {
		return err
	}
	if !eventAllowed(ev.Type) {
		return fmt.Errorf("trace: unknown event type %q (not in allow-list)", ev.Type)
	}

	prevHash, lastSeq, err := traceTail(p)
	if err != nil {
		return err
	}
	ev.Seq = lastSeq + 1
	if ev.Timestamp == "" {
		ev.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	ev.PrevHash = prevHash
	if ev.Hash, err = eventHash(ev); err != nil {
		return fmt.Errorf("trace: hash event: %w", err)
	}

	line, err := canonicalBytes(ev)
	if err != nil {
		return fmt.Errorf("trace: serialize event: %w", err)
	}
	line = append(line, '\n')

	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(line); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// traceTail reads the file's last complete line and returns (prev_hash,
// last seq). A missing file yields the genesis pair: sha256(""), 0. The
// prev_hash commits to the previous line's canonical bytes, so the tail is
// re-canonicalized (never trusted raw) before hashing.
func traceTail(p string) (string, int, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return hashHex(nil), 0, nil
		}
		return "", 0, err
	}
	lines := traceLines(b)
	if len(lines) == 0 {
		return hashHex(nil), 0, nil
	}
	var last TraceEvent
	if err := json.Unmarshal(lines[len(lines)-1], &last); err != nil {
		return "", 0, fmt.Errorf("trace: last line of %s is unparseable — refusing to extend a broken chain: %w", p, err)
	}
	if last.Hash == "" {
		return "", 0, fmt.Errorf("trace: last line of %s carries no hash — refusing to extend a broken chain", p)
	}
	prev, err := canonicalBytes(last)
	if err != nil {
		return "", 0, err
	}
	return hashHex(prev), last.Seq, nil
}

func traceLines(b []byte) [][]byte {
	var lines [][]byte
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, []byte(line))
		}
	}
	return lines
}

// --- verification -----------------------------------------------------------------

// VerifyChain re-reads the run's trace and recomputes the chain: every line
// must parse, carry seq == its 1-based line number, hash to its stored value,
// and commit via prev_hash to the previous line's canonical bytes (line 1 to
// sha256("")). The first bad line is named in the returned error.
func VerifyChain(runID string) error {
	p, err := traceFilePath(runID)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("verify chain: no trace file for run %q (%s)", runID, p)
		}
		return err
	}
	lines := traceLines(b)
	prevCanonical := hashHex(nil) // sha256 of the empty string
	for i, raw := range lines {
		n := i + 1
		var ev TraceEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			return fmt.Errorf("verify chain: line %d is not valid JSON: %w", n, err)
		}
		if ev.Seq != n {
			return fmt.Errorf("verify chain: line %d has seq %d, want %d", n, ev.Seq, n)
		}
		want, err := eventHash(ev)
		if err != nil {
			return fmt.Errorf("verify chain: line %d: %w", n, err)
		}
		if ev.Hash != want {
			return fmt.Errorf("verify chain: line %d hash mismatch (stored %s, recomputed %s) — event tampered or malformed", n, ev.Hash, want)
		}
		if ev.PrevHash != prevCanonical {
			return fmt.Errorf("verify chain: line %d prev_hash does not match line %d's canonical bytes (stored %s, want %s)", n, n-1, ev.PrevHash, prevCanonical)
		}
		// The next line's prev_hash commits to THIS line's canonical bytes.
		cb, err := canonicalBytes(ev)
		if err != nil {
			return fmt.Errorf("verify chain: line %d: %w", n, err)
		}
		prevCanonical = hashHex(cb)
	}
	return nil
}

// CriticalControlEventsPresent checks the minimal mechanical completeness
// rule: a trace with any events must contain workflow.started AND one of
// workflow.ended / workflow.aborted / post_game.completed. An absent or empty
// file is no error yet (nothing has happened, or capture has not started).
func CriticalControlEventsPresent(runID string) error {
	p, err := traceFilePath(runID)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // zero events — no error yet
		}
		return err
	}
	lines := traceLines(b)
	if len(lines) == 0 {
		return nil
	}
	var hasStarted, hasEnded, hasAborted, hasPostGame bool
	for i, raw := range lines {
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return fmt.Errorf("critical events: line %d of run %s is not valid JSON: %w", i+1, runID, err)
		}
		switch probe.Type {
		case EventWorkflowStarted:
			hasStarted = true
		case EventWorkflowEnded:
			hasEnded = true
		case EventWorkflowAborted:
			hasAborted = true
		case EventPostGameCompleted:
			hasPostGame = true
		}
	}
	if !hasStarted {
		return fmt.Errorf("critical events: run %s trace has %d events but no %s", runID, len(lines), EventWorkflowStarted)
	}
	if !hasEnded && !hasAborted && !hasPostGame {
		return fmt.Errorf("critical events: run %s has %s but no terminal event (%s, %s or %s)",
			runID, EventWorkflowStarted, EventWorkflowEnded, EventWorkflowAborted, EventPostGameCompleted)
	}
	return nil
}
