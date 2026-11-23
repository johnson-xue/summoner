package main

// shelllex.go — a conservative shell lexer for the write-class classifier.
//
// The classifier used to work on the raw command text: any '>' anywhere was
// a redirect, and segments were split by string surgery. That misjudged in
// both directions at once — `ls … 2>/dev/null` and `awk 'NR>=1213' f` (two
// of the incident run's reads) were "writes", while a verb produced by a
// substitution slipped through. So the command is first LEXED: the lexer
// walks the text the way the shell does and says, for every character,
// whether the shell would treat it as an operator or as literal data.
//
// What the lexer knows (and nothing more — it is not a shell parser):
//
//   - quoting: '…' (all literal), "…" (literal except \ ` and $( )),
//     $'…' (ANSI-C: backslash escapes, nothing expands), and an unquoted
//     backslash (the next character is literal — `\>` is text, not a
//     redirect);
//   - comments: an unquoted # at the start of a word runs to end of line;
//   - arithmetic: $(( … )) and the (( … )) command are inert for the
//     redirect channel; their words are still judged (see below);
//   - the conditional [[ … ]] in command position — at the start, after an
//     operator, or after a keyword (`if [[ a > b ]]`): its < > ( ) are
//     comparison operators, not redirects;
//   - parameter expansions ${…}: their grammar is bash's own (a '>' inside
//     is text); only the command substitutions within are live;
//   - heredoc bodies: text up to the delimiter line. A '>' in a body is
//     data. Substitutions inside a body ARE live, so they are lexed;
//   - command substitution: $( … ) and `…` run a command of their own. The
//     nested text is lexed as a command in its own right and spliced in
//     between break markers, so a verb inside a substitution, a double
//     quoted string or a heredoc body is judged like any other verb;
//   - command boundaries: ; | & newlines and the parentheses of a
//     subshell, a function body, a case arm or a process substitution.
//
// Tokens are words (dequoted) and operators. Two marker operators, "$("
// and "$)", stand for a spliced substitution. A word whose VALUE bash
// computes when it runs is marked `expanded`: it holds a substitution, an
// arithmetic or parameter expansion, or ANSI-C quoting. Its text here is
// only a spelling of that value, so in command position it names no known
// program — `"$(echo rm)" -rf x` and `$'\x72\x6d' -rf x` both run rm.
//
// Unbalanced input never fails: an unterminated quote, substitution or
// heredoc simply runs to the end of the text — the conservative reading for
// a classifier whose false negatives cost the iron law. The lexer is
// linear in the length of the text: the gate runs it on every Bash call
// with a ten-second budget.

import (
	"strings"
	"unicode/utf8"
)

type shTokKind int

const (
	shWord shTokKind = iota
	shOp
)

// shTok is one lexed token. A word carries its dequoted text; an operator
// carries its spelling (";", "|", "&&", ">", ">>", ">&", "<<<", "\n", …).
type shTok struct {
	kind shTokKind
	text string
	// attachedBefore / attachedAfter apply to the "$(" / "$)" markers:
	// literal characters of the same word touch the substitution there.
	attachedBefore bool
	attachedAfter  bool
	// expanded marks a word whose value bash computes when it runs (see
	// the file comment).
	expanded bool
}

const (
	opSubstOpen  = "$("
	opSubstClose = "$)"

	// opLexStop marks text the lexer could not get through. A hook that
	// hangs is worse than one that over-blocks, so every lexer loop carries
	// a progress guard: an iteration that does not advance emits this marker
	// and stops. The classifier answers a write for it — never a silent
	// truncation, which would leave the unread text unjudged.
	opLexStop = "!lex-stop"

	// opArithOpen / opArithClose bracket arithmetic — $(( … )) and the
	// (( … )) command. Inside arithmetic a '>' is a comparison, so the
	// redirect channel skips it; the words are still lexed as a command,
	// because a classifier that is not a shell parser must not let a
	// nesting spelling hide a verb (`((rm scratch))` is denied).
	opArithOpen  = "$(("
	opArithClose = "))"
)

