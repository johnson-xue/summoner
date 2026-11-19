// summoner-status — the read-only status viewer of the single-loop rewrite
// (spec docs/superpowers/specs/2026-11-09-single-loop-checkpoint-design.md §4
// 只读视图 + controller Ruling 4). NOT a hook: hooks.json never wires it. The
// /summoner:status command (commands/summoner-status.md) runs it over Bash and
// pastes its output verbatim; when a checkpoint is pending, the trailing
// checkpoint block of this very render is what the model embeds so the Stop
// hook's display verification (checkpoint id + nonce in last_assistant_message)
// passes by construction. The render format is canonical in
// references/checkpoint-protocol.md — never hand-write a variant.
//
// READ-ONLY, NEVER A GATE: it mutates no state file and appends no trace
// event. Exit 2 is reserved for unreadable INPUT (unparseable stdin) only —
// unreadable run state, ambiguity (multiple live runs) and an absent run are
// surfaced honestly on stdout/stderr with exit 0. Ambiguity is never guessed:
// every live run is named and nothing is rendered for it.
//
// Input is the same JSON envelope the other hooks parse; session_id is the one
// field read. `{}` (unknown session id) is legal: the viewer then resolves the
// project's live runs across sessions — exactly one renders, several are named.
//
// Sandbox tier (spec §3 信任阶梯, as shipped by hooks/contract): probe active →
// L2; probe inactive + no sandbox claim in the user's settings → L1（未配置沙箱）;
// probe inactive + settings claiming sandbox.enabled → BLOCKED（配置了沙箱但未生效）
// — never a silent degradation.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/johnson-xue/summoner/hooks/contract"
	shared "github.com/johnson-xue/summoner/hooks/shared"
)

func main() {
	if len(os.Args) != 1 {
		if len(os.Args) != 3 || os.Args[1] != "--verify-trace" {
			fmt.Fprintln(os.Stderr, "usage: summoner-status --verify-trace <exact-file.jsonl>")
			os.Exit(2)
		}
		digest, err := shared.VerifyTraceFile(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"trace_sha256": digest}); err != nil {
			os.Exit(2)
		}
		return
	}
	os.Exit(runStatus(os.Stdin, os.Stdout, os.Stderr))
}

// statusInput is the parsed payload: one JSON object, of which only
// session_id and the optional `view` are read.
type statusInput struct {
	SessionID string
	View      string
}

