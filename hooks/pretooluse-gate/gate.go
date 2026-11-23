package main

// gate.go — the pure decision core: decide() inspects the parsed payload plus
// the loaded run state and returns allow/deny with the side-effect plan;
// apply() performs the I/O (reducer transitions, counters, trace appends).
// decide() touches the filesystem in exactly ONE place: buildMutationDecl's
// preimage read (recovery round I3, §4) — freezing what the target's bytes
// ARE is part of admission itself and cannot be deferred past the effect.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

const skillPhaseSubmit = "summoner:phase-submit"

// skillChange is the requirement-change micro-skill (spec §1 增补 + T10
// ruling): subcommand `open` freezes the development position and enters
// CHANGE_OPEN; `apply` submits the delta + impact assessment. It is the ONLY
// Skill call the gate allows while WAIT_USER (the explicit checkpoint entry
// into the change loop).
const skillChange = "summoner:change"

// skillStatus is the read-only status viewer (Ruling 4's render surface).
// It is allowed at WAIT_USER: a pending checkpoint must stay re-displayable
// (content feedback and page navigation re-render before re-asking) — the
// viewer reads state and renders, never advances anything.
const skillStatus = "summoner:status"

// skillReviewSubmit is the envelope-registration micro-skill (T17, spec §3 ⑤):
// its args are ONE review envelope (strict JSON) which the gate decodes,
// validates against the run's frozen acceptance view minus recorded drops and
// the project's check registry, then registers through RequestReview. Legal
// entries: REVIEW_PENDING (a [v] decision's obligation, a same-envelope
// re-send, or an in-flight review) and — spec §1 终局⑤默认强制 — the TERMINAL
// DEFAULT position: ACTIVE immediately after the fixed review phase's
// acceptance (the user's default CONTINUE at the review checkpoint). An
// explicit SKIP of the review phase closes the default (UNREVIEWED); [v] at
// any checkpoint remains the separate on-demand path (spec §4).
const skillReviewSubmit = "summoner:review-submit"

// skillProbeSubmit is the OUTCOME_UNKNOWN resolution micro-skill (T19, spec §5
// OUTCOME_UNKNOWN 与副作用恢复): its args are the whole resolution batch
// {unknown_tool_use_ids[], outcome, probe_tool_use_ids[]} — one OBSERVED
// read-class Bash probe per unknown, equal lengths. The gate validates the
// ENTIRE batch against the run's trace (every probe: read-class Bash, observed
// result, recorded after the unknown's marking event, not failed, never
// reused) before any transition; the reducer then resolves each entry with its
// own durable probe receipt. Legal only while OUTCOME_UNKNOWN.
const skillProbeSubmit = "summoner:probe-submit"

// decision is decide()'s verdict: allow with an effect plan, or deny with a
// human-readable reason (the exit-2 stderr text). A deny may also carry a
// submission rejection to trace: phase_submitted.rejected is the record of
// EVERY submission-path failure during an active run (ruling 6 + review I8).
// denyEvents are trace events a DENIAL itself owes (T19: an honest BLOCKED
// check.result when the ladder refuses to execute a check — the refusal is an
// outcome, not silence); main() appends them on the deny path.
type decision struct {
	allow      bool
	reason     string
	eff        *effects      // nil = allowed, nothing to mutate
	reject     *submitReject // non-nil: trace phase_submitted.rejected on deny
	denyEvents []shared.TraceEvent
}

// submitReject marks a denial that must persist phase_submitted.rejected.
type submitReject struct {
	phaseID string // "" when the payload never parsed far enough to carry one
}

// effects is the ordered side-effect plan apply() executes. It never mixes:
// a denied call carries no effects (its only record is the exit-2 stderr —
// except phase-submit reducer rejections, which also trace
// phase_submitted.rejected per ruling 6, and T19's denyEvents).
type effects struct {
	openPhase     string                   // OpenPhase(id): Skill candidate (ruling 3) or phase-submit fallback
	submit        *shared.PhaseSubmission  // SubmitPhase payload (ruling 6)
	staged        *shared.StagedSnapshot   // C1: hook-verified semantic staging pinned onto the checkpoint
	l1Checks      []shared.CheckDef        // T19: framework-adapter refs of the submit, run in-gate
	delegation    string                   // subagent_type — StartDelegation + delegation.started
	delegateRevID string                   // review_id bound to the delegation, when reviewer
	delegateAttID string                   // attempt id bound to the reviewer delegation (C3)
	reviewSubmit  *shared.Envelope         // RequestReview + review.prepared (summoner:review-submit)
	reviewRecover *reviewRecovery          // RecoverReview — the wedged-attempt recovery form (recovery round C3)
	reviewReg     []shared.CheckDef        // the manifest registry the mint re-freezes check defs from
	changeOpen    *changeOpenEffect        // OpenRequirementChangeAt + change.opened (summoner:change open)
	changeApply   *shared.RequirementDelta // ApplyRequirementDelta + change.delta_applied (summoner:change apply)
	probeSubmit   *probeSubmitEffect       // ResolveOutcomeProbed + tool.resolved (summoner:probe-submit)
	bashClass     *bashClassEffect         // T19: read-class Bash markers on tool.started
	writeDecl     *shared.MutationDecl     // I3: the frozen pre-effect declaration on an admitted Write/Edit
}

// changeOpenEffect is the summoner:change `open` payload: the note (what the
// user wants changed) and the model-proposed development position to freeze.
type changeOpenEffect struct {
	note     string
	position shared.DevPosition
}

// reviewRecovery is the review-submit RECOVERY form's strict metadata
// (recovery round C3): the wedged in-flight review and exactly the attempt
// whose result never arrived. Nothing else rides this channel — no envelope,
// no transcript path, no verdict.
type reviewRecovery struct {
	reviewID  string
	attemptID string
}

// bashClassEffect is the T19 admission record for a READ-CLASS Bash call: the
// markers apply() writes on its tool.started. read_class is the crash-scan
// class (a Bash intent without it is write-class by definition); probe marks
// the in-band sandbox-probe channel (only the anchored invocation shape gets
// it); l2 carries the admitted project check whose result posttooluse joins.
// At most one of probe/l2 is set (the shapes are mutually exclusive: the probe
// invocation has no operands, an exact-match check line is a fixed command).
// command + probeOf are the recovery-round I3 additions: every read-class
// Bash's started carries its command verbatim, and when the command BYTE-EQUALS
// an unresolved declared unknown's ExactProbeCommand (and that ticket is not
// yet bound — one-shot), started also carries probe_of: the PostToolUse
// matcher then records its verdict on the probe's result.
type bashClassEffect struct {
	readClass bool
	probe     bool
	l2        *shared.CheckDef
	command   string
	probeOf   string
}

// probeSubmitEffect is the validated resolution batch of summoner:probe-submit
// — decide() already verified every pair against the trace; apply() replays
// the validation findings into reducer receipts. Each pair carries its OWN
// verdict: the one the PostToolUse matcher recorded on the probe's result
// (never model-supplied — the wire outcome field is gone).
type probeSubmitEffect struct {
	pairs []probePair // positionally joined: unknown ↔ its own probe + the matcher's verdict
}

type probePair struct {
	unknownID string
	probeID   string
	verdict   string // the recorded probe_match verdict (absent ⇒ indeterminate)
	replay    bool   // entry already resolved with this exact pair: idempotent no-op
}

func deny(reason string) decision { return decision{allow: false, reason: reason} }

// denyWithEvent denies while tracing the events the denial itself owes — T19's
// honest BLOCKED check.results: when the ladder refuses to execute a check
// (no/declined sandbox proof, network class), the refusal is an observed
// outcome the trace must carry, never silence.
func denyWithEvent(reason string, evs ...shared.TraceEvent) decision {
	return decision{allow: false, reason: reason, denyEvents: evs}
}

// blockedCheckResult builds the BLOCKED check.result event of a ladder refusal
// (status "blocked" + a reason code; NO tier claim — a blocked result never
// asserts a trust basis it did not earn).
func blockedCheckResult(def shared.CheckDef, reason string) shared.TraceEvent {
	payload := map[string]interface{}{
		"check_id": def.CheckID,
		"trust":    def.Trust,
		"status":   shared.CheckStatusBlocked,
		"reason":   reason,
		"digest":   shared.CheckDefDigest(def),
	}
	return shared.TraceEvent{Type: shared.EventCheckResult, Payload: payload}
}

// denySubmit is a deny that also records phase_submitted.rejected — every
// submission-path failure during an active run leaves that trace (I8).
func denySubmit(phaseID, reason string) decision {
	return decision{allow: false, reason: reason, reject: &submitReject{phaseID: phaseID}}
}

func allow(eff *effects) decision { return decision{allow: true, eff: eff} }

// decide routes one tool call through the gate rules (rulings 3-9).
func decide(in *gateInput, run *shared.RunState) decision {
	skill := stringField(in.ToolInput, "skill")
	isSubmit := in.ToolName == "Skill" && skill == skillPhaseSubmit

	// Budget first (ruling 9), minus the human-control escapes (T18, spec §5
	// 预算与终止: only the user can add budget or replan — the control-plane
	// surfaces that carry an exhausted run to its checkpoint and the user's
	// answer back stay usable; everything else waits for the extension).
	if !budgetControlExempt(in, run, isSubmit) {
		if exhausted, why := run.BudgetExhausted(); exhausted {
			return deny(fmt.Sprintf(
				"Summoner gate: %s — remaining budget is zero (余量为零). Only the user can add budget or replan: submit the phase via summoner:phase-submit so the checkpoint can present the extension question.",
				why))
		}
	}

	switch in.ToolName {
	case "Skill":
		return decideSkill(in, run, skill)
	case "Edit", "Write":
		// Recovery round I3 (§4): a declared mutation freezes its pre-effect
		// declaration at admission — eligibility first (the matrix denial is
		// the more fundamental one), then the literal-intent validation over
		// the ACTUAL preimage, which may deny BEFORE any effect runs.
		d := decideWrite(run, in.ToolName)
		if !d.allow {
			return d
		}
		decl, problem := buildMutationDecl(in)
		if problem != "" {
			return deny(problem)
		}
		d.eff = &effects{writeDecl: decl}
		return d
	case "NotebookEdit":
		// §4: notebook edits have no shipped declaration (the mechanical
		// matcher cannot freeze what it cannot compute) — denied pre-effect.
		return deny("Summoner gate: NotebookEdit has no supported mutation declaration (notebook edits are an unsupported capability) — denied before effects (spec §4).")
	case "Bash":
		command := stringField(in.ToolInput, "command")
		// T19 ladder order (spec §3 Bash 副作用信任阶梯): the probe channel,
		// then the write matrix, then L2 admission, then ordinary reads.
		if shared.IsSandboxProbeInvocation(command) {
			// The in-band sandbox probe — byte-exact admitted spelling only
			// (fix-round F1: any env-prefix, $HOME/~/planted or foreign-dir
			// variant is judged below as ordinary Bash and can never produce
			// a probe observation). Marker recorded for posttooluse's
			// observation join (see bashClassEffect).
			return allow(&effects{bashClass: &bashClassEffect{readClass: true, probe: true}})
		}
		if isWriteClassCommand(command) {
			// A write-class command never earns L2 admission even when it
			// byte-matches a registered check: checks must be read-class to be
			// creditable (mutating checks stay ordinary writes).
			return decideWrite(run, "Bash")
		}
		if def, ok := matchProjectCheck(command); ok {
			return decideL2Check(run, def)
		}
		// Ordinary read-class Bash: observed only, class persisted for the
		// crash scan (a Bash intent without read_class is write-class). The
		// exact-command probe ticket binds here (I3): status serves each
		// unresolved declared unknown's ExactProbeCommand; the admission that
		// byte-equals it consumes the one-shot ticket (probe_of on started).
		return allow(&effects{bashClass: &bashClassEffect{
			readClass: true,
			command:   command,
			probeOf:   probeTicketFor(run, command),
		}})
	case "Agent":
		return decideAgent(in, run)
	case "AskUserQuestion":
		return decideAskUser(in, run)
	case "Read", "Grep", "Glob", "ToolSearch":
		return allow(nil)
	default:
		// Unmodeled tool surface (0.6.1, user report 2026-11-23: a live run
		// denied the harness's own read-only tools and every undeclared MCP
		// tool, forcing the model onto read-only Bash).
		//
		// A live run gates MUTATION. A tool whose SCHEMA has no write,
		// delegation, or process-control semantics is admitted with no
		// declaration (harnessReadOnlyTools, plus LSP — first-party
		// navigation). An MCP tool is NOT presumed read-only: the hook input
		// carries only tool_name and tool_input, with no annotation channel, so
		// the project's manifest tool_scope.allowed_tools is the only trusted
		// declaration (user ruling 2026-11-23: 按清单声明). The gate still
		// fails closed when the manifest cannot load.
		// Modeled mutation tools never reach this path: their admission
		// branches above run first, so neither the set nor a manifest entry can
		// smuggle write access past Bash/Edit/Write/Agent/AskUserQuestion.
		if in.ToolName == "LSP" || harnessReadOnlyTools[in.ToolName] || manifestDeclaresMCPTool(in.ToolName) {
			return allow(nil)
		}
		if strings.HasPrefix(in.ToolName, "mcp__") {
			return deny(fmt.Sprintf(
				"Summoner gate: tool %q is not declared usable in this run — denied before any effect. It is not listed in this project's summoner.yaml. If it only reads (a code-search or memory server, say), declare it in summoner.yaml and reload:\n  tool_scope:\n    allowed_tools: [\"%s\"]\nAn undeclared tool may mutate, so it stays closed until the project declares it (the Phase 1 diagnosis freeze holds for MCP writes too).",
				in.ToolName, in.ToolName))
		}
		return deny(fmt.Sprintf(
			"Summoner gate: tool %q is denied during a live run — it is not on the harness's read-only set (membership requires a verified schema with no write, delegation, or process-control semantics; this tool's has not been verified, or has one). Do not declare it in summoner.yaml to get past this: finish or checkpoint the run first, or use the modeled channel for the same effect (a file change goes through Edit/Write; a command through Bash).",
			in.ToolName))
	}
}