// shBreaks are the operators that end the current command.
var shBreaks = map[string]bool{
	";": true, ";;": true, "\n": true,
	"|": true, "||": true, "|&": true, "&": true, "&&": true,
	// A subshell, a function body, a case arm and a process substitution
	// each begin or end a command: `(cd x && rm y)`, `f() { rm x; }; f`,
	// `case $x in a) rm x;; esac`, `cat <(rm x)`.
	"(": true, ")": true,
}

// shBoundaries are the characters that cannot continue a word.
const shBoundaries = " \t\n;&|<>()"

type pendingHeredoc struct {
	delim     string
	stripTabs bool
}

type lexer struct {
	r              []rune
	i              int
	toks           []shTok
	word           strings.Builder // the word under construction
	wordOpen       bool            // a quote or a character opened the current word ("" is a word)
	wordHasLiteral bool            // a literal character (not a quote) is in the current word
	wordExpanded   bool            // the word's value is computed when it runs
	pendingClose   int             // token index of the current word's last "$)" marker, -1 if none
	awaitDelim     *pendingHeredoc // the next word is this heredoc's delimiter
	pending        []pendingHeredoc
}

// lexShell tokenizes one shell command text.
func lexShell(src string) []shTok {
	l := &lexer{r: []rune(src), pendingClose: -1}
	l.run()
	return l.toks
}

func (l *lexer) op(text string) { l.toks = append(l.toks, shTok{kind: shOp, text: text}) }

// flush ends the current word. A word opened by a quote alone is still a
// word (the empty operand that once let `git -C "" reset` swallow its
// subcommand).
func (l *lexer) flush() {
	if l.wordOpen {
		text := l.word.String()
		l.toks = append(l.toks, shTok{kind: shWord, text: text, expanded: l.wordExpanded})
		if l.awaitDelim != nil {
			l.awaitDelim.delim = text
			l.awaitDelim = nil
		}
	}
	l.word.Reset()
	l.wordOpen = false
	l.wordHasLiteral = false
	l.wordExpanded = false
	l.pendingClose = -1
}

// put appends literal text to the current word. Text that spells an
// expansion ($X, a backtick) marks the word expanded.
func (l *lexer) put(s string) {
	l.word.WriteString(s)
	l.wordOpen = true
	l.wordHasLiteral = true
	if strings.ContainsAny(s, "$`") {
		l.wordExpanded = true
	}
}

// hasPrefix reports whether the text at the cursor starts with s. It
// compares runes in place: converting the rest of the text to a string per
// operator character made lexing quadratic, and a 300 KB command did not
// finish inside the gate's budget.
func (l *lexer) hasPrefix(s string) bool {
	n := utf8.RuneCountInString(s)
	if len(l.r)-l.i < n {
		return false
	}
	k := 0
	for _, r := range s {
		if l.r[l.i+k] != r {
			return false
		}
		k++
	}
	return true
}

