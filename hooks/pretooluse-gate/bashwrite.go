package main

// bashwrite.go — the conservative Bash write-class heuristic (Task 3 ruling
// 4; hardened in review round 1, I4). Bash has no structural write signature,
// so the gate classifies the command text: anything that plausibly mutates
// the filesystem or repo state is treated exactly like Edit/Write.
// Conservative means erring toward "write": a false positive costs one denied
// command, a false negative costs the iron law.
//
// Write classes (the ruled list, kept deliberately short and testable):
//   - any redirection to a file: >, >>, 2>, &>, … (any '>' in the command)
//   - command-position mutation verbs: rm, mv, cp, tee, chmod, chown,
//     touch, mkdir — recognized behind absolute paths (/bin/rm, /usr/bin/git)
//     and wrappers (command, env with VAR=val operands, nohup), with
//     leading VAR=value assignments skipped
//   - sed in-place (-i / --in-place)
//   - git with a state-rewriting subcommand:
//     commit, push, reset, checkout, rebase, merge, stash
//
// Segmentation: the command splits on ; | & and newlines, with $( and `
// exposed as breaks (echo $(rm x) runs rm); each segment is tokenized with
// single/double quotes respected, so `git -C "/tmp/a repo" reset --hard`
// keeps the spaced path as ONE flag value (bare whitespace splitting
// mis-classified that as read-only). `command`/`nohup` drop only themselves
// (their flags take no values — `command -v rm` stays read-only); `env`
// drops itself plus its flags and VAR=val operands.
//
// "cp when target isn't read-only" is simplified to always write-class:
// deciding target writability from a command string is unreliable, and the
// conservative side is to block.
//
// Known residuals (accepted, documented): sudo-prefixed commands, interactive
// editors (vim/nano/ed), dd/truncate/ln/install, xargs-chained writers, git
// global flags outside the value-taking set handled below, and any '>' inside
// a quoted argument (e.g. echo "a->b") is treated as a redirect —
// over-blocking by design.

import (
	"path"
	"regexp"
	"strings"
)

var (
	writeClassVerbs = map[string]bool{
		"rm": true, "mv": true, "cp": true, "tee": true,
		"chmod": true, "chown": true, "touch": true, "mkdir": true,
	}
	gitWriteSubcommands = map[string]bool{
		"commit": true, "push": true, "reset": true, "checkout": true,
		"rebase": true, "merge": true, "stash": true,
	}
	// gitFlagsWithValue — global git flags whose argument must be skipped
	// before the subcommand: git -C /repo commit …
	gitFlagsWithValue = map[string]bool{
		"-C": true, "-c": true, "--git-dir": true, "--work-tree": true,
		"--namespace": true, "--exec-path": true,
	}
	envAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
)

// isWriteClassCommand reports whether the Bash command plausibly writes.
func isWriteClassCommand(command string) bool {
	if command == "" {
		return false
	}
	// Any redirection to a file — >, >>, 2>, &>, 2>&1 — counts.
	if strings.Contains(command, ">") {
		return true
	}
	// Expose command substitution to the segment splitter.
	norm := strings.ReplaceAll(command, "$(", "; ")
	norm = strings.ReplaceAll(norm, "`", "; ")
	for _, seg := range strings.FieldsFunc(norm, func(r rune) bool {
		return r == ';' || r == '|' || r == '&' || r == '\n' || r == '\r'
	}) {
		if classifySegment(tokenize(seg)) {
			return true
		}
	}
	return false
}

// tokenize splits one shell segment into words with quoting respected:
// single- and double-quoted spans join the current word (quotes stripped).
// Escapes are not interpreted — conservative and sufficient for verbs.
func tokenize(s string) []string {
	var toks []string
	var cur strings.Builder
	var quote rune
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return toks
}

// classifySegment reports whether a tokenized segment starts a mutation.
func classifySegment(words []string) bool {
	verbIdx, verb := resolveVerb(words)
	if verbIdx < 0 {
		return false
	}
	switch verb {
	case "sed":
		for _, w := range words[verbIdx+1:] {
			if strings.HasPrefix(w, "-i") || strings.Contains(w, "--in-place") {
				return true
			}
		}
	case "git":
		args := words[verbIdx+1:]
		for j := 0; j < len(args); j++ {
			a := args[j]
			if gitFlagsWithValue[a] {
				j++ // skip the flag's value (quotes already joined it)
				continue
			}
			if strings.HasPrefix(a, "-") {
				continue
			}
			if gitWriteSubcommands[a] {
				return true
			}
			break // first real subcommand; not a write class
		}
	default:
		return writeClassVerbs[verb]
	}
	return false
}

// resolveVerb finds the real verb of a tokenized segment: it skips leading
// VAR=value assignments, drops the wrappers `env` (plus its flags and
// operands), `command` and `nohup`, and reduces absolute paths to their base
// name (/bin/rm → rm). Returns the verb's token index, or -1 when no verb
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
			// env's own flags and VAR=val operands precede the command.
			for i < len(words) && (strings.HasPrefix(words[i], "-") || envAssign.MatchString(words[i])) {
				i++
			}
		case "command", "nohup":
			// Their flags take no values — drop only the wrapper itself,
			// so `command -v rm` stays read-only.
			i++
		default:
			return i, base
		}
	}
	return -1, ""
}
