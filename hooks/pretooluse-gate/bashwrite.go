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
// Three channels, judged independently (writeClassRule):
//
//   - REDIRECTS, one at a time (redirectRule). The lexer (shelllex.go)
//     says which '>' are operators, so `ls 2>/dev/null`, `echo "a > b"`,
//     `if [[ a > b ]]` and a '>' in a comment, a heredoc body or a
//     parameter expansion are no longer writes. A redirection to a FILE is
//     a write unless the target is exactly /dev/null; a file-descriptor
//     copy (2>&1) or close (>&-) is not a file target. Anything else is a
//     file: `> /dev/null > f`, `>$NULL`, `>~/dev/null` all write.
//
//   - THE COMMAND WORD (commandRule): a command name bash computes when it
//     runs — from a substitution, a backtick, a variable, an arithmetic or
//     parameter expansion, or $'…' — names no known program and writes:
//     `"$(echo rm)" -rf x` runs rm. So does a command supplied outright by
//     a substitution (`"$(echo rm)" -rf x`).
//
//   - VERBS, per command (verbTableRule): the command-position words after
//     assignments, wrappers (env / command / builtin / nohup) and reserved
//     words (`then rm x`, `time make`, `! rm`, `function f {…}`, `coproc`).
//
// The verb table (D5.5, user ruling 2026-11-23 — the diagnosis freeze covers
// everything that can change the tree, not just file writers). The ruling
// names CATEGORIES; a category is covered by the spellings of its tools,
// not by one example each (ship review round 1):
//
//   - file and process writers: rm mv cp tee chmod chown touch mkdir dd
//     truncate ln install sudo xargs eval, shells (sh bash zsh), `source`;
//   - interpreters with inline code: python -c (in any short cluster) and
//     programs read from stdin, python -m <module> outside a small
//     read-only set, node -e/-p/-r/- (in any cluster), perl -i/-e, awk
//     system() / `| "cmd"` / `print >` / -f, sed -i and sed `w file`,
//     gofmt -w;
//   - build and run: make, npx; go build/install/get/generate/fmt/clean/
//     run/mod (writing forms)/env -w; npm/pnpm/yarn/bun/deno run-test-build
//     forms; pip/pipx/poetry/uv install forms, bunx, uvx;
//   - remote and copy: ssh rsync scp; downloads: curl whenever it saves
//     (short options cluster), wget; unpacking and repacking: tar
//     x/c/r/u/A, unzip; find -exec/-delete/-fprint;
//   - wrappers that run a command (timeout nice watch caffeinate …) are
//     judged by trying every later word as a verb;
//   - sed -i, and git: every subcommand outside the read-only table writes
//     (unknown ones included — the default flipped in 0.7.0), and the
//     argument-dependent ones (branch tag config reflog stash worktree …)
//     are judged by their own read/write forms.
//
// `go test` is deliberately read-class: reproducing a failure is diagnosis
// work, and registered project checks commonly run it (`go test -c`, which
// writes a binary, is not).
//
// Known residuals (a text classifier is not a shell): a script run as an
// argument (`python3 x.py`, `node x.js`, `./x.sh`), tools whose write is a
// subcommand the table does not name yet (docker, kubectl, sqlite3, cargo,
// brew, gzip, zip, …), curl long options outside the list, and shell
// constructs the lexer does not model. An allowlist default (unknown verb →
// write, as git already is) would close the class; it is a policy decision.
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
		"bunx": true, "uvx": true,
		// `source` and `.` run a script file.
		"source": true, ".": true,
		// File writers by category (ship review round 3): remove a directory,
		// change a group, remove a link, make a node, wipe, make a temp file,
		// split a file, apply a patch.
		"rmdir": true, "chgrp": true, "unlink": true, "mkfifo": true, "shred": true,
		"mktemp": true, "split": true, "patch": true,
		// Process writers.
		"kill": true, "pkill": true, "killall": true,
		// The other shells (ship review round 3): dash, ksh, fish … run code.
		"dash": true, "ksh": true, "mksh": true, "fish": true, "csh": true, "tcsh": true, "ash": true, "busybox": true,
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
	// are judged below. Anything in neither table writes. Only real git
	// builtins belong here: a name git does not have is an alias, and an
	// alias (`git -c alias.x='!cmd' x`) runs a command.
	gitReadOnlySubcommands = map[string]bool{
		"status": true, "log": true, "diff": true, "show": true,
		"rev-parse": true, "rev-list": true, "ls-files": true, "ls-tree": true, "ls-remote": true,
		"cat-file": true, "describe": true, "blame": true, "annotate": true, "grep": true,
		"shortlog": true, "name-rev": true, "merge-base": true, "for-each-ref": true, "merge-tree": true,
		"check-ignore": true, "check-attr": true, "check-ref-format": true,
		"var": true, "version": true, "help": true, "count-objects": true, "fsck": true, "fsck-objects": true,
		"verify-commit": true, "verify-tag": true, "verify-pack": true,
		"cherry": true, "patch-id": true,
		"show-ref": true, "show-branch": true, "diff-tree": true, "diff-index": true,
		"range-diff": true, "fast-export": true, "whatchanged": true, "diff-files": true, "show-index": true,
	}

	// gitReadOnlyFlags are global git options with no effect on the tree.
	gitReadOnlyFlags = map[string]bool{
		"--version": true, "--help": true, "-h": true, "-p": true, "-P": true,
		"--no-pager": true, "--paginate": true, "--bare": true,
		"--no-optional-locks": true, "--literal-pathspecs": true, "--no-literal-pathspecs": true,
		"--html-path": true, "--man-path": true, "--info-path": true,
		"--no-replace-objects": true,
	}

	// gitFlagsWithValue — global git flags whose argument must be skipped
	// before the subcommand: git -C /repo commit … (--exec-path doubles as
	// a bare read: it prints the path.)
	gitFlagsWithValue = map[string]bool{
		"-C": true, "-c": true, "--git-dir": true, "--work-tree": true,
		"--namespace": true, "--exec-path": true,
	}

	// goWriteSubcommands / goModWriteSubcommands: `go test` and `go vet`
	// are diagnosis work; everything that builds, installs, fetches,
	// formats or generates writes.
	goWriteSubcommands    = map[string]bool{"build": true, "install": true, "get": true, "generate": true, "fmt": true, "clean": true, "run": true, "work": true, "fix": true, "tool": true}
	goModWriteSubcommands = map[string]bool{"tidy": true, "download": true, "vendor": true, "edit": true, "init": true}

	// JS package managers: read-only subcommands; everything else runs,
	// installs or publishes.
	pkgReadSubcommands = map[string]bool{
		"ls": true, "list": true, "ll": true, "la": true, "v": true, "view": true, "info": true, "show": true,
		"outdated": true, "help": true, "version": true, "--version": true, "-v": true, "-V": true,
		"ping": true, "root": true, "prefix": true, "bin": true, "doctor": true, "search": true, "s": true,
		"explain": true, "why": true, "licenses": true,
	}

	// shellNames are the shells that run their argument as code.
	shellNames = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "mksh": true,
		"fish": true, "csh": true, "tcsh": true, "ash": true, "busybox": true}

	// goToolReadSubcommands are `go tool` subcommands that only read.
	goToolReadSubcommands = map[string]bool{"nm": true, "cover": true, "objdump": true, "addr2line": true, "buildid": true,
		"vet": true, "pprof": true}

	// pythonReadModules are the -m modules that only read and print.
	pythonReadModules = map[string]bool{
		"json.tool": true, "json": true, "pprint": true, "this": true, "platform": true, "sys": true, "base64": true,
	}

	// awkNames are the awk family; the program rules apply to each.
	awkNames = map[string]bool{"awk": true, "gawk": true, "mawk": true, "nawk": true, "original-awk": true}

	// curlShortWithValue are curl's short options that take a value.
	curlShortWithValue = map[byte]bool{
		'A': true, 'b': true, 'c': true, 'C': true, 'd': true, 'D': true, 'e': true, 'E': true,
		'F': true, 'H': true, 'K': true, 'm': true, 'o': true, 'P': true, 'Q': true, 'T': true,
		'u': true, 'U': true, 'w': true, 'x': true, 'X': true, 'y': true, 'Y': true,
	}

	// curlLongWrites are curl's long options that always save or send a file.
	curlLongWrites = map[string]bool{
		"remote-name": true, "remote-name-all": true, "remote-header-name": true, "cookie-jar": true,
		"trace": true, "trace-ascii": true, "etag-save": true, "config": true, "upload-file": true, "output-dir": true,
	}

	// tarLongWrites are tar's long forms that unpack or repack.
	tarLongWrites = map[string]bool{
		"--extract": true, "--get": true, "--create": true, "--append": true, "--update": true,
		"--catenate": true, "--concatenate": true, "--delete": true,
	}

	envAssign    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	pythonNameRe = regexp.MustCompile(`^python[0-9.]*$`)
	pipNameRe    = regexp.MustCompile(`^pip[0-9.]*$`)
	awkSystemRe  = regexp.MustCompile(`system\s*\(`)
	awkPipeCmdRe = regexp.MustCompile(`\|\s*["']`)
	sedWriteRe   = regexp.MustCompile(`(?:^|[;{}]|\n)\s*(?:[0-9]+|\$|/(?:\\.|[^/\\\n;])*/[IgMm]*|\\[0-9]+)?(?:,(?:[0-9]+|\$|/(?:\\.|[^/\\\n;])*/[IgMm]*))?\s*(?:!\s*)?w\s+[^\s;{}]`)

	// shellReservedWords may precede the verb of a command: `then rm x`,
	// `time make`, `! rm`, `{ rm; }`, `coproc rm`, `function f {…}`. They
	// are skipped when they come first.
	shellReservedWords = map[string]bool{
		"then": true, "do": true, "else": true, "elif": true, "if": true, "while": true,
		"until": true, "for": true, "case": true, "select": true, "function": true,
		"time": true, "!": true, "{": true, "}": true, "coproc": true,
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
	return ruleFor(lexShell(command))
}