// parseInput decodes one payload object. Anything that is not a single JSON
// object is an error — the caller fails closed (exit 2, the only exit-2 path).
// Trailing garbage after the object is refused. EMPTY (or whitespace-only)
// stdin is the EMPTY OBJECT: the protocol's own render recipe is
// `summoner-status <<< '{}'`, and a bare invocation is the model's natural
// spelling of the same thing — it renders the useful no-payload view instead
// of erroring (user report 2026-11-19).
func parseInput(r io.Reader) (*statusInput, error) {
	var raw struct {
		SessionID string `json:"session_id"`
		View      string `json:"view"`
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("stdin is not a hook JSON object: %v", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return &statusInput{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("stdin is not a hook JSON object: %v", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing data after the JSON object")
	}
	return &statusInput{SessionID: raw.SessionID, View: raw.View}, nil
}

// runStatus is the whole viewer. Returns the process exit code.
func runStatus(stdin io.Reader, outW, errW io.Writer) int {
	in, err := parseInput(stdin)
	if err != nil {
		fmt.Fprintf(errW, "summoner-status: unreadable input — %v\n", err)
		return 2
	}
	runs, err := liveRunsFor(in.SessionID)
	if err != nil {
		// Unreadable state is not an input error: surface it, stay exit 0.
		fmt.Fprintf(errW, "summoner-status: run state unreadable — %v\n", err)
		fmt.Fprintln(outW, "Summoner run state unreadable — cannot render (read-only viewer; nothing was changed).")
		return 0
	}
	switch {
	case len(runs) == 0:
		fmt.Fprintln(outW, "No active Summoner run for this session/project (read-only viewer; nothing was started).")
		return 0
	case len(runs) > 1:
		ids := make([]string, 0, len(runs))
		for _, r := range runs {
			ids = append(ids, r.RunID+" ("+string(r.Status)+")")
		}
		fmt.Fprintf(outW, "%d live Summoner runs — ambiguous; naming them, rendering none: %s\n",
			len(runs), strings.Join(ids, ", "))
		return 0
	}
	run := runs[0]
	// Strictly typed read-only views (final-fix wave C4/C1c). The ENTIRE
	// output of view:checkpoint is the canonical checkpoint message — the
	// same bytes the Stop hook's whole-message binding compares; ordinary
	// route/history diagnostics never join that view. view:review serves the
	// EXACT reviewer launch capsule bytes plus the read-only review state —
	// status never mints or rotates authority.
	switch in.View {
	case "checkpoint":
		cp := run.CurrentCheckpoint()
		if cp == nil {
			fmt.Fprintln(errW, "summoner-status: view:checkpoint — no open checkpoint (read-only viewer).")
			return 0
		}
		fmt.Fprint(outW, shared.RenderCheckpointMessage(run, *cp))
		return 0
	case "review":
		fmt.Fprint(outW, reviewView(run))
		return 0
	case "":
		// default: the ordinary status render
	default:
		fmt.Fprintf(errW, "summoner-status: unknown view %q (supported: checkpoint, review) — refusing to guess.\n", in.View)
		return 2
	}
	fmt.Fprint(outW, renderStatus(run, sandboxTier()))
	return 0
}

// reviewView serves the read-only review surface: for each review record its
// id/state/attempt and — for a dispatchable PREPARED attempt — the EXACT
// launch capsule bytes (the whole Agent prompt, verbatim). Copying these
// bytes into the Agent prompt is the only legal dispatch.
func reviewView(run *shared.RunState) string {
	var b strings.Builder
	if len(run.Reviews) == 0 {
		return "no review records (read-only view)\n"
	}
	for i := range run.Reviews {
		rv := run.Reviews[i]
		fmt.Fprintf(&b, "review %s — %s", rv.ReviewID, rv.Status)
		if rv.ActiveAttemptID != "" {
			fmt.Fprintf(&b, " — active attempt %s", rv.ActiveAttemptID)
		}
		b.WriteString("\n")
		if rv.Status == shared.ReviewPrepared && rv.ActiveAttemptID != "" {
			if capsule, ok := shared.BuildReviewCapsule(run, rv.ReviewID, rv.ActiveAttemptID); ok {
				if bytes, err := capsule.Bytes(); err == nil {
					b.WriteString("launch capsule (pass VERBATIM as the reviewer Agent prompt — whole-message equality):\n")
					b.Write(bytes)
				}
			}
		}
	}
	return b.String()
}

// liveRunsFor resolves the live (non-CLOSED) runs to view. A session id scopes
// through shared.SessionRuns (the same resolution the hooks use); an empty one
// falls back to scanning every session under the project key — the model does
// not know its own session id when invoking the viewer over Bash.
func liveRunsFor(sessionID string) ([]*shared.RunState, error) {
	if sessionID != "" {
		runs, err := shared.SessionRuns(sessionID)
		if err != nil {
			return nil, err
		}
		return liveRuns(runs), nil
	}
	root, err := statusStateRoot()
	if err != nil {
		return nil, err
	}
	projDir := filepath.Join(root, shared.ProjectKey())
	sessions, err := os.ReadDir(projDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*shared.RunState
	for _, s := range sessions {
		if !s.IsDir() {
			continue
		}
		runs, err := shared.SessionRuns(s.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, liveRuns(runs)...)
	}
	return out, nil
}

// statusStateRoot mirrors the shared package's state-root resolution
// (runStateRoot is package-private); kept in lockstep by tests only through
// the env override, which both paths honor identically.
func statusStateRoot() (string, error) {
	if d := os.Getenv("SUMMONER_STATE_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".summoner", "state-v2"), nil
}

// liveRuns filters out CLOSED runs — a closed run has nothing left to view.
func liveRuns(runs []*shared.RunState) []*shared.RunState {
	var live []*shared.RunState
	for _, r := range runs {
		if r.Status != shared.StatusClosed {
			live = append(live, r)
		}
	}
	return live
}

// --- sandbox tier ------------------------------------------------------------------

// sandboxConfigured reads the user's settings best-effort: $CLAUDE_CONFIG_DIR
// or ~/.claude, settings.json — an absent or unparseable file is simply
// unconfigured (a viewer must never fail over a settings read). A sandbox
// block claiming enablement counts as configured; the probe then decides
// whether the claim is actually in effect.
func sandboxConfigured() bool {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		dir = filepath.Join(home, ".claude")
	}
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return false
	}
	var doc struct {
		Sandbox struct {
			Enabled bool `json:"enabled"`
		} `json:"sandbox"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return false
	}
	return doc.Sandbox.Enabled
}

// sandboxTier composes the local settings read with the shipped classifier
// (hooks/contract): the probe is injectable, so tests never touch the home.
func sandboxTier() string {
	switch contract.ClassifySandbox(sandboxConfigured()) {
	case contract.TierL2:
		return "L2"
	case contract.TierBlocked:
		return "BLOCKED（配置了沙箱但未生效）"
	default:
		return "L1（未配置沙箱）"
	}
}

// --- the canonical render (Ruling 4: one format, two uses) --------------------------

// renderStatus renders one live run: the §4 read-only view (route, visits,
// artifact versions, unmet criteria, who is awaited, default recall, budget,
// sandbox tier) plus — when a checkpoint is pending — the checkpoint block the
// model embeds verbatim. Pure: it reads only the given state.
func renderStatus(run *shared.RunState, sandbox string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "⚡ SUMMONER STATUS — run %s · workflow %s · %s\n", run.RunID, run.Workflow, run.Status)
	b.WriteString(routeLine(run))
	b.WriteString(visitLines(run))
	b.WriteString(artifactLines(run))
	b.WriteString(unmetLines(run))
	b.WriteString(changeLines(run))
	b.WriteString(unknownLines(run))
	fmt.Fprintf(&b, "awaiting: %s\n", awaitingLine(run))
	fmt.Fprintf(&b, "default recall: %s\n", defaultRecallLine(run))
	fmt.Fprintf(&b, "budget: %s\n", budgetLine(run))
	b.WriteString(postGameLines(run))
	fmt.Fprintf(&b, "sandbox: %s\n", sandbox)
	if cp := run.CurrentCheckpoint(); cp != nil {
		// The embedded checkpoint block IS the canonical message — the same
		// renderer view:checkpoint serves and the Stop hook binds (C4: one
		// shared renderer, no second form to drift).
		b.WriteString(shared.RenderCheckpointMessage(run, *cp))
	}
	return b.String()
}

// routeLine renders the phase route: the framework's per-workflow sequence
// (all seven verbs are routed — hooks/shared/workflow.go), or the run's own
// visits when the workflow has no framework sequence (forward-compat for
// unlisted workflows).
func routeLine(run *shared.RunState) string {
	seq, err := shared.WorkflowPhases(run.Workflow)
	if err == nil {
		return "route: " + strings.Join(seq, " → ") + "\n"
	}
	if len(run.Phases) == 0 {
		return "route: (no framework sequence; visits: none yet)\n"
	}
	ids := make([]string, 0, len(run.Phases))
	for _, p := range run.Phases {
		ids = append(ids, p.ID)
	}
	return "route: (no framework sequence; visits: " + strings.Join(ids, " → ") + ")\n"
}

// visitState names one visit's state in the run's own vocabulary.
func visitState(p shared.PhaseState) string {
	switch {
	case p.Open:
		return "open"
	case p.Outcome == "":
		return "submitted (awaiting decision)"
	default:
		return p.Outcome // accepted | skipped | recalled
	}
}

func visitLines(run *shared.RunState) string {
	if len(run.Phases) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("visits:\n")
	for _, p := range run.Phases {
		fmt.Fprintf(&b, "  - %s: %s\n", p.ID, visitState(p))
	}
	return b.String()
}

// artifactLines renders the checkpoint-frozen artifact versions (已验产物版本),
// latest checkpoint per path wins, sorted for determinism.
func artifactLines(run *shared.RunState) string {
	merged := map[string]string{}
	for _, cp := range run.Checkpoints {
		for p, d := range cp.ArtifactVersions {
			merged[p] = d
		}
	}
	if len(merged) == 0 {
		return ""
	}
	paths := make([]string, 0, len(merged))
	for p := range merged {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	b.WriteString("artifacts (checkpoint-frozen versions):\n")
	for _, p := range paths {
		fmt.Fprintf(&b, "  - %s @ %s\n", p, shortDigest(merged[p]))
	}
	return b.String()
}

func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

// unmetLines renders the unmet criteria (未满足标准): ids marked stale plus the
// pending checkpoint's not-yet-accepted criteria.
func unmetLines(run *shared.RunState) string {
	cp := run.CurrentCheckpoint()
	pending := map[string]bool{}
	if cp != nil {
		for _, id := range cp.CriteriaIDs {
			pending[id] = true
		}
	}
	var lines []string
	for _, id := range run.Stale {
		lines = append(lines, "  - "+id+" (stale)")
		delete(pending, id) // a stale id pending re-acceptance shows once, as stale
	}
	if cp != nil {
		ids := append([]string(nil), cp.CriteriaIDs...)
		sort.Strings(ids)
		for _, id := range ids {
			if pending[id] {
				lines = append(lines, "  - "+id+" (pending at "+cp.ID+")")
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "unmet criteria:\n" + strings.Join(lines, "\n") + "\n"
}

// changeLines renders the CHANGE_OPEN sub-state (spec §1 增补, T10): why the
// change opened, the SAVED development position the loop restores on accept,
// and the requirements version the delta bumped. Rendered while a change is
// open (before and after its delta is applied); nothing once it closed.
func changeLines(run *shared.RunState) string {
	if run.Change == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("requirement change (CHANGE_OPEN):\n")
	note := run.Change.Note
	if note == "" {
		note = "—"
	}
	fmt.Fprintf(&b, "  note: %s\n", note)
	p := run.Change.SavedPosition
	task := p.CurrentTask
	if task == "" {
		task = "—"
	}
	progress := p.TestProgress
	if progress == "" {
		progress = "—"
	}
	pending := "—"
	if len(p.PendingVerifications) > 0 {
		pending = strings.Join(p.PendingVerifications, ", ")
	}
	fmt.Fprintf(&b, "  saved position: current_task: %s · test_progress: %s · pending_verifications: %s\n", task, progress, pending)
	fmt.Fprintf(&b, "  requirements version: %d\n", run.RequirementsVersion)
	return b.String()
}

// unknownLines renders the reconciliation ladder (recovery round I3): every
// unresolved OUTCOME_UNKNOWN entry with its capability, target and — for a
// supported declaration — the EXACT probe command the gate's one-shot ticket
// binds to. Status SERVES these bytes; it never mints authority (running the
// command is an ordinary charged read, and only the gate admission that
// byte-equals this command consumes the ticket). An unsupported declaration
// names its honest ceiling: indeterminate-only.
func unknownLines(run *shared.RunState) string {
	var unresolved []shared.OutcomeUnknown
	for _, e := range run.OutcomeUnknowns {
		if !e.Resolved {
			unresolved = append(unresolved, e)
		}
	}
	if len(unresolved) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "outcome unknowns (%d unresolved):\n", len(unresolved))
	for _, e := range unresolved {
		if cmd := e.MutationDecl.ExactProbeCommand(); cmd != "" {
			fmt.Fprintf(&b, "  - %s: %s %s — exact probe: %s\n", e.ToolUseID, e.MutationDecl.Capability, e.MutationDecl.Target, cmd)
			continue
		}
		cap := shared.CapabilityUnsupported
		if e.MutationDecl != nil && e.MutationDecl.Capability != "" {
			cap = e.MutationDecl.Capability
		}
		fmt.Fprintf(&b, "  - %s: %s — no exact probe (resolves indeterminate-only)\n", e.ToolUseID, cap)
	}
	return b.String()
}

// awaitingLine names who or what the run currently waits for (当前等待谁).
func awaitingLine(run *shared.RunState) string {
	if run.Change != nil && !run.Change.Applied {
		return "requirement change open (CHANGE_OPEN) — the model owes delta + impact assessment, then the single-point confirm"
	}
	if run.Change != nil {
		// Applied: WAIT_USER at the single-point confirm checkpoint.
		if cp := run.CurrentCheckpoint(); cp != nil {
			return fmt.Sprintf("等待变更确认 — user decision at change-confirm checkpoint %s (nonce %s)", cp.ID, cp.Nonce)
		}
		return "等待变更确认 (change-confirm checkpoint)"
	}
	switch run.Status {
	case shared.StatusWaitUser:
		if cp := run.CurrentCheckpoint(); cp != nil {
			return fmt.Sprintf("user decision at checkpoint %s (nonce %s)", cp.ID, cp.Nonce)
		}
		return "user decision (checkpoint pending)"
	case shared.StatusReviewPending:
		for i := len(run.Reviews) - 1; i >= 0; i-- {
			rv := run.Reviews[i]
			if rv.Status == shared.ReviewPrepared || rv.Status == shared.ReviewRunning {
				return fmt.Sprintf("reviewer %s (%s)", rv.ReviewID, rv.Status)
			}
		}
		return "reviewer (no in-flight review record)"
	case shared.StatusOutcomeUnknown:
		n := 0
		for _, e := range run.OutcomeUnknowns {
			if !e.Resolved {
				n++
			}
		}
		return fmt.Sprintf("reconciliation probes (%d unresolved)", n)
	case shared.StatusPostGamePending:
		return "post-game questionnaire (CLOSED only after it persists)"
	default: // ACTIVE
		for _, p := range run.Phases {
			if p.Open {
				return fmt.Sprintf("phase work in %q (the model executes; submit via summoner:phase-submit)", p.ID)
			}
		}
		return "next phase to open per the manifest mapping"
	}
}

// defaultRecallLine mirrors the reducer's recall plan (spec §4: 默认指向最近
// 已接受前序阶段) — read-only, never applied here. I4 (recovery round): when
// the pending checkpoint carries its offered RecallTargets, the line mirrors
// THOSE — "latest" resolves to its visit and the offered labels are exactly
// what "Recall: <target>" accepts here (the reducer's planRecallTarget set,
// never a status-side recomputation).
func defaultRecallLine(run *shared.RunState) string {
	if cp := run.CurrentCheckpoint(); cp != nil && len(cp.RecallTargets) > 0 {
		labels := make([]string, 0, len(cp.RecallTargets))
		for _, t := range cp.RecallTargets {
			labels = append(labels, t.Label)
		}
		head := "latest"
		if t := cp.RecallTargets[0]; t.Label == "latest" && t.VisitID != "" {
			head = "latest → " + t.VisitID
		}
		return fmt.Sprintf("%s (offered: %s)", head, strings.Join(labels, ", "))
	}
	for i := len(run.Phases) - 1; i >= 0; i-- {
		if run.Phases[i].Outcome == "accepted" {
			return run.Phases[i].ID + " (latest accepted prior phase)"
		}
	}
	if cp := run.CurrentCheckpoint(); cp != nil && cp.PhaseID != "" {
		return cp.PhaseID + " (no accepted prior phase — recall re-enters the current phase)"
	}
	return "none (no phase history yet)"
}

func budgetLine(run *shared.RunState) string {
	lim := func(l, c int) string {
		if l <= 0 {
			return fmt.Sprintf("%d/∞", c)
		}
		return fmt.Sprintf("%d/%d", c, l)
	}
	// T18: the settlement state is part of the budget view — 未确认 names the
	// run whose first checkpoint still owes the budget answer (the post-hoc
	// rule rejects a budget-less CONTINUE there); extensions count the
	// applied extension receipts.
	state := "未确认（首检查点待确认）"
	if run.BudgetConfirmed {
		state = "已确认"
	}
	if n := len(run.BudgetExtensions); n > 0 {
		state += fmt.Sprintf(" · extended ×%d", n)
	}
	return fmt.Sprintf("phase_tool_calls %s · failed_checks %s · run_rework %s · delegations %s · %s",
		lim(run.Limits.PhaseToolCalls, run.Counters.PhaseToolCalls),
		lim(run.Limits.FailedChecks, run.Counters.FailedChecks),
		lim(run.Limits.RunRework, run.Counters.RunRework),
		lim(run.Limits.Delegations, run.Counters.Delegations),
		state)
}

// postGameLines renders the POST_GAME_PENDING closure view (T18, spec §6):
// which pinned questionnaire markers are still owed, the fixed journal path,
// and what closure requires. The questionnaire questions carry the pinned
// "[pg:qN]" tokens + run id; the journal writes to the fixed path; CLOSED
// comes only after both persist.
func postGameLines(run *shared.RunState) string {
	if run.Status != shared.StatusPostGamePending {
		return ""
	}
	kind := "normal"
	if run.PostGame != nil && run.PostGame.Kind != "" {
		kind = run.PostGame.Kind
	}
	var b strings.Builder
	fmt.Fprintf(&b, "post-game (kind %s — CLOSED only after the questionnaire and journal persist):\n", kind)
	for _, m := range shared.PostGameMarkers() {
		state := "owed"
		if run.PostGame != nil && run.PostGame.Answers[m] != "" {
			state = "answered"
		}
		fmt.Fprintf(&b, "  - %s %s: %s\n", shared.PostGameMarkerToken(m), m, state)
	}
	if run.PostGame != nil && run.PostGame.Journal != nil {
		fmt.Fprintf(&b, "  journal: %s @ %s (observed)\n", shared.JournalPath(run.RunID), shortDigest(run.PostGame.Journal.Digest))
	} else {
		fmt.Fprintf(&b, "  journal: %s (not yet written)\n", shared.JournalPath(run.RunID))
	}
	return b.String()
}
