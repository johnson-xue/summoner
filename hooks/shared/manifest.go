package shared

// manifest.go — T17 runtime-manifest projection. The hooks consume exactly two
// slices of the project's summoner.yaml: the review route's declared
// verification criteria (ADDITIVE over the framework default set — a manifest
// never replaces the default axes, references/manifest-spec.md) and the
// mechanical-check registry (the CheckDef source ValidateEnvelope freezes
// mechanical criteria against). A missing file is the no-manifest flow
// (nil, nil — framework defaults); an unparseable one fails CLOSED: the freeze
// check and the envelope registry must never silently degrade to defaults on a
// corrupt manifest.
//
// The YAML is decoded through shadow types (yaml tags) into the canonical
// shared types so the check registry round-trips byte-compatibly with the JSON
// CheckDef (CheckDefDigest recomputes identically from either path).

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// RuntimeManifest is the minimal summoner.yaml projection the runtime hooks
// consume.
type RuntimeManifest struct {
	// ReviewCriteriaIDs are the phases.review.verification_spec.criteria ids —
	// additions to DefaultReviewCriteriaIDs, never replacements.
	ReviewCriteriaIDs []string
	// CompatV1 marks a manifest whose on-disk document is version "1": it was
	// translated to the v2 capability-map view IN MEMORY (zero-touch upgrade —
	// the file is never rewritten) and callers may disclose compat mode.
	CompatV1 bool
	// ReadOnlyTools is tool_scope.allowed_tools — tool names the PROJECT
	// declares read-only for its workflows (e.g. LSP-first codebases naming
	// their MCP search tools). The pretooluse gate admits an otherwise
	// unmodeled tool named here as an observed read; modeled mutation tools
	// (Bash/Edit/Write/Agent/...) never consult this list — their admission
	// branches run first, so a declaration cannot smuggle write access.
	ReadOnlyTools []string
	// ReviewCriteria is the same set with COMPLETE meaning (goal/method/
	// check_id, required by default) — the staging path merges these as
	// manifest additions (final-fix wave C1), so a manifest-declared criterion
	// never reaches an envelope as a bare id.
	ReviewCriteria []Criterion
	// CheckRegistry is the manifest's check_registry decoded into the
	// canonical CheckDef (digest field names shared with the JSON form).
	CheckRegistry []CheckDef
	// Phases retains each declared phase's parsed skill binding in either
	// documented form (fix-round N1) — informational for consumers; the
	// runtime's authority is review scope + the check registry, and no routing
	// is built on it here.
	Phases map[string]SkillBinding
	// SuggestedBudgets is the manifest's suggested_budgets projection (T18):
	// the run-start budget SUGGESTION displayed at the first checkpoint for
	// user confirmation — never a pre-authorized limit (references/
	// manifest-spec.md). Nil = no suggestion (the framework default render).
	SuggestedBudgets *BudgetLimits
}

// SkillBinding is one phases.<id>.skill binding in either documented form
// (references/manifest-spec.md): the string form (`skill: name`, including the
// explicit `none`) sets Default only; the expanded mapping form sets Default
// plus the explicitly selected Alternate providers.
type SkillBinding struct {
	Default   string
	Alternate []string
}

// yamlCheckDef mirrors CheckDef with YAML tags (manifest v2 key names).
type yamlCheckDef struct {
	CheckID  string   `yaml:"check_id"`
	Command  string   `yaml:"command"`
	Args     []string `yaml:"args"`
	Cwd      string   `yaml:"cwd"`
	EnvAllow []string `yaml:"env_allow"`
	Digest   string   `yaml:"digest"`
	Trust    string   `yaml:"trust"`
}

// yamlCriterion is one verification_spec.criteria entry (id is the only field
// the runtime projection consumes).
type yamlCriterion struct {
	ID      string `yaml:"id"`
	Goal    string `yaml:"goal"`
	Method  string `yaml:"method"`
	CheckID string `yaml:"check_id"`
}

// yamlVerificationSpec is phases.<phase>.verification_spec.
type yamlVerificationSpec struct {
	Criteria []yamlCriterion `yaml:"criteria"`
	CheckIDs []string        `yaml:"check_ids"`
	Readable []string        `yaml:"readable"`
	Rubric   string          `yaml:"rubric"`
}