// ruleFor judges an already lexed command. Split from writeClassRule so the
// lexer's own stop marker can be pinned by a test.
func ruleFor(toks []shTok) string {
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
	var suspended []int
	for i, t := range toks {
		if t.kind != shOp {
			continue
		}
		switch t.text {
		case opArithOpen:
			arith++
		case opArithClose:
			arith--
		case opSubstOpen:
			// A substitution inside arithmetic is a COMMAND again: its
			// redirects write (`$(( $(cat f > out) ))`, ship review r2).
			suspended = append(suspended, arith)
			arith = 0
		case opSubstClose:
			if n := len(suspended); n > 0 {
				arith = suspended[n-1]
				suspended = suspended[:n-1]
			}
		}
		if arith > 0 || !fileTargetOps[t.text] {
			continue // inside arithmetic a '>' is a comparison
		}
		target := ""
		if i+1 < len(toks) && toks[i+1].kind == shWord {
			target = toks[i+1].text
		}
		if isDevTarget(target) {
			continue // a character device: nothing durable is written
		}
		if t.text == ">&" && (isDigits(target) || target == "-") {
			continue // a file-descriptor copy or close — not a file target
		}
		return fmt.Sprintf("redirect %s to %q", t.text, target)
	}
	return ""
}

// isDevTarget: redirects to these write nothing durable — /dev/null and the
// fd devices (`echo hi > /dev/stderr`, a common logging idiom).
func isDevTarget(t string) bool {
	switch t {
	case "/dev/null", "/dev/stdout", "/dev/stderr", "/dev/tty":
		return true
	}
	return devFDRe.MatchString(t)
}

// devFDRe: `/dev/fd/N` only — a prefix match let `/dev/fd/../../tmp/x`
// through (on Linux /dev/fd resolves under /proc, where `..` escapes).
var devFDRe = regexp.MustCompile(`^/dev/fd/[0-9]+$`)

// verbRule judges every command of the token stream and returns the first
// rule that matches. Commands are separated by the break operators, and a
// substitution's nested text is a command of its own; the words that follow
// a substitution's closing marker belong to the OUTER command again — they
// continue its word (the argument `"...$(x)/tail"`) or follow it
// (`X=$(cmd) rm -rf x` runs rm). Gathering words per nesting level keeps
// both true: a continuation word is never mistaken for a command word, and
// a real command after an assignment is never lost.
func verbRule(toks []shTok) string {
	if rule := substitutedCommandRule(toks); rule != "" {
		return rule
	}
	outer := []*[]shTok{} // the commands suspended by an open substitution / arithmetic
	outerIn := []bool{}   // their stdin-live state
	var parens []parenFrame
	words := []shTok{}
	stdinLive, procArg := false, false
	skipTarget := false  // the next word is an input redirect's target
	targetSubst := false // this substitution is an input redirect's target
	var outerSkip []bool // per suspended command: skip the word after its substitution closes
	var outerProc []bool // per suspended command: its computed-program flag
	for idx := 0; idx < len(toks); idx++ {
		t := toks[idx]
		if n := len(parens); n > 0 && t.text != ")" {
			parens[n-1].saw = true
		}
		if t.kind == shWord {
			if skipTarget {
				skipTarget = false
				continue
			}
			words = append(words, t)
			continue
		}
		switch t.text {
		case opSubstOpen, opArithOpen:
			suspended := words // by value: the variable is reused for the nested command
			outer = append(outer, &suspended)
			outerIn = append(outerIn, stdinLive)
			outerProc = append(outerProc, procArg)
			outerSkip = append(outerSkip, targetSubst)
			words = []shTok{}
			stdinLive, procArg, targetSubst = false, false, false
			continue
		case opSubstClose, opArithClose:
			if n := len(outer); n > 0 {
				words = *outer[n-1]
				outer = outer[:n-1]
				stdinLive = outerIn[n-1]
				outerIn = outerIn[:n-1]
				procArg = outerProc[len(outerProc)-1]
				outerProc = outerProc[:len(outerProc)-1]
				skipTarget = outerSkip[len(outerSkip)-1] // `<<<"$(…)"`: its word is the target
				outerSkip = outerSkip[:len(outerSkip)-1]
			}
			continue
		}
		if t.text == "(" {
			// Every "(" opens a frame; when words precede it, the frame is an
			// ARGUMENT-position process substitution (`python3 <(echo code)`
			// runs the descriptor as its program) — the outer command then
			// takes a computed program, judged when its frame closes.
			zshProc := len(words) > 0 && words[len(words)-1].text == "=" && !words[len(words)-1].expanded
			if zshProc {
				// zsh's `=(cmd)`: a process substitution too (`python3
				// =(echo code)` runs the temp file). The "=" is its opener,
				// not an operand (ship review round 4, C11).
				words = append([]shTok(nil), words[:len(words)-1]...)
			}
			suspended := words
			f := parenFrame{words: &suspended, stdin: stdinLive, proc: procArg}
			if len(words) > 0 || zshProc {
				f.proc = true
			}
			parens = append(parens, f)
			words = []shTok{}
			stdinLive, procArg = false, false
			continue
		}
		if t.text == ")" && len(parens) > 0 {
			if rule := commandRule(words, stdinLive || procArg); rule != "" {
				return rule
			}
			f := parens[len(parens)-1]
			if f.proc && !f.saw {
				// `f() { …; }` — a function definition, not a process
				// substitution: the parens are plain command breaks.
				parens = parens[:len(parens)-1]
				words, stdinLive, procArg = []shTok{}, false, false
				continue
			}
			parens = parens[:len(parens)-1]
			words, stdinLive = *f.words, f.stdin
			procArg = f.proc
			continue
		}
		if shBreaks[t.text] {
			if rule := commandRule(words, stdinLive || procArg); rule != "" {
				return rule
			}
			words = []shTok{}
			// A pipe hands this command's stdout to the NEXT command's
			// stdin — `echo '…' | python3` runs the interpreter on it.
			stdinLive = t.text == "|" || t.text == "|&"
			procArg = false
			continue
		}
		if inputRedirectOps[t.text] && idx+1 < len(toks) {
			if n := len(words); n > 0 && !words[n-1].expanded && isDigits(words[n-1].text) {
				// `python3 0<<EOF`: the digit word is the redirect's fd, not
				// an operand of the command.
				words = words[:n-1]
			}
			switch next := toks[idx+1]; {
			case next.kind == shWord:
				// `python3 <<'EOF'`, `python3 < script.py`: the word after the
				// operator is the delimiter / file / herestring — data, not
				// an argument. `< /dev/null` delivers nothing.
				if next.text != "/dev/null" {
					stdinLive = true
				}
				skipTarget = true
			case next.text == opSubstOpen:
				// `<<<"$(…)"`, `< "$(cat f)"`: a computed value supplies stdin.
				stdinLive, targetSubst = true, true
			case next.text == "(":
				stdinLive = true // `< <(…)`: a process substitution feeds stdin
			}
		}
	}
	return commandRule(words, stdinLive || procArg)
}

// parenFrame is one "(" frame: the words and stdin state suspended behind
// it, and whether it is an argument-position process substitution (proc).
type parenFrame struct {
	words *[]shTok
	stdin bool
	proc  bool
	saw   bool // a token was seen inside: a process substitution's body
}

// inputRedirectOps feed a command's stdin from the command text.
var inputRedirectOps = map[string]bool{"<": true, "<<": true, "<<-": true, "<<<": true, "<>": true}

// hasPositional reports whether any argument is a bare word (a script file
// for an interpreter). Option VALUES read as positionals here: callers use
// it only to stay quiet when a program is named.
// noValueLong are the long options every interpreter spells the same way:
// they take no separate value.
var noValueLong = map[string]bool{"--version": true, "--help": true, "-V": true, "-h": true}

// operandNamesProgram walks an interpreter's options and reports whether an
// operand — a script file — is named. An option that takes a separate VALUE
// consumes the next word (valueShort: the short letters that do; every long
// option without "=" does unless it is in noValueLong): with stdin live,
// `python3 -W ignore` still runs the program on stdin, so "ignore" is not a
// script (ship review round 3).
// isStdinName reports a path that IS the command's stdin: a script operand
// spelled that way runs what stdin delivers (`python3 /dev/stdin < s.py`).
func isStdinName(a string) bool {
	return a == "/dev/stdin" || a == "/dev/fd/0" || a == "/proc/self/fd/0"
}

// interpreterValueLong are, per interpreter, the long options that take a
// SEPARATE value. Every other long option takes none: an unknown long flag
// must not swallow the script (`node --no-warnings tools/x.js`).
var interpreterValueLong = map[string]map[string]bool{
	"python": {"--check-hash-based-pycs": true, "--warn": true},
	"node":   {"--require": true, "--import": true, "--preload": true, "--inspect-port": true, "--experimental-loader": true},
	"perl":   {"--module": true, "--include": true},
	"ruby":   {"--require": true},
	"php": {"--define": true, "--ini": true, "--bind": true, "--run": true, "--process-begin": true,
		"--process-code": true, "--process-file": true, "--process-end": true, "--process-header": true},
	"lua": {},
}

