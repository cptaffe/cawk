package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// runCawk runs a cawk program against input in-process and returns stdout.
// Extra args may be -v var=val assignments.
func runCawk(t *testing.T, prog, input string, args ...string) string {
	t.Helper()
	var assigns []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-v" && i+1 < len(args):
			assigns = append(assigns, args[i+1])
			i++
		case strings.HasPrefix(args[i], "-v"):
			assigns = append(assigns, args[i][2:])
		}
	}
	var out strings.Builder
	if _, err := run(prog, strings.NewReader(input), &out, assigns, nil); err != nil {
		t.Fatalf("cawk: %v", err)
	}
	return out.String()
}

func check(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s:\ngot:  %q\nwant: %q", name, got, want)
	}
}

// ─── 5.1  Stream scanner: /re/ is x-expression ───────────────────────────────

// /re/ fires for each match of re in the stream — not per line.
// $0 = exactly the matched text.
func TestXSemantics(t *testing.T) {
	check(t, "x-semantics: each word is its own $0",
		runCawk(t, `/[a-z]+/ { print $0 }`, "hello world\n"),
		"hello\nworld\n")
}

// Bytes that don't match any rule are silently advanced one byte.
func TestXAdvancesStream(t *testing.T) {
	check(t, "digits silently advance",
		runCawk(t, `/[a-z]+/ { print $0 }`, "abc 123 def\n"),
		"abc\ndef\n")
}

// First matching rule wins; later rules never see the same bytes.
func TestFirstRuleWins(t *testing.T) {
	check(t, "first rule wins",
		runCawk(t, `/abc/{print "ABC"} /[a-z]+/{print "word:", $0}`,
			"abc xyz abc\n"),
		"ABC\nword: xyz\nABC\n")
}

// Multi-line pattern matches atomically.
func TestMultiLinePattern(t *testing.T) {
	check(t, "two-line match",
		runCawk(t, `/foo\nbar\n/{print "MATCH"} {print "line:", $0}`,
			"foo\nbar\nbaz\n"),
		"MATCH\nline: baz\n")
}

// When multi-line pattern does not match, lines fall through to bare block.
func TestMultiLinePatternFallthrough(t *testing.T) {
	check(t, "no chord",
		runCawk(t, `/foo\nbar\n/{print "CHORD"} {print "line:", $0}`,
			"foo\nqux\n"),
		"line: foo\nline: qux\n")
}

// Hotkey daemon example: multi-line patterns race against line-based catch-all.
func TestHotkeyDaemonPattern(t *testing.T) {
	input := "alt x\nz\nalt x\nq\nctrl c\n"
	check(t, "hotkey",
		runCawk(t, `/alt x\nz\n/{print "chord:alt-x-z"} /ctrl c\n/{print "quit"} {print "key:"$0}`,
			input),
		"chord:alt-x-z\nkey:alt x\nkey:q\nquit\n")
}

// ─── 5.1 (cont.)  Implicit scanner: bare block ───────────────────────────────

// Bare block { } is the AWK-compat line scanner: $0 = line without trailing \n.
func TestImplicitScanner(t *testing.T) {
	check(t, "bare block line scanner",
		runCawk(t, `{ print ">>", $0 }`, "a\nb\nc\n"),
		">> a\n>> b\n>> c\n")
}

// Bare block gives $0 without the trailing newline.
func TestImplicitScannerNoBareNewline(t *testing.T) {
	check(t, "no trailing newline in $0",
		runCawk(t, `{ print length($0) }`, "hello\n"),
		"5\n")
}

// Explicit /[^\n]*\n/ includes the newline in $0; bare block does not.
func TestImplicitVsExplicit(t *testing.T) {
	check(t, "explicit /[^\\n]*\\n/ includes newline",
		runCawk(t, `/[^\n]*\n/ { print length($0) }`, "hello\n"),
		"6\n")
}

// Bare block and explicit /re/ can coexist; /re/ consumes first.
func TestNoPatternAndRegexCoexist(t *testing.T) {
	check(t, "mixed rules",
		runCawk(t, `/foo\n/{print "FOO"} {print "other:", $0}`, "foo\nbar\nfoo\n"),
		"FOO\nother: bar\nFOO\n")
}

