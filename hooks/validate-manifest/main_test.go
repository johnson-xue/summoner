package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestChainReferencesUndeclaredPhase(t *testing.T) {
	// fix 在 chain 里但未在 phases 声明 → 应报错
	manifest := `version: "1"
project:
  name: test-proj
phases:
  debug:
    skill: antia-debug
  verify:
    skill: antia-test
workflows:
  bugfix:
    chain: [debug, fix, verify]
    checkpoints: after_each
`
	p := writeTemp(t, "summoner.yaml", manifest)
	errs, ok := Validate(p)
	if ok {
		t.Fatalf("expected FAIL for undeclared phase 'fix', got PASS; errors=%v", errs)
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e, "fix") && strings.Contains(e, "未声明") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error mentioning undeclared phase 'fix', got %v", errs)
	}
}

func TestValidManifestPasses(t *testing.T) {
	manifest := `version: "1"
project:
  name: test-proj
phases:
  debug:
    skill: antia-debug
  fix:
    skill: antia-subsystem
  verify:
    skill: antia-test
workflows:
  bugfix:
    chain: [debug, fix, verify]
    checkpoints: after_each
`
	p := writeTemp(t, "summoner.yaml", manifest)
	errs, ok := Validate(p)
	if !ok {
		t.Fatalf("expected PASS, got FAIL; errors=%v", errs)
	}
}

func TestMalformedYAMLFails(t *testing.T) {
	manifest := `version: "1"
  project:
    name: [unclosed
`
	p := writeTemp(t, "summoner.yaml", manifest)
	_, ok := Validate(p)
	if ok {
		t.Fatal("expected FAIL for malformed yaml, got PASS")
	}
}
