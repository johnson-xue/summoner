package main

// writeclass_test.go — the Bash write-class classifier (0.7.0 D5.3 + D5.5).
//
// Two channels, judged independently:
//
//   - REDIRECTS: every redirection is judged on its own, by a shell-aware
//     lexer (quotes, $'…', escapes, comments, heredoc bodies, $(( )) ). A
//     redirection to a FILE is a write unless the target is exactly
//     /dev/null; a file-descriptor copy (2>&1) is not a file target.
//   - VERBS: the command-position words, per segment.
//
// Before 0.7.0 any '>' anywhere meant "write": `ls … 2>/dev/null` and
// `awk 'NR>=1213 && /^func /' file` — two of the incident run's reads —
// were denied under the diagnosis freeze, while `make`, `rsync`, `ssh` and
// an unknown git subcommand passed as reads.
//
// The classifier returns the RULE that matched, so a denial can name it.

import (
	"strings"
	"testing"

	shared "github.com/johnson-xue/summoner/hooks/shared"
)

func TestWriteClassReadOnlyForms(t *testing.T) {
	cases := map[string]string{
		"incident: ls with stderr discarded":             `ls -la ~/.summoner/state-v2/*/ 2>/dev/null`,
		"incident: awk range with a quoted comparison":   `awk 'NR>=1213 && /^func /' hooks/gate.go`,
		"stderr discarded, stdout copied to stderr":      `cmd 2>/dev/null 1>&2`,
		"append form to /dev/null":                       `cmd >>/dev/null`,
		"both streams to /dev/null":                      `cmd &> /dev/null`,
		"both streams appended to /dev/null":             `cmd &>> /dev/null`,
		"input from /dev/null":                           `cmd </dev/null`,
		"quoted /dev/null target":                        `cmd 2> "/dev/null"`,
		"discard inside a substitution":                  `echo "$(cat hooks/gate.go 2>/dev/null)"`,
		"greater-than inside double quotes":              `echo "a > b"`,
		"greater-than inside single quotes":              `echo 'a > b'`,
		"greater-than dollar-quoted":                     `printf '%s\n' $'a > b'`,
		"escaped redirect sign":                          `echo a \> b`,
		"conditional expression":                         `[[ a > b ]] && echo yes`,
		"conditional with substitutions":                 `[[ "$(cat v)" > 3 ]] && echo big`,
		"arithmetic expansion":                           `echo $((1 > 2))`,
		"arithmetic command":                             `(( 1 > 2 ))`,
		"comment":                                        `ls -la # see docs > notes`,
		"heredoc body":                                   "grep -f - <<'EOF' hooks/gate.go\npattern > here\nEOF",
		"heredoc body, unquoted delimiter, no expansion": "sort <<EOF hooks/gate.go\na > b\nEOF",
		"lexing corner":                                  `echo "$(cat "a>b")"`,
		"herestring":                                     `summoner-status <<< '{}'`,
		"fd copy glued to a discard":                     `cmd 2>/dev/null 1>&2 2>&1`,
	}
	for name, cmd := range cases {
		if rule := writeClassRule(cmd); rule != "" {
			t.Errorf("%s: a read-only form is classified as a write by rule %q (%s)", name, rule, cmd)
		}
		if isWriteClassCommand(cmd) {
			t.Errorf("%s: isWriteClassCommand = true, want false (%s)", name, cmd)
		}
	}
}

