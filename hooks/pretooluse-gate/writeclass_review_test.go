package main

// writeclass_review_test.go — the adversarial tables of the 0.7.0 ship
// review (classifier lens), kept as regression pins. Every write here was
// judged READ by the first classifier, most with the side effect executed
// in bash to prove it; every read here was denied under the diagnosis
// freeze, the incident's own failure class.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// mustBeWrite / mustBeRead judge one input through the guarded classifier.
func mustBeWrite(t *testing.T, cmd string, rulePart string) {
	t.Helper()
	rule, ok := classifyGuarded(cmd)
	switch {
	case !ok:
		t.Errorf("WRITE %q: the classifier hung or panicked", cmd)
	case rule == "":
		t.Errorf("WRITE judged READ: %q", cmd)
	case rulePart != "" && !strings.Contains(rule, rulePart):
		t.Errorf("WRITE %q: rule %q does not name %q", cmd, rule, rulePart)
	}
}

func mustBeRead(t *testing.T, cmd string) {
	t.Helper()
	rule, ok := classifyGuarded(cmd)
	switch {
	case !ok:
		t.Errorf("READ %q: the classifier hung or panicked", cmd)
	case rule != "":
		t.Errorf("READ judged WRITE by %q: %q", rule, cmd)
	}
}

// TestReviewCommandWordBuiltAtRuntime (Critical): a command name that bash
// computes — from a quoted substitution, a backtick, a variable, or part
// of a word — is unknown until it runs. Each spelling below executed its
// effect under the diagnosis freeze.
func TestReviewCommandWordBuiltAtRuntime(t *testing.T) {
	for _, cmd := range []string{
		`"$(echo rm)" -rf x`,
		`"` + "`echo rm`" + `" -rf x`,
		`"$(echo r)m" x`,
		`r"$(echo m)" x`,
		`X=touch; "$X" q`,
		`"$MAKE" test`,
		`$GIT push`,
		`"$PYTHON" -c 'open("f","w")'`,
		`"$(echo git)" push`,
		`"$(echo sh)" -c 'rm x'`,
		`$'\x72\x6d' -rf x`,
	} {
		mustBeWrite(t, cmd, "command")
	}
	// A substitution or variable in ARGUMENT position is ordinary text.
	for _, cmd := range []string{
		`git -C "$REPO" status`, `ls "$HOME"`, `echo "$PATH"`, `grep -n "$PAT" file`,
		`cat "$(git rev-parse --show-toplevel)/README.md"`, `cat "$(echo hi)"`, `echo "$((1 + 2))"`, `echo "$(( $(wc -l < f) ))"`,
	} {
		mustBeRead(t, cmd)
	}
}

// TestReviewGroupingForms: case arms, function definitions, process
// substitution and coproc are command boundaries or command-bearing forms.
func TestReviewGroupingForms(t *testing.T) {
	for _, cmd := range []string{
		`case "" in *) rm -rf x;; esac`,
		`for f in *.go; do case $f in *.tmp) rm $f;; esac; done`,
		`cat <(rm -rf x)`,
		`diff <(sort a) <(rm -rf b)`,
		`wc -l <(rm x)`,
		`while read -r l; do echo "$l"; done < <(rm x)`,
		`f() { rm x; }; f`,
		`function f { rm x; }; f`,
		`coproc rm -rf x`,
		`echo hi |& tee log`,
	} {
		mustBeWrite(t, cmd, "")
	}
	for _, cmd := range []string{
		`diff <(sort a) <(sort b)`,
		`while read -r l; do echo "$l"; done < <(cat f)`,
		`case "$x" in a) echo one;; b) echo two;; esac`,
	} {
		mustBeRead(t, cmd)
	}
}