// ─── 5.2  Capture groups ─────────────────────────────────────────────────────

func TestPositionalGroups(t *testing.T) {
	check(t, "date groups",
		runCawk(t, `/([0-9]{4})-([0-9]{2})-([0-9]{2})/{print $1,$2,$3}`,
			"2024-01-15\n"),
		"2024 01 15\n")
}

func TestNamedGroups(t *testing.T) {
	check(t, "named groups",
		runCawk(t, `/(?P<y>\d{4})-(?P<m>\d{2})-(?P<d>\d{2})/{print $d"/"$m"/"$y}`,
			"2024-01-15\n"),
		"15/01/2024\n")
}

func TestNamedAndPositional(t *testing.T) {
	check(t, "named and positional agree",
		runCawk(t, `/(?P<word>[a-z]+)/{print $1, $word}`, "hello\n"),
		"hello hello\n")
}

func TestNoCaptures(t *testing.T) {
	// No capture groups → $1 is "" (empty string); print OFS-joins args.
	// print "hi", "|", "" = "hi" + OFS + "|" + OFS + "" = "hi | "
	check(t, "no captures $1 empty",
		runCawk(t, `/[a-z]+/{print $0, "|", $1}`, "hi\n"),
		"hi | \n")
}

func TestCaptureGroupsMultiple(t *testing.T) {
	check(t, "multiple key=val matches",
		runCawk(t, `/([a-z]+)=([0-9]+)/{print $1,"is",$2}`,
			"x=1 y=2 z=3\n"),
		"x is 1\ny is 2\nz is 3\n")
}

// ─── 5.3  Guards: if ($0 ~ /re/) ─────────────────────────────────────────────

func TestGuard(t *testing.T) {
	check(t, "guard: match",
		runCawk(t, `{ if ($0 ~ /error/) { print "ERR:", $0 } }`,
			"ok\nerror here\nfine\n"),
		"ERR: error here\n")
}

func TestAntiGuard(t *testing.T) {
	check(t, "anti-guard: no match",
		runCawk(t, `{ if ($0 !~ /error/) { print $0 } }`,
			"ok\nerror\nfine\n"),
		"ok\nfine\n")
}

func TestGuardWithX(t *testing.T) {
	// Guard inside bare block (bare block gives $0 without trailing newline).
	check(t, "guard in bare block",
		runCawk(t, `{ if ($0 ~ /foo/) { print "has foo:", $0 } }`,
			"bar\nfoo here\nbaz\n"),
		"has foo: foo here\n")
}

// ─── 5.4  Nested /re/ {} inside blocks ───────────────────────────────────────

// Inner /re/ { } iterates over all matches of re within the current $0.
func TestNestedX(t *testing.T) {
	check(t, "nested x: words per line",
		runCawk(t, `{ /[a-z]+/ { print $0 } }`, "hello world\nbaz\n"),
		"hello\nworld\nbaz\n")
}

// Nested /re/ with capture groups.
func TestNestedXCaptures(t *testing.T) {
	check(t, "nested x: key=val captures",
		runCawk(t, `{ /([a-z]+)=([0-9]+)/ { print $1, "→", $2 } }`,
			"x=1 y=2 z=3\n"),
		"x → 1\ny → 2\nz → 3\n")
}

// Nested x-expressions can combine outer and inner scanners.
func TestNestedXY(t *testing.T) {
	check(t, "nested /re/ inside /re/",
		runCawk(t, `/[^\n]*\n/ { /[^ \n]+/ { print "W:", $0 } }`,
			"one two three\n"),
		"W: one\nW: two\nW: three\n")
}

// ─── 5.5  Match state save/restore ───────────────────────────────────────────

// After a nested /re/ { } block, the outer $0/$1/... are fully restored.
func TestMatchStateRestore(t *testing.T) {
	check(t, "match state restored after inner loop",
		runCawk(t, `/([^\n]+)\n/ { /([a-z]+)/ { } ; print $1 }`,
			"hello world\n"),
		"hello world\n")
}

