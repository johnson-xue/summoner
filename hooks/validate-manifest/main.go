// validate-manifest validates the v2 capability map. It does not plan or run
// workflows, grant tool permissions, or activate suggested budgets.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/johnson-xue/summoner/hooks/shared"
	"gopkg.in/yaml.v3"
)

// JSON is the wire contract, including the shipped CheckDef tags and digest.
// YAML is decoded without coercing scalar types, then passed through a strict
// JSON decoder so registry definitions cannot silently lose underscore keys.
type manifest struct {
	Version string `json:"version"`
	Project struct {
		Name string `json:"name"`
	} `json:"project"`
	Phases           map[string]*phase `json:"phases"`
	CheckRegistry    []shared.CheckDef `json:"check_registry,omitempty"`
	ToolScope        *toolScope        `json:"tool_scope,omitempty"`
	SuggestedBudgets map[string]int    `json:"suggested_budgets,omitempty"`
}

type phase struct {
	Skill            *skillBinding     `json:"skill,omitempty"`
	VerificationSpec *verificationSpec `json:"verification_spec,omitempty"`
}

// A scalar skill names the unique default provider. The expanded form exposes
// named alternates, never a sequence to execute or multiple default providers.
type skillBinding struct {
	Default   string   `json:"default"`
	Alternate []string `json:"alternate,omitempty"`
}

func (s *skillBinding) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &s.Default)
	}
	type binding skillBinding // avoid recursively calling UnmarshalJSON
	return strictJSON(b, (*binding)(s))
}

type verificationSpec struct {
	Criteria []shared.Criterion `json:"criteria,omitempty"`
	CheckIDs []string           `json:"check_ids,omitempty"`
	Readable []string           `json:"readable,omitempty"`
	Rubric   string             `json:"rubric"`
}

type toolScope struct {
	AllowedTools []string `json:"allowed_tools,omitempty"`
	Readable     []string `json:"readable,omitempty"`
	Writable     []string `json:"writable,omitempty"`
}

