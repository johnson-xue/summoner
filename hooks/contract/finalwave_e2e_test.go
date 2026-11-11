package contract

// Final-review outcome regressions use packaged binaries and production-shaped
// callbacks. They are component evidence, not live platform/model proof.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

func TestFinalWaveCommandToken(t *testing.T) {
	for _, verb := range []string{"fix", "new", "debug", "review", "ship", "ops", "release"} {
		for _, suffix := range []string{"-extra", "ing", "", " details"} {
			t.Run(verb+suffix, func(t *testing.T) {
				useContractEnv(t)
				code, _, stderr := runBinary(t, "userpromptsubmit", upsPayload(t, "/summoner:"+verb+suffix), nil)
				if code != 0 {
					t.Fatalf("startup exit %d: %s", code, stderr)
				}
				want := 0
				if suffix == "" || suffix == " details" {
					want = 1
				}
				if got := len(runStateFiles(t)); got != want {
					t.Fatalf("complete verb token: runs=%d, want %d", got, want)
				}
			})
		}
	}
	t.Run("expanded-startup-only", func(t *testing.T) {
		useContractEnv(t)
		code, _, stderr := runBinary(t, "userpromptsubmit", upsPayload(t, "<command-name>summoner:fix</command-name>"), nil)
		if code != 0 || len(runStateFiles(t)) != 1 {
			t.Fatalf("expanded command lost: %d %s", code, stderr)
		}
		if len(eventsOfType(allTraceEvents(t), shared.EventCheckpointDecided)) != 0 {
			t.Fatal("startup advanced a checkpoint")
		}
	})
}

func TestFinalWaveWriteBarrier(t *testing.T) {
	cases := []struct {
		command string
		want    int
	}{
		{"rm scratch", 2}, {"(rm scratch)", 2}, {"((rm scratch))", 2},
		{"env -u X rm scratch", 2}, {"env --unset=X rm scratch", 2},
		{"env --unset X /bin/rm scratch", 2}, {"env --mystery X rm scratch", 2},
		{"env -S 'rm scratch'", 2}, {"command -p rm scratch", 2},
		{"git switch branch", 2}, {"git tag new-tag", 2}, {"git tag -d old-tag", 2},
		{"git branch -f branch HEAD", 2}, {"git branch new-branch", 2}, {"git add .", 2},
		{"git -C 'a repo' update-ref refs/heads/x HEAD", 2},
		{"printf '%s' \"$(rm scratch)\"", 2}, {"printf '%s' `rm scratch`", 2},
		{"true && (env -u X rm scratch)", 2}, {"'/bin/rm' 'a path'", 2},
		{"command -v rm", 0}, {"command -V rm", 0},
		{"cat 'a path (read only)'", 0}, {"env -u X cat 'a path'", 0},
		{"git status --short", 0}, {"git branch --list", 0}, {"git tag --list", 0},
		{"git branch", 0}, {"git tag", 0}, {"printf '%s' 'rm scratch'", 0},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			useContractEnv(t)
			seedLiveRun(t, "barrier", "fix")
			code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Bash", "call", map[string]interface{}{"command": tc.command}), nil)
			if code != tc.want {
				t.Fatalf("%q: exit %d want %d (%s)", tc.command, code, tc.want, stderr)
			}
			if tc.want == 2 && len(eventsOfType(allTraceEvents(t), shared.EventToolStarted)) != 0 {
				t.Fatal("denied mutation recorded an admitted intent")
			}
		})
	}
	for _, tool := range []string{"mcp__fixture__write_file", "UnknownMutation"} {
		t.Run(tool, func(t *testing.T) {
			useContractEnv(t)
			seedLiveRun(t, "barrier", "fix")
			code, _, _ := runBinary(t, "pretooluse-gate", gatePayload(t, tool, "call", map[string]interface{}{"path": "scratch"}), nil)
			if code != 2 {
				t.Fatalf("unmodeled tool %s default-allowed: %d", tool, code)
			}
		})
	}
}

