package main

// bashwrite.go — the conservative Bash write-class heuristic (Task 3 ruling
// 4). Bash has no structural write signature, so the gate classifies the
// command text: anything that plausibly mutates the filesystem or repo state
// is treated exactly like Edit/Write. Conservative means erring toward
// "write": a false positive costs one denied command, a false negative costs
// the iron law.
//
// Write classes (the ruled list, kept deliberately short and testable):
//   - any redirection to a file: >, >>, 2>, &>, … (any '>' in the command)
//   - command-position verbs: rm, mv, cp, tee, chmod, chown, touch, mkdir
//   - sed in-place (-i / --in-place)
//   - git with a state-rewriting subcommand:
//     commit, push, reset, checkout, rebase, merge, stash
//
// Command position means the first word of any segment, where the command is
// split on ; | & and newlines, after $( and ` are exposed as segment breaks
// (echo $(rm x) runs rm). Leading VAR=value assignments are skipped
// (FOO=1 rm x). "cp when target isn't read-only" is simplified to always
// write-class: deciding target writability from a command string is
// unreliable, and the conservative side is to block.
//
// Known residuals (accepted, documented): sudo-prefixed commands, interactive
// editors (vim/nano/ed), dd/truncate/ln/install, xargs-chained writers,
// git global flags outside the small value-taking set handled below, and any
// '>' appearing inside a quoted argument (e.g. echo "a->b") is treated as a
// redirect — over-blocking by design.

import (
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
		words := strings.Fields(seg)
		// Skip leading environment assignments: FOO=1 rm …
		i := 0
		for i < len(words) && envAssign.MatchString(words[i]) {
			i++
		}
		if i >= len(words) {
			continue
		}
		switch words[i] {
		case "sed":
			for _, w := range words[i+1:] {
				if strings.HasPrefix(w, "-i") || strings.Contains(w, "--in-place") {
					return true
				}
			}
		case "git":
			args := words[i+1:]
			for j := 0; j < len(args); j++ {
				a := args[j]
				if gitFlagsWithValue[a] {
					j++ // skip the flag's value
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
			if writeClassVerbs[words[i]] {
				return true
			}
		}
	}
	return false
}