// harnessReadOnlyTools are the harness's own tools admitted during a live run
// without a declaration. Membership requires a VERIFIED schema: no field that
// writes project state, addresses another agent, or starts/stops a process.
// A harmless-sounding name is not evidence — Monitor ("watch") runs a shell
// command, which made it a full Bash bypass (0.6.1 ship review, Critical).
//
// Admitted: WebFetch/WebSearch (GET-only reads), TaskGet/TaskList (task-list
// reads), TodoWrite (the harness todo list), ScheduleWakeup (a timer — every
// call it wakes still passes this gate), ListAgents (a listing).
//
// Deliberately DENIED here, pending a schema review: Monitor (runs a shell),
// TaskStop (kills background tasks, including write-class commands, with no
// OUTCOME_UNKNOWN record), TaskCreate/TaskUpdate (assignee semantics
// unverified — may address an agent), SendMessage and the worktree/plan
// session switches.
var harnessReadOnlyTools = map[string]bool{
	"WebFetch":       true,
	"WebSearch":      true,
	"TaskGet":        true,
	"TaskList":       true,
	"TodoWrite":      true,
	"ScheduleWakeup": true,
	"ListAgents":     true,
}

// mcpToolPrefix is the Claude Code MCP tool naming pattern, mcp__<server>__<tool>.
const mcpToolPrefix = "mcp__"

// manifestDeclaresMCPTool reports whether the project's summoner.yaml names
// the tool in tool_scope.allowed_tools — and the tool is an MCP tool. The
// declaration channel is closed to every other name: a manifest entry for a
// harness tool (SendMessage, Monitor, …) would otherwise launder any denial
// past the gate, since the denial recipe is exactly "declare it". Fail-closed:
// an unloadable manifest declares nothing.
func manifestDeclaresMCPTool(name string) bool {
	if !strings.HasPrefix(name, mcpToolPrefix) {
		return false
	}
	m, err := shared.LoadRuntimeManifest()
	if err != nil || m == nil {
		return false
	}
	for _, declared := range m.ReadOnlyTools {
		if declared == name {
			return true
		}
	}
	return false
}

// declFileCap bounds the preimage read: a target whose declared bytes exceed
// 16 MiB is refused declaration (the frozen expect hash would be computable,
// but the pre-effect read itself becomes the cost a caller can impose).
const declFileCap = 16 << 20

// buildMutationDecl freezes a Write/Edit intent's pre-effect declaration
// (recovery round I3, §4): capability, the RESOLVED absolute target (F5
// "normalized target": a relative file_path and its absolute spelling mint
// byte-identical decls — and the exact probe command probes the resolved
// path), input digest, the ACTUAL preimage (sha256 of the target's current
// bytes, or "absent" for a new file), and the expect hash of the exact
// resulting bytes. The literal Edit validates over the actual preimage
// BEFORE any effect: old_string must occur exactly once (or carry
// replace_all with ≥1 occurrence); ambiguous or non-applying edits are
// denied pre-effect. A non-regular or oversized target is denied — a
// declaration the matcher cannot compute is never minted.
func buildMutationDecl(in *gateInput) (*shared.MutationDecl, string) {
	target := stringField(in.ToolInput, "file_path")
	if target == "" {
		return nil, "Summoner gate: " + in.ToolName + " requires a file_path to declare its mutation — denied before effects."
	}
	resolved := target
	if !filepath.IsAbs(target) {
		resolved = filepath.Join(shared.ProjectDir(), target)
	}
	st, err := os.Stat(resolved)
	if err == nil && !st.Mode().IsRegular() {
		return nil, fmt.Sprintf("Summoner gate: %s target %s is not a regular file — no supported mutation declaration, denied before effects.", in.ToolName, target)
	}
	if err == nil && st.Size() > declFileCap {
		return nil, fmt.Sprintf("Summoner gate: %s target %s exceeds the 16 MiB declaration cap — denied before effects.", in.ToolName, target)
	}
	current, err := os.ReadFile(resolved)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Sprintf("Summoner gate: cannot read the current bytes of %s to freeze its mutation declaration — denying before effects: %v", target, err)
	}
	preimage := "absent"
	if err == nil {
		preimage = shared.HashBytes(current)
	}

	switch in.ToolName {
	case "Write":
		content := stringField(in.ToolInput, "content")
		return &shared.MutationDecl{
			Capability:  shared.CapabilityFileWrite,
			Target:      resolved, // F5: the normalized absolute target
			InputDigest: shared.HashBytes([]byte(content)),
			Preimage:    preimage,
			Expect:      shared.HashBytes([]byte(content)),
		}, ""
	case "Edit":
		old, new := stringField(in.ToolInput, "old_string"), stringField(in.ToolInput, "new_string")
		replaceAll, _ := in.ToolInput["replace_all"].(bool)
		n := strings.Count(string(current), old)
		if n == 0 || (n > 1 && !replaceAll) {
			if n == 0 {
				return nil, fmt.Sprintf("Summoner gate: Edit's old_string is not present in %s — the literal edit does not apply; denied before effects.", target)
			}
			return nil, fmt.Sprintf("Summoner gate: Edit's old_string must occur exactly once in %s without replace_all — it occurs %d times, the literal edit is ambiguous; denied before effects.", target, n)
		}
		transformed := string(current)
		if replaceAll {
			transformed = strings.ReplaceAll(transformed, old, new)
		} else {
			transformed = strings.Replace(transformed, old, new, 1)
		}
		return &shared.MutationDecl{
			Capability:  shared.CapabilityFileEdit,
			Target:      resolved, // F5: the normalized absolute target
			InputDigest: shared.HashBytes([]byte(old + "\x00" + new)),
			Preimage:    preimage,
			Expect:      shared.HashBytes([]byte(transformed)),
		}, ""
	}
	return nil, "" // unreachable: only Write/Edit reach here
}

// probeTicketFor returns the unknown id whose EXACT probe command this read
// command byte-equals (I3 ticket binding): unresolved declared entries only,
// first match, and only while the ticket is unbound — a started event already
// carrying probe_of for the id consumes it permanently (one probe per
// unknown; a crashed probe is a lost observation that resolves indeterminate,
// never a free rebind). An unreadable trace degrades to no binding: the probe
// still runs as an ordinary read, but no resolution will accept it later.
func probeTicketFor(run *shared.RunState, command string) string {
	if run.Status != shared.StatusOutcomeUnknown || command == "" {
		return ""
	}
	evs, err := shared.LoadRunEvents(run.RunID)
	if err != nil {
		return ""
	}
	bound := make(map[string]bool)
	for _, ev := range evs {
		if ev.Type != shared.EventToolStarted {
			continue
		}
		if id, _ := ev.Payload["probe_of"].(string); id != "" {
			bound[id] = true
		}
	}
	for i := range run.OutcomeUnknowns {
		e := &run.OutcomeUnknowns[i]
		if e.Resolved || bound[e.ToolUseID] {
			continue
		}
		if e.MutationDecl != nil && e.MutationDecl.ExactProbeCommand() == command {
			return e.ToolUseID
		}
	}
	return ""
}

// matchProjectCheck resolves an exact-match project check definition for a
// read-class Bash command (T19 L2 admission, spec §3). The manifest load is
// deliberately LENIENT here: a missing manifest (nil) means no registry → no
// match (an ordinary read); a CORRUPT manifest also fails open for ordinary
// reads — the authority paths fail closed independently (admission without a
// registry is impossible, and posttooluse's result join skips on a corrupt
// manifest, so no check.result credit can ever arise from this leniency;
// bricking every read-class command over a torn summoner.yaml would only
// prevent the model from repairing it).
func matchProjectCheck(command string) (shared.CheckDef, bool) {
	m, err := shared.LoadRuntimeManifest()
	if err != nil || m == nil {
		return shared.CheckDef{}, false
	}
	return shared.MatchProjectCheck(command, m.CheckRegistry)
}

// decideL2Check admits ONE exact-match project check execution (T19, spec §3
// L2). Requirements: the definition is not network-class (L3 → honest BLOCKED
// l3_unsupported, never executed, never downgraded to "just run it"), and the
// run's trace holds a refused in-band sandbox-probe observation the admission
// can PAIR with — strictly one probe per admitted check (fix-round M1): the
// latest probe must be newer than the probe the PREVIOUS L2 admission
// consumed, so a fresh probe is required after every admission. No, declined
// or already-consumed probe → honest BLOCKED sandbox_unproven. Byte-exact
// match with the registered line IS the sandbox-bypass check on the actual
// Bash input: nothing can ride along an exact match.
func decideL2Check(run *shared.RunState, def shared.CheckDef) decision {
	if shared.IsNetworkCheckCommand(def) {
		return denyWithEvent(fmt.Sprintf(
			"Summoner gate: check %s (%s) is network-class (L3) — the sandbox cannot isolate a network check, so the ladder refuses to run it (BLOCKED l3_unsupported; 不降级为\"跑了吧\"). Use an L1 framework adapter or cite the observed BLOCKED result.",
			def.CheckID, def.Command), blockedCheckResult(def, "l3_unsupported"))
	}
	evs, err := shared.LoadRunEvents(run.RunID)
	if err != nil {
		return deny(fmt.Sprintf(
			"Summoner gate: cannot read the run's trace to verify the sandbox-probe pairing for check %s — denying (fail-closed): %v",
			def.CheckID, err))
	}
	probeID, pairing := shared.L2Pairing(evs, shared.GateFutureSeq)
	switch pairing {
	case shared.PairingMissing:
		return denyWithEvent(fmt.Sprintf(
			"Summoner gate: check %s needs a paired in-band sandbox probe before it may run — no probe observed in this run (BLOCKED sandbox_unproven). Run %s first (the adapter prints its wrapper; its refused fresh write proves the sandbox at that point in time), then re-run the exact check line.",
			def.CheckID, shared.SandboxProbeBin), blockedCheckResult(def, "sandbox_unproven"))
	case shared.PairingDeclined:
		return denyWithEvent(fmt.Sprintf(
			"Summoner gate: check %s is BLOCKED sandbox_unproven — the latest in-band probe (%s) did not prove the sandbox (write succeeded or unproven effect). L2 needs a refused probe; re-run %s and then the exact check line.",
			def.CheckID, probeID, shared.SandboxProbeBin), blockedCheckResult(def, "sandbox_unproven"))
	case shared.PairingConsumed:
		return denyWithEvent(fmt.Sprintf(
			"Summoner gate: check %s is BLOCKED sandbox_unproven — probe %s is already consumed by the previous L2 admission. The pairing is strictly one fresh probe per admitted check: re-run %s, then the exact check line.",
			def.CheckID, probeID, shared.SandboxProbeBin), blockedCheckResult(def, "sandbox_unproven"))
	}
	return allow(&effects{bashClass: &bashClassEffect{readClass: true, l2: &def}})
}