// TestReviewCategorySpellings: D5.5 names CATEGORIES (build and run,
// downloads, unpacking, inline code) — the spellings of each category's
// tools must all be covered, not one example per tool.
func TestReviewCategorySpellings(t *testing.T) {
	writes := map[string]string{
		// build and run — Go
		"go build": "go build", "go build .": "go build", "go install": "go install", "go get -u x": "go get",
		"go mod tidy": "go mod", "go mod download": "go mod", "go mod vendor": "go mod", "go mod edit -require=x": "go mod",
		"go generate ./...": "go generate", "go fmt ./...": "go fmt", "go env -w GOFLAGS=-mod=vendor": "go env",
		"gofmt -w .": "gofmt", "goimports -w .": "goimports",
		// build and run — JS
		"npm run build": "npm run", "npm test": "npm test", "npm exec -- sh -c 'rm x'": "npm exec", "npm x -- tsc": "npm x",
		"pnpm install": "pnpm", "pnpm add x": "pnpm", "pnpm dlx x": "pnpm", "pnpm exec x": "pnpm", "pnpm run build": "pnpm",
		"yarn install": "yarn", "yarn add x": "yarn", "yarn dlx x": "yarn", "yarn run build": "yarn",
		"bun install": "bun", "bun run x.ts": "bun", "bunx tsc": "bunx",
		"deno run x.ts": "deno", "deno task build": "deno",
		// build and run — Python
		"pip3.9 install x": "pip", "python3 -m pip install x": "python", "pip download x": "pip",
		"pipx install x": "pipx", "poetry install": "poetry", "uv pip install x": "uv",
		"python3 -ic 'open(\"f\",\"w\")'": "python", "python3 -Oc 'print(1)'": "python", "python3 -Bc 'print(1)'": "python",
		`node -pe "require('fs').writeFileSync('f','')"`: "node",
		"python3 -m compileall .":                        "python", "python3 -m venv .venv": "python", "python3 -m http.server": "python",
		// downloads
		"curl -sSo out.json https://example.com": "curl", "curl -sfLo out https://example.com": "curl",
		"curl -sSOJ https://example.com": "curl", "curl -JO https://example.com": "curl",
		"curl -D hdr.txt https://example.com": "curl", "curl -c cookies.txt https://example.com": "curl",
		"curl --trace-ascii log.txt https://example.com": "curl", "curl --etag-save etag.txt https://example.com": "curl",
		"curl -K curl.conf https://example.com": "curl", "curl -T file https://example.com": "curl",
		// unpack / repack
		"tar -czf x.tgz .": "tar", "tar -cf x.tar dir": "tar", "tar -rf x.tar f": "tar", "tar -uf x.tar dir": "tar",
		"tar -Af a.tar b.tar": "tar", "tar --delete -f x.tar f": "tar", "tar --create -f x.tar dir": "tar",
		// awk / sed code
		`awk 'BEGIN{system ("rm -rf x")}'`: "awk", `awk 'BEGIN{print "touch x" | "sh"}'`: "awk",
		"awk -E prog.awk f": "awk", "awk --exec prog.awk f": "awk", "awk -i lib.awk f": "awk", "awk -l lib f": "awk",
		"gawk -f p.awk f": "gawk", "mawk -f p.awk f": "mawk",
		`sed '/x/w out.txt' f`: "sed", `sed 's/a/b/w out.txt' f`: "sed",
		// wrappers that run a command
		"setsid rm x": "setsid", "arch -x86_64 rm x": "arch", "caffeinate -i make test": "caffeinate",
		"parallel rm {} ::: a b": "parallel", "stdbuf -oL make": "stdbuf", "script -q /dev/null make": "script",
		"xcrun make": "xcrun", "flock lockfile make": "flock", "taskset -c 0 make": "taskset", "ionice -c3 make": "ionice",
		"source x.sh": "source", ". x.sh": "source",
	}
	for cmd, rule := range writes {
		mustBeWrite(t, cmd, rule)
	}
	// Reads in the same families stay reads.
	for _, cmd := range []string{
		"go test ./...", "go vet ./...", "go list ./...", "go mod why x", "go mod graph", "go env", "gofmt -l .",
		"npm ls", "npm run", "pnpm ls", "yarn list", "bun --version", "pip list", "pip show x", "pip3 --version",
		"python3 -m json.tool f.json", "python3 -m json f.json",
		"curl -s https://example.com", "curl -sS -f -o /dev/null https://example.com", "curl -sSo - https://example.com",
		"curl -sLo - https://example.com", "curl -sSo/dev/null https://example.com",
		"curl -D - https://example.com", "curl -sSLo - https://example.com",
		"tar -tf x.tar", "tar -tvf x.tar", "tar -tzf x.tgz",
		"awk -F: '{print $1}' /etc/passwd", "awk 'NR>=1213 && /^func /' hooks/gate.go", "awk '{getline < \"f\"; print}'",
		"sed -n '1,5p' f", "sed -e 's/a/b/' f",
		"diff <(sort a) <(sort b)",
	} {
		mustBeRead(t, cmd)
	}
}