// operandNamesProgram walks an interpreter's options and reports whether an
// operand — a script file — is named. An option that takes a separate VALUE
// consumes the next word (valueShort: the short letters that do; the long
// ones are in interpreterValueLong): with stdin live, `python3 -W ignore`
// still runs the program on stdin, so "ignore" is not a script. --version /
// --help run nothing.
func operandNamesProgram(kind, valueShort string, args []string) bool {
	for k := 0; k < len(args); k++ {
		a := args[k]
		if a == "--" {
			return k+1 < len(args)
		}
		if noValueLong[a] {
			return true // --version / --help: no program runs
		}
		if !strings.HasPrefix(a, "-") {
			if isStdinName(a) {
				continue // the "script" is stdin itself
			}
			return true
		}
		if strings.HasPrefix(a, "--") {
			if !strings.Contains(a, "=") && interpreterValueLong[kind][a] {
				k++
			}
			continue
		}
		if len(a) > 1 && strings.ContainsRune(valueShort, rune(a[len(a)-1])) {
			k++
		}
	}
	return false
}

// substitutedCommandRule catches a command substitution that IS the first
// word of a command — `"$(echo rm)" -rf x`: nothing literal stands before
// it in command position, so its OUTPUT is the command name (and words
// after it are its arguments). That command is unknown until it runs, and
// the nested text says nothing about it (there it is `echo rm`). The rule
// needs a word after the substitution; a bare `$(ls)` is its nested command
// alone. Inside arithmetic the spliced words are numbers, not a command.
func substitutedCommandRule(toks []shTok) string {
	arith := 0
	for i, t := range toks {
		if t.kind != shOp {
			continue
		}
		switch t.text {
		case opArithOpen:
			arith++
			continue
		case opArithClose:
			arith--
			continue
		case opSubstOpen:
		default:
			continue
		}
		if arith > 0 || t.attachedBefore || t.attachedAfter {
			continue // inside arithmetic, or part of a larger word
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
			if u.kind != shOp {
				if depth == 0 {
					return "command substitution in command position: $(…) supplies the command"
				}
				continue
			}
			switch {
			case u.text == opSubstOpen:
				depth++
			case u.text == opSubstClose:
				depth--
				if depth == 0 && u.attachedAfter {
					// `"$(git rev-parse …)"/bin/tool`: the substitution is
					// the DIRECTORY of a program path, not the command.
					return ""
				}
			case depth == 0 && shBreaks[u.text] && !(j == i+1 && u.text == ";"):
				return "" // the command ended with no word after the substitution
			}
		}
	}
	return ""
}

// commandRule judges one command — its words only (operators excluded) —
// by its command word and its verb.
func commandRule(words []shTok, stdinLive bool) string {
	if len(words) == 0 {
		return ""
	}
	if words[0].text == "case" {
		return "" // the words up to `)` are the case subject, not a command
	}
	// `env -S '<words>'`: the string holds options, assignments and the
	// command — judged as a command of its own.
	for k, w := range words {
		if (w.text == "-S" || w.text == "--split-string") && k+1 < len(words) && strings.ContainsAny(words[k+1].text, " \t") {
			if rule := ruleFor(lexShell(words[k+1].text)); rule != "" {
				return rule
			}
		}
	}
	// An empty word names no program (the lexer keeps a word that holds a
	// substitution together, so an assignment's value never poses as the
	// command).
	if n := len(words); n > 1 && words[0].text == "" && words[0].expanded && strings.HasPrefix(words[1].text, "/") {
		// `$(git rev-parse --show-toplevel)/bin/tool status`: the
		// substitution is the DIRECTORY, the next word the program path.
		words = append([]shTok{words[1]}, words[2:]...)
	} else if n > 1 && words[0].text == "" && !words[0].expanded {
		words = words[1:]
	}
	for len(words) > 0 && (shellReservedWords[words[0].text] || envAssign.MatchString(words[0].text)) {
		if words[0].text == "function" && len(words) > 1 {
			words = words[1:] // the function's name precedes its body
		}
		if words[0].text == "time" {
			// `time [-p] [-l] [-o FILE] command` — the keyword's own options.
			words = words[1:]
			for len(words) > 0 && (words[0].text == "-p" || words[0].text == "--portability" || words[0].text == "-l" || words[0].text == "--list" ||
				strings.HasPrefix(words[0].text, "--format=") || strings.HasPrefix(words[0].text, "-f")) {
				words = words[1:]
			}
			if len(words) > 1 && (words[0].text == "-o" || words[0].text == "--output") {
				words = words[2:] // … and its value
			}
			if len(words) > 1 && (words[0].text == "-f" || words[0].text == "--format") {
				words = words[2:] // GNU time's format and its value
			}
			if len(words) > 0 && strings.HasPrefix(words[0].text, "-") {
				// An option this rule does not model: `\time -a rm -rf x`
				// (the EXTERNAL time) and `time -x rm …` run the words that
				// follow. Fail closed (ship review round 4, C10).
				return "time … (an option the classifier does not model — the words that follow may be the command)"
			}
			continue
		}
		words = words[1:]
	}
	if len(words) == 0 {
		return ""
	}
	texts := make([]string, len(words))
	for i, w := range words {
		texts[i] = w.text
	}
	verbIdx, verb := resolveVerbTok(words)
	if verbIdx < 0 {
		return ""
	}
	if words[verbIdx].expanded {
		// The command word is computed when it runs: a substitution, an
		// expansion or $'…' supplies it, and its text here is only a
		// spelling of that value. One spelling is still a known PROGRAM
		// NAME: a path whose last component is literal
		// ("${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status") — the
		// expansion only chooses the directory, so the basename is judged
		// like any verb ("$BIN/rm" is rm). When the expansion supplies the
		// last component, nothing is known about the program.
		if base, literal := literalBasename(words[verbIdx].text); literal {
			verb = base
		} else {
			return "command word is computed at run time (a substitution, an expansion or $'…' supplies it) — unknown until it runs"
		}
	}
	args := texts[verbIdx+1:]
	expanded := make([]bool, len(args))
	for i := range args {
		expanded[i] = words[verbIdx+1+i].expanded
	}
	if lower := strings.ToLower(verb); scanningWrapper(lower) && wrapperRunsComputedCommand(lower, words[verbIdx+1:]) {
		return verb + " … (the wrapped command is computed at run time — unknown until it runs)"
	}
	if rule := verbTableRule(verb, args, stdinLive, expanded); rule != "" {
		return rule
	}
	// macOS's default filesystem is case-insensitive: `RM -rf x` runs rm.
	// An exact-case verb the tables do not know is judged by its lower form.
	if lower := strings.ToLower(verb); lower != verb {
		if rule := verbTableRule(lower, args, stdinLive, expanded); rule != "" {
			return rule
		}
		return scanningWrapperRule(lower, args, stdinLive)
	}
	return scanningWrapperRule(verb, args, stdinLive)
}

// wrapperRunsComputedCommand reports whether a scanning wrapper's arguments
// hold a command word computed at run time: `timeout 5 $(echo rm) -rf x`
// runs whatever the substitution prints, and the wrapper scan — which
// matches table verbs by spelling — saw nothing (ship review round 3).
// wrapperValueOptions: per scanning wrapper, the options that take a
// SEPARATE value (their value is not the wrapped command).
var wrapperValueOptions = map[string]map[string]bool{
	"timeout":    {"-s": true, "--signal": true, "-k": true, "--kill-after": true},
	"nice":       {"-n": true, "--adjustment": true},
	"flock":      {"-w": true, "--wait": true, "-E": true, "--conflict-exit-code": true, "-c": true},
	"doas":       {"-u": true, "-C": true, "-L": true},
	"strace":     {"-o": true, "-e": true, "-p": true, "-u": true, "-b": true, "-D": true, "-I": true, "-k": true},
	"ltrace":     {"-o": true, "-e": true, "-p": true, "-u": true},
	"xcrun":      {"--sdk": true, "-sdk": true, "--target": true, "--arch": true, "-arch": true, "-f": true, "-F": true},
	"arch":       {"-arch": true},
	"caffeinate": {"-t": true, "-w": true, "-u": true},
	"stdbuf":     {"-i": true, "-o": true, "-e": true},
	"parallel":   {"-j": true, "--jobs": true, "-a": true, "--arg-file": true},
	"taskset":    {"-c": true, "--cpu-list": true, "-p": true},
	"ionice":     {"-c": true, "-n": true, "-p": true, "-u": true},
	"chrt":       {"-p": true, "-a": true},
	"prlimit":    {"-p": true, "--pid": true, "-c": true, "--cmd": true},
	"script":     {"-c": true, "--command": true, "-t": true, "-f": true, "-a": true, "-F": true, "-T": true, "-e": true, "-O": true},
	"setsid":     {},
}

// infoOnly: the first argument asks for the program's version or help.
func infoOnly(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "--version", "-v", "-V", "--help", "-h", "version":
		return true
	}
	return false
}

// rtkWriters: rtk's subcommands that change configuration or project files.
var rtkWriters = map[string]bool{"init": true, "config": true, "trust": true, "lint": true, "pint": true, "ecs": true,
	"install": true, "uninstall": true, "setup": true, "telemetry": true}

// wrapperTakesValue: the option is in the wrapper's arity table, also in its
// ATTACHED spelling (`xcrun -sdkmacosx`, `flock -w5`).
// wrapperTakesValue: the option is in the wrapper's arity table — exactly
// (`flock -w 5`) or with its value ATTACHED to a short option (`xcrun
// -sdkmacosx`, `ionice -c2`).
func wrapperTakesValue(table map[string]bool, a string) bool {
	if table[a] {
		return true
	}
	return len(a) > 2 && a[0] == '-' && a[1] != '-' && table[a[:2]]
}

// wrapperValueIsNext: the option's value is the NEXT word (skip it).
func wrapperValueIsNext(table map[string]bool, a string) bool {
	return table[a]
}

// wrapperOperandsBeforeCommand: wrappers whose wrapped command is NOT the
// first operand (flock: the lock file comes first).
var wrapperOperandsBeforeCommand = map[string]int{"flock": 1, "script": 1}

var wrapperDurationRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?[smhd]?$`)

// wrapperRunsComputedCommand reports whether a scanning wrapper's wrapped
// COMMAND is computed at run time — an expanded word in the command
// position: `timeout 5 $(echo rm) -rf x`, `nice "$RM" -rf x`. Option
// values (arity table) and durations are not operands; an expanded word
// elsewhere (`timeout 5 echo $(date)`) is an ordinary argument.
func wrapperRunsComputedCommand(verb string, args []shTok) bool {
	valueOpts, before := wrapperValueOptions[verb], wrapperOperandsBeforeCommand[verb]
	for k := 0; k < len(args); k++ {
		w := args[k]
		a := w.text
		if a == "--" || (strings.HasPrefix(a, "--") && strings.Contains(a, "=")) {
			continue
		}
		if wrapperTakesValue(valueOpts, a) {
			if wrapperValueIsNext(valueOpts, a) {
				k++ // the option's value
			}
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		if envAssign.MatchString(a) || wrapperDurationRe.MatchString(a) {
			continue
		}
		if before > 0 {
			before--
			continue
		}
		return w.expanded
	}
	return false
}

// literalBasename splits an expanded command word into its final path
// component and reports whether that component is literal (no expansion
// inside it).
func literalBasename(text string) (string, bool) {
	if !strings.ContainsRune(text, '/') {
		return "", false
	}
	if strings.HasSuffix(text, "/") {
		// `"/bin/$(echo rm)"`: the last component is supplied by what
		// follows — computed (ship review round 4, C8).
		return "", false
	}
	base := path.Base(text)
	if strings.ContainsAny(base, "$`\\") {
		return "", false
	}
	return base, true
}

// verbTableRule is the per-verb table: "" when the verb, with these
// arguments, is read-class.
func verbTableRule(verb string, args []string, stdinLive bool, expanded []bool) string {
	switch {
	case verb == "git":
		return gitRule(args, expanded)
	case awkNames[verb]:
		return awkRule(verb, args)
	case verb == "find":
		return findRule(args)
	case verb == "tar":
		return tarRule(args)
	case verb == "curl":
		return curlRule(args)
	case verb == "wget":
		return wgetRule(args)
	case verb == "go":
		return goRule(args)
	case verb == "gofmt" || verb == "goimports" || verb == "gofumpt":
		return writeFlagRule(verb, args)
	case verb == "npm" || verb == "pnpm" || verb == "yarn" || verb == "bun" || verb == "deno":
		return packageManagerRule(verb, args)
	case pipNameRe.MatchString(verb):
		return anyArgRule(verb, args, "install", "uninstall", "download", "wheel", "build")
	case verb == "pipx":
		return anyArgRule(verb, args, "install", "uninstall", "run", "runpip", "inject", "upgrade", "reinstall", "ensurepath")
	case verb == "poetry":
		return anyArgRule(verb, args, "install", "add", "remove", "update", "lock", "new", "init", "build", "publish", "self")
	case verb == "uv":
		return uvRule(args)
	case pythonNameRe.MatchString(verb):
		return pythonRule(verb, args, stdinLive)
	case verb == "node":
		return nodeRule(args, stdinLive)
	case verb == "perl":
		return perlRule(args, stdinLive)
	case verb == "sed":
		return sedRule(args)
	case formatterWriters[verb]:
		return formatterRule(verb, args)
	case verb == "ruby" || verb == "php" || verb == "lua":
		return inlineCodeRule(verb, args, stdinLive)
	case verb == "unzip":
		return unzipRule(args)
	case shellNames[verb]:
		if syntaxCheckOnly(args) || shellInfoOnly(args) {
			return "" // -n parses without running anything; --version / --help print
		}
	}
	if !shellNames[verb] && infoOnly(args) {
		return "" // --version / --help: the program is asked about itself
	}
	if writeClassVerbs[verb] {
		if verb == "source" || verb == "." {
			return "source (runs a script file)"
		}
		return "write-class verb: " + verb
	}
	return ""
}

// formatterWriters are the code formatters and fixers a model runs by
// reflex: with their write flag they rewrite the tree, and before the
// posture panel every one of them read as an unknown verb (an UNMOTIVATED
// edit — "just format it" — not a bypass).
var formatterWriters = map[string]bool{
	"prettier": true, "eslint": true, "ruff": true, "black": true, "golangci-lint": true, "isort": true, "tsc": true,
}

func formatterRule(verb string, args []string) string {
	has := func(flags ...string) bool {
		for _, a := range args {
			for _, f := range flags {
				if a == f || strings.HasPrefix(a, f+"=") {
					return true
				}
			}
		}
		return false
	}
	switch verb {
	case "prettier":
		if has("-w", "--write") {
			return "prettier --write (rewrites files)"
		}
	case "eslint":
		if has("--fix") && !has("--fix-dry-run") {
			return "eslint --fix (rewrites files)"
		}
	case "ruff":
		if len(args) > 0 && args[0] == "format" && !has("--check", "--diff") {
			return "ruff format (rewrites files)"
		}
		if len(args) > 0 && args[0] == "check" && has("--fix") && !has("--diff") {
			return "ruff check --fix (rewrites files)"
		}
	case "black":
		if !has("--check", "--diff", "--version", "--help") {
			return "black (rewrites files unless --check / --diff)"
		}
	case "golangci-lint":
		if has("--fix") {
			return "golangci-lint --fix (rewrites files)"
		}
	case "isort":
		if !has("--check-only", "-c", "--diff", "-df", "--df", "--version", "-h", "--help", "--show-config", "--show-files", "--show-diff") {
			return "isort (rewrites imports unless --check-only / --diff)"
		}
	case "tsc":
		if !has("--noEmit", "--version", "--help", "--showConfig", "--listFilesOnly", "--clean") {
			return "tsc (emits files beside the sources unless --noEmit)"
		}
	}
	return ""
}

// scanningWrapperRule judges the wrappers whose own options hide the real
// command from a keyword-level reading — `timeout 5 make test`,
// `nice -n 10 go run x`, `caffeinate -i make`: the command starts after an
// unknown number of option/value words. Every later word is tried as a
// verb; the first the table names wins. The cost is one over-block when an
// argument happens to equal a table verb (`timeout 5 grep rm notes.txt`);
// the alternative is letting a wrapped writer through.
func scanningWrapper(verb string) bool {
	switch verb {
	case "timeout", "nice", "watch", "time", "doas", "strace", "ltrace", "setsid", "arch", "caffeinate",
		"parallel", "stdbuf", "script", "xcrun", "flock", "taskset", "ionice", "chrt", "prlimit", "trap", "rtk":
		return true
	}
	return false
}

// commandStringWrappers run an argument as a COMMAND STRING: `watch -n 2
// 'rm -rf x'`, `script -c 'cmd'`, `flock lock -c 'cmd'`. The string is one
// opaque word to a spelling match, so it is lexed and judged as a command.
var commandStringWrappers = map[string]bool{"watch": true, "script": true, "flock": true, "trap": true, "parallel": true}

func scanningWrapperRule(verb string, args []string, stdinLive bool) string {
	if !scanningWrapper(verb) {
		return ""
	}
	if verb == "rtk" {
		// rtk is a transparent output-condensing wrapper: `rtk <command…>`
		// runs the command unchanged (on this user's machine every command
		// may arrive here already rewritten to `rtk …`). Its own front words:
		// global options, `bypass` (run without condensing), and
		// `hook check '<command>'` (a command STRING). Unmodelled, the whole
		// freeze was void behind it: `rtk rm -rf x` read as an unknown verb
		// (posture panel, diagnosis-flow lens).
		for len(args) > 0 && (strings.HasPrefix(args[0], "-") || args[0] == "bypass" || args[0] == "proxy" ||
			args[0] == "err" || args[0] == "summary" || args[0] == "test" || args[0] == "run") {
			// proxy = bypass; err / summary / test / run wrap the command that
			// follows (round 5).
			args = args[1:]
		}
		if len(args) >= 2 && args[0] == "hook" && args[1] == "check" {
			args = args[2:]
		}
		if len(args) > 0 && rtkWriters[args[0]] {
			return "rtk " + args[0] + " (changes rtk's configuration or project files)"
		}
		if len(args) == 0 {
			return ""
		}
		if len(args) == 1 && strings.ContainsAny(args[0], " \t") {
			// What follows is ONE word: a command string.
			if rule := ruleFor(lexShell(args[0])); rule != "" {
				return "rtk … " + rule
			}
		}
		name := strings.ToLower(args[0])
		if scanningWrapper(name) {
			return scanningWrapperRule(name, args[1:], stdinLive)
		}
		if rule := verbTableRule(name, args[1:], stdinLive, nil); rule != "" {
			return "rtk … " + rule
		}
		return ""
	}
	valueOpts, before := wrapperValueOptions[verb], wrapperOperandsBeforeCommand[verb]
	for k := 0; k < len(args); k++ {
		w := args[k]
		if wrapperTakesValue(valueOpts, w) {
			// The option and its value: neither is the command — except for
			// a command-string wrapper, whose -c VALUE is the command.
			if wrapperValueIsNext(valueOpts, w) {
				if commandStringWrappers[verb] && k+1 < len(args) {
					if rule := ruleFor(lexShell(args[k+1])); rule != "" {
						return verb + " … " + rule
					}
				}
				k++
			}
			continue
		}
		if w == "--" || (strings.HasPrefix(w, "--") && strings.Contains(w, "=")) {
			continue
		}
		if commandStringWrappers[verb] {
			// Judged BEFORE the assignment skip: `watch -n 2 'FOO=1 rm -rf
			// x'` opens with an assignment, which must not hide its command.
			if rule := ruleFor(lexShell(w)); rule != "" {
				return verb + " … " + rule
			}
		}
		if strings.HasPrefix(w, "-") || envAssign.MatchString(w) || wrapperDurationRe.MatchString(w) {
			continue
		}
		if before > 0 {
			before--
			continue
		}
		// THE wrapped command: this operand and nothing after it. Trying
		// every later word as a verb denied `timeout 60 go test .` (the
		// "." of go test read as `source`) — an ordinary spelling.
		if commandStringWrappers[verb] {
			if rule := ruleFor(lexShell(w)); rule != "" {
				return verb + " … " + rule
			}
		}
		name := strings.ToLower(w)
		if !strings.ContainsAny(name, "$`\\") && strings.ContainsRune(name, '/') {
			name = path.Base(name) // `timeout 5 /bin/rm -rf x`: a path names its program
		}
		if scanningWrapper(name) {
			return scanningWrapperRule(name, args[k+1:], stdinLive) // nice -n 5 ionice -c2 rm x
		}
		// Words are tried without case (macOS's filesystem runs `RM`), and
		// stdinLive travels with the line: `nice python3 < s.py` runs the
		// interpreter on the file's contents.
		if rule := verbTableRule(name, args[k+1:], stdinLive, nil); rule != "" {
			return verb + " … " + rule
		}
		return ""
	}
	return ""
}

