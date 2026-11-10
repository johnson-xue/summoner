package shared

// checkrun.go — the L1/L2/L3 check trust ladder's shared cores plus the
// OUTCOME_UNKNOWN probe-evidence joins (T19; spec §3 Bash 副作用信任阶梯 +
// §5 OUTCOME_UNKNOWN 与副作用恢复). Everything here is either PURE
// (classifiers, op adapters, scans over in-memory events) or BOUNDED
// (file reads with hard caps and deadlines) — no registry command is ever
// exec'd, and no function here mutates run state or appends trace events
// (callers — the gate, posttooluse, session-start, stop — own the writes).
//
// The ladder:
//   - L1 trust:framework — one of three shipped Go adapter ops
//     (file-contains / digest-match / grep-count) executed IN-GATE at
//     summoner:phase-submit, any environment, never a project command.
//   - L2 trust:project — the registered command line runs ONLY as an exact-
//     match MODEL Bash call the gate admits, and admission requires a paired
//     in-band sandbox-effect probe (hooks/bin/summoner-sandbox-probe, whose
//     fresh exclusive write the sandbox must REFUSE) observed for that
//     execution. Point-in-time: the pairing proves the sandbox at the probe's
//     moment, not end-to-end — the TOCTOU/model-paced residual is declared in
//     the tier wording and the docs, never claimed away.
//   - L3 — network-class commands the sandbox cannot isolate: refused with an
//     honest BLOCKED l3_unsupported, never downgraded to "just run it".
//
// A hook child is never inside the sandbox, so NOTHING here can authorize L2
// by probing from a hook process — the only sandbox-effect evidence the
// ladder accepts is the in-band adapter wrapper observed in a tool result.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// --- check refs -------------------------------------------------------------------

// ParseCheckRef splits one PhaseSubmission.CheckRefs entry: the canonical form
// is `check://<check_id>` with an OPTIONAL pointer tail (`check://<id>/<any>`)
// that is documentation, never authority — the ladder joins the id to registry
// definitions and observed results, so no false credit can come from the tail.
func ParseCheckRef(ref string) (string, error) {
	const prefix = "check://"
	if !strings.HasPrefix(ref, prefix) {
		return "", fmt.Errorf("check ref %q must start with %q", ref, prefix)
	}
	rest := strings.TrimPrefix(ref, prefix)
	id := rest
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		id = rest[:i]
	}
	if id == "" || strings.ContainsAny(id, " \t@") {
		return "", fmt.Errorf("check ref %q carries no valid check id (want check://<check_id>[/<pointer>])", ref)
	}
	return id, nil
}

// --- L1 adapter ops -----------------------------------------------------------------

// The three shipped L1 adapter operations. A trust:framework registry entry
// MUST name one of these as its command; anything else is BLOCKED unexecuted
// (declaring framework trust never promotes a project command to L1 —
// references/manifest-spec.md).
const (
	CheckOpFileContains = "file-contains"
	CheckOpDigestMatch  = "digest-match"
	CheckOpGrepCount    = "grep-count"
)

// L1 bounds (panel graft: per-check deadline ≪ the 10s hook timeout AND an
// aggregate cap across every check of one submit; reads and match counts are
// hard-capped so no check can turn the gate into a file reader).
const (
	L1PerCheckBudget  = 2 * time.Second
	L1AggregateBudget = 4 * time.Second
	L1MaxFileBytes    = 4 << 20
	L1MaxMatches      = 1 << 20
)

// Check outcome statuses (check.result payload values).
const (
	CheckStatusPass    = "pass"
	CheckStatusFail    = "fail"
	CheckStatusBlocked = "blocked"
)

// Sandbox probe effects (the adapter wrapper's vocabulary).
const (
	SandboxProbeRefused  = "refused"  // the fresh write was refused → sandbox in effect
	SandboxProbeWritable = "writable" // the write succeeded → NOT sandboxed (L2 declines)
	SandboxProbeUnproven = "unproven" // unrelated I/O error → no claim either way
)