// Outer $0 is unchanged after inner loop iterates and modifies inner $0.
// Note: /([^\n]+)\n/ captures the newline into $0, so "print "outer:", $0"
// produces a double-newline (the newline in $0 plus ORS).
func TestNestedDollarZero(t *testing.T) {
	// Use a bare block so $0 is the line without trailing newline — cleaner output.
	check(t, "outer $0 unchanged by inner loop",
		runCawk(t, `{ /[a-z]+/ { print "inner:", $0 }; print "outer:", $0 }`,
			"hello world\n"),
		"inner: hello\ninner: world\nouter: hello world\n")
}

// $0 assignment persists within the same action block.
func TestDollarZeroAssign(t *testing.T) {
	check(t, "$0 assignment",
		runCawk(t, `{ $0 = toupper($0); print }`, "hello\n"),
		"HELLO\n")
}

// ─── 5.6  y// extension (gap iteration) ──────────────────────────────────────

func TestY(t *testing.T) {
	// Gaps between digit runs in "a1b2c" = "a", "b", "c".
	check(t, "y// gaps",
		runCawk(t, `{ y/[0-9]+/ { print $0 } }`, "a1b2c\n"),
		"a\nb\nc\n")
}

// y// always includes the trailing gap, even if empty.
func TestYTrailingGap(t *testing.T) {
	// "abc123def456" has an empty trailing gap after "456".
	check(t, "y// empty trailing gap",
		runCawk(t, `BEGIN{x="abc123def456"} { y/[0-9]+/ { print "|"$0"|" } }`,
			"abc123def456\n"),
		"|abc|\n|def|\n||\n")
}

func TestYVsX(t *testing.T) {
	// Split on commas: gaps are the fields.
	check(t, "y// split on comma",
		runCawk(t, `{ y/,/ { print $0 } }`, "a,b,c\n"),
		"a\nb\nc\n")
}

// break works inside y//.
func TestYBreak(t *testing.T) {
	check(t, "y break",
		runCawk(t, `{ y/[0-9]+/ { if ($0 == "b") break; print $0 } }`, "a1b2c\n"),
		"a\n")
}

// ─── 5.7  AWK compatibility ───────────────────────────────────────────────────

func TestBeginEnd(t *testing.T) {
	check(t, "BEGIN END",
		runCawk(t, `BEGIN{print "start"} /[^\n]*\n/{} END{print "end"}`, "x\n"),
		"start\nend\n")
}

func TestBeginOnly(t *testing.T) {
	check(t, "BEGIN only",
		runCawk(t, `BEGIN{print "hello"}`, ""),
		"hello\n")
}

func TestCountLines(t *testing.T) {
	check(t, "count lines",
		runCawk(t, `BEGIN{n=0} {n++} END{print n}`, "a\nb\nc\n"),
		"3\n")
}

func TestEndSums(t *testing.T) {
	check(t, "sum numbers",
		runCawk(t, `BEGIN{s=0} /[0-9]+/{s+=$0} END{print s}`, "1\n2\n3\n"),
		"6\n")
}

func TestArrays(t *testing.T) {
	check(t, "array freq",
		runCawk(t, `/([a-z]+)/{a[$1]++} END{print a["a"],a["b"],a["c"]}`, "a b a c b a\n"),
		"3 2 1\n")
}

func TestUserFunction(t *testing.T) {
	check(t, "user function",
		runCawk(t, `function double(x){return x*2} /[0-9]+/{print double($0)}`,
			"3\n7\n"),
		"6\n14\n")
}

func TestGsub(t *testing.T) {
	check(t, "gsub",
		runCawk(t, `{ gsub(/o/, "0"); print $0 }`, "foo bar boo\n"),
		"f00 bar b00\n")
}

func TestGsubOnDollarZero(t *testing.T) {
	// gsub targeting $0 (via match stack) then print should reflect the change.
	check(t, "gsub $0 then print",
		runCawk(t, `{ gsub(/l/, "L"); print }`, "hello\n"),
		"heLLo\n")
}

