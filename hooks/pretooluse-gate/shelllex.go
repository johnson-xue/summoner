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
//   - arithmetic: $(( … )) and the (( … )) command are inert data;
//   - the conditional [[ … ]] in command position: its < > ( ) are
//     comparison operators, not redirects;
//   - heredoc bodies: text up to the delimiter line. A '>' in a body is
//     data. Substitutions inside a body ARE live, so they are lexed;
//   - command substitution: $( … ) and `…` run a command of their own. The
//     nested text is lexed as a command in its own right and spliced in
//     between break markers, so a verb inside a substitution, a double
//     quoted string or a heredoc body is judged like any other verb.
//
// Tokens are words (dequoted) and operators. Two marker operators, "$("
// and "$)", stand for a spliced substitution; each carries whether literal
// characters of the same word touch it there, which is what tells a
// substitution that IS a word (`"$(echo rm)" -rf x` — its output becomes the
// command) from one that merely builds part of a word
// (`$(git rev-parse …)/bin/tool`).
//
// Unbalanced input never fails: an unterminated quote, substitution or
// heredoc simply runs to the end of the text — the conservative reading for
// a classifier whose false negatives cost the iron law.

import (
	"strings"
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
	awaitDelim     *pendingHeredoc // the next word is this heredoc's delimiter
	pending        []pendingHeredoc
}

// lexShell tokenizes one shell command text.
func lexShell(src string) []shTok {
	l := &lexer{r: []rune(src)}
	l.run()
	return l.toks
}

func (l *lexer) op(text string) { l.toks = append(l.toks, shTok{kind: shOp, text: text}) }

func (l *lexer) lastOp() string {
	if n := len(l.toks); n > 0 && l.toks[n-1].kind == shOp {
		return l.toks[n-1].text
	}
	return ""
}

// flush ends the current word. A word opened by a quote alone is still a
// word (the empty operand that once let `git -C "" reset` swallow its
// subcommand).
func (l *lexer) flush() {
	if l.wordOpen {
		text := l.word.String()
		l.toks = append(l.toks, shTok{kind: shWord, text: text})
		if l.awaitDelim != nil {
			l.awaitDelim.delim = text
			l.awaitDelim = nil
		}
	}
	l.word.Reset()
	l.wordOpen = false
	l.wordHasLiteral = false
}

// put appends literal text to the current word.
func (l *lexer) put(s string) {
	l.word.WriteString(s)
	l.wordOpen = true
	l.wordHasLiteral = true
}

func (l *lexer) hasPrefix(s string) bool {
	return strings.HasPrefix(string(l.r[l.i:]), s)
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
			dquote = !dquote
			l.wordOpen = true
			l.i++
			continue

		case '$':
			if !dquote && l.i+1 < len(l.r) && l.r[l.i+1] == '\'' {
				// $'…' — ANSI-C quoting. Escapes are kept verbatim: the
				// classifier compares targets literally, so an escaped
				// "/dev/null" simply fails the exemption (conservative).
				j := l.i + 2
				for j < len(l.r) && l.r[j] != '\'' {
					if l.r[j] == '\\' && j+1 < len(l.r) {
						j++
					}
					j++
				}
				l.put(string(l.r[l.i+2 : j]))
				l.i = j + 1
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
			l.conditional()
			advance = 1
		case '\n':
			l.flush()
			l.conditional()
			l.op("\n")
			l.awaitDelim = nil // a heredoc operator with no delimiter has no body
			l.i++
			l.heredocBodies()
			continue
		case ';':
			l.flush()
			l.conditional()
			if l.hasPrefix(";;") {
				l.op(";;")
				advance = 2
			} else {
				l.op(";")
				advance = 1
			}
		case '|':
			l.flush()
			l.conditional()
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
			l.conditional()
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
			l.conditional()
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
			l.conditional()
			l.op(")")
			advance = 1
		case '<':
			l.flush()
			l.conditional()
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
			l.conditional()
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
func (l *lexer) splice(inner string, attachedBefore, attachedAfter bool) {
	// A quote alone may have opened the current word ("$(…)", `$(…)`" is one
	// word whose value is the output); a literal character before the
	// substitution already has its word flushed.
	openedByQuote := l.wordOpen && !l.wordHasLiteral
	l.flush()
	l.toks = append(l.toks,
		shTok{kind: shOp, text: opSubstOpen, attachedBefore: attachedBefore, attachedAfter: attachedAfter},
		shTok{kind: shOp, text: ";"})
	l.toks = append(l.toks, lexShell(inner)...)
	l.toks = append(l.toks, shTok{kind: shOp, text: ";"},
		shTok{kind: shOp, text: opSubstClose, attachedBefore: attachedBefore, attachedAfter: attachedAfter})
	l.wordOpen = attachedAfter || openedByQuote
	l.wordHasLiteral = false
}

// conditional consumes a `[[ … ]]` conditional expression when the word just
// flushed was "[[" in command position. Inside it, < > ( ) are comparison
// operators of the conditional grammar — `[[ a > b ]]` redirects nothing —
// so its text is inert except for command substitutions, which are live. An
// unterminated conditional swallows the rest of the text, as a syntax error
// would run nothing.
func (l *lexer) conditional() {
	n := len(l.toks)
	if n == 0 || l.toks[n-1].kind != shWord || l.toks[n-1].text != "[[" {
		return
	}
	if n > 1 {
		prev := l.toks[n-2]
		if prev.kind != shOp || !(shBreaks[prev.text] || prev.text == "(" || prev.text == ")") {
			return // an argument named "[[", not the conditional
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

// arithmetic splices the content of an arithmetic context into the token
// stream, bracketed by the arithmetic markers.
func (l *lexer) arithmetic(inner string, attachedBefore, attachedAfter bool) {
	openedByQuote := l.wordOpen && !l.wordHasLiteral
	l.flush()
	l.toks = append(l.toks, shTok{kind: shOp, text: opArithOpen}, shTok{kind: shOp, text: ";"})
	l.toks = append(l.toks, lexShell(inner)...)
	l.toks = append(l.toks, shTok{kind: shOp, text: ";"}, shTok{kind: shOp, text: opArithClose})
	l.wordOpen = attachedAfter || openedByQuote
	l.wordHasLiteral = false
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
		if string(line) == h.delim {
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

// spliceLine lexes the command substitutions of one inert line (a heredoc
// body) into the token stream, between break markers.
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
