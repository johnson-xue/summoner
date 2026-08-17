package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestMemory returns a Memory backed by an isolated temp DB (no LLM —
// extractSummary falls back to fallbackSummary, no network). The test owns
// the DB path via SUMMONER_DB_PATH so runs are hermetic.
func newTestMemory(t *testing.T, project string) *Memory {
	t.Helper()
	tmpDir := t.TempDir()
	orig := os.Getenv("SUMMONER_DB_PATH")
	t.Cleanup(func() { os.Setenv("SUMMONER_DB_PATH", orig) })
	os.Setenv("SUMMONER_DB_PATH", tmpDir)

	m, err := NewMemory(project)
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}
	// LLM client creation is non-fatal; force nil so SavePhase uses the
	// deterministic fallbackSummary path (no network, no env dependency).
	m.llmClient = nil
	t.Cleanup(func() { m.Close() })
	return m
}

// TestSavePhase_GetPhase_RoundTrip is the承重-wall integration test: it proves
// the core write→read path works end-to-end. It is also the safety net for
// A1 (EditSummary column fix) since it exercises the phases table the edit
// mutates.
func TestSavePhase_GetPhase_RoundTrip(t *testing.T) {
	m := newTestMemory(t, "roundtrip-proj")

	wfID, err := m.StartWorkflow("summoner:fix", "nil pointer in task.go:234")
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}

	phaseID, err := m.SavePhase(SavePhaseRequest{
		WorkflowID: wfID,
		PhaseName:  "diagnose",
		SkillName:  "test-skill",
		FullOutput: "root cause: nil deref at task.go:234\nfix: add nil check",
	})
	if err != nil {
		t.Fatalf("SavePhase: %v", err)
	}
	if phaseID <= 0 {
		t.Fatalf("SavePhase returned non-positive phaseID: %d", phaseID)
	}

	phase, err := m.GetPhase(phaseID)
	if err != nil {
		t.Fatalf("GetPhase: %v", err)
	}
	if phase.WorkflowID != wfID {
		t.Errorf("WorkflowID = %q, want %q", phase.WorkflowID, wfID)
	}
	if phase.PhaseName != "diagnose" {
		t.Errorf("PhaseName = %q, want %q", phase.PhaseName, "diagnose")
	}
	if phase.Summary == "" {
		t.Error("Summary is empty; fallbackSummary should have produced text")
	}
	if phase.FullOutputSize == 0 {
		t.Error("FullOutputSize = 0; expected > 0")
	}

	// Full output round-trip via chunks.
	full, err := m.GetFullOutput(phaseID)
	if err != nil {
		t.Fatalf("GetFullOutput: %v", err)
	}
	if !strings.Contains(full, "nil deref at task.go:234") {
		t.Errorf("GetFullOutput lost content; got: %q", full)
	}
}

// TestEditSummary_RoundTrip is the RED→GREEN test for A1: EditSummary's UPDATE
// references phases.updated_at, a column that does NOT exist on the phases
// table. Before the migration fix this errors with "no such column: updated_at"
// and the edit feature never works. After the fix the edited summary persists.
func TestEditSummary_RoundTrip(t *testing.T) {
	m := newTestMemory(t, "edit-proj")

	wfID, err := m.StartWorkflow("summoner:fix", "test input")
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	phaseID, err := m.SavePhase(SavePhaseRequest{
		WorkflowID: wfID,
		PhaseName:  "diagnose",
		SkillName:  "test-skill",
		FullOutput: "initial output for edit test",
	})
	if err != nil {
		t.Fatalf("SavePhase: %v", err)
	}

	origSummary, err := m.GetSummary(phaseID)
	if err != nil {
		t.Fatalf("GetSummary (before edit): %v", err)
	}

	newSummary := "[manually edited] root cause confirmed at task.go:234"
	if err := m.EditSummary(phaseID, newSummary, "manual correction"); err != nil {
		t.Fatalf("EditSummary failed (A1 regression — updated_at column missing?): %v", err)
	}

	got, err := m.GetSummary(phaseID)
	if err != nil {
		t.Fatalf("GetSummary (after edit): %v", err)
	}
	if got != newSummary {
		t.Errorf("EditSummary did not persist: got %q, want %q", got, newSummary)
	}
	if got == origSummary {
		t.Error("EditSummary: summary unchanged after edit (no-op write)")
	}

	// The edit must mark summary_edited and record an intervention.
	phase, err := m.GetPhase(phaseID)
	if err != nil {
		t.Fatalf("GetPhase after edit: %v", err)
	}
	if phase.SummaryEdited != true {
		t.Errorf("SummaryEdited = %v, want true", phase.SummaryEdited)
	}
}

// TestSavePhase_LargeOutput_ChunkBoundary exercises saveChunks' batch insert
// (>1 batch of batchSize=100 chunks at ChunkSize=64KB). Catches off-by-one in
// placeholder count vs value count for the last partial batch.
func TestSavePhase_LargeOutput_ChunkBoundary(t *testing.T) {
	m := newTestMemory(t, "large-proj")
	wfID, err := m.StartWorkflow("summoner:fix", "large output test")
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}

	// 1.5 * ChunkSize * 100 → forces >100 chunks → multiple batches + a
	// partial final batch. Use a single repeated byte so size math is exact.
	line := strings.Repeat("x", ChunkSize) + "\n"
	// ~150 chunks worth of data.
	fullOutput := strings.Repeat(line, 150)

	phaseID, err := m.SavePhase(SavePhaseRequest{
		WorkflowID: wfID,
		PhaseName:  "fix",
		SkillName:  "test-skill",
		FullOutput: fullOutput,
	})
	if err != nil {
		t.Fatalf("SavePhase (large): %v", err)
	}

	full, err := m.GetFullOutput(phaseID)
	if err != nil {
		t.Fatalf("GetFullOutput (large): %v", err)
	}
	if len(full) != len(fullOutput) {
		t.Errorf("large output size mismatch: got %d, want %d", len(full), len(fullOutput))
	}
	if full != fullOutput {
		t.Error("large output content mismatch after chunk round-trip")
	}
}

// Ensure filepath import is used even if other tests move (compile guard).
var _ = filepath.Join
