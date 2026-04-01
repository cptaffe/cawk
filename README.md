# cawk

`cawk` is an AWK interpreter that takes inspiration from Rob Pike's 1987 paper
[Structural Regular Expressions][sre].  The core idea from that paper: instead
of `/re/ { }` firing once per line *that contains* a match, it fires once per
*match in the stream*, with `$0` set to exactly what was matched.  Same syntax,
different semantics.

Rules are tried in order at the current stream position.  The first to match
consumes its text and moves on.  Bytes that no rule claims are skipped one at a
time.

[sre]: https://doc.cat-v.org/bell_labs/structural_regexps/se.pdf

## Rules

```
BEGIN { }       runs once before input
END   { }       runs once after input
/re/  { }       fires for each anchored match of re in the stream
      { }       bare block — AWK-compatible line scanner
```

The bare block is shorthand for `/[^\n]*\n/` with the trailing newline stripped
from `$0`.  If you write `/[^\n]*\n/ { }` explicitly, the newline is included.

## `$0`, `$1`/`$2`/…, `$name`

`$0` is the matched text.  Parenthesised groups in the pattern populate `$1`,
`$2`, …; named groups `(?P<name>…)` also set `$name`.  These are regex capture
groups — there is no `FS`, `NF`, or field splitting.

```awk
/([0-9]{4})-([0-9]{2})-([0-9]{2})/ { print $1, $2, $3 }
# 2024-01-15  →  2024 01 15

/(?P<y>\d{4})-(?P<m>\d{2})-(?P<d>\d{2})/ { print $d"/"$m"/"$y }
# 2024-01-15  →  15/01/2024
```

Named and positional groups index the same captures, so in
`/(?P<word>[a-z]+)/`, `$1` and `$word` return the same thing.

Assigning to `$0` updates it for the rest of the action:

```awk
{ $0 = toupper($0); print }
```

## Nested `/re/ { }` inside blocks

Inside an action, `/re/ { stmts }` iterates over all non-overlapping matches of
`re` within the current `$0`, pushing a fresh match state for each one:

```awk
{ /[a-z]+/ { print $0 } }
# "hello world"  →  hello / world

{ /([a-z]+)=([0-9]+)/ { print $1, "=", $2 } }
# "x=1 y=2 z=3"  →  x = 1 / y = 2 / z = 3
```

The outer `$0`/`$1`/… are fully restored when the inner block exits.

## Guards

No special syntax — just standard AWK conditionals:

```awk
{ if ($0 ~ /error/)  { print "ERR:", $0 } }
{ if ($0 !~ /^#/)    { print $0 } }
```

## `y/re/ { }` — gaps

`y/re/ { stmts }` iterates over the gaps *between* matches of `re` in `$0`.
Only valid inside a block.

```awk
{ y/,/ { print $0 } }
# "a,b,c"  →  a / b / c
```

This is a cawk extension borrowed from sam's command language; it doesn't appear
in Pike's "AWK of the future" examples but fills a genuine gap (no AWK idiom
exists for gap iteration).

## Examples from the paper

**Rectangle extraction.** Pike opens the paper with an ASCII-art grid and shows
how x-expression rules extract rectangle positions character-run by character-run.
Each rule's `$0` is the matched run; `length($0)` is its width:

```awk
BEGIN { x=1; y=1 }
/ +/  { x += length($0) }
/#+/  { print "rect", x, x+length($0), y, y+1; x += length($0) }
/\n/  { x=1; y++ }
```

**Bibliography search.** From Pike's paper: find titles of Bimmler's papers in a
`refer` database where each entry is a block of `%`-prefixed lines.
`/(.+\n)+/` matches a whole paragraph; the guard tests for the author; the inner
`/.*\n/` then iterates over lines looking for the title field:

```awk
/(.+\n)+/ {
    if ($0 ~ /%A.*Bimmler/) {
        /.*\n/ { if ($0 ~ /^%T/) { print $0 } }
    }
}
```

**Hotkey daemon.** Multi-line patterns compete against a bare-block catch-all.
The chord `/alt x\nz\n/` matches the two lines atomically; if only `alt x\n`
arrives before something else, it falls through to the bare block:

```awk
/alt x\nz\n/ { print "chord: alt-x-z" }
/ctrl c\n/   { exit }
             { print "key:", $0 }
```

Input `alt x\nz\nalt x\nq\nctrl c\n` produces:

```
chord: alt-x-z
key: alt x
key: q
```

## What's kept from AWK, what isn't

Missing: `FS` / field splitting / `NF`, `RS` / `NR` / `FNR`, `next` /
`nextfile`, range patterns, `-F`.  These don't map cleanly onto stream scanning.

Everything else is standard AWK: `BEGIN`/`END`, all control flow, all builtins
(`gsub`, `sub`, `split`, `substr`, `sprintf`, `match`, `length`, `toupper`,
`tolower`, math functions, `getline` from file or pipe), user-defined functions,
arrays, output redirection, `-v`, `-f`.

## Building and testing

```bash
go build -o cawk .
go test ./...
```

The generated parser `awk.go` is committed, so `goyacc` is only needed when
editing the grammar (`awk.go.y`):

```bash
go install golang.org/x/tools/cmd/goyacc@latest
go generate
```

## How it works

The lexer is Rob Pike's [state-function design][lex] with context-sensitive `/`
disambiguation.  The parser is goyacc (LALR(1)); concatenation uses injected
`CONCAT_OP` tokens to avoid reduce/reduce conflicts.

For the stream scanner, all top-level rules are compiled into a single combined
NFA (`regexp/syntax` + a small custom Thompson executor in `internal/nfa`).
Each `InstMatch` instruction carries its rule index in the field the standard
library leaves unused.  One anchored NFA pass per stream position tries all
rules simultaneously in O(rules × input) time with no per-position recompilation.

`$0`/captures live on a push-down stack.  Each `/re/ { }` — top-level or nested
— pushes a frame on entry and pops on exit, so outer state is always restored.

[lex]: https://www.youtube.com/watch?v=HxaD_trXwRE

## References

- [Structural Regular Expressions][sre] — Rob Pike (1987)
- [A Tutorial for the sam Command Language][sam] — Rob Pike
- [The AWK Programming Language][awk] — Aho, Weinberger, Kernighan

[sam]: http://doc.cat-v.org/plan_9/4th_edition/papers/sam/
[awk]: https://ia803404.us.archive.org/0/items/pdfy-MgN0H1joIoDVoIC7/The_AWK_Programming_Language.pdf