// yamlPhase is one phases.<id> entry.
type yamlPhase struct {
	Skill            *yamlSkillBinding     `yaml:"skill"`
	VerificationSpec *yamlVerificationSpec `yaml:"verification_spec"`
}

// yamlSkillBinding decodes BOTH documented skill forms (fix-round N1): the
// string form (`skill: name`, including the explicit `none`) and the expanded
// mapping form (`skill: {default: name, alternate: [names]}`) — the
// validate-manifest schema (references/summoner.schema.json skill_binding) is
// the shape authority; there is no third form. Anything else fails closed with
// an error naming the violated rule (the document walk has already rejected
// nulls, aliases and duplicate keys by the time this runs). T11 twin rule: a
// `default: none` carrying alternates is rejected here exactly as
// validate-manifest rejects it.
type yamlSkillBinding struct {
	Default   string
	Alternate []string
}

func (s *yamlSkillBinding) UnmarshalYAML(node *yaml.Node) error {
	const form = "skill binding must be the string form or the {default, alternate} mapping form"
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Tag != "!!str" || node.Value == "" {
			return fmt.Errorf("phases.<id>.skill line=%d requires a non-empty provider name (the string form or the mapping form)", node.Line)
		}
		s.Default = node.Value
		return nil
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, val := node.Content[i], node.Content[i+1]
			switch key.Value {
			case "default":
				if val.Kind != yaml.ScalarNode || val.Tag != "!!str" || val.Value == "" {
					return fmt.Errorf("phases.<id>.skill.default line=%d must be a string (a non-empty provider name, or none)", val.Line)
				}
				s.Default = val.Value
			case "alternate":
				if val.Kind != yaml.SequenceNode {
					return fmt.Errorf("phases.<id>.skill.alternate line=%d must be a list of provider names (alternates are choices, never a sequence to execute)", val.Line)
				}
				s.Alternate = make([]string, 0, len(val.Content))
				for _, entry := range val.Content {
					if entry.Kind != yaml.ScalarNode || entry.Tag != "!!str" || entry.Value == "" {
						return fmt.Errorf("phases.<id>.skill.alternate line=%d entries must be non-empty strings", entry.Line)
					}
					s.Alternate = append(s.Alternate, entry.Value)
				}
			default:
				return fmt.Errorf("unknown key %q in phases.<id>.skill line=%d — the mapping form is exactly {default, alternate}", key.Value, key.Line)
			}
		}
		if s.Default == "" {
			return fmt.Errorf("phases.<id>.skill line=%d requires a non-empty default (the expanded form names one default provider)", node.Line)
		}
		// T17 residual (closed by T11): validate-manifest rejects a `none`
		// default with alternates ("skill none cannot have alternate
		// providers") — the loader is its twin and rejects the same shape.
		if s.Default == "none" && len(s.Alternate) > 0 {
			return fmt.Errorf("phases.<id>.skill line=%d: skill none cannot have alternate providers", node.Line)
		}
		if len(s.Alternate) == 0 {
			s.Alternate = nil // an empty alternate list is semantically "no alternates"
		}
		return nil
	}
	return fmt.Errorf("phases.<id>.skill line=%d: %s", node.Line, form)
}

// runtimeManifestDoc is the subset of summoner.yaml the runtime reads.
type runtimeManifestDoc struct {
	Version          string                 `yaml:"version"`
	Project          map[string]interface{} `yaml:"project"`
	Phases           map[string]yamlPhase   `yaml:"phases"`
	CheckRegistry    []yamlCheckDef         `yaml:"check_registry"`
	SuggestedBudgets *yamlSuggestedBudgets  `yaml:"suggested_budgets"`
	ToolScope        *yamlToolScope         `yaml:"tool_scope"`
}

// yamlToolScope mirrors the documented tool_scope shape. Only allowed_tools
// is consumed (the ReadOnlyTools projection); readable/writable are
// declarations the runtime does not act on.
type yamlToolScope struct {
	AllowedTools []string `yaml:"allowed_tools"`
	Readable     []string `yaml:"readable"`
	Writable     []string `yaml:"writable"`
}

