// cawk — structural regular expression stream processor
// Inspired by Rob Pike's "Structural Regular Expressions" paper.
//
// Usage: cawk [-v var=val] [-f progfile | 'prog'] [file ...]
package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const usage = `Usage: cawk [-v var=val] [-f progfile | 'prog'] [file ...]

cawk is a stream processor driven by structural regular expressions.
Input is treated as a byte stream; /re/ patterns are x-expressions that
consume exactly what they match (including newlines).

Options:
  -v var=val   Set variable before execution
  -f progfile  Read program from file

Execution model:
  At each stream position, rules are tried in document order.
  The first matching rule fires: $0 = the match, $1/$2/... = capture groups.
  The stream advances by len($0). If no rule matches, advance one byte silently.

Rule forms:
  BEGIN { }    Run before stream processing
  END { }      Run after stream processing
  /regex/ { }  x-expression: anchored match at current stream position (may span lines)
               $0 = matched text, $1/$2/... = capture groups, $name = named groups.
  { }          Bare block: implicit /[^\n]*\n/ scanner; $0 = line without trailing newline.

Statements inside blocks:
  /re/ { }     Inner x-expression: iterate over all matches of re in current $0.
               Pushes/pops match state; outer $0 restored after block.
  y/re/ { }    Gap iteration: iterate over gaps between matches of re in $0.

Guards (standard AWK):
  if ($0 ~ /re/) { ... }    fire if $0 contains match of re
  if ($0 !~ /re/) { ... }   fire if $0 does not contain match of re

Named capture groups:
  /(?P<year>\d{4})-(?P<month>\d{2})/ { print $year, $month }

Example — date reformatter:
  /(?P<y>\d{4})-(?P<m>\d{2})-(?P<d>\d{2})/ { printf "%s/%s/%s\n", $d, $m, $y }

Example — hotkey daemon:
  /alt x\nz\n/ { print "CHORD: alt-x z" }
  /ctrl c\n/   { exit }
  { print "key:", $0 }

Example — word frequency:
  /[a-zA-Z]+/ { freq[$0]++ }
  END { for (w in freq) print freq[w], w }
`

func main() {
	args := os.Args[1:]
	var progSource string
	var progFile string
	var assigns []string
	var files []string

	i := 0
	for i < len(args) {
		arg := args[i]
		switch {
		case arg == "-v":
			i++
			if i >= len(args) {
				die("missing argument for -v")
			}
			assigns = append(assigns, args[i])
		case strings.HasPrefix(arg, "-v"):
			assigns = append(assigns, arg[2:])

		case arg == "-f":
			i++
			if i >= len(args) {
				die("missing argument for -f")
			}
			progFile = args[i]
		case strings.HasPrefix(arg, "-f"):
			progFile = arg[2:]

		case arg == "--":
			i++
			files = append(files, args[i:]...)
			i = len(args)

		case strings.HasPrefix(arg, "-") && len(arg) > 1:
			die("unknown flag: %s", arg)

		default:
			if progSource == "" && progFile == "" {
				progSource = arg
			} else {
				files = append(files, args[i:]...)
				i = len(args)
			}
		}
		i++
	}

	if progFile != "" {
		data, err := os.ReadFile(progFile)
		if err != nil {
			die("cannot open program file %s: %v", progFile, err)
		}
		progSource = string(data)
	}

	if progSource == "" {
		fmt.Fprintf(os.Stderr, "%s", usage)
		os.Exit(1)
	}

	exitCode, err := run(progSource, os.Stdin, os.Stdout, assigns, files)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cawk: %v\n", err)
		os.Exit(2)
	}
	os.Exit(exitCode)
}

// run parses and executes a cawk program, reading from stdin and writing to
// stdout.  It returns the exit code and any parse or runtime error.
// Extracted from main so tests can call it directly without a subprocess.
func run(progSource string, stdin io.Reader, stdout io.Writer, assigns, files []string) (int, error) {
	prog, err := parseProgram("<input>", progSource)
	if err != nil {
		return 2, err
	}

	interp := newInterpreter(prog)
	interp.Stdin = stdin
	interp.Stdout = stdout

	// ARGC/ARGV
	interp.ARGC = float64(len(files) + 1)
	argv := arrayVal()
	argv.arr["0"] = strVal("cawk")
	for j, f := range files {
		argv.arr[strconv.Itoa(j+1)] = strVal(f)
	}
	interp.global.setLocal("ARGV", argv)

	// ENVIRON
	environ := arrayVal()
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			environ.arr[parts[0]] = strVal(parts[1])
		}
	}
	interp.global.setLocal("ENVIRON", environ)

	// -v assignments
	for _, a := range assigns {
		parts := strings.SplitN(a, "=", 2)
		if len(parts) != 2 {
			return 1, fmt.Errorf("bad assignment: %s", a)
		}
		v := numStr(parts[1])
		interp.setVar(parts[0], v, interp.global)
	}

	if err := interp.Run(files); err != nil {
		return 1, err
	}
	return interp.exitStatus, nil
}

func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "cawk: "+format+"\n", args...)
	os.Exit(1)
}