var (
	idPattern      = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	skillPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(:[A-Za-z0-9][A-Za-z0-9._-]*)?$`)
	projectPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	envPattern     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// The rejected keys have actionable replacements, not a compatibility parser.
var retiredKeys = map[string]string{
	"workflows":     "phases capability map; framework commands own phase order",
	"chain":         "phases capability map; framework commands own phase order",
	"fan_out":       "phases.review.verification_spec and one independent reviewer",
	"graph":         "phases capability map; plans describe tasks, not executable topology",
	"checkpoints":   "the mandatory checkpoint after every phase (no manifest enum)",
	"routing_rules": "phases skill bindings and their default/alternate providers",
	"triggers":      "phases capability bindings; no manifest-driven transitions",
	"mode":          "phases capability map in the single reducer",
}

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	path, skillsDir := "summoner.yaml", ""
	pathSet, projectNameOnly := false, false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--project-name":
			projectNameOnly = true
		case args[i] == "--skills-dir":
			if i+1 >= len(args) || args[i+1] == "" || strings.HasPrefix(args[i+1], "-") || skillsDir != "" {
				fmt.Fprintln(os.Stderr, "--skills-dir requires one directory value")
				return 2
			}
			i++
			skillsDir = args[i]
		case strings.HasPrefix(args[i], "-") || pathSet:
			fmt.Fprintln(os.Stderr, "usage: validate-manifest [path] [--skills-dir <dir>] [--project-name]")
			return 2
		default:
			path, pathSet = args[i], true
		}
	}

	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "INVALID path=%s\n  - file_not_found: %v\n", path, err)
		return 1
	}
	m, err := parseManifest(b)
	if err == nil {
		err = validateManifest(m, skillsDir)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "INVALID path=%s\n  - %v\n", path, err)
		return 1
	}
	if projectNameOnly {
		fmt.Println(m.Project.Name)
		return 0
	}
	check := "skipped"
	if skillsDir != "" {
		check = "on dir=" + skillsDir
	}
	fmt.Printf("VALID path=%s version=2 project=%s phases=%d checks=%d skills_check=%s\n",
		path, m.Project.Name, len(m.Phases), len(m.CheckRegistry), check)
	return 0
}

func strictJSON(b []byte, into interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(into)
}

func parseManifest(b []byte) (manifest, error) {
	var m manifest
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return m, fmt.Errorf("parse_error: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		return m, fmt.Errorf("parse_error: expected exactly one YAML document")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return m, fmt.Errorf("parse_error: manifest must be an object")
	}
	value, err := yamlValue(doc.Content[0], "manifest")
	if err != nil {
		return m, err
	}
	object := value.(map[string]interface{})
	if object["version"] != "2" {
		return m, fmt.Errorf("invalid_version: expected string \"2\"; migrate to v2 phases capability map (references/manifest-spec.md)")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return m, fmt.Errorf("parse_error: %w", err)
	}
	if err := strictJSON(encoded, &m); err != nil {
		return m, fmt.Errorf("invalid_shape: %w", err)
	}
	return m, nil
}

// Accept a JSON-compatible YAML document only. Reject duplicate keys at EVERY
// depth (including sequence entries), aliases/merges, nulls and custom tags.
// These constructs must not hide retired keys or erase a configured capability.
func yamlValue(n *yaml.Node, path string) (interface{}, error) {
	switch n.Kind {
	case yaml.MappingNode:
		out := make(map[string]interface{}, len(n.Content)/2)
		for i := 0; i < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return nil, fmt.Errorf("invalid_key: %s line=%d; use string keys, not YAML merges", path, key.Line)
			}
			childPath := path + "." + key.Value
			if replacement, retired := retiredKeys[key.Value]; retired {
				return nil, fmt.Errorf("legacy_key: %s line=%d; migrate to v2 %s (references/manifest-spec.md)", childPath, key.Line, replacement)
			}
			if _, exists := out[key.Value]; exists {
				return nil, fmt.Errorf("duplicate_key: %s line=%d", childPath, key.Line)
			}
			value, err := yamlValue(val, childPath)
			if err != nil {
				return nil, err
			}
			out[key.Value] = value
		}
		return out, nil
	case yaml.SequenceNode:
		out := make([]interface{}, 0, len(n.Content))
		for i, child := range n.Content {
			value, err := yamlValue(child, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		return out, nil
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!str", "!!int", "!!float", "!!bool":
			var value interface{}
			if err := n.Decode(&value); err != nil {
				return nil, fmt.Errorf("invalid_scalar: %s: %w", path, err)
			}
			return value, nil
		}
	}
	return nil, fmt.Errorf("invalid_yaml: %s line=%d; nulls, aliases and non-JSON tags are not supported", path, n.Line)
}

func validateManifest(m manifest, skillsDir string) error {
	if !projectPattern.MatchString(m.Project.Name) || strings.Contains(m.Project.Name, "..") {
		return fmt.Errorf("invalid_project: project.name must be a portable namespace (letters/digits, dot, underscore, hyphen; no '..')")
	}
	if len(m.Phases) == 0 {
		return fmt.Errorf("missing_field: phases must be a non-empty capability map")
	}
	registry := make(map[string]shared.CheckDef, len(m.CheckRegistry))
	for i, check := range m.CheckRegistry {
		if !idPattern.MatchString(check.CheckID) || strings.TrimSpace(check.Command) == "" {
			return fmt.Errorf("invalid_check: check_registry[%d] requires check_id and command", i)
		}
		if _, exists := registry[check.CheckID]; exists {
			return fmt.Errorf("duplicate_check: check_id=%s", check.CheckID)
		}
		if check.Trust != "framework" && check.Trust != "project" {
			return fmt.Errorf("invalid_trust: check_id=%s requires trust: framework|project (a declaration, not authorization)", check.CheckID)
		}
		if err := stringList(check.EnvAllow, "check_registry."+check.CheckID+".env_allow"); err != nil {
			return err
		}
		for _, key := range check.EnvAllow {
			if !envPattern.MatchString(key) {
				return fmt.Errorf("invalid_env: check_id=%s env_allow=%q", check.CheckID, key)
			}
		}
		want := shared.CheckDefDigest(check)
		if !digestPattern.MatchString(check.Digest) || check.Digest != want {
			return fmt.Errorf("invalid_digest: check_id=%s digest must equal shared.CheckDefDigest: %s; refresh definition and freeze check_id@digest in the envelope", check.CheckID, want)
		}
		registry[check.CheckID] = check
	}

	// Stable diagnostics for maps: never let Go map iteration choose the error.
	names := make([]string, 0, len(m.Phases))
	for name := range m.Phases {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := m.Phases[name]
		if !idPattern.MatchString(name) || p == nil {
			return fmt.Errorf("invalid_phase: phases.%s must be a named capability object", name)
		}
		if name == "review" {
			if p.Skill != nil || p.VerificationSpec == nil {
				return fmt.Errorf("invalid_review: migrate phases.review to verification_spec (criteria, check_ids, readable, rubric), not a reviewer skill")
			}
			if err := validateVerification(*p.VerificationSpec, registry); err != nil {
				return err
			}
			continue
		}
		if p.Skill == nil || p.VerificationSpec != nil {
			return fmt.Errorf("invalid_phase: phases.%s requires skill; verification_spec belongs to phases.review", name)
		}
		if err := validateSkill(name, *p.Skill, skillsDir); err != nil {
			return err
		}
	}
	if scope := m.ToolScope; scope != nil {
		for _, field := range []struct {
			name   string
			values []string
		}{{"allowed_tools", scope.AllowedTools}, {"readable", scope.Readable}, {"writable", scope.Writable}} {
			if err := stringList(field.values, "tool_scope."+field.name); err != nil {
				return err
			}
		}
	}
	budgetNames := make([]string, 0, len(m.SuggestedBudgets))
	for key := range m.SuggestedBudgets {
		budgetNames = append(budgetNames, key)
	}
	sort.Strings(budgetNames)
	for _, key := range budgetNames {
		switch key {
		case "phase_tool_calls", "failed_checks", "run_rework", "delegations", "check_timeout_seconds":
		default:
			return fmt.Errorf("unknown_budget: suggested_budgets.%s is not a hook-counted limit", key)
		}
		if m.SuggestedBudgets[key] <= 0 {
			return fmt.Errorf("invalid_budget: suggested_budgets.%s must be a positive integer; suggestions require first-checkpoint user confirmation", key)
		}
	}
	return nil
}

func validateSkill(phase string, binding skillBinding, skillsDir string) error {
	providers := append([]string{binding.Default}, binding.Alternate...)
	if err := stringList(providers, "phases."+phase+".skill default/alternate"); err != nil {
		return err
	}
	if binding.Default == "none" && len(binding.Alternate) > 0 {
		return fmt.Errorf("invalid_skill: phases.%s skill none cannot have alternate providers", phase)
	}
	for i, skill := range providers {
		if !skillPattern.MatchString(skill) || (i > 0 && skill == "none") {
			return fmt.Errorf("invalid_skill: phases.%s provider=%q; use a local name or plugin:skill", phase, skill)
		}
		if skillsDir == "" || skill == "none" || strings.Contains(skill, ":") {
			continue // plugin-qualified names resolve through the installed runtime
		}
		info, err := os.Stat(filepath.Join(skillsDir, skill, "SKILL.md"))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("skill_not_found: phase=%s skill=%s dir=%s", phase, skill, skillsDir)
		}
	}
	return nil
}

func validateVerification(spec verificationSpec, registry map[string]shared.CheckDef) error {
	if strings.TrimSpace(spec.Rubric) == "" {
		return fmt.Errorf("missing_field: phases.review.verification_spec.rubric")
	}
	if err := stringList(spec.Readable, "verification_spec.readable"); err != nil {
		return err
	}
	if err := stringList(spec.CheckIDs, "verification_spec.check_ids"); err != nil {
		return err
	}
	allowed := make(map[string]bool, len(spec.CheckIDs))
	for _, id := range spec.CheckIDs {
		if !idPattern.MatchString(id) {
			return fmt.Errorf("invalid_check_ref: verification_spec.check_ids uses bare registered ids; check_id@digest belongs in envelope verification_scope.check_ids")
		}
		if _, ok := registry[id]; !ok {
			return fmt.Errorf("unregistered_check: verification_spec.check_ids references %q", id)
		}
		allowed[id] = true
	}
	seen := make(map[string]bool, len(spec.Criteria))
	for _, criterion := range spec.Criteria {
		if !idPattern.MatchString(criterion.ID) || seen[criterion.ID] {
			return fmt.Errorf("invalid_criterion: criteria ids must be valid and unique: %q", criterion.ID)
		}
		seen[criterion.ID] = true
		if strings.TrimSpace(criterion.Goal) == "" || strings.TrimSpace(criterion.Scope) == "" || strings.TrimSpace(criterion.Expected) == "" {
			return fmt.Errorf("missing_field: criterion=%s requires goal, scope and expected", criterion.ID)
		}
		switch criterion.Method {
		case shared.MethodMechanical:
			if !allowed[criterion.CheckID] {
				return fmt.Errorf("invalid_criterion: criterion=%s must reference a registered check_id listed in verification_spec.check_ids", criterion.ID)
			}
		case shared.MethodJudgment:
			if criterion.CheckID != "" {
				return fmt.Errorf("invalid_criterion: judgment criterion=%s cites a rubric, not check_id", criterion.ID)
			}
		default:
			return fmt.Errorf("invalid_criterion: criterion=%s method must be mechanical|judgment", criterion.ID)
		}
	}
	return nil
}

func stringList(values []string, field string) error {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" || seen[value] {
			return fmt.Errorf("invalid_list: %s requires non-empty unique strings (got %q)", field, value)
		}
		seen[value] = true
	}
	return nil
}
