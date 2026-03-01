package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// callBuiltin dispatches to built-in AWK functions.
func (interp *Interpreter) callBuiltin(name string, args []Expr, scope *Scope) *Value {
	switch name {
	// Math
	case "sin":
		return numVal(math.Sin(interp.numArg(args, 0, scope)))
	case "cos":
		return numVal(math.Cos(interp.numArg(args, 0, scope)))
	case "atan2":
		return numVal(math.Atan2(interp.numArg(args, 0, scope), interp.numArg(args, 1, scope)))
	case "exp":
		return numVal(math.Exp(interp.numArg(args, 0, scope)))
	case "log":
		n := interp.numArg(args, 0, scope)
		if n <= 0 {
			fmt.Fprintf(os.Stderr, "cawk: log of non-positive number\n")
			return numVal(math.NaN())
		}
		return numVal(math.Log(n))
	case "sqrt":
		n := interp.numArg(args, 0, scope)
		if n < 0 {
			fmt.Fprintf(os.Stderr, "cawk: sqrt of negative number\n")
			return numVal(math.NaN())
		}
		return numVal(math.Sqrt(n))
	case "int":
		return numVal(math.Trunc(interp.numArg(args, 0, scope)))
	case "rand":
		return numVal(rng.Float64())
	case "srand":
		var seed int64
		if len(args) > 0 {
			seed = int64(interp.numArg(args, 0, scope))
		} else {
			seed = time.Now().UnixNano()
		}
		old := rng
		_ = old
		rng = rand.New(rand.NewSource(seed))
		return numVal(float64(seed)) // return old seed (simplified)

	// String
	case "length":
		if len(args) == 0 {
			return numVal(float64(len(interp.getField(0).toString())))
		}
		v := interp.evalExpr(args[0], scope)
		if v.isArray() {
			return numVal(float64(len(v.arr)))
		}
		return numVal(float64(len(v.toString())))

	case "index":
		s := interp.strArg(args, 0, scope)
		t := interp.strArg(args, 1, scope)
		pos := strings.Index(s, t)
		if pos < 0 {
			return numVal(0)
		}
		return numVal(float64(pos + 1))

	case "substr":
		s := interp.strArg(args, 0, scope)
		runes := []rune(s)
		start := int(interp.numArg(args, 1, scope))
		if start < 1 {
			start = 1
		}
		if start > len(runes)+1 {
			return strVal("")
		}
		if len(args) >= 3 {
			length := int(interp.numArg(args, 2, scope))
			end := start - 1 + length
			if end > len(runes) {
				end = len(runes)
			}
			if start-1 > end {
				return strVal("")
			}
			return strVal(string(runes[start-1 : end]))
		}
		return strVal(string(runes[start-1:]))

	case "split":
		s := interp.strArg(args, 0, scope)
		// arg[1] is the array to split into
		arr := interp.getOrCreateArray(args[1], scope)
		// Clear the array
		arr.arr = make(map[string]*Value)

		fs := " " // default: whitespace (no FS variable in stream mode)
		if len(args) >= 3 {
			fs = interp.patternArg(args, 2, scope)
		}
		var parts []string
		if fs == " " {
			parts = strings.Fields(s)
		} else if len(fs) == 1 {
			parts = strings.Split(s, fs)
		} else {
			re := mustCompile(fs)
			parts = re.Split(s, -1)
		}
		for i, p := range parts {
			arr.arr[strconv.Itoa(i+1)] = numStr(p)
		}
		return numVal(float64(len(parts)))

	case "sub":
		return interp.builtinSub(args, scope, false)
	case "gsub":
		return interp.builtinSub(args, scope, true)

	case "match":
		s := interp.strArg(args, 0, scope)
		pattern := interp.patternArg(args, 1, scope)
		re := mustCompile(pattern)
		loc := re.FindStringIndex(s)
		if loc == nil {
			interp.setVar("RSTART", numVal(0), scope)
			interp.setVar("RLENGTH", numVal(-1), scope)
			return numVal(0)
		}
		// Convert byte offset to rune offset+1
		rstart := len([]rune(s[:loc[0]])) + 1
		rlength := len([]rune(s[loc[0]:loc[1]]))
		interp.setVar("RSTART", numVal(float64(rstart)), scope)
		interp.setVar("RLENGTH", numVal(float64(rlength)), scope)
		return numVal(float64(rstart))

	case "sprintf":
		if len(args) == 0 {
			panic(runtimeError("Empty sequence"))
		}
		format := interp.strArg(args, 0, scope)
		fargs := make([]interface{}, len(args)-1)
		for i, a := range args[1:] {
			fargs[i] = interp.evalExpr(a, scope)
		}
		return strVal(awkSprintf(format, fargs, interp.OFMT))

	case "tolower":
		return strVal(strings.ToLower(interp.strArg(args, 0, scope)))
	case "toupper":
		return strVal(strings.ToUpper(interp.strArg(args, 0, scope)))

	case "asorti":
		// asorti(src, dest) — sort src's INDICES into dest; return count
		if len(args) < 1 {
			return numVal(0)
		}
		src := interp.evalExpr(args[0], scope)
		if !src.isArray() {
			return numVal(0)
		}
		keys := make([]string, 0, len(src.arr))
		for k := range src.arr {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		dest := arrayVal()
		for i, k := range keys {
			dest.arr[strconv.Itoa(i+1)] = strVal(k)
		}
		if len(args) >= 2 {
			interp.assignExpr(args[1], dest, scope)
		}
		return numVal(float64(len(keys)))

	case "asort":
		// asort(src, dest) — sort src's VALUES into dest; return count
		if len(args) < 1 {
			return numVal(0)
		}
		src := interp.evalExpr(args[0], scope)
		if !src.isArray() {
			return numVal(0)
		}
		vals := make([]*Value, 0, len(src.arr))
		for _, v := range src.arr {
			vals = append(vals, v)
		}
		sort.Slice(vals, func(i, j int) bool {
			return compare(vals[i], vals[j]) < 0
		})
		dest := arrayVal()
		for i, v := range vals {
			dest.arr[strconv.Itoa(i+1)] = v
		}
		if len(args) >= 2 {
			interp.assignExpr(args[1], dest, scope)
		}
		return numVal(float64(len(vals)))

	case "system":
		cmd := interp.strArg(args, 0, scope)
		return numVal(float64(runSystem(cmd)))

	case "getline":
		return numVal(0) // simplified

	case "close":
		name := interp.strArg(args, 0, scope)
		interp.closeFile(name)
		return numVal(0)

	case "fflush":
		// Flush output — no-op for now
		return numVal(0)

	// Bitwise operations (gawk extension)
	case "and":
		a := numToUint(interp.numArg(args, 0, scope))
		b := numToUint(interp.numArg(args, 1, scope))
		return numVal(float64(a & b))
	case "or":
		a := numToUint(interp.numArg(args, 0, scope))
		b := numToUint(interp.numArg(args, 1, scope))
		return numVal(float64(a | b))
	case "xor":
		a := numToUint(interp.numArg(args, 0, scope))
		b := numToUint(interp.numArg(args, 1, scope))
		return numVal(float64(a ^ b))
	case "compl":
		a := numToUint(interp.numArg(args, 0, scope))
		return numVal(float64(^a))
	case "lshift":
		a := numToUint(interp.numArg(args, 0, scope))
		b := uint(interp.numArg(args, 1, scope))
		return numVal(float64(a << b))
	case "rshift":
		a := numToUint(interp.numArg(args, 0, scope))
		b := uint(interp.numArg(args, 1, scope))
		return numVal(float64(a >> b))

	case "print":
		// print() as a function call — unusual but handle it
		panic(runtimeError("Empty sequence"))

	default:
		panic(runtimeError(fmt.Sprintf("Call to undefined function %s", name)))
	}
}

// numArg evaluates argument i as a number.
func (interp *Interpreter) numArg(args []Expr, i int, scope *Scope) float64 {
	if i >= len(args) {
		return 0
	}
	return interp.evalExpr(args[i], scope).toNumber()
}

// patternArg extracts a regex pattern string from argument i.
// If the arg is a RegexExpr literal, returns its pattern directly.
// Otherwise evaluates to string.
func (interp *Interpreter) patternArg(args []Expr, i int, scope *Scope) string {
	if i >= len(args) {
		return ""
	}
	if re, ok := args[i].(*RegexExpr); ok {
		return re.Pattern
	}
	return interp.evalExpr(args[i], scope).toString()
}

// strArg evaluates argument i as a string.
func (interp *Interpreter) strArg(args []Expr, i int, scope *Scope) string {
	if i >= len(args) {
		return ""
	}
	return interp.evalExpr(args[i], scope).toString()
}

// builtinSub implements sub() and gsub().
func (interp *Interpreter) builtinSub(args []Expr, scope *Scope, global bool) *Value {
	if len(args) < 2 {
		panic(runtimeError("sub/gsub requires at least 2 args"))
	}
	// arg0: regex
	var pattern string
	switch p := args[0].(type) {
	case *RegexExpr:
		pattern = p.Pattern
	default:
		pattern = interp.strArg(args, 0, scope)
	}

	// arg1: replacement
	replacement := interp.strArg(args, 1, scope)

	// arg2: target (default $0)
	var target string
	var targetExpr Expr
	if len(args) >= 3 {
		targetExpr = args[2]
		target = interp.evalExpr(targetExpr, scope).toString()
	} else {
		targetExpr = &FieldExpr{Index: &NumberExpr{Val: 0}}
		target = interp.getField(0).toString()
	}

	re := mustCompile(pattern)

	count := 0
	result := re.ReplaceAllStringFunc(target, func(match string) string {
		if !global && count > 0 {
			return match
		}
		count++
		// Handle & in replacement (represents the matched string)
		r := replaceAmpersand(replacement, match)
		return r
	})

	interp.assignExpr(targetExpr, numStr(result), scope)
	return numVal(float64(count))
}

// replaceAmpersand replaces & in replacement with match, and \& with literal &.
func replaceAmpersand(replacement, match string) string {
	var buf strings.Builder
	for i := 0; i < len(replacement); i++ {
		if replacement[i] == '\\' && i+1 < len(replacement) {
			switch replacement[i+1] {
			case '&':
				buf.WriteByte('&')
				i++
			case '\\':
				buf.WriteByte('\\')
				i++
			default:
				buf.WriteByte(replacement[i])
			}
		} else if replacement[i] == '&' {
			buf.WriteString(match)
		} else {
			buf.WriteByte(replacement[i])
		}
	}
	return buf.String()
}

// awkSprintf formats a string using AWK printf conventions.
func awkSprintf(format string, args []interface{}, ofmt string) string {
	var buf strings.Builder
	argIdx := 0

	getArg := func() *Value {
		if argIdx >= len(args) {
			return &Value{kind: kindUninitialized}
		}
		v := args[argIdx]
		argIdx++
		if val, ok := v.(*Value); ok {
			return val
		}
		return &Value{kind: kindUninitialized}
	}

	i := 0
	for i < len(format) {
		if format[i] != '%' {
			buf.WriteByte(format[i])
			i++
			continue
		}
		i++
		if i >= len(format) {
			break
		}
		if format[i] == '%' {
			buf.WriteByte('%')
			i++
			continue
		}

		// Parse format spec
		start := i - 1
		// Flags
		for i < len(format) && strings.ContainsRune("-+0 #", rune(format[i])) {
			i++
		}
		// Width (* = take from args)
		widthStar := false
		if i < len(format) && format[i] == '*' {
			widthStar = true
			i++
		} else {
			for i < len(format) && format[i] >= '0' && format[i] <= '9' {
				i++
			}
		}
		// Precision (* = take from args)
		precStar := false
		if i < len(format) && format[i] == '.' {
			i++
			if i < len(format) && format[i] == '*' {
				precStar = true
				i++
			} else {
				for i < len(format) && format[i] >= '0' && format[i] <= '9' {
					i++
				}
			}
		}
		if i >= len(format) {
			break
		}

		// Build actual spec, substituting * widths
		specFmt := format[start:i+1]
		if widthStar {
			widthVal := int(getArg().toNumber())
			specFmt = strings.Replace(specFmt, "*", strconv.Itoa(widthVal), 1)
		}
		if precStar {
			precVal := int(getArg().toNumber())
			specFmt = strings.Replace(specFmt, "*", strconv.Itoa(precVal), 1)
		}
		spec := specFmt
		conv := format[i]
		i++

		arg := getArg()
		switch conv {
		case 'd', 'i':
			n := int64(arg.toNumber())
			fmtStr := strings.Replace(spec, string(conv), "d", 1)
			buf.WriteString(fmt.Sprintf(fmtStr, n))
		case 'u':
			n := uint64(arg.toNumber())
			fmtStr := strings.Replace(spec, "u", "d", 1)
			buf.WriteString(fmt.Sprintf(fmtStr, n))
		case 'o':
			n := int64(arg.toNumber())
			buf.WriteString(fmt.Sprintf(spec, n))
		case 'x':
			n := int64(arg.toNumber())
			buf.WriteString(fmt.Sprintf(spec, n))
		case 'X':
			n := int64(arg.toNumber())
			buf.WriteString(fmt.Sprintf(spec, n))
		case 'e', 'E':
			buf.WriteString(fmt.Sprintf(spec, arg.toNumber()))
		case 'f':
			buf.WriteString(fmt.Sprintf(spec, arg.toNumber()))
		case 'g', 'G':
			buf.WriteString(fmt.Sprintf(spec, arg.toNumber()))
		case 's':
			s := arg.toStringFmt(ofmt)
			fmtStr := spec
			buf.WriteString(fmt.Sprintf(fmtStr, s))
		case 'c':
			n := arg.toNumber()
			if n != 0 {
				buf.WriteRune(rune(int(n)))
			} else {
				s := arg.toString()
				if len(s) > 0 {
					buf.WriteRune(rune(s[0]))
				}
			}
		}
	}
	return buf.String()
}

// runSystem runs a shell command and returns the exit code.
func runSystem(cmd string) int {
	c := exec.Command("sh", "-c", cmd)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin
	if err := c.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}

// startPipe starts a command and returns a writer for its stdin.
type pipeWriter struct {
	cmd *exec.Cmd
	w   *os.File
}

func (pw *pipeWriter) Write(p []byte) (n int, err error) {
	return pw.w.Write(p)
}

func (pw *pipeWriter) Close() error {
	pw.w.Close()
	return pw.cmd.Wait()
}

func startPipe(cmdStr string) (*pipeWriter, error) {
	cmd := exec.Command("sh", "-c", cmdStr)
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdin = pr
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return nil, err
	}
	pr.Close()
	return &pipeWriter{cmd: cmd, w: pw}, nil
}

