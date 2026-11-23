package main

// bashwrite.go — the Bash write-class classifier (0.7.0 D5.3 + D5.5).
//
// During a live run the gate classifies every Bash command: a write-class
// command is treated exactly like Edit/Write (denied under the diagnosis
// freeze, at a pending checkpoint and mid-review; never marked read_class,
// so a crash makes it an outcome-unknown side effect). Bash has no
// structural write signature, so the command TEXT is judged — conservatively:
// a false positive costs one denied command, a false negative costs the
// iron law.
//
// Two channels, judged independently (writeClassRule):
//
//   - REDIRECTS, one at a time (redirectRule). The lexer (shelllex.go)
//     says which '>' are operators, so `ls 2>/dev/null`, `echo "a > b"`,
//     `[[ a > b ]]` and a '>' in a comment or heredoc body are no longer
//     writes. A redirection to a FILE is a write unless the target is
//     exactly /dev/null; a file-descriptor copy (2>&1) or close (>&-) is
//     not a file target. Anything else is a file: `> /dev/null > f`,
//     `>$NULL`, `>~/dev/null` all write.
//
//   - VERBS, per command (commandRule): the command-position words after
//     assignments, wrappers (env / command / nohup) and reserved words
//     (`then rm x`, `time make`, `! rm`). A substitution that IS the first
//     word (`"$(echo rm)" -rf x`) writes: its output becomes the command.
//
// The verb table (D5.5, user ruling 2026-11-23 — the diagnosis freeze
// covers everything that can change the tree, not just file writers):
//
//   - file and process writers: rm mv cp tee chmod chown touch mkdir dd
//     truncate ln install sudo xargs eval, and shells (sh bash zsh);
//   - interpreters with inline code: python -c, node -e, perl -i/-e,
//     awk system() / `| getline` / `print >`, awk -f;
//   - build and run: make, go run, npm i/install/ci/add, npx, pip install;
//   - remote and copy: ssh rsync scp; download to disk: curl -o/-O, wget;
//     unpack: tar -x, unzip; find -exec/-delete;
//   - sed -i, and git: every subcommand outside the read-only table writes
//     (unknown ones included — the default flipped in 0.7.0).
//
// Known residuals (accepted, conservative or out of reach of a text
// classifier): a script run as an argument (`bash x.sh` is write-class
// through its shell; `python3 x.py`, `node x.js` and `./x.sh` are not),
// dd-like writers not in the table, curl/wget variants without the listed
// flags, and compound shell syntax the lexer does not model (case
// patterns). Each of those is a verb the table simply does not name yet.
//
// What a write-class verdict costs depends on the run's status: under the
// diagnosis freeze and at a pending checkpoint the call is denied; while a
// phase is open for work it is ordinary work again.

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