// --- git ------------------------------------------------------------------------

// gitRule judges a git invocation. Read-only subcommands pass; the
// argument-dependent ones are judged by their own forms; everything else —
// the write table and any subcommand outside both tables — writes.
func gitRule(args []string, expanded []bool) string {
	isExpanded := func(j int) bool { return j < len(expanded) && expanded[j] }
	for j := 0; j < len(args); j++ {
		a := args[j]
		if gitFlagsWithValue[a] {
			if isExpanded(j) {
				continue // `-C$(pwd)`: the value rides inside this word
			}
			if j+1 >= len(args) {
				if a == "--exec-path" {
					return "" // bare: prints the path
				}
				return "git (unfinished option " + a + ")"
			}
			// `git -c alias.x='!cmd' x` runs cmd through an alias.
			if a == "-c" && strings.HasPrefix(args[j+1], "alias.") {
				return "git -c alias.* (an alias runs a command)"
			}
			j++
			continue
		}
		if strings.HasPrefix(a, "-") {
			if (strings.HasPrefix(a, "--git-dir=") || strings.HasPrefix(a, "--work-tree=") ||
				strings.HasPrefix(a, "--namespace=") || strings.HasPrefix(a, "--exec-path=")) && strings.HasSuffix(a, "=") &&
				j+1 < len(args) && isExpanded(j+1) {
				// `--git-dir=$(pwd)/.git`: the substitution split the value
				// into its own EXPANDED word — consume it as the value. A
				// literal next word is the subcommand after an empty value
				// (`git --namespace= push`), never a value.
				j++
				continue
			}
			if gitReadOnlyFlags[a] || strings.HasPrefix(a, "--git-dir=") || strings.HasPrefix(a, "--work-tree=") ||
				strings.HasPrefix(a, "--namespace=") || strings.HasPrefix(a, "--exec-path=") ||
				strings.HasPrefix(a, "-c") || strings.HasPrefix(a, "-C") {
				if strings.HasPrefix(a, "-c") && strings.HasPrefix(strings.TrimPrefix(a, "-c"), "alias.") {
					return "git -c alias.* (an alias runs a command)"
				}
				continue
			}
			return "git (unknown option " + a + ")"
		}
		rest := args[j+1:]
		for _, flag := range rest {
			if flag == "--help" || flag == "-h" {
				return "" // documentation: whatever the subcommand is
			}
		}
		if a == "difftool" {
			return "" // opens a viewer: a read
		}
		for _, flag := range rest {
			if strings.HasPrefix(flag, "--output=") || flag == "--output" {
				return "git " + a + " --output (writes the output to a file)"
			}
		}
		switch a {
		case "stash":
			if len(rest) > 0 && (rest[0] == "list" || rest[0] == "show") {
				return ""
			}
		case "worktree":
			if len(rest) > 0 && rest[0] == "list" {
				return ""
			}
		case "submodule":
			if len(rest) > 0 && rest[0] == "status" {
				return ""
			}
		case "notes":
			if len(rest) > 0 && (rest[0] == "show" || rest[0] == "list") {
				return ""
			}
		case "bundle":
			if len(rest) > 0 && (rest[0] == "verify" || rest[0] == "list-heads") {
				return ""
			}
		case "apply":
			for _, flag := range rest {
				if flag == "--apply" {
					return "git apply --apply (applies the patch)"
				}
			}
			for _, flag := range rest {
				if flag == "--check" || flag == "--stat" || flag == "--numstat" || flag == "--summary" {
					return ""
				}
			}
		case "format-patch":
			for _, flag := range rest {
				if flag == "--stdout" {
					return ""
				}
			}
		case "branch":
			if branchMutation(rest) {
				return "git branch (create, delete, force or unknown options)"
			}
			return ""
		case "tag":
			if tagMutation(rest) {
				return "git tag (create, delete, annotate or unknown options)"
			}
			return ""
		case "config":
			if configWrites(rest) {
				return "git config (write)"
			}
			return ""
		case "symbolic-ref":
			nonFlag := 0
			for _, flag := range rest {
				if !strings.HasPrefix(flag, "-") {
					nonFlag++
					continue
				}
				if flag != "--short" && flag != "-q" && flag != "--quiet" {
					return "git symbolic-ref (write)"
				}
			}
			if nonFlag > 1 {
				return "git symbolic-ref (write)"
			}
			return ""
		case "remote":
			if len(rest) == 0 || rest[0] == "-v" || rest[0] == "--verbose" || rest[0] == "show" || rest[0] == "get-url" {
				return ""
			}
			return "git remote " + rest[0]
		case "reflog":
			if len(rest) > 0 && rest[0] == "expire" {
				for _, flag := range rest[1:] {
					if flag == "--dry-run" || flag == "-n" {
						return "" // reports what would be pruned
					}
				}
				return "git reflog expire"
			}
			if len(rest) > 0 && rest[0] == "delete" {
				return "git reflog delete"
			}
			return "" // show, a count, a ref, --date=…: all reads
		case "archive":
			for _, flag := range rest {
				if flag == "-o" || flag == "--output" || strings.HasPrefix(flag, "--output=") {
					return "git archive -o (writes an archive)"
				}
			}
			return ""
		case "hash-object":
			for _, flag := range rest {
				if strings.HasPrefix(flag, "-w") {
					return "git hash-object -w (writes an object)"
				}
			}
			return ""
		case "bisect":
			if len(rest) > 0 && (rest[0] == "log" || rest[0] == "status" || rest[0] == "terms") {
				return ""
			}
			return "git bisect " + firstWord(rest)
		case "lfs":
			if len(rest) > 0 && (rest[0] == "ls-files" || rest[0] == "status" || rest[0] == "env") {
				return ""
			}
			return "git lfs " + firstWord(rest)
		}
		if gitWriteSubcommands[a] {
			return "git " + a
		}
		if gitReadOnlySubcommands[a] {
			return ""
		}
		return "git subcommand outside the read-only table: " + a
	}
	return ""
}

func firstWord(words []string) string {
	if len(words) == 0 {
		return "(no subcommand)"
	}
	return words[0]
}

// branchMutation distinguishes `git branch`'s listing forms from creation,
// deletion, force and unknown options. Short flags may be clustered
// (`-avv`, `-vv`): a cluster of listing letters is a listing.
func branchMutation(args []string) bool {
	listing := false
	pattern := false // only --list / -l take bare arguments as patterns
	value := false   // a listing option that takes a separate value was just seen
	for _, a := range args {
		switch {
		case a == "-t" || a == "--track" || a == "--no-track" || a == "--set-upstream-to" || a == "-u" ||
			a == "--unset-upstream" || strings.HasPrefix(a, "--set-upstream-to="):
			return true // tracking options name a NEW branch (ship review r2)
		case a == "--list" || a == "-l":
			listing, pattern = true, true
		case a == "--contains" || a == "--no-contains" || a == "--merged" || a == "--no-merged" ||
			a == "--points-at" || a == "--sort" || a == "--format" || a == "--efilter":
			listing, value = true, true // the NEXT word is this option's value
		case a == "--show-current" || a == "--column" || a == "--no-column" ||
			a == "--ignore-case" || a == "-i":
			listing = true
		case strings.HasPrefix(a, "--format=") || strings.HasPrefix(a, "--sort=") || strings.HasPrefix(a, "--points-at=") ||
			strings.HasPrefix(a, "--contains=") || strings.HasPrefix(a, "--merged=") || strings.HasPrefix(a, "--efilter="):
			listing = true
		case len(a) > 1 && a[0] == '-' && a[1] != '-' && onlyLetters(a[1:], "larvi"):
			listing = true
			if strings.ContainsRune(a[1:], 'l') {
				pattern = true
			}
		case strings.HasPrefix(a, "-"):
			return true
		case value:
			value = false // `git branch --contains HEAD`: HEAD is the value
			continue
		case pattern:
			continue // a pattern behind --list / -l
		case !listing:
			return true // a name without a listing flag: create / copy
		default:
			return true // a bare name beside listing flags still creates (`git branch -a newb`)
		}
	}
	return false
}