// CheckOutcome is one L1 adapter result: pass/fail from the op's predicate,
// blocked when the check could not honestly run (unknown op, malformed args,
// unreadable/oversized file, budget exhausted). BLOCKED never ticks the
// failed-check budget — it is an inability, not a failure.
type CheckOutcome struct {
	Status string
	Reason string
}

// RunL1Check executes ONE L1 adapter op in-process: bounded file read, pure
// predicate, no exec, no network. deadline is the AGGREGATE budget deadline
// for the whole submit (the caller stamps start+L1AggregateBudget); a check
// that starts past it is blocked without touching the filesystem.
func RunL1Check(def CheckDef, deadline time.Time) CheckOutcome {
	if time.Now().After(deadline) {
		return CheckOutcome{CheckStatusBlocked, "l1_aggregate_deadline_exceeded"}
	}
	switch def.Command {
	case CheckOpFileContains:
		return l1FileContains(def)
	case CheckOpDigestMatch:
		return l1DigestMatch(def)
	case CheckOpGrepCount:
		return l1GrepCount(def)
	default:
		return CheckOutcome{CheckStatusBlocked, fmt.Sprintf("l1_unknown_adapter:%s — framework checks run only the shipped adapters (%s, %s, %s)", def.Command, CheckOpFileContains, CheckOpDigestMatch, CheckOpGrepCount)}
	}
}

// l1ResolvePath resolves an op's file argument: def.Cwd (absolute, or
// project-relative) then the argument (absolute, or cwd-relative).
func l1ResolvePath(def CheckDef, p string) string {
	base := ProjectDir()
	if def.Cwd != "" {
		if filepath.IsAbs(def.Cwd) {
			base = def.Cwd
		} else {
			base = filepath.Join(base, def.Cwd)
		}
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(base, p))
}

// l1Read bounded-reads one op's file argument (whole file, hard-capped).
func l1Read(def CheckDef, args []string) ([]byte, CheckOutcome) {
	if len(args) != 1 {
		return nil, CheckOutcome{CheckStatusBlocked, fmt.Sprintf("l1_args: want 1 file argument, got %d", len(args))}
	}
	f, err := os.Open(l1ResolvePath(def, args[0]))
	if err != nil {
		return nil, CheckOutcome{CheckStatusBlocked, fmt.Sprintf("l1_unreadable: %v", err)}
	}
	defer f.Close()
	// Cap +1 so an exactly-at-cap file passes and anything larger is caught.
	buf := make([]byte, L1MaxFileBytes+1)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, CheckOutcome{CheckStatusBlocked, fmt.Sprintf("l1_unreadable: %v", err)}
	}
	if n > L1MaxFileBytes {
		return nil, CheckOutcome{CheckStatusBlocked, fmt.Sprintf("l1_read_cap: file exceeds %d bytes", L1MaxFileBytes)}
	}
	return buf[:n], CheckOutcome{}
}

func l1FileContains(def CheckDef) CheckOutcome {
	if len(def.Args) != 2 {
		return CheckOutcome{CheckStatusBlocked, fmt.Sprintf("l1_args: %s wants (path, needle), got %d args", CheckOpFileContains, len(def.Args))}
	}
	b, bad := l1Read(def, def.Args[:1])
	if bad.Status != "" {
		return bad
	}
	if strings.Contains(string(b), def.Args[1]) {
		return CheckOutcome{Status: CheckStatusPass}
	}
	return CheckOutcome{Status: CheckStatusFail, Reason: "needle absent"}
}

var l1DigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func l1DigestMatch(def CheckDef) CheckOutcome {
	if len(def.Args) != 2 {
		return CheckOutcome{CheckStatusBlocked, fmt.Sprintf("l1_args: %s wants (path, sha256), got %d args", CheckOpDigestMatch, len(def.Args))}
	}
	if !l1DigestPattern.MatchString(def.Args[1]) {
		return CheckOutcome{CheckStatusBlocked, "l1_args: digest must be lowercase 64-hex sha256"}
	}
	b, bad := l1Read(def, def.Args[:1])
	if bad.Status != "" {
		return bad
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) == def.Args[1] {
		return CheckOutcome{Status: CheckStatusPass}
	}
	return CheckOutcome{Status: CheckStatusFail, Reason: "digest mismatch"}
}

