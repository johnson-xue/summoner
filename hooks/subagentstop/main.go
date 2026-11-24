// subagentstop — the SubagentStop hook of the single-loop rewrite (spec
// docs/superpowers/specs/2026-11-09-single-loop-checkpoint-design.md §3 崩溃恢复
// + §5 SubagentStop row: 子 transcript 路径登记、⑤ 结果补记; 不路由).
//
// A REGISTRAR/BACKFILLER, never a gate: the platform grants SubagentStop
// block-decision powers, but this binary NEVER blocks — a subagent's exit must
// not be bricked by reconciliation. Exit 0 always; the single fail-closed path
// is unparseable stdin (exit 2).
//
// Verified platform fact: internal agents (prompt suggestions, /btw, …) also
// fire SubagentStop with their own agent_type — only {reviewer, worker} are
// Summoner delegations; every other agent_type is ignored entirely.
//
// For a reviewer/worker stop against the session's one live run:
//   - the agent transcript path is registered as evidence
//     (delegation.completed {agent_id, agent_type, transcript_path} — spec §7:
//     大证据本机存、trace 记路径);
//   - a REVIEWER stop whose run has an in-flight review (RUNNING, or PREPARED
//     — the §3 崩溃恢复 crash window) backfills the structured result: the
//     transcript's SubagentHandback tool_use input.message carrying a JSON
//     object with the ReviewResult fields is replayed through
//     RecordReviewResult, which validates everything (enums, review identity,
//     conclusion state) and refuses LATE duplicates — a result for an already
//     concluded review never affects a new generation (按调用 id 去重). A
//     refused or failed backfill is a stderr note, never an error for the
//     subagent's exit.
//
// The un-brick path is SUMMONER_HOOKS_DISABLE == "1" exactly: enforce nothing,
// exit 0 — but using it while exactly one run is live appends one
// hooks.disabled event first (a recorded contract violation).
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

// subagentStopSource stamps every trace event this binary appends.
const subagentStopSource = "subagentstop"

// Platform event name (ruling 2). Empty is tolerated for lenient payloads.
const eventSubagentStop = "SubagentStop"

// delegatedAgentTypes is the delegation allow-list (mirrors the gate's ruling:
// reviewer|worker only, after NormalizeAgentType strips the framework's own
// plugin prefix — production stops arrive as summoner:reviewer /
// summoner:worker). Everything else — internal platform agents included —
// is not a Summoner delegation and produces nothing.
var delegatedAgentTypes = map[string]bool{"reviewer": true, "worker": true}

// subagentStopInput is the parsed SubagentStop payload: exactly the typed
// fields the hook needs (ruling 2). last_assistant_message is carried but not
// trusted: the report of record lives in the transcript's SubagentHandback.
type subagentStopInput struct {
	EventName            string
	SessionID            string
	StopHookActive       bool
	AgentID              string
	AgentType            string
	TranscriptPath       string
	LastAssistantMessage string
}

// parseInput decodes one SubagentStop payload. Anything that is not a single
// JSON object is an error — the caller fails closed (exit 2), the one
// fail-closed path of this hook. Trailing garbage after the object is refused.
func parseInput(r io.Reader) (*subagentStopInput, error) {
	var raw struct {
		HookEventName        string `json:"hook_event_name"`
		SessionID            string `json:"session_id"`
		StopHookActive       bool   `json:"stop_hook_active"`
		AgentID              string `json:"agent_id"`
		AgentType            string `json:"agent_type"`
		AgentTranscriptPath  string `json:"agent_transcript_path"`
		LastAssistantMessage string `json:"last_assistant_message"`
	}
	dec := json.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("stdin is not a hook JSON object: %v", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing data after the JSON object")
	}
	return &subagentStopInput{
		EventName:            raw.HookEventName,
		SessionID:            raw.SessionID,
		StopHookActive:       raw.StopHookActive,
		AgentID:              raw.AgentID,
		AgentType:            raw.AgentType,
		TranscriptPath:       raw.AgentTranscriptPath,
		LastAssistantMessage: raw.LastAssistantMessage,
	}, nil
}

func main() {
	os.Exit(runSubagentStop(os.Stdin, os.Stderr))
}

