package shared

// manifest_compat_test.go — zero-touch v1 upgrade suite (user ruling
// 2026-11-16: "summoner 的升级要求零门槛"). A version "1" summoner.yaml must
// load through the SAME v2 pipeline after an in-memory translation; the
// on-disk file is never rewritten. Only "1" translates — any other version
// still fails closed.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// v1CompatFixture mirrors the documented v1 shapes (no project names — the
// framework never hardcodes a project): string skill bindings, debug+diagnose
// split, implement candidates (rpc/subsystem/gmt/migrate), per-phase triggers,
// top-level routing_rules and workflows, and a review phase bound to a skill.
const v1CompatFixture = `version: "1"
project:
  name: sample-project
phases:
  debug:
    skill: skill-code-debug
    triggers: [config]
  diagnose:
    skill: skill-infra-debug
  config:
    skill: skill-config
  proto:
    skill: skill-proto
  migrate:
    skill: skill-migrate
  ops:
    skill: skill-build
  runtime:
    skill: skill-serverctl
  subsystem:
    skill: skill-newsystem
  rpc:
    skill: skill-api
  gmt:
    skill: skill-gmt
  logic:
    skill: skill-logic
  fix:
    skill: skill-logic
  test:
    skill: skill-test
  verify:
    skill: skill-test
  reproduce:
    skill: skill-test
  review:
    skill: skill-docwriter
  plan:
    skill: skill-docwriter
  security:
    skill: none
routing_rules:
  route_by_diagnosis:
    input_field: routing_tag
    map:
      logic: logic
      rpc: rpc
workflows:
  bugfix:
    chain: [debug, reproduce, fix, verify, review]
    checkpoints: after_each
`

