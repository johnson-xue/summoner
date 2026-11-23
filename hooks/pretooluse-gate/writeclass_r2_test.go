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
		// make stays write-class in every form: GNU make runs $(shell …)
		// while PARSING, so even -n can execute a command (ship review r3).
		{"write", "make -n test"},
		{"write", "make --dry-run test"},
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
		// ---- ship review round 3 (classifier): holes the round-2 fixes opened ----
		{"write", "bash -c 'rm -rf x' -n"}, // -n AFTER the code is $0: the code runs
		{"write", "bash script.sh -n"},
		{"read", "bash -n script.sh"},
		{"read", "bash -n -c 'rm -rf x'"},
		{"write", "echo 'print(42)' | python3 -W ignore"}, // an option's VALUE is not a script
		{"write", "python3 -X dev < s.py"},
		{"write", "perl -I lib < s.pl"},
		{"write", "ruby -I lib < s.rb"},
		{"write", "php -d foo=bar < s.php"},
		{"write", "node --inspect-port 9229 < s.js"},
		{"write", "lua -l mod < s.lua"},
		{"read", "python3 script.py < in.txt"}, // the program is the file; stdin is data
		{"read", "python3 -W ignore script.py < in.txt"},
		{"write", "python3 < <(echo 'print(1)')"},
		{"write", "node < <(echo code)"},
		{"write", "python3 <(echo 'import os')"},
		{"write", "env -u$(echo FOO) rm x"}, // the value rides inside the option word
		{"write", "env -u$(echo FOO) make"},
		{"read", "env -u$(echo FOO) printenv PATH"},
		{"write", "git --git-dir=$(echo .git) push"},
		{"write", "git --work-tree=$(echo /tmp) clean"},
		{"write", "git --namespace=$(echo ns) stash"},
		{"write", "git --exec-path=$(echo /p) push"},
		{"write", "git --namespace= push"}, // an EMPTY value: the next word is the subcommand
		{"write", "git --exec-path= push"},
		{"read", "git --namespace= status"},
		{"read", "git --git-dir=$(pwd)/.git status"}, // pins attachedAfter (the suffix word)
		{"write", "npm audit fix"},
		{"write", "npm audit fix --force"},
		{"write", "go tool cover -html=c.out -o out.html"},
		{"read", "go tool cover -func=c.out"},
		{"write", "timeout 5 $(echo rm) -rf x"}, // a wrapper runs the computed command
		{"write", `timeout 5 "$(echo rm)" -rf x`},
		{"write", "nice $(echo rm) x"},
		{"write", "/usr/bin/time $(echo rm) x"},
		{"write", "watch -n 2 $(echo rm) x"},
		{"write", "doas $(echo rm) x"},
		{"write", "xcrun $(echo rm) x"},
		{"write", "dash -c 'rm -rf x'"},
		{"write", "ksh -c 'rm -rf x'"},
		{"write", "ENV FOO=1 rm x"}, // the case-insensitive filesystem runs env
		{"write", "NOHUP rm x"},
		{"write", "echo x > /dev/fd/../../tmp/x"},
		{"read", "echo x > /dev/fd/2"},
		{"write", "sed '1e rm -rf x' /dev/null"}, // GNU sed's e command
		{"write", "sed -E 's/x/y/e' f"},
		{"read", "sed 's/a/b/g' f"},
		{"read", "sed -n '1,5p' f"},
		{"read", "sed '/^e/d' f"},
		{"write", "watch -n 2 'rm -rf x'"}, // wrappers that run a command STRING
		{"write", "script -c 'rm -rf x' /dev/null"},
		{"write", "flock /tmp/lock -c 'rm -rf x'"},
		{"read", "git branch --contains HEAD"}, // listing options with a value
		{"read", "git branch --merged main"},
		{"read", "git branch --points-at HEAD"},
		{"read", "cat <<'EOF'\n$(rm -rf x)\nEOF"}, // a quoted delimiter: no expansion
		{"write", "cat <<EOF\n$(rm -rf x)\nEOF"},
		{"read", "unzip -c x.zip"},
		{"read", "tar -x --to-stdout -f x.tar"},
		{"read", "go test -coverprofile /dev/null ./..."},
		{"read", "python3 --version < /dev/null"},
		{"read", "node --version < /dev/null"},
		{"read", "python3 -m pip cache list"},
		{"read", "python3 -m pip index versions x"},
		// ---- the author's own pass over the round-3 machinery ----
		{"write", "python3 0<<'EOF'\nopen(\"x\",\"w\")\nEOF"}, // the fd digit is not an operand
		{"write", "nice python3 < s.py"},                      // stdin state travels into the wrapper
		{"write", "timeout 5 python3 <<'EOF'\nprint(1)\nEOF"},
		{"read", "timeout 5 echo $(date)"}, // a computed ARGUMENT is not the command
		{"write", "ruby - < prog.rb"},
		{"read", "php -f script.php < in.txt"},
		{"write", "env python3 < s.py"},
		{"write", "python3<<<\"$(echo code)\""},
		{"read", "cat <<\"EOF\"\n$(rm -rf x)\nEOF"},
		{"read", "(cd /tmp && ls)"},
		{"write", "(cd /tmp && rm x)"},
		{"read", "f() { ls; }; f"},
		{"write", "python3 -mjson.tool x.json"}, // the attached module spelling
		{"write", "perl -MPOSIX -e 1"},          // a loaded module is code
		{"write", "perl -M POSIX < s.pl"},
		{"write", "git diff --output=d.txt"}, // a read-only subcommand writing its output
		{"write", "git log --output=log.txt"},
		{"write", "rmdir build"}, // file writers by category
		{"write", "patch -p1 < x.patch"},
		{"read", "git diff --stat"},
		{"read", "git log --oneline -3"},
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

// TestWriteClassTarToStdoutInAnyPosition — the doc says `tar -O` reads; the
// round-4 closure review found only the glued spelling did (ship review r4, N6).
func TestWriteClassTarToStdoutInAnyPosition(t *testing.T) {
	for _, cmd := range []string{
		"tar -O -xf x.tar", "tar -x -O -f x.tar", "tar -xf x.tar -O", "tar xf x.tar -O", "tar -O --extract -f x.tar", "tar -xOf x.tar path",
	} {
		if rule := writeClassRule(cmd); rule != "" {
			t.Errorf("%s: read expected (stdout), got %q", cmd, rule)
		}
	}
	for _, cmd := range []string{"tar -r -O -f x.tar", "tar --delete -f x.tar -O", "tar -Af a.tar b.tar", "tar -cf out.tar dir"} {
		if rule := writeClassRule(cmd); rule == "" {
			t.Errorf("%s: an archive is modified, want write", cmd)
		}
	}
}