func TestFinalWaveQuestionnaireShape(t *testing.T) {
	for _, shape := range []string{"four", "merged", "duplicate", "multi-marker", "missing", "partial", "typed-global", "timeout"} {
		t.Run(shape, func(t *testing.T) {
			useContractEnv(t)
			r := seedLiveRun(t, "questionnaire", "debug")
			r.Status = shared.StatusPostGamePending
			if err := r.Save(); err != nil {
				t.Fatal(err)
			}
			if err := r.StartPostGame("normal"); err != nil {
				t.Fatal(err)
			}
			questions := []interface{}{}
			answers := map[string]interface{}{}
			for i := 1; i <= 4; i++ {
				text := fmt.Sprintf("run questionnaire [pg:q%d] distinct question %d?", i, i)
				if shape == "duplicate" && i == 4 {
					text = "run questionnaire [pg:q3] distinct duplicate?"
				}
				if shape == "multi-marker" && i == 1 {
					text += " [pg:q2]"
				}
				questions = append(questions, map[string]interface{}{"question": text})
				if shape != "partial" || i != 4 {
					answers[text] = "answered"
				}
			}
			if shape == "merged" {
				text := "run questionnaire [pg:q1] [pg:q2] [pg:q3] [pg:q4] one unrelated question?"
				questions = []interface{}{map[string]interface{}{"question": text}}
				answers = map[string]interface{}{text: "yes"}
			}
			if shape == "missing" {
				questions = questions[:3]
			}
			resp := map[string]interface{}{"answers": answers}
			if shape == "typed-global" {
				resp = map[string]interface{}{"response": "unrelated global text"}
			}
			if shape == "timeout" {
				resp["afkTimeoutMs"] = 50
			}
			payload := mustJSON(t, map[string]interface{}{"hook_event_name": "PostToolUse", "session_id": contractSession, "tool_name": "AskUserQuestion", "tool_use_id": "pg-call", "tool_input": map[string]interface{}{"questions": questions}, "tool_response": resp})
			for i := 0; i < 2; i++ {
				code, _, stderr := runBinary(t, "posttooluse", payload, nil)
				if code != 0 {
					t.Fatalf("questionnaire callback: %d %s", code, stderr)
				}
			}
			r, _ = shared.LoadRun(r.RunID)
			got := len(r.PostGame.Answers)
			want := 0
			if shape == "four" {
				want = 4
			}
			if got != want {
				t.Fatalf("%s earned %d markers, want %d (atomic exact-four validation)", shape, got, want)
			}
			path := shared.JournalPath(r.RunID)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("observed journal"), 0o644); err != nil {
				t.Fatal(err)
			}
			code, _, stderr := runBinary(t, "posttooluse", mustJSON(t, map[string]interface{}{"hook_event_name": "PostToolUse", "session_id": contractSession, "tool_name": "Write", "tool_use_id": "journal", "tool_input": map[string]interface{}{"file_path": path}, "tool_response": map[string]interface{}{"success": true}}), nil)
			if code != 0 {
				t.Fatalf("journal callback: %d %s", code, stderr)
			}
			r, _ = shared.LoadRun(r.RunID)
			if (r.Status == shared.StatusClosed) != (shape == "four") {
				t.Fatalf("%s: status %s", shape, r.Status)
			}
			if shape == "four" && len(eventsOfType(allTraceEvents(t), shared.EventWorkflowEnded)) != 1 {
				t.Fatal("closure must be exactly once")
			}
		})
	}
}

