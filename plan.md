# cawk Implementation Plan

Based on Rob Pike's Structural Regular Expressions paper, `sam_tut.txt`,
and the design discussion in this session.

---

## 1. Design Goals

cawk is "the awk of the future" from Pike's paper: a streaming batch
processor where patterns are x-expressions (extractors), not g-expressions
(guards/filters). The key properties:

- Input is a byte stream, not an array of lines.
- Top-level rules are sam-style structural chains: `x/re/ g/re/ { block }`
- `$0` is exactly the text the leading `x` extracted; `$1`/`$2`/... are captures.
- `$name` references named capture groups: `(?P<name>...)` in the regex.
- Match state (i.e. `$0`, `$n`, `$name`) is scoped per `x`/`y` operation and
  saved/restored on nested `x`/`y` entry/exit.
- `g/re/` and `v/re/` are pure guards: they test `$0` but never change it
  or set any `$n`.
- Inside blocks, AWK statements are used for logic; `g/re/{}` `x/re/{}`
  `y/re/{}` `v/re/{}` are structural statements that operate on `$0`.
- For expression conditions inside blocks, use `if (expr) {}` — not a
  bare `expr { }` pattern (which is grammatically ambiguous).
- A bare `{ block }` with no chain is shorthand for `x/[^\n]*\n/ { block }`
  (the AWK-compatible line-by-line default).
- Multiple top-level rules compete: first matching rule wins at each
  stream position (lex/scanner semantics), then advance.
- `BEGIN {}` and `END {}` are unchanged.
- AWK builtins, user functions, getline from files/pipes, and all
  AWK statements inside blocks are unchanged.
- cawk is a filter/extractor, not a text editor: it does not write
  back to input. Output is produced by `print`/`printf` in blocks.

---

## 2. Language Syntax

### 2.1 Top-level program structure

```
program:
    funcdef*
    rule*

rule:
    BEGIN block
  | END block
  | chain block          -- sam-style structural chain + AWK block
  | block                -- shorthand for x/[^\n]*\n/ { block }

chain:
    X REGEX              -- must start with x or y (stream scanner)
  | Y REGEX
  | chain G REGEX        -- append guard
  | chain V REGEX        -- append anti-guard
  | chain X REGEX        -- append structural loop (operates on $0)
  | chain Y REGEX        -- append structural gaps (operates on $0)

block:
    { stmts }
```

Critically: chain elements at the top level have **no** block after the
REGEX. The single block comes at the end of the entire chain. This is how
sam chains work: `x/re/ g/re/ x/re/ command` — the command comes last.

### 2.2 Execution semantics of a chain

Given rule: `x/A/ g/B/ x/C/ v/D/ { action }`

**Step 1 (stream scanner):** Find anchored match of `A` at current buffer pos.
- If no match: rule does not fire. Try next rule.
- If match: `$0` = matched text; `$1`/`$2`/... = A's captures. Advance = `len($0)`.
  Then execute the rest of the chain (`g/B/ x/C/ v/D/`) with this `$0`.

**Step 2 (`g/B/`):** Test if `$0` contains a match of `B`.
- If no match: chain aborts. See note on tentative vs. commit semantics below.

  > **Design choice — tentative semantics (chosen):** if any `g`/`v` fails, the
  > rule does not fire AND does not advance. The next rule is tried at the same
  > position. This allows `x/re/ g/filter/` to be a precise extractor, not just
  > a scanner that discards unmatched extractions.