func TestSplit(t *testing.T) {
	check(t, "split",
		runCawk(t, `BEGIN{n=split("a:b:c",a,":"); for(i=1;i<=n;i++) print a[i]}`, ""),
		"a\nb\nc\n")
}

func TestGetlineFile(t *testing.T) {
	f, _ := os.CreateTemp("", "cawk-test-*.txt")
	f.WriteString("secret\n")
	f.Close()
	defer os.Remove(f.Name())

	check(t, "getline file",
		runCawk(t, `BEGIN { getline line < "`+f.Name()+`"; print line }`, ""),
		"secret\n")
}

func TestGetlineFromPipe(t *testing.T) {
	check(t, "getline pipe",
		runCawk(t, `BEGIN { "echo hello" | getline x; print x }`, ""),
		"hello\n")
}

// ─── 5.7 (cont.)  exit ───────────────────────────────────────────────────────

func TestExit(t *testing.T) {
	// /stop\n/ fires and calls exit; no-pattern rule has already handled "a" and "b".
	check(t, "exit",
		runCawk(t, `/stop\n/{exit} {print $0}`, "a\nb\nstop\nc\n"),
		"a\nb\n")
}

func TestExitRunsEnd(t *testing.T) {
	check(t, "exit runs END",
		runCawk(t, `/stop\n/{exit} END{print "done"}`, "a\nstop\nb\n"),
		"done\n")
}

// ─── 5.7 (cont.)  Arithmetic, strings, control flow ─────────────────────────

func TestArithmetic(t *testing.T) {
	check(t, "arithmetic",
		runCawk(t, `BEGIN{print 2+3, 10/4, 2^8}`, ""),
		"5 2.5 256\n")
}

func TestStringConcat(t *testing.T) {
	check(t, "concat",
		runCawk(t, `BEGIN{a="hello"; b="world"; print a " " b}`, ""),
		"hello world\n")
}

func TestSubstr(t *testing.T) {
	check(t, "substr",
		runCawk(t, `BEGIN{print substr("hello world", 7)}`, ""),
		"world\n")
}

func TestMatch(t *testing.T) {
	check(t, "match builtin",
		runCawk(t, `{ if (match($0, /[0-9]+/)) print "found at", RSTART }`,
			"abc123def\n"),
		"found at 4\n")
}

func TestPrintf(t *testing.T) {
	check(t, "printf",
		runCawk(t, `BEGIN{printf "%05d\n", 42}`, ""),
		"00042\n")
}

func TestOFS(t *testing.T) {
	check(t, "OFS",
		runCawk(t, `BEGIN{OFS="-"; print "a","b","c"}`, ""),
		"a-b-c\n")
}

func TestTildeOperator(t *testing.T) {
	check(t, "~",
		runCawk(t, `{ if ($0 ~ /foo/) { print "yes" } else { print "no" } }`,
			"foobar\nbaz\n"),
		"yes\nno\n")
}

func TestNomatchOperator(t *testing.T) {
	check(t, "!~",
		runCawk(t, `{ if ($0 !~ /foo/) print "no foo" }`, "bar\nfoo\nqux\n"),
		"no foo\nno foo\n")
}

func TestBreakContinue(t *testing.T) {
	check(t, "break continue",
		runCawk(t, `BEGIN{for(i=1;i<=5;i++){if(i==3)continue; if(i==5)break; print i}}`, ""),
		"1\n2\n4\n")
}

func TestWhileLoop(t *testing.T) {
	check(t, "while",
		runCawk(t, `BEGIN{i=1; while(i<=3){print i; i++}}`, ""),
		"1\n2\n3\n")
}

func TestDoWhile(t *testing.T) {
	check(t, "do while",
		runCawk(t, `BEGIN{i=0; do{i++; print i}while(i<3)}`, ""),
		"1\n2\n3\n")
}

func TestForIn(t *testing.T) {
	check(t, "for-in sum",
		runCawk(t, `BEGIN{a["x"]=1; a["y"]=2; for(k in a) s+=a[k]; print s}`, ""),
		"3\n")
}

