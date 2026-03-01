# Bug: top-level /regex/ patterns cannot match newlines in $0

## Summary

Standard AWK strips the record terminator before assigning `$0`, so a
top-level pattern like `/command b\n/` never matches even though the
character sequence `command b\n` exists in the input stream.  For a tool
that wants to treat the stream as structured text (e.g. a line-oriented
event dispatcher), this makes it impossible to anchor patterns to a
complete record boundary using `\n`.

## Reproduction

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

Only the `^hello$` pattern fires.  The `hello\n` pattern never matches
because `$0` is `"hello"`, not `"hello\n"`.

## Expected behaviour

cawk extends AWK with structural regular expressions that treat input as
a character stream.  In that model it is natural to write a pattern that
spans a record boundary, e.g. `/foo\nbar/` to match two consecutive
lines.  At minimum, `/hello\n/` ought to match a line whose content is
`hello`, with the `\n` acting as an end-of-record anchor (equivalent to
`$` in standard AWK).

Two possible fixes:

1. **Retain the newline in `$0`** for pattern matching (breaking AWK
   compatibility for programs that print `$0` and expect no double
   newline, but correct for stream-oriented use).

2. **Treat `\n` in a pattern as `$`** when the pattern is applied
   against a line-split record — i.e. normalise `/foo\n/` to `/foo$/`
   before matching.  This is backward-compatible with standard AWK
   behaviour.

3. **Add a stream mode** (e.g. `-0` flag or `RS=""`) in which cawk does
   not split on newlines at all and applies patterns to the raw byte
   stream, letting structural regex operate across record boundaries.

## Context

Discovered while writing a line-oriented 9P event dispatcher
(`hotkeys.cawk`) that sits between a keyboard daemon and Acme.  Each
event is one line (e.g. `command n`).  The script used patterns of the
form `/command n\n/` intending to match exactly the line `command n`,
but every event fell through to the catch-all and was forwarded
unchanged.  Switching to `/^command n$/` fixed the problem.

The cawk README describes structural operations (`x`, `y`, `g`, `v`) as
working on the *current text* (`$0`), which correctly implies that `\n`
is not present.  However the structural-regex mental model leads users to
expect that `\n` in a top-level pattern will match the line terminator,
since it does in sam, sed, and most other tools that expose structural
regex.