**Step 3 (`x/C/`):** Structural loop over matches of `C` within current `$0`.
- For each match: push new match state (`$0` = match of `C` in outer `$0`,
  `$n` = C's captures), execute remaining chain and action, then pop.

**Step 4 (`v/D/`):** Anti-guard. Tests current `$0` (inner, set by `x/C/`).
- If `$0` matches `D`: skip this iteration.
- If `$0` does not match `D`: proceed to action.

**Step 5 (`{ action }`):** Execute AWK block with current `$0`/`$n`/`$name` in scope.

### 2.3 Match state and `$0`/`$n`/`$name`

Match state is a stack. Each entry has:

- `Text string` — the value of `$0`
- `Groups []string` — `$1`, `$2`, ... (1-indexed, stored 0-indexed)
- `Named map[string]string` — `$name`

Rules for what sets match state:

- `x/re/` and `y/re/` **push** a new match state on entry, **pop** on exit.
- `g/re/` and `v/re/` **never** modify match state (read `$0`, produce no output).
- The stream scanner's `x` is the initial push; it is popped after the rule's
  action completes.

Reading `$n` (n > 0): `Groups[n-1]` from top of stack. Empty string if out of range.  
Reading `$0`: `Text` from top of stack.  
Reading `$name`: `Named["name"]` from top of stack. Empty string if not found.

Note: `$name` only appears in source as `$identifier` (dollar + letters).
`$1`, `$2`, ... remain positional (dollar + digits).

Named groups in Go regexp: `(?P<name>pattern)`  
Named groups from `g`/`v`: **not** exposed. Guards test; they do not extract.

### 2.4 Structural statements inside blocks

Inside a block, `x`/`y`/`g`/`v` are full statements with their own blocks:

```
x/re/ { stmts }    -- push match per match of re in $0; run stmts; pop
y/re/ { stmts }    -- push match per gap between re-matches in $0; pop
g/re/ { stmts }    -- run stmts only if $0 contains match of re
v/re/ { stmts }    -- run stmts only if $0 does NOT contain match of re
```

These are unchanged from the current design. The key addition: `x` and `y`
statements now explicitly save/restore the outer match state on entry/exit.
Previously this was implicit and broken (inner `x` would clobber outer `$0`).

### 2.5 `$name` — named group references

In source: `$year`, `$month`, `$host` — dollar sign followed by an identifier.  
In regex: `(?P<year>\d{4})`, `(?P<month>\d{2})`, `(?P<host>[a-z.]+)`

Example:

```awk
x/(?P<host>[a-z.]+):(?P<port>\d+)\n/ {
    print $host, $port
}
```

`$name` reads from the `Named` map of the current (top) match state.
`$name` from an outer `x` is visible in the action but shadowed if an inner
`x`/`y` defines a group with the same name. When the inner `x` pops, the
outer `$host` is visible again. There is no implicit scoping magic — named
groups are just map lookups.

### 2.6 What is removed / changed

**Removed from the top-level grammar:**

- `Pattern` interface (`BeginPattern`, `EndPattern`, `RegexPattern`, `ExprPattern`)
- `ExprPattern`: `expr { block }` at the top level — use `x/[^\n]*\n/ { if (expr) {} }`
- Range patterns (already removed in prior iteration)
- `next` / `nextfile` statements (already removed)
- `NR`, `FNR`, `NF`, `FS`, `RS` special variables (already removed)
- `$n` as FS-split fields — `$0`/`$1`/`$2` are now exclusively match-state
- `-F` flag (already removed)

**Unchanged:**

- `BEGIN` / `END`
- All AWK statements inside blocks: `if`/`else`, `for`, `while`, `do`,
  `break`, `continue`, `return`, `print`, `printf`, `delete`, `exit`
- All builtin functions: `gsub`, `sub`, `match`, `split`, `substr`,
  `sprintf`, `length`, `toupper`, `tolower`, `index`, `sin`, `cos`,
  `sqrt`, `int`, `rand`, `srand`, `systime`, `strftime`, `asorti`,
  `asort`, `system`, `getline` (from file/pipe)
- User-defined functions
- Arrays and all array operations
- `OFS`, `ORS`, `OFMT`, `SUBSEP`, `ENVIRON`, `ARGC`, `ARGV`, `FILENAME`
- `-v` flag, `-f` flag
- Output redirection: `print > file`, `print >> file`, `print | cmd`
- Getline from file: `getline var < file`
- Getline from pipe: `cmd | getline var`

---

## 3. File-by-File Changes

### 3.1 `token.go`

Add:

```go
itemNamedGroupRef  // $name — dollar followed by identifier
```

The existing `itemX`, `itemY`, `itemG`, `itemV` tokens are unchanged. They
are already used for structural statements inside blocks. At the top level,
the same tokens appear as chain operators (no block follows them in the
chain — the block comes at the end of the whole chain).

### 3.2 `lexer.go`

Change the `'$'` handling in `lexExpr` / `lexPrimary`.

Current: `'$'` emits `itemDollar`, followed by whatever comes next.  
New: if `'$'` is followed immediately by a letter or underscore, lex the
full identifier and emit `itemNamedGroupRef` with that name as the value.
If `'$'` is followed by a digit or `'('`: existing behavior (positional or
computed field).

```go
case '$':
    l.next()
    if isAlpha(l.peek()) {
        for isAlphaNum(l.peek()) { l.next() }
        l.emit(itemNamedGroupRef)  // value = identifier text
        return lexExpr
    }
    // else: existing handling (DOLLAR token, positional field)
```

The `itemNamedGroupRef` value is the identifier name **without** the leading `$`.

No other lexer changes needed. Chain parsing is handled in the grammar;
the lexer just emits `X`/`Y`/`G`/`V` `REGEX` tokens as before.

### 3.3 `ast.go`

**Remove:**

- `Pattern` interface and all implementations:
  `BeginPattern`, `EndPattern`, `RegexPattern`, `ExprPattern`
- `Pattern` field from `Rule` struct

**Add:** `ChainOp` interface and implementations:

```go
type ChainOp interface{ chainOp() }

// XChainOp: first in chain = stream scanner; subsequent = structural loop
type XChainOp struct { Regex string }
// YChainOp: first in chain = stream gaps; subsequent = structural gaps
type YChainOp struct { Regex string }
// GChainOp: guard — pass only if $0 contains match
type GChainOp struct { Regex string }
// VChainOp: anti-guard — pass only if $0 does not contain match
type VChainOp struct { Regex string }

func (*XChainOp) chainOp() {}
func (*YChainOp) chainOp() {}
func (*GChainOp) chainOp() {}
func (*VChainOp) chainOp() {}
```

**Modify** `Rule`:

```go
type Rule struct {
    IsBegin bool
    IsEnd   bool
    // Chain is nil for bare { block } — implicit x/[^\n]*\n/
    Chain   []ChainOp
    Action  []Stmt
}
```

**Add** to `Expr` types:

```go
// NamedGroupExpr references a named capture group: $name in source.
type NamedGroupExpr struct{ Name string }
func (*NamedGroupExpr) exprNode() {}
```

Keep unchanged: `XStmt`, `YStmt`, `GStmt`, `VStmt` (structural statements
inside blocks) and all other `Stmt` and `Expr` types.

### 3.4 `scope.go`

**Add:** Match state stack

```go
// MatchState holds the $0/$n/$name values set by one x or y operation.
type MatchState struct {
    Text        string            // $0
    Groups      []string          // $1, $2, ... (1-indexed, stored 0-indexed)
    NamedGroups map[string]string // $name
}
```

**Add** to `Interpreter` struct:

```go
matchStack []MatchState
```

**Add** methods:

```go
func (interp *Interpreter) pushMatch(ms MatchState) {
    interp.matchStack = append(interp.matchStack, ms)
}

func (interp *Interpreter) popMatch() {
    n := len(interp.matchStack)
    if n > 0 {
        interp.matchStack = interp.matchStack[:n-1]
    }
}

func (interp *Interpreter) currentMatch() MatchState {
    if len(interp.matchStack) == 0 {
        return MatchState{}
    }
    return interp.matchStack[len(interp.matchStack)-1]
}

// matchStateFromRegex builds a MatchState from a compiled regex and match.
// m is the result of FindStringSubmatchIndex on text.
func matchStateFromRegex(re *regexp.Regexp, text string, m []int) MatchState {
    ms := MatchState{
        Text:        text[m[0]:m[1]],
        NamedGroups: make(map[string]string),
    }
    names := re.SubexpNames()
    for i := 1; i < len(m)/2; i++ {
        start, end := m[2*i], m[2*i+1]
        var val string
        if start >= 0 {
            val = text[start:end]
        }
        ms.Groups = append(ms.Groups, val)
        if names[i] != "" {
            ms.NamedGroups[names[i]] = val
        }
    }
    return ms
}
```

**Change** `getDollar` / `setDollar`:

```go
// getDollar(n):
//   n == 0  → interp.currentMatch().Text
//   n >= 1  → interp.currentMatch().Groups[n-1], or "" if out of range

// setDollar(n, val):
//   Only $0 and $n>0 assignment are meaningful for in-block manipulation.
//   Mutate the top of the stack directly.
if len(interp.matchStack) == 0 { return }
ms := &interp.matchStack[len(interp.matchStack)-1]
if n == 0 { ms.Text = val }
// $n (n>0): update Groups[n-1], grow slice if needed.
```

**Remove** from `Interpreter` struct:

- `currentLine string` (now: `interp.currentMatch().Text`)
- `fields []string` (now: `Groups` in match state)
- `nexting bool` (already removed)
- `currentScanner` (already removed)

Rename/remove `setMatch`, `setLine` helpers — replaced by `pushMatch`/`popMatch`.

### 3.5 `awk.go.y` (grammar)

**Change** `rule` production:

```
OLD:
  rule:
      pattern block  { $$ = &Rule{Pattern: $1, Action: $2} }
    | BEGIN block    { $$ = &Rule{Pattern: &BeginPattern{}, Action: $2} }
    | END block      { $$ = &Rule{Pattern: &EndPattern{}, Action: $2} }
    ;
  pattern:
      /* empty */ | expr | REGEX
    ;

NEW:
  rule:
      chain block
      { $$ = &Rule{Chain: $1, Action: $2} }
    | block
      { $$ = &Rule{Chain: nil, Action: $1} }
    | BEGIN block
      { $$ = &Rule{IsBegin: true, Action: $2} }
    | END block
      { $$ = &Rule{IsEnd: true, Action: $2} }
    ;

  chain:
      X REGEX
      { $$ = []ChainOp{&XChainOp{Regex: $2}} }
    | Y REGEX
      { $$ = []ChainOp{&YChainOp{Regex: $2}} }
    | chain G REGEX
      { $$ = append($1, &GChainOp{Regex: $3}) }
    | chain V REGEX
      { $$ = append($1, &VChainOp{Regex: $3}) }
    | chain X REGEX
      { $$ = append($1, &XChainOp{Regex: $3}) }
    | chain Y REGEX
      { $$ = append($1, &YChainOp{Regex: $3}) }
    ;
```

Remove the `pattern` production entirely.

**Add** to `primary` (expression):

```
primary:
    ...existing primaries...
  | NAMED_GROUP_REF
    { $$ = &NamedGroupExpr{Name: $1} }
  ;
```

where `NAMED_GROUP_REF` is the new token for `$name` references.

**Keep unchanged:** structural statements inside `stmt`:

```
stmt: ... | X REGEX block | Y REGEX block | G REGEX block | V REGEX block
```

The REGEX token in the chain productions is unambiguous: at the top level,
`X REGEX` can only appear at the start of a chain (before a block), while
inside a block, `X REGEX block` is a structural statement with its own block.
The parser state makes this unambiguous. The context-sensitive lexer already
handles `/` as `REGEX_START` vs. `DIV`; chain `X REGEX` / `Y REGEX` follow
the same rules as structural statements.

### 3.6 `eval.go`

**Rewrite** `scanStream`:

```go
func (interp *Interpreter) scanStream(r io.Reader, filename string) {
    interp.FILENAME = filename
    br := bufio.NewReader(r)
    var buf []byte
    pos := 0
    eof := false

    readLine := func() bool { ... }  // unchanged

    rules := interp.mainRules()  // non-BEGIN, non-END rules

    // Compute lookahead: max newlines in any leading XChainOp regex.
    lookahead := 1
    for _, rule := range rules {
        if len(rule.Chain) > 0 {
            if xop, ok := rule.Chain[0].(*XChainOp); ok {
                n := strings.Count(xop.Regex, `\n`) + 1
                if n > lookahead { lookahead = n }
            }
        }
    }

    // ensureLines: buffer at least n complete lines from pos.
    ensureLines := func(n int) { ... }  // unchanged

    for {
        ensureLines(lookahead)
        if pos >= len(buf) { break }

        fired := false
        for _, rule := range rules {
            adv, ok := interp.tryRuleChain(rule, buf, pos)
            if ok {
                pos += adv
                fired = true
                break
            }
        }
        if !fired { pos++ }  // advance one byte (Pike's model)

        if interp.exiting { break }
    }
}
```

**Add** `tryRuleChain`:

```go
// tryRuleChain attempts to fire a rule at buf[pos:].
// Returns (advance, fired).
// If the chain's leading x matches (tentatively), executes the rest
// of the chain. If the full chain passes, fires the action and returns
// the match length. If any g/v in the chain fails, returns (0, false)
// WITHOUT advancing (tentative semantics).
func (interp *Interpreter) tryRuleChain(rule *Rule, buf []byte, pos int) (int, bool) {
    if rule.IsBegin || rule.IsEnd {
        return 0, false
    }

    // Bare block: implicit x/[^\n]*\n/
    if len(rule.Chain) == 0 {
        slice := buf[pos:]
        nl := bytes.IndexByte(slice, '\n')
        if nl < 0 {
            if !eof || len(slice) == 0 { return 0, false }
            // EOF without trailing newline: consume remainder
            nl = len(slice)
            interp.pushMatch(MatchState{Text: string(slice)})
            interp.execStmts(rule.Action, interp.global)
            interp.popMatch()
            return nl, true
        }
        line := string(slice[:nl])
        interp.pushMatch(MatchState{Text: line})
        interp.execStmts(rule.Action, interp.global)
        interp.popMatch()
        return nl + 1, true
    }

    // Chain must start with X or Y (stream scanner).
    switch op := rule.Chain[0].(type) {

    case *XChainOp:
        re := compileDotAll(op.Regex)
        slice := string(buf[pos:])
        m := re.FindStringSubmatchIndex(slice)
        if m == nil || m[0] != 0 { return 0, false }
        ms := matchStateFromRegex(re, slice, m)
        adv := m[1]
        interp.pushMatch(ms)
        fired := interp.execChainTail(rule.Chain[1:], rule.Action)
        interp.popMatch()
        if fired { return adv, true }
        return 0, false  // tentative: x matched but chain failed

    case *YChainOp:
        // Leading y at stream level: iterate gaps from pos to EOF.
        // This is unusual. Interpret as: find the next gap at pos.
        // (A leading y at stream level is rare; see Section 6.2.)
        re := compileDotAll(op.Regex)
        slice := string(buf[pos:])
        gaps := structuralGaps(slice, re)
        if len(gaps) == 0 { return 0, false }
        ms := MatchState{Text: gaps[0]}
        interp.pushMatch(ms)
        fired := interp.execChainTail(rule.Chain[1:], rule.Action)
        interp.popMatch()
        if fired {
            return computeGapAdvance(slice, re, 0), true
        }
        return 0, false
    }
    return 0, false
}
```

**Add** `execChainTail`:

```go
// execChainTail executes chain elements ops[0:] against the current
// match state ($0 = current top of matchStack). If all guards pass and
// all structural loops have at least one iteration, runs action and
// returns true. Returns false if any guard fails or a structural op
// matches nothing.
func (interp *Interpreter) execChainTail(ops []ChainOp, action []Stmt) bool {
    if len(ops) == 0 {
        interp.execStmts(action, interp.global)
        return true
    }

    switch op := ops[0].(type) {

    case *GChainOp:
        re := compileDotAll(op.Regex)
        if !re.MatchString(interp.currentMatch().Text) {
            return false
        }
        return interp.execChainTail(ops[1:], action)

    case *VChainOp:
        re := compileDotAll(op.Regex)
        if re.MatchString(interp.currentMatch().Text) {
            return false
        }
        return interp.execChainTail(ops[1:], action)

    case *XChainOp:
        // Structural loop: iterate over all matches of re within $0.
        re := compileDotAll(op.Regex)
        text := interp.currentMatch().Text
        allMatches := re.FindAllStringSubmatchIndex(text, -1)
        if len(allMatches) == 0 { return false }
        fired := false
        for _, m := range allMatches {
            ms := matchStateFromRegex(re, text, m)
            interp.pushMatch(ms)
            if interp.execChainTail(ops[1:], action) {
                fired = true
            }
            interp.popMatch()
            if interp.exiting { break }
        }
        return fired

    case *YChainOp:
        // Structural gaps: iterate over gaps between matches in $0.
        re := compileDotAll(op.Regex)
        text := interp.currentMatch().Text
        gaps, positions := structuralGapsWithPos(text, re)
        if len(gaps) == 0 { return false }
        fired := false
        for i, gap := range gaps {
            _ = positions[i]
            ms := MatchState{Text: gap}
            interp.pushMatch(ms)
            if interp.execChainTail(ops[1:], action) {
                fired = true
            }
            interp.popMatch()
            if interp.exiting { break }
        }
        return fired
    }
    return false
}
```

**Change** structural statement execution (`XStmt`, `YStmt`, `GStmt`, `VStmt`):

- `XStmt` execution (`evalXStmt`): use `pushMatch(ms)` before action,
  `popMatch()` after — replacing the old save/restore of `$0`/`$n`.
- Same pattern for `YStmt`.
- `GStmt` and `VStmt` do **not** push/pop (they are guards).

**Change** `evalExpr` for field access:

```go
// FieldExpr ($n):
n := evaluate index expression as int
if n == 0: return interp.currentMatch().Text
if n >= 1: return interp.currentMatch().Groups[n-1] or ""

// NamedGroupExpr:
ms := interp.currentMatch()
if v, ok := ms.NamedGroups[expr.Name]; ok { return strVal(v) }
return strVal("")
```

**Change** assignment to `$n`: mutate `matchStack` top's `Text` (for `$0`)
or `Groups` slice (for `$n`, n > 0). This allows `{ $0 = toupper($0); print }`.

### 3.7 `builtin.go`

**`match()`:** sets `RSTART`, `RLENGTH` as before. Does not affect `$0`/`$n`.
No change needed.

**`sub()` / `gsub()`:** when target is `$0` (implicit), modify
`interp.currentMatch().Text` in-place (mutate top of match stack).
When target is a variable, modify that variable. Replace the
`setField(0, ...)` call with a direct mutation of the match stack top.

**`split()`:** already fixed to not use `FS`. No further change.

No other builtin changes needed.

### 3.8 `regex.go`

**Add** `structuralGapsWithPos`:

```go
// Like structuralGaps but also returns the byte offsets of each gap
// within the original string. Used by execChainTail for YChainOp.
func structuralGapsWithPos(s string, re *regexp.Regexp) ([]string, [][2]int)
```

The existing `structuralGaps` and `structuralExtract` remain.
`compileDotAll` remains unchanged (wraps regex with `(?s)` flag).

**Add** `computeGapAdvance` (helper for stream-level `y`): returns the byte
count to advance after a stream-level `y` operation (advance past the first
match of `re` to reach the start of the first gap).

---

## 4. Implementation Order

Work in this order to keep the build green at each step:

1. **`token.go`** — add `itemNamedGroupRef`. Build passes (new token, nothing uses it yet).

2. **`lexer.go`** — handle `$identifier` → emit `itemNamedGroupRef`. Build passes (token
   emitted but not parsed yet; parser will error on it only if test programs use `$name` syntax).

3. **`ast.go`** — add `ChainOp` interface and implementations; add `NamedGroupExpr`;
   change `Rule` struct; remove `Pattern` interface and all `Pattern` types.
   Build will fail until grammar and eval are updated.

4. **`awk.go.y`** — add `chain` production; change `rule` production; remove `pattern`;
   add `NAMED_GROUP_REF` token and `NamedGroupExpr` primary. Run `go generate`
   (regenerates `awk.go` via goyacc). Build passes with new grammar.

5. **`scope.go`** — add `MatchState` struct; add `matchStack` to `Interpreter`;
   add `pushMatch`/`popMatch`/`currentMatch`; add `matchStateFromRegex`;
   change `getDollar`/`setDollar`; remove old helpers.
   Build may fail until `eval.go` is updated.

6. **`eval.go`** — rewrite `scanStream` with chain-based `tryRuleChain` and
   `execChainTail`; update `XStmt`/`YStmt`/`GStmt`/`VStmt` execution to use
   push/popMatch; update `evalExpr` for `NamedGroupExpr`; update `$0` assignment.
   `go build` passes.

7. **`regex.go`** — add `structuralGapsWithPos`; add `computeGapAdvance`.
   `go build` passes.

8. **`awk_test.go`** — rewrite all tests for new model (see Section 5).
   `go test` passes.

9. **`builtin.go`** — fix `sub`/`gsub` to mutate match stack top when target is `$0`.
   `go test` passes.

10. **Final** — `go generate && go build`; commit `awk.go` (generated parser);
    verify no regressions.

---

## 5. Test Plan

### 5.1 Stream scanner basics

**TestXChainBasic**
- prog: `x/[a-z]+/ { print $0 }`
- input: `"hello world\n"`
- want: `"hello\nworld\n"`
- (`x` extracts each word; space and newline are silently advanced)

**TestXChainCaptures**
- prog: `x/([0-9]{4})-([0-9]{2})-([0-9]{2})/ { print $1, $2, $3 }`
- input: `"2024-01-15\n"`
- want: `"2024 01 15\n"`

**TestXChainMultiLine**
- prog: `x/alt x\nz\n/ { print "CHORD" }`
- input: `"alt x\nz\n"`
- want: `"CHORD\n"`

**TestXChainFirstWins**
- prog: `x/abc/ { print "ABC" }  x/[a-z]+/ { print "word:", $0 }`
- input: `"abc xyz\n"`
- want: `"ABC\nword: xyz\n"`
- (`abc` fires for first token; `[a-z]+` fires for `xyz`)

**TestImplicitLineScanner**
- prog: `{ print "line:", $0 }`
- input: `"a\nb\nc\n"`
- want: `"line: a\nline: b\nline: c\n"`
- (`$0` does not include the `\n`)

**TestImplicitVsExplicit**  
`x/[^\n]*\n/ { print "x:", $0 }` vs `{ print "impl:", $0 }` on input `"hello\n"`:
- The `x` version: `$0 = "hello\n"` (includes newline, since `x` extracted it).
- The implicit version: `$0 = "hello"` (no newline, stripped by implicit scanner).
- This is a documented semantic difference.

### 5.2 Guards (`g` and `v`)

**TestGGuard**
- prog: `x/[^\n]*\n/ g/error/ { print "ERR:", $0 }`
- input: `"ok\nerror here\nok\n"`
- want: `"ERR: error here\n"`

**TestVGuard**
- prog: `x/[^\n]*\n/ v/error/ { print $0 }`
- input: `"ok\nerror here\nfine\n"`
- want: `"ok\nfine\n"`

**TestGVTentative**
- prog: `x/hello/ g/world/ { print "yes" }   x/[a-z]+/ { print $0 }`
- input: `"hello world\n"`
- First rule: `x` matches `"hello"` but `g/world/` fails (hello ≠ world).
  Tentative semantics: retreat to pos 0, try second rule.
- Second rule: `x/[a-z]+/` matches `"hello"`, prints `"hello"`.
- Then `" world\n"`: first rule misses; second matches `"world"`.
- want: `"hello\nworld\n"`

### 5.3 Chain tail: nested `x` and `y`

**TestChainNestedX**
- prog: `x/(.+\n)+/ x/[^\n]*\n/ g/key/ { print $0 }`
- input: `"key: a\nvalue: b\n\nother: c\n"`
- Outer `x` matches paragraph `"key: a\nvalue: b\n"`.
  Inner `x` iterates lines; `g/key/` passes for `"key: a\n"`.
- want: `"key: a\n"`

**TestBimmler** (from Pike's paper)
- prog: `x/(.+\n)+/ g/%A.*Bimmler/ x/.*\n/ g/%T/ { print $0 }`
- Matches paragraphs by Bimmler, extracts title lines.

**TestChainY**
- prog: `x/[^\n]*\n/ y/[0-9]+/ { print "non-num:", $0 }`
- input: `"abc123def456\n"`
- Outer `x` extracts `"abc123def456\n"` as `$0`.
  Inner `y` gaps between `[0-9]+` matches: `"abc"`, `"def"`, `"\n"`.
- want: `"non-num: abc\nnon-num: def\nnon-num: \n"`

### 5.4 Named groups (`$name`)

**TestNamedGroups**

```awk
x/(?P<year>\d{4})-(?P<month>\d{2})-(?P<day>\d{2})/ {
    print $year, $month, $day
}
```

- input: `"2024-01-15\n"`
- want: `"2024 01 15\n"`

**TestNamedGroupsFallback**
- prog: `x/(?P<word>[a-z]+)/ { print $word, $1 }`
- `$word` and `$1` should be identical (both are the first capture group).
- input: `"hello\n"`
- want: `"hello hello\n"`

**TestNamedGroupsNested**

```awk
x/(?P<line>[^\n]*)\n/ {
    x/(?P<word>[a-z]+)/ { print $word }
    print "done:", $line
}
```

- input: `"foo bar\n"`
- Inner `x`: `$word = "foo"`, print `"foo"`; `$word = "bar"`, print `"bar"`.
  After inner `x` loop: outer `$line = "foo bar"` is restored.
- want: `"foo\nbar\ndone: foo bar\n"`

**TestNamedGroupsGDoesNotSet**
- prog: `x/(?P<line>[^\n]*)\n/ g/(?P<word>foo)/ { print $line, $word }`
- `$word` is **not** set by `g` (guards do not push match state).
- input: `"foo\n"`
- want: `"foo \n"` (`$line = "foo"`, `$word = ""`)

### 5.5 Match state save/restore

**TestSaveRestore**

```awk
x/([^\n]+)\n/ {
    x/([a-z]+)/ { }   // inner loop sets $1 to each word
    print $1           // should be outer $1 (the whole line)
}
```

- input: `"hello world\n"`
- Inner `x` sets `$1 = "hello"` then `$1 = "world"`.
  After inner loop, outer state is restored: `$1 = "hello world"`.
- want: `"hello world\n"`

**TestNestedDollarZero**

```awk
x/([^\n]+)\n/ {
    x/[a-z]+/ { print $0 }
    print "outer:", $0
}
```

- input: `"hello world\n"`
- want: `"hello\nworld\nouter: hello world\n"`

**TestDollarZeroAssignment**
- prog: `{ $0 = toupper($0); print }`
- input: `"hello\n"`
- want: `"HELLO\n"`
- (Assignment to `$0` mutates the match stack top.)

### 5.6 AWK compatibility inside blocks

**TestCountLines**
- prog: `BEGIN{n=0} { n++ } END{print n}`
- input: `"a\nb\nc\n"`
- want: `"3\n"`

**TestCountAndFilter**

```awk
BEGIN{n=0}
x/[^\n]*\n/ {
    n++
    if (n == 2) { print "second:", $0 }
    g/error/ { print "ERR:", $0 }
}
```

- input: `"ok\nsecond\nerror line\n"`
- want: `"second: second\nERR: error line\n"`

**TestUserFunction**
- prog: `function double(x){return x*2}  x/[0-9]+/{print double($0)}`
- input: `"3\n7\n"`
- want: `"6\n14\n"`

**TestArrays**
- prog: `x/([a-z]+)/ { count[$1]++ }  END { print count["a"], count["b"] }`
- input: `"a b a b b\n"`
- want: `"2 3\n"`

**TestBuiltins**
- prog: `x/[^\n]*\n/ { gsub(/o/, "0"); print $0 }`
- input: `"foo bar\n"`
- want: `"f00 bar\n"`

**TestBeginEnd**
- prog: `BEGIN{print "start"} { } END{print "end"}`
- input: `"x\n"`
- want: `"start\nend\n"`

**TestGetlineFile**: write `"secret\n"` to a temp file, then:
- prog: `BEGIN { getline line < "/path/to/tmpfile"; print line }`
- want: `"secret\n"`

**TestOutputRedirect**
- prog: `x/[^\n]*\n/ { print $0 > "/path/to/tmpfile" }`
- input: `"hello\nworld\n"`
- file should contain `"hello\nworld\n"`

### 5.7 Hotkey daemon

**TestHotkeyChord**

```awk
x/alt x\nz\n/ { print "CHORD:alt-x-z" }
x/ctrl c\n/   { exit }
{ print "key:", $0 }
```

- input: `"alt x\nz\nalt x\nq\nctrl c\n"`
- want: `"CHORD:alt-x-z\nkey: alt x\nkey: q\n"`
- (`ctrl c` fires `exit`; no further output)

### 5.8 Structural examples from Pike's paper

**TestCapitalizeStandaloneI**
- prog: `x/[A-Za-z]+/ g/i/ v/../ { print "I" }`
- input: `"this is a test\n"`
- Extracts words; `g/i/` keeps those containing `i`; `v/../` rejects
  those with 2+ chars. Only standalone `"i"` passes → print `"I"`.
- want: `"I\n"`

**TestRenameVar** (conceptual)

```awk
{
    y/\\n/ x/[a-zA-Z_][a-zA-Z_0-9]*/ g/n/ v/../ { print "num" }
}
```

The bare `{ }` is the implicit line scanner giving `$0 = line`; the
`y`/`x`/`g`/`v` chain operates on `$0` inside the block.
`y/\\n/` skips literal `\n` escapes; `x` extracts identifiers; `g/n/`
keeps those containing `n`; `v/../` rejects multi-char ones.

### 5.9 Edge cases

**TestNoMatchAdvance**
- prog: `x/[A-Z]+/ { print $0 }`
- input: `"Hello WORLD\n"`
- `x/[A-Z]+/` matches `"H"` (from Hello), then `"WORLD"`.
- want: `"H\nWORLD\n"`

**TestEOFNoTrailingNewline**
- prog: `{ print $0 }`
- input: `"hello"` (no trailing newline)
- want: `"hello\n"`

**TestExitInRule**
- prog: `x/stop\n/ { exit }  { print $0 }`
- input: `"a\nb\nstop\nc\n"`
- want: `"a\nb\n"`

**TestExitRunsEnd**
- prog: `x/stop\n/ { exit }  END { print "done" }`
- input: `"a\nstop\n"`
- want: `"done\n"`

**TestEmptyInput**
- prog: `BEGIN{print "hello"} { print $0 } END{print "world"}`
- input: `""`
- want: `"hello\nworld\n"`

---

## 6. Open Questions / Future Work

### 6.1 Tentative vs. commit semantics for `g`/`v` in chains

In `x/A/ g/B/ { action }`: if `g/B/` fails, does the stream advance by
`len(A)` (commit) or stay at pos (tentative)?

Tentative (chosen above) is more powerful — the stream is only advanced
when the full chain commits. Commit is simpler but means unmatched
extractions are silently consumed. Document the chosen behavior clearly.

### 6.2 Stream-level `y`

A leading `y` at the stream level (`y/re/ { action }` as a top-level rule)
is unusual. It would mean "fire for each gap between `re`-matches in the
remaining stream." This requires buffering the whole stream or processing
gaps lazily. Leave unimplemented for now; emit a parse-time error if `y`
appears as the first element of a top-level chain.

### 6.3 `OFMT` and number-to-string formatting

`$0` is always a string (the matched text). Arithmetic can be performed
on it (it will be parsed as a number if numeric). `OFMT` is used when
printing numbers. No change from AWK semantics here.

### 6.4 Multiple files

`FILENAME`, `ARGC`, `ARGV` are unchanged. The stream scanner runs per-file
in sequence. Match state is reset between files. `BEGIN`/`END` run once.

### 6.5 NR equivalent

With no record model, `NR` is gone. Users who want a line count use:

```awk
BEGIN{n=0} { n++ } END{print n}
```

Or count match firings with a counter variable in the rule.

### 6.6 `$0` mutation and output reconstruction

cawk is a filter: it only produces output via `print`/`printf`. There is
no mechanism to "output the modified input with substitutions." For sed-like
behavior, use `sed` or combine cawk with other tools.

For common cases, `sub`/`gsub` on `$0` followed by `print` gives a useful
approximation:

```awk
{ gsub(/foo/, "bar"); print $0 }
```

### 6.7 Getline interaction with match state

`getline` from a file populates a variable, not `$0`. It does not affect
the match stack. (Getline from the main stream is not supported — the main
stream is owned by the scanner.)

---

## 7. Examples — The New cawk

```awk
# Count words (x-expression word extraction)
x/[a-zA-Z]+/ { words++ }
END { print words }

# Print lines containing "error" (chain: line scanner + guard)
x/[^\n]*\n/ g/error/ { print $0 }

# Date parsing with named groups
x/(?P<y>\d{4})-(?P<m>\d{2})-(?P<d>\d{2})/ {
    printf "%s/%s/%s\n", $d, $m, $y
}

# Hotkey daemon
x/ctrl alt del\n/ { system("lock-screen") }
x/ctrl c\n/       { exit }
{ }                         # consume and ignore other keys

# Paragraph search (from Pike's paper)
x/(.+\n)+/ g/Bimmler/ x/.*\n/ g/^%T/ { print $0 }

# Rename standalone variable n → num, protecting \n escapes
# (outer { } = implicit line scanner; chain operates on $0 = each line)
{
    y/\\n/ x/[a-zA-Z_][a-zA-Z_0-9]*/ g/n/ v/../ { print "num" }
}

# Word frequency count
x/[a-zA-Z]+/ { freq[$0]++ }
END { for (w in freq) print freq[w], w }

# CSV field extraction (second field)
x/[^\n]*\n/ {
    x/(?P<field>[^,\n]+)/ {
        fieldnum++
        if (fieldnum == 2) print $field
    }
    fieldnum = 0
}
```