// runSubagentStop is the whole hook. Returns the process exit code: 0 for
// every outcome, 2 only for unparseable input.
func runSubagentStop(stdin io.Reader, errW io.Writer) int {
	// Opt-in default: a dormant hook settles no delegation. Checked before
	// the input is read — see shared.HooksDormant.
	if shared.HooksDormant() {
		return 0
	}
	in, err := parseInput(stdin)
	if err != nil {
		fmt.Fprintf(errW, "summoner subagentstop: unreadable hook input — fail-closed: %v\n", err)
		return 2
	}
	if in.EventName != "" && in.EventName != eventSubagentStop {
		fmt.Fprintf(errW, "summoner subagentstop: unknown hook_event_name %q (this binary serves SubagentStop) — ignoring\n", in.EventName)
		return 0
	}

	// SUMMONER_HOOKS_DISABLE — the un-brick hatch (spec §5), armed by the
	// exact value "1" only, same contract as the gate's and posttooluse's:
	// enforce nothing, but using it while exactly one run is live is a
	// recorded contract violation. A state-read error here does not re-brick
	// the session: the hatch outranks state.
	if os.Getenv("SUMMONER_HOOKS_DISABLE") == "1" {
		if runs, err := shared.SessionRuns(in.SessionID); err == nil {
			if live := liveRuns(runs); len(live) == 1 {
				if err := shared.AppendEvent(live[0].RunID, in.SessionID, shared.TraceEvent{
					Type:   shared.EventHooksDisabled,
					Source: subagentStopSource,
					Payload: map[string]interface{}{
						"agent_id":   in.AgentID,
						"agent_type": in.AgentType,
					},
				}); err != nil {
					fmt.Fprintf(errW, "summoner subagentstop: hooks.disabled trace append failed: %v\n", err)
				}
			}
		}
		return 0
	}

	// Delegation filter (verified platform fact): internal agents fire this
	// event too — only reviewer/worker are Summoner delegations. Fix-round C1:
	// plugin agents report namespaced types (`summoner:reviewer` /
	// `summoner:worker` in production), so the SAME normalization the gate
	// applies runs here — a namespaced Summoner stop is processed, not
	// silently dropped; everything else is still ignored.
	agentType := shared.NormalizeAgentType(in.AgentType)
	if !delegatedAgentTypes[agentType] {
		return 0
	}

	// Run resolution — warn, never fail: a registrar must not brick the
	// subagent's exit (same asymmetry as the Stop hook's).
	runs, err := shared.SessionRuns(in.SessionID)
	if err != nil {
		fmt.Fprintf(errW, "summoner subagentstop: session run-state unreadable — registering nothing: %v\n", err)
		return 0
	}
	live := liveRuns(runs)
	switch {
	case len(live) == 0:
		return 0 // no live run: not our business — no events
	case len(live) > 1:
		ids := make([]string, 0, len(live))
		for _, r := range live {
			ids = append(ids, r.RunID)
		}
		fmt.Fprintf(errW, "summoner subagentstop: %d live Summoner runs in this session (%s) — ambiguous; refusing to guess which run to register against\n",
			len(live), strings.Join(ids, ", "))
		return 0
	}
	run := live[0]

	if in.TranscriptPath == "" {
		fmt.Fprintf(errW, "summoner subagentstop: %s stop carried no agent_transcript_path — nothing to register\n", agentType)
		return 0
	}

	// Registration FIRST (evidence before mutation, mirroring the gate's
	// intent-before-effect ordering): if the evidence channel is broken, do
	// not conclude a review whose record would be unverifiable — the review
	// stays in flight for a later re-fire or T7 reconciliation.
	if err := shared.AppendEvent(run.RunID, in.SessionID, shared.TraceEvent{
		Type:   shared.EventDelegationCompleted,
		Source: subagentStopSource,
		Payload: map[string]interface{}{
			"agent_id":        in.AgentID,
			"agent_type":      in.AgentType,
			"transcript_path": in.TranscriptPath,
		},
	}); err != nil {
		fmt.Fprintf(errW, "summoner subagentstop: delegation.completed trace append failed — skipping review backfill (review stays in flight): %v\n", err)
		return 0
	}

	if agentType == "reviewer" {
		backfillReview(run, in, errW)
	}
	return 0
}

