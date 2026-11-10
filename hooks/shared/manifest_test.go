package shared

// manifest_test.go — T17 runtime-manifest projection suite. The loader reads
// the PROJECT ROOT summoner.yaml through ProjectDir() (CLAUDE_PROJECT_DIR in
// tests); a missing file is the no-manifest flow (nil, nil — framework
// defaults), an unparseable one is a fail-closed error.

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// setProjectDirForManifest redirects the project dir for one test (go1.16: no
// t.Setenv; same pattern as shared_test.go's setProjectDir).
func setProjectDirForManifest(t *testing.T, dir string) {
	t.Helper()
	old, had := os.LookupEnv("CLAUDE_PROJECT_DIR")
	if err := os.Setenv("CLAUDE_PROJECT_DIR", dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			os.Setenv("CLAUDE_PROJECT_DIR", old)
		} else {
			os.Unsetenv("CLAUDE_PROJECT_DIR")
		}
	})
}

// writeProjectManifest writes summoner.yaml into a fresh temp project dir and
// points the loader at it.
func writeProjectManifest(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "summoner.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	setProjectDirForManifest(t, dir)
	return dir
}

func TestLoadRuntimeManifestAbsent(t *testing.T) {
	setProjectDirForManifest(t, t.TempDir())
	m, err := LoadRuntimeManifest()
	if err != nil {
		t.Fatalf("a missing manifest is the no-manifest flow, not an error: %v", err)
	}
	if m != nil {
		t.Fatalf("absent manifest must load as nil, got %+v", m)
	}
}

func TestLoadRuntimeManifestProjections(t *testing.T) {
	writeProjectManifest(t, `version: "2"
project:
  name: sample-project
phases:
  requirements:
    skill: superpowers:brainstorming
  review:
    verification_spec:
      criteria:
        - id: crit-extra
          goal: logout flow covered
          method: judgment
        - id: crit-authz
          goal: authz regression
          method: mechanical
          check_id: unit
      check_ids: [unit]
      readable: [docs/spec.md]
      rubric: plugin:agents/reviewer.md
check_registry:
  - check_id: unit
    command: go
    args: [test, ./...]
    cwd: .
    env_allow: [PATH]
    digest: d177246245d54d344e2dc1b623abf85e4137d724d7eff0c2e79a8e7cf68d63d4
    trust: project
`)
	m, err := LoadRuntimeManifest()
	if err != nil {
		t.Fatalf("LoadRuntimeManifest: %v", err)
	}
	if m == nil {
		t.Fatal("a present manifest must load")
	}
	if !reflect.DeepEqual(m.ReviewCriteriaIDs, []string{"crit-extra", "crit-authz"}) {
		t.Fatalf("review criteria ids = %v, want [crit-extra crit-authz]", m.ReviewCriteriaIDs)
	}
	if len(m.CheckRegistry) != 1 {
		t.Fatalf("check registry = %+v, want one entry", m.CheckRegistry)
	}
	c := m.CheckRegistry[0]
	if c.CheckID != "unit" || c.Command != "go" || c.Trust != "project" ||
		c.Digest != "d177246245d54d344e2dc1b623abf85e4137d724d7eff0c2e79a8e7cf68d63d4" ||
		!reflect.DeepEqual(c.Args, []string{"test", "./..."}) || !reflect.DeepEqual(c.EnvAllow, []string{"PATH"}) {
		t.Fatalf("check def decoded = %+v — the shared.CheckDef field names must survive the YAML round-trip", c)
	}
	if CheckDefDigest(c) != c.Digest {
		t.Fatalf("the yaml shadow type must decode into shared.CheckDef byte-compatibly: digest %s vs stored %s", CheckDefDigest(c), c.Digest)
	}
}

