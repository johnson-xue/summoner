package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/johnson-xue/summoner/hooks/shared"
	"gopkg.in/yaml.v3"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "tests", "fixtures", "manifests", name)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Keep CLI tests in-process so they cover the same entry point as the binary.
// No parallel tests: stdout/stderr are temporarily redirected together.
func runOutput(t *testing.T, args ...string) (int, string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = f, f
	defer func() { os.Stdout, os.Stderr = stdout, stderr }()
	rc := run(args)
	return rc, readFile(t, f.Name())
}

func TestRunArgParsing(t *testing.T) {
	for _, args := range [][]string{
		{"--skills-dir"}, {"--help"}, {"--skills-dir", "--help"},
		{fixture(t, "valid-minimal.yaml"), "extra.yaml"},
	} {
		if rc, out := runOutput(t, args...); rc != 2 {
			t.Errorf("args %q: rc=%d, want usage error 2: %s", args, rc, out)
		}
	}
	if rc, _ := runOutput(t, "/nonexistent/manifest.yaml"); rc != 1 {
		t.Errorf("missing file: rc=%d, want 1", rc)
	}
}

func TestV2GoldenManifests(t *testing.T) {
	for _, name := range []string{"valid-minimal.yaml", "valid-full.yaml", "valid-generated.yaml", "valid-with-skills.yaml", "valid-plugin-qualified.yaml"} {
		t.Run(name, func(t *testing.T) {
			rc, out := runOutput(t, fixture(t, name))
			if rc != 0 || !strings.Contains(out, "VALID") || strings.Contains(out, "workflows=") {
				t.Fatalf("v2 capability map must validate without workflow topology: rc=%d %s", rc, out)
			}
		})
	}
}

func TestV2RejectsLegacyWithMigrationHint(t *testing.T) {
	base := readFile(t, fixture(t, "valid-minimal.yaml"))
	for _, tc := range []struct{ name, yaml, replacement string }{
		{"version", strings.Replace(base, `version: "2"`, `version: "1"`, 1), "phases"},
		{"chain", base + "workflows:\n  fix:\n    chain: [diagnosis]\n", "phases"},
		{"fan_out", base + "fan_out: [{persona: reviewer}]\n", "verification_spec"},
		{"graph", base + "graph: {nodes: []}\n", "phases"},
		{"checkpoints", base + "checkpoints: none\n", "checkpoint"},
		{"routing_rules", base + "routing_rules: {}\n", "phases"},
		{"nested", strings.Replace(base, "skill: none", "skill: none\n    graph: {}", 1), "phases"},
		{"legacy-fixture", readFile(t, fixture(t, "legacy-rejected.yaml")), "phases"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "summoner.yaml")
			writeFile(t, path, tc.yaml)
			rc, out := runOutput(t, path)
			if rc != 1 || !strings.Contains(out, "migrate") || !strings.Contains(out, tc.replacement) {
				t.Fatalf("legacy manifest must fail with v2 migration hint (%s): rc=%d %s", tc.replacement, rc, out)
			}
		})
	}
}