// backfillReview replays the reviewer's structured result from its transcript
// through RecordReviewResult (spec §3 崩溃恢复: 能从子 transcript 找到有效结果
// 就补记). Only a REVIEWER's transcript may conclude a review; the run must
// have one in flight (RUNNING, or PREPARED — the crash window). Absent or
// unparseable results register the transcript only; a refused replay (unknown
// review, illegal status, LATE duplicate of a concluded review) is a stderr
// note — never an error for the subagent's exit.
func backfillReview(run *shared.RunState, in *subagentStopInput, errW io.Writer) {
	rv := inFlightReview(run)
	if rv == nil {
		return // nothing to backfill: registration only
	}
	res, ok := parseReviewResult(in.TranscriptPath)
	if !ok {
		return // absent/unparseable: registration only
	}
	// F2 ownership classification FIRST (adjudication §3 "After user
	// retry/rotation, A is archival only"): match the transcript's initial
	// record against EVERY admitted attempt's capsule — the platform-origin
	// owner, whichever generation it is. A late RETIRED attempt's stop is
	// ARCHIVAL: its receipt gets the observed transcript, and the stop leaves —
	// the result is never replayed, B stays in flight, the review is neither
	// concluded nor failed by its predecessor's ghost.
	owner := shared.IdentifyChildAttempt(run, rv, in.TranscriptPath)
	if owner != "" && owner != rv.ActiveAttemptID {
		if att := rv.ActiveAttempt(owner); att != nil && att.ChildTranscriptPath == "" {
			att.ChildTranscriptPath = in.TranscriptPath
			if err := run.Save(); err != nil {
				fmt.Fprintf(errW, "summoner subagentstop: archival receipt Save failed (transcript stays trace-registered only): %v\n", err)
				return
			}
		}
		fmt.Fprintf(errW, "summoner subagentstop: retired attempt %s stopped late (after retry/rotation) — archival: its receipt carries the transcript, the result is never replayed; active attempt %s stays in flight\n", owner, rv.ActiveAttemptID)
		return
	}
	// Recovery round C3 (§3 line 35: attempt receipts carry the "observed
	// child identity/transcript"): stamp the platform-registered transcript
	// path on the ACTIVE attempt's receipt — first observation wins
	// (append-only receipt), and only once the capsule match PROVED the
	// transcript is the active attempt's own (owner == "" leaves the receipt
	// unstamped — an unproven correlation must not become a false evidence
	// join when the quarantine below persists). In-memory only here; the
	// conclusion commit below persists it, and a refusal path discards it
	// with the rest of the untouched state.
	if owner == rv.ActiveAttemptID {
		if att := rv.ActiveAttempt(rv.ActiveAttemptID); att != nil && att.ChildTranscriptPath == "" {
			att.ChildTranscriptPath = in.TranscriptPath
		}
	}
	// QA-1 ownership bind (final-fix wave, adjudication §3): the child must
	// OWN its launch before its transcript can conclude or fail this review —
	// the transcript's INITIAL record must be the EXACT admitted capsule of
	// the active attempt (one admitted parent tool_use, append-only receipt).
	// Checked BEFORE evidence reconciliation and verdict acceptance; an
	// unproven correlation quarantines through the same FAILED + review-result
	// routing (the run stays decidable), and ownership is never inferred from
	// echoed tokens, ordering or agent-type guesses. A result naming a
	// DIFFERENT review is a late duplicate of an old generation — its refusal
	// stays RecordReviewResult's, so the gate keys on the in-flight id like
	// the reconciliation below.
	if res.ReviewID == rv.ReviewID {
		if problem := shared.VerifyChildOwnership(run, rv, in.TranscriptPath); problem != nil {
			routeReviewProtocolError(run, in, rv.ReviewID, problem, errW)
			return
		}
	}
	// T17 evidence reconciliation (spec §3 证据对账) runs BEFORE the verdict is
	// accepted — and only for a result naming the IN-FLIGHT review (a late
	// duplicate's ids are the old generation's business; its refusal stays
	// RecordReviewResult's) and only when evidence ids are claimed (BLOCKED
	// reports a reason code, not a quality conclusion — nothing to join). A
	// forged id routes through RecordReviewProtocolError: FAILED + a
	// review-result checkpoint the user can still decide, persisted.
	if res.ReviewID == rv.ReviewID && len(res.EvidenceToolCallIDs) > 0 {
		if problem := shared.ReconcileEvidence(res.EvidenceToolCallIDs, in.TranscriptPath); problem != nil {
			routeReviewProtocolError(run, in, res.ReviewID, problem, errW)
			return
		}
	}
	// Fix-round m1: fire review.failed only when THIS call routed the failure.
	// RecordReviewResult refuses a late duplicate of an already-concluded
	// review by returning an error, and a review concluded FAILED by an
	// EARLIER call still reads Status==FAILED after the refusal — keying on
	// the status alone would trace a second failure that never happened.
	wasFailed := reviewStatus(run, res.ReviewID) == shared.ReviewFailed
	if err := run.RecordReviewResult(res); err != nil {
		fmt.Fprintf(errW, "summoner subagentstop: review backfill for %s refused (state untouched or routed per reducer) — late or invalid results never affect a new generation: %v\n",
			res.ReviewID, err)
		// RecordReviewResult's own protocol-error paths (enum violations, the
		// rubber-stamp rule) land the review FAILED with the routing already
		// persisted — record the same review.failed the reconciliation path
		// does. Refusals that touched nothing (unknown id, late duplicate)
		// leave no FAILED record and no event.
		if !wasFailed && reviewStatus(run, res.ReviewID) == shared.ReviewFailed {
			appendReviewFailed(run, in, res.ReviewID, err.Error(), errW)
		}
		return
	}
	payload := map[string]interface{}{
		"review_id":  res.ReviewID,
		"run_status": res.RunStatus,
	}
	// M3 (recovery round): the verdict names the ATTEMPT it concluded — the
	// reducer defaults an attempt-less result to the active attempt; belt the
	// same default here so the trace always carries the join key.
	if res.AttemptID == "" {
		if rv := reviewRecord(run, res.ReviewID); rv != nil {
			res.AttemptID = rv.ActiveAttemptID
		}
	}
	if res.AttemptID != "" {
		payload["attempt_id"] = res.AttemptID
	}
	if res.Verdict != "" {
		payload["verdict"] = res.Verdict
	}
	// Evidence mirror (T12 fix-back): the SAME reconciled list the reducer
	// records (res.EvidenceToolCallIDs) is mirrored into the trace so the
	// verdict is auditable trace-side — the review-evidence scorer joins
	// these ids against tool.started records inside the review window.
	// Pinned rule: COMPLETED+PASS always carries a non-empty list (the
	// reducer's ValidateEvidence rubber-stamp guard rejects PASS with no
	// claimed ids before this event traces); NEEDS-FIX/BLOCKED carry the
	// list only when the reviewer claimed ids (a defect report or a reason
	// code is not an exoneration — evidence is optional, never fabricated).
	if len(res.EvidenceToolCallIDs) > 0 {
		payload["evidence_tool_call_ids"] = res.EvidenceToolCallIDs
	}
	// Digest mirror (T12 fix-back 2, split ruling A): the digests the reducer
	// just wrote onto the review record are mirrored so the trace carries the
	// verdict's envelope/artifact identity — digests-in-trace is established
	// §7 precedent (tool.result carries response_sha256, never content).
	// envelope_digest is set at registration; artifact_digest is the minted
	// artifact-set identity every recorded result — COMPLETED or BLOCKED —
	// carries and had compared (C1b; a reason-code block still names the
	// pinned envelope it was operating on); each mirrors only when non-empty.
	if rec := reviewRecord(run, res.ReviewID); rec != nil {
		if rec.EnvelopeDigest != "" {
			payload["envelope_digest"] = rec.EnvelopeDigest
		}
		if rec.ArtifactDigest != "" {
			payload["artifact_digest"] = rec.ArtifactDigest
		}
	}
	if err := shared.AppendEvent(run.RunID, in.SessionID, shared.TraceEvent{
		Type:    shared.EventReviewVerdict,
		Source:  subagentStopSource,
		Payload: payload,
	}); err != nil {
		fmt.Fprintf(errW, "summoner subagentstop: review.verdict trace append failed (conclusion persisted): %v\n", err)
	}
}

