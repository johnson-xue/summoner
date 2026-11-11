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
	m := &RuntimeManifest{Phases: map[string]SkillBinding{}}
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
