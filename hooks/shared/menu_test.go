package shared

// menu_test.go — T16 TDD suite: the persisted decision-menu projection
// (S2/F1 decision surface). Pages of ≤4 options (the platform caps 4 options
// per question), anchored continue+stop, every legal action reachable in ≤2
// pages, more:/back: navigation labels, set at EVERY checkpoint-open path.

import (
	"reflect"
	"testing"
)

// labelByKeyword finds the one menu label leading with the keyword.
func labelByKeyword(t *testing.T, labels []string, kw string) string {
	t.Helper()
	for _, l := range labels {
		if menuKeyword(l) == kw {
			return l
		}
	}
	t.Fatalf("no %q-led label in %v", kw, labels)
	return ""
}

// TestMenuProjectionPagingInvariants: for every checkpoint kind the pages
// obey the T16 contract — ≤4 labels per page, ≤2 pages, every page anchors
// continue+stop when the kind's menu has them, every legal label reachable on
// some page, no foreign label introduced, and the more:/back: navigation
// labels point the right way.
func TestMenuProjectionPagingInvariants(t *testing.T) {
	kinds := []CheckpointKind{CheckpointPhase, CheckpointReviewResult, CheckpointChangeConfirm, CheckpointRecovery}
	for _, kind := range kinds {
		full := DecisionMenuLabels(kind)
		pages := MenuProjection(kind)
		if len(pages) == 0 {
			t.Fatalf("%s: projection has no pages", kind)
		}
		if len(pages) > 2 {
			t.Errorf("%s: %d pages — every legal action must be reachable in ≤2 pages", kind, len(pages))
		}
		reached := map[string]bool{}
		for i, pg := range pages {
			if len(pg.Labels) > 4 {
				t.Errorf("%s page %d: %d labels — the platform caps 4 options/question", kind, i+1, len(pg.Labels))
			}
			if len(pg.Labels) == 0 {
				t.Errorf("%s page %d: empty page", kind, i+1)
			}
			for _, l := range pg.Labels {
				reached[l] = true
				if !containsID(full, l) {
					t.Errorf("%s page %d introduced label %q outside the kind's legal menu", kind, i+1, l)
				}
			}
			// Anchors: continue and stop ride EVERY page when the kind offers them.
			for _, anchor := range []string{"continue", "stop"} {
				if !containsKeyword(full, anchor) {
					continue
				}
				if !containsKeyword(pg.Labels, anchor) {
					t.Errorf("%s page %d lacks the %q anchor (every page carries continue+stop)", kind, i+1, anchor)
				}
			}
			// Navigation labels: more: towards a next page, back: from page 2 on.
			if i < len(pages)-1 && pg.NavNext == "" {
				t.Errorf("%s page %d: missing more: navigation label", kind, i+1)
			}
			if i == len(pages)-1 && pg.NavNext != "" {
				t.Errorf("%s last page must not offer more: (nothing after it)", kind)
			}
			if i > 0 && pg.NavPrev == "" {
				t.Errorf("%s page %d: missing back: navigation label", kind, i+1)
			}
			if i == 0 && pg.NavPrev != "" {
				t.Errorf("%s first page must not offer back:", kind)
			}
		}
		for _, l := range full {
			if !reached[l] {
				t.Errorf("%s: legal label %q reachable on no page", kind, l)
			}
		}
		// Single-page kinds never page.
		if len(full) <= 4 && len(pages) != 1 {
			t.Errorf("%s: %d legal labels fit one page, got %d pages", kind, len(full), len(pages))
		}
	}
}

func containsKeyword(labels []string, kw string) bool {
	for _, l := range labels {
		if menuKeyword(l) == kw {
			return true
		}
	}
	return false
}