func TestLoadRuntimeManifestWithoutReviewSpec(t *testing.T) {
	writeProjectManifest(t, `version: "2"
project:
  name: sample-project
phases:
  requirements:
    skill: superpowers:brainstorming
`)
	m, err := LoadRuntimeManifest()
	if err != nil {
		t.Fatalf("LoadRuntimeManifest: %v", err)
	}
	if m == nil || len(m.ReviewCriteriaIDs) != 0 || len(m.CheckRegistry) != 0 {
		t.Fatalf("a manifest without phases.review/check_registry loads empty projections, got %+v", m)
	}
}

func TestLoadRuntimeManifestUnparseableFailsClosed(t *testing.T) {
	for name, content := range map[string]string{
		"not yaml":      ":::: not yaml [\n",
		"not an object": "- just\n- a\n- list\n",
	} {
		t.Run(name, func(t *testing.T) {
			writeProjectManifest(t, content)
			if m, err := LoadRuntimeManifest(); err == nil || m != nil {
				t.Fatalf("an unparseable manifest must fail closed, got (%+v, %v)", m, err)
			}
		})
	}
}

func TestDefaultReviewCriteriaIDs(t *testing.T) {
	ids := DefaultReviewCriteriaIDs()
	want := []string{"crit-correctness", "crit-idiom", "crit-architecture", "crit-security", "crit-impact", "crit-prove-it"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("default full-rubric set = %v, want %v (the single-rubric ⑤ axes + Prove-It)", ids, want)
	}
	// The returned slice is a copy — callers cannot mutate the default.
	ids[0] = "mutated"
	if reflect.DeepEqual(DefaultReviewCriteriaIDs(), ids) {
		t.Fatal("DefaultReviewCriteriaIDs must return a fresh copy")
	}
}

// validManifestDoc is the fixed baseline every invalid case mutates: it loads
// clean (its digest is the true CheckDefDigest of its registry entry).
const validManifestDoc = `version: "2"
project:
  name: sample-project
phases:
  review:
    verification_spec:
      criteria:
        - id: crit-extra
          goal: logout flow covered
          method: judgment
      rubric: plugin:agents/reviewer.md
check_registry:
  - check_id: unit
    command: go
    args: [test, ./...]
    cwd: .
    env_allow: [PATH]
    digest: d177246245d54d344e2dc1b623abf85e4137d724d7eff0c2e79a8e7cf68d63d4
    trust: project
`

// TestLoadRuntimeManifestBaseline: the fixed baseline loads clean — every
// invalid-shape case below is a MINIMAL mutation of it.
func TestLoadRuntimeManifestBaseline(t *testing.T) {
	writeProjectManifest(t, validManifestDoc)
	m, err := LoadRuntimeManifest()
	if err != nil {
		t.Fatalf("baseline manifest must load: %v", err)
	}
	if m == nil || !reflect.DeepEqual(m.ReviewCriteriaIDs, []string{"crit-extra"}) || len(m.CheckRegistry) != 1 {
		t.Fatalf("baseline projection = %+v", m)
	}
}

// --- fix-round 2 (N1): documented expanded skill binding ---------------------------