// run is the lexer loop over l.r at l.i. The double-quoted state is a
// single flag: quoting does not nest across a double quote except through a
// substitution, and a substitution is lexed by a recursive run of its own.
func (l *lexer) run() {
	dquote := false
	at := -1
	for l.i < len(l.r) {
		if l.i == at {
			l.flush()
			l.op(opLexStop)
			return
		}
		at = l.i
		c := l.r[l.i]
		switch c {
		case '\\':
			// An unquoted backslash makes the next character literal. Inside
			// double quotes it only escapes $ ` " \ and newline; anything
			// else keeps both characters, as the shell does.
			if l.i+1 < len(l.r) {
				next := l.r[l.i+1]
				if next == '\n' {
					l.i += 2 // line continuation: neither character is part of any word
					continue
				}
				if dquote && !strings.ContainsRune("$`\"\\", next) {
					l.word.WriteRune('\\')
				}
				l.word.WriteRune(next)
				l.wordOpen = true
				l.wordHasLiteral = true
				l.i += 2
				continue
			}
			l.put("\\")
			l.i++
			continue

		case '\'':
			if dquote {
				l.put("'")
				l.i++
				continue
			}
			j := l.i + 1
			for j < len(l.r) && l.r[j] != '\'' {
				j++
			}
			l.word.WriteString(string(l.r[l.i+1 : j]))
			l.wordOpen = true
			l.i = j + 1
			continue

		case '"':
			if dquote && l.pendingClose >= 0 && l.i+1 < len(l.r) && !strings.ContainsRune(shBoundaries, l.r[l.i+1]) {
				// A closing quote with a word character right after it keeps
				// the word going: `"$(x)"/bin/tool` is one word, so the
				// substitution is not the whole of it.
				l.toks[l.pendingClose].attachedAfter = true
			}
			dquote = !dquote
			l.wordOpen = true
			l.i++
			continue

		case '$':
			if !dquote && l.i+1 < len(l.r) && l.r[l.i+1] == '\'' {
				// $'…' — ANSI-C quoting. Escapes are kept verbatim (the
				// redirect targets compare literally, so an escaped
				// "/dev/null" fails the exemption); the word is computed.
				j := l.i + 2
				for j < len(l.r) && l.r[j] != '\'' {
					if l.r[j] == '\\' && j+1 < len(l.r) {
						j++
					}
					j++
				}
				l.put(string(l.r[l.i+2 : j]))
				l.wordExpanded = true
				l.i = j + 1
				continue
			}
			if l.i+1 < len(l.r) && l.r[l.i+1] == '{' {
				// ${…} — a parameter expansion: its grammar is bash's own
				// (`>` inside is text: `${x:?err > file}` writes nothing);
				// only the command substitutions within it are live.
				end := braceEnd(l.r, l.i+2)
				l.put("${" + string(l.r[l.i+2:end]))
				l.spliceLine(l.r[l.i+2 : end])
				if end < len(l.r) {
					l.put("}")
				}
				l.i = end + 1
				continue
			}
			if l.hasPrefix("$((") {
				// Arithmetic expansion.
				end := parenEnd(l.r, l.i+2)
				l.arithmetic(string(l.r[l.i+3:end]), l.wordHasLiteral, l.attachedAfter(end, dquote))
				l.i = end + 1
				continue
			}
			if l.i+1 < len(l.r) && l.r[l.i+1] == '(' {
				l.substitution(dquote)
				continue
			}
			l.put("$")
			l.i++
			continue

		case '`':
			end := backtickEnd(l.r, l.i+1)
			l.splice(string(l.r[l.i+1:end]), l.wordHasLiteral, l.attachedAfter(end, dquote))
			l.i = end + 1
			continue

		case '#':
			if !dquote && !l.wordOpen {
				// A comment: an unquoted # at the start of a word.
				for l.i < len(l.r) && l.r[l.i] != '\n' {
					l.i++
				}
				continue
			}
			l.put("#")
			l.i++
			continue
		}

		if dquote {
			l.word.WriteRune(c)
			l.wordOpen = true
			l.wordHasLiteral = true
			l.i++
			continue
		}

		advance := 0
		switch c {
		case ' ', '\t', '\r':
			l.flush()
			if l.conditional() {
				continue
			}
			advance = 1
		case '\n':
			l.flush()
			if l.conditional() {
				continue
			}
			l.op("\n")
			l.awaitDelim = nil // a heredoc operator with no delimiter has no body
			l.i++
			l.heredocBodies()
			continue
		case ';':
			l.flush()
			if l.conditional() {
				continue
			}
			if l.hasPrefix(";;") {
				l.op(";;")
				advance = 2
			} else {
				l.op(";")
				advance = 1
			}
		case '|':
			l.flush()
			if l.conditional() {
				continue
			}
			switch {
			case l.hasPrefix("||"):
				l.op("||")
				advance = 2
			case l.hasPrefix("|&"):
				l.op("|&")
				advance = 2
			default:
				l.op("|")
				advance = 1
			}
		case '&':
			l.flush()
			if l.conditional() {
				continue
			}
			switch {
			case l.hasPrefix("&>>"):
				l.op("&>>")
				advance = 3
			case l.hasPrefix("&>"):
				l.op("&>")
				advance = 2
			case l.hasPrefix("&&"):
				l.op("&&")
				advance = 2
			default:
				l.op("&")
				advance = 1
			}
		case '(':
			l.flush()
			if l.conditional() {
				continue
			}
			if l.hasPrefix("((") {
				// Arithmetic command in command position.
				end := parenEnd(l.r, l.i+1)
				l.arithmetic(string(l.r[l.i+2:end]), false, false)
				l.i = end + 1
				continue
			}
			l.op("(")
			advance = 1
		case ')':
			l.flush()
			if l.conditional() {
				continue
			}
			l.op(")")
			advance = 1
		case '<':
			l.flush()
			if l.conditional() {
				continue
			}
			switch {
			case l.hasPrefix("<<<"):
				l.op("<<<")
				advance = 3
			case l.hasPrefix("<<-"):
				l.op("<<-")
				l.heredocPending(true)
				advance = 3
			case l.hasPrefix("<<"):
				l.op("<<")
				l.heredocPending(false)
				advance = 2
			case l.hasPrefix("<>"):
				l.op("<>")
				advance = 2
			default:
				l.op("<")
				advance = 1
			}
		case '>':
			l.flush()
			if l.conditional() {
				continue
			}
			switch {
			case l.hasPrefix(">>"):
				l.op(">>")
				advance = 2
			case l.hasPrefix(">|"):
				l.op(">|")
				advance = 2
			case l.hasPrefix(">&"):
				l.op(">&")
				advance = 2
			default:
				l.op(">")
				advance = 1
			}
		default:
			l.word.WriteRune(c)
			l.wordOpen = true
			l.wordHasLiteral = true
			advance = 1
		}
		l.i += advance
	}
	l.flush()
}

