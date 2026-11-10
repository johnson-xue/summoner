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
	os.Exit(runStatus(os.Stdin, os.Stdout, os.Stderr))
}

// statusInput is the parsed payload: one JSON object, of which only session_id
// is read (the viewer consumes the same envelope the hooks do).
type statusInput struct {
	SessionID string
}

// parseInput decodes one payload object. Anything that is not a single JSON
// object is an error — the caller fails closed (exit 2, the only exit-2 path).
// Trailing garbage after the object is refused.
func parseInput(r io.Reader) (*statusInput, error) {
	var raw struct {
		SessionID string `json:"session_id"`
	}
	dec := json.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("stdin is not a hook JSON object: %v", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing data after the JSON object")
	}
	return &statusInput{SessionID: raw.SessionID}, nil
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
	fmt.Fprint(outW, renderStatus(runs[0], sandboxTier()))
	return 0
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
	fmt.Fprintf(&b, "awaiting: %s\n", awaitingLine(run))
	fmt.Fprintf(&b, "default recall: %s\n", defaultRecallLine(run))
	fmt.Fprintf(&b, "budget: %s\n", budgetLine(run))
	fmt.Fprintf(&b, "sandbox: %s\n", sandbox)
	if cp := run.CurrentCheckpoint(); cp != nil {
		b.WriteString(checkpointBlock(run, *cp))
	}
	return b.String()
}

// routeLine renders the phase route: the framework's per-workflow sequence, or
// the run's own visits when the workflow has no framework sequence yet
// (review/ship/ops/release open per the project manifest — T9).
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

// awaitingLine names who or what the run currently waits for (当前等待谁).
func awaitingLine(run *shared.RunState) string {
	if run.Change != nil && !run.Change.Applied {
		return "requirement change open (CHANGE_OPEN) — the model owes delta + impact assessment, then the single-point confirm"
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
// 已接受前序阶段) — read-only, never applied here.
func defaultRecallLine(run *shared.RunState) string {
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
	return fmt.Sprintf("phase_tool_calls %s · failed_checks %s · run_rework %s · delegations %s",
		lim(run.Limits.PhaseToolCalls, run.Counters.PhaseToolCalls),
		lim(run.Limits.FailedChecks, run.Counters.FailedChecks),
		lim(run.Limits.RunRework, run.Counters.RunRework),
		lim(run.Limits.Delegations, run.Counters.Delegations))
}

// menuForKind mirrors shared.legalDecisionForKind: every label LEADS with the
// exact decision keyword (PostToolUse maps the leading word; any other leading
// word = decision rejected). review-result checkpoints take the full default
// set — their note text (verdict) guides which options make sense.
func menuForKind(kind shared.CheckpointKind) []string {
	switch kind {
	case shared.CheckpointChangeConfirm:
		return []string{
			"accept: 接受变更并恢复开发",
			"stop: 停止——保留状态退出",
		}
	case shared.CheckpointRecovery:
		return []string{
			"continue: 接受现状，恢复运行",
			"recall: 回城——回到指定历史阶段重做",
			"stop: 停止——保留状态退出",
		}
	default: // PHASE, REVIEW_RESULT
		return []string{
			"continue: 继续下一阶段",
			"skip: 跳过该阶段（标 UNVERIFIED / UNREVIEWED，不能伪造 PASS）",
			"done: 完成——进入收尾（post-game）",
			"recall: 回城——回到指定历史阶段重做",
			"review: [v] 对本阶段产物发起独立审查",
			"stop: 停止——保留状态退出",
		}
	}
}

// checkpointBlock renders the pending checkpoint (id, nonce, kind, phase,
// note, criteria, artifact versions, decision menu) — the block the model
// embeds verbatim; the Stop hook binds the display off its id + nonce.
func checkpointBlock(run *shared.RunState, cp shared.CheckpointState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n⚡ SUMMONER CHECKPOINT %s — nonce %s — %s\n", cp.ID, cp.Nonce, cp.Kind)
	phase := cp.PhaseID
	if phase == "" {
		phase = "—"
	}
	fmt.Fprintf(&b, "phase: %s\n", phase)
	note := cp.Note
	if note == "" {
		note = "—"
	}
	fmt.Fprintf(&b, "note: %s\n", note)
	criteria := append([]string(nil), cp.CriteriaIDs...)
	sort.Strings(criteria)
	if len(criteria) == 0 {
		b.WriteString("criteria: —\n")
	} else {
		b.WriteString("criteria: " + strings.Join(criteria, ", ") + "\n")
	}
	paths := make([]string, 0, len(cp.ArtifactVersions))
	for p := range cp.ArtifactVersions {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		b.WriteString("artifacts: —\n")
	} else {
		b.WriteString("artifacts:\n")
		for _, p := range paths {
			fmt.Fprintf(&b, "  - %s @ %s\n", p, shortDigest(cp.ArtifactVersions[p]))
		}
	}
	b.WriteString("menu:\n")
	for _, opt := range menuForKind(cp.Kind) {
		b.WriteString("  " + opt + "\n")
	}
	_ = run // checkpoint self-carries everything the block renders
	return b.String()
}