// startPipeReader starts a command and returns a reader for its stdout.
type pipeReaderEntry struct {
	cmd *exec.Cmd
	r   *os.File
}

func (pr *pipeReaderEntry) Read(p []byte) (n int, err error) {
	return pr.r.Read(p)
}

func (pr *pipeReaderEntry) Close() error {
	pr.r.Close()
	return pr.cmd.Wait()
}

func startPipeReader(cmdStr string) *pipeReaderEntry {
	cmd := exec.Command("sh", "-c", cmdStr)
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil
	}
	cmd.Stdout = pw
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return nil
	}
	pw.Close()
	return &pipeReaderEntry{cmd: cmd, r: pr}
}

func (interp *Interpreter) closeFile(name string) {
	for _, prefix := range []string{">:", ">>:", "|:", "<:"} {
		key := prefix + name
		if v, ok := interp.files[key]; ok {
			if c, ok := v.(interface{ Close() error }); ok {
				c.Close()
			}
			delete(interp.files, key)
		}
		if v, ok := interp.pipes[key]; ok {
			if c, ok := v.(interface{ Close() error }); ok {
				c.Close()
			}
			delete(interp.pipes, key)
		}
	}
}

// Avoid unused import warnings
var _ = regexp.QuoteMeta
var _ = strconv.Itoa