// budgetControlExempt names the HUMAN-CONTROL budget escapes (T18, spec §5
// 预算与终止 — an exhausted run must still be able to reach its checkpoint and
// receive the user's answer; everything else waits for the extension):
//
//   - summoner:phase-submit — the exhausted phase closes into its checkpoint,
//     where the extension question rides (exhaust → submit → render → answer).
//     Budget arithmetic note (fix-round F2, amended by the final-fix wave's
//     I1 visit buckets): the exempt submit runs AFTER SubmitPhase closed the
//     phase and BEFORE the continue decision authorizes the next visit —
//     outside any visit bucket — so it never ticks CountToolCall and at the
//     exhaustion checkpoint the counter is exactly L; one extension (L → 2L)
//     unblocks at every L. The pre-provider window AFTER the decision
//     charges the authorized upcoming bucket (I1), and the counter only runs
//     past the limit via the change loop (mid-phase exempt calls tick),
//     where each user-approved extension adds a full L again and repeated
//     extensions always unblock;
//   - AskUserQuestion while WAIT_USER (the nonce-gated decision channel;
//     review I5 keeps ordinary ACTIVE questions budget-gated) and while
//     POST_GAME_PENDING (the questionnaire is owed regardless of budget);
//   - summoner:change — the user-driven replan surface ("only the user can
//     add budget or replan" names it);
//   - the EXACT read-only summoner-status invocation — the render that shows
//     the exhausted budget and the extension offer must stay runnable;
//   - Write/Edit of the run's FIXED post-game journal path while
//     POST_GAME_PENDING — closure observations are owed regardless of budget;
//   - read-class Bash while OUTCOME_UNKNOWN — recovery probes must be able to
//     resolve the episode (write-class probes stay budget-gated);
//   - summoner:probe-submit while OUTCOME_UNKNOWN (T19) — the resolution
//     channel of the recovery episode: an exhausted run must still be able to
//     carry its unknowns to verdicts and back to a decidable state.
func budgetControlExempt(in *gateInput, run *shared.RunState, isSubmit bool) bool {
	if isSubmit {
		return true
	}
	switch {
	case in.ToolName == "AskUserQuestion" &&
		(run.Status == shared.StatusWaitUser || run.Status == shared.StatusPostGamePending):
		return true
	case in.ToolName == "Skill" && stringField(in.ToolInput, "skill") == skillChange:
		return true
	case in.ToolName == "Skill" && stringField(in.ToolInput, "skill") == skillStatus:
		// Ship review F2: the read-only status viewer is a control-plane
		// escape exactly like the Bash spellings below — an exhausted run
		// must still be able to render the budget state and the extension
		// offer it is deciding on. decide() checks this BEFORE routing, so
		// without this case Skill(summoner:status) died at the budget gate
		// while the exact-Bash form sailed through.
		return true
	case in.ToolName == "Skill" && stringField(in.ToolInput, "skill") == skillProbeSubmit &&
		run.Status == shared.StatusOutcomeUnknown:
		return true
	case in.ToolName == "Skill" && stringField(in.ToolInput, "skill") == skillReviewSubmit &&
		isReviewRecoveryForm(in):
		// Recovery-round C3: the review-submit RECOVERY form (strict
		// expected_attempt_id metadata) is the user's channel out of a wedged
		// RUNNING review — it must stay reachable with every budget exhausted
		// (adjudication §3 line 37). The plain registration form is NOT exempt.
		return true
	case in.ToolName == "Bash" && isStatusInvocation(stringField(in.ToolInput, "command")):
		return true
	case (in.ToolName == "Write" || in.ToolName == "Edit") &&
		run.Status == shared.StatusPostGamePending &&
		shared.IsJournalPath(stringField(in.ToolInput, "file_path"), run.RunID):
		return true
	case in.ToolName == "Bash" && run.Status == shared.StatusOutcomeUnknown &&
		!isWriteClassCommand(stringField(in.ToolInput, "command")):
		return true
	}
	return false
}

// decideSkill — ruling 6 (phase-submit + framework verbs) and ruling 3
// (sequence-driven phase opening for non-summoner skills).
func decideSkill(in *gateInput, run *shared.RunState, skill string) decision {
	if skill == "" {
		return deny("Summoner gate: Skill call without a skill name — cannot be verified, denying (fail-closed).")
	}

	// WAIT_USER discipline: while a checkpoint is pending, the only Skill
	// calls through are summoner:change (the explicit checkpoint entry into
	// the change loop, spec §1 增补 — it supersedes the pending checkpoint),
	// summoner:phase-submit, which owns its own WAIT_USER rejection path
	// (SubmitPhase refuses it and the rejection is traced as
	// phase_submitted.rejected), and summoner:status — the READ-ONLY viewer
	// is the re-render surface at a pending checkpoint (content feedback and
	// page navigation re-render before re-asking; blocking it leaves the
	// model no legitimate way to display what the user must decide on).
	// Every other skill waits for the decision.
	if run.Status == shared.StatusWaitUser && skill != skillChange && skill != skillPhaseSubmit && skill != skillStatus {
		return deny(fmt.Sprintf(
			"Summoner gate: Skill(%s) is blocked while the run waits for a user decision (WAIT_USER) — only summoner:change (the change-loop entry) and the read-only summoner:status may run there; present the checkpoint and wait for the decision.",
			skill))
	}

	if skill == skillChange {
		return decideChangeSkill(in, run)
	}

	if skill == skillReviewSubmit {
		return decideReviewSubmit(in, run)
	}

	if skill == skillProbeSubmit {
		return decideProbeSubmit(in, run)
	}

	if skill == skillPhaseSubmit {
		args, ok := in.ToolInput["args"].(string)
		if !ok {
			return denySubmit("", "Summoner gate: summoner:phase-submit requires args — a JSON string carrying the phase submission (phase_id, artifacts, criteria, check refs).")
		}
		var sub shared.PhaseSubmission
		if err := json.Unmarshal([]byte(args), &sub); err != nil {
			return denySubmit("", fmt.Sprintf("Summoner gate: summoner:phase-submit args is not valid JSON: %v", err))
		}
		if sub.PhaseID == "" {
			return denySubmit("", "Summoner gate: summoner:phase-submit payload requires phase_id.")
		}
		// 0.7.0 D1: the structured conclusion is part of the payload contract.
		// The outcome enum must be legal, and a diagnosis must state its
		// conclusion and how it was reached — denied before any effect.
		if reason := shared.ValidateSubmissionConclusion(&sub); reason != "" {
			return denySubmit(sub.PhaseID, "Summoner gate: summoner:phase-submit "+reason)
		}
		// T17 standalone-review scope freeze (fix-round m3: EVERY phase of the
		// route, not just the entry): on the standalone review/ship routes any
		// accepted submission freezes criteria into the acceptance view, so
		// every phase submission is machine-checked against the effective
		// verification_spec — a later phase cannot launder scope-foreign
		// criteria into the view a [v] would force into every envelope.
		if standaloneReviewRoute(run.Workflow) {
			if reason := checkReviewScopeCriteria(sub.CriteriaIDs); reason != "" {
				return denySubmit(sub.PhaseID, reason)
			}
		}
		// T19: check refs — every ref must name a registered check the ladder
		// can actually credit, and the submission must not carry the manifest
		// itself (definitions inside reviewed artifacts get no mechanical
		// authority). L1 framework refs come back as defs to run in-gate; L2
		// project refs must already have a PASSING observed result under the
		// CURRENT definition digest.
		l1, reason := validateCheckRefs(run, &sub)
		if reason != "" {
			return denySubmit(sub.PhaseID, reason)
		}
		// Final-fix wave C1: build the hook-verified semantic staging —
		// hook-hashed CURRENT artifact digests (producer digests are advisory
		// and replaced), planned scope for not-yet-existing paths, COMPLETE
		// criterion meaning (proposal merged with shipped defaults plus
		// manifest additions on review-phase workflows), frozen check defs
		// with recomputed digests, effective scope. A shape-invalid proposal
		// or a divergent redefinition of a shipped/manifest criterion is a
		// denial BEFORE any effect.
		staged, reason := stagePhaseSemantics(run, &sub, l1)
		if reason != "" {
			return denySubmit(sub.PhaseID, reason)
		}
		eff := &effects{submit: &sub, staged: staged, l1Checks: l1}
		// Ruling 3 fallback: with no open phase, auto-open the payload's
		// phase iff it is the next expected one (protects skill-less phases,
		// e.g. a read-only P0 with no Skill call). Only attempted where
		// OpenPhase is legal (ACTIVE, no change in flight) so a rejection
		// always comes from SubmitPhase — and is traced as
		// phase_submitted.rejected. Out-of-order ids get no auto-open.
		if run.Status == shared.StatusActive && run.Change == nil && openPhaseOf(run) == nil {
			if next := shared.NextExpectedPhase(run); next != "" && next == sub.PhaseID {
				eff.openPhase = sub.PhaseID
			}
		}
		return allow(eff)
	}

	if strings.HasPrefix(skill, "summoner:") {
		// Framework verbs other than phase-submit: recorded, never phase-opening.
		return allow(nil)
	}

	// Non-summoner skill: a phase-opening candidate when the sequence allows
	// an open (ACTIVE, no change in flight, nothing open, nothing owed).
	eff := &effects{}
	if run.Status == shared.StatusActive && run.Change == nil &&
		openPhaseOf(run) == nil && run.CurrentCheckpoint() == nil {
		if next := shared.NextExpectedPhase(run); next != "" {
			eff.openPhase = next
		}
	}
	return allow(eff)
}

// --- summoner:change payload (strict decode; T10) -----------------------------

// changeOpenArgs / changeApplyArgs are the per-subcommand strict payload
// shapes (unknown fields are rejected — the same fail-closed principle as the
// known-tool shape validation). position is a POINTER: `open` requires the
// object present (null/absent both fail).
type changeOpenArgs struct {
	Subcommand string              `json:"subcommand"`
	Note       string              `json:"note"`
	Position   *shared.DevPosition `json:"position"`
}

type changeApplyArgs struct {
	Subcommand string   `json:"subcommand"`
	Note       string   `json:"note"`
	Changed    []string `json:"changed"`
	Affected   []string `json:"affected"`
}

// strictUnmarshal decodes bytes into v rejecting unknown fields — a malformed
// payload for a KNOWN tool is a fail-closed deny, never an observe-and-allow.
func strictUnmarshal(data []byte, v interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// decideChangeSkill maps Skill(summoner:change) onto the change loop: `open`
// → OpenRequirementChangeAt (legal from ACTIVE and WAIT_USER — the explicit
// checkpoint entry); `apply` → ApplyRequirementDelta (legal only in
// CHANGE_OPEN; the reducer is the authority). Payload-shape failures and
// illegal states are exit-2 denials ONLY — untraced (standing ruling:
// non-phase-submit denials are untraced). The budget check already ran in
// decide(), before any counting.
func decideChangeSkill(in *gateInput, run *shared.RunState) decision {
	args, ok := in.ToolInput["args"].(string)
	if !ok {
		return deny("Summoner gate: summoner:change requires args — a JSON string carrying {subcommand: open|apply, …} (skills/change/SKILL.md).")
	}
	// One JSON object first (validates the whole string), then a strict
	// per-subcommand decode so unknown fields fail closed per shape.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(args), &probe); err != nil {
		return deny(fmt.Sprintf("Summoner gate: summoner:change args is not valid JSON: %v", err))
	}
	var sub string
	if raw, ok := probe["subcommand"]; ok {
		if err := json.Unmarshal(raw, &sub); err != nil {
			return deny("Summoner gate: summoner:change subcommand must be a string (open|apply).")
		}
	}
	switch sub {
	case "open":
		var a changeOpenArgs
		if err := strictUnmarshal([]byte(args), &a); err != nil {
			return deny(fmt.Sprintf("Summoner gate: summoner:change open payload is malformed (unknown or mistyped field): %v", err))
		}
		if a.Note == "" {
			return deny("Summoner gate: summoner:change open requires note — what the user wants changed.")
		}
		if a.Position == nil {
			return deny("Summoner gate: summoner:change open requires position — {current_task, test_progress, pending_verifications} (the development position to freeze).")
		}
		// Status/change-open legality mirrors the reducer (belt: the reducer
		// re-validates inside its commit; a mismatch denies before any trace).
		if run.Status != shared.StatusActive && run.Status != shared.StatusWaitUser {
			return deny(fmt.Sprintf("Summoner gate: summoner:change open requires ACTIVE or WAIT_USER, status is %s.", run.Status))
		}
		if run.Change != nil {
			return deny("Summoner gate: summoner:change open refused — a requirement change is already open (apply its delta, then close at the confirm checkpoint).")
		}
		return allow(&effects{changeOpen: &changeOpenEffect{note: a.Note, position: *a.Position}})
	case "apply":
		var a changeApplyArgs
		if err := strictUnmarshal([]byte(args), &a); err != nil {
			return deny(fmt.Sprintf("Summoner gate: summoner:change apply payload is malformed (unknown or mistyped field): %v", err))
		}
		if a.Note == "" {
			return deny("Summoner gate: summoner:change apply requires note — the delta statement (what changed this time).")
		}
		if run.Change == nil {
			return deny("Summoner gate: summoner:change apply refused — no requirement change open (CHANGE_OPEN); open the change loop first.")
		}
		return allow(&effects{changeApply: &shared.RequirementDelta{Note: a.Note, Changed: a.Changed, Affected: a.Affected}})
	default:
		return deny(fmt.Sprintf("Summoner gate: summoner:change subcommand must be open or apply, got %q.", sub))
	}
}