// tagMutation distinguishes `git tag`'s listing forms from creation,
// annotation, deletion and signing. A bare NAME means different things
// with and without a listing flag: `git tag -l "v1*"` lists, while
// `git tag v1` CREATES the tag when it does not exist. The short letters
// mean something else than for branch: `-a` ANNOTATES here, `-n` lists
// with lines.
func tagMutation(args []string) bool {
	listing := false
	for _, a := range args {
		switch {
		case a == "-l" || a == "--list" || a == "--contains" || a == "--no-contains" ||
			a == "--merged" || a == "--no-merged" || a == "--points-at" || a == "--column" || a == "--no-column" ||
			a == "--sort" || a == "--format" || a == "-i" || a == "--ignore-case":
			listing = true
		case strings.HasPrefix(a, "-n") || strings.HasPrefix(a, "--format=") || strings.HasPrefix(a, "--sort=") ||
			strings.HasPrefix(a, "--points-at=") || strings.HasPrefix(a, "--contains=") || strings.HasPrefix(a, "--merged="):
			listing = true
		case strings.HasPrefix(a, "-"):
			return true // -a -s -m -f -d -v -u -F -e …
		case !listing:
			return true // a name without a listing flag: create
		}
	}
	return false
}

// configWrites separates `git config`'s reads (a name, or a read verb) from
// writes (a name and a value, or a write verb).
func configWrites(args []string) bool {
	names := 0
	for k := 0; k < len(args); k++ {
		a := args[k]
		if !strings.HasPrefix(a, "-") {
			names++
			continue
		}
		switch {
		case a == "--type":
			k++ // its value follows as a separate word
		case a == "--bool" || a == "--int" || a == "--bool-or-int" || a == "--path" || a == "--fixed-value":
			// read-side type selectors
		case a == "--default":
			k++ // its value follows as a separate word
		case a == "--global" || a == "--local" || a == "--system" || a == "--worktree":
			// a scope selector: `git config --global --list` reads
		case a == "-f" || a == "--file":
			k++ // its value follows as a separate word
		case strings.HasPrefix(a, "--file="):
		case a == "--get" || a == "--get-all" || a == "--get-regexp" || a == "--get-urlmatch" ||
			a == "--list" || a == "-l" || a == "--name-only" || a == "--show-origin" ||
			a == "--show-scope" || a == "--show-names" || a == "-z" || a == "--null" ||
			strings.HasPrefix(a, "--type=") || strings.HasPrefix(a, "--get-"):
		default:
			return true // --add, --unset, --replace-all, --edit, … or an option the classifier does not know
		}
	}
	return names >= 2 // name + value
}

// --- awk / sed -------------------------------------------------------------------

// awkRule judges an awk program: it is code. system() and `print … | "cmd"`
// run commands, `print > "file"` writes, and -f / --exec / --source / -i /
// -l run or load a program the gate cannot see. Comparisons (`NR>=1213`,
// `a > b`) write nothing.
func awkRule(verb string, args []string) string {
	prog := ""
	for k := 0; k < len(args) && prog == ""; k++ {
		a := args[k]
		if !strings.HasPrefix(a, "-") || len(a) == 1 {
			prog = a
			continue
		}
		if strings.HasPrefix(a, "--") {
			name := strings.TrimSuffix(a[2:], "")
			if eq := strings.Index(name, "="); eq >= 0 {
				name = name[:eq]
			}
			switch name {
			case "file", "exec", "source", "include", "load":
				return verb + " --" + name + " (program from a file or the command line)"
			case "assign", "field-separator":
				if !strings.Contains(a, "=") {
					k++ // the value follows
				}
			}
			continue
		}
		// A short cluster: its first letter decides (-f FILE, -E FILE,
		// -i FILE, -l LIB run or load a program; -v and -F take a value).
		switch a[1] {
		case 'f', 'E', 'i', 'l':
			return verb + " -" + string(a[1]) + " (program from a file or the command line)"
		case 'v', 'F':
			if len(a) == 2 {
				k++ // the value follows
			}
		}
	}
	if prog == "" {
		return ""
	}
	for _, stmt := range strings.FieldsFunc(prog, func(r rune) bool { return r == ';' || r == '{' || r == '}' || r == '\n' }) {
		s := strings.TrimSpace(stmt)
		switch {
		case awkSystemRe.MatchString(s):
			return verb + " system() (runs a command)"
		case strings.HasPrefix(s, "print") && awkOutput(s):
			return verb + " print > (writes a file)"
		case strings.HasPrefix(s, "print") && awkPipeCmdRe.MatchString(s):
			return verb + ` print | "cmd" (pipes into a command)`
		case strings.Contains(s, "getline") && strings.Contains(s, "|"):
			return verb + " … | getline (runs a command)"
		}
	}
	return ""
}

// awkOutput reports a '>' in an awk print statement that is not the '>='
// comparison.
func awkOutput(s string) bool {
	depth := 0
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			if depth > 0 {
				depth--
			}
		case c == '>' && depth == 0 && (i+1 >= len(s) || s[i+1] != '='):
			return true // a redirect at the statement's own level
		}
	}
	return false
}

// sedRule: -i edits in place; a `w file` command writes a file.
func sedRule(args []string) string {
	// Which word is the script: the word after a bare -e / --expression, an
	// attached -e… / --expression=…; otherwise the FIRST operand — every
	// later operand is an input file (`sed -n 1p e2e.log`). -f anywhere is a
	// program from a file.
	viaOption, scriptSeen, expectScript := false, false, false
	for _, a := range args {
		if a == "-e" || a == "--expression" {
			viaOption = true
		}
	}
	for _, a := range args {
		switch {
		case a == "-e" || a == "--expression":
			expectScript = true
			continue
		case strings.HasPrefix(a, "-i") || strings.Contains(a, "--in-place"):
			return "sed -i (in-place edit)"
		case a == "-f" || a == "--file" || strings.HasPrefix(a, "-f") && len(a) > 2 || strings.HasPrefix(a, "--file="):
			return "sed -f (program from a file the gate cannot see)"
		}
		script := ""
		switch {
		case expectScript:
			script, expectScript = a, false
		case strings.HasPrefix(a, "--expression="):
			script = a[len("--expression="):]
		case strings.HasPrefix(a, "-e") && len(a) > 2:
			script = a[2:]
		case !strings.HasPrefix(a, "-"):
			if viaOption || scriptSeen {
				continue // an input file
			}
			script, scriptSeen = a, true
		default:
			continue // an option whose script is another word
		}
		if rule := sedScriptRule(script); rule != "" {
			return rule
		}
	}
	return ""
}

// sedScriptRule judges one sed script: a `w` command writes, GNU `e` runs.
func sedScriptRule(s string) string {
	if sedWriteRe.MatchString(s) {
		return "sed w (writes a file)"
	}
	for _, c := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == '{' || r == '}' || r == '\n' }) {
		cmd := sedSkipAddress(strings.TrimSpace(c))
		if strings.HasPrefix(cmd, "!") {
			cmd = strings.TrimSpace(cmd[1:])
		}
		if len(cmd) > 0 && cmd[0] == 'e' && (len(cmd) == 1 || !isASCIILetters(cmd[1:2])) {
			return "sed e (runs a shell command — GNU sed)"
		}
		if len(cmd) > 3 && cmd[0] == 's' {
			switch sedSubstitutionFlags(cmd) {
			case "e":
				return "sed e (runs a shell command — GNU sed)"
			case "w":
				return "sed w (writes a file)"
			}
		}
	}
	return ""
}

// sedSkipAddress removes the leading address (numeric, $, /re/ with escaped
// slashes, \N, and a range joined by a comma).
func sedSkipAddress(c string) string {
	for i := 0; i < len(c); {
		switch {
		case c[i] >= '0' && c[i] <= '9':
			for i < len(c) && c[i] >= '0' && c[i] <= '9' {
				i++
			}
		case c[i] == '$' || c[i] == ',' || c[i] == '+' || c[i] == '~':
			i++
		case c[i] == '/':
			i++
			for i < len(c) && c[i] != '/' {
				if c[i] == '\\' && i+1 < len(c) {
					i++
				}
				i++
			}
			i++ // the closing slash
			for i < len(c) && (c[i] == 'I' || c[i] == 'M' || c[i] == 'g') {
				i++
			}
		case c[i] == '\\':
			i += 2
		default:
			return c[i:]
		}
	}
	return ""
}

// sedSubstitutionFlags judges `s<delim>…<delim>…<delim>flags` with any
// non-letter delimiter: "e" when GNU's execute flag is present, "w" when the
// pattern space is written to a file, "" otherwise.
func sedSubstitutionFlags(cmd string) string {
	delim := cmd[1]
	if isASCIILetters(cmd[1:2]) || delim == '\\' || delim == ' ' {
		return ""
	}
	i, found := 2, 0
	for ; i < len(cmd) && found < 2; i++ {
		if cmd[i] == '\\' && i+1 < len(cmd) {
			i++
			continue
		}
		if cmd[i] == delim {
			found++
		}
	}
	if found < 2 {
		return ""
	}
	for ; i < len(cmd); i++ {
		c := cmd[i]
		if c == 'e' {
			return "e"
		}
		if c == 'w' {
			return "w"
		}
		if !isASCIILetters(cmd[i:i+1]) && (c < '0' || c > '9') {
			break // the flags ended
		}
	}
	return ""
}

// findRule: -exec / -ok run a command, -delete removes, -fprint* write.
func findRule(args []string) string {
	for _, a := range args {
		for _, opt := range []string{"-exec", "-ok", "-delete", "-fprint", "-fls"} {
			if strings.HasPrefix(a, opt) {
				return "find " + opt + " (runs a command, deletes or writes)"
			}
		}
	}
	return ""
}

