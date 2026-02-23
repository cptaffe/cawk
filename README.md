# cawk — AWK with Structural Regular Expressions

`cawk` is an AWK interpreter written in Go that extends traditional AWK with **structural regular expressions** inspired by Rob Pike's [Structural Regular Expressions](https://doc.cat-v.org/bell_labs/structural_regexps/se.pdf) paper and the [sam](http://sam.cat-v.org/) text editor.

## Architecture

The implementation uses several principled design choices:

- **Lexer**: Rob Pike's state-function lexer ([Lexical Scanning in Go](https://www.youtube.com/watch?v=HxaD_trXwRE)) with context-sensitive `/` disambiguation (regex vs division)
- **Parser**: goyacc (LALR(1)) with explicit CONCAT_OP injection to resolve the classic "concatenation vs binary operators" ambiguity
- **Interpreter**: Tree-walking interpreter with proper AWK semantics

## Features

### Standard AWK
- All standard AWK patterns: `/regex/`, `expr`, `BEGIN`, `END`, range patterns
- Full expression language: arithmetic, string ops, regex match (`~`, `!~`), ternary, assignment
- Arrays with multi-dimensional subscripts (`a[i,j]`)
- All standard built-ins: `print`, `printf`, `sprintf`, `length`, `substr`, `index`, `split`, `sub`, `gsub`, `match`, `toupper`, `tolower`, math functions
- User-defined functions with local variables and arrays passed by reference
- `getline` in all forms (plain, `< file`, `cmd |`, `| var`)
- Pipe output (`print | "cmd"`, `print > "file"`, `print >> "file"`)
- Field splitting and rebuilding with `OFS`
- All control flow: `if`/`else`, `while`, `do`/`while`, `for`, `for`/`in`, `break`, `continue`, `next`, `nextfile`, `exit`

### Structural Regular Expressions (Extensions)

Inspired by Rob Pike's paper, `cawk` adds four new structural operations that work on the **current text** (`$0`):

#### `x/regex/ { body }` — extract matches
Run `body` for each **match** of `/regex/` in the current text. Inside the body, `$0` is the matched substring.

```awk
# Extract all numbers from a line
{ x/[0-9]+/ { print $0 } }
```

#### `y/regex/ { body }` — extract gaps  
Run `body` for each **gap** (non-matching part) between matches of `/regex/`. Inside the body, `$0` is the gap text.

```awk
# Print all non-numeric parts of each line
{ y/[0-9]+/ { printf "%s\n", $0 } }
```

#### `g/regex/ { body }` — guard (if matches)
Run `body` only if the current text **matches** `/regex/`.

```awk
# Process only lines containing a digit
{ g/[0-9]/ { print "has digit:", $0 } }
```

#### `v/regex/ { body }` — void guard (if not matches)
Run `body` only if the current text does **not match** `/regex/`.

```awk
# Skip comment lines
{ v/^#/ { print } }
```

#### Composable

Structural commands can be composed — the "current text" is updated as you descend:

```awk
# For each number-containing line, extract and sum all numbers
{
    g/[0-9]/ {
        x/[0-9]+/ {
            sum += $0+0
        }
    }
}
END { print "total:", sum }
```

#### As Patterns

Structural operations can also be used as rule patterns:

```awk
# This rule runs for each match of /[0-9]+/ in $0
x/[0-9]+/ { print "found number:", $0 }
```

## Usage

```
cawk [-F fs] [-v var=val] [-f progfile | 'prog'] [file ...]

Options:
  -F fs        Set field separator (default: whitespace)
  -v var=val   Set variable before execution
  -f progfile  Read program from file
```

## Examples

```bash
# Word count
awk '{for(i=1;i<=NF;i++) c[$i]++} END{for(w in c) print c[w], w}' file | sort -rn

# Sum a column
awk '{sum += $2} END {print sum}' file

# Extract emails using structural regex
cawk '{x/[a-z.]+@[a-z.]+/{print $0}}' emails.txt

# Process log lines with numbers, ignoring comments
cawk '{v/^#/{g/error/{x/[0-9]+/{print "code:", $0}}}}' log.txt

# CSV processing
cawk -F, 'NR>1{print $1, "age:", $2}' data.csv
```

## Building

```bash
go build -o cawk .
```

The generated parser `awk.go` is committed, so no extra tools are needed for a plain build.

To regenerate it after editing the grammar (`awk.go.y`):

```bash
go install golang.org/x/tools/cmd/goyacc@latest
go generate
go build -o cawk .
```

The grammar lives in `awk.go.y` (the `.go.y` extension gives editors Go-flavoured syntax highlighting on the yacc source).

## Testing

```bash
go test ./...
```

## Implementation Notes

### CONCAT_OP Injection
Standard AWK concatenation (`a b`) is ambiguous with unary `+`/`-` in LALR(1) grammars. `cawk` solves this by injecting explicit `CONCAT_OP` tokens in the lexer when adjacent "value" tokens are seen, rather than using `expr expr` in the grammar. This eliminates the reduce/reduce conflicts between binary arithmetic and unary operators.

### Lexer Context
The lexer is context-sensitive for:
- `/` ambiguity (regex vs division): tracks last token type
- Structural keywords (`x`, `y`, `g`, `v`): only treated as keywords when followed by `/regex/`
- Print redirect: `>` in print context returns `REDIR` token to avoid conflict with comparison

### Array Pass-by-Reference
AWK arrays passed to functions are modified in-place by mutating the `Value.arr` field directly rather than creating new `*Value` objects, preserving the reference-sharing semantics required by the AWK standard.

## References

- [The AWK Programming Language](https://ia803404.us.archive.org/0/items/pdfy-MgN0H1joIoDVoIC7/The_AWK_Programming_Language.pdf) — Aho, Weinberger, Kernighan
- [Structural Regular Expressions](https://doc.cat-v.org/bell_labs/structural_regexps/se.pdf) — Rob Pike  
- [Lexical Scanning in Go](https://www.youtube.com/watch?v=HxaD_trXwRE) — Rob Pike's state-function lexer
- [plan9port awk](https://github.com/9fans/plan9port) — the target behavior