// yamlSuggestedBudgets mirrors the documented suggested_budgets shape: five
// OPTIONAL positive-integer axes. check_timeout_seconds is tolerated (it is
// a documented axis) but not consumed — BudgetLimits has no timeout axis.
type yamlSuggestedBudgets struct {
	PhaseToolCalls      *int `yaml:"phase_tool_calls"`
	FailedChecks        *int `yaml:"failed_checks"`
	RunRework           *int `yaml:"run_rework"`
	Delegations         *int `yaml:"delegations"`
	CheckTimeoutSeconds *int `yaml:"check_timeout_seconds"`
}

// LoadRuntimeManifest reads ProjectDir()/summoner.yaml. Absent → (nil, nil)
// (the no-manifest flow); a document that is not exactly one v2 mapping with
// string keys → error (fail closed: callers must deny, not default — plain
// garbage like ":::[" parses as anything-but that shape, and a corrupt
// manifest must never silently degrade the freeze check or the check registry
// to framework defaults). Fields beyond the runtime's projection are
// tolerated — the manifest serves skills and CLIs too; only a broken DOCUMENT
// is the runtime's business.
//
// Fix-round I2: the runtime consumes the manifest as review-scope and
// check-registry AUTHORITY, so it enforces the validator's invariants for
// everything it consumes — a manifest the project's own tooling declares
// INVALID must never load as authority. Rejected here: retired keys at any
// mapping depth, YAML aliases/merges, nulls and non-JSON tags, duplicate
// mapping keys, registry entries that are not exact CheckDef shapes
// (unknown fields), non-unique or malformed check ids, empty commands,
// trust outside {framework, project}, digest labels that are not the
// recomputed CheckDefDigest, and verification criteria whose method is
// invalid or whose citations violate the mechanical/judgment split.
// Intentionally TOLERATED divergences from validate-manifest (nothing the
// runtime consumes depends on them): goal/scope/expected are not required on
// criteria, verification_spec.rubric is not required, the check_ids listing
// is not required (mechanical citations are checked against the registry
// itself), and env_allow names are not pattern-checked. Fix-round N1: the
// phases.<id>.skill binding accepts BOTH documented forms (string and the
// {default, alternate} mapping) and retains the parsed binding on
// RuntimeManifest.Phases — informationally; anything beyond the two forms
// rejects, and skill-provider semantics (name patterns, --skills-dir
// resolution) remain validate-manifest's authority. T11 alignment: the two
// former divergences are closed — a `default: none` with alternates and an
// empty/absent phases map are rejected here exactly as the validator rejects
// them (twins pinned in both suites).
func LoadRuntimeManifest() (*RuntimeManifest, error) {
	b, err := os.ReadFile(filepath.Join(ProjectDir(), "summoner.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read summoner.yaml: %w", err)
	}
	// Document-level guards (mirrors validate-manifest's parse contract).
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse summoner.yaml: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("parse summoner.yaml: expected exactly one YAML document")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("parse summoner.yaml: manifest must be an object")
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return nil, fmt.Errorf("parse summoner.yaml: top-level keys must be strings (line %d)", key.Line)
		}
	}
	// v1 zero-touch compat (user ruling 2026-11-16: the upgrade must cost a
	// project nothing). Peek the version BEFORE retired-key validation: a
	// version "1" document is translated to its v2 capability-map view in
	// memory and proceeds through the SAME v2 pipeline — the on-disk file is
	// never rewritten. Only "1" translates; any other version still fails
	// closed below.
	var versionPeek struct {
		Version string `yaml:"version"`
	}
	if err := root.Decode(&versionPeek); err != nil {
		return nil, fmt.Errorf("parse summoner.yaml: %w", err)
	}
	compatV1 := versionPeek.Version == "1"
	if compatV1 {
		translated, err := TranslateV1Manifest(root)
		if err != nil {
			return nil, err
		}
		root = translated
	}
	if err := validateManifestNode(root, "manifest"); err != nil {
		return nil, err
	}
	var mdoc runtimeManifestDoc
	if err := root.Decode(&mdoc); err != nil {
		return nil, fmt.Errorf("parse summoner.yaml: %w", err)
	}
	if mdoc.Version != "2" {
		return nil, fmt.Errorf("parse summoner.yaml: version must be \"2\" (got %q) — migrate via references/manifest-spec.md", mdoc.Version)
	}
	// T17 residual (closed by T11): validate-manifest requires a non-empty
	// phases capability map ("missing_field: phases must be a non-empty
	// capability map"); the loader rejects the same empty/absent shapes — it
	// never loads as authority a manifest the project's own tooling declares
	// invalid.
	if len(mdoc.Phases) == 0 {
		return nil, errors.New("parse summoner.yaml: missing_field: phases must be a non-empty capability map")
	}
	m := &RuntimeManifest{Phases: map[string]SkillBinding{}, CompatV1: compatV1}
	if ts := mdoc.ToolScope; ts != nil && len(ts.AllowedTools) > 0 {
		seen := make(map[string]bool, len(ts.AllowedTools))
		for _, name := range ts.AllowedTools {
			// The gate consumes this list as admission authority for read-only
			// unmodeled tools — a duplicate or empty entry is a broken
			// declaration, not a tolerated divergence.
			if name == "" || seen[name] {
				return nil, fmt.Errorf("parse summoner.yaml: tool_scope.allowed_tools entries must be unique non-empty strings (got %q)", name)
			}
			seen[name] = true
			m.ReadOnlyTools = append(m.ReadOnlyTools, name)
		}
	}
	for id, p := range mdoc.Phases {
		if p.Skill != nil {
			m.Phases[id] = SkillBinding{Default: p.Skill.Default, Alternate: p.Skill.Alternate}
		}
	}
	registry := make(map[string]bool, len(mdoc.CheckRegistry))
	for i, y := range mdoc.CheckRegistry {
		def := CheckDef{
			CheckID:  y.CheckID,
			Command:  y.Command,
			Args:     y.Args,
			Cwd:      y.Cwd,
			EnvAllow: y.EnvAllow,
			Digest:   y.Digest,
			Trust:    y.Trust,
		}
		if err := validateCheckDef(root, i, def, registry); err != nil {
			return nil, err
		}
		registry[def.CheckID] = true
		m.CheckRegistry = append(m.CheckRegistry, def)
	}
	if spec := mdoc.Phases["review"].VerificationSpec; spec != nil {
		seen := make(map[string]bool, len(spec.Criteria))
		for i, c := range spec.Criteria {
			if err := validateManifestCriterion(i, c, registry, seen); err != nil {
				return nil, err
			}
			if c.ID != "" {
				m.ReviewCriteriaIDs = append(m.ReviewCriteriaIDs, c.ID)
				m.ReviewCriteria = append(m.ReviewCriteria, Criterion{
					ID:       c.ID,
					Goal:     c.Goal,
					Method:   CriterionMethod(c.Method),
					CheckID:  c.CheckID,
					Required: true,
					Scope:    "accepted artifacts",
					// A manifest criterion without a stated goal still gets an
					// honest expected-form line naming its source — complete
					// meaning, never a bare id.
					Expected: fmt.Sprintf("As declared in summoner.yaml phases.review.verification_spec.criteria (%s)", c.Method),
				})
			}
		}
	}
	if err := validateSuggestedBudgets(root, mdoc.SuggestedBudgets); err != nil {
		return nil, err
	}
	if y := mdoc.SuggestedBudgets; y != nil {
		var lim BudgetLimits
		set := false
		if y.PhaseToolCalls != nil {
			lim.PhaseToolCalls, set = *y.PhaseToolCalls, true
		}
		if y.FailedChecks != nil {
			lim.FailedChecks, set = *y.FailedChecks, true
		}
		if y.RunRework != nil {
			lim.RunRework, set = *y.RunRework, true
		}
		if y.Delegations != nil {
			lim.Delegations, set = *y.Delegations, true
		}
		// check_timeout_seconds is validated above but not consumed —
		// BudgetLimits has no timeout axis. An object naming none of the four
		// consumed axes projects no suggestion (the framework default offer).
		if set {
			m.SuggestedBudgets = &lim
		}
	}
	return m, nil
}