func TestV2StrictValidation(t *testing.T) {
	base := readFile(t, fixture(t, "valid-full.yaml"))
	for _, tc := range []struct{ name, old, replacement string }{
		{"numeric-version", `version: "2"`, "version: 2"},
		{"unknown-version", `version: "2"`, `version: "3"`},
		{"empty-project", "name: fixture-project", `name: "  "`},
		{"unknown-top", `version: "2"`, "version: \"2\"\nunknown: true"},
		{"duplicate-top", `version: "2"`, "version: \"2\"\nversion: \"2\""},
		{"null-phase", "  diagnosis:\n    skill: none", "  diagnosis: null"},
		{"unknown-phase-field", "skill: none", "skill: none\n    unknown: true"},
		{"invalid-phase-id", "  diagnosis:", "  BadPhase!:"},
		{"empty-skill", "skill: none", `skill: ""`},
		{"bad-plugin-name", "skill: none", "skill: plugin:"},
		{"variant-no-default", "      default: demo-debug\n", ""},
		{"variant-duplicate-default", "alternate: [superpowers:systematic-debugging]", "alternate: [demo-debug]"},
		{"variant-unknown-field", "alternate: [superpowers:systematic-debugging]", "fallback: [demo-debug]"},
		{"review-is-not-a-skill", "  review:\n    verification_spec:", "  review:\n    skill: superpowers:requesting-code-review\n    verification_spec:"},
		{"review-spec-scalar", "    verification_spec:\n", "    verification_spec: invalid\n"},
		{"review-spec-null", "  diagnosis:\n    skill: none", "  diagnosis:\n    skill: none\n  review: {verification_spec: null}"},
		{"unregistered-check", "check_ids: [unit]", "check_ids: [missing]"},
		{"manifest-not-envelope", "check_ids: [unit]", "check_ids: [unit@digest]"},
		{"criterion-check-missing", "check_id: unit\n", "check_id: missing\n"},
		{"criterion-method", "method: mechanical", "method: automatic"},
		{"criterion-duplicate", "id: behavior", "id: unit-tests"},
		{"criterion-unknown", "goal: Tests pass", "goal: Tests pass\n          verdict: PASS"},
		{"registry-wire-name", "  - check_id: unit", "  - id: unit"},
		{"registry-duplicate-key", "    command: go", "    command: go\n    command: go"},
		{"registry-command-type", "    command: go", "    command: [go, test]"},
		{"registry-stale-digest", "    command: go", "    command: changed"},
		{"registry-digest-missing", "    digest: d177246245d54d344e2dc1b623abf85e4137d724d7eff0c2e79a8e7cf68d63d4\n", ""},
		{"registry-trust", "trust: project", "trust: trusted"},
		{"registry-env-type", "env_allow: [PATH]", "env_allow: {PATH: value}"},
		{"budget-negative", "phase_tool_calls: 40", "phase_tool_calls: -1"},
		{"budget-zero", "phase_tool_calls: 40", "phase_tool_calls: 0"},
		{"budget-string", "phase_tool_calls: 40", `phase_tool_calls: "40"`},
		{"fake-token-budget", "phase_tool_calls: 40", "total_token_budget: 40"},
		{"tool-scope-type", "allowed_tools: [Read, Grep, Bash]", "allowed_tools: Bash"},
		{"tool-scope-unknown", "allowed_tools: [Read, Grep, Bash]", "permissions: allow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(base, tc.old) {
				t.Fatalf("test mutation %q not found in fixture", tc.old)
			}
			path := filepath.Join(t.TempDir(), "summoner.yaml")
			writeFile(t, path, strings.Replace(base, tc.old, tc.replacement, 1))
			if rc, out := runOutput(t, path); rc != 1 {
				t.Fatalf("invalid v2 manifest accepted: rc=%d %s", rc, out)
			}
		})
	}
	for _, contents := range []string{"", "[]", "null", base + "\n---\n" + base, "version: &v \"2\"\nproject: {name: *v}\nphases: {diagnosis: {skill: none}}\n"} {
		path := filepath.Join(t.TempDir(), "summoner.yaml")
		writeFile(t, path, contents)
		if rc, out := runOutput(t, path); rc != 1 {
			t.Errorf("non-manifest/ambiguous YAML accepted: rc=%d %s", rc, out)
		}
	}
}

// TestV2RejectsEmptyPhasesAndNoneWithAlternates (T11 twins): the runtime
// loader (hooks/shared/manifest_test.go) rejects these two shapes at load —
// this pins the CLI authority side so the two can never diverge again.
func TestV2RejectsEmptyPhasesAndNoneWithAlternates(t *testing.T) {
	for _, tc := range []struct{ name, yaml string }{
		{"no-phases-empty-map", "version: \"2\"\nproject:\n  name: sample-project\nphases: {}\n"},
		{"no-phases-key-absent", "version: \"2\"\nproject:\n  name: sample-project\n"},
		{"none-default-with-alternates", "version: \"2\"\nproject:\n  name: sample-project\nphases:\n  diagnosis:\n    skill:\n      default: none\n      alternate: [demo-debug]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "summoner.yaml")
			writeFile(t, path, tc.yaml)
			rc, out := runOutput(t, path)
			if rc != 1 {
				t.Errorf("%s must be INVALID: rc=%d %s", tc.name, rc, out)
			}
		})
	}
}

func TestSkillsCheckLocalAndQualified(t *testing.T) {
	dir := fixture(t, "skills")
	for _, name := range []string{"valid-with-skills.yaml", "valid-plugin-qualified.yaml", "valid-full.yaml"} {
		for _, args := range [][]string{{fixture(t, name), "--skills-dir", dir}, {"--skills-dir", dir, fixture(t, name)}} {
			if rc, out := runOutput(t, args...); rc != 0 || !strings.Contains(out, "skills_check=on") {
				t.Errorf("qualified skills resolve at runtime, local skills on disk: rc=%d %s", rc, out)
			}
		}
	}
	if rc, out := runOutput(t, fixture(t, "invalid-skill-not-found.yaml"), "--skills-dir", dir); rc != 1 || !strings.Contains(out, "skill_not_found") {
		t.Errorf("missing local skill must fail: rc=%d %s", rc, out)
	}
}