// routeReviewProtocolError concludes a review through the reducer's protocol
// error path (FAILED + REVIEW_RESULT checkpoint + WAIT_USER, all persisted —
// the run stays decidable) and traces review.failed. When the reducer refuses
// to route (status changed underneath, review already concluded, failed Save)
// nothing is traced and the review stays as it was — a stderr note only,
// never an error for the subagent's exit.
func routeReviewProtocolError(run *shared.RunState, in *subagentStopInput, reviewID string, problem error, errW io.Writer) {
	routed, err := run.RecordReviewProtocolError(reviewID, problem.Error())
	if !routed {
		fmt.Fprintf(errW, "summoner subagentstop: protocol error for review %s NOT routed (state untouched, review stays in flight): %v\n", reviewID, err)
		return
	}
	appendReviewFailed(run, in, reviewID, problem.Error(), errW)
	fmt.Fprintf(errW, "summoner subagentstop: review %s marked FAILED and routed to a review-result checkpoint (the run stays decidable): %v\n", reviewID, err)
}

// appendReviewFailed records one review.failed event (failure persisted first).
func appendReviewFailed(run *shared.RunState, in *subagentStopInput, reviewID, reason string, errW io.Writer) {
	if err := shared.AppendEvent(run.RunID, in.SessionID, shared.TraceEvent{
		Type:   shared.EventReviewFailed,
		Source: subagentStopSource,
		Payload: map[string]interface{}{
			"review_id": reviewID,
			"reason":    reason,
		},
	}); err != nil {
		fmt.Fprintf(errW, "summoner subagentstop: review.failed trace append failed (FAILED routing persisted): %v\n", err)
	}
}

