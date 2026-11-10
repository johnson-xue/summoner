package shared

// manifest_test.go — T17 runtime-manifest projection suite. The loader reads
// the PROJECT ROOT summoner.yaml through ProjectDir() (CLAUDE_PROJECT_DIR in
// tests); a missing file is the no-manifest flow (nil, nil — framework
// defaults), an unparseable one is a fail-closed error.

import (
	"os"
	"path/filepath"
	"reflect"
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