func TestWriteClassWriteForms(t *testing.T) {
	cases := map[string]struct{ cmd, rule string }{
		// --- redirects: each one judged on its own -------------------------
		"plain redirect":               {"echo hi > f.txt", ">"},
		"after a /dev/null discard":    {`$(cat f > /dev/null > g)`, "g"},
		"glued after the discard":      {`cmd >/dev/null>g`, "g"},
		"variable target":              {`cmd >$NULL`, ">"},
		"tilde form":                   {`cmd >~/dev/null`, ">"},
		"append":                       {"echo hi >> f.txt", ">>"},
		"clobber":                      {"cmd >| f", ">|"},
		"both streams":                 {"cmd &> f", "&>"},
		"both streams appended":        {"cmd &>> f", "&>>"},
		"read-write open":              {"cmd <> f", "<>"},
		"word form of the fd operator": {"cmd >& f", ">&"},
		"hidden behind a discard":      {"ls 2>/dev/null > notes.txt", "notes.txt"},
		"shell runs an inline script":  {`sh -c 'echo x > "$1"' _ f`, "sh"},

		// --- D5.5 (a): the diagnosis freeze covers these too ---------------
		"python inline code":      {`python3 -c "print(1)"`, "python"},
		"python inline, glued":    {`python -c'import os'`, "python"},
		"node inline code":        {`node -e 'console.log(1)'`, "node"},
		"perl in-place":           {`perl -i -pe 's/a/b/' f`, "perl"},
		"make":                    {"make test", "make"},
		"go run":                  {"go run ./cmd/x", "go run"},
		"npm i":                   {"npm i", "npm"},
		"npm install":             {"npm install --no-audit", "npm"},
		"npx":                     {"npx tsc --noEmit", "npx"},
		"pip install":             {"pip install requests", "pip"},
		"ssh":                     {"ssh host 'ls'", "ssh"},
		"rsync":                   {"rsync -a src/ host:/dst", "rsync"},
		"scp":                     {"scp a host:b", "scp"},
		"curl -o":                 {"curl -s -o out.json https://example.com", "curl"},
		"curl --output":           {"curl --output=out.json https://example.com", "curl"},
		"curl remote-name":        {"curl -O https://example.com/x.tgz", "curl"},
		"wget to disk":            {"wget https://example.com/x.tgz", "wget"},
		"wget -O file":            {"wget -O x.tgz https://example.com", "wget"},
		"tar -x":                  {"tar -xzf x.tgz", "tar"},
		"tar --extract":           {"tar --extract -f x.tar", "tar"},
		"tar old-style flags":     {"tar xzf x.tgz", "tar"},
		"unzip":                   {"unzip x.zip", "unzip"},
		"git restore":             {"git restore x.go", "git"},
		"git am":                  {"git am p.mbox", "git"},
		"git bundle create":       {"git bundle create b.bundle main", "git"},
		"git filter-branch":       {"git filter-branch --tree-filter 'rm x' HEAD", "git"},
		"unknown git subcommand":  {"git frobnicate", "git"},
		"git remote add":          {"git remote add origin git@h:x/y.git", "git"},
		"find -exec":              {`find . -name x -exec rm {} \;`, "find"},
		"find -delete":            {"find . -name '*.tmp' -delete", "find"},
		"awk system()":            {`awk 'BEGIN{system("ls")}'`, "awk"},
		"awk program from a file": {`awk -f probe.awk hooks/gate.go`, "awk"},
		"awk output redirect":     {`awk '{print > "out"}' f`, "awk"},
		"awk pipes a command":     {`awk '{"ls" | getline; print}'`, "awk"},

		// --- verb channel, found while rewriting the lexer ------------------
		"verb behind a reserved word":      {"if true; then rm x; fi", "rm"},
		"verb behind time":                 {"time make test", "make"},
		"verb behind a negation":           {"! rm -rf x", "rm"},
		"verb from a substitution":         {`$(echo rm) -rf x`, "$("},
		"verb behind wrappers and a quote": {`env FOO=1 command "/bin/rm" -rf x`, "rm"},
	}
	for name, c := range cases {
		rule := writeClassRule(c.cmd)
		if rule == "" {
			t.Errorf("%s: a write form is classified as a read (%s)", name, c.cmd)
			continue
		}
		if !strings.Contains(rule, c.rule) {
			t.Errorf("%s: rule = %q, want it to name %q", name, rule, c.rule)
		}
		if !isWriteClassCommand(c.cmd) {
			t.Errorf("%s: isWriteClassCommand = false, want true", name)
		}
	}
}