// TestLoadRuntimeManifestGoldenFixtureLoads is the re-reviewer's exact repro:
// the golden fixture tests/fixtures/manifests/valid-full.yaml — pinned VALID by
// validate-manifest's own suite — declares the expanded mapping skill form and
// must LOAD through the runtime loader (round 1 died with "cannot unmarshal
// !!map into string", denying every phase-submit on review/ship and every
// review-submit for a fully-valid manifest).
func TestLoadRuntimeManifestGoldenFixtureLoads(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "manifests", "valid-full.yaml"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	writeProjectManifest(t, string(b))
	m, err := LoadRuntimeManifest()
	if err != nil {
		t.Fatalf("the validate-manifest-pinned VALID golden fixture must load, got: %v", err)
	}
	if m == nil {
		t.Fatal("a present manifest must load")
	}
	if !reflect.DeepEqual(m.ReviewCriteriaIDs, []string{"unit-tests", "behavior"}) {
		t.Fatalf("review criteria ids = %v, want [unit-tests behavior]", m.ReviewCriteriaIDs)
	}
	if len(m.CheckRegistry) != 1 || m.CheckRegistry[0].CheckID != "unit" {
		t.Fatalf("check registry = %+v, want the fixture's unit entry", m.CheckRegistry)
	}
	// The parsed bindings are retained in BOTH documented forms.
	if got := m.Phases["diagnosis"]; got.Default != "none" || len(got.Alternate) != 0 {
		t.Fatalf("diagnosis binding = %+v, want the string form's default %q", got, "none")
	}
	if got := m.Phases["debug"]; got.Default != "demo-debug" || !reflect.DeepEqual(got.Alternate, []string{"superpowers:systematic-debugging"}) {
		t.Fatalf("debug binding = %+v, want default demo-debug + the declared alternate", got)
	}
	if _, ok := m.Phases["review"]; ok {
		t.Fatalf("phases[%q] = %+v — the review phase declares verification_spec, no skill; nothing to retain", "review", m.Phases["review"])
	}
}

// TestLoadRuntimeManifestMixedSkillForms: both documented forms coexist in one
// manifest — string (`skill: name` / `skill: none`) and mapping (`default` +
// optional `alternate`, empty alternate list legal) — and the parsed bindings
// are retained per phase.
func TestLoadRuntimeManifestMixedSkillForms(t *testing.T) {
	writeProjectManifest(t, `version: "2"
project:
  name: sample-project
phases:
  requirements:
    skill: superpowers:brainstorming
  ops:
    skill: none
  diagnosis:
    skill:
      default: project-diagnosis
  debug:
    skill:
      default: demo-debug
      alternate: [superpowers:systematic-debugging]
  verify:
    skill:
      default: demo-verify
      alternate: []
`)
	m, err := LoadRuntimeManifest()
	if err != nil {
		t.Fatalf("a manifest using both documented skill forms must load: %v", err)
	}
	want := map[string]SkillBinding{
		"requirements": {Default: "superpowers:brainstorming"},
		"ops":          {Default: "none"},
		"diagnosis":    {Default: "project-diagnosis"},
		"debug":        {Default: "demo-debug", Alternate: []string{"superpowers:systematic-debugging"}},
		"verify":       {Default: "demo-verify"},
	}
	if !reflect.DeepEqual(m.Phases, want) {
		t.Fatalf("retained bindings = %+v, want %+v", m.Phases, want)
	}
}

// TestLoadRuntimeManifestMalformedSkillBindingFailsClosed: everything beyond
// the two documented forms rejects with a DISTINCT error naming the shape
// rule — the fail-closed posture of fix-round I2 is preserved.
func TestLoadRuntimeManifestMalformedSkillBindingFailsClosed(t *testing.T) {
	const base = `version: "2"
project:
  name: sample-project
phases:
  debug:
    skill:
%s
`
	cases := map[string]struct {
		skill      string
		wantSubstr string
	}{
		"unknown mapping key": {
			"      default: demo-debug\n      extra: nope",
			"unknown key"},
		"non-string default": {
			"      default: 3",
			"skill.default line=7 must be a string"},
		"missing default": {
			"      alternate: [demo-debug]",
			"requires a non-empty default"},
		"empty mapping": {
			"      {}",
			"requires a non-empty default"},
		"alternate not a list": {
			"      default: demo-debug\n      alternate: demo-other",
			"must be a list of provider names"},
		"alternate entry not a string": {
			"      default: demo-debug\n      alternate: [3]",
			"entries must be non-empty strings"},
		"alternate entry empty string": {
			`      default: demo-debug` + "\n" + `      alternate: [""]`,
			"entries must be non-empty strings"},
		"empty string form": {
			`      ""`,
			"requires a non-empty provider name"},
		"sequence form (no third form)": {
			"      [demo-debug, demo-other]",
			"string form or the {default, alternate} mapping"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			writeProjectManifest(t, fmt.Sprintf(base, tc.skill))
			m, err := LoadRuntimeManifest()
			if err == nil || m != nil {
				t.Fatalf("a malformed skill binding must fail closed, got (%+v, %v)", m, err)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("error %q must name the distinct rule %q", err.Error(), tc.wantSubstr)
			}
		})
	}

	// skill: null is rejected by the document walk (nulls at any depth), pinned
	// here alongside the shape rules.
	writeProjectManifest(t, `version: "2"
project:
  name: sample-project
phases:
  debug:
    skill:
`)
	if m, err := LoadRuntimeManifest(); err == nil || m != nil {
		t.Fatalf("skill: null must fail closed per the null walk, got (%+v, %v)", m, err)
	}
}