// TestReviewReadonlyDiagnosisCommands: read-only commands a model really
// runs while diagnosing — several denied by the first classifier with a
// false reason.
func TestReviewReadonlyDiagnosisCommands(t *testing.T) {
	for _, cmd := range []string{
		// [[ ]] behind a keyword
		`if [[ $a > $b ]]; then echo big; fi`,
		`while [[ $i > 0 ]]; do i=$((i-1)); done`,
		`if [[ a == b ]]; then echo same; elif [[ a > b ]]; then echo big; fi`,
		`! [[ a > b ]]`,
		`{ [[ a > b ]]; }`,
		`if [[ -f x && $n -gt 3 ]]; then echo ok; fi`,
		// arithmetic
		`echo $(( $(wc -l < f) > 10 ))`, `echo $((3 > 2))`, `(( n > 0 )) && echo yes`,
		// git read forms
		`git --version`, `git --help`, `git -h`, `git -p log`, `git -P log --oneline`,
		`git --no-optional-locks status`, `git --literal-pathspecs ls-files`, `git --exec-path`,
		`git config user.name`, `git config --get remote.origin.url`, `git config --list`,
		`git symbolic-ref --short HEAD`, `git symbolic-ref -q HEAD`,
		`git reflog -5`, `git reflog HEAD`, `git reflog show main`, `git reflog --date=iso`,
		`git branch -avv`, `git branch -vv`, `git branch --show-current`, `git branch -r`, `git tag -n`, `git tag --sort=-creatordate`,
		`git stash list`, `git stash show -p`, `git worktree list`, `git submodule status`,
		`git notes show`, `git bundle verify x.bundle`, `git show-ref`, `git show-ref --heads`,
		`git diff-tree -r HEAD`, `git diff-index HEAD`, `git show-branch`, `git range-diff a..b c..d`,
		`git apply --check p.diff`, `git apply --stat p.diff`, `git format-patch -1 --stdout`,
		`git bisect log`, `git bisect status`, `git lfs ls-files`, `git lfs status`,
		`git log --oneline | head -20`, `git diff --stat HEAD~1`, `git blame f.go`, `git describe --tags`,
	} {
		mustBeRead(t, cmd)
	}
	// …and the write forms beside them stay writes.
	for _, cmd := range []string{
		`git reflog expire --all`, `git reflog delete HEAD@{0}`, `git branch -d x`, `git branch -D x`,
		`git tag -a v1`, `git tag -d v1`, `git config --unset user.name`, `git config user.name x`,
		`git stash`, `git stash drop`, `git worktree add ../x`, `git submodule update --init`,
		`git notes add -m x`, `git bundle create x.bundle main`, `git apply p.diff`, `git bisect reset`,
		`git -c alias.dag='!rm -rf x' dag`, `git push`, `git commit -m x`,
	} {
		mustBeWrite(t, cmd, "")
	}
}

