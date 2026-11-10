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

	"gopkg.in/yaml.v3"
)

// RuntimeManifest is the minimal summoner.yaml projection the runtime hooks
// consume.
type RuntimeManifest struct {
	// ReviewCriteriaIDs are the phases.review.verification_spec.criteria ids —
	// additions to DefaultReviewCriteriaIDs, never replacements.
	ReviewCriteriaIDs []string
	// CheckRegistry is the manifest's check_registry decoded into the
	// canonical CheckDef (digest field names shared with the JSON form).
	CheckRegistry []CheckDef
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
	Skill            string                `yaml:"skill"`
	VerificationSpec *yamlVerificationSpec `yaml:"verification_spec"`
}

// runtimeManifestDoc is the subset of summoner.yaml the runtime reads.
type runtimeManifestDoc struct {
	Version       string                 `yaml:"version"`
	Project       map[string]interface{} `yaml:"project"`
	Phases        map[string]yamlPhase   `yaml:"phases"`
	CheckRegistry []yamlCheckDef         `yaml:"check_registry"`
}

// LoadRuntimeManifest reads ProjectDir()/summoner.yaml. Absent → (nil, nil)
// (the no-manifest flow); a document that is not exactly one v2 mapping with
// string keys → error (fail closed: callers must deny, not default — plain
// garbage like ":::[" parses as anything-but that shape, and a corrupt
// manifest must never silently degrade the freeze check or the check registry
// to framework defaults). Fields beyond the runtime's projection are
// tolerated — the manifest serves skills and CLIs too; only a broken DOCUMENT
// is the runtime's business.
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
	var mdoc runtimeManifestDoc
	if err := root.Decode(&mdoc); err != nil {
		return nil, fmt.Errorf("parse summoner.yaml: %w", err)
	}
	if mdoc.Version != "2" {
		return nil, fmt.Errorf("parse summoner.yaml: version must be \"2\" (got %q) — migrate via references/manifest-spec.md", mdoc.Version)
	}
	m := &RuntimeManifest{}
	if spec := mdoc.Phases["review"].VerificationSpec; spec != nil {
		for _, c := range spec.Criteria {
			if c.ID != "" {
				m.ReviewCriteriaIDs = append(m.ReviewCriteriaIDs, c.ID)
			}
		}
	}
	for _, y := range mdoc.CheckRegistry {
		m.CheckRegistry = append(m.CheckRegistry, CheckDef{
			CheckID:  y.CheckID,
			Command:  y.Command,
			Args:     y.Args,
			Cwd:      y.Cwd,
			EnvAllow: y.EnvAllow,
			Digest:   y.Digest,
			Trust:    y.Trust,
		})
	}
	return m, nil
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