// isReviewRecoveryForm reports whether a Skill(summoner:review-submit) call
// carries the RECOVERY form: strict args decoding whose envelope includes a
// non-empty expected_attempt_id (recovery round C3). Undecodable args are not
// the recovery form — the ordinary registration path reports them.
func isReviewRecoveryForm(in *gateInput) bool {
	args, ok := in.ToolInput["args"].(string)
	if !ok {
		return false
	}
	env, err := shared.DecodeEnvelope([]byte(args))
	return err == nil && env.ExpectedAttemptID != ""
}

// decideReviewSubmit maps Skill(summoner:review-submit) onto the envelope
// channel (T17): decode the args strictly, validate the envelope against the
// run's frozen acceptance view minus recorded drops with the project manifest's
// check registry, and hand the envelope to apply() for the reducer-first
// registration (RequestReview → review.prepared). Legal ONLY in
// REVIEW_PENDING; every payload-shape or validation failure is an exit-2
// denial ONLY — untraced (standing ruling: non-phase-submit denials are
// untraced). The WAIT_USER discipline in decideSkill already refuses the
// channel while a checkpoint is pending.
//
// Recovery round C3: the args may instead carry the RECOVERY form (identity
// triple + strict expected_attempt_id) — the wedged-RUNNING-attempt recovery
// operation (budget-exempt via budgetControlExempt). A recovery never mints
// or registers: it hands RecoverReview the strict metadata and nothing else.
func decideReviewSubmit(in *gateInput, run *shared.RunState) decision {
	args, ok := in.ToolInput["args"].(string)
	if ok {
		if env, err := shared.DecodeEnvelope([]byte(args)); err == nil && env.ExpectedAttemptID != "" {
			// F6 (re-review): the recovery form is bound to THIS run — a
			// payload naming a foreign run id (a replayed form from another
			// session's wedged review) never recovers here.
			if env.RunID != run.RunID {
				return deny(fmt.Sprintf(
					"Summoner gate: summoner:review-submit recovery form names run %q but this session's live run is %q — the recovery channel serves this run's own wedged review only.",
					env.RunID, run.RunID))
			}
			// Recovery form: the in-flight review's wedged attempt, nothing
			// else. The reducer owns every legality rule (status, RUNNING
			// review, attempt match); its refusal denies here untraced.
			return allow(&effects{reviewRecover: &reviewRecovery{reviewID: env.ReviewID, attemptID: env.ExpectedAttemptID}})
		}
	}
	if run.Status != shared.StatusReviewPending && !terminalDefaultReviewReady(run) {
		if reviewPhaseSkipped(run) {
			return deny("Summoner gate: summoner:review-submit refused — the review phase was explicitly skipped (UNREVIEWED), so the default terminal review is closed; [v] at a checkpoint is the on-demand path if the user wants one anyway.")
		}
		return deny(fmt.Sprintf(
			"Summoner gate: summoner:review-submit is the envelope registration channel and is legal only in REVIEW_PENDING (a [v] decision's obligation or an in-flight review) or at the terminal default position (ACTIVE immediately after the review phase's acceptance), status is %s.",
			run.Status))
	}
	if !ok {
		return deny("Summoner gate: summoner:review-submit requires args — a JSON string carrying the identity-only registration {schema_version, run_id, review_id} (skills/review-submit/SKILL.md).")
	}
	env, err := shared.DecodeEnvelope([]byte(args))
	if err != nil {
		return deny(fmt.Sprintf("Summoner gate: summoner:review-submit args is not a valid review registration: %v", err))
	}
	// Belt for the reducer's in-flight rule: a NEW review id while another
	// review is PREPARED/RUNNING is refused with the rule's own words (the
	// reducer's status gate would otherwise mask it in REVIEW_PENDING without
	// an outstanding obligation). A re-send of the IN-FLIGHT review's own
	// envelope stays idempotent (RequestReview's fast path).
	for i := range run.Reviews {
		rv := &run.Reviews[i]
		if (rv.Status == shared.ReviewPrepared || rv.Status == shared.ReviewRunning) && rv.ReviewID != env.ReviewID {
			return deny(fmt.Sprintf(
				"Summoner gate: summoner:review-submit refused — review %s is still in flight (one review at a time); re-send ITS envelope idempotently or conclude it before requesting another.",
				rv.ReviewID))
		}
	}
	// The check registry comes from the project manifest; a corrupt manifest
	// fails CLOSED — the registry must never silently degrade to empty and
	// let a mechanical criterion through unfrozen.
	var registry []shared.CheckDef
	if m, err := shared.LoadRuntimeManifest(); err != nil {
		return deny(fmt.Sprintf("Summoner gate: summoner:review-submit cannot load the project manifest: %v", err))
	} else if m != nil {
		registry = m.CheckRegistry
	}
	// C1: the registration is identity-only; the reducer MINTS the complete
	// envelope from the pinned acceptance snapshot. The mint runs here too —
	// a drift (artifacts changed since acceptance, unfreezable check
	// definitions, empty view) denies BEFORE any effect; the reducer re-mints
	// authoritatively inside its commit path.
	if minted, err := shared.MintEnvelope(run, env, registry); err != nil {
		return deny(fmt.Sprintf("Summoner gate: summoner:review-submit cannot mint the envelope from the pinned acceptance snapshot: %v", err))
	} else if err := shared.ValidateEnvelope(minted, run.AcceptanceView(), run.CriteriaDrops, registry); err != nil {
		return deny(fmt.Sprintf("Summoner gate: summoner:review-submit envelope rejected: %v", err))
	}
	return allow(&effects{reviewSubmit: &env, reviewReg: registry})
}

// terminalDefaultReviewReady reports the terminal default-review position
// (spec §1 终局⑤默认强制: the terminal independent review runs by DEFAULT,
// explicitly skippable): the run is ACTIVE, its workflow has the fixed
// "review" phase, and that phase's LATEST visit was ACCEPTED — the user's
// default CONTINUE at the review-phase checkpoint. Never true for arbitrary
// ACTIVE positions: before the review phase ran, after an explicit SKIP
// (UNREVIEWED), or after a recall revoked the acceptance, the default is
// closed and [v] (spec §4) remains the only review entry.
func terminalDefaultReviewReady(run *shared.RunState) bool {
	if run.Status != shared.StatusActive || !workflowHasReviewPhase(run.Workflow) {
		return false
	}
	v := latestReviewPhaseVisit(run)
	return v != nil && v.Outcome == "accepted"
}

// reviewPhaseSkipped reports the explicit-skip closure: the workflow has a
// review phase and its latest visit was settled as skipped (spec §1: 可显式
// 跳过 → UNREVIEWED — the artifacts carry UNREVIEWED, and the default
// terminal review does not run).
func reviewPhaseSkipped(run *shared.RunState) bool {
	if !workflowHasReviewPhase(run.Workflow) {
		return false
	}
	v := latestReviewPhaseVisit(run)
	return v != nil && v.Outcome == "skipped"
}

// workflowHasReviewPhase reports whether the workflow's fixed sequence
// contains the "review" phase (dev routes embed it terminally; the standalone
// review route is context+review; ship embeds it mid-route; ops/release have
// none and can never take the default branch).
func workflowHasReviewPhase(workflow string) bool {
	seq, err := shared.WorkflowPhases(workflow)
	if err != nil {
		return false
	}
	for _, id := range seq {
		if id == "review" {
			return true
		}
	}
	return false
}

// latestReviewPhaseVisit returns the latest visit of the "review" phase
// (recall appends fresh visits, so last-entry-wins), or nil.
func latestReviewPhaseVisit(run *shared.RunState) *shared.PhaseState {
	var last *shared.PhaseState
	for i := range run.Phases {
		if run.Phases[i].ID == "review" {
			last = &run.Phases[i]
		}
	}
	return last
}

// standaloneReviewRoute reports whether the workflow is a standalone
// review/ship route whose acceptance view is review-rubric-shaped and
// therefore scope-checked on every phase submission. The dev routes
// (fix/new/debug) embed their review phase after project-shaped work — their
// acceptance view is not review-rubric-shaped and is not constrained here.
func standaloneReviewRoute(workflow string) bool {
	return workflow == "review" || workflow == "ship"
}

// checkReviewScopeCriteria validates one standalone-review scope submission
// against the effective verification_spec: the framework default full-rubric
// set PLUS the project manifest's declared additions (a manifest extends the
// default axes, never replaces them — references/manifest-spec.md). A missing
// manifest is the no-manifest flow (defaults only); an unparseable one fails
// CLOSED. Returns "" when every criterion id is in the effective set.
func checkReviewScopeCriteria(ids []string) string {
	allowed := shared.DefaultReviewCriteriaIDs()
	m, err := shared.LoadRuntimeManifest()
	if err != nil {
		return fmt.Sprintf("Summoner gate: summoner:phase-submit cannot verify the review scope — the project manifest is unparseable (fail-closed): %v", err)
	}
	if m != nil {
		allowed = append(allowed, m.ReviewCriteriaIDs...)
	}
	for _, id := range ids {
		known := false
		for _, a := range allowed {
			if a == id {
				known = true
				break
			}
		}
		if !known {
			return fmt.Sprintf(
				"Summoner gate: summoner:phase-submit criterion %q is not in the effective verification_spec (the framework default full-rubric axes plus the manifest's declared additions) — a standalone review may only freeze criteria the rubric can verify (references/manifest-spec.md).",
				id)
		}
	}
	return ""
}

// --- final-fix wave C1: hook-verified semantic staging ------------------------------

// stagePhaseSemantics builds the StagedSnapshot of one phase submission.
// Hooks validate and normalize everything; the model's proposal is input,
// never authority:
//
//   - artifacts: every EXISTING path is re-hashed from CURRENT bytes (a
//     producer-supplied digest is advisory and discarded); a path that does
//     not exist yet moves to PlannedScope — the checkpoint pins real bytes
//     only, and planned scope stays visibly future;
//   - criterion meaning: every proposed criterion needs an id, a legal method
//     and non-empty meaning (goal or expected); mechanical criteria must cite
//     a check id; a proposal that REDEFINES a shipped default or
//     manifest-declared criterion with different meaning is denied — an
//     existing criterion's meaning changes only through the approved
//     change-confirm mechanism;
//   - defaults merge: on a workflow WITH a review phase (fix/new/review/ship;
//     controller QA-2 — ops/release mint no review debt) the COMPLETE shipped
//     default criteria plus the manifest's verification_spec additions join
//     the staged set, so even a no-manifest or id-only submission reviews
//     against complete semantics;
//   - check defs: the submission's admitted L1 defs freeze with RECOMPUTED
//     digests and check_id@digest scope entries.
//
// Returns ("", reason) on the first violation.
func stagePhaseSemantics(run *shared.RunState, sub *shared.PhaseSubmission, l1Defs []shared.CheckDef) (*shared.StagedSnapshot, string) {
	staged := &shared.StagedSnapshot{}
	for _, a := range sub.Artifacts {
		if a.Path == "" {
			return nil, "Summoner gate: summoner:phase-submit artifact path is empty — an artifact ref names a file."
		}
		digest, err := shared.HashArtifactFile(a.Path)
		if err != nil {
			// Not readable now: planned scope, never a frozen artifact version.
			planned := false
			for _, p := range staged.PlannedScope {
				if p == a.Path {
					planned = true
					break
				}
			}
			if !planned {
				staged.PlannedScope = append(staged.PlannedScope, a.Path)
			}
			continue
		}
		ref := shared.ArtifactRef{Path: a.Path, Digest: digest, Kind: a.Kind}
		staged.ArtifactSet = append(staged.ArtifactSet, ref)
	}
	// The submission's artifact list becomes the HOOK-hashed existing set (the
	// checkpoint's artifact versions pin real bytes; planned paths ride the
	// snapshot's PlannedScope instead).
	sub.Artifacts = append([]shared.ArtifactRef(nil), staged.ArtifactSet...)

	proposed := make(map[string]shared.Criterion, len(sub.Criteria))
	for _, c := range sub.Criteria {
		if c.ID == "" {
			return nil, "Summoner gate: summoner:phase-submit proposed criterion carries no id."
		}
		if _, dup := proposed[c.ID]; dup {
			return nil, fmt.Sprintf("Summoner gate: summoner:phase-submit proposed criterion %q twice — criterion ids are unique.", c.ID)
		}
		switch c.Method {
		case shared.MethodMechanical, shared.MethodJudgment:
		default:
			return nil, fmt.Sprintf("Summoner gate: summoner:phase-submit proposed criterion %q method %q must be mechanical or judgment.", c.ID, c.Method)
		}
		if c.Method == shared.MethodMechanical && c.CheckID == "" {
			return nil, fmt.Sprintf("Summoner gate: summoner:phase-submit mechanical criterion %q must cite a registered check_id.", c.ID)
		}
		if c.Goal == "" && c.Expected == "" && c.Rubric == "" {
			return nil, fmt.Sprintf("Summoner gate: summoner:phase-submit proposed criterion %q carries no meaning (goal/expected/rubric all empty) — propose the criterion as meaning, not just a name.", c.ID)
		}
		proposed[c.ID] = c
		staged.Criteria = append(staged.Criteria, c)
	}

	if shared.WorkflowHasReviewPhase(run.Workflow) {
		merge := func(c shared.Criterion, source string) string {
			if p, clash := proposed[c.ID]; clash {
				if criterionMeaningDiffers(p, c) {
					return fmt.Sprintf(
						"Summoner gate: summoner:phase-submit criterion %q redefines the %s declared meaning — existing-criterion meaning changes require the approved change-confirm mechanism (summoner:change).",
						c.ID, source)
				}
				return "" // identical re-statement: already staged
			}
			staged.Criteria = append(staged.Criteria, c)
			return ""
		}
		for _, d := range shared.DefaultReviewCriteria() {
			if reason := merge(d, "framework default"); reason != "" {
				return nil, reason
			}
		}
		m, err := shared.LoadRuntimeManifest()
		if err != nil {
			return nil, fmt.Sprintf("Summoner gate: summoner:phase-submit cannot stage review semantics — the project manifest is unparseable (fail-closed): %v", err)
		}
		if m != nil {
			for _, mc := range m.ReviewCriteria {
				if reason := merge(mc, "manifest verification_spec"); reason != "" {
					return nil, reason
				}
			}
		}
	}

	for _, def := range l1Defs {
		frozen := def
		frozen.Digest = shared.CheckDefDigest(def)
		staged.CheckDefs = append(staged.CheckDefs, frozen)
		staged.CheckScope.CheckIDs = append(staged.CheckScope.CheckIDs, frozen.CheckID+"@"+frozen.Digest)
	}
	staged.CheckScope.Readable = []string{shared.ProjectDir()}
	staged.CheckScope.OutputSchema = "review-result/v1"
	staged.Requirements = append([]shared.ProposedRequirement(nil), sub.Requirements...)
	return staged, ""
}