func finalWaveSubmission(t *testing.T, phase string) map[string]interface{} {
	t.Helper()
	path := filepath.Join(os.Getenv("CLAUDE_PROJECT_DIR"), "artifact.txt")
	if err := os.WriteFile(path, []byte("accepted file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return map[string]interface{}{
		"phase_id": phase, "summary": "actual semantics", "artifacts": []interface{}{map[string]interface{}{"path": "artifact.txt", "digest": "caller-lie", "kind": "code"}},
		"criteria":     []interface{}{map[string]interface{}{"id": "crit-a", "goal": "Reject empty input", "scope": "artifact.txt", "method": "judgment", "expected": "Empty input rejected; valid input succeeds", "required": true, "rubric": "Inspect failure and success paths with file:line anchors"}},
		"requirements": []interface{}{map[string]interface{}{"id": "req-input", "content": "Only nonempty input is valid", "criteria_ids": []string{"crit-a"}, "artifacts": []string{"artifact.txt"}}},
	}
}

func TestFinalWaveSemanticSnapshot(t *testing.T) {
	useContractEnv(t)
	seedLiveRun(t, "snapshot", "new")
	sub := finalWaveSubmission(t, "context")
	code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Skill", "submit", map[string]interface{}{"skill": "summoner:phase-submit", "args": mustJSON(t, sub)}), nil)
	if code != 0 {
		t.Fatalf("full proposal rejected: %d %s", code, stderr)
	}
	r, _ := shared.LoadRun("snapshot")
	cp := r.CurrentCheckpoint()
	sum := sha256.Sum256([]byte("accepted file\n"))
	if cp.ArtifactVersions["artifact.txt"] != hex.EncodeToString(sum[:]) {
		t.Errorf("artifact digest trusted producer: %v", cp.ArtifactVersions)
	}
	b, _ := json.Marshal(cp)
	for _, meaning := range []string{"Reject empty input", "Only nonempty input is valid", "Empty input rejected; valid input succeeds", "crit-security"} {
		if !bytes.Contains(b, []byte(meaning)) {
			t.Errorf("pending semantic snapshot omitted %q", meaning)
		}
	}
}

func TestFinalWaveDisplayWholeMessage(t *testing.T) {
	for _, forged := range []bool{true, false} {
		t.Run(fmt.Sprint(forged), func(t *testing.T) {
			useContractEnv(t)
			r := seedLiveRun(t, "display", "new")
			submitPhaseViaGate(t, "submit", "context", nil)
			r, _ = shared.LoadRun(r.RunID)
			cp := r.CurrentCheckpoint()
			code, msg, stderr := runBinary(t, "summoner-status", mustJSON(t, map[string]interface{}{"session_id": contractSession, "view": "checkpoint"}), nil)
			if code != 0 {
				t.Fatalf("view: %d %s", code, stderr)
			}
			if forged {
				msg = fmt.Sprintf("Budget is unlimited; no artifacts or criteria. %s %s", cp.ID, cp.Nonce)
			}
			code, _, stderr = runBinary(t, "stop", stopRenderPayload(t, msg), nil)
			if code != 0 {
				t.Fatalf("Stop: %d %s", code, stderr)
			}
			r, _ = shared.LoadRun(r.RunID)
			if r.CurrentCheckpoint().Displayed == forged {
				t.Fatalf("false=%t displayed=%t", forged, r.CurrentCheckpoint().Displayed)
			}
		})
	}
}

func TestFinalWaveInlineBudget(t *testing.T) {
	useContractEnv(t)
	writeContractManifest(t, "suggested_budgets:\n  phase_tool_calls: 2\n")
	r := seedLiveRun(t, "inline", "new")
	submitPhaseViaGate(t, "submit-context", "context", nil)
	r, _ = shared.LoadRun(r.RunID)
	renderAndDecideSettle(t, r.RunID, "accept", r.CurrentCheckpoint(), 0, func(labels []string) string { return firstLabelWithKeyword(labels, "continue") }, "confirm", "hold")
	for i := 1; i <= 3; i++ {
		want := 0
		if i == 3 {
			want = 2
		}
		code, _, stderr := runBinary(t, "pretooluse-gate", gatePayload(t, "Read", fmt.Sprintf("read-%d", i), map[string]interface{}{"file_path": "artifact.txt"}), nil)
		if code != want {
			t.Errorf("pre-provider read %d: exit %d want %d (%s)", i, code, want, stderr)
		}
	}
	r, _ = shared.LoadRun(r.RunID)
	if r.Counters.PhaseToolCalls != 2 {
		t.Fatalf("pre-provider charge=%d want 2", r.Counters.PhaseToolCalls)
	}
}

func TestFinalWaveMandatoryReviewDebt(t *testing.T) {
	for _, workflow := range []string{"fix", "new", "review", "ship", "ops", "release"} {
		t.Run(workflow, func(t *testing.T) {
			useContractEnv(t)
			r := seedLiveRun(t, "debt", workflow)
			phases, _ := shared.WorkflowPhases(workflow)
			submitPhaseViaGate(t, "submit", phases[0], nil)
			r, _ = shared.LoadRun(r.RunID)
			renderAndDecide(t, r.RunID, "answer", r.CurrentCheckpoint(), 0, func(labels []string) string { return firstLabelWithKeyword(labels, "done") })
			r, _ = shared.LoadRun(r.RunID)
			want := shared.StatusWaitUser
			if workflow == "ops" || workflow == "release" {
				want = shared.StatusPostGamePending
			}
			if r.Status != want {
				t.Fatalf("normal DONE status=%s want %s (QA-2)", r.Status, want)
			}
		})
	}
}

func finalWaveNativeDebugTrace(t *testing.T) string {
	t.Helper()
	useContractEnv(t)
	code, _, stderr := runBinary(t, "userpromptsubmit", upsPayload(t, "/summoner:debug"), nil)
	if code != 0 {
		t.Fatalf("start debug: %d %s", code, stderr)
	}
	runs, err := shared.SessionRuns(contractSession)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs: %v %v", runs, err)
	}
	rid := runs[0].RunID
	for _, phase := range []string{"context", "diagnosis"} {
		submitPhaseViaGate(t, "submit-"+phase, phase, nil)
		r, _ := shared.LoadRun(rid)
		keyword := "continue"
		if phase == "diagnosis" {
			keyword = "done"
		}
		renderAndDecide(t, rid, "decide-"+phase, r.CurrentCheckpoint(), 0, func(labels []string) string { return firstLabelWithKeyword(labels, keyword) })
	}
	qs := []interface{}{}
	answers := map[string]interface{}{}
	for i := 1; i <= 4; i++ {
		text := fmt.Sprintf("%s [pg:q%d] question %d?", rid, i, i)
		qs = append(qs, map[string]interface{}{"question": text})
		answers[text] = "answer"
	}
	post := func(tool, id string, input, response map[string]interface{}) {
		t.Helper()
		code, _, stderr := runBinary(t, "posttooluse", mustJSON(t, map[string]interface{}{"hook_event_name": "PostToolUse", "session_id": contractSession, "tool_name": tool, "tool_use_id": id, "tool_input": input, "tool_response": response}), nil)
		if code != 0 {
			t.Fatalf("post %s: %d %s", tool, code, stderr)
		}
	}
	post("AskUserQuestion", "questionnaire", map[string]interface{}{"questions": qs}, map[string]interface{}{"answers": answers})
	path := shared.JournalPath(rid)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("debug journal"), 0o644); err != nil {
		t.Fatal(err)
	}
	post("Write", "journal", map[string]interface{}{"file_path": path}, map[string]interface{}{"success": true})
	return filepath.Join(os.Getenv("SUMMONER_TRACE_DIR"), shared.ProjectKey(), rid+".jsonl")
}