var (
	// writeClassVerbs are write-class in every form.
	writeClassVerbs = map[string]bool{
		"rm": true, "mv": true, "cp": true, "tee": true,
		"chmod": true, "chown": true, "touch": true, "mkdir": true,
		"dd": true, "truncate": true, "ln": true, "install": true,
		"sudo": true, "xargs": true, "eval": true, "exec": true,
		"sh": true, "bash": true, "zsh": true,
		// D5.5 (a)
		"make": true, "npx": true, "ssh": true, "rsync": true, "scp": true, "unzip": true,
		"__ambiguous_wrapper__": true,
	}

	// gitWriteSubcommands are git subcommands that rewrite state (the
	// original table; restore / am / bundle / filter-branch need not be
	// listed — everything outside the read-only table writes).
	gitWriteSubcommands = map[string]bool{
		"commit": true, "push": true, "reset": true, "checkout": true,
		"rebase": true, "merge": true, "stash": true, "switch": true,
		"add": true, "rm": true, "mv": true, "clean": true, "apply": true,
		"cherry-pick": true, "revert": true, "fetch": true, "pull": true,
		"update-ref": true, "update-index": true, "read-tree": true,
		"worktree": true, "init": true, "clone": true, "gc": true,
		"prune": true, "repack": true, "notes": true, "submodule": true,
		"restore": true, "am": true, "bundle": true, "filter-branch": true,
	}

	// gitReadOnlySubcommands are read-only by nature; subcommands whose
	// effect depends on their arguments (branch, tag, config, remote, …)
	// are judged below. Anything in neither table writes.
	gitReadOnlySubcommands = map[string]bool{
		"status": true, "log": true, "diff": true, "show": true,
		"rev-parse": true, "rev-list": true, "ls-files": true, "ls-tree": true, "ls-remote": true,
		"cat-file": true, "describe": true, "blame": true, "annotate": true, "grep": true,
		"shortlog": true, "name-rev": true, "merge-base": true, "for-each-ref": true,
		"check-ignore": true, "check-attr": true, "check-ref-format": true,
		"var": true, "version": true, "help": true, "count-objects": true, "fsck": true,
		"verify-commit": true, "verify-tag": true, "verify-pack": true,
		"whatchanged": true, "cherry": true, "patch-id": true, "dag": true,
	}

	// gitFlagsWithValue — global git flags whose argument must be skipped
	// before the subcommand: git -C /repo commit …
	gitFlagsWithValue = map[string]bool{
		"-C": true, "-c": true, "--git-dir": true, "--work-tree": true,
		"--namespace": true, "--exec-path": true,
	}

	// npmInstallSubcommands and pipInstallSubcommands are the package
	// managers' install forms (D5.5: build and run).
	npmInstallSubcommands = map[string]bool{"i": true, "install": true, "in": true, "ci": true, "add": true}
	pipInstallSubcommands = map[string]bool{"install": true, "uninstall": true}

	// findEffectOptions make find run a command, delete, or write a file.
	findEffectOptions = []string{"-exec", "-ok", "-delete", "-fprint", "-fls"}

	envAssign    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	pythonNameRe = regexp.MustCompile(`^python[0-9.]*$`)

	// shellReservedWords may precede the verb of a command: `then rm x`,
	// `time make`, `! rm`, `{ rm; }`. They are skipped when they come first.
	shellReservedWords = map[string]bool{
		"then": true, "do": true, "else": true, "elif": true, "if": true, "while": true,
		"until": true, "for": true, "case": true, "select": true, "function": true,
		"time": true, "!": true, "{": true, "}": true,
	}
)

// fileTargetOps are the redirection operators whose target is a file.
var fileTargetOps = map[string]bool{
	">": true, ">>": true, ">|": true, "&>": true, "&>>": true, ">&": true, "<>": true,
}

// writeClassRule returns the rule that makes the Bash command write-class,
// or "" when the command is read-class. The rule is what a denial names, so
// the model sees WHY and what to change.
func writeClassRule(command string) string {
	if strings.TrimSpace(command) == "" {
		return ""
	}
	toks := lexShell(command)
	for _, t := range toks {
		if t.kind == shOp && t.text == opLexStop {
			return "shell lexer stopped early (unparseable command text)"
		}
	}
	if rule := redirectRule(toks); rule != "" {
		return rule
	}
	return verbRule(toks)
}

// isWriteClassCommand reports whether the Bash command plausibly writes.
func isWriteClassCommand(command string) bool {
	return writeClassRule(command) != ""
}

// redirectRule judges every redirection on its own.
func redirectRule(toks []shTok) string {
	arith := 0
	for i, t := range toks {
		if t.kind != shOp {
			continue
		}
		switch t.text {
		case opArithOpen:
			arith++
		case opArithClose:
			arith--
		}
		if arith > 0 || !fileTargetOps[t.text] {
			continue // inside arithmetic a '>' is a comparison
		}
		target := ""
		if i+1 < len(toks) && toks[i+1].kind == shWord {
			target = toks[i+1].text
		}
		if target == "/dev/null" {
			continue // the one file target nobody writes
		}
		if t.text == ">&" && (isDigits(target) || target == "-") {
			continue // a file-descriptor copy or close — not a file target
		}
		return fmt.Sprintf("redirect %s to %q", t.text, target)
	}
	return ""
}