// criterionMeaningDiffers reports whether two criteria with the same id carry
// different MEANING (any content field). Id equality alone is not identity.
func criterionMeaningDiffers(a, b shared.Criterion) bool {
	return a.Goal != b.Goal || a.Expected != b.Expected || a.Scope != b.Scope ||
		a.Method != b.Method || a.Required != b.Required || a.CheckID != b.CheckID || a.Rubric != b.Rubric
}

// --- T19: check refs (the ladder at submit) ------------------------------------

// validateCheckRefs vets a submission's CheckRefs against the project registry
// and the run's observed results (T19; spec §3 check registry — a producer's
// citation never manufactures credit):
//
//   - the submission's own artifacts must not include summoner.yaml — a check
//     definition inside the artifacts under review carries no mechanical
//     authority (the ref would be verified against a definition the producer
//     could still edit);
//   - every ref must parse (check://<id>[/<pointer>]) and name a REGISTERED
//     check; duplicate ids collapse to one evaluation;
//   - trust framework (L1): the definition's command must be one of the three
//     shipped adapter ops — the defs come back for apply() to execute
//     in-gate (bounded, never exec'd);
//   - trust project (L2): the newest observed check.result for that id must
//     EXIST, be a PASS, and carry the CURRENT recomputed definition digest —
//     a stale digest (definition changed since) or a missing/blocked/failed
//     result gives the citation no credit;
//   - network-class project checks (L3) can never earn credit: refuse the ref
//     outright (they only ever produce BLOCKED l3_unsupported).
//
// A missing manifest is the no-manifest flow: any check ref is then unknown
// and refused. An unparseable manifest fails CLOSED.
func validateCheckRefs(run *shared.RunState, sub *shared.PhaseSubmission) ([]shared.CheckDef, string) {
	if len(sub.CheckRefs) == 0 {
		return nil, ""
	}
	for _, a := range sub.Artifacts {
		if a.Path == "summoner.yaml" || strings.HasSuffix(a.Path, "/summoner.yaml") {
			return nil, "Summoner gate: summoner:phase-submit check refs are refused — the submission's artifacts include summoner.yaml, and check definitions inside the artifacts under review carry no mechanical authority (definitions must come from the reviewed-and-committed manifest, not the submission)."
		}
	}
	m, err := shared.LoadRuntimeManifest()
	if err != nil {
		return nil, fmt.Sprintf("Summoner gate: summoner:phase-submit cannot verify check refs — the project manifest is unreadable (fail-closed): %v", err)
	}
	if m == nil {
		return nil, "Summoner gate: summoner:phase-submit check refs are refused — this project declares no summoner.yaml check_registry, so no mechanical check citation can be verified."
	}
	byID := make(map[string]shared.CheckDef, len(m.CheckRegistry))
	for _, def := range m.CheckRegistry {
		byID[def.CheckID] = def
	}
	var l1 []shared.CheckDef
	seen := make(map[string]bool, len(sub.CheckRefs))
	var evs []shared.TraceEvent
	haveEvents := false
	for _, ref := range sub.CheckRefs {
		id, perr := shared.ParseCheckRef(ref)
		if perr != nil {
			return nil, fmt.Sprintf("Summoner gate: summoner:phase-submit check ref %q is malformed: %v", ref, perr)
		}
		if seen[id] {
			continue // duplicate citation: one evaluation per check id
		}
		seen[id] = true
		def, ok := byID[id]
		if !ok {
			return nil, fmt.Sprintf("Summoner gate: summoner:phase-submit check ref %q names no check in the project's check_registry — a citation can only reference registered checks.", ref)
		}
		switch def.Trust {
		case "framework":
			switch def.Command {
			case shared.CheckOpFileContains, shared.CheckOpDigestMatch, shared.CheckOpGrepCount:
			default:
				return nil, fmt.Sprintf(
					"Summoner gate: summoner:phase-submit check ref %q names a framework check whose command %q is not a shipped L1 adapter op (%s, %s, %s) — declaring framework trust never promotes an arbitrary command to L1.",
					ref, def.Command, shared.CheckOpFileContains, shared.CheckOpDigestMatch, shared.CheckOpGrepCount)
			}
			l1 = append(l1, def)
		case "project":
			if shared.IsNetworkCheckCommand(def) {
				return nil, fmt.Sprintf("Summoner gate: summoner:phase-submit check ref %q names the network-class check %s — the ladder refuses to run network checks (L3, BLOCKED l3_unsupported), so the citation can never earn credit; drop the ref.", ref, def.CheckID)
			}
			if !haveEvents {
				evs, err = shared.LoadRunEvents(run.RunID)
				if err != nil {
					return nil, fmt.Sprintf("Summoner gate: summoner:phase-submit cannot read the run's trace to verify check ref %q (fail-closed): %v", ref, err)
				}
				haveEvents = true
			}
			res, found := shared.NewestCheckResult(evs, def.CheckID)
			if !found {
				return nil, fmt.Sprintf(
					"Summoner gate: summoner:phase-submit check ref %q has no observed check.result for %s — run the registered check line first (it needs a paired in-band sandbox probe), then cite it.",
					ref, def.CheckID)
			}
			if status, _ := res.Payload["status"].(string); status != shared.CheckStatusPass {
				return nil, fmt.Sprintf(
					"Summoner gate: summoner:phase-submit check ref %q cannot take credit from %s's newest result (%s) — only a passing, current-definition L2 execution backs a citation.",
					ref, def.CheckID, status)
			}
			if digest, _ := res.Payload["digest"].(string); digest != shared.CheckDefDigest(def) {
				return nil, fmt.Sprintf(
					"Summoner gate: summoner:phase-submit check ref %q is stale — %s's newest passing result ran an older definition (digest mismatch); re-run the check under the current registry definition.",
					ref, def.CheckID)
			}
		default:
			return nil, fmt.Sprintf("Summoner gate: summoner:phase-submit check ref %q names check %s with unknown trust tier %q (want framework|project).", ref, def.CheckID, def.Trust)
		}
	}
	return l1, ""
}

// --- T19: probe-submit (OUTCOME_UNKNOWN resolution channel) ---------------------

// probeSubmitArgs is the strict payload of summoner:probe-submit (recovery
// round I3): one OBSERVED probe per unknown, positionally joined, equal
// lengths — a single probe never resolves two unknowns, and an unresolved
// unknown is never resolved without its own probe. There is NO outcome field
// on the wire: the PostToolUse matcher computes every verdict from the
// probe's observed stdout and records it on the probe's tool.result; this
// submit only NAMES the pairs.
type probeSubmitArgs struct {
	Op                string   `json:"op"`
	UnknownToolUseIDs []string `json:"unknown_tool_use_ids"`
	ProbeToolUseIDs   []string `json:"probe_tool_use_ids"`
}

// recordedProbeVerdict reads the matcher's verdict off the probe's recorded
// tool.result (posttooluse writes payload probe_match). Absent — the result
// never landed or carried no match — returns "": a lost observation resolves
// indeterminate, never a guess.
func recordedProbeVerdict(evs []shared.TraceEvent, probeID string) string {
	for _, ev := range evs {
		if ev.Type != shared.EventToolResult || ev.ToolUseID != probeID {
			continue
		}
		m, _ := ev.Payload["probe_match"].(map[string]interface{})
		if m == nil {
			return ""
		}
		v, _ := m["verdict"].(string)
		return v
	}
	return ""
}