// attachedAfter reports whether literal characters of the same word follow
// the character at end (a quote closing the span is not one).
func (l *lexer) attachedAfter(end int, dquote bool) bool {
	if end >= len(l.r) {
		return false
	}
	if dquote && l.r[end] == '"' {
		return false
	}
	return !strings.ContainsRune(shBoundaries, l.r[end])
}

// substitution splices a $( … ) command substitution into the token stream.
func (l *lexer) substitution(dquote bool) {
	end := parenEnd(l.r, l.i+2) // index of the matching ')', or len
	l.splice(string(l.r[l.i+2:end]), l.wordHasLiteral, l.attachedAfter(end, dquote))
	l.i = end + 1
}

// splice lexes the text of a substitution (or a heredoc line) as a command
// in its own right and brackets it with break markers, so its verbs are
// judged like any other. A word stays open on a side where literal
// characters touch the substitution.
//
// A word that a QUOTE alone opened ("$(cmd)") is not flushed: it holds no
// literal text, and emitting an empty word there put a word token in front
// of the substitution — which is what let `"$(echo rm)" -rf x` read as "a
// command named empty string" instead of "a command supplied by the
// substitution" (ship review round 1).
func (l *lexer) splice(inner string, attachedBefore, attachedAfter bool) {
	openedByQuote := l.wordOpen && !l.wordHasLiteral
	if openedByQuote {
		l.word.Reset()
		l.wordOpen = false
		l.wordHasLiteral = false
		l.wordExpanded = false
	} else {
		l.wordExpanded = true // the prefix of a word that continues with the substitution
		l.flush()
	}
	l.toks = append(l.toks,
		shTok{kind: shOp, text: opSubstOpen, attachedBefore: attachedBefore, attachedAfter: attachedAfter},
		shTok{kind: shOp, text: ";"})
	l.toks = append(l.toks, lexShell(inner)...)
	l.toks = append(l.toks, shTok{kind: shOp, text: ";"},
		shTok{kind: shOp, text: opSubstClose, attachedBefore: attachedBefore, attachedAfter: attachedAfter})
	l.pendingClose = len(l.toks) - 1
	l.wordOpen = attachedAfter || openedByQuote
	l.wordHasLiteral = false
	l.wordExpanded = true
}