// --- v1 zero-touch compat translation ----------------------------------------------
//
// TranslateV1Manifest rewrites a parsed version-"1" document into its v2
// capability-map view, in memory. The upgrade contract (user ruling
// 2026-11-16): a project updates the plugin and NOTHING else — its v1
// summoner.yaml keeps working. Deterministic rules, no judgment calls:
//
//   - version "1" → "2"
//   - top-level `workflows`/`routing_rules` (retired topology) are dropped;
//     every phase entry survives, so every capability stays reachable
//   - per-phase `triggers` (retired auto-transitions) are stripped; the
//     triggered phases remain capability keys selected at checkpoints
//   - `debug` + `diagnose` merge into the framework route id `diagnosis`
//     (debug's skill is the default, diagnose's the alternate)
//   - `rpc`/`subsystem`/`gmt`/`migrate` merge into the framework route id
//     `implement` (first present in that priority order is the default)
//   - a `review` entry carrying only a skill binding is dropped (review
//     evidence is the verification_spec, never a skill)
//   - entries already using v2 route ids win — a partially-migrated file is
//     never overridden
//
// The output runs through the SAME v2 validation as a native document: a
// genuinely malformed v1 file still fails closed; compat never lowers the
// bar, it only lifts the version wall.

// v1DiagnosisSources merge into `diagnosis` (first = default, rest = alternates).
var v1DiagnosisSources = []string{"debug", "diagnose"}