func l1GrepCount(def CheckDef) CheckOutcome {
	if len(def.Args) != 3 {
		return CheckOutcome{CheckStatusBlocked, fmt.Sprintf("l1_args: %s wants (path, pattern, count), got %d args", CheckOpGrepCount, len(def.Args))}
	}
	want, err := strconv.Atoi(def.Args[2])
	if err != nil || want < 0 {
		return CheckOutcome{CheckStatusBlocked, fmt.Sprintf("l1_args: count must be a non-negative integer (got %q)", def.Args[2])}
	}
	re, err := regexp.Compile(def.Args[1])
	if err != nil {
		return CheckOutcome{CheckStatusBlocked, fmt.Sprintf("l1_pattern: %v", err)}
	}
	b, bad := l1Read(def, def.Args[:1])
	if bad.Status != "" {
		return bad
	}
	count := len(re.FindAllIndex(b, L1MaxMatches+1))
	if count > L1MaxMatches {
		return CheckOutcome{CheckStatusBlocked, fmt.Sprintf("l1_match_cap: more than %d matches", L1MaxMatches)}
	}
	if count == want {
		return CheckOutcome{Status: CheckStatusPass}
	}
	return CheckOutcome{Status: CheckStatusFail, Reason: fmt.Sprintf("match count %d != %d", count, want)}
}

// --- L2 exact match + L3 network class ----------------------------------------------

// ExactCheckCommandLine is the canonical Bash spelling of a registered check:
// command plus args, single-space joined. The gate admits an L2 execution only
// when the model's Bash command string EQUALS this — no chaining, no extra
// spacing, no wrappers (that byte equality is the sandbox-bypass check on the
// actual Bash input: nothing can ride along an exact match).
func ExactCheckCommandLine(def CheckDef) string {
	if len(def.Args) == 0 {
		return def.Command
	}
	return def.Command + " " + strings.Join(def.Args, " ")
}

// MatchProjectCheck finds the project-trust registry definition whose exact
// command line the given Bash command IS (first registry match — registry ids
// are unique; two entries sharing a command line is the manifest author's
// ambiguity and resolves deterministically by declaration order).
func MatchProjectCheck(command string, registry []CheckDef) (CheckDef, bool) {
	for _, def := range registry {
		if def.Trust != "project" {
			continue
		}
		if command == ExactCheckCommandLine(def) {
			return def, true
		}
	}
	return CheckDef{}, false
}

// networkCheckVerbs is the L3 class: commands whose PRIMARY verb is a network
// client. The sandbox's isolation cannot make a network check safe to trust —
// the check's need is beyond what the platform can prove, so the ladder
// refuses to execute it and reports BLOCKED l3_unsupported (不降级为"跑了吧").
var networkCheckVerbs = map[string]bool{
	"curl": true, "wget": true, "ssh": true, "scp": true, "sftp": true,
	"rsync": true, "nc": true, "ncat": true, "socat": true, "ping": true,
	"telnet": true, "ftp": true, "dig": true, "host": true, "nslookup": true,
}

// IsNetworkCheckCommand reports whether a project check's command line is
// network-class (L3): its first token, reduced to a base name, is a network
// client verb.
func IsNetworkCheckCommand(def CheckDef) bool {
	tok := def.Command
	if i := strings.IndexAny(tok, " \t"); i >= 0 {
		tok = tok[:i]
	}
	if strings.ContainsRune(tok, '/') {
		tok = path.Base(tok)
	}
	return networkCheckVerbs[tok]
}

// --- the in-band sandbox probe -------------------------------------------------------

// SandboxProbeBin is the framework-owned in-band probe adapter. It runs as a
// MODEL Bash call (inside the sandbox, unlike every hook child), attempts a
// fresh exclusive write the sandbox must refuse, and prints the wrapper line
// ParseSandboxProbeWrapper consumes. Not a hook: hooks.json never wires it.
const SandboxProbeBin = "summoner-sandbox-probe"

// SandboxProbeSignature identifies the adapter's wrapper line.
const SandboxProbeSignature = SandboxProbeBin + "/v1"