// decideProbeSubmit validates the ENTIRE resolution batch before any partial
// transition (T19; spec §5 — no auto-replay, every unknown resolves through
// its own observed probe):
//
//   - legal only while OUTCOME_UNKNOWN (the WAIT_USER discipline in
//     decideSkill already blocks it at checkpoints);
//   - strict shape: op "resolve", equal-length non-empty id arrays with no
//     duplicates inside the batch — and NO outcome field (the strict decode
//     rejects it; verdicts belong to the matcher, never the wire);
//   - every unknown id must be a recorded entry of THIS run: an unresolved
//     entry takes the verdict the matcher RECORDED on its probe's result
//     (absent ⇒ indeterminate — a lost observation); an already-resolved
//     entry accepts only the IDENTICAL (verdict, probe) pair as an idempotent
//     replay — anything else is rejected;
//   - a SUPPORTED declaration (exact probe command exists) resolves only
//     through the probe that bound its ticket: the probe's started must carry
//     probe_of == the unknown AND the exact command byte-for-byte. An
//     unsupported/absent declaration imposes look-discipline only
//     (shared.ProbeEvidence) and resolves indeterminate REGARDLESS of what
//     any recorded match says;
//   - every probe id must be UNUSED as a receipt anywhere in the run (stale
//     or reused probes are rejected) and must validate as evidence via the
//     trace join: a read-class BASH call with an observed result, recorded
//     AFTER the unknown's marking event, not failed (shared.ProbeEvidence —
//     the join is scoped to this run's trace, so foreign run/session ids
//     never validate).
//
// Failures are exit-2 denials ONLY — untraced (standing ruling: non-phase-
// submit denials are untraced).
func decideProbeSubmit(in *gateInput, run *shared.RunState) decision {
	args, ok := in.ToolInput["args"].(string)
	if !ok {
		return deny("Summoner gate: summoner:probe-submit requires args — a JSON string carrying {op:\"resolve\", unknown_tool_use_ids[], probe_tool_use_ids[]} (skills/probe-submit/SKILL.md). The outcome is NOT supplied: the PostToolUse matcher records it from the probe's observed stdout.")
	}
	var a probeSubmitArgs
	if err := strictUnmarshal([]byte(args), &a); err != nil {
		return deny(fmt.Sprintf(
			"Summoner gate: summoner:probe-submit args is malformed (unknown or mistyped field): %v — the resolution form is {op:\"resolve\", unknown_tool_use_ids[], probe_tool_use_ids[]}; outcomes are no longer accepted on the wire, the PostToolUse matcher computes every verdict from the observed stdout.",
			err))
	}
	if a.Op != "resolve" {
		return deny(fmt.Sprintf(
			"Summoner gate: summoner:probe-submit requires op \"resolve\" (the only operation; verdicts come from the PostToolUse matcher, never the wire), got %q.",
			a.Op))
	}
	if len(a.UnknownToolUseIDs) == 0 {
		return deny("Summoner gate: summoner:probe-submit requires at least one unknown_tool_use_ids entry.")
	}
	if len(a.UnknownToolUseIDs) != len(a.ProbeToolUseIDs) {
		return deny(fmt.Sprintf(
			"Summoner gate: summoner:probe-submit requires probe_tool_use_ids to match unknown_tool_use_ids one-for-one (one OBSERVED probe per unknown), got %d unknowns and %d probes.",
			len(a.UnknownToolUseIDs), len(a.ProbeToolUseIDs)))
	}
	seenUnknown := make(map[string]bool, len(a.UnknownToolUseIDs))
	seenProbe := make(map[string]bool, len(a.ProbeToolUseIDs))
	for _, id := range a.UnknownToolUseIDs {
		if seenUnknown[id] {
			return deny(fmt.Sprintf("Summoner gate: summoner:probe-submit lists unknown %s twice — each entry resolves once.", id))
		}
		seenUnknown[id] = true
	}
	for _, id := range a.ProbeToolUseIDs {
		if seenProbe[id] {
			return deny(fmt.Sprintf("Summoner gate: summoner:probe-submit reuses probe %s inside the batch — one probe is evidence for exactly one unknown.", id))
		}
		seenProbe[id] = true
	}
	// The verdicts live in the trace (the matcher's probe_match records), so
	// every path — replay included — needs it. Unreadable trace: fail closed.
	evs, err := shared.LoadRunEvents(run.RunID)
	if err != nil {
		return deny(fmt.Sprintf("Summoner gate: summoner:probe-submit cannot read the run's trace to validate the probes — denying (fail-closed): %v", err))
	}
	entryByID := make(map[string]*shared.OutcomeUnknown, len(run.OutcomeUnknowns))
	for i := range run.OutcomeUnknowns {
		entryByID[run.OutcomeUnknowns[i].ToolUseID] = &run.OutcomeUnknowns[i]
	}
	// Replay-first classification (the RecordDecision precedent): an entry
	// already resolved with the IDENTICAL (verdict, probe) pair is an
	// idempotent no-op whose legality must not depend on the CURRENT status —
	// the original resolution may have already moved the run on. Any fresh
	// resolution in the batch re-imposes the OUTCOME_UNKNOWN requirement.
	eff := &probeSubmitEffect{}
	fresh := false
	for i, unknownID := range a.UnknownToolUseIDs {
		probeID := a.ProbeToolUseIDs[i]
		entry, known := entryByID[unknownID]
		if !known {
			return deny(fmt.Sprintf(
				"Summoner gate: summoner:probe-submit names unknown %q — no such OUTCOME_UNKNOWN entry in this run (foreign or invented ids never resolve).",
				unknownID))
		}
		verdict := recordedProbeVerdict(evs, probeID)
		if verdict != "" && verdict != shared.OutcomeAccepted && verdict != shared.OutcomeFailed && verdict != shared.OutcomeIndeterminate {
			verdict = "" // a corrupted/foreign record is no verdict at all
		}
		if entry.Resolved {
			if entry.Outcome == verdict && entry.ProbeToolUseID == probeID {
				eff.pairs = append(eff.pairs, probePair{unknownID: unknownID, probeID: probeID, verdict: verdict, replay: true})
				continue // identical re-delivery: idempotent no-op
			}
			return deny(fmt.Sprintf(
				"Summoner gate: summoner:probe-submit cannot re-resolve %s — it already resolved as %s via probe %s (a conflicting or probe-swapped replay is rejected).",
				unknownID, entry.Outcome, entry.ProbeToolUseID))
		}
		// Fresh resolution: an absent matcher record is a LOST observation —
		// indeterminate, never a guess and never another free probe.
		if verdict == "" {
			verdict = shared.OutcomeIndeterminate
		}
		// An unsupported/absent declaration resolves indeterminate REGARDLESS
		// of what any recorded match says — the reducer belt coerces too;
		// defense in depth.
		if entry.MutationDecl.ExactProbeCommand() == "" {
			verdict = shared.OutcomeIndeterminate
		}
		fresh = true
		eff.pairs = append(eff.pairs, probePair{unknownID: unknownID, probeID: probeID, verdict: verdict})
	}
	if fresh && run.Status != shared.StatusOutcomeUnknown {
		return deny(fmt.Sprintf(
			"Summoner gate: summoner:probe-submit is the OUTCOME_UNKNOWN resolution channel and is legal only in OUTCOME_UNKNOWN, status is %s.",
			run.Status))
	}
	if !fresh {
		return allow(&effects{probeSubmit: eff}) // pure replay: nothing to re-validate
	}
	for _, pair := range eff.pairs {
		if pair.replay {
			continue
		}
		// The probe must be unused anywhere in the run: a receipt is one
		// unknown's evidence, never a reusable token.
		for j := range run.OutcomeUnknowns {
			if run.OutcomeUnknowns[j].ProbeToolUseID == pair.probeID {
				return deny(fmt.Sprintf(
					"Summoner gate: summoner:probe-submit probe %s is stale — it is already the receipt of unknown %s; every unknown resolves through its own fresh probe.",
					pair.probeID, run.OutcomeUnknowns[j].ToolUseID))
			}
		}
		markSeq, ok := shared.MarkingSeq(evs, pair.unknownID)
		if !ok {
			return deny(fmt.Sprintf(
				"Summoner gate: summoner:probe-submit unknown %s has no tool.outcome_unknown marking event in the trace — the entry and the trace disagree; refusing (fail-closed).",
				pair.unknownID))
		}
		if err := shared.ProbeEvidence(evs, pair.unknownID, pair.probeID, markSeq); err != nil {
			return deny(fmt.Sprintf(
				"Summoner gate: summoner:probe-submit probe %s is not valid evidence for unknown %s — %v (evidence is a read-class Bash call with an observed result, recorded after the unknown was marked, never failed).",
				pair.probeID, pair.unknownID, err))
		}
		// A supported declaration resolves ONLY through its bound ticket: the
		// probe's started must carry probe_of == this unknown AND the exact
		// declared command byte-for-byte (R6: an unrelated read-class call —
		// however well observed — is not this unknown's evidence).
		entry := entryByID[pair.unknownID]
		if cmd := entry.MutationDecl.ExactProbeCommand(); cmd != "" {
			started, _, _ := shared.StartedEvent(evs, pair.probeID)
			boundID, _ := started["probe_of"].(string)
			boundCmd, _ := started["command"].(string)
			if boundID != pair.unknownID || boundCmd != cmd {
				return deny(fmt.Sprintf(
					"Summoner gate: summoner:probe-submit probe %s does not bind unknown %s's ticket — a declared mutation resolves ONLY through its exact probe command (%q), admitted with probe_of == the unknown on its tool.started. Re-run the exact command (summoner-status serves it), then re-submit.",
					pair.probeID, pair.unknownID, cmd))
			}
		}
	}
	return allow(&effects{probeSubmit: eff})
}

// decideWrite — the write-eligibility matrix (ruling 4). POST_GAME_PENDING
// allows writes before anything else is considered (review I3: the emergency
// stop journal is writable even when no diagnosis was ever accepted); then
// WAIT_USER/REVIEW_PENDING/OUTCOME_UNKNOWN deny; ACTIVE/CHANGE_OPEN face only
// the diagnosis iron law. CLOSED never reaches here (not a live run).
func decideWrite(run *shared.RunState, toolName string) decision {
	switch run.Status {
	case shared.StatusPostGamePending:
		return allow(nil) // journal persistence precedes CLOSED
	case shared.StatusWaitUser:
		cp := run.CurrentCheckpoint()
		where := "its checkpoint"
		if cp != nil {
			where = "checkpoint " + cp.ID
		}
		return deny(fmt.Sprintf(
			"Summoner gate: %s is blocked — the run waits for a user decision at %s (WAIT_USER). Present the checkpoint and wait for the user.", toolName, where))
	case shared.StatusReviewPending:
		return deny(fmt.Sprintf(
			"Summoner gate: %s is blocked while a review is in flight (REVIEW_PENDING) — artifacts must not change mid-review.", toolName))
	case shared.StatusOutcomeUnknown:
		return deny(fmt.Sprintf(
			"Summoner gate: %s is blocked until the unresolved tool outcome is probed (OUTCOME_UNKNOWN) — mutating actions are never auto-replayed.", toolName))
	}
	if run.Workflow == "fix" || run.Workflow == "debug" {
		if !hasAcceptedPhase(run, "diagnosis") {
			return deny(fmt.Sprintf(
				"Summoner gate: %s is blocked — workflow %s enforces diagnosis-before-code: writes are denied until the diagnosis phase is accepted at its checkpoint (Phase 1 iron law; 诊断未被接受).", toolName, run.Workflow))
		}
	}
	return allow(nil)
}

// decideAgent — ruling 7: typed delegation only (reviewer|worker), within
// budget; a reviewer must be bound to a PREPARED review whose id the prompt
// carries. Legality mirrors StartDelegation exactly (review round 2, R1):
// reviewer only in REVIEW_PENDING, worker only in ACTIVE — pre-checked here
// so an illegal delegation denies BEFORE any intent event is appended.
// Fix-round C1: production plugin agents invoke as <plugin>:<name>, so the
// framework's types also arrive as `summoner:reviewer` / `summoner:worker`;
// both spellings are admitted (NormalizeAgentType strips exactly the
// framework's own plugin prefix) and effects carry the canonical short form.
func decideAgent(in *gateInput, run *shared.RunState) decision {
	raw := stringField(in.ToolInput, "subagent_type")
	if raw == "" {
		return deny("Summoner gate: Agent call without subagent_type — the gate cannot verify the delegation (fail-closed).")
	}
	typ := shared.NormalizeAgentType(raw)
	if typ != "reviewer" && typ != "worker" {
		return deny(fmt.Sprintf(
			"Summoner gate: Agent subagent_type %q is not allowed — only reviewer or worker (production plugin spelling: summoner:reviewer / summoner:worker; fork/general-purpose delegation is refused).",
			raw))
	}
	// Same semantics as BudgetExhausted: a zero limit is unlimited.
	if run.Limits.Delegations > 0 && run.Counters.Delegations >= run.Limits.Delegations {
		return deny(fmt.Sprintf(
			"Summoner gate: delegation budget exhausted (%d/%d) — only the user can add budget.",
			run.Counters.Delegations, run.Limits.Delegations))
	}
	eff := &effects{delegation: typ}
	if typ == "reviewer" {
		if run.Status != shared.StatusReviewPending {
			return deny(fmt.Sprintf(
				"Summoner gate: reviewer delegation requires REVIEW_PENDING (a PREPARED review to run), status is %s.", run.Status))
		}
		// C1c + adjudication §2: the prompt is the EXACT launch capsule — one
		// compact JSON object the shared Go layer serialized over the stored
		// envelope, attempt and digests. The WHOLE normalized prompt must
		// equal those bytes; there is no token or payload containment, so a
		// hostile prose prompt that merely mentions the review id can never
		// bind, and any prefix/suffix around the capsule rejects.
		prompt := stringField(in.ToolInput, "prompt")
		matched := ""
		var capsuleID string
		for i := range run.Reviews {
			rv := run.Reviews[i]
			if rv.Status != shared.ReviewPrepared {
				continue
			}
			capsule, ok := shared.BuildReviewCapsule(run, rv.ReviewID, rv.ActiveAttemptID)
			if !ok {
				continue
			}
			want, err := capsule.Bytes()
			if err != nil {
				continue
			}
			if shared.NormalizePrompt(prompt) == shared.NormalizePrompt(string(want)) {
				matched = rv.ReviewID
				capsuleID = rv.ActiveAttemptID
				break
			}
		}
		if matched == "" {
			// Serve the honest recovery instruction: the exact bytes come from
			// the status view, never from model memory.
			return deny("Summoner gate: reviewer delegation requires the EXACT launch capsule as the whole prompt — run summoner-status (view:review) to obtain the exact capsule bytes for the PREPARED review's active attempt, then pass them verbatim as the Agent prompt; prose prompts, added prefixes/suffixes or partial payloads never bind (whole-message equality).")
		}
		eff.delegateRevID = matched
		eff.delegateAttID = capsuleID
	} else if run.Status != shared.StatusActive {
		return deny(fmt.Sprintf(
			"Summoner gate: worker delegation requires ACTIVE, status is %s — delegation is phase work, not a checkpoint bypass.", run.Status))
	}
	return allow(eff)
}