// verbRule judges every command of the token stream (commands are separated
// by the break operators) and returns the first rule that matches.
func verbRule(toks []shTok) string {
	if rule := substitutedCommandRule(toks); rule != "" {
		return rule
	}
	start := 0
	for i := 0; i <= len(toks); i++ {
		if i == len(toks) || (toks[i].kind == shOp && shBreaks[toks[i].text]) {
			if rule := commandRule(toks[start:i]); rule != "" {
				return rule
			}
			start = i + 1
		}
	}
	return ""
}

// substitutedCommandRule catches a command substitution that IS the first
// word of a command — `"$(echo rm)" -rf x`: nothing literal stands between
// the substitution and the command position, so its OUTPUT is the command
// name (and words after it are its arguments). That command is unknown until
// it runs, and the nested text says nothing about it (here it is `echo rm`).
// A substitution that merely builds part of a word —
// `$(git rev-parse …)/bin/tool`, `x="$(cat v)"` — is judged by the word it
// sits in, like any other. The rule needs words AFTER the substitution; a
// bare `"$(ls)"` is its nested command alone.
func substitutedCommandRule(toks []shTok) string {
	for i, t := range toks {
		if t.kind != shOp || t.text != opSubstOpen || t.attachedBefore || t.attachedAfter {
			continue
		}
		if i > 0 {
			prev := toks[i-1]
			if !(prev.kind == shOp && (shBreaks[prev.text] || prev.text == "(" || prev.text == ")")) {
				continue // not in command position
			}
		}
		depth := 0
		for j := i; j < len(toks); j++ {
			u := toks[j]
			if u.kind == shOp {
				switch {
				case u.text == opSubstOpen:
					depth++
				case u.text == opSubstClose:
					depth--
				case depth == 0 && shBreaks[u.text] && !(j == i+1 && u.text == ";"):
					return "" // the command ended with no word after the substitution
				}
				continue
			}
			if depth == 0 {
				return "command substitution in command position: $(…) supplies the command"
			}
		}
	}
	return ""
}

// commandRule judges one command's verb.
func commandRule(cmd []shTok) string {
	if len(cmd) == 0 {
		return ""
	}
	var words []string
	for _, t := range cmd {
		if t.kind == shWord {
			words = append(words, t.text)
		}
	}
	for len(words) > 0 && (shellReservedWords[words[0]] || envAssign.MatchString(words[0])) {
		if words[0] == "time" {
			// `time [-p] [-l] [-o FILE] command` — the keyword's own options.
			words = words[1:]
			for len(words) > 0 && (words[0] == "-p" || words[0] == "--portability" || words[0] == "-l" || words[0] == "--list" ||
				strings.HasPrefix(words[0], "--format=") || strings.HasPrefix(words[0], "-f")) {
				words = words[1:]
			}
			if len(words) > 0 && (words[0] == "-o" || words[0] == "--output") {
				words = words[2:] // … and its value
			}
			continue
		}
		words = words[1:]
	}
	if len(words) == 0 {
		return ""
	}
	verbIdx, verb := resolveVerb(words)
	if verbIdx < 0 {
		return ""
	}
	args := words[verbIdx+1:]
	if rule := verbTableRule(verb, args); rule != "" {
		return rule
	}
	return scanningWrapperRule(verb, args)
}