// probeWrapper is the adapter's output shape.
type probeWrapper struct {
	Probe  string `json:"probe"`
	Effect string `json:"effect"`
	Detail string `json:"detail,omitempty"`
}

// IsSandboxProbeInvocation reports whether a Bash command is EXACTLY the
// anchored probe invocation: every segment's verb token is a path ENDING
// `/hooks/bin/summoner-sandbox-probe`, with no operands, no redirects, no
// second command. The BARE spelling is deliberately NOT admitted — the probe's
// observation feeds L2 admission, and a bare name is PATH-shadowable by a
// planted binary; only the plugin-bin anchor is the framework's own artifact.
func IsSandboxProbeInvocation(command string) bool {
	if command == "" {
		return false
	}
	norm := strings.ReplaceAll(command, "$(", "; ")
	norm = strings.ReplaceAll(norm, "`", "; ")
	segs := 0
	for _, seg := range strings.FieldsFunc(norm, func(r rune) bool {
		return r == ';' || r == '|' || r == '&' || r == '\n' || r == '\r'
	}) {
		segs++
		words := probeTokenize(seg)
		verbIdx, verb := resolveProbeVerb(words)
		if verbIdx < 0 || len(words) != verbIdx+1 {
			return false // any operand, wrapper or second token breaks the shape
		}
		if !strings.HasSuffix(verb, "/hooks/bin/"+SandboxProbeBin) {
			return false
		}
	}
	return segs > 0
}

// probeTokenize mirrors the gate's tokenizer (quotes join spans; an empty
// quoted operand still yields a token).
func probeTokenize(s string) []string {
	var toks []string
	var cur strings.Builder
	var quote rune
	hasTok := false
	flush := func() {
		if hasTok || cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
			hasTok = false
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			hasTok = true
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return toks
}

// probeAssign matches leading VAR=value assignments for resolveProbeVerb.
var probeAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// resolveProbeVerb skips leading VAR=value assignments and the wrappers
// env/command/nohup (mirroring the gate's resolveVerb) and reduces absolute
// paths to their base name.
func resolveProbeVerb(words []string) (int, string) {
	i := 0
	for i < len(words) {
		if probeAssign.MatchString(words[i]) {
			i++
			continue
		}
		base := words[i]
		if strings.ContainsRune(base, '/') {
			base = path.Base(base)
		}
		switch base {
		case "env":
			i++
			for i < len(words) && (strings.HasPrefix(words[i], "-") || probeAssign.MatchString(words[i])) {
				i++
			}
		case "command", "nohup":
			i++
		default:
			return i, words[i]
		}
	}
	return -1, ""
}

// ParseSandboxProbeWrapper finds the LAST adapter wrapper line in a Bash
// stdout and returns its effect (refused|writable|unproven). Plain command
// output never parses — the signature string is the gate.
func ParseSandboxProbeWrapper(stdout string) (string, bool) {
	effect, found := "", false
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, SandboxProbeSignature) {
			continue
		}
		var w probeWrapper
		if err := json.Unmarshal([]byte(line), &w); err != nil || w.Probe != SandboxProbeSignature {
			continue
		}
		switch w.Effect {
		case SandboxProbeRefused, SandboxProbeWritable, SandboxProbeUnproven:
			effect, found = w.Effect, true
		}
	}
	return effect, found
}

// SandboxProbeResult is one sandbox-effect probe outcome: the effect
// (refused|writable|unproven) and a detail string for the wrapper's audience.
type SandboxProbeResult struct {
	Effect string
	Detail string
}