// v1ImplementSources merge into `implement` (first present = default).
var v1ImplementSources = []string{"rpc", "subsystem", "gmt", "migrate"}

func TranslateV1Manifest(root *yaml.Node) (*yaml.Node, error) {
	out := &yaml.Node{Kind: yaml.MappingNode, Line: root.Line, Column: root.Column}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, val := root.Content[i], root.Content[i+1]
		switch key.Value {
		case "version":
			out.Content = append(out.Content, key, v1ScalarStr("2", val.Line))
		case "workflows", "routing_rules":
			// Retired topology keys: the framework owns phase order; the
			// phases they routed between all survive below.
		case "phases":
			phases, err := translateV1Phases(val)
			if err != nil {
				return nil, err
			}
			out.Content = append(out.Content, key, phases)
		default:
			out.Content = append(out.Content, key, val)
		}
	}
	return out, nil
}

// translateV1Phases strips retired per-phase keys, drops the retired
// review→skill binding and synthesizes the framework route ids a v1 file
// spells differently. Original key order is preserved; synthesized entries
// append in a fixed order so the translated view is deterministic.
func translateV1Phases(phases *yaml.Node) (*yaml.Node, error) {
	if phases.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("parse summoner.yaml: v1 phases must be a mapping (line %d)", phases.Line)
	}
	entries := make(map[string]*yaml.Node, len(phases.Content)/2)
	original := make(map[string]bool, len(entries))
	out := &yaml.Node{Kind: yaml.MappingNode, Line: phases.Line, Column: phases.Column}
	for i := 0; i+1 < len(phases.Content); i += 2 {
		key, val := phases.Content[i], phases.Content[i+1]
		if key.Kind != yaml.ScalarNode || val.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("parse summoner.yaml: v1 phases entries must be mappings (line %d)", key.Line)
		}
		entry := &yaml.Node{Kind: yaml.MappingNode, Line: val.Line, Column: val.Column}
		for j := 0; j+1 < len(val.Content); j += 2 {
			if val.Content[j].Value == "triggers" {
				continue // retired: checkpoint-side selection replaces auto-trigger
			}
			entry.Content = append(entry.Content, val.Content[j], val.Content[j+1])
		}
		entries[key.Value] = entry
		original[key.Value] = true
	}
	// The v1 review→skill binding is retired; drop the entry when skill is
	// all it carried (a verification_spec-bearing entry is already v2-shaped).
	if rv := entries["review"]; rv != nil && len(rv.Content) == 2 && rv.Content[0].Value == "skill" {
		delete(entries, "review")
	}
	// Synthesize framework route ids the v1 file spells differently; an
	// existing entry always wins (partially-migrated files are not overridden).
	if _, ok := entries["diagnosis"]; !ok {
		if def, alts, line, ok := v1MergeSources(entries, v1DiagnosisSources); ok {
			entries["diagnosis"] = v1BindingEntry(def, alts, line)
		}
	}
	if _, ok := entries["implement"]; !ok {
		if def, alts, line, ok := v1MergeSources(entries, v1ImplementSources); ok {
			entries["implement"] = v1BindingEntry(def, alts, line)
		}
	}
	// Rebuild in original key order, then the synthesized ids in fixed order.
	for i := 0; i+1 < len(phases.Content); i += 2 {
		key := phases.Content[i]
		if entry, ok := entries[key.Value]; ok {
			out.Content = append(out.Content, key, entry)
		}
	}
	for _, id := range []string{"diagnosis", "implement"} {
		if entry, ok := entries[id]; ok && !original[id] {
			out.Content = append(out.Content, v1ScalarStr(id, phases.Line), entry)
		}
	}
	return out, nil
}