func TestLoadRuntimeManifestV1CompatTranslates(t *testing.T) {
	dir := writeProjectManifest(t, v1CompatFixture)
	m, err := LoadRuntimeManifest()
	if err != nil {
		t.Fatalf("a v1 manifest is the zero-touch compat flow, not an error: %v", err)
	}
	if m == nil {
		t.Fatal("translated v1 manifest must load as a manifest, not nil")
	}
	if !m.CompatV1 {
		t.Fatal("a translated v1 manifest must set CompatV1 so callers can disclose compat mode")
	}
	// debug+diagnose merge into the framework route id `diagnosis`.
	if got := m.Phases["diagnosis"]; got.Default != "skill-code-debug" || !reflect.DeepEqual(got.Alternate, []string{"skill-infra-debug"}) {
		t.Fatalf("v1 debug/diagnose must merge into diagnosis {default: skill-code-debug, alternate: [skill-infra-debug]}, got %+v", got)
	}
	// rpc/subsystem/gmt/migrate merge into the framework route id `implement`
	// (first present in that priority order is the default).
	if got := m.Phases["implement"]; got.Default != "skill-api" || !reflect.DeepEqual(got.Alternate, []string{"skill-newsystem", "skill-gmt", "skill-migrate"}) {
		t.Fatalf("v1 rpc/subsystem/gmt/migrate must merge into implement, got %+v", got)
	}
	// fix keeps its v1 default; routing targets stay reachable as their own
	// capability keys (dropped routing_rules never deletes a phase).
	if got := m.Phases["fix"]; got.Default != "skill-logic" || got.Alternate != nil {
		t.Fatalf("v1 fix default must survive verbatim, got %+v", got)
	}
	for _, id := range []string{"config", "proto", "ops", "runtime", "test", "verify", "reproduce", "plan", "security", "logic", "subsystem", "rpc", "gmt", "migrate"} {
		if _, ok := m.Phases[id]; !ok {
			t.Fatalf("v1 capability key %q must survive translation (routing stays reachable)", id)
		}
	}
	// The v1 review→skill binding is retired: review evidence is the
	// verification_spec, never a skill.
	if _, ok := m.Phases["review"]; ok {
		t.Fatal("a v1 review skill binding must be dropped, not loaded as phases.review")
	}
	// Zero-touch pin: the file on disk is byte-identical after the load.
	after, err := os.ReadFile(filepath.Join(dir, "summoner.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != v1CompatFixture {
		t.Fatal("compat translation must never rewrite the on-disk summoner.yaml")
	}
}

func TestLoadRuntimeManifestV1CompatMinimal(t *testing.T) {
	writeProjectManifest(t, `version: "1"
project:
  name: sample-project
phases:
  debug:
    skill: skill-code-debug
  fix:
    skill: skill-logic
`)
	m, err := LoadRuntimeManifest()
	if err != nil {
		t.Fatalf("minimal v1 manifest must translate: %v", err)
	}
	if got := m.Phases["diagnosis"]; got.Default != "skill-code-debug" || got.Alternate != nil {
		t.Fatalf("debug alone must become diagnosis {default, no alternate}, got %+v", got)
	}
	if _, ok := m.Phases["implement"]; ok {
		t.Fatal("no implement candidates in the v1 file means no synthesized implement entry")
	}
}

func TestLoadRuntimeManifestV1CompatImplementPriority(t *testing.T) {
	writeProjectManifest(t, `version: "1"
project:
  name: sample-project
phases:
  subsystem:
    skill: skill-newsystem
  gmt:
    skill: skill-gmt
`)
	m, err := LoadRuntimeManifest()
	if err != nil {
		t.Fatalf("v1 without rpc must still translate: %v", err)
	}
	if got := m.Phases["implement"]; got.Default != "skill-newsystem" || !reflect.DeepEqual(got.Alternate, []string{"skill-gmt"}) {
		t.Fatalf("implement priority is rpc,subsystem,gmt,migrate — subsystem leads when rpc is absent, got %+v", got)
	}
}

func TestLoadRuntimeManifestV1CompatPrefersExistingV2Ids(t *testing.T) {
	// A v1 file that already carries the v2 route id is treated as partially
	// migrated: the existing entry wins and the v1 twin never overrides it.
	writeProjectManifest(t, `version: "1"
project:
  name: sample-project
phases:
  diagnosis:
    skill: skill-existing
  debug:
    skill: skill-code-debug
`)
	m, err := LoadRuntimeManifest()
	if err != nil {
		t.Fatalf("partially-migrated v1 must translate: %v", err)
	}
	if got := m.Phases["diagnosis"]; got.Default != "skill-existing" {
		t.Fatalf("an existing diagnosis entry must win over the v1 debug merge, got %+v", got)
	}
}

func TestLoadRuntimeManifestOtherVersionsStillFailClosed(t *testing.T) {
	writeProjectManifest(t, `version: "3"
project:
  name: sample-project
phases:
  fix:
    skill: skill-logic
`)
	_, err := LoadRuntimeManifest()
	if err == nil || !strings.Contains(err.Error(), `version must be "2"`) {
		t.Fatalf("only version \"1\" translates — anything else fails closed, got %v", err)
	}
}

func TestLoadRuntimeManifestV1CompatTriggersStrippedEverywhere(t *testing.T) {
	// triggers is a retired key rejected at ANY depth for v2 documents; the
	// translator must strip it from every phase entry, not just debug.
	writeProjectManifest(t, `version: "1"
project:
  name: sample-project
phases:
  debug:
    skill: skill-code-debug
    triggers: [config]
  config:
    skill: skill-config
    triggers: [config]
`)
	m, err := LoadRuntimeManifest()
	if err != nil {
		t.Fatalf("triggers on any v1 phase must be stripped in translation: %v", err)
	}
	if _, ok := m.Phases["config"]; !ok {
		t.Fatal("the trigger TARGET phase must survive (its binding is still a capability)")
	}
}

func TestLoadRuntimeManifestToolScopeReadOnlyTools(t *testing.T) {
	writeProjectManifest(t, `version: "2"
project:
  name: sample-project
phases:
  fix:
    skill: skill-logic
tool_scope:
  allowed_tools: ["mcp__graph__search_code", "mcp__graph__get_snippet"]
`)
	m, err := LoadRuntimeManifest()
	if err != nil {
		t.Fatalf("tool_scope.allowed_tools must project as ReadOnlyTools: %v", err)
	}
	if !reflect.DeepEqual(m.ReadOnlyTools, []string{"mcp__graph__search_code", "mcp__graph__get_snippet"}) {
		t.Fatalf("ReadOnlyTools projection mismatch: %+v", m.ReadOnlyTools)
	}
}

func TestLoadRuntimeManifestToolScopeRejectsDuplicatesAndEmpties(t *testing.T) {
	for name, doc := range map[string]string{
		"duplicate": `version: "2"
project:
  name: sample-project
phases:
  fix:
    skill: skill-logic
tool_scope:
  allowed_tools: ["mcp__a__x", "mcp__a__x"]
`,
		"empty": `version: "2"
project:
  name: sample-project
phases:
  fix:
    skill: skill-logic
tool_scope:
  allowed_tools: ["mcp__a__x", ""]
`,
	} {
		writeProjectManifest(t, doc)
		if _, err := LoadRuntimeManifest(); err == nil {
			t.Fatalf("tool_scope.allowed_tools %s entry must fail closed (the gate consumes it as admission authority)", name)
		}
	}
}