// RunSandboxProbe performs the fresh-exclusive sandbox-effect probe (T19,
// brief ruling: never overwrite or remove an EXISTING file — the fixed HOME
// write of the old detector is gone). It attempts to CREATE a randomly named
// probe file in dir with O_EXCL — a name collision (practically impossible)
// is an unrelated error and reports unproven. Classification is deliberately
// conservative:
//
//   - the open SUCCEEDS → writable: the environment is NOT sandboxed (the
//     adapter removed ONLY its own fresh file on the way out);
//   - the open fails with a PERMISSION/READ-ONLY refusal (EPERM, EACCES,
//     EROFS) → refused: a sandbox (or equivalent confinement) is in effect;
//   - any other error (missing directory, I/O, quota, name collision) →
//     unproven: no claim either way — an honest non-observation.
//
// This runs IN-BAND (the adapter binary, as a model Bash call inside the
// sandbox) or at INSTALL time (the contract detector). A hook child calling
// it proves nothing about the model's sandbox — hooks authorize no L2.
func RunSandboxProbe(dir string) SandboxProbeResult {
	name, err := randomProbeName()
	if err != nil {
		return SandboxProbeResult{Effect: SandboxProbeUnproven, Detail: "rand: " + err.Error()}
	}
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		f.Close()
		os.Remove(path) // remove ONLY the file this call created
		return SandboxProbeResult{Effect: SandboxProbeWritable, Detail: "fresh write succeeded"}
	}
	if isRefusal(err) {
		return SandboxProbeResult{Effect: SandboxProbeRefused, Detail: err.Error()}
	}
	return SandboxProbeResult{Effect: SandboxProbeUnproven, Detail: err.Error()}
}

// isRefusal classifies an open error as a sandbox-style refusal: a permission
// denial or a read-only filesystem. Everything else is an unrelated I/O error
// — conservatively NOT evidence of a sandbox.
func isRefusal(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == syscall.EPERM || errno == syscall.EACCES || errno == syscall.EROFS
}

// randomProbeName draws the fresh exclusive name (crypto/rand, 16 hex chars —
// the collision space dwarfs any realistic file count in the probe directory).
func randomProbeName() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf(".summoner-sandbox-probe-%x", b), nil
}

// --- trace scans (crash windows + evidence joins) -------------------------------------

// DanglingIntent is one crash-window intent a scan found: a MUTATING tool
// call whose result never landed, or (SessionStart only) a worker delegation
// that never completed. Read-only calls, Ask/decision bookkeeping, Skill
// channels (their reducers commit before the intent event) and reviewer
// delegations (the §3 review lifecycle owns them) are never crash candidates.
type DanglingIntent struct {
	ToolUseID string
	Kind      string // "tool" | "worker-delegation"
	ToolName  string
}

// traceIndex is the join index over one run's events.
type traceIndex struct {
	started  map[string]TraceEvent // tool_use_id → tool.started
	resolved map[string]bool       // ids with a tool.result or tool.failed
	markSeq  map[string]int        // unknown id → tool.outcome_unknown seq
	probeSeq map[string]int        // probe tool_use_id → tool.result seq
	events   []TraceEvent
}

// LoadRunEvents reads a run's trace file tolerantly (unparseable lines are
// skipped — a scan must classify what is readable, never crash on a torn
// tail; AppendEvent is the component that fails closed on corruption).
func LoadRunEvents(runID string) ([]TraceEvent, error) {
	p, err := traceFilePath(runID)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var evs []TraceEvent
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev TraceEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		evs = append(evs, ev)
	}
	return evs, nil
}

func indexTrace(evs []TraceEvent) *traceIndex {
	idx := &traceIndex{
		started:  map[string]TraceEvent{},
		resolved: map[string]bool{},
		markSeq:  map[string]int{},
		probeSeq: map[string]int{},
		events:   evs,
	}
	for _, ev := range evs {
		switch ev.Type {
		case EventToolStarted:
			if ev.ToolUseID != "" {
				idx.started[ev.ToolUseID] = ev
			}
		case EventToolResult, EventToolFailed:
			if ev.ToolUseID != "" {
				idx.resolved[ev.ToolUseID] = true
			}
		}
		if ev.Type == EventToolOutcomeUnknown && ev.ToolUseID != "" {
			idx.markSeq[ev.ToolUseID] = ev.Seq
		}
		if ev.Type == EventToolResult && ev.ToolUseID != "" {
			if _, ok := ev.Payload["sandbox_probe"]; ok {
				idx.probeSeq[ev.ToolUseID] = ev.Seq
			}
		}
	}
	return idx
}