// v1MergeSources walks the source phase ids in priority order: the first with
// a skill default becomes the merged default (its own alternates prepend);
// every later source's default appends as an alternate. Sources without a
// skill binding are skipped. Duplicates of the chosen default never append.
func v1MergeSources(entries map[string]*yaml.Node, sources []string) (def string, alts []string, line int, ok bool) {
	for _, id := range sources {
		entryDef, entryAlts := v1SkillOf(entries[id])
		if entryDef == "" || entryDef == "none" {
			continue
		}
		if !ok {
			def, line, ok = entryDef, entries[id].Line, true
			for _, a := range entryAlts {
				if a != def && a != "" {
					alts = append(alts, a)
				}
			}
			continue
		}
		if entryDef != def {
			alts = append(alts, entryDef)
		}
	}
	return def, alts, line, ok
}

// v1SkillOf extracts a phase entry's skill binding in either form: the string
// form yields (name, nil); the mapping form yields (default, alternate).
func v1SkillOf(entry *yaml.Node) (def string, alts []string) {
	if entry == nil {
		return "", nil
	}
	for j := 0; j+1 < len(entry.Content); j += 2 {
		if entry.Content[j].Value != "skill" {
			continue
		}
		s := entry.Content[j+1]
		switch s.Kind {
		case yaml.ScalarNode:
			def = s.Value
		case yaml.MappingNode:
			for k := 0; k+1 < len(s.Content); k += 2 {
				switch s.Content[k].Value {
				case "default":
					def = s.Content[k+1].Value
				case "alternate":
					for _, e := range s.Content[k+1].Content {
						if e.Kind == yaml.ScalarNode {
							alts = append(alts, e.Value)
						}
					}
				}
			}
		}
	}
	return def, alts
}

// v1BindingEntry builds a phase entry node carrying the expanded skill form
// {default, alternate} — the same shape a native v2 document uses.
func v1BindingEntry(def string, alts []string, line int) *yaml.Node {
	skill := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Line: line, Column: 1}
	skill.Content = append(skill.Content, v1ScalarStr("default", line), v1ScalarStr(def, line))
	if len(alts) > 0 {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Line: line, Column: 1}
		for _, a := range alts {
			seq.Content = append(seq.Content, v1ScalarStr(a, line))
		}
		skill.Content = append(skill.Content, v1ScalarStr("alternate", line), seq)
	}
	entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Line: line, Column: 1}
	entry.Content = append(entry.Content, v1ScalarStr("skill", line), skill)
	return entry
}

func v1ScalarStr(v string, line int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v, Line: line, Column: 1}
}

// validateSuggestedBudgets enforces the validator's exact shape on the
// suggested_budgets the runtime consumes (T18): exactly the five known keys
// (a typo'd axis would silently degrade the offer) and positive integers for
// the axes present (absent = no suggestion for that axis). An object naming
// none of the four CONSUMED axes (e.g. only check_timeout_seconds) is legal
// per the validator and simply projects no suggestion — the caller decides
// from the decoded fields whether a BudgetLimits projection exists.
func validateSuggestedBudgets(root *yaml.Node, y *yamlSuggestedBudgets) error {
	if y == nil {
		return nil
	}
	node := manifestMappingEntry(root, "suggested_budgets")
	if node != nil {
		known := map[string]bool{
			"phase_tool_calls": true, "failed_checks": true, "run_rework": true,
			"delegations": true, "check_timeout_seconds": true,
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind == yaml.ScalarNode && !known[key.Value] {
				return fmt.Errorf("parse summoner.yaml: unknown key %q in suggested_budgets line=%d — the axes are phase_tool_calls, failed_checks, run_rework, delegations, check_timeout_seconds", key.Value, key.Line)
			}
		}
	}
	check := func(name string, v *int) error {
		if v == nil {
			return nil
		}
		if *v <= 0 {
			return fmt.Errorf("parse summoner.yaml: suggested_budgets.%s must be a positive integer (got %d) — omit the axis to make no suggestion", name, *v)
		}
		return nil
	}
	for name, v := range map[string]*int{
		"phase_tool_calls":      y.PhaseToolCalls,
		"failed_checks":         y.FailedChecks,
		"run_rework":            y.RunRework,
		"delegations":           y.Delegations,
		"check_timeout_seconds": y.CheckTimeoutSeconds,
	} {
		if err := check(name, v); err != nil {
			return err
		}
	}
	return nil
}