// verbTableRule is the per-verb table: "" when the verb, with these
// arguments, is read-class.
func verbTableRule(verb string, args []string) string {
	switch {
	case verb == "git":
		return gitRule(args)
	case verb == "awk":
		return awkRule(args)
	case verb == "find":
		return findRule(args)
	case verb == "tar":
		return tarRule(args)
	case verb == "curl":
		return curlRule(args)
	case verb == "wget":
		return wgetRule(args)
	case verb == "go":
		if len(args) > 0 && args[0] == "run" {
			return "go run (builds and runs)"
		}
	case verb == "npm":
		if len(args) > 0 && npmInstallSubcommands[args[0]] {
			return "npm " + args[0] + " (installs packages)"
		}
	case verb == "pip" || verb == "pip3":
		if len(args) > 0 && pipInstallSubcommands[args[0]] {
			return "pip " + args[0] + " (changes the environment)"
		}
	case pythonNameRe.MatchString(verb):
		for _, a := range args {
			if a == "--" {
				return ""
			}
			// -c takes the program as an argument; a lone "-" reads it from
			// stdin — inline code in another spelling.
			if len(a) >= 2 && a[0] == '-' && a[1] == 'c' {
				return verb + " -c (inline code)"
			}
			if a == "-" {
				return verb + " - (program read from stdin)"
			}
		}
	case verb == "node":
		for _, a := range args {
			if a == "--" {
				return ""
			}
			if a == "-" {
				return "node - (program read from stdin)"
			}
			if a == "-e" || a == "--eval" || a == "-p" || a == "--print" ||
				strings.HasPrefix(a, "--eval=") || strings.HasPrefix(a, "--print=") {
				return "node (inline code)"
			}
		}
	case verb == "ruby" || verb == "php":
		for _, a := range args {
			if (verb == "ruby" && (a == "-e" || strings.HasPrefix(a, "-e"))) ||
				(verb == "php" && (a == "-r" || strings.HasPrefix(a, "--run="))) {
				return verb + " (inline code)"
			}
		}
	case verb == "perl":
		for _, a := range args {
			if len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsAny(a[1:], "ie") {
				return "perl -i/-e (in-place or inline code)"
			}
		}
	case verb == "sed":
		for _, a := range args {
			if strings.HasPrefix(a, "-i") || strings.Contains(a, "--in-place") {
				return "sed -i (in-place edit)"
			}
		}
	}
	if writeClassVerbs[verb] {
		return "write-class verb: " + verb
	}
	return ""
}

// scanningWrapperRule judges the wrappers whose own options hide the real
// command from a keyword-level reading — `timeout 5 make test`,
// `nice -n 10 go run x`: the command starts after an unknown number of
// option/value words. Every later word is tried as a verb; the first the
// table names wins. The cost is one over-block when an argument happens to
// equal a table verb (`timeout 5 grep rm notes.txt`); the alternative is
// letting a wrapped writer through.
func scanningWrapperRule(verb string, args []string) string {
	switch verb {
	case "timeout", "nice", "watch", "doas", "strace", "ltrace":
	default:
		return ""
	}
	for k, w := range args {
		if w == "--" || strings.HasPrefix(w, "-") || envAssign.MatchString(w) {
			continue
		}
		if rule := verbTableRule(w, args[k+1:]); rule != "" {
			return verb + " … " + rule
		}
	}
	return ""
}

// gitRule judges a git invocation. Read-only subcommands pass; the
// argument-dependent ones are judged by their flags; everything else — the
// write table and any subcommand outside both tables — writes.
func gitRule(args []string) string {
	for j := 0; j < len(args); j++ {
		a := args[j]
		if gitFlagsWithValue[a] {
			if j+1 >= len(args) {
				return "git (unfinished option " + a + ")"
			}
			j++
			continue
		}
		if strings.HasPrefix(a, "-") {
			// Unknown global flags may take a value or dispatch a helper.
			if a == "--no-pager" || a == "--paginate" || a == "--bare" || strings.HasPrefix(a, "--git-dir=") ||
				strings.HasPrefix(a, "--work-tree=") || strings.HasPrefix(a, "--namespace=") ||
				strings.HasPrefix(a, "-c") || strings.HasPrefix(a, "-C") {
				continue
			}
			return "git (unknown option " + a + ")"
		}
		if gitWriteSubcommands[a] {
			return "git " + a
		}
		switch a {
		case "branch", "tag":
			if gitRefMutation(args[j+1:]) {
				return "git " + a + " (create, delete or force)"
			}
			return ""
		case "config":
			for _, flag := range args[j+1:] {
				if flag == "--get" || flag == "--get-all" || flag == "--get-regexp" || flag == "--list" || flag == "-l" {
					return ""
				}
			}
			return "git config (write)"
		case "symbolic-ref":
			if len(args[j+1:]) != 1 || strings.HasPrefix(args[j+1], "-") {
				return "git symbolic-ref (write)"
			}
			return ""
		case "remote":
			rest := args[j+1:]
			if len(rest) == 0 || rest[0] == "-v" || rest[0] == "--verbose" || rest[0] == "show" || rest[0] == "get-url" {
				return ""
			}
			return "git remote " + rest[0]
		case "reflog":
			rest := args[j+1:]
			if len(rest) == 0 || rest[0] == "show" {
				return ""
			}
			return "git reflog " + rest[0]
		case "archive":
			for _, flag := range args[j+1:] {
				if flag == "-o" || flag == "--output" || strings.HasPrefix(flag, "--output=") {
					return "git archive -o (writes an archive)"
				}
			}
			return ""
		case "hash-object":
			for _, flag := range args[j+1:] {
				if strings.HasPrefix(flag, "-w") {
					return "git hash-object -w (writes an object)"
				}
			}
			return ""
		}
		if gitReadOnlySubcommands[a] {
			return ""
		}
		return "git subcommand outside the read-only table: " + a
	}
	return ""
}