// tarRule: an extract, create, append, update or concatenate flag, in any
// spelling — bundled short options (-xzf, -czf), long forms, and the
// old-style `tar xzf`.
func tarRule(args []string) string {
	for _, a := range args {
		if a == "--to-command" || strings.HasPrefix(a, "--to-command=") {
			return "tar --to-command (pipes the archive into a command)"
		}
	}
	// -O in ANY position sends everything to stdout (`tar -O -xf x.tar`,
	// `tar -xf x.tar -O`, `tar -x --to-stdout -f x.tar`): nothing is written
	// to disk — unless an archive is being modified (-r -u -A --delete).
	toStdout, modifies, letters := false, false, ""
	for _, a := range args {
		if a == "--to-stdout" || a == "-O" {
			toStdout = true
		}
		if tarLongWrites[a] && a != "--extract" && a != "--get" && a != "--create" {
			modifies = true // append / update / catenate / delete an archive
		}
		if len(a) > 1 && a[0] == '-' && a[1] != '-' {
			letters += a[1:]
		}
	}
	if strings.ContainsAny(letters, "ruAd") {
		modifies = true
	}
	if toStdout && !modifies {
		return ""
	}
	for k, a := range args {
		if tarLongWrites[a] {
			return "tar " + a + " (unpacks or repacks)"
		}
		letters := ""
		switch {
		case len(a) > 1 && a[0] == '-' && a[1] != '-':
			letters = a[1:]
		case k == 0 && len(a) > 1 && a[0] != '-':
			letters = a
		}
		if isASCIILetters(letters) && strings.ContainsAny(letters, "xcruA") && !strings.ContainsRune(letters, 'O') {
			return "tar -x/-c/-r/-u/-A (unpacks or repacks)"
		}
	}
	return ""
}