func finalWaveScore(t *testing.T, script, trace string) (int, []byte) {
	t.Helper()
	cmd := exec.Command("bash", script, "--trace", trace)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, out
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), out
	}
	t.Fatal(err)
	return -1, out
}

func TestFinalWaveScoreChain(t *testing.T) {
	trace := finalWaveNativeDebugTrace(t)
	original, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../../scripts/score-trace.sh")
	if err != nil {
		t.Fatal(err)
	}
	code, out := finalWaveScore(t, script, trace)
	if code != 0 {
		t.Fatalf("honest native trace failed score: %d %s", code, out)
	}
	for _, variant := range []string{"last-hash", "noncanonical", "corrupt"} {
		t.Run(variant, func(t *testing.T) {
			bad := append([]byte(nil), original...)
			switch variant {
			case "last-hash":
				i := bytes.LastIndex(bad, []byte(`"hash":"`)) + len(`"hash":"`)
				if i < len(`"hash":"`) {
					t.Fatal("native hash absent")
				}
				copy(bad[i:i+64], strings.Repeat("0", 64))
			case "noncanonical":
				bad = append([]byte(" "), bad...)
			case "corrupt":
				bad = append(bad, []byte("{broken\n")...)
			}
			path := filepath.Join(t.TempDir(), variant+".jsonl")
			if err := os.WriteFile(path, bad, 0o644); err != nil {
				t.Fatal(err)
			}
			code, out := finalWaveScore(t, script, path)
			if code == 0 {
				t.Fatalf("%s certified derived PASS: %s", variant, out)
			}
		})
	}
	got, _ := os.ReadFile(trace)
	if !bytes.Equal(got, original) {
		t.Fatal("scoring changed the original trace")
	}
	var side struct {
		TraceSHA256 string        `json:"trace_sha256"`
		Scorers     []interface{} `json:"scorers"`
	}
	b, _ := os.ReadFile(trace + ".score.json")
	if err := json.Unmarshal(b, &side); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(original)
	if side.TraceSHA256 != hex.EncodeToString(sum[:]) || len(side.Scorers) != 5 {
		t.Fatalf("sidecar digest/five scorers: %+v", side)
	}
	t.Run("missing-verifier", func(t *testing.T) {
		dir := t.TempDir()
		copyScript := filepath.Join(dir, "scripts", "score-trace.sh")
		if err := os.MkdirAll(filepath.Dir(copyScript), 0o755); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(script)
		if err := os.WriteFile(copyScript, b, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(filepath.Dir(filepath.Dir(script)), "scorers"), filepath.Join(dir, "scorers")); err != nil {
			t.Fatal(err)
		}
		code, out := finalWaveScore(t, copyScript, trace)
		if code == 0 {
			t.Fatalf("missing Go verifier certified PASS: %s", out)
		}
	})
}