// gitRefMutation distinguishes the genuine branch/tag listing forms from
// creation, deletion, force and unknown options. Unknown shapes stay write-class.
func gitRefMutation(args []string) bool {
	if len(args) == 0 {
		return false
	}
	listing := false
	for _, a := range args {
		switch a {
		case "--list", "-l", "--show-current", "--contains", "--no-contains", "--merged", "--no-merged", "--points-at", "-a", "--all", "-r", "--remotes", "-v", "-vv":
			listing = true
		default:
			if strings.HasPrefix(a, "--format=") || strings.HasPrefix(a, "--sort=") {
				listing = true
				continue
			}
			if strings.HasPrefix(a, "-") {
				return true
			}
			if !listing {
				return true
			}
		}
	}
	return !listing
}

// awkRule judges an awk program: it is code. system() and `cmd | getline`
// run commands, `print > "file"` writes, -f runs a program the gate cannot
// see. Comparisons (`NR>=1213`, `a > b`) write nothing.
func awkRule(args []string) string {
	prog := ""
	for k := 0; k < len(args) && prog == ""; k++ {
		a := args[k]
		switch {
		case a == "-f" || a == "--file" || strings.HasPrefix(a, "--file="):
			return "awk -f (program from a file)"
		case a == "-F" || a == "-v" || a == "--field-separator" || a == "--assign":
			k++ // each takes the next argument as its value
		case strings.HasPrefix(a, "-"):
			// a bundled or attached option (-F:, -vx=1, -W …)
		default:
			prog = a
		}
	}
	if prog == "" {
		return ""
	}
	for _, stmt := range strings.FieldsFunc(prog, func(r rune) bool { return r == ';' || r == '{' || r == '}' || r == '\n' }) {
		s := strings.TrimSpace(stmt)
		switch {
		case strings.Contains(s, "system("):
			return "awk system() (runs a command)"
		case strings.HasPrefix(s, "print") && awkOutputRedirect(s):
			return "awk print > (writes a file)"
		case strings.Contains(s, "getline") && strings.Contains(s, "|"):
			return "awk … | getline (runs a command)"
		}
	}
	return ""
}

// awkOutputRedirect reports a '>' that is not the '>=' comparison.
func awkOutputRedirect(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '>' && (i+1 >= len(s) || s[i+1] != '=') {
			return true
		}
	}
	return false
}

// findRule: -exec / -ok run a command, -delete removes, -fprint* write.
func findRule(args []string) string {
	for _, a := range args {
		for _, opt := range findEffectOptions {
			if strings.HasPrefix(a, opt) {
				return "find " + opt + " (runs a command, deletes or writes)"
			}
		}
	}
	return ""
}