// MutatingDanglingIntents classifies crash-window intents over one run's
// events. includeWorkerDelegations is the SessionStart form: after a session
// restart the previous session's worker delegations are dead processes whose
// side effects are uncertain — the Stop form passes false because a
// background worker may legitimately still be running at a normal per-turn
// Stop (a live delegation is not a crash). Reviewer delegations are NEVER
// candidates: the §3 crash-recovery path (same-envelope re-send, transcript
// backfill) owns them, and incomplete review control state stays separate
// from uncertain external mutations. Worker correlation is per-type FIFO
// (SubagentStop carries no originating tool_use_id — declared limitation);
// a worker start also resolves when its own Agent call has a tool.failed.
func MutatingDanglingIntents(evs []TraceEvent, includeWorkerDelegations bool) []DanglingIntent {
	idx := indexTrace(evs)
	var out []DanglingIntent
	for _, ev := range evs {
		if ev.Type != EventToolStarted {
			continue
		}
		name, _ := ev.Payload["tool_name"].(string)
		if !mutatingToolName(name, ev.Payload) {
			continue
		}
		if idx.resolved[ev.ToolUseID] {
			continue
		}
		if _, already := idx.markSeq[ev.ToolUseID]; already {
			continue // a prior scan already classified this id
		}
		out = append(out, DanglingIntent{ToolUseID: ev.ToolUseID, Kind: "tool", ToolName: name})
	}
	if includeWorkerDelegations {
		var openWorkers []string
		for _, ev := range evs {
			switch ev.Type {
			case EventDelegationStarted:
				if sub, _ := ev.Payload["subagent_type"].(string); NormalizeAgentType(sub) == "worker" {
					openWorkers = append(openWorkers, ev.ToolUseID)
				}
			case EventDelegationCompleted, EventDelegationFailed:
				if typ, _ := ev.Payload["agent_type"].(string); NormalizeAgentType(typ) == "worker" || typ == "" {
					if len(openWorkers) > 0 {
						openWorkers = openWorkers[1:] // FIFO: the OLDEST open start absorbs the completion
					}
				}
			}
		}
		for _, id := range openWorkers {
			if idx.resolved[id] {
				continue // the Agent call itself failed → no delegation ever ran
			}
			if _, already := idx.markSeq[id]; already {
				continue
			}
			out = append(out, DanglingIntent{ToolUseID: id, Kind: "worker-delegation", ToolName: "Agent"})
		}
	}
	return out
}

// mutatingToolName reports whether a tool.started event names a MUTATING call:
// the file tools by name, Bash by the gate's read_class marker being ABSENT
// (the gate persists the class at observation time — a Bash intent without
// the marker went through the write-class branch). Everything else (Read,
// Grep, Ask, Skill bookkeeping, unknown tools) is read-only/status class.
func mutatingToolName(name string, payload map[string]interface{}) bool {
	switch name {
	case "Edit", "Write", "NotebookEdit":
		return true
	case "Bash":
		read, _ := payload["read_class"].(bool)
		return !read
	}
	return false
}

// MarkingSeq returns the seq of the tool.outcome_unknown event that marked an
// unknown id (the current-episode boundary for probe evidence).
func MarkingSeq(evs []TraceEvent, unknownID string) (int, bool) {
	idx := indexTrace(evs)
	seq, ok := idx.markSeq[unknownID]
	return seq, ok
}