func TestForInDelete(t *testing.T) {
	check(t, "delete array element",
		runCawk(t, `BEGIN{a[1]=1;a[2]=2;a[3]=3; delete a[2]; print length(a)}`, ""),
		"2\n")
}

// ─── 5.7 (cont.)  Builtins ───────────────────────────────────────────────────

func TestLength(t *testing.T) {
	check(t, "length($0)",
		runCawk(t, `/[a-z]+/ { print length($0) }`, "hello\n"),
		"5\n")
}

func TestTolower(t *testing.T) {
	check(t, "tolower",
		runCawk(t, `{ print tolower($0) }`, "Hello World\n"),
		"hello world\n")
}

func TestToupper(t *testing.T) {
	check(t, "toupper",
		runCawk(t, `{ print toupper($0) }`, "hello\n"),
		"HELLO\n")
}

func TestIndex(t *testing.T) {
	check(t, "index",
		runCawk(t, `BEGIN{print index("hello world", "world")}`, ""),
		"7\n")
}

func TestSprintfBuiltin(t *testing.T) {
	check(t, "sprintf",
		runCawk(t, `BEGIN{s=sprintf("%.2f", 3.14159); print s}`, ""),
		"3.14\n")
}

// ─── 5.7 (cont.)  Output, -v flag ────────────────────────────────────────────

func TestOutputRedirect(t *testing.T) {
	f, _ := os.CreateTemp("", "cawk-out-*.txt")
	name := f.Name()
	f.Close()
	os.Remove(name)
	defer os.Remove(name)

	runCawk(t, `{print $0 > "`+name+`"}`, "hello\nworld\n")
	data, _ := os.ReadFile(name)
	check(t, "redirect >", string(data), "hello\nworld\n")
}

func TestVFlag(t *testing.T) {
	check(t, "-v flag",
		runCawk(t, `BEGIN{print x}`, "", "-v", "x=42"),
		"42\n")
}

// ─── 5.8  Sparse advance ──────────────────────────────────────────────────────

// Bytes not matched by any rule are silently advanced one at a time.
func TestSparseAdvance(t *testing.T) {
	// Uppercase runs: H (from Hello), WORLD, BAR — lowercase/spaces advanced.
	check(t, "sparse advance",
		runCawk(t, `/[A-Z]+/{print $0}`, "Hello WORLD foo BAR\n"),
		"H\nWORLD\nBAR\n")
}

// ─── 5.9  Edge cases ──────────────────────────────────────────────────────────

func TestEmptyInput(t *testing.T) {
	check(t, "empty input",
		runCawk(t, `BEGIN{print "hi"} {print $0} END{print "bye"}`, ""),
		"hi\nbye\n")
}

func TestEOFNoNewline(t *testing.T) {
	check(t, "EOF without newline",
		runCawk(t, `{ print $0 }`, "hello"),
		"hello\n")
}

// break/continue inside nested /re/ { } are scoped to the inner loop.
func TestInnerRegexBreak(t *testing.T) {
	check(t, "break inside /re/ {}",
		runCawk(t, `{ /[0-9]+/ { if($0=="2")break; print $0 }; print "done" }`,
			"1 2 3\n"),
		"1\ndone\n")
}

func TestInnerRegexContinue(t *testing.T) {
	check(t, "continue inside /re/ {}",
		runCawk(t, `{ /[0-9]+/ { if($0=="2")continue; print $0 } }`, "1 2 3\n"),
		"1\n3\n")
}

// ─── 5.10  Variables, accumulation ───────────────────────────────────────────

func TestVariables(t *testing.T) {
	check(t, "accumulate",
		runCawk(t, `BEGIN{x=0} /[0-9]+/ { x+=$0 } END{print x}`, "10\n20\n30\n"),
		"60\n")
}

// Condition inside bare block (replaces AWK expression pattern).
func TestConditionInBlock(t *testing.T) {
	check(t, "if guard in block",
		runCawk(t, `{ if ($0 ~ /^a/) { print "a-word:", $0 } }`,
			"apple\nbanana\napricot\n"),
		"a-word: apple\na-word: apricot\n")
}