// tarRule: an extract flag, in any spelling — -x, -xzf, --extract, --get,
// and the old-style `tar xzf`.
func tarRule(args []string) string {
	for k, a := range args {
		if a == "--extract" || a == "--get" {
			return "tar -x (unpacks)"
		}
		letters := ""
		switch {
		case len(a) > 1 && a[0] == '-' && a[1] != '-':
			letters = a[1:]
		case k == 0 && len(a) > 1 && a[0] != '-':
			letters = a
		}
		if isASCIILetters(letters) && strings.ContainsRune(letters, 'x') {
			return "tar -x (unpacks)"
		}
	}
	return ""
}

// curlRule: curl prints to stdout unless told to save (-o/--output) or to
// keep the remote name (-O). An output target of "-" is still stdout.
func curlRule(args []string) string {
	for k := 0; k < len(args); k++ {
		a := args[k]
		target, saves := "", false
		switch {
		case a == "-o" || a == "--output":
			saves = true
			if k+1 < len(args) {
				target = args[k+1]
				k++
			}
		case strings.HasPrefix(a, "--output="):
			saves, target = true, strings.TrimPrefix(a, "--output=")
		case strings.HasPrefix(a, "-o") && len(a) > 2:
			saves, target = true, a[2:]
		case a == "-O" || a == "--remote-name" || strings.HasPrefix(a, "--remote-name"):
			return "curl -O (downloads to disk)"
		}
		if saves && target != "-" && target != "/dev/null" {
			return "curl -o (downloads to disk)"
		}
	}
	return ""
}