// ProbeEvidence validates ONE probe id as the evidence for ONE unknown id:
// an observed READ-CLASS BASH result (gate-admitted tool.started with the
// read_class marker plus a tool.result, no tool.failed — a failed probe
// observes nothing) recorded AFTER the unknown's marking event (current
// episode; prior-episode evidence is stale). The join is scoped to THIS run's
// trace, so foreign run/session ids never validate.
func ProbeEvidence(evs []TraceEvent, unknownID, probeID string, markingSeq int) error {
	idx := indexTrace(evs)
	start, ok := idx.started[probeID]
	if !ok {
		return fmt.Errorf("probe %s has no admitted tool.started in this run's trace", probeID)
	}
	if name, _ := start.Payload["tool_name"].(string); name != "Bash" {
		return fmt.Errorf("probe %s is not a Bash call (the reconciliation probe channel is read-class Bash)", probeID)
	}
	if read, _ := start.Payload["read_class"].(bool); !read {
		return fmt.Errorf("probe %s was not read-class (write-class probes are denied during OUTCOME_UNKNOWN)", probeID)
	}
	if !idx.resolved[probeID] {
		return fmt.Errorf("probe %s has no observed result", probeID)
	}
	for _, ev := range evs {
		if ev.Type == EventToolFailed && ev.ToolUseID == probeID {
			return fmt.Errorf("probe %s failed — a failed probe observes nothing", probeID)
		}
		if ev.Type == EventToolResult && ev.ToolUseID == probeID && ev.Seq <= markingSeq {
			return fmt.Errorf("probe %s predates the unknown's marking (stale/prior-episode evidence)", probeID)
		}
	}
	return nil
}

// LatestSandboxProbe returns the newest in-band sandbox-probe observation
// with seq < beforeSeq (beforeSeq 0 = unbounded): the probe's tool_use_id and
// whether its effect was REFUSED (sandbox proven at that point-in-time
// moment). Writable/unproven return refused=false — an honest decline.
func LatestSandboxProbe(evs []TraceEvent, beforeSeq int) (id string, refused bool, found bool) {
	best := ""
	bestRefused := false
	for _, ev := range evs {
		if ev.Type != EventToolResult {
			continue
		}
		if beforeSeq > 0 && ev.Seq >= beforeSeq {
			continue
		}
		m, _ := ev.Payload["sandbox_probe"].(map[string]interface{})
		if m == nil {
			continue
		}
		effect, _ := m["effect"].(string)
		switch effect {
		case SandboxProbeRefused, SandboxProbeWritable, SandboxProbeUnproven:
			best = ev.ToolUseID
			bestRefused = effect == SandboxProbeRefused
			found = true
		}
	}
	return best, bestRefused, found
}

// NewestCheckResult returns the newest check.result event naming a check id
// (executions and honest BLOCKEDs both count — the ladder's newest outcome
// governs a later reference).
func NewestCheckResult(evs []TraceEvent, checkID string) (TraceEvent, bool) {
	var best TraceEvent
	found := false
	for _, ev := range evs {
		if ev.Type != EventCheckResult {
			continue
		}
		if id, _ := ev.Payload["check_id"].(string); id == checkID {
			best = ev
			found = true
		}
	}
	return best, found
}

// L2Admission returns the l2_check binding the gate recorded on a call's
// tool.started (check_id, digest, cwd, env_allow) plus that event's seq —
// the invocation half of the admission/result join.
func L2Admission(evs []TraceEvent, toolUseID string) (map[string]interface{}, int, bool) {
	idx := indexTrace(evs)
	start, ok := idx.started[toolUseID]
	if !ok {
		return nil, 0, false
	}
	binding, ok := start.Payload["l2_check"].(map[string]interface{})
	if !ok {
		return nil, 0, false
	}
	return binding, start.Seq, true
}

// StartedEvent returns a call's tool.started payload and seq — the
// gate-recorded admission record observation hooks join against (the probe
// marker, the L2 binding). The gate is the ONLY writer of these markers, so a
// marker found here is proof the gate admitted that call through that channel.
func StartedEvent(evs []TraceEvent, toolUseID string) (map[string]interface{}, int, bool) {
	idx := indexTrace(evs)
	start, ok := idx.started[toolUseID]
	if !ok {
		return nil, 0, false
	}
	return start.Payload, start.Seq, true
}

// BashExit extracts a Bash exit status from a tool_response when the platform
// exposes one (exit_code / exitCode / status / code). Absent → (0, false):
// the caller records an honest BLOCKED exit_unobservable, never a guessed 0.
func BashExit(toolResponse interface{}) (int, bool) {
	m, ok := toolResponse.(map[string]interface{})
	if !ok {
		return 0, false
	}
	for _, key := range []string{"exit_code", "exitCode", "status", "code"} {
		switch v := m[key].(type) {
		case float64:
			return int(v), true
		case int:
			return v, true
		}
	}
	return 0, false
}