// arithmetic splices the content of an arithmetic context into the token
// stream, bracketed by the arithmetic markers. The word it belongs to is
// computed at run time.
func (l *lexer) arithmetic(inner string, attachedBefore, attachedAfter bool) {
	openedByQuote := l.wordOpen && !l.wordHasLiteral
	l.flush()
	l.toks = append(l.toks, shTok{kind: shOp, text: opArithOpen}, shTok{kind: shOp, text: ";"})
	l.toks = append(l.toks, lexShell(inner)...)
	l.toks = append(l.toks, shTok{kind: shOp, text: ";"}, shTok{kind: shOp, text: opArithClose})
	l.wordOpen = attachedAfter || openedByQuote
	l.wordHasLiteral = false
	l.wordExpanded = true
}

// conditional consumes a `[[ … ]]` conditional expression when the word just
// flushed was "[[" in command position — at the start, after an operator,
// or after a reserved word (`if [[…`, `while [[…`, `! [[…`, `{ [[…`): its
// < > ( ) are comparison operators there, and the redirect channel must not
// read them. Its text is inert except for command substitutions, which are
// live. An unterminated conditional swallows the rest of the text, as a
// syntax error would run nothing. Reports whether it consumed anything.
func (l *lexer) conditional() bool {
	n := len(l.toks)
	if n == 0 || l.toks[n-1].kind != shWord || l.toks[n-1].text != "[[" {
		return false
	}
	if n > 1 {
		prev := l.toks[n-2]
		atCommandStart := prev.kind == shOp && (shBreaks[prev.text] || prev.text == "(" || prev.text == ")")
		if prev.kind == shWord && shellReservedWords[prev.text] {
			atCommandStart = true
		}
		if !atCommandStart {
			return false // an argument named "[[", not the conditional
		}
	}
	end := condEnd(l.r, l.i)
	l.op(";")
	l.spliceLine(l.r[l.i:end])
	l.op(";")
	if end < len(l.r) {
		l.i = end + 2
	} else {
		l.i = end
	}
	return true
}

// condEnd returns the index of the `]]` that closes a conditional starting
// at from (a word boundary on both sides), or len(r).
func condEnd(r []rune, from int) int {
	for i := from; i < len(r); {
		switch r[i] {
		case '\\':
			i += 2
		case '\'':
			i++
			for i < len(r) && r[i] != '\'' {
				i++
			}
			i++
		case '"':
			i = dquoteEnd(r, i+1)
		case '`':
			i = backtickEnd(r, i+1) + 1
		case '$':
			if i+1 < len(r) && r[i+1] == '(' {
				i = parenEnd(r, i+2) + 1
			} else {
				i++
			}
		case ']':
			if i+1 < len(r) && r[i+1] == ']' && (i == from || strings.ContainsRune(shBoundaries, r[i-1])) &&
				(i+2 >= len(r) || strings.ContainsRune(shBoundaries, r[i+2])) {
				return i
			}
			i++
		default:
			i++
		}
	}
	return len(r)
}

// heredocPending notes that the next word is a heredoc delimiter.
func (l *lexer) heredocPending(stripTabs bool) {
	l.pending = append(l.pending, pendingHeredoc{stripTabs: stripTabs})
	l.awaitDelim = &l.pending[len(l.pending)-1]
}

// heredocBodies consumes the bodies of every heredoc opened on the line that
// just ended, in order.
func (l *lexer) heredocBodies() {
	for _, h := range l.pending {
		l.heredocBody(h)
	}
	l.pending = nil
}