// TestLoadRuntimeManifestInvalidShapesFailClosed (fix-round I2): the runtime
// consumes the manifest as review scope and check-registry AUTHORITY, so
// every validator-invalid shape it used to accept must now fail closed at
// load — the runtime must never grant authority the project's own tooling
// declares INVALID. Each case names the invariant it violates.
func TestLoadRuntimeManifestInvalidShapesFailClosed(t *testing.T) {
	cases := map[string]string{
		"wrong stored digest label": strings.Replace(validManifestDoc,
			"digest: d177246245d54d344e2dc1b623abf85e4137d724d7eff0c2e79a8e7cf68d63d4",
			"digest: e177246245d54d344e2dc1b623abf85e4137d724d7eff0c2e79a8e7cf68d63d4", 1),
		"trust typo":    strings.Replace(validManifestDoc, "trust: project", "trust: banana", 1),
		"empty command": strings.Replace(validManifestDoc, "command: go", "command: \"\"", 1),
		"duplicate check_id (byte-identical second entry)": validManifestDoc + `  - check_id: unit
    command: go
    args: [test, ./...]
    cwd: .
    env_allow: [PATH]
    digest: d177246245d54d344e2dc1b623abf85e4137d724d7eff0c2e79a8e7cf68d63d4
    trust: project
`,
		"unknown check entry field": strings.Replace(validManifestDoc, "    command: go", "    command: go\n    extra: nope", 1),
		"judgment criterion citing check_id": strings.Replace(validManifestDoc,
			"          method: judgment\n",
			"          method: judgment\n          check_id: unit\n", 1),
		"mechanical criterion citing unregistered check": strings.Replace(validManifestDoc,
			"          method: judgment\n      rubric:",
			"          method: judgment\n        - id: crit-mech\n          goal: g\n          method: mechanical\n          check_id: ghost\n      rubric:", 1),
		"mechanical criterion without check_id": strings.Replace(validManifestDoc,
			"          method: judgment\n      rubric:",
			"          method: judgment\n        - id: crit-mech\n          goal: g\n          method: mechanical\n      rubric:", 1),
		"criterion method invalid": strings.Replace(validManifestDoc, "method: judgment", "method: vibes", 1),
		"duplicate criterion ids": strings.Replace(validManifestDoc,
			"          method: judgment\n      rubric:",
			"          method: judgment\n        - id: crit-extra\n          goal: g2\n          method: judgment\n      rubric:", 1),
		"retired key chain": "chain: []\n" + validManifestDoc,
		"retired key fan_out nested": strings.Replace(validManifestDoc,
			"phases:\n  review:\n", "phases:\n  review:\n    fan_out: []\n", 1),
		"yaml alias": strings.Replace(validManifestDoc,
			"      rubric: plugin:agents/reviewer.md",
			"      rubric: &rub plugin:agents/reviewer.md\n      readable: [*rub]", 1),
		"null value": strings.Replace(validManifestDoc, "project:\n  name: sample-project", "project:", 1),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			writeProjectManifest(t, content)
			m, err := LoadRuntimeManifest()
			if err == nil || m != nil {
				t.Fatalf("a validator-invalid manifest must fail closed at load, got (%+v, %v)", m, err)
			}
		})
	}
}
