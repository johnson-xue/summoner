package shared

// menu.go — the persisted decision-menu projection (T16 decision surface,
// spec §7 checkpoint schema addition). AskUserQuestion caps 4 options per
// question (controller-verified S2), but the PHASE/REVIEW_RESULT menu carries
// six actions — so every checkpoint persists a PAGED projection at open:
// pages of ≤4 labels, each page anchoring `continue` + `stop` (when legal for
// the kind) plus two kind-relevant actions, with `more:`/`back:` navigation
// labels. Every legal action is reachable in ≤2 pages for the shipped kinds.
//
// The projection bounds only what ONE decision question may offer. The full
// menu stays visible in the display-verified render block (summoner-status
// prints it plus a paging note); the PreToolUse gate pins any nonce-carrying
// question's offered labels to one page EXACTLY. Navigation between pages is
// nonce-free content feedback — never a decision, never state, never an
// event; the page validation is the whole enforcement.

import (
	"strings"
	"unicode"
)

// Navigation labels of the paging note: rendered lines, not question options
// (a page's ≤4 option slots belong to decisions alone).
const (
	MenuNavNext = "more: 查看其余选项"
	MenuNavPrev = "back: 返回上一页"
)

// menuPageMaxOptions is the per-page option ceiling — the platform's 4
// options/question cap (S2).
const menuPageMaxOptions = 4

// MenuPage is one page of the paged-menu projection persisted on
// CheckpointState: the decision option labels a single question may offer,
// plus the navigation labels the render's paging note prints when a
// next/previous page exists.
type MenuPage struct {
	Labels  []string `json:"labels"`
	NavNext string   `json:"nav_next,omitempty"`
	NavPrev string   `json:"nav_prev,omitempty"`
}

// Matches reports whether offered equals the page's labels as an exact label
// SET: same length, every offered label on the page, none missing, no
// duplicates padding. Order is irrelevant (the platform renders options as a
// list; the recorder maps by leading keyword).
func (p MenuPage) Matches(offered []string) bool {
	if len(offered) != len(p.Labels) {
		return false
	}
	counts := make(map[string]int, len(p.Labels))
	for _, l := range p.Labels {
		counts[l]++
	}
	for _, l := range offered {
		counts[l]--
		if counts[l] < 0 {
			return false
		}
	}
	return true
}

// MenuPageFor returns the persisted page whose label set equals offered
// exactly, or nil when no page matches. nil is also the fail-closed answer
// for a checkpoint with no projection.
func (cp *CheckpointState) MenuPageFor(offered []string) *MenuPage {
	for i := range cp.MenuPages {
		if cp.MenuPages[i].Matches(offered) {
			return &cp.MenuPages[i]
		}
	}
	return nil
}

// OnMenuPage reports whether the exact label sits on one of the checkpoint's
// persisted pages (the S2 pinning makes every legal answer such a label — a
// label on NO page is a gate-bypass artifact the recorder must reject).
func (cp *CheckpointState) OnMenuPage(label string) bool {
	for _, pg := range cp.MenuPages {
		for _, l := range pg.Labels {
			if l == label {
				return true
			}
		}
	}
	return false
}

// IsOfferedRecallTarget reports whether a recall answer's remainder names an
// offered target of this checkpoint: "latest"/empty (the §4 default) or one
// of the persisted RecallTargets labels. The SHIPPED kind-menu recall label
// is a description whose remainder names nothing — the caller maps that back
// to the default (re-review F1).
func (cp *CheckpointState) IsOfferedRecallTarget(target string) bool {
	if target == "" || target == "latest" {
		return true
	}
	for _, t := range cp.RecallTargets {
		if t.Label == target {
			return true
		}
	}
	return false
}

// menuHasRecallAction reports whether a kind's full menu carries a recall
// action (the kinds where a named-target page is meaningful).
func menuHasRecallAction(kind CheckpointKind) bool {
	for _, l := range DecisionMenuLabels(kind) {
		if menuKeyword(l) == "recall" {
			return true
		}
	}
	return false
}

// DecisionMenuLabels returns the FULL legal decision menu of a checkpoint
// kind — the canonical label text: the status render prints these lines, the
// paged projection partitions them, and every label LEADS with the decision
// keyword (the PostToolUse recorder maps the leading word). Single source:
// this function; summoner-status renders it verbatim.
func DecisionMenuLabels(kind CheckpointKind) []string {
	switch kind {
	case CheckpointChangeConfirm:
		return []string{
			"accept: 接受变更并恢复开发",
			"stop: 停止——保留状态退出",
		}
	case CheckpointRecovery:
		return []string{
			"continue: 接受现状，恢复运行",
			"recall: 回城——回到指定历史阶段重做",
			"stop: 停止——保留状态退出",
		}
	case CheckpointReviewDebt:
		// The mandatory ⑤ debt menu: NO done (the debt is what done could
		// not discharge — re-offering it here would wedge); skip IS the
		// explicit UNREVIEWED finish (spec §0 item 2).
		return []string{
			"review: [v] 完成终局审查（默认必经）",
			"skip: 跳过终局审查——以 UNREVIEWED 收尾（不能伪造 PASS）",
			"continue: 返回继续开发",
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

// MenuProjection partitions a kind's full menu into pages of ≤4 options:
// menus that fit one page stay one page (no navigation); larger menus anchor
// `continue` + `stop` on every page and split the remaining actions two per
// page in menu order (the shipped kinds reach every action in ≤2 pages —
// pinned by test).
func MenuProjection(kind CheckpointKind) []MenuPage {
	labels := DecisionMenuLabels(kind)
	if len(labels) <= menuPageMaxOptions {
		return []MenuPage{{Labels: append([]string(nil), labels...)}}
	}
	var anchorFirst, anchorLast string
	var actions []string
	for _, l := range labels {
		switch menuKeyword(l) {
		case "continue":
			anchorFirst = l
		case "stop":
			anchorLast = l
		default:
			actions = append(actions, l)
		}
	}
	pages := make([]MenuPage, 0, (len(actions)+1)/2)
	for i := 0; i < len(actions); i += 2 {
		page := make([]string, 0, menuPageMaxOptions)
		if anchorFirst != "" {
			page = append(page, anchorFirst)
		}
		page = append(page, actions[i])
		if i+1 < len(actions) {
			page = append(page, actions[i+1])
		}
		if anchorLast != "" {
			page = append(page, anchorLast)
		}
		pages = append(pages, MenuPage{Labels: page})
	}
	for i := range pages {
		if i+1 < len(pages) {
			pages[i].NavNext = MenuNavNext
		}
		if i > 0 {
			pages[i].NavPrev = MenuNavPrev
		}
	}
	return pages
}

// menuKeyword extracts a label's decision keyword: the lowercased first word
// with punctuation stripped ("continue: 继续下一阶段" → "continue"). Same
// normalization the PostToolUse recorder applies to answers.
func menuKeyword(label string) string {
	fields := strings.Fields(label)
	if len(fields) == 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range strings.ToLower(fields[0]) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