// TestMenuProjectionPhasePages pins the full-menu split: page 1 carries
// continue+stop+skip+done, page 2 carries continue+stop+recall+review.
func TestMenuProjectionPhasePages(t *testing.T) {
	full := DecisionMenuLabels(CheckpointPhase)
	pages := MenuProjection(CheckpointPhase)
	if len(pages) != 2 {
		t.Fatalf("PHASE projection = %d pages, want 2 (six actions at ≤4 options/page)", len(pages))
	}
	continueL, stopL := labelByKeyword(t, full, "continue"), labelByKeyword(t, full, "stop")
	want1 := []string{continueL, labelByKeyword(t, full, "skip"), labelByKeyword(t, full, "done"), stopL}
	want2 := []string{continueL, labelByKeyword(t, full, "recall"), labelByKeyword(t, full, "review"), stopL}
	if !reflect.DeepEqual(pages[0].Labels, want1) {
		t.Errorf("page 1 = %v, want %v", pages[0].Labels, want1)
	}
	if !reflect.DeepEqual(pages[1].Labels, want2) {
		t.Errorf("page 2 = %v, want %v", pages[1].Labels, want2)
	}
	if pages[0].NavNext == "" || pages[0].NavPrev != "" {
		t.Errorf("page 1 nav = (%q, %q), want (more:, \"\")", pages[0].NavNext, pages[0].NavPrev)
	}
	if pages[1].NavPrev == "" || pages[1].NavNext != "" {
		t.Errorf("page 2 nav = (%q, %q), want (\"\", back:)", pages[1].NavNext, pages[1].NavPrev)
	}
}

// TestMenuProjectionSinglePageKinds: the narrow kinds fit one page and carry
// no navigation labels.
func TestMenuProjectionSinglePageKinds(t *testing.T) {
	for _, kind := range []CheckpointKind{CheckpointChangeConfirm, CheckpointRecovery} {
		pages := MenuProjection(kind)
		if len(pages) != 1 {
			t.Fatalf("%s = %d pages, want 1", kind, len(pages))
		}
		if pages[0].NavNext != "" || pages[0].NavPrev != "" {
			t.Errorf("%s single page must carry no navigation labels, got (%q, %q)", kind, pages[0].NavNext, pages[0].NavPrev)
		}
	}
}

// TestMenuPageMatches: the offered-set predicate — exact label SET equality
// (order-insensitive), wrong count / foreign / duplicated labels all fail.
func TestMenuPageMatches(t *testing.T) {
	full := DecisionMenuLabels(CheckpointPhase)
	continueL, stopL := labelByKeyword(t, full, "continue"), labelByKeyword(t, full, "stop")
	pg := MenuPage{Labels: []string{continueL, "skip: s", "done: d", stopL}}
	if !pg.Matches([]string{"skip: s", stopL, continueL, "done: d"}) {
		t.Error("reordered exact set must match")
	}
	if pg.Matches([]string{continueL, "skip: s", "done: d"}) {
		t.Error("subset (3 of 4) must not match")
	}
	if pg.Matches([]string{continueL, "skip: s", "done: d", stopL, stopL}) {
		t.Error("duplicate padding must not match")
	}
	if pg.Matches([]string{continueL, "skip: s", "done: d", "recall: r"}) {
		t.Error("foreign label substitution must not match")
	}
	if pg.Matches(nil) {
		t.Error("empty offered set must not match a non-empty page")
	}
	if pg.Matches([]string{""}) {
		t.Error("an option without a label must not match")
	}
}