// decideAskUser — ruling 8 + the T16 two-turn/page contract: gated only
// while WAIT_USER, where the decision question must (1) carry the current
// checkpoint's nonce, (2) arrive on the turn AFTER the render (S1: the
// display the Stop hook binds at turn end is a precondition of
// RecordDecision — a same-turn answer is always decision_rejected), and (3)
// offer exactly ONE persisted menu page (S2: the offered label set is pinned
// to CheckpointState.MenuPages; navigation is nonce-free content feedback,
// never a question channel). Elsewhere it is ordinary Q&A: it advances
// nothing (PostToolUse records decisions only for pending checkpoints).
func decideAskUser(in *gateInput, run *shared.RunState) decision {
	if run.Status != shared.StatusWaitUser {
		return allow(nil)
	}
	cp := run.CurrentCheckpoint()
	if cp == nil {
		return deny("Summoner gate: WAIT_USER with no open checkpoint — run state is inconsistent, denying (fail-closed).")
	}
	if len(cp.MenuPages) == 0 {
		return deny(fmt.Sprintf(
			"Summoner gate: checkpoint %s carries no persisted menu projection — state predates the paged menu; re-render the checkpoint so the page set is rebuilt.",
			cp.ID))
	}
	qs, _ := in.ToolInput["questions"].([]interface{})
	var nonceQs []map[string]interface{}
	for _, q := range qs {
		m, _ := q.(map[string]interface{})
		if strings.Contains(stringField(m, "question"), cp.Nonce) {
			nonceQs = append(nonceQs, m)
		}
	}
	if len(nonceQs) == 0 {
		return deny(fmt.Sprintf(
			"Summoner gate: AskUserQuestion while WAIT_USER must carry the current checkpoint's nonce in a question text (checkpoint %s, nonce %s) — otherwise the answer cannot bind to the checkpoint.",
			cp.ID, cp.Nonce))
	}
	// S1 two-turn guard: the display binding the Stop hook writes at turn end
	// is the sole Displayed setter; without it the answer can only ever be
	// decision_rejected — deny at the door with the instruction. Since 0.7.0
	// (D2) the two turns need no USER turn between them: the Stop hook binds
	// the pasted checkpoint and hands the turn straight back with the
	// question to ask.
	if !cp.Displayed {
		return deny(fmt.Sprintf(
			"Summoner gate: the decision question cannot bind yet — checkpoint %s (nonce %s) has not been displayed. The protocol is two-turn: paste the checkpoint message (summoner-status view:checkpoint; an explanation may precede it) and END the turn — the Stop hook binds the display and immediately hands back the exact question to ask.",
			cp.ID, cp.Nonce))
	}
	// S2 offered-set pinning: every nonce-carrying question's options must
	// equal one persisted page exactly (both pages of a full menu are legal;
	// any subset, superset or foreign label is not).
	for _, m := range nonceQs {
		options, _ := m["options"].([]interface{})
		offered := make([]string, 0, len(options))
		for _, raw := range options {
			om, _ := raw.(map[string]interface{})
			offered = append(offered, stringField(om, "label"))
		}
		if cp.MenuPageFor(offered) == nil {
			return deny(fmt.Sprintf(
				"Summoner gate: the decision question's options must equal one persisted menu page of checkpoint %s exactly (the offered set matches no page) — copy ONE page's labels verbatim: page 1 is printed in the checkpoint's 请决定 section, every page in the summoner-status detail block (menu pages); more:/back: navigation is nonce-free content feedback, not an option.",
				cp.ID))
		}
	}
	// T18: budget/extend ride-along questions must equal the checkpoint's
	// persisted pointer-label pairs exactly (labels are pointers to displayed
	// numbers, never carriers of them) — validated at the door like the
	// decision page; PostToolUse re-verifies before recording.
	if _, problem := shared.ParseBudgetRideAlongs(cp, qs); problem != "" {
		return deny("Summoner gate: " + problem)
	}
	return allow(nil)
}

// apply executes the effect plan. Two orderings, by path (review I6/I8):
//
//   - submit path (summoner:phase-submit): the reducer runs FIRST — its
//     rejections consume nothing and must leave no tool.started, only
//     phase_submitted.rejected. On success the intent record (tool.started)
//     and result events (phase.started/phase.closed/checkpoint.opened) are
//     appended, then counters.
//
//   - dispatch path (everything else): intent events (tool.started,
//     delegation.started) are appended BEFORE any mutation — a trace failure
//     denies with nothing consumed (no review started, no counters spent),
//     so a retry after repair succeeds. A failure AFTER mutations began
//     (reducer Save, counter Save) denies with the mutation persisted:
//     dangling intent events are the correct OUTCOME_UNKNOWN posture.
func apply(run *shared.RunState, in *gateInput, eff *effects, errW io.Writer) error {
	appendAll := func(events []shared.TraceEvent) error {
		for _, ev := range events {
			ev.Source = gateSource
			ev.ToolUseID = in.ToolUseID
			if err := shared.AppendEvent(run.RunID, in.SessionID, ev); err != nil {
				fmt.Fprintf(errW, "Summoner gate: trace append (%s) failed — denying (fail-closed): %v\n", ev.Type, err)
				return err
			}
		}
		return nil
	}
	rejectSubmit := func(phaseID string, err error) error {
		payload := map[string]interface{}{"reason": err.Error()}
		if phaseID != "" {
			payload["phase_id"] = phaseID
		}
		if terr := shared.AppendEvent(run.RunID, run.SessionID, shared.TraceEvent{
			Type:      shared.EventPhaseSubmittedRejected,
			Source:    gateSource,
			ToolUseID: in.ToolUseID,
			Payload:   payload,
		}); terr != nil {
			fmt.Fprintf(errW, "Summoner gate: phase_submitted.rejected trace append also failed: %v\n", terr)
		}
		fmt.Fprintf(errW, "Summoner gate: summoner:phase-submit rejected — %v\n", err)
		return err
	}

	if eff != nil && eff.submit != nil {
		// --- submit path: reducer first, rejections un-intended (I8) ---
		if eff.openPhase != "" {
			if err := run.OpenPhase(eff.openPhase); err != nil {
				return rejectSubmit(eff.openPhase, err)
			}
		}
		cp, err := run.SubmitPhase(*eff.submit, eff.staged)
		if err != nil {
			return rejectSubmit(eff.submit.PhaseID, err)
		}
		checkFailed := false // one FAIL among this submission's L1 checks stops the auto-pass
		events := []shared.TraceEvent{{
			Type:    shared.EventToolStarted,
			Payload: map[string]interface{}{"tool_name": in.ToolName},
		}}
		if eff.openPhase != "" {
			events = append(events, shared.TraceEvent{
				Type:    shared.EventPhaseStarted,
				Payload: map[string]interface{}{"phase_id": eff.openPhase},
			})
		}
		events = append(events,
			shared.TraceEvent{
				Type:    shared.EventPhaseClosed,
				Payload: map[string]interface{}{"phase_id": eff.submit.PhaseID},
			},
			shared.TraceEvent{
				Type: shared.EventCheckpointOpened,
				Payload: map[string]interface{}{
					"checkpoint_id": cp.ID,
					"phase_id":      eff.submit.PhaseID,
					"nonce":         cp.Nonce,
				},
			})
		if err := appendAll(events); err != nil {
			return err // state already moved (WAIT_USER) — deny fail-closed
		}
		// T19: run the L1 framework adapters the refs named (in-gate, bounded,
		// never exec'd) and trace each outcome. A FAIL ticks the failed-check
		// budget — it does not un-submit: the checkpoint is where the user
		// sees the criteria evidence and the budget state; an honest BLOCKED
		// (unreadable file, budget cap, malformed def) ticks nothing.
		if len(eff.l1Checks) > 0 {
			l1Deadline := time.Now().Add(shared.L1AggregateBudget)
			for _, def := range eff.l1Checks {
				outcome := shared.RunL1Check(def, l1Deadline)
				payload := map[string]interface{}{
					"check_id": def.CheckID,
					"trust":    "framework",
					"tier":     "L1",
					"status":   outcome.Status,
					"digest":   shared.CheckDefDigest(def),
				}
				if outcome.Reason != "" {
					payload["reason"] = outcome.Reason
				}
				if err := appendAll([]shared.TraceEvent{{Type: shared.EventCheckResult, Payload: payload}}); err != nil {
					return err
				}
				if outcome.Status == shared.CheckStatusFail {
					checkFailed = true
					run.CountFailedCheck()
				}
			}
		}
		run.CountToolCall(in.ToolUseID)
		if err := run.Save(); err != nil {
			fmt.Fprintf(errW, "Summoner gate: persisting run state failed — denying (fail-closed): %v\n", err)
			return err
		}
		// 0.7.0 D3: LAST, after the counters — the submit call sits outside
		// any visit bucket here exactly as it does before a human CONTINUE,
		// so an auto-pass never charges it to the next visit.
		if shared.AutoPassablePhase(cp.PhaseID) {
			return autoPass(run, cp, checkFailed, appendAll, errW)
		}
		return nil
	}

	// --- change path (summoner:change): reducer first, like the submit path
	// — a reducer rejection (illegal status, change already open, empty
	// position) consumes nothing, leaves NO trace (standing ruling:
	// non-phase-submit denials untraced) and denies with its reason. On
	// success the intent record and the change event are appended, then the
	// budget counter.
	if eff != nil && eff.changeOpen != nil {
		if err := run.OpenRequirementChangeAt(eff.changeOpen.note, eff.changeOpen.position); err != nil {
			fmt.Fprintf(errW, "Summoner gate: summoner:change open rejected — %v\n", err)
			return err
		}
		events := []shared.TraceEvent{
			{
				Type:    shared.EventToolStarted,
				Payload: map[string]interface{}{"tool_name": in.ToolName},
			},
			{
				Type: shared.EventChangeOpened,
				Payload: map[string]interface{}{
					"note": eff.changeOpen.note,
					"position": map[string]interface{}{
						"current_task":          eff.changeOpen.position.CurrentTask,
						"test_progress":         eff.changeOpen.position.TestProgress,
						"pending_verifications": eff.changeOpen.position.PendingVerifications,
					},
				},
			},
		}
		if err := appendAll(events); err != nil {
			return err // state already moved (CHANGE_OPEN) — deny fail-closed
		}
		run.CountToolCall(in.ToolUseID)
		if err := run.Save(); err != nil {
			fmt.Fprintf(errW, "Summoner gate: persisting run state failed — denying (fail-closed): %v\n", err)
			return err
		}
		return nil
	}
	if eff != nil && eff.changeApply != nil {
		report, err := run.ApplyRequirementDelta(*eff.changeApply)
		if err != nil {
			fmt.Fprintf(errW, "Summoner gate: summoner:change apply rejected — %v\n", err)
			return err
		}
		payload := map[string]interface{}{
			"note":                 eff.changeApply.Note,
			"requirements_version": report.RequirementsVersion,
		}
		if len(report.NewlyStale) > 0 {
			payload["newly_stale"] = report.NewlyStale
		}
		if len(report.Preserved) > 0 {
			payload["preserved"] = report.Preserved
		}
		events := []shared.TraceEvent{
			{
				Type:    shared.EventToolStarted,
				Payload: map[string]interface{}{"tool_name": in.ToolName},
			},
			{
				Type:    shared.EventChangeDeltaApplied,
				Payload: payload,
			},
		}
		if err := appendAll(events); err != nil {
			return err // state already moved (WAIT_USER at the confirm checkpoint) — deny fail-closed
		}
		run.CountToolCall(in.ToolUseID)
		if err := run.Save(); err != nil {
			fmt.Fprintf(errW, "Summoner gate: persisting run state failed — denying (fail-closed): %v\n", err)
			return err
		}
		return nil
	}

	// --- review-submit RECOVERY path (recovery round C3): the wedged RUNNING
	// attempt parks at its retry checkpoint — reducer-first like every runtime
	// channel; a reducer refusal consumes nothing, leaves NO trace and denies
	// with its reason. A recovery registers NOTHING (no review.prepared) and
	// charges NOTHING (§5: recovery escapes uncharged); the only trace is the
	// admitted call's tool.started, exactly like any control-plane Skill call.
	if eff != nil && eff.reviewRecover != nil {
		if err := run.RecoverReview(eff.reviewRecover.reviewID, eff.reviewRecover.attemptID); err != nil {
			fmt.Fprintf(errW, "Summoner gate: summoner:review-submit recovery rejected — %v\n", err)
			return err
		}
		if err := appendAll([]shared.TraceEvent{{
			Type:    shared.EventToolStarted,
			Payload: map[string]interface{}{"tool_name": in.ToolName},
		}}); err != nil {
			return err // state already moved — deny fail-closed
		}
		if err := run.Save(); err != nil {
			fmt.Fprintf(errW, "Summoner gate: persisting run state failed — denying (fail-closed): %v\n", err)
			return err
		}
		return nil
	}

	// --- review-submit path (summoner:review-submit): reducer first, like the
	// submit path — a reducer rejection (illegal status, envelope/consumption
	// problems the pre-checks could not see) consumes nothing, leaves NO trace
	// (standing ruling: non-phase-submit denials untraced) and denies with its
	// reason. On success tool.started + review.prepared append (exactly one
	// review.prepared per successful call), then the budget counter.
	if eff != nil && eff.reviewSubmit != nil {
		if _, err := run.RequestReview(*eff.reviewSubmit, eff.reviewReg); err != nil {
			fmt.Fprintf(errW, "Summoner gate: summoner:review-submit rejected — %v\n", err)
			return err
		}
		// I6: review.prepared carries the COMPLETE minted semantics — every
		// active criterion with its definition, the artifact set with pinned
		// digests, the verification scope and the attempt identity — so the
		// scorers audit real production input, not a bare review id.
		prepared := map[string]interface{}{"review_id": eff.reviewSubmit.ReviewID}
		if rv := run.FindReview(eff.reviewSubmit.ReviewID); rv != nil {
			prepared["envelope_digest"] = rv.EnvelopeDigest
			prepared["artifact_digest"] = rv.ArtifactDigest
			crits := make([]interface{}, 0, len(rv.Envelope.ExitCriteria))
			for _, c := range rv.Envelope.ExitCriteria {
				crits = append(crits, map[string]interface{}{
					"id": c.ID, "goal": c.Goal, "method": string(c.Method), "required": c.Required,
				})
			}
			prepared["criteria"] = crits
			arts := make([]interface{}, 0, len(rv.Envelope.ArtifactSet))
			for _, a := range rv.Envelope.ArtifactSet {
				arts = append(arts, map[string]interface{}{"path": a.Path, "digest": a.Digest, "kind": a.Kind})
			}
			prepared["artifacts"] = arts
			if len(rv.Envelope.CheckDefs) > 0 {
				defs := make([]interface{}, 0, len(rv.Envelope.CheckDefs))
				for _, d := range rv.Envelope.CheckDefs {
					defs = append(defs, map[string]interface{}{"check_id": d.CheckID, "digest": shared.CheckDefDigest(d), "trust": d.Trust})
				}
				prepared["check_defs"] = defs
			}
			prepared["scope"] = map[string]interface{}{
				"readable":      rv.Envelope.VerificationScope.Readable,
				"check_ids":     rv.Envelope.VerificationScope.CheckIDs,
				"output_schema": rv.Envelope.VerificationScope.OutputSchema,
			}
			if rv.ActiveAttemptID != "" {
				prepared["attempt_id"] = rv.ActiveAttemptID
			}
		}
		events := []shared.TraceEvent{
			{
				Type:    shared.EventToolStarted,
				Payload: map[string]interface{}{"tool_name": in.ToolName},
			},
			{
				Type:    shared.EventReviewPrepared,
				Payload: prepared,
			},
		}
		if err := appendAll(events); err != nil {
			return err // state already moved (REVIEW_PENDING) — deny fail-closed
		}
		run.CountToolCall(in.ToolUseID)
		if err := run.Save(); err != nil {
			fmt.Fprintf(errW, "Summoner gate: persisting run state failed — denying (fail-closed): %v\n", err)
			return err
		}
		return nil
	}

	// --- probe-submit path (summoner:probe-submit, T19): reducer-first like the
	// other runtime channels — decide() validated the whole batch against the
	// trace already, so a reducer rejection here (status race, conflicting
	// replay) consumes nothing, leaves NO trace (standing ruling: non-phase-
	// submit denials untraced) and denies with its reason. Each entry resolves
	// with its OWN durable probe receipt; only entries THIS call newly resolved
	// get a tool.resolved event (a re-delivered batch re-observes its replays
	// as silent no-ops). The last entry of the episode lands the restore or the
	// recovery checkpoint inside the reducer's commit.
	if eff != nil && eff.probeSubmit != nil {
		events := []shared.TraceEvent{{
			Type:    shared.EventToolStarted,
			Payload: map[string]interface{}{"tool_name": in.ToolName},
		}}
		for _, pair := range eff.probeSubmit.pairs {
			newly, err := run.ResolveOutcomeProbed(pair.unknownID, pair.verdict, pair.probeID)
			if err != nil {
				fmt.Fprintf(errW, "Summoner gate: summoner:probe-submit rejected — %v\n", err)
				return err
			}
			if newly {
				events = append(events, shared.TraceEvent{
					Type: shared.EventToolResolved,
					Payload: map[string]interface{}{
						"tool_use_id":       pair.unknownID,
						"outcome":           pair.verdict,
						"probe_tool_use_id": pair.probeID,
					},
				})
			}
		}
		if err := appendAll(events); err != nil {
			return err // resolutions already persisted — deny fail-closed
		}
		run.CountToolCall(in.ToolUseID)
		if err := run.Save(); err != nil {
			fmt.Fprintf(errW, "Summoner gate: persisting run state failed — denying (fail-closed): %v\n", err)
			return err
		}
		return nil
	}

	// --- dispatch path: intents before any mutation (I6) ---
	started := map[string]interface{}{"tool_name": in.ToolName}
	if eff != nil && eff.bashClass != nil {
		started["read_class"] = true
		// I3: the read-class admission carries its command verbatim (the
		// probe-submit binding and the posttooluse matcher join on it) and,
		// when this admission consumed an unknown's one-shot ticket, the
		// probe_of reference the matcher needs.
		if eff.bashClass.command != "" {
			started["command"] = eff.bashClass.command
		}
		if eff.bashClass.probeOf != "" {
			started["probe_of"] = eff.bashClass.probeOf
		}
		if eff.bashClass.probe {
			started["sandbox_probe"] = "v1"
		}
		if eff.bashClass.l2 != nil {
			started["l2_check"] = map[string]interface{}{
				"check_id":  eff.bashClass.l2.CheckID,
				"digest":    shared.CheckDefDigest(*eff.bashClass.l2),
				"cwd":       eff.bashClass.l2.Cwd,
				"env_allow": eff.bashClass.l2.EnvAllow,
			}
		}
	}
	// I3: an admitted Write/Edit freezes its pre-effect declaration onto the
	// intent record — the crash scan carries it onto the OUTCOME_UNKNOWN entry
	// and the mechanical matcher resolves against exactly these bytes.
	if eff != nil && eff.writeDecl != nil {
		started["mutation_decl"] = map[string]interface{}{
			"capability":   eff.writeDecl.Capability,
			"target":       eff.writeDecl.Target,
			"input_digest": eff.writeDecl.InputDigest,
			"preimage":     eff.writeDecl.Preimage,
			"expect":       eff.writeDecl.Expect,
		}
	}
	events := []shared.TraceEvent{{
		Type:    shared.EventToolStarted,
		Payload: started,
	}}
	if eff != nil && eff.delegation != "" {
		payload := map[string]interface{}{"subagent_type": eff.delegation}
		if eff.delegateRevID != "" {
			payload["review_id"] = eff.delegateRevID
		}
		// M3 (recovery round): the reviewer delegation names the ATTEMPT it
		// dispatches — the C3 ownership key the trace-side verdict join binds
		// against (review.verdict.attempt_id must equal this).
		if eff.delegateAttID != "" {
			payload["attempt_id"] = eff.delegateAttID
		}
		events = append(events, shared.TraceEvent{
			Type:    shared.EventDelegationStarted,
			Payload: payload,
		})
	}
	if err := appendAll(events); err != nil {
		return err // nothing mutated — retry after repair is clean
	}
	if eff != nil && eff.delegation != "" {
		// One reducer call, one Save: the review transition and both
		// counter consumptions persist atomically (review round 2, R1). The
		// admitted parent Agent tool_use id lands on the attempt receipt —
		// C3's ownership key for the child's initial record.
		if err := run.StartDelegation(eff.delegateRevID, eff.delegation == "reviewer", in.ToolUseID); err != nil {
			fmt.Fprintf(errW, "Summoner gate: StartDelegation failed — denying: %v\n", err)
			return err
		}
		return nil
	}
	if eff != nil && eff.openPhase != "" {
		if err := run.OpenPhase(eff.openPhase); err != nil {
			fmt.Fprintf(errW, "Summoner gate: OpenPhase(%s) failed — denying: %v\n", eff.openPhase, err)
			return err
		}
		if err := appendAll([]shared.TraceEvent{{
			Type:    shared.EventPhaseStarted,
			Payload: map[string]interface{}{"phase_id": eff.openPhase},
		}}); err != nil {
			return err
		}
	}
	run.CountToolCall(in.ToolUseID)
	if err := run.Save(); err != nil {
		fmt.Fprintf(errW, "Summoner gate: persisting run state failed — denying (fail-closed): %v\n", err)
		return err
	}
	return nil
}

