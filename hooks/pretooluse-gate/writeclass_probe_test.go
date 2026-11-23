package main

// writeclass_probe_test.go — the classifier must never hang or panic, and
// must hold up beyond the two tables in writeclass_test.go.
//
// Both matter more than any single verdict: the gate runs on EVERY Bash
// call of a live run with a 10-second budget, and a hook that hangs or
// crashes fails closed — one bad command text would block every command
// after it. The first draft of the 0.7.0 lexer looped forever on any
// multi-line command (a newline branch that forgot to advance) and hid the
// verb of a line-continued command; neither table caught either. So every
// case here runs under a watchdog, and a seeded fuzz asserts the two
// structural properties outright.

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

// classifyGuarded runs writeClassRule under a watchdog and a recover.
// ok is false when the classifier hung or panicked.
func classifyGuarded(cmd string) (rule string, ok bool) {
	done := make(chan string, 1)
	go func() {
		defer func() {
			if recover() != nil {
				done <- "!panic"
			}
		}()
		done <- writeClassRule(cmd)
	}()
	select {
	case r := <-done:
		if r == "!panic" {
			return "", false
		}
		return r, true
	case <-time.After(3 * time.Second):
		return "", false
	}
}

// TestWriteClassBeyondTheTables: reads met in real diagnosis work stay
// reads; writers wrapped, continued across lines or fed through stdin stay
// writes.
func TestWriteClassBeyondTheTables(t *testing.T) {
	reads := []string{
		`git diff --stat HEAD~1`, `git -c core.pager=cat log`, `ls -la | grep -v total`, `echo "$HOME"`,
		`for f in *.go; do wc -l "$f"; done`, `while read -r l; do echo "$l"; done < f`, `shasum -a 256 'x'`,
		`sed -n '/^func /p' x.go`, `awk -F: '{print $1}' /etc/passwd`, `tar -tf x.tar`, `curl -s https://example.com | head`,
		`curl -sS -f -o /dev/null https://example.com`, `wget -qO- https://example.com | head`, `go test ./... 2>&1 | tail -20`,
		`python3 --version`, `git branch --show-current`, `git remote -v`, `git config --get user.name`, `git reflog`,
		`git archive HEAD | tar -t`, `test -f x && echo yes`, `[ -f x ]`, `cd /tmp && pwd`, `head -n 5 <(cat f)`,
		`case $x in a) echo;; esac`, `echo "it's > here"`, `echo 'a"b'`, `echo $'\x3e' file`, `echo "$(date)"`,
		`foo 2>&1 >/dev/null`, `echo x 1>&2`, `echo a #b > c`, `[[ -f x ]] && cat x || echo "no > file"`,
		`echo ${x:-y}`, `git log --format='{{.}}'`,
		// multi-line commands, with and without a heredoc on the line
		"ls\ncat f", "ls\r\ncat f", "cd a\nls -la 2>/dev/null\ngit status",
		"cat <<EOF > /dev/null\nx > y\nEOF", "cat <<'EOF'\na > b\nEOF\nls",
		// line continuation: the next line's words are the same command
		"ls && \\\n cat f", "ls \\\n -la", "ls\r\n cat f",
		// the status viewer's own spellings
		`"${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status" <<< '{}'`,
	}
	for _, cmd := range reads {
		rule, ok := classifyGuarded(cmd)
		switch {
		case !ok:
			t.Errorf("read %q: the classifier hung or panicked", cmd)
		case rule != "":
			t.Errorf("read %q is judged a write by %q", cmd, rule)
		}
	}

	writes := map[string]string{
		`echo hi | tee f.txt`:                   "tee",
		`sudo ls`:                               "sudo",
		`bash -c 'ls'`:                          "bash",
		`find . -type f -execdir rm {} +`:       "find",
		`git -C /x checkout -- .`:               "git checkout",
		`git push`:                              "git push",
		`env -i FOO=1 sh -c 'x'`:                "sh",
		`nohup make test &`:                     "make",
		`(cd sub && make)`:                      "make",
		`time npm ci`:                           "npm",
		`x=1; rsync -a a b`:                     "rsync",
		`echo a>b`:                              ">",
		`echo \\> x`:                            ">",
		`: > f`:                                 ">",
		`> f`:                                   ">",
		"cat <<'EOF' | tee out\nx\nEOF":         "tee",
		`echo "$(date)" > log`:                  ">",
		`cmd > >(tee log)`:                      ">",
		"ls && \\\n rm -rf x":                   "rm",
		"ls\r\n rm -rf x":                       "rm",
		"python3 - <<'EOF'\nopen('x','w')\nEOF": "python",
		`node -e "require('fs')"`:               "node",
		`ruby -e 'File.write("x","")'`:          "ruby",
		`time -p make test`:                     "make",
		`builtin rm x`:                          "rm",
		`timeout 5 make test`:                   "make",
		`nice -n 10 go run x`:                   "go run",
		`git lfs pull`:                          "git",
		`watch -n 5 sh -c 'echo x > log'`:       "sh",
	}
	for cmd, want := range writes {
		rule, ok := classifyGuarded(cmd)
		switch {
		case !ok:
			t.Errorf("write %q: the classifier hung or panicked", cmd)
		case rule == "":
			t.Errorf("write %q is judged a read", cmd)
		case !strings.Contains(rule, want):
			t.Errorf("write %q: rule %q does not name %q", cmd, rule, want)
		}
	}
}

// TestWriteClassNeverHangsOrPanics: seeded random command texts built from
// the shell's awkward characters and multi-character operators. Whatever the
// text, the classifier terminates and returns a verdict.
func TestWriteClassNeverHangsOrPanics(t *testing.T) {
	pieces := []string{
		" ", " ", "\t", "\n", "\r", "'", `"`, "`", `\`, "$", "(", ")", "{", "}", "[", "]", "<", ">", ";", "&", "|", "#", "=",
		"*", "?", "/", "-", "_", ":", ".", ",", "a", "A", "1", "é",
		"$(", "$'", "((", "))", "[[", "]]", "<<", "<<<", "<<-", ">>", ">&", "2>", ">", ">|", "&>", "/dev/null", "EOF",
		"rm", "echo", "cat", "make", "git", "status", "python3", "-c", "-i", "find", "-exec", "tar", "-x", "ssh",
	}
	rng := rand.New(rand.NewSource(20261123))
	for n := 0; n < 6000; n++ {
		var b strings.Builder
		for k := rng.Intn(28); k >= 0; k-- {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		cmd := b.String()
		if _, ok := classifyGuarded(cmd); !ok {
			t.Fatalf("input %q made the classifier hang or panic", cmd)
		}
	}
}
