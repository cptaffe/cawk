// cawk — awk from plan9port with Structural Regular Expressions
// See: https://doc.cat-v.org/bell_labs/structural_regexps/se.pdf
//
// Usage: cawk [-F sep] [-v var=val] [-f progfile | 'prog'] [file ...]
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const usage = `Usage: cawk [-F fs] [-v var=val] [-f progfile | 'prog'] [file ...]

Options:
  -F fs        Set field separator (default: space)
  -v var=val   Set variable before execution
  -f progfile  Read program from file
  -e prog      Program text (alternative to inline)

Structural regex extensions (from Rob Pike's paper):
  /regex/      Pattern: extracts matches (not lines containing match)
  x/regex/ {}  Statement: loop over all matches in current text
  y/regex/ {}  Statement: loop over gaps between matches
  g/regex/ {}  Statement: run body if current text matches
  v/regex/ {}  Statement: run body if current text does NOT match
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
		case arg == "-F":
			i++
			if i >= len(args) {
				die("missing argument for -F")
			}
			// Set FS; handle escape sequences
			fs := unescapeFS(args[i])
			assigns = append(assigns, "FS="+fs)
		case strings.HasPrefix(arg, "-F"):
			fs := unescapeFS(arg[2:])
			assigns = append(assigns, "FS="+fs)

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

		case arg == "-e":
			i++
			if i >= len(args) {
				die("missing argument for -e")
			}
			progSource += "\n" + args[i]

		case arg == "--":
			i++
			files = append(files, args[i:]...)
			i = len(args)

		case strings.HasPrefix(arg, "-") && len(arg) > 1:
			// Unknown flag — treat remaining as files
			files = append(files, args[i:]...)
			i = len(args)

		default:
			// First non-flag argument: if no program yet, it's the program
			if progSource == "" && progFile == "" {
				progSource = arg
			} else {
				files = append(files, args[i:]...)
				i = len(args)
			}
		}
		i++
	}

	// Read program
	if progFile != "" {
		data, err := os.ReadFile(progFile)
		if err != nil {
			die("cannot open program file %s: %v", progFile, err)
		}
		progSource = string(data)
	}

	if progSource == "" {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	// Parse program
	prog, err := parseProgram("<stdin>", progSource)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cawk: cmd. line:1: %v\n", err)
		os.Exit(2)
	}

	// Create interpreter
	interp := newInterpreter(prog)

	// Set ARGC/ARGV
	interp.ARGC = float64(len(files) + 1)
	argv := arrayVal()
	argv.arr["0"] = strVal("cawk")
	for j, f := range files {
		argv.arr[strconv.Itoa(j+1)] = strVal(f)
	}
	interp.global.setLocal("ARGV", argv)

	// Set ENVIRON
	environ := arrayVal()
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			environ.arr[parts[0]] = strVal(parts[1])
		}
	}
	interp.global.setLocal("ENVIRON", environ)

	// Process -v and -F assignments
	for _, a := range assigns {
		parts := strings.SplitN(a, "=", 2)
		if len(parts) != 2 {
			die("bad assignment: %s", a)
		}
		name := parts[0]
		val := parts[1]
		v := numStr(val)
		interp.setVar(name, v, interp.global)
		interp.setSpecialVar(name, v)
	}

	// Run
	if err := interp.Run(files); err != nil {
		fmt.Fprintf(os.Stderr, "cawk: %v\n", err)
		os.Exit(1)
	}
	os.Exit(interp.exitStatus)
}

func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "cawk: "+format+"\n", args...)
	os.Exit(1)
}

func unescapeFS(fs string) string {
	fs = strings.ReplaceAll(fs, `\t`, "\t")
	fs = strings.ReplaceAll(fs, `\n`, "\n")
	// Handle hex escapes like \x21
	if strings.Contains(fs, `\x`) {
		var result strings.Builder
		i := 0
		for i < len(fs) {
			if fs[i] == '\\' && i+1 < len(fs) && fs[i+1] == 'x' && i+3 < len(fs) {
				hex := fs[i+2 : i+4]
				var val int
				fmt.Sscanf(hex, "%x", &val)
				result.WriteByte(byte(val))
				i += 4
				continue
			}
			result.WriteByte(fs[i])
			i++
		}
		return result.String()
	}
	return fs
}