// heredocBody consumes one heredoc body: text up to its delimiter line. No
// operators live in it, but command substitutions do, so they are lexed.
func (l *lexer) heredocBody(h pendingHeredoc) {
	for l.i < len(l.r) {
		end := l.i
		for end < len(l.r) && l.r[end] != '\n' {
			end++
		}
		line := l.r[l.i:end]
		for h.stripTabs && len(line) > 0 && line[0] == '\t' {
			line = line[1:]
		}
		// CRLF text ends its lines with "\r": bash compares the delimiter
		// without it, and a body that never ended would swallow the command
		// after it.
		if strings.TrimSuffix(string(line), "\r") == h.delim {
			l.i = end + 1 // past the delimiter's newline
			return
		}
		l.spliceLine(line)
		if end >= len(l.r) {
			l.i = end // unterminated body: it runs to the end of the text
			return
		}
		l.i = end + 1
	}
}

// spliceLine lexes the command substitutions of one inert span (a heredoc
// body, a conditional, a parameter expansion) into the token stream,
// between break markers.
func (l *lexer) spliceLine(line []rune) {
	for k := 0; k < len(line); {
		switch line[k] {
		case '\\':
			k += 2
		case '$':
			if k+1 < len(line) && line[k+1] == '(' {
				end := parenEnd(line, k+2)
				if k+2 < len(line) && line[k+2] == '(' {
					k = end + 1 // arithmetic expansion: inert
					continue
				}
				l.splice(string(line[k+2:end]), false, false)
				k = end + 1
			} else {
				k++
			}
		case '`':
			end := backtickEnd(line, k+1)
			l.splice(string(line[k+1:end]), false, false)
			k = end + 1
		default:
			k++
		}
	}
}

// parenEnd returns the index of the ')' that closes the parenthesis group
// opening just before from (every unquoted '(' counts, every unquoted ')'
// discounts), or len(r) when the group never closes.
func parenEnd(r []rune, from int) int {
	depth := 1
	for i := from; i < len(r); {
		switch r[i] {
		case '\\':
			i += 2
		case '\'':
			i++
			for i < len(r) && r[i] != '\'' {
				i++
			}
			i++
		case '"':
			i = dquoteEnd(r, i+1)
		case '`':
			i = backtickEnd(r, i+1) + 1
		case '(':
			depth++
			i++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
			i++
		default:
			i++
		}
	}
	return len(r)
}

// braceEnd returns the index of the '}' that closes a `${…}` parameter
// expansion opening just before from, or len(r).
func braceEnd(r []rune, from int) int {
	depth := 1
	for i := from; i < len(r); {
		switch r[i] {
		case '\\':
			i += 2
		case '\'':
			i++
			for i < len(r) && r[i] != '\'' {
				i++
			}
			i++
		case '"':
			i = dquoteEnd(r, i+1)
		case '`':
			i = backtickEnd(r, i+1) + 1
		case '$':
			if i+1 < len(r) && r[i+1] == '(' {
				i = parenEnd(r, i+2) + 1
			} else {
				i++
			}
		case '{':
			depth++
			i++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
			i++
		default:
			i++
		}
	}
	return len(r)
}

// dquoteEnd returns the index just past a double-quoted span starting at
// from; a substitution inside it may hold quotes of its own, so $( … ) is
// skipped as a unit.
func dquoteEnd(r []rune, from int) int {
	for i := from; i < len(r); {
		switch r[i] {
		case '\\':
			i += 2
		case '"':
			return i + 1
		case '$':
			if i+1 < len(r) && r[i+1] == '(' {
				i = parenEnd(r, i+2) + 1
			} else {
				i++
			}
		case '`':
			i = backtickEnd(r, i+1) + 1
		default:
			i++
		}
	}
	return len(r)
}

// backtickEnd returns the index of the backtick closing the one before
// from, or len(r).
func backtickEnd(r []rune, from int) int {
	for i := from; i < len(r); {
		switch r[i] {
		case '\\':
			i += 2
		case '`':
			return i
		default:
			i++
		}
	}
	return len(r)
}