// TestWriteClassDenialNamesTheRule — acceptance criterion 9 at the gate:
// under the diagnosis freeze every read-only form passes and leaves the
// read_class marker the crash scan keys on; every write form is denied with
// a reason that names the rule it matched. The rule is in the message so the
// model can see WHY and what to change.
func TestWriteClassDenialNamesTheRule(t *testing.T) {
	useGateEnv(t)
	startFixRun(t, shared.BudgetLimits{}) // diagnosis NOT accepted: freeze on

	reads := []string{
		`ls -la ~/.summoner/state-v2/*/ 2>/dev/null`,
		`awk 'NR>=1213 && /^func /' hooks/gate.go`,
		`git status 2>/dev/null`,
		`go test ./... 2>/dev/null`,
	}
	for i, cmd := range reads {
		id := "tu-read-" + string(rune('a'+i))
		code, stderr := callGate(t, toolPayloadID(t, "Bash", id, bashInput(cmd)))
		if code != 0 {
			t.Errorf("read-only form denied under the freeze: %q (stderr: %q)", cmd, stderr)
			continue
		}
		marked := false
		for _, ev := range gateEventsOfType(t, "run-1", shared.EventToolStarted) {
			if ev.ToolUseID == id {
				_, marked = ev.Payload["read_class"]
			}
		}
		if !marked {
			t.Errorf("an admitted read-class call must carry the read_class marker: %q", cmd)
		}
	}

	writes := map[string]string{
		`echo hi > f.txt`:                 ">",
		`ls 2>/dev/null > notes.txt`:      "notes.txt",
		`python3 -c "print(1)"`:           "python",
		`make test`:                       "make",
		`go run ./cmd/x`:                  "go run",
		`ssh host 'ls'`:                   "ssh",
		`rsync -a src/ host:/dst`:         "rsync",
		`curl -s -o out.json https://x.y`: "curl",
		`npm i`:                           "npm",
		`tar -xzf x.tgz`:                  "tar",
		`git restore x.go`:                "git",
		`find . -name x -exec rm {} \\;`:  "find",
		`awk 'BEGIN{system("ls")}'`:       "awk",
		`if true; then rm x; fi`:          "rm",
	}
	for cmd, rule := range writes {
		code, stderr := callGate(t, toolPayload(t, "Bash", bashInput(cmd)))
		if code != 2 {
			t.Errorf("write form admitted under the freeze: %q (stderr: %q)", cmd, stderr)
			continue
		}
		if !strings.Contains(stderr, "diagnosis") {
			t.Errorf("denial of %q must name the freeze: %q", cmd, stderr)
		}
		if !strings.Contains(stderr, "写类规则") || !strings.Contains(stderr, rule) {
			t.Errorf("denial of %q must name the matched rule %q: %q", cmd, rule, stderr)
		}
	}
}

// TestWriteClassAfterDiagnosisStillWork: the freeze is the diagnosis gate —
// once diagnosis is accepted, a write-class command is ordinary work again
// (and is NOT marked read-class: the crash scan treats it as mutating).
func TestWriteClassAfterDiagnosisStillWork(t *testing.T) {
	useGateEnv(t)
	r := startFixRun(t, shared.BudgetLimits{})
	acceptPhase(t, r, "context")
	acceptPhase(t, r, "diagnosis")

	code, stderr := callGate(t, toolPayloadID(t, "Bash", "tu-make", bashInput("make test")))
	if code != 0 {
		t.Fatalf("after diagnosis acceptance a write-class command is ordinary work: exit %d (stderr %q)", code, stderr)
	}
	for _, ev := range gateEventsOfType(t, "run-1", shared.EventToolStarted) {
		if ev.ToolUseID == "tu-make" {
			if _, marked := ev.Payload["read_class"]; marked {
				t.Fatal("a write-class command must never be marked read_class (the crash scan keys on its absence)")
			}
		}
	}
}