func TestCounterInBlock(t *testing.T) {
	check(t, "line counter guard",
		runCawk(t, `BEGIN{n=0} {n++; if(n==2){print "second:", $0}}`, "x\ny\nz\n"),
		"second: y\n")
}

// ─── Regression: lookahead latency — /re\n/ must not wait for the next line ──
//
// The lookahead formula used to be strings.Count(regex, `\n`) + 1, which
// meant a pattern like /command s\n/ required TWO newlines in the buffer
// before the match was attempted.  On a live pipe (stdin open, events
// arriving one at a time) this caused one event of latency: the rule would
// not fire until the NEXT event arrived to satisfy ensureLines(2).
//
// Fix: if the regex ends with \n, no +1 is needed — the match is complete
// at the final newline and we do not need to peek at the following line.

// TestNewlinePatternNoLookaheadLatency verifies that /re\n/ fires as soon as
// the matching line arrives on a live pipe, WITHOUT requiring a second line.
// It writes one event to cawk's stdin, reads the output that must appear
// immediately, then closes the pipe — if the rule needed a second line first,
// Wait() would time out (or no output would arrive before close).
func TestNewlinePatternNoLookaheadLatency(t *testing.T) {
	// Verify that /re\n/ fires as soon as the matching line arrives, without
	// requiring a second line.  Use an io.Pipe so the scanner sees a live
	// reader (not a bytes.Reader already at EOF).
	pr, pw := io.Pipe()
	var out strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		run(`/command s\n/ { printf "got:%s", $0 } { print "other:", $0 }`,
			pr, &out, nil, nil)
	}()
	io.WriteString(pw, "command s\n")
	pw.Close()
	<-done
	check(t, "newline-pattern fires on single event (no latency)",
		out.String(), "got:command s\n")
}

// ─── Regression: bug.md — top-level /re/ must match literal \n ───────────────
//
// Old cawk treated top-level /re/ as an expression pattern evaluated against
// $0 (line without trailing newline), so /hello\n/ never matched.  New cawk
// uses x-expression semantics: the pattern is anchored against the raw stream,
// so /hello\n/ correctly consumes "hello\n" as a single token.

// Exact reproduction from bug.md: /hello\n/ fires; /^hello$/ does not.
// In old cawk: only "matched without newline" printed (and twice, due to
// expression-pattern line-based evaluation matching both "hello" and "world").
// In new cawk: "matched with newline" printed once; /^hello$/ never fires
// ($ is end-of-stream in x-expression mode, not end-of-line).
func TestBugNewlineInTopLevelPattern(t *testing.T) {
	check(t, "bug: /hello\\n/ matches (x-expression)",
		runCawk(t, `/hello\n/ { print "matched with newline" } /^hello$/ { print "matched without newline" }`,
			"hello\nworld\n"),
		"matched with newline\n")
}

// $0 includes the newline when /re\n/ is the matching pattern.
func TestBugDollarZeroIncludesNewline(t *testing.T) {
	check(t, "bug: $0 includes matched newline",
		runCawk(t, `/hello\n/ { printf "|%s|", $0 }`, "hello\n"),
		"|hello\n|")
}

// The hotkeys idiom: /event\n/ consumes the line; printf "!%s",$0 re-emits
// with a leading "!" and no double-newline (since $0 already contains \n).
// Unmatched lines fall to the bare-block catch-all, which strips the newline
// and re-adds it via ORS — producing clean output.
func TestBugEventDispatchPattern(t *testing.T) {
	check(t, "bug: hotkeys event dispatch with \\n pattern",
		runCawk(t, `/command s\n/ { printf "!%s", $0 } { print $0 }`,
			"command s\nrepeat command s\nshift a\n"),
		"!command s\nrepeat command s\nshift a\n")
}

// Multi-line x-expression: /foo\nbar\n/ matches two consecutive lines atomically.
func TestBugMultilineXExpr(t *testing.T) {
	check(t, "bug: multi-line pattern spans record boundary",
		runCawk(t, `/foo\nbar\n/ { print "chord" } { print $0 }`,
			"foo\nbar\nbaz\n"),
		"chord\nbaz\n")
}