// wgetRule: wget saves to a file by default; only an output document of "-"
// (-O -) keeps it on stdout.
func wgetRule(args []string) string {
	for k := 0; k < len(args); k++ {
		a := args[k]
		switch {
		case a == "-O" || a == "--output-document":
			if k+1 < len(args) {
				if args[k+1] == "-" {
					return ""
				}
				k++
			}
		case strings.HasPrefix(a, "--output-document="):
			if strings.TrimPrefix(a, "--output-document=") == "-" {
				return ""
			}
		case strings.HasPrefix(a, "-O") && len(a) > 2:
			if a[2:] == "-" {
				return ""
			}
		case len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsRune(a[1:], 'O'):
			rest := a[strings.Index(a, "O")+1:]
			if rest == "" {
				if k+1 < len(args) {
					if args[k+1] == "-" {
						return ""
					}
					k++
				}
			} else if rest == "-" {
				return ""
			}
		}
	}
	return "wget (downloads to disk)"
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func isASCIILetters(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

// isStatusInvocation reports whether a Bash command is EXACTLY the canonical
// read-only summoner-status render (T18 budget escape): every command of it
// resolves to the summoner-status binary — the BARE spelling or an explicit
// plugin-bin path ending `/hooks/bin/summoner-status` (the documented
// `${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status` invocation, whatever
// CLAUDE_PLUGIN_ROOT resolves to; commands/status.md is the other source of
// the spelling) — with nothing after it but the canonical herestring payload
// (`"<plugin>/hooks/bin/summoner-status" <<< '{}'`,
// references/checkpoint-protocol.md). Any other token (a flag, an argument,
// a redirect, a second command, another directory prefix, a substituted
// payload) fails the exemption and the command is judged by the ordinary
// write-class and budget rules.
func isStatusInvocation(command string) bool {
	if command == "" {
		return false
	}
	toks := lexShell(command)
	matched := 0
	start := 0
	for i := 0; i <= len(toks); i++ {
		if i == len(toks) || (toks[i].kind == shOp && shBreaks[toks[i].text]) {
			if len(toks[start:i]) > 0 {
				if !isStatusCommand(toks[start:i]) {
					return false
				}
				matched++
			}
			start = i + 1
		}
	}
	return matched > 0
}

// isStatusCommand reports whether one command's tokens are exactly the
// status binary plus at most the canonical herestring payload.
func isStatusCommand(cmd []shTok) bool {
	words := make([]string, 0, len(cmd))
	verbAt := -1 // token index of the verb word
	for _, t := range cmd {
		if t.kind != shWord {
			continue
		}
		words = append(words, t.text)
	}
	verbIdx, verb := resolveVerb(words)
	if verbIdx < 0 || verb != "summoner-status" || !isStatusVerbToken(words[verbIdx]) {
		return false
	}
	seen := -1
	for i, t := range cmd {
		if t.kind == shWord {
			seen++
			if seen == verbIdx {
				verbAt = i
				break
			}
		}
	}
	rest := cmd[verbAt+1:]
	switch {
	case len(rest) == 0:
		return true
	case len(rest) == 2 && rest[0].kind == shOp && rest[0].text == "<<<" && rest[1].kind == shWord:
		return isJSONObjectLiteral(rest[1].text)
	}
	return false
}

// isStatusVerbToken reports whether one verb token is an ACCEPTED spelling
// of the summoner-status binary (F1): the bare `summoner-status`, or an
// explicit plugin-bin path ending `/hooks/bin/summoner-status`. Any other
// directory prefix (`/usr/local/bin/summoner-status`, `./summoner-status`,
// `some/dir/summoner-status`) is a DIFFERENT binary as far as this gate
// knows and never earns the budget exemption.
func isStatusVerbToken(tok string) bool {
	if tok == "summoner-status" {
		return true
	}
	return strings.HasSuffix(tok, "/hooks/bin/summoner-status")
}

// isJSONObjectLiteral reports whether one shell token is a JSON object
// literal (`{}`, `{...}`). It is a shape check for the status herestring
// payload only — never a JSON parser. A token carrying '<' or '>' is
// rejected: a JSON object literal never needs either, and such a "payload"
// is really a redirect — the reviewer's `summoner-status <<< {a:>pwned}`
// probe created a file while staying budget-exempt.
func isJSONObjectLiteral(tok string) bool {
	if len(tok) < 2 || !strings.HasPrefix(tok, "{") || !strings.HasSuffix(tok, "}") {
		return false
	}
	return !strings.ContainsAny(tok, "<>")
}

// resolveVerb finds the real verb of a command's words: it skips leading
// VAR=value assignments, drops the wrappers `env` (plus its flags and
// operands), `command` and `nohup`, and reduces absolute paths to their base
// name (/bin/rm → rm). Returns the verb's word index, or -1 when no verb
// remains (e.g. a bare `env`).
func resolveVerb(words []string) (int, string) {
	i := 0
	for i < len(words) {
		// Leading assignments before anything else: FOO=1 rm x
		if envAssign.MatchString(words[i]) {
			i++
			continue
		}
		base := words[i]
		if strings.ContainsRune(base, '/') {
			base = path.Base(base)
		}
		switch base {
		case "env":
			i++
			for i < len(words) {
				a := words[i]
				if a == "--" {
					i++
					break
				}
				if envAssign.MatchString(a) || a == "-i" || a == "--ignore-environment" || a == "-0" {
					i++
					continue
				}
				if a == "-u" || a == "--unset" || a == "-C" || a == "--chdir" {
					if i+1 >= len(words) {
						return i, "__ambiguous_wrapper__"
					}
					i += 2
					continue
				}
				if strings.HasPrefix(a, "--unset=") || strings.HasPrefix(a, "--chdir=") || (strings.HasPrefix(a, "-u") && len(a) > 2) {
					i++
					continue
				}
				if strings.HasPrefix(a, "-") {
					return i, "__ambiguous_wrapper__"
				}
				break
			}
		case "command", "builtin":
			i++
			for i < len(words) && (words[i] == "-p" || words[i] == "--") {
				i++
			}
			if i < len(words) && (words[i] == "-v" || words[i] == "-V") {
				return -1, ""
			}
			if i < len(words) && strings.HasPrefix(words[i], "-") {
				return i, "__ambiguous_wrapper__"
			}
		case "nohup":
			i++
			if i < len(words) && words[i] == "--" {
				i++
			}
			if i < len(words) && strings.HasPrefix(words[i], "-") {
				return i, "__ambiguous_wrapper__"
			}
		default:
			return i, base
		}
	}
	return -1, ""
}