// manifestMappingEntry returns the top-level mapping node under key, or nil
// when the key is absent or not a mapping (the typed decode reports those).
func manifestMappingEntry(root *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key && root.Content[i+1].Kind == yaml.MappingNode {
			return root.Content[i+1]
		}
	}
	return nil
}

// --- validator-mirrored invariants (fix-round I2) -----------------------------------

var (
	manifestIDPattern     = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	manifestDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// manifestRetiredKeys mirrors validate-manifest's retired key map: v1 topology
// keys with actionable v2 replacements. Rejected at ANY mapping depth — a
// retired key nested anywhere still declares topology the single reducer owns.
var manifestRetiredKeys = map[string]string{
	"workflows":     "phases capability map; framework commands own phase order",
	"chain":         "phases capability map; framework commands own phase order",
	"fan_out":       "phases.review.verification_spec and one independent reviewer",
	"graph":         "phases capability map; plans describe tasks, not executable topology",
	"checkpoints":   "the mandatory checkpoint after every phase (no manifest enum)",
	"routing_rules": "phases skill bindings and their default/alternate providers",
	"triggers":      "phases capability bindings; no manifest-driven transitions",
	"mode":          "phases capability map in the single reducer",
}

// validateManifestNode accepts a JSON-compatible YAML subtree only, mirroring
// validate-manifest's yamlValue contract: no aliases/merges, no nulls or
// custom tags, string mapping keys, no duplicate keys, no retired keys — at
// every depth. These constructs must not hide retired keys or erase a
// configured capability underneath the runtime's projection.
func validateManifestNode(n *yaml.Node, path string) error {
	switch n.Kind {
	case yaml.MappingNode:
		seen := make(map[string]bool, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return fmt.Errorf("parse summoner.yaml: invalid key at %s line=%d — use string keys, not YAML merges", path, key.Line)
			}
			child := path + "." + key.Value
			if replacement, retired := manifestRetiredKeys[key.Value]; retired {
				return fmt.Errorf("parse summoner.yaml: retired key %s line=%d — migrate to %s (references/manifest-spec.md)", child, key.Line, replacement)
			}
			if seen[key.Value] {
				return fmt.Errorf("parse summoner.yaml: duplicate key %s line=%d", child, key.Line)
			}
			seen[key.Value] = true
			if err := validateManifestNode(val, child); err != nil {
				return err
			}
		}
		return nil
	case yaml.SequenceNode:
		for i, child := range n.Content {
			if err := validateManifestNode(child, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!str", "!!int", "!!float", "!!bool":
			return nil
		}
		return fmt.Errorf("parse summoner.yaml: invalid node at %s line=%d — nulls, aliases and non-JSON tags are not supported", path, n.Line)
	case yaml.AliasNode:
		return fmt.Errorf("parse summoner.yaml: YAML alias at %s line=%d — nulls, aliases and non-JSON tags are not supported", path, n.Line)
	}
	return fmt.Errorf("parse summoner.yaml: unsupported node at %s line=%d", path, n.Line)
}

// validateCheckDef enforces the validator's per-entry invariants on one
// registry entry the runtime consumes as mechanical-review authority:
// well-formed unique check_id, non-empty command, trust enum, digest label
// equal to the RECOMPUTED CheckDefDigest, and the exact CheckDef yaml shape
// (no unknown fields inside the entry).
func validateCheckDef(root *yaml.Node, idx int, def CheckDef, seen map[string]bool) error {
	const where = "check_registry"
	if !manifestIDPattern.MatchString(def.CheckID) {
		return fmt.Errorf("parse summoner.yaml: %s[%d] requires a valid check_id (lowercase letters, digits, _, -)", where, idx)
	}
	if seen[def.CheckID] {
		return fmt.Errorf("parse summoner.yaml: duplicate check_id %q in %s", def.CheckID, where)
	}
	if def.Command == "" {
		return fmt.Errorf("parse summoner.yaml: %s.%s requires command", where, def.CheckID)
	}
	if def.Trust != "framework" && def.Trust != "project" {
		return fmt.Errorf("parse summoner.yaml: invalid trust on %s.%s — requires trust: framework|project (a declaration, not authorization)", where, def.CheckID)
	}
	if !manifestDigestPattern.MatchString(def.Digest) || def.Digest != CheckDefDigest(def) {
		return fmt.Errorf("parse summoner.yaml: invalid digest on %s.%s — must equal shared.CheckDefDigest: %s; refresh the definition and freeze check_id@digest in the envelope", where, def.CheckID, CheckDefDigest(def))
	}
	// Exact shape: an unknown field inside an entry the runtime freezes
	// against would silently change what the digest commits to.
	if entry := manifestSequenceEntry(root, where, idx); entry != nil {
		known := map[string]bool{"check_id": true, "command": true, "args": true, "cwd": true, "env_allow": true, "digest": true, "trust": true}
		for i := 0; i+1 < len(entry.Content); i += 2 {
			key := entry.Content[i]
			if key.Kind == yaml.ScalarNode && !known[key.Value] {
				return fmt.Errorf("parse summoner.yaml: unknown field %q in %s[%d] — registry entries are exactly the CheckDef shape {check_id, command, args, cwd, env_allow, digest, trust}", key.Value, where, idx)
			}
		}
	}
	return nil
}

// manifestSequenceEntry returns the idx-th entry node of a top-level sequence
// key, or nil when the shape does not match (the typed decode reports those).
func manifestSequenceEntry(root *yaml.Node, key string, idx int) *yaml.Node {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != key || root.Content[i+1].Kind != yaml.SequenceNode {
			continue
		}
		seq := root.Content[i+1]
		if idx < len(seq.Content) {
			return seq.Content[idx]
		}
	}
	return nil
}