// reviewStatus reads one review record's status ("" when absent).
func reviewStatus(run *shared.RunState, reviewID string) shared.ReviewState {
	for i := range run.Reviews {
		if run.Reviews[i].ReviewID == reviewID {
			return run.Reviews[i].Status
		}
	}
	return ""
}

// reviewRecord returns one review record by id (nil when absent).
func reviewRecord(run *shared.RunState, reviewID string) *shared.ReviewRecord {
	for i := range run.Reviews {
		if run.Reviews[i].ReviewID == reviewID {
			return &run.Reviews[i]
		}
	}
	return nil
}

// inFlightReview returns the run's unconcluded review (RUNNING, or PREPARED —
// the crash window where dispatch persistence was lost), or nil. At most one
// exists by construction: RequestReview refuses a second while one is in
// flight.
func inFlightReview(run *shared.RunState) *shared.ReviewRecord {
	for i := range run.Reviews {
		switch run.Reviews[i].Status {
		case shared.ReviewPrepared, shared.ReviewRunning:
			return &run.Reviews[i]
		}
	}
	return nil
}

// --- transcript parsing ------------------------------------------------------------

// parseReviewResult scans an agent transcript (JSONL) for the reviewer's
// structured result: the SubagentHandback tool_use blocks whose input.message
// carries a JSON object with the ReviewResult fields. The LAST such object
// wins — the final handback is the report of record. Unparseable lines and
// non-handback content are skipped (a transcript holds much more than the
// report).
func parseReviewResult(path string) (shared.ReviewResult, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return shared.ReviewResult{}, false
	}
	var last shared.ReviewResult
	found := false
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, "SubagentHandback") {
			continue // cheap pre-filter: handback lines are rare
		}
		for _, msg := range handbackMessages(line) {
			if res, ok := extractReviewResult(msg); ok {
				last = res
				found = true
			}
		}
	}
	return last, found
}

// handbackMessages extracts the input.message strings of every
// SubagentHandback tool_use block in one transcript line (an assistant entry).
// A line that is not JSON, or has no such block, yields nothing.
func handbackMessages(line string) []string {
	var entry struct {
		Message struct {
			Content []struct {
				Type  string `json:"type"`
				Name  string `json:"name"`
				Input struct {
					Message string `json:"message"`
				} `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return nil
	}
	var msgs []string
	for _, block := range entry.Message.Content {
		if block.Type == "tool_use" && block.Name == "SubagentHandback" && block.Input.Message != "" {
			msgs = append(msgs, block.Input.Message)
		}
	}
	return msgs
}

// extractReviewResult pulls the ReviewResult JSON object out of one handback
// message: bare, fenced (```json … ``` / ``` … ```), or embedded in prose.
// A candidate must carry review_id and run_status — everything else is
// validated by RecordReviewResult.
func extractReviewResult(msg string) (shared.ReviewResult, bool) {
	s := strings.TrimSpace(msg)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		} else {
			s = ""
		}
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
	}
	if res, ok := unmarshalResult(s); ok {
		return res, true
	}
	// Embedded in prose: first '{' … last '}'.
	if i := strings.IndexByte(s, '{'); i >= 0 {
		if j := strings.LastIndexByte(s, '}'); j > i {
			return unmarshalResult(s[i : j+1])
		}
	}
	return shared.ReviewResult{}, false
}

// unmarshalResult decodes one candidate string; unknown fields are tolerated
// (the reviewer report may carry more than the wire contract), but review_id
// and run_status are the minimum shape.
func unmarshalResult(s string) (shared.ReviewResult, bool) {
	var res shared.ReviewResult
	if err := json.Unmarshal([]byte(s), &res); err != nil {
		return res, false
	}
	if res.ReviewID == "" || res.RunStatus == "" {
		return res, false
	}
	return res, true
}

// liveRuns filters out CLOSED runs — a closed run is not registered against.
func liveRuns(runs []*shared.RunState) []*shared.RunState {
	var live []*shared.RunState
	for _, r := range runs {
		if r.Status != shared.StatusClosed {
			live = append(live, r)
		}
	}
	return live
}