// TestReviewLexerEdges: characters after a conditional, CRLF heredocs,
// exact targets and delimiters.
func TestReviewLexerEdges(t *testing.T) {
	for _, cmd := range []string{
		"[[ 1 -eq 1 ]]>f", "[[ 1 -eq 1 ]]>|f", "[[ 1 -eq 1 ]]>>f",
		"cat <<'EOF'\r\na > b\r\nEOF\r\nrm -rf x \r\n",
		"cat <<-EOF\n\t$(rm x)\n\tEOF",
		"cat <<EOF\nEOFX\n$(rm x)\nEOF",
		"cat <<EOF\n$(rm x)\nEOF",
		"echo `rm x`",
		"env -S 'rm x'", "env -",
		"echo [[ a > b ]]",
		"cmd > /DEV/NULL", "cmd 2> /dev/nullx", "curl -sSLO https://example.com", "tar -cf x.tar dir",
	} {
		mustBeWrite(t, cmd, "")
	}
	for _, cmd := range []string{
		"[[ 1 -eq 1 ]] && echo yes", "[[ 1 -eq 1 ]]; echo yes", "[[ 1 -eq 1 ]]\necho yes",
		"echo ${x:?err > file}",
		// -O extracts to STDOUT: nothing is written (ship review round 2 —
		// this row was pinned as a write, which was the false denial).
		"tar -xOf x.tar f",
	} {
		mustBeRead(t, cmd)
	}
}

// TestReviewInlineCodeFlags: the inline-code flags of node and python, and
// the write forms of git reflog / branch / tag / config — guards the
// review found unpinned.
func TestReviewInlineCodeFlags(t *testing.T) {
	for _, cmd := range []string{
		`node -p "1+1"`, `node --print "1+1"`, `node -`, `node -e "1"`,
		"git branch -d x", "git tag -a v1", "git config --unset k", "git config k v",
	} {
		mustBeWrite(t, cmd, "")
	}
	for _, cmd := range []string{`node --version`, `node -v`, `node script.js`, `node -c script.js`} {
		mustBeRead(t, cmd)
	}
}

// TestReviewLexerIsLinearInInputSize: the gate has a 10-second budget and
// the classifier runs on every Bash call. A 37 KB operator-dense command
// took 1.4 s and a 560 KB one never finished in time.
func TestReviewLexerIsLinearInInputSize(t *testing.T) {
	big := strings.Repeat("x 2>/dev/null; ", 20000) // ~300 KB
	start := time.Now()
	rule, ok := classifyGuarded(big)
	elapsed := time.Since(start)
	if !ok {
		t.Fatalf("a 300 KB command made the classifier hang or panic")
	}
	if rule != "" {
		t.Fatalf("a 300 KB read-only command is judged a write by %q", rule)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("classifying a 300 KB command took %s — the gate's budget is 10 s", elapsed)
	}
	t.Logf("300 KB command classified in %s", elapsed)
}

var _ = fmt.Sprintf

// TestReviewLexerStopMarkerIsAWrite pins the classifier's answer to the
// lexer's stop marker directly on the token stream (the marker is
// unreachable by construction, so only a direct token test can pin it).
func TestReviewLexerStopMarkerIsAWrite(t *testing.T) {
	stop := []shTok{{kind: shOp, text: opLexStop}, {kind: shWord, text: "ls"}}
	if rule := ruleFor(stop); !strings.Contains(rule, "lexer stopped early") {
		t.Fatalf("a lexer stop must be a write verdict, got %q", rule)
	}
	plain := []shTok{{kind: shWord, text: "ls"}, {kind: shWord, text: "-la"}}
	if rule := ruleFor(plain); rule != "" {
		t.Fatalf("a plain read-only token stream must stay read, got %q", rule)
	}
	// The progress guard itself: a lexer loop that cannot advance stops
	// instead of hanging, and says so.
	stopAtStart := lexShell("\n")
	_ = stopAtStart
	if got := writeClassRule("ls\nls"); got != "" {
		t.Fatalf("multi-line read-only command judged %q", got)
	}
}

// TestReviewLiteralBasenameIsAProgramName: an expanded command word whose
// last path component is literal names a program — the documented status
// viewer spelling depends on it — and that program is judged like any verb.
func TestReviewLiteralBasenameIsAProgramName(t *testing.T) {
	for _, cmd := range []string{
		`"${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-status" <<< '{}'`,
		`"$ROOT/bin/tool" status`,
	} {
		mustBeRead(t, cmd)
	}
	for _, cmd := range []string{`"$BIN/rm" -rf x`, `"$BIN/git" push`, `"${DIR}/make" test`} {
		mustBeWrite(t, cmd, "")
	}
}