func TestV2RegistryEnvelopeCompatibility(t *testing.T) {
	path := fixture(t, "valid-full.yaml")
	if rc, out := runOutput(t, path); rc != 0 {
		t.Fatalf("full manifest: rc=%d %s", rc, out)
	}
	// YAML registry keys must serialize directly to the shipped CheckDef JSON
	// contract, not to a parallel check type with different names or hashing.
	var raw struct {
		Registry []map[string]interface{} `yaml:"check_registry"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, path)), &raw); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(raw.Registry)
	if err != nil {
		t.Fatal(err)
	}
	var defs []shared.CheckDef
	if err := json.Unmarshal(b, &defs); err != nil || len(defs) != 1 {
		t.Fatalf("registry wire decode: %v, %+v", err, defs)
	}
	def := defs[0]
	if def.Digest != shared.CheckDefDigest(def) {
		t.Fatalf("fixture digest does not match shipped CheckDefDigest: %+v", def)
	}
	frozen := shared.FrozenView{Requirements: []shared.FrozenRequirement{{ID: "requirement", CriteriaIDs: []string{"unit-tests"}}}}
	env := shared.Envelope{
		ExitCriteria:      []shared.Criterion{{ID: "unit-tests", Method: shared.MethodMechanical, CheckID: def.CheckID}},
		VerificationScope: shared.VerificationScope{CheckIDs: []string{def.CheckID + "@" + def.Digest}},
	}
	if err := shared.ValidateEnvelope(env, frozen, nil, defs); err != nil {
		t.Fatalf("manifest digest must support frozen check_id@digest: %v", err)
	}
	env.VerificationScope.CheckIDs = []string{def.CheckID}
	if err := shared.ValidateEnvelope(env, frozen, nil, defs); err == nil {
		t.Fatal("bare manifest check id must NOT bypass the envelope freeze")
	}
}

func TestV2SchemaRegistryContract(t *testing.T) {
	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(repoRoot(t), "references", "summoner.schema.json"))), &schema); err != nil {
		t.Fatal(err)
	}
	props := schema["properties"].(map[string]interface{})
	version := props["version"].(map[string]interface{})
	if !reflect.DeepEqual(version["enum"], []interface{}{"2"}) || schema["additionalProperties"] != false {
		t.Fatal("schema must describe ONLY v2 capability maps, with a strict field allow-list")
	}
	defs, ok := schema["definitions"].(map[string]interface{})
	if !ok {
		t.Fatal("schema must define check_def")
	}
	check := defs["check_def"].(map[string]interface{})
	fields := check["properties"].(map[string]interface{})
	typ := reflect.TypeOf(shared.CheckDef{})
	if len(fields) != typ.NumField() {
		t.Fatalf("registry field count diverges from shared.CheckDef: %d vs %d", len(fields), typ.NumField())
	}
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if _, ok := fields[name]; !ok {
			t.Errorf("registry schema missing shipped JSON tag %q", name)
		}
	}
}

// Build the validator under test, not a possibly stale checked-in executable.
// Copy the actual generators unchanged into an isolated installation layout.
func generatorInstallation(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"scripts", "hooks/bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, script := range []string{"summoner-init.sh", "summoner-bp.sh", "summoner-setup.sh", "validate-manifest.sh", "init-memory-db.sh"} {
		path := filepath.Join(root, "scripts", script)
		writeFile(t, path, readFile(t, filepath.Join(repoRoot(t), "scripts", script)))
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "build", "-o", filepath.Join(root, "hooks/bin/validate-manifest"), ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build validator: %v\n%s", err, out)
	}
	return root
}

func TestGeneratorsEmitV2Golden(t *testing.T) {
	root := generatorInstallation(t)
	golden := readFile(t, fixture(t, "valid-generated.yaml"))
	for _, tc := range []struct {
		script string
		args   []string
		input  string
	}{
		{"summoner-init.sh", []string{"--quick", "--project", "fixture-project"}, "\n"},
		{"summoner-init.sh", []string{"3", "--project", "fixture-project"}, strings.Repeat("\n", 10)},
		{"summoner-bp.sh", []string{"--quick", "--project", "fixture-project"}, "\n"},
		{"summoner-bp.sh", []string{"--project", "fixture-project"}, strings.Repeat("\n", 10)},
		{"summoner-setup.sh", []string{"--quick", "--project", "fixture-project"}, "\n"},
	} {
		t.Run(tc.script+strings.Join(tc.args, "-"), func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command("bash", append([]string{filepath.Join(root, "scripts", tc.script)}, tc.args...)...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "SUMMONER_MEMORY_DIR="+filepath.Join(dir, "memory"))
			cmd.Stdin = strings.NewReader(tc.input)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("real generator failed: %v\n%s", err, out)
			}
			path := filepath.Join(dir, "summoner.yaml")
			if rc, out := runOutput(t, path); rc != 0 {
				t.Fatalf("generated manifest must pass v2 validator: rc=%d %s", rc, out)
			}
			if got := readFile(t, path); got != golden {
				t.Errorf("generated capability map differs from golden:\n%s", got)
			}
			if tc.script == "summoner-setup.sh" {
				if _, err := os.Stat(filepath.Join(dir, "memory", "fixture-project.db")); err != nil {
					t.Fatalf("setup must initialize memory in the isolated directory: %v", err)
				}
			}
		})
	}
}

func TestShellValidatorFailsClosedWithoutBinary(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "validate-manifest.sh")
	writeFile(t, script, readFile(t, filepath.Join(repoRoot(t), "scripts", "validate-manifest.sh")))
	cmd := exec.Command("bash", script, fixture(t, "legacy-rejected.yaml"))
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("missing v2 binary must not fall back to a legacy grep validator: %s", out)
	}
}
