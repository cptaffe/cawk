# Bug: top-level /regex/ patterns cannot match newlines in $0

**Status: RESOLVED** — Fixed by x-expression semantics (commit 42672a7).

## Summary

Standard AWK strips the record terminator before assigning `$0`, so a
top-level pattern like `/command b\n/` never matches even though the
character sequence `command b\n` exists in the input stream.  For a tool
that wants to treat the stream as structured text (e.g. a line-oriented
event dispatcher), this makes it impossible to anchor patterns to a
complete record boundary using `\n`.

## Reproduction (old cawk)

```awk
# prog.awk
/hello\n/ { print "matched with newline" }
/^hello$/ { print "matched without newline" }
```

```
$ printf 'hello\nworld\n' | cawk -f prog.awk
matched without newline
matched without newline
```

Only the `^hello$` pattern fired because `/hello\n/` was evaluated as an
expression pattern against `$0 = "hello"` (no trailing newline), so the
literal `\n` in the regex could never match.

## Resolution

cawk now uses **x-expression semantics** for all top-level `/re/` rules.
The pattern is matched anchored against the raw input stream (not against a
pre-split line), so `/hello\n/` correctly consumes the sequence `"hello\n"`
as a single token.

```
$ printf 'hello\nworld\n' | cawk -f prog.awk
matched with newline
```

- `/hello\n/` fires once, consuming `"hello\n"`.  `$0 = "hello\n"` (the
  matched bytes, including the newline).
- `/^hello$/` never fires: `$` in x-expression mode is end-of-stream, not
  end-of-line.
- `"world\n"` is consumed silently (no matching rule).

## Hotkeys idiom

The original motivating case (line-oriented event dispatcher) now works
directly:

```awk
/command s\n/  { printf "!%s", $0 }   # drop: $0 = "command s\n"; printf avoids double-newline
/command w\n/  { printf "!%s", $0 }
{ print $0 }                           # forward: bare block strips \n, print re-adds via ORS
```

`printf "!%s", $0` is used instead of `print "!" $0` because `$0` already
contains the trailing newline consumed by the pattern; `print` would add a
second one via ORS.

## Regression tests

See `TestBugNewlineInTopLevelPattern`, `TestBugDollarZeroIncludesNewline`,
`TestBugEventDispatchPattern`, and `TestBugMultilineXExpr` in `awk_test.go`.