// autoPass is the adaptive-checkpoint step of the submit path (0.7.0 D3): a
// confirmed, evidenced submission on a phase that needs no human judgment is
// accepted in the same call — the checkpoint record is kept (accepted by the
// framework, see shared.AutoPassCheckpoint), the run returns to ACTIVE, and
// checkpoint.auto is traced after the checkpoint's own check results.
//
// Anything that owes the user a decision simply stays a human checkpoint:
// AutoPassBlocker says why (an unclear or failed submission, no evidence, a
// FAIL among the checks, a riding extension question, an exhausted budget)
// and WAIT_USER is the default posture — the submission itself succeeded. A
// trace failure AFTER the state moved denies fail-closed, like every
// post-mutation append here.
func autoPass(run *shared.RunState, cp shared.CheckpointState, checkFailed bool, appendAll func([]shared.TraceEvent) error, errW io.Writer) error {
	if reason := shared.AutoPassBlocker(run, cp, checkFailed); reason != "" {
		fmt.Fprintf(errW, "Summoner gate: checkpoint %s stays with the user — %s\n", cp.ID, reason)
		return nil
	}
	if err := run.AutoPassCheckpoint(cp.ID, checkFailed); err != nil {
		return fmt.Errorf("Summoner gate: auto-passing checkpoint %s failed — the submission is recorded and the checkpoint stays with the user: %v", cp.ID, err)
	}
	if err := appendAll([]shared.TraceEvent{{
		Type: shared.EventCheckpointAuto,
		Payload: map[string]interface{}{
			"checkpoint_id": cp.ID,
			"phase_id":      cp.PhaseID,
			"outcome":       cp.Outcome,
		},
	}}); err != nil {
		return err // state already moved (ACTIVE) — deny fail-closed
	}
	return nil
}

// --- run-state read helpers (pure) -------------------------------------------

func stringField(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// validateShape enforces the known tool_input shapes before classification
// (review I7): during an active run, a malformed payload for a KNOWN tool is
// a fail-closed deny, never an observe-and-allow. Unknown tool names keep the
// observe-and-allow policy — the gate does not model their inputs.
func validateShape(in *gateInput) error {
	if in.ToolName == "" {
		return fmt.Errorf("tool_name is empty")
	}
	switch in.ToolName {
	case "Bash", "Edit", "Write", "NotebookEdit", "Agent", "Skill", "AskUserQuestion":
	default:
		return nil // unknown tool name: observe and allow
	}
	if in.ToolInput == nil {
		return fmt.Errorf("tool_input for %s must be an object", in.ToolName)
	}
	needString := func(key string) error {
		if _, ok := in.ToolInput[key].(string); !ok {
			return fmt.Errorf("%s requires a string %q", in.ToolName, key)
		}
		return nil
	}
	switch in.ToolName {
	case "Bash":
		return needString("command")
	case "Edit":
		if err := needString("file_path"); err != nil {
			return err
		}
		if err := needString("old_string"); err != nil {
			return err
		}
		return needString("new_string")
	case "Write":
		if err := needString("file_path"); err != nil {
			return err
		}
		return needString("content")
	case "NotebookEdit":
		if err := needString("notebook_path"); err != nil {
			return err
		}
		return needString("new_source")
	case "Agent":
		if err := needString("subagent_type"); err != nil {
			return err
		}
		return needString("prompt")
	case "Skill":
		return needString("skill")
	case "AskUserQuestion":
		qs, ok := in.ToolInput["questions"].([]interface{})
		if !ok {
			return fmt.Errorf("AskUserQuestion requires a questions array")
		}
		for i, q := range qs {
			m, ok := q.(map[string]interface{})
			if !ok {
				return fmt.Errorf("AskUserQuestion questions[%d] must be an object", i)
			}
			if _, ok := m["question"].(string); !ok {
				return fmt.Errorf("AskUserQuestion questions[%d] requires a string question", i)
			}
		}
	}
	return nil
}

func openPhaseOf(run *shared.RunState) *shared.PhaseState {
	for i := range run.Phases {
		if run.Phases[i].Open {
			return &run.Phases[i]
		}
	}
	return nil
}

func hasAcceptedPhase(run *shared.RunState, id string) bool {
	for _, p := range run.Phases {
		if p.ID == id && p.Outcome == "accepted" {
			return true
		}
	}
	return false
}
