# cawk — AWK with x-expression semantics

`cawk` is Rob Pike's "AWK of the future" from his 1987 paper
[Structural Regular Expressions][sre]: same AWK syntax, one semantic
change — top-level `/re/ { }` rules are **x-expressions** that fire on
each match in the raw byte stream, not on lines.

[sre]: https://doc.cat-v.org/bell_labs/structural_regexps/se.pdf

---

## The one semantic change

| | `/re/ { action }` | `$0` |
|---|---|---|
| **AWK** | fires for each input **line** that *contains* `re` | the full line |
| **cawk** | fires each time `re` matches **anchored at the current stream position** | exactly the matched text |

The syntax is identical — this is precisely the change Pike proposes.

Rules are tried in source order.  The first to match at the current
position wins and consumes its matched text.  Unmatched bytes advance
one at a time (Pike's "silent skip").

---

## Rule forms

```
BEGIN { action }      once before input
END   { action }      once after input
/re/  { action }      x-expression: fires for each anchored match in the stream
      { action }      bare block: AWK-compat line scanner
```

The bare block is shorthand for `/[^\n]*\n/` with the trailing `\n`
stripped from `$0`.  An explicit `/[^\n]*\n/ { }` rule includes the
newline in `$0`.

---

## Match state: `$0`, `$1`/`$2`/…, `$name`

`$0` is the matched text.  Capture groups populate `$1`, `$2`, …;
named groups `(?P<name>…)` additionally populate `$name`.  These are
**capture groups, not FS-split fields** — there is no `FS`, `NF`, or `NR`.

```awk
# positional groups
/([0-9]{4})-([0-9]{2})-([0-9]{2})/ { print $1, $2, $3 }
```
`2024-01-15` → `2024 01 15`

```awk
# named groups — $name syntax
/(?P<y>\d{4})-(?P<m>\d{2})-(?P<d>\d{2})/ { print $d"/"$m"/"$y }
```
`2024-01-15` → `15/01/2024`

Named and positional groups index the same captures: in
`/(?P<word>[a-z]+)/`, `$1` and `$word` return the same string.

Assigning to `$0` replaces the match text for the rest of the action:

```awk
{ $0 = toupper($0); print }
```

---

## Guards: `if ($0 ~ /re/)`

There are no `g//` or `v//` keywords.  Conditions use standard AWK:

```awk
{ if ($0 ~ /error/)  { print "ERR:", $0 } }   # fire if line contains "error"
{ if ($0 !~ /^#/)    { print $0 } }           # skip comment lines
```

`~` and `!~` test `$0` without changing it.

---

## Nested `/re/ { }` inside blocks

Inside an action block, `/re/ { stmts }` iterates over all
non-overlapping matches of `re` within the current `$0`:

```awk
{ /[a-z]+/ { print $0 } }
```
`hello world` → `hello` then `world`

Each iteration pushes a fresh match state — its own `$0`, captures,
named groups — and pops it on exit.  The outer `$0`/`$1`/… are fully
restored afterwards.

```awk
{ /([a-z]+)=([0-9]+)/ { print $1, "=", $2 } }
```
`x=1 y=2 z=3` → `x = 1` / `y = 2` / `z = 3`

---

## `y/re/ { }` — gap iteration

`y/re/ { stmts }` iterates over the **gaps** between matches of `re`
in `$0`.  Only valid inside a block; not a top-level rule form.

```awk
{ y/,/ { print $0 } }
```
`a,b,c` → `a` / `b` / `c`

`y` is a cawk extension — it is not in Pike's "AWK of the future"
examples but comes from sam's command language.  It fills a genuine
gap (no AWK idiom exists for gap iteration).

---

## Examples from the paper

### Rectangle extraction (Peter-on-Silicon)

The opening example in Pike's paper.  Given ASCII art:

```
   ###
  ##
 ####
```

extract each `#`-run's position.  In standard AWK this requires
tracking character position with `split` or a character loop.  In
cawk, x-expression rules fire on the exact runs:

```awk
BEGIN { x=1; y=1 }
/ +/  { x += length($0) }
/#+/  { print "rect", x, x+length($0), y, y+1; x += length($0) }
/\n/  { x=1; y++ }
```

Each rule's `$0` is the matched run; `length($0)` is the run length.
Space runs advance `x`; `#` runs emit a rectangle and advance `x`;
newlines reset `x` and increment `y`.

### Bibliography search (Bimmler)

From Pike's paper: extract titles of papers by Bimmler from a `refer`
database where each entry is a block of `%`-prefixed field lines
separated by blank lines:

```awk
/(.+\n)+/ {
    if ($0 ~ /%A.*Bimmler/) {
        /.*\n/ { if ($0 ~ /^%T/) { print $0 } }
    }
}
```

`/(.+\n)+/` matches one paragraph (a run of non-blank lines); `$0` is
the full entry.  `$0 ~ /%A.*Bimmler/` guards on authorship.
The inner `/.*\n/` iterates over lines within the paragraph; `$0 ~
/^%T/` selects the title field.

This is the exact code Pike shows.  In standard AWK the equivalent
requires `RS=""` paragraph mode, `split($0, …, "\n")`, and index
arithmetic.

### Hotkey daemon

Multi-line patterns match key chords atomically; unmatched lines fall
through to a catch-all bare block:

```awk
/alt x\nz\n/ { print "chord: alt-x-z" }
/ctrl c\n/   { exit }
             { print "key:", $0 }
```

Input `alt x\nz\nalt x\nq\nctrl c\n`:

```
chord: alt-x-z
key: alt x
key: q
```

At stream position 0 the chord `/alt x\nz\n/` matches the first two
lines atomically and fires.  At position 8, `alt x\n` is present but
`q\n` follows (not `z\n`), so the chord fails and the bare block fires
`key: alt x`.  Then `q\n` → `key: q`.  Then `/ctrl c\n/` fires and
exits.

This pattern — competing multi-line rules plus a line-level catch-all —
is the idiom for event-driven dispatch from a key daemon.

---

## Standard AWK compatibility

What is **kept** from AWK:

- `BEGIN` / `END`
- All statements: `if`/`else`, `for`, `while`, `do`, `break`,
  `continue`, `return`, `print`, `printf`, `delete`, `exit`
- All builtins: `gsub`, `sub`, `match`, `split`, `substr`, `sprintf`,
  `length`, `toupper`, `tolower`, `index`, `sin`, `cos`, `sqrt`,
  `int`, `rand`, `srand`, `system`, `getline` (from file or pipe)
- User-defined functions
- Arrays (including multi-dimensional `a[i,j]`)
- `OFS`, `ORS`, `OFMT`, `SUBSEP`, `CONVFMT`, `ENVIRON`, `ARGC`, `ARGV`, `FILENAME`
- Output redirection: `>`, `>>`, `|`
- `-v var=val`, `-f progfile`
- `~` and `!~` operators

What is **not** in cawk:

- `FS`, field splitting, `$1`/`$2`/… as fields, `NF`
  — replaced by regex capture groups
- `RS`, `NR`, `FNR`
  — there is no record concept; the stream is scanned continuously
- `next`, `nextfile`
  — no record loop to skip
- Range patterns (`pat1, pat2`)
  — stateful; not compatible with stream scanning
- `-F` flag

---

## Building

```bash
go build -o cawk .
```

The generated parser `awk.go` is committed; no extra tools are needed
for a plain build.  To regenerate after editing the grammar
(`awk.go.y`):

```bash
go install golang.org/x/tools/cmd/goyacc@latest
go generate
go build -o cawk .
```

## Testing

```bash
go test ./...
```

---

## Usage

```
cawk [-v var=val] [-f progfile | 'prog'] [file ...]
```

---

## Implementation notes

### Lexer
Rob Pike's state-function lexer ([Lexical Scanning in Go][lex]) with
context-sensitive `/` disambiguation (regex vs. division).

[lex]: https://www.youtube.com/watch?v=HxaD_trXwRE

### Parser
goyacc (LALR(1)).  Explicit `CONCAT_OP` tokens are injected by the
lexer to resolve the "concatenation vs. binary operator" ambiguity
without reduce/reduce conflicts.

### Stream scanner
A single combined NFA (stitched `*syntax.Prog`) is built once from all
top-level rules via `regexp/syntax`.  Each `InstMatch` instruction
carries a rule index in its otherwise-unused `Arg` field.  A custom
Thompson NFA executor (`internal/nfa`) runs a single anchored pass over
the buffer at each stream position, returning the winning rule index and
`matchcap` slice.  This gives O(m·n) scan time where m is rules and n
is input length, with no per-position regex recompilation.

### Match state stack
`$0`, `$1`/`$2`/…, and `$name` live on a push-down stack.  Top-level
rules push one frame; inner `/re/ { }` blocks push a frame per
iteration.  Pop restores the outer state completely.  This is how the
Bimmler example can freely nest outer paragraph context with inner
line iteration.

---

## References

- [Structural Regular Expressions][sre] — Rob Pike (1987)
- [A Tutorial for the sam Command Language][sam] — Rob Pike
- [The AWK Programming Language][awk] — Aho, Weinberger, Kernighan

[sam]: http://doc.cat-v.org/plan_9/4th_edition/papers/sam/
[awk]: https://ia803404.us.archive.org/0/items/pdfy-MgN0H1joIoDVoIC7/The_AWK_Programming_Language.pdf
