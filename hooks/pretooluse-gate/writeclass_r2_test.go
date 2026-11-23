package main

// writeclass_r2_test.go — ship review round 2 (classifier lens): the bypasses
// and false denials reproduced in the review, pinned as a table. Each row is a
// command from a real diagnosis session or its natural variant.

import (
	"fmt"
	"strings"
	"testing"
)

func TestWriteClassRound2Matrix(t *testing.T) {
	rows := []struct{ want, cmd string }{
		// ---- bypasses: an interpreter whose program arrives on stdin ----
		{"write", "python3 <<'EOF'\nopen(\"x\",\"w\")\nEOF"},
		{"write", "python3 <<< 'open(\"x\",\"w\")'"},
		{"write", "echo 'open(\"x\",\"w\")' | python3"},
		{"write", "python3 < script.py"},
		{"write", "node <<'EOF'\nrequire('fs').writeFileSync('x','')\nEOF"},
		{"write", "echo 'require(\"fs\").writeFileSync(\"x\",\"\")' | node"},
		{"write", "node < script.js"},
		{"write", "ruby <<< 'File.write(\"x\",\"\")'"},
		{"write", "php <<< '<?php file_put_contents(\"x\",\"y\");'"},
		{"write", "perl <<'EOF'\nunlink \"x\"\nEOF"},
		{"write", "lua <<<'os.remove(\"x\")'"},
		{"write", `perl -E 'unlink "x"'`},
		{"write", `perl -iE 's/a/b/' f`},
		// ---- bypasses: process substitution live in [[ ]] and ${…} ----
		{"write", "[[ -e <(rm -rf x) ]]"},
		{"write", "[[ -e >(rm -rf x) ]]"},
		{"write", "echo ${x:-<(rm -rf y)}"},
		{"write", "[[ -e ${y:-<(rm -rf y)} ]]"},
		// ---- bypasses: arithmetic hiding live substitutions ----
		{"write", "echo $(( $(cat f > out; echo 1) ))"},
		{"write", "(( $(cat f > out) ))"},
		{"write", "cat <<EOF\n$(( $(rm -rf y) ))\nEOF"},
		{"write", "[[ $(( $(rm -rf y) )) -eq 1 ]]"},
		// ---- bypasses: a bare substitution word is swallowed ----
		{"write", "git -C $(pwd) push"},
		{"write", "git -C $(pwd) reset"},
		{"write", "git -C $(pwd) stash"},
		{"write", "env -u $(echo FOO) rm x"},
		{"write", "env -C $(pwd) make"},
		{"write", `X=1 $(echo rm) -rf x`},
		{"write", `! $(echo rm) -rf x`},
		{"write", `if true; then $(echo rm) -rf x; fi`},
		{"write", "`echo rm` -rf x"},
		// ---- bypasses: modelled tools ----
		{"write", "git branch -t newb HEAD"},
		{"write", "git branch -a newb"},
		{"write", "go test -coverprofile=cover.out ./..."},
		{"write", "go test -o out.test ./pkg"},
		{"write", "/usr/bin/time -p make test"},
		{"write", "command time make test"},
		{"write", "RM -rf x"}, // macOS's case-insensitive filesystem runs it
		{"write", "GIT push"},
		{"write", "echo x > $'\\x2fdev/null'"}, // ANSI-C target: not the exempt spelling
		// ---- false denials: read-only commands judged write ----
		{"read", "git config --global --list"},
		{"read", "git config --global user.email"},
		{"read", "git config --local --get user.name"},
		{"read", "git config -f .git/config --list"},
		{"read", "git config --global core.pager"},
		{"read", "git config --global --get-regexp '^alias\\.'"},
		{"read", "npm audit"},
		{"read", "npm audit --json"},
		{"read", "python3 -m pip show requests"},
		{"read", "python3 -m pip list"},
		{"read", "unzip -l x.zip"},
		{"read", "unzip -p x.zip path"},
		{"read", "make -n test"},
		{"read", "bash -n script.sh"},
		{"read", "go tool nm ./bin/x"},
		{"read", "git --git-dir=\"$(pwd)/.git\" status"},
		{"read", "tar -xOf x.tar path"},
		{"read", "echo hi > /dev/stderr"},
		{"read", "echo hi > /dev/stdout"},
		{"read", "echo hi 1>&-"},
		{"read", `awk '{print ($1 > $2)}'`},
		{"read", "watch -n 2 'git status'"},
		// a heredoc body's substitution is an argument, not a command
		{"read", "cat <<EOF\nToday is $(date)\nEOF\necho done"},
		// ---- controls: forms that already work must keep working ----
		{"write", `git -C "$(pwd)" push`},
		{"write", `env -u "$(echo FOO)" rm x`},
		{"write", `"$(echo rm)" -rf x`},
		{"read", "echo $(date)"},
		{"read", "ls $(pwd)"},
		{"read", "git status"},
		{"read", "git log --oneline -5"},
		{"write", "git push"},
		{"read", "cat <<'EOF'\nliteral $(not a command)\nEOF"},
		{"write", "echo x > out.txt"},
		{"read", "echo x > /dev/null"},
		{"read", "python3 -c 'print(1)' == nil"}, // sanity: -c is write below
	}
	rows = rows[:len(rows)-1] // drop the sanity line; -c is a write
	rows = append(rows, struct{ want, cmd string }{"write", "python3 -c 'print(1)'"})
	fail := 0
	for _, r := range rows {
		rule := writeClassRule(r.cmd)
		got := "read"
		if rule != "" {
			got = "write"
		}
		if got != r.want {
			fail++
			t.Errorf("want %s, got %s (rule %q): %q", r.want, got, rule, r.cmd)
		}
	}
	if fail > 0 {
		t.Logf("%d/%d rows mismatched", fail, len(rows))
	}
}

// TestWriteClassDeepNestingFailsClosed — nesting is bounded: past the depth
// cap the lexer stops (lex-stop marker → write) instead of spending seconds
// recursing. 2k-deep took 127 ms and 8k-deep 2.6 s before the cap.
func TestWriteClassDeepNestingFailsClosed(t *testing.T) {
	deep := strings.Repeat("$(", 200) + "ls" + strings.Repeat(")", 200)
	if rule := writeClassRule(deep); rule == "" || !strings.Contains(rule, "lexer stopped") {
		t.Fatalf("200-deep substitution: rule = %q, want the lex-stop fail-closed rule", rule)
	}
	// A plausible deep-but-legal command still classifies normally.
	if rule := writeClassRule("echo $(cat $(dirname $(pwd))/x)"); rule != "" {
		t.Fatalf("3-deep substitution judged write (%q)", rule)
	}
	_ = fmt.Sprintf
}
