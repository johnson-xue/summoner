package shared

// budget.go — the T18 budget ride-along channel: the nonce-free budget
// confirm (run's first checkpoint) and extension (exhaustion checkpoint)
// questions that ride the SAME AskUserQuestion call as the nonce decision
// question, like T17's drop:/keep: ride-alongs. Detection keys on the leading
// keyword of an option label (budget:/extend:); the offered label PAIR must
// equal the checkpoint's persisted pointer labels exactly — labels are
// pointers to persisted numbers, never carriers of them (panel TQ3: no new
// Decision values; SetBudgetLimits/ExtendBudget apply persisted values only).

import (
	"fmt"
	"strings"
)

// The canonical pointer labels (single source: the render prints them, the
// gate pins offered sets to them, the recorder maps answers by them).
const (
	BudgetConfirmLabel   = "budget: 按已显示预算" // retired 0.6.1: kept for pre-0.6.1 trace replay tolerance
	BudgetUnlimitedLabel = "budget: 不设上限"   // retired 0.6.1: kept for pre-0.6.1 trace replay tolerance
	BudgetExtendLabel    = "extend: 追加已显示额度"
	BudgetHoldLabel      = "extend: 不追加"
)

// ResolveStartLimits returns the run-start limits (T18, 0.6.1): budgets are
// manifest-authored — the manifest's suggested_budgets projection applies as
// configuration with NO runtime ask; absent manifest → unlimited (zero
// limits). A corrupt manifest fails CLOSED (the loader's standing rule: the
// suggestion is consumed as authority).
func ResolveStartLimits() (BudgetLimits, error) {
	m, err := LoadRuntimeManifest()
	if err != nil {
		return BudgetLimits{}, fmt.Errorf("budget resolution: project manifest unreadable: %w", err)
	}
	if m == nil || m.SuggestedBudgets == nil {
		return BudgetLimits{}, nil
	}
	return *m.SuggestedBudgets, nil
}

// budgetRideAlongKind names the two ride-along question kinds.
const (
	rideAlongBudget = "budget"
	rideAlongExtend = "extend"
)

// AskUserQuestion payload field names (the shared-side copy of the wire
// constants; the posttooluse package keeps its own set).
const (
	fieldAskQuestions = "questions"
	fieldAskQuestion  = "question"
	fieldAskOptions   = "options"
	fieldAskLabel     = "label"
)

// BudgetRideAlong is one parsed budget/extend ride-along question: its kind,
// full text (the answers map key) and the two offered labels (already
// validated to equal the persisted pair).
type BudgetRideAlong struct {
	Kind   string
	Text   string
	Labels [2]string
}

// ParseBudgetRideAlongs validates every budget/extend question of an
// AskUserQuestion payload against the checkpoint's persisted asks ("" problem
// = all well-formed; nil slice = none present). A question is a budget
// ride-along iff any option label leads with the keyword budget or extend.
// Shape contract: exactly two options equal to the persisted pointer pair
// (set-equal, order-free), nonce-free (the nonce binds the DECISION
// question), at most one question per kind, and the matching persisted ask
// (BudgetAsk/ExtendAsk) must exist on THIS checkpoint. Any violation is a
// problem string the callers treat as a whole-callback rejection — a
// malformed or stale budget question must not launder a decision, a drop or a
// limit change past the channel.
func ParseBudgetRideAlongs(cp *CheckpointState, questions []interface{}) ([]BudgetRideAlong, string) {
	var out []BudgetRideAlong
	for _, raw := range questions {
		m, _ := raw.(map[string]interface{})
		if m == nil {
			continue
		}
		options, _ := m[fieldAskOptions].([]interface{})
		text, _ := m[fieldAskQuestion].(string)
		kind := ""
		for _, opt := range options {
			om, _ := opt.(map[string]interface{})
			label, _ := om[fieldAskLabel].(string)
			switch menuKeyword(label) {
			case rideAlongBudget:
				kind = rideAlongBudget
			case rideAlongExtend:
				kind = rideAlongExtend
			}
			if kind != "" {
				break
			}
		}
		if kind == "" {
			continue
		}
		labels := make([]string, 0, len(options))
		for _, opt := range options {
			om, _ := opt.(map[string]interface{})
			if label, _ := om[fieldAskLabel].(string); label != "" {
				labels = append(labels, label)
			}
		}
		var want [2]string
		if kind == rideAlongBudget {
			// 0.6.1: the run-start budget ask is retired — budgets are
			// manifest-authored (suggested_budgets applies at StartRun) or
			// unlimited; a budget question is never valid at any checkpoint.
			return nil, fmt.Sprintf("budget question %q is not a checkpoint question — budgets are manifest-authored: declare suggested_budgets in summoner.yaml (applied at run start) or run unlimited; only extend: rides a checkpoint, at exhaustion", budgetTruncate(text))
		} else {
			if cp.ExtendAsk == nil {
				return nil, fmt.Sprintf("extend question %q rides at a checkpoint with no persisted extension offer — extension questions exist only at checkpoints opened while the budget was exhausted", budgetTruncate(text))
			}
			want = [2]string{cp.ExtendAsk.ExtendLabel, cp.ExtendAsk.HoldLabel}
		}
		switch {
		case len(labels) != 2:
			return nil, fmt.Sprintf("%s question %q must carry exactly two options, got %d", kind, budgetTruncate(text), len(labels))
		case (labels[0] != want[0] || labels[1] != want[1]) && (labels[0] != want[1] || labels[1] != want[0]):
			return nil, fmt.Sprintf("%s question %q must offer the persisted pointer labels exactly (%q / %q) — labels point at displayed numbers and never carry their own", kind, budgetTruncate(text), want[0], want[1])
		case text == "":
			return nil, fmt.Sprintf("%s question must carry question text", kind)
		case strings.Contains(text, cp.Nonce):
			return nil, fmt.Sprintf("%s question %q carries the checkpoint nonce — the nonce binds the DECISION question; budget questions ride along nonce-free", kind, budgetTruncate(text))
		}
		for _, prev := range out {
			if prev.Kind == kind {
				return nil, fmt.Sprintf("call carries two %s questions — at most one per kind (the answers map would be ambiguous)", kind)
			}
		}
		out = append(out, BudgetRideAlong{Kind: kind, Text: text, Labels: want})
	}
	return out, ""
}

// budgetTruncate keeps a rejection reason bounded (cosmetic byte cut).
func budgetTruncate(s string) string {
	const n = 80
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// BudgetSettlement is the observed budget ride-along answer of ONE decision
// callback (T18): since 0.6.1 only the EXTEND answer carries meaning (the
// delta comes from the persisted ExtendAsk inside the reducer); a Confirm
// payload is inert (budgets settle at StartRun — manifest-authored or
// unlimited) and survives only for pre-0.6.1 trace replay tolerance.
// It persists only WITH the decision (RecordDecisionWithBudget, one commit)
// or through the standalone setters (partial-answer semantics).
type BudgetSettlement struct {
	// ToolUseID is the platform tool_use_id of the answering AskUserQuestion
	// call — the replay/idempotency key of both settlement kinds.
	ToolUseID string
	// Confirm is non-nil when the budget question was answered: the limits to
	// set (zero = the unlimited choice). Nil = the question was unanswered.
	Confirm *BudgetLimits
	// Extend marks an answered extend question that chose the persisted delta.
	Extend bool
}