// TestCheckpointsCarryMenuProjection: every checkpoint-open path persists the
// projection — SubmitPhase (PHASE), ApplyRequirementDelta (CHANGE_CONFIRM),
// RecordReviewResult (REVIEW_RESULT), ResolveOutcome's recovery checkpoint
// (RECOVERY), and the change loop's re-rendered successor.
func TestCheckpointsCarryMenuProjection(t *testing.T) {
	// PHASE.
	r := startTestRun(t, BudgetLimits{})
	cp := openAndSubmit(t, r, "context")
	if !reflect.DeepEqual(cp.MenuPages, MenuProjection(CheckpointPhase)) {
		t.Fatalf("SubmitPhase checkpoint menu_pages = %+v, want the PHASE projection", cp.MenuPages)
	}

	// CHANGE_CONFIRM.
	r2 := startTestRun(t, BudgetLimits{})
	r2.DevPosition = DevPosition{CurrentTask: "t"}
	if err := r2.OpenRequirementChange("rename field"); err != nil {
		t.Fatal(err)
	}
	if _, err := r2.ApplyRequirementDelta(RequirementDelta{Note: "delta"}); err != nil {
		t.Fatal(err)
	}
	cc := r2.CurrentCheckpoint()
	if cc == nil || cc.Kind != CheckpointChangeConfirm {
		t.Fatalf("change-confirm checkpoint = %+v", cc)
	}
	if !reflect.DeepEqual(cc.MenuPages, MenuProjection(CheckpointChangeConfirm)) {
		t.Fatalf("change-confirm menu_pages = %+v, want the CHANGE_CONFIRM projection", cc.MenuPages)
	}

	// REVIEW_RESULT.
	r3 := startTestRun(t, BudgetLimits{})
	openAndSubmit(t, r3, "context", "c-1")
	displayCurrent(t, r3)
	decideCurrent(t, r3, DecisionContinue)
	if _, err := r3.RequestReview(testEnvelope(), nil); err != nil {
		t.Fatal(err)
	}
	if err := r3.RecordReviewResult(coveringResult(r3, "rev-1", "pass")); err != nil {
		t.Fatal(err)
	}
	rr := r3.CurrentCheckpoint()
	if rr == nil || rr.Kind != CheckpointReviewResult {
		t.Fatalf("review-result checkpoint = %+v", rr)
	}
	if !reflect.DeepEqual(rr.MenuPages, MenuProjection(CheckpointReviewResult)) {
		t.Fatalf("review-result menu_pages = %+v, want the REVIEW_RESULT projection", rr.MenuPages)
	}

	// RECOVERY.
	r4 := startTestRun(t, BudgetLimits{})
	r4.MarkOutcomeUnknown("tu-crash")
	if err := r4.Save(); err != nil {
		t.Fatal(err)
	}
	if err := r4.ResolveOutcome("tu-crash", OutcomeFailed); err != nil {
		t.Fatal(err)
	}
	rv := r4.CurrentCheckpoint()
	if rv == nil || rv.Kind != CheckpointRecovery {
		t.Fatalf("recovery checkpoint = %+v", rv)
	}
	if !reflect.DeepEqual(rv.MenuPages, MenuProjection(CheckpointRecovery)) {
		t.Fatalf("recovery menu_pages = %+v, want the RECOVERY projection", rv.MenuPages)
	}

	// Re-rendered successor of a WAIT_USER-entered change.
	r5 := startTestRun(t, BudgetLimits{})
	phaseCp := openAndSubmit(t, r5, "context") // WAIT_USER, undisplayed
	if err := r5.OpenRequirementChange("checkpoint-time scope change"); err != nil {
		t.Fatal(err)
	}
	if _, err := r5.ApplyRequirementDelta(RequirementDelta{Note: "delta"}); err != nil {
		t.Fatal(err)
	}
	displayCurrent(t, r5)
	if err := r5.RecordDecision(r5.CurrentCheckpoint().ID, DecisionAcceptRequirementChange, ""); err != nil {
		t.Fatal(err)
	}
	succ := r5.CurrentCheckpoint()
	if succ == nil || succ.ID == phaseCp.ID {
		t.Fatalf("re-rendered successor missing: %+v", r5.Checkpoints)
	}
	if !reflect.DeepEqual(succ.MenuPages, MenuProjection(CheckpointPhase)) {
		t.Fatalf("re-rendered successor menu_pages = %+v, want the PHASE projection", succ.MenuPages)
	}
}