// validateManifestCriterion enforces the validator's criterion invariants for
// the ids the runtime freezes as standalone-review scope: unique well-formed
// ids, method ∈ {mechanical, judgment}, mechanical citations pointing at a
// check registered in THIS manifest, judgment criteria citing no check_id.
func validateManifestCriterion(idx int, c yamlCriterion, registry map[string]bool, seen map[string]bool) error {
	where := fmt.Sprintf("phases.review.verification_spec.criteria[%d]", idx)
	if !manifestIDPattern.MatchString(c.ID) || seen[c.ID] {
		return fmt.Errorf("parse summoner.yaml: invalid_criterion at %s — criteria ids must be valid and unique (%q)", where, c.ID)
	}
	seen[c.ID] = true
	switch CriterionMethod(c.Method) {
	case MethodMechanical:
		if !registry[c.CheckID] {
			return fmt.Errorf("parse summoner.yaml: invalid_criterion at %s — mechanical criterion %q must cite a check_id registered in this manifest's check_registry (got %q)", where, c.ID, c.CheckID)
		}
	case MethodJudgment:
		if c.CheckID != "" {
			return fmt.Errorf("parse summoner.yaml: invalid_criterion at %s — judgment criterion %q cites a rubric, not check_id", where, c.ID)
		}
	default:
		return fmt.Errorf("parse summoner.yaml: invalid_criterion at %s — criterion %q method must be mechanical|judgment (got %q)", where, c.ID, c.Method)
	}
	return nil
}

// defaultReviewCriteriaIDs is the framework's full-rubric verification set for
// standalone review/ship routes: the five review axes plus Prove-It. A
// project's verification_spec criteria are ADDITIONS to this set.
var defaultReviewCriteriaIDs = []string{
	"crit-correctness",
	"crit-idiom",
	"crit-architecture",
	"crit-security",
	"crit-impact",
	"crit-prove-it",
}

// DefaultReviewCriteriaIDs returns a copy of the framework default
// verification-criterion ids (callers cannot mutate the package-level set).
func DefaultReviewCriteriaIDs() []string {
	out := make([]string, len(defaultReviewCriteriaIDs))
	copy(out, defaultReviewCriteriaIDs)
	return out
}
