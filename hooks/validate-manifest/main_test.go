package main

import (
	"testing"

	"github.com/johnson-xue/memory-validator"
)

// TestRunArgParsing — CLI surface contract (review found this unguarded):
// flag order-independence, unknown-flag usage error, missing --skills-dir value.
func TestRunArgParsing(t *testing.T) {
	if rc := run([]string{"--skills-dir"}); rc != 2 {
		t.Errorf("--skills-dir without value: rc = %d, want 2", rc)
	}
	if rc := run([]string{"--help"}); rc != 2 {
		t.Errorf("unknown flag: rc = %d, want 2 (usage error, not file_not_found)", rc)
	}
	if rc := run([]string{"/nonexistent/manifest.yaml"}); rc != 1 {
		t.Errorf("missing file: rc = %d, want 1", rc)
	}
}

func TestFilterPluginQualified(t *testing.T) {
	cases := []struct {
		name    string
		errs    []memoryvalidator.ValidationError
		wantOK  bool
		wantLen int
	}{
		{
			name: "plugin-qualified skill_not_found is filtered",
			errs: []memoryvalidator.ValidationError{
				{Code: "skill_not_found", Fields: map[string]string{"phase": "define", "skill": "superpowers:brainstorming"}},
			},
			wantOK:  true,
			wantLen: 0,
		},
		{
			name: "local skill_not_found is fatal",
			errs: []memoryvalidator.ValidationError{
				{Code: "skill_not_found", Fields: map[string]string{"phase": "debug", "skill": "missing-local-skill"}},
			},
			wantOK:  false,
			wantLen: 1,
		},
		{
			name: "unrelated errors pass through",
			errs: []memoryvalidator.ValidationError{
				{Code: "undeclared_phase", Fields: map[string]string{"workflow": "fix", "phase": "ghost"}},
				{Code: "skill_not_found", Fields: map[string]string{"phase": "define", "skill": "summoner:summoner"}},
			},
			wantOK:  false,
			wantLen: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := filterPluginQualified(c.errs)
			if ok != c.wantOK {
				t.Errorf("ok = %v, want %v", ok, c.wantOK)
			}
			if len(got) != c.wantLen {
				t.Errorf("len(errs) = %d, want %d (got %+v)", len(got), c.wantLen, got)
			}
		})
	}
}