// curlRule: curl prints to stdout unless told to save — an output file
// (-o/--output, -D/--dump-header, -c/--cookie-jar: a target of "-" keeps
// stdout), the remote name (-O/-J), a trace, an etag file, a config or an
// upload. Short options cluster (`-sSo out`), so a cluster is walked
// letter by letter past the letters that take a value.
func curlRule(args []string) string {
	for k := 0; k < len(args); k++ {
		a := args[k]
		if a == "--" {
			return ""
		}
		if strings.HasPrefix(a, "--") {
			name, value := a[2:], ""
			if eq := strings.Index(name, "="); eq >= 0 {
				name, value = name[:eq], name[eq+1:]
			}
			if curlLongWrites[name] {
				return "curl --" + name + " (sends or saves a file)"
			}
			if name == "output" || name == "dump-header" {
				if value == "" && k+1 < len(args) {
					value = args[k+1]
					k++
				}
				if value != "-" && value != "/dev/null" {
					return "curl --" + name + " (saves a file)"
				}
			}
			continue
		}
		if len(a) > 1 && a[0] == '-' {
			rest := a[1:]
			for len(rest) > 0 {
				letter := rest[0]
				rest = rest[1:]
				switch letter {
				case 'O', 'J':
					return "curl -O/-J (saves under the remote name)"
				case 'o', 'D', 'c', 'K', 'T':
					value := rest
					if value == "" && k+1 < len(args) {
						value = args[k+1]
						k++
					}
					if (letter == 'o' || letter == 'D' || letter == 'c') && (value == "-" || value == "/dev/null") {
						rest = ""
						continue // still stdout
					}
					return "curl -" + string(letter) + " (sends or saves a file)"
				default:
					if curlShortWithValue[letter] {
						if rest == "" {
							if k+1 < len(args) {
								k++ // the next argument is this option's value
							}
							break
						}
						rest = "" // the rest of the cluster is this option's value
					}
				}
			}
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

// goRule: `go test` (without -c), `go vet`, `go list`, `go version`, `go
// doc`, `go help` and `go env` (without -w) are diagnosis work; everything
// that builds, installs, fetches, formats or generates writes.
func goRule(args []string) string {
	if len(args) == 0 {
		return ""
	}
	switch args[0] {
	case "test":
		for k, a := range args[1:] {
			if a == "-c" || strings.HasPrefix(a, "-c=") || a == "--compile" {
				return "go test -c (writes a test binary)"
			}
			name, value := a, ""
			if eq := strings.IndexByte(a, '='); eq >= 0 {
				name, value = a[:eq], a[eq+1:]
			}
			switch name {
			case "-o", "-coverprofile", "-cpuprofile", "-memprofile", "-blockprofile", "-mutexprofile", "-trace":
				// A space-separated value is the next word: `go test
				// -coverprofile /dev/null ./...` writes nothing.
				if value == "" && k+1 < len(args[1:]) && args[1:][k+1] == "/dev/null" {
					continue
				}
				if value != "/dev/null" {
					return "go test " + name + " (writes a file)"
				}
			case "--outputdir":
				return "go test --outputdir (writes files)"
			}
		}
		return ""
	case "vet", "list", "version", "doc", "help", "bug":
		return ""
	case "env":
		for _, a := range args[1:] {
			if a == "-w" || a == "-u" || a == "--unset" {
				return "go env -w/-u (changes the go environment file)"
			}
		}
		return ""
	case "mod":
		if len(args) > 1 && goModWriteSubcommands[args[1]] {
			return "go mod " + args[1] + " (changes the module files)"
		}
		return ""
	}
	if args[0] == "tool" {
		if len(args) > 1 && goToolReadSubcommands[args[1]] {
			if args[1] == "cover" || args[1] == "pprof" {
				for _, a := range args[2:] {
					if (args[1] == "cover" && (a == "-o" || strings.HasPrefix(a, "-o="))) || a == "--output" || strings.HasPrefix(a, "--output=") {
						return "go tool " + args[1] + " -o (writes a file)"
					}
					if args[1] == "pprof" && (a == "-output" || strings.HasPrefix(a, "-output=") || a == "-png" || a == "-svg" || a == "-pdf" || a == "--png" || a == "--svg" || a == "--pdf") {
						return "go tool pprof " + a + " (writes a file)"
					}
				}
			}
			return ""
		}
		return "go tool (builds, installs or changes the tree)"
	}
	if goWriteSubcommands[args[0]] {
		return "go " + args[0] + " (builds, installs or changes the tree)"
	}
	return ""
}

// writeFlagRule: gofmt / goimports / gofumpt rewrite files with -w.
func writeFlagRule(verb string, args []string) string {
	for _, a := range args {
		if a == "-w" {
			return verb + " -w (rewrites files)"
		}
	}
	return ""
}

// packageManagerRule: the run / test / build / install / publish forms of
// npm, pnpm, yarn, bun and deno write; the listing forms read; an unknown
// subcommand writes (the tools evolve faster than this table).
func packageManagerRule(verb string, args []string) string {
	if len(args) == 0 {
		return "" // usage
	}
	sub := args[0]
	if pkgReadSubcommands[sub] {
		return ""
	}
	if sub == "audit" {
		if len(args) > 1 && (args[1] == "fix" || args[1] == "--fix") {
			return verb + " audit fix (installs and rewrites the lockfile)"
		}
		return "" // reads the dependency tree; installs nothing
	}
	if sub == "get" {
		return "" // npm get <key>
	}
	if sub == "config" && len(args) > 1 && (args[1] == "get" || args[1] == "list" || args[1] == "ls") {
		return ""
	}
	if sub == "pkg" && len(args) > 1 && args[1] == "get" {
		return ""
	}
	if sub == "run" && len(args) == 1 {
		return "" // lists the scripts
	}
	if verb == "deno" && (sub == "info" || sub == "lint" || sub == "check" || sub == "types" || sub == "doc") {
		return ""
	}
	return verb + " " + sub + " (runs, installs, builds or publishes)"
}

// uvRule: uv's install / environment / run forms write.
func uvRule(args []string) string {
	if len(args) == 0 {
		return ""
	}
	switch args[0] {
	case "pip":
		return anyArgRule("uv pip", args[1:], "install", "uninstall", "sync")
	case "--version", "version", "help", "tree", "git":
		return ""
	case "python":
		if len(args) > 1 && (args[1] == "list" || args[1] == "find") {
			return ""
		}
	case "tool":
		if len(args) > 1 && (args[1] == "list" || args[1] == "dir") {
			return ""
		}
	}
	if args[0] == "cache" && len(args) > 1 && args[1] == "dir" {
		return ""
	}
	return "uv " + args[0] + " (installs, creates an environment or runs code)"
}

// pythonRule: -c in any short cluster (-ic, -Oc), `-m <module>` outside a
// small read-only set, and — with no program argument — a program read from
// stdin (`python3 <<'EOF'`, `echo … | python3`, `python3 < script.py`), which
// runs whatever arrives (ship review round 2).
func pythonRule(verb string, args []string, stdinLive bool) string {
	for k := 0; k < len(args); k++ {
		a := args[k]
		if a == "--" {
			return ""
		}
		if a == "-" {
			return verb + " - (program read from stdin)"
		}
		if len(a) >= 2 && a[0] == '-' && a[1] != '-' && strings.ContainsRune(a[1:], 'c') {
			return verb + " -c (inline code)"
		}
		if strings.HasPrefix(a, "-m") && len(a) > 2 {
			// The attached spelling (-mjson.tool): the same module rule.
			if rule := pythonModuleRule(verb, a[2:], args[k+1:]); rule != "" {
				return rule
			}
			return ""
		}
		if a == "-m" {
			if k+1 >= len(args) {
				return verb + " -m <module> (runs a module)"
			}
			if rule := pythonModuleRule(verb, args[k+1], args[k+2:]); rule != "" {
				return rule
			}
			return "" // a read-only module consumes its input as DATA
		}
		if !strings.HasPrefix(a, "-") {
			break // the script: everything after it belongs to the script
		}
	}
	if stdinLive && !operandNamesProgram("python", "WXQ", args) {
		return verb + " (program read from stdin)"
	}
	return ""
}

// pythonModuleRule: a read-only -m module only reads and prints; pip, site
// and zipfile have read subcommands.
func pythonModuleRule(verb, module string, rest []string) string {
	if pythonReadModules[module] {
		return ""
	}
	switch module {
	case "pip":
		if len(rest) > 0 {
			switch rest[0] {
			case "list", "show", "check", "freeze", "--version", "-V", "help", "--help", "index", "debug", "inspect":
				return ""
			case "config":
				if len(rest) > 1 && (rest[1] == "list" || rest[1] == "get" || rest[1] == "debug") {
					return ""
				}
			case "cache":
				if len(rest) > 1 && (rest[1] == "list" || rest[1] == "info") {
					return ""
				}
			}
		}
	case "site":
		return ""
	case "zipfile":
		if len(rest) > 0 && (rest[0] == "-l" || rest[0] == "--list") {
			return ""
		}
	}
	if infoOnly(rest) {
		return "" // `python3 -m venv --help`, `-m pytest --version`
	}
	return verb + " -m <module> (runs a module)"
}

// nodeRule: -e / -p / --eval / --print / -r / --require / --import / a
// program from stdin, in any short cluster.
func nodeRule(args []string, stdinLive bool) string {
	for _, a := range args {
		if a == "--" {
			return ""
		}
		if a == "-" || a == "-e" || a == "-r" || a == "-p" || a == "--eval" || a == "--print" || a == "--require" ||
			strings.HasPrefix(a, "--eval=") || strings.HasPrefix(a, "--print=") || strings.HasPrefix(a, "--require=") ||
			strings.HasPrefix(a, "--import=") || strings.HasPrefix(a, "--preload=") {
			return "node (inline code or a preloaded module)"
		}
		if len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsAny(a[1:], "epr") {
			return "node (inline code or a preloaded module)"
		}
		if !strings.HasPrefix(a, "-") {
			break // the script: everything after it belongs to the script
		}
	}
	if stdinLive && !operandNamesProgram("node", "", args) {
		return "node (program read from stdin)"
	}
	return ""
}

// perlRule: -i / -e / -E (any case of E, which also runs inline code) in a
// short cluster; with no program argument, a program read from stdin runs.
func perlRule(args []string, stdinLive bool) string {
	for _, a := range args {
		if a == "--" {
			return ""
		}
		if strings.HasPrefix(a, "-M") || strings.HasPrefix(a, "-m") {
			return "perl -m/-M (loads a module)" // -M POSIX, -MPOSIX, -mfoo: code loads
		}
		if len(a) > 1 && a[0] == '-' && a[1] != '-' {
			// A cluster whose FIRST letter takes a value (-Ilib, -CSD, -F…)
			// carries that value in the rest of the cluster: not inline code.
			if !strings.ContainsRune("ICFDx", rune(a[1])) && strings.ContainsAny(a[1:], "ieE") {
				return "perl -i/-e/-E (in-place or inline code)"
			}
		}
		if !strings.HasPrefix(a, "-") {
			break // the script: everything after it belongs to the script
		}
	}
	if stdinLive && !operandNamesProgram("perl", "IMmCFDx", args) {
		return "perl (program read from stdin)"
	}
	return ""
}

// unzipRule: listing, testing and printing members read; extraction writes.
func unzipRule(args []string) string {
	for _, a := range args {
		if len(a) > 1 && a[0] == '-' && strings.ContainsAny(a[1:2], "lvtcpZ") {
			return "" // -l list, -v verbose list, -t test, -p/-c print to stdout, -Z zipinfo
		}
	}
	return "write-class verb: unzip"
}

// shellInfoOnly: the shell is asked about itself (`bash --version`,
// `zsh --help`, `sh -v`): the first option is an information flag.
// shellInfoOnly: the shell is asked about itself and runs nothing
// (`bash --version`, `zsh --help`, `sh -?`). The short -h / -v / -V are
// OPTIONS (-h hashall, -v verbose) — `bash -h -c 'rm -rf x'` runs the code
// (ship review round 5).
func shellInfoOnly(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "--version", "--help", "-?":
		return true
	}
	return false
}

// syntaxCheckOnly: `bash -n file` parses the file without running anything.
func syntaxCheckOnly(args []string) bool {
	// Bash stops parsing options at the first operand and at -c's string:
	// `bash -c 'rm x' -n` RUNS the code (the trailing -n is $0). Only a
	// leading -n parses without running (ship review round 3).
	for _, a := range args {
		if a == "-n" || a == "--noexec" {
			return true
		}
		if !strings.HasPrefix(a, "-") || a == "-c" {
			return false
		}
	}
	return false
}

// inlineCodeRule: ruby -e, php -r and lua -e take a program on the command
// line, alone or inside a short cluster.
// phpInlineLong: php's long options that carry inline code.
var phpInlineLong = map[string]bool{"run": true, "process-begin": true, "process-code": true, "process-file": true,
	"process-end": true, "process-header": true}

func inlineCodeRule(verb string, args []string, stdinLive bool) string {
	flag, valueShort := "e", ""
	switch verb {
	case "php":
		// php -r and the process-code family (-B -R -E -H, --run,
		// --process-begin/code/end/header) all take inline PHP.
		flag, valueShort = "r", "dctz"
	case "ruby":
		valueShort = "ICFK0xy"
	case "lua":
		valueShort = "\x00" // no value-taking option; -l loads a module (code)
	}
	named := false
	for k, a := range args {
		if a == "--" {
			return ""
		}
		if a == "-" && verb == "ruby" {
			return "ruby - (program read from stdin)"
		}
		if verb == "php" {
			if a == "-f" || a == "--file" || strings.HasPrefix(a, "--file=") {
				// php -f <file>: the script is the option's value — unless
				// that value is stdin itself.
				named = true
				if k+1 < len(args) && isStdinName(args[k+1]) {
					named = false
				}
				continue
			}
			if strings.HasPrefix(a, "--") && phpInlineLong[strings.Split(a[2:], "=")[0]] {
				return "php " + a + " (inline code)"
			}
			if len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsAny(a[1:], "rBREH") {
				return "php -r/-B/-R/-E/-H (inline code)"
			}
		}
		if len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsRune(a[1:], rune(flag[0])) {
			return verb + " -" + flag + " (inline code)"
		}
		// ruby -r / lua -l load a library: code runs.
		if !strings.HasPrefix(a, "--") && (strings.HasPrefix(a, "-r") && verb == "ruby" || strings.HasPrefix(a, "-l") && verb == "lua") {
			return verb + " " + a[:2] + " (loads a module)"
		}
		if !strings.HasPrefix(a, "-") {
			break // the script: everything after it belongs to the script
		}
	}
	if stdinLive && !named && !operandNamesProgram(verb, valueShort, args) {
		return verb + " (program read from stdin)"
	}
	return ""
}

// anyArgRule names the rule when any argument is one of the write words.
func anyArgRule(verb string, args []string, writeWords ...string) string {
	for _, a := range args {
		for _, w := range writeWords {
			if a == w {
				return verb + " " + w + " (changes the environment)"
			}
		}
	}
	return ""
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

func onlyLetters(s, set string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune(set, c) {
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
	verbAt := -1
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
// operands), `command`, `builtin` and `nohup`, and reduces absolute paths to
// their base name (/bin/rm → rm). Returns the verb's word index, or -1 when
// no verb remains (e.g. a bare `env`).
func resolveVerb(words []string) (int, string) {
	toks := make([]shTok, len(words))
	for i, w := range words {
		toks[i] = shTok{text: w}
	}
	return resolveVerbTok(toks)
}

// resolveVerbTok is resolveVerb over lexed tokens: a value-taking option
// whose own word carries an expansion (`env -u$(echo FOO) rm x`) holds its
// value already and must not swallow the command word. The wrapper names
// match without case: macOS's default filesystem runs `ENV` and `NOHUP`.
func resolveVerbTok(words []shTok) (int, string) {
	i := 0
	for i < len(words) {
		// Leading assignments before anything else: FOO=1 rm x
		if envAssign.MatchString(words[i].text) {
			i++
			continue
		}
		if words[i].text == "" && !words[i].expanded {
			// The empty word a closing quote leaves names no program. (A
			// COMPUTED empty word is the command — `X=1 "$(echo rm)" -rf x`,
			// `X="$Y" "$(echo rm)" -rf x`.)
			i++
			continue
		}
		base := words[i].text
		if strings.ContainsRune(base, '/') {
			base = path.Base(base)
		}
		switch strings.ToLower(base) {
		case "env":
			i++
			for i < len(words) {
				a := words[i].text
				if a == "--" {
					i++
					break
				}
				if envAssign.MatchString(a) || a == "-i" || a == "--ignore-environment" || a == "-0" {
					i++
					continue
				}
				if a == "" && !words[i].expanded {
					i++ // the empty word a closing quote leaves: not the command
					continue
				}
				if a == "-" {
					// Ends env's own options (`env - NODE_ENV=test node x.js`);
					// alone it names nothing — an ambiguous wrapper.
					if i+1 >= len(words) {
						return i, "__ambiguous_wrapper__"
					}
					i++
					continue
				}
				if a == "-S" || a == "--split-string" {
					i += 2 // the option and its argument string
					continue
				}
				if a == "-u" || a == "--unset" || a == "-C" || a == "--chdir" {
					if words[i].expanded {
						i++
						continue
					}
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
			for i < len(words) && (words[i].text == "-p" || words[i].text == "--") {
				i++
			}
			if i < len(words) && (words[i].text == "-v" || words[i].text == "-V") {
				return -1, ""
			}
			if i < len(words) && strings.HasPrefix(words[i].text, "-") {
				return i, "__ambiguous_wrapper__"
			}
		case "nohup":
			i++
			if i < len(words) && words[i].text == "--" {
				i++
			}
			if i < len(words) && strings.HasPrefix(words[i].text, "-") {
				return i, "__ambiguous_wrapper__"
			}
		default:
			return i, base
		}
	}
	return -1, ""
}
