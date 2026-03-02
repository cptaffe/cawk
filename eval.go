package main

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// exitSignal is panicked to implement exit.
type exitSignal struct{ code int }

// --- Entry point ---

func (interp *Interpreter) Run(files []string) error {
	runPhase := func(fn func()) (exited bool) {
		defer func() {
			if r := recover(); r != nil {
				if es, ok := r.(exitSignal); ok {
					interp.exitStatus = es.code
					exited = true
				} else {
					panic(r)
				}
			}
		}()
		fn()
		return false
	}

	// BEGIN
	exited := runPhase(func() {
		for _, rule := range interp.prog.Rules {
			if rule.IsBegin {
				interp.execAction(rule.Action, interp.global)
				if interp.exiting {
					return
				}
			}
		}
	})

	// Stream scanning
	if !exited {
		exited = runPhase(func() {
			if len(files) == 0 {
				interp.scanStream(os.Stdin, "-")
			} else {
				for _, f := range files {
					if f == "-" {
						interp.scanStream(os.Stdin, "-")
					} else {
						fh, err := os.Open(f)
						if err != nil {
							fmt.Fprintf(os.Stderr, "cawk: %v\n", err)
							continue
						}
						interp.scanStream(fh, f)
						fh.Close()
					}
					if interp.exiting {
						return
					}
				}
			}
		})
	}

	_ = exited
	runPhase(func() {
		for _, rule := range interp.prog.Rules {
			if rule.IsEnd {
				interp.execAction(rule.Action, interp.global)
			}
		}
	})

	interp.closeAll()
	return nil
}

// mainRules returns all rules except BEGIN and END.
func (interp *Interpreter) mainRules() []*Rule {
	var out []*Rule
	for _, r := range interp.prog.Rules {
		if !r.IsBegin && !r.IsEnd {
			out = append(out, r)
		}
	}
	return out
}

// scanStream runs the greedy stream scanner over r.
//
// At each stream position, rules are tried in order. The first matching
// rule fires: $0 = the match, stream advances by len($0). If no rule
// matches at the current position, one more read is attempted — giving
// multi-line patterns a chance to see their second line — before silently
// advancing one byte (Pike's sparse coverage model).
//
// No line-counting or lookahead heuristic: the buffer is filled with
// whatever each Read() call returns (pipe writes are typically atomic;
// file reads come in large chunks). This keeps latency minimal for
// event-per-line patterns like /command s\n/.
func (interp *Interpreter) scanStream(r io.Reader, filename string) {
	interp.FILENAME = filename

	var buf []byte
	pos := 0
	eof := false

	// fill appends one Read()-worth of data to buf.
	// Returns true if any bytes were added.
	//
	// We use r directly rather than bufio.Reader: bufio defers a combined
	// (n, io.EOF) return into two calls — (n, nil) then (0, io.EOF) — which
	// means eof is set one fill() call later than expected, causing us to
	// advance past the first byte before the bare-block EOF path can fire.
	fill := func() bool {
		if eof {
			return false
		}
		tmp := make([]byte, 4096)
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			eof = true
		}
		return n > 0
	}

	rules := interp.mainRules()

	// Prime the buffer.
	fill()

	for {
		// Compact: drop processed bytes once enough have accumulated
		// so we don't hold onto the whole input in memory forever.
		if pos > 1<<16 {
			buf = buf[pos:]
			pos = 0
		}

		// Refill if the buffer is empty.
		if pos >= len(buf) {
			if !fill() {
				break
			}
			continue
		}

		// Try every rule at the current position.
		fired := false
		for _, rule := range rules {
			adv, ok := interp.tryRule(rule, buf, pos, eof)
			if ok {
				pos += adv
				fired = true
				break
			}
		}

		if fired {
			if interp.exiting {
				return
			}
			continue
		}

		// No rule matched. Read more data (or discover EOF) then retry
		// before concluding this position is unmatched.
		//
		// "continue" unconditionally after fill() because fill() may
		// have added 0 bytes but flipped eof=true — in that case tryRule
		// needs another shot with the updated eof flag so the bare-block
		// EOF path (no trailing newline) can fire.
		if !eof {
			fill()
			continue
		}

		// Truly unmatched at EOF: silent advance.
		pos++

		if interp.exiting {
			return
		}
	}
}

// tryRule attempts to match rule at buf[pos:].
// Returns (advance, true) on success (action has been executed).
// Returns (0, false) if the rule does not match.
func (interp *Interpreter) tryRule(rule *Rule, buf []byte, pos int, eof bool) (int, bool) {
	slice := buf[pos:]

	if rule.Regex == "" {
		// Implicit bare-block rule: /[^\n]*\n/ semantics.
		// $0 = line WITHOUT trailing newline (AWK compat: { print $0 } prints cleanly).
		nl := bytes.IndexByte(slice, '\n')
		if nl < 0 {
			// No newline found.
			if !eof || len(slice) == 0 {
				// Not at EOF, or nothing to consume.
				return 0, false
			}
			// EOF with no trailing newline: treat remaining bytes as last line.
			interp.pushMatch(MatchState{Text: string(slice)})
			interp.execAction(rule.Action, interp.global)
			interp.popMatch()
			return len(slice), true
		}
		// Strip the newline: $0 = line (no \n), advance past the \n.
		interp.pushMatch(MatchState{Text: string(slice[:nl])})
		interp.execAction(rule.Action, interp.global)
		interp.popMatch()
		return nl + 1, true
	}

	// Explicit /Regex/ rule: anchored x-expression.
	// Compile with (?s) so . matches \n (dot-all mode).
	re := compileDotAll(rule.Regex)
	m := re.FindSubmatchIndex(slice)
	if m == nil || m[0] != 0 {
		return 0, false
	}

	// Build match state from capture groups.
	ms := matchStateFromRegex(re, string(slice), m)
	interp.pushMatch(ms)
	interp.execAction(rule.Action, interp.global)
	interp.popMatch()
	// Commit: advance by match length regardless of action.
	// Zero-length match advances by 1 to avoid infinite loop.
	adv := m[1]
	if adv == 0 {
		adv = 1
	}
	return adv, true
}

// reCache memoises compiled regexes so we pay the compilation cost once per
// unique pattern string rather than once per stream position per rule.
// The evaluator is single-threaded so no locking is needed.
var reCache = map[string]*regexp.Regexp{}

// compileDotAll compiles a regex with (?s) so . matches \n.
func compileDotAll(pattern string) *regexp.Regexp {
	if re, ok := reCache[pattern]; ok {
		return re
	}
	re, err := regexp.Compile("(?s)" + pattern)
	if err != nil {
		panic(runtimeError(fmt.Sprintf("bad regex %q: %v", pattern, err)))
	}
	reCache[pattern] = re
	return re
}

// --- Action and statement execution ---

func (interp *Interpreter) execAction(stmts []Stmt, scope *Scope) {
	for _, s := range stmts {
		interp.execStmt(s, scope)
		if interp.breaking || interp.continuing || interp.returning || interp.exiting {
			return
		}
	}
}

func (interp *Interpreter) execStmt(s Stmt, scope *Scope) {
	if s == nil {
		return
	}
	switch st := s.(type) {
	case *ExprStmt:
		interp.evalExpr(st.Expr, scope)

	case *PrintStmt:
		interp.execPrint(st, scope)

	case *IfStmt:
		cond := interp.evalExpr(st.Cond, scope)
		if cond.toBool() {
			interp.execAction(st.Then, scope)
		} else if st.Else != nil {
			interp.execAction(st.Else, scope)
		}

	case *WhileStmt:
		for {
			if !interp.evalExpr(st.Cond, scope).toBool() {
				break
			}
			interp.execAction(st.Body, scope)
			if interp.breaking {
				interp.breaking = false
				break
			}
			if interp.continuing {
				interp.continuing = false
				continue
			}
			if interp.returning || interp.exiting {
				return
			}
		}

	case *DoStmt:
		for {
			interp.execAction(st.Body, scope)
			if interp.breaking {
				interp.breaking = false
				break
			}
			if interp.continuing {
				interp.continuing = false
			}
			if interp.returning || interp.exiting {
				return
			}
			if !interp.evalExpr(st.Cond, scope).toBool() {
				break
			}
		}

	case *ForStmt:
		if st.Init != nil {
			interp.execStmt(st.Init, scope)
		}
		for {
			if st.Cond != nil && !interp.evalExpr(st.Cond, scope).toBool() {
				break
			}
			interp.execAction(st.Body, scope)
			if interp.breaking {
				interp.breaking = false
				break
			}
			if interp.continuing {
				interp.continuing = false
			}
			if interp.returning || interp.exiting {
				return
			}
			if st.Post != nil {
				interp.evalExpr(st.Post, scope)
			}
		}

	case *ForInStmt:
		arr := interp.evalExpr(st.Array, scope)
		if arr == nil || !arr.isArray() {
			return
		}
		keys := make([]string, 0, len(arr.arr))
		for k := range arr.arr {
			keys = append(keys, k)
		}
		for _, k := range keys {
			interp.assignExpr(st.Var, strVal(k), scope)
			interp.execAction(st.Body, scope)
			if interp.breaking {
				interp.breaking = false
				break
			}
			if interp.continuing {
				interp.continuing = false
				continue
			}
			if interp.returning || interp.exiting {
				return
			}
		}

	case *DeleteStmt:
		interp.execDelete(st.Expr, scope)

	case *ReturnStmt:
		if st.Value != nil {
			interp.returnVal = interp.evalExpr(st.Value, scope)
		} else {
			interp.returnVal = &Value{kind: kindUninitialized}
		}
		interp.returning = true

	case *BreakStmt:
		interp.breaking = true

	case *ContinueStmt:
		interp.continuing = true

	case *ExitStmt:
		code := 0
		if st.Status != nil {
			code = int(interp.evalExpr(st.Status, scope).toNumber())
		}
		panic(exitSignal{code})

	case *BlockStmt:
		interp.execAction(st.Body, scope)

	// Inner x-expression: /re/ { } inside a block.
	// Iterates over all non-overlapping matches of Regex within current $0.
	case *RegexStmt:
		interp.execRegexStmt(st, scope)

	// y/re/ { } — gap iteration (extension).
	case *YStmt:
		interp.execYStmt(st, scope)
	}
}

// execRegexStmt executes an inner x-expression: /re/ { stmts } inside a block.
// Iterates over all non-overlapping matches of Regex within the current $0.
// Pushes a new MatchState for each match; pops it after the body runs.
// break/continue are consumed locally (scoped to the match loop).
// exit propagates upward.
func (interp *Interpreter) execRegexStmt(st *RegexStmt, scope *Scope) {
	re := compileDotAll(st.Regex)
	text := interp.currentMatch().Text
	all := re.FindAllStringSubmatchIndex(text, -1)
	for _, m := range all {
		ms := matchStateFromRegex(re, text, m)
		interp.pushMatch(ms)
		interp.execAction(st.Action, scope)
		interp.popMatch()
		if interp.breaking {
			interp.breaking = false
			break
		}
		if interp.continuing {
			interp.continuing = false
			continue
		}
		if interp.returning || interp.exiting {
			break
		}
	}
}

// execYStmt executes y/re/ { stmts } — gap iteration.
// Iterates over the gaps between matches of Regex within the current $0.
// Includes all gaps including empty ones (e.g., trailing gap after last match).
// Pushes a new MatchState for each gap; pops it after the body runs.
func (interp *Interpreter) execYStmt(st *YStmt, scope *Scope) {
	re := compileDotAll(st.Regex)
	text := interp.currentMatch().Text
	matches := re.FindAllStringIndex(text, -1)

	// Collect all gaps (including empty ones).
	prev := 0
	var gaps []string
	for _, m := range matches {
		gaps = append(gaps, text[prev:m[0]]) // may be empty if match at start
		prev = m[1]
	}
	gaps = append(gaps, text[prev:]) // trailing gap (may be empty)

	for _, gap := range gaps {
		interp.pushMatch(MatchState{Text: gap})
		interp.execAction(st.Body, scope)
		interp.popMatch()
		if interp.breaking {
			interp.breaking = false
			break
		}
		if interp.continuing {
			interp.continuing = false
			continue
		}
		if interp.returning || interp.exiting {
			break
		}
	}
}

func (interp *Interpreter) execDelete(e Expr, scope *Scope) {
	switch ex := e.(type) {
	case *IdentExpr:
		v := interp.getVar(ex.Name, scope)
		if v != nil && v.isArray() {
			v.arr = make(map[string]*Value)
		} else {
			interp.setVar(ex.Name, &Value{kind: kindUninitialized}, scope)
		}
	case *IndexExpr:
		arr := interp.evalExpr(ex.Array, scope)
		if arr == nil || !arr.isArray() {
			return
		}
		idxVals := make([]*Value, len(ex.Indices))
		for i, idx := range ex.Indices {
			idxVals[i] = interp.evalExpr(idx, scope)
		}
		key := arrayKey(idxVals, interp.SUBSEP)
		delete(arr.arr, key)
	}
}

// --- Expression evaluator ---

func (interp *Interpreter) evalExpr(e Expr, scope *Scope) *Value {
	if e == nil {
		return &Value{kind: kindUninitialized}
	}
	switch ex := e.(type) {
	case *NumberExpr:
		return numVal(ex.Val)

	case *StringExpr:
		return strVal(ex.Val)

	case *RegexExpr:
		// /pattern/ as an expression: test match against $0 (current match text).
		if matchRegex(ex.Pattern, interp.currentMatch().Text) {
			return numVal(1)
		}
		return numVal(0)

	case *IdentExpr:
		return interp.getVar(ex.Name, scope)

	case *FieldExpr:
		idx := int(interp.evalExpr(ex.Index, scope).toNumber())
		return interp.getField(idx)

	case *NamedGroupExpr:
		// $name — look up named capture group in top of match stack.
		ms := interp.currentMatch()
		if ms.NamedGroups != nil {
			if v, ok := ms.NamedGroups[ex.Name]; ok {
				return strVal(v)
			}
		}
		return strVal("")

	case *IndexExpr:
		arr := interp.getOrCreateArray(ex.Array, scope)
		idxVals := make([]*Value, len(ex.Indices))
		for i, idx := range ex.Indices {
			idxVals[i] = interp.evalExpr(idx, scope)
		}
		key := arrayKey(idxVals, interp.SUBSEP)
		if v, ok := arr.arr[key]; ok {
			return v
		}
		v := &Value{kind: kindUninitialized}
		arr.arr[key] = v
		return v

	case *UnaryExpr:
		return interp.evalUnary(ex, scope)

	case *BinaryExpr:
		return interp.evalBinary(ex, scope)

	case *AssignExpr:
		return interp.evalAssign(ex, scope)

	case *IncrDecrExpr:
		return interp.evalIncrDecr(ex, scope)

	case *TernaryExpr:
		if interp.evalExpr(ex.Cond, scope).toBool() {
			return interp.evalExpr(ex.Then, scope)
		}
		return interp.evalExpr(ex.Else, scope)

	case *ConcatExpr:
		left := interp.evalExpr(ex.Left, scope).toStringFmt(interp.OFMT)
		right := interp.evalExpr(ex.Right, scope).toStringFmt(interp.OFMT)
		return strVal(left + right)

	case *CallExpr:
		return interp.evalCall(ex, scope)

	case *GetlineExpr:
		return interp.evalGetline(ex, scope)

	case *InExpr:
		arr := interp.getVar(interp.arrayName(ex.Array), scope)
		if arr == nil || !arr.isArray() {
			return numVal(0)
		}
		switch k := ex.Key.(type) {
		case *ConcatExpr:
			parts := interp.flattenConcat(k, scope)
			keys := make([]string, len(parts))
			for i, p := range parts {
				keys[i] = p.toString()
			}
			key := strings.Join(keys, interp.SUBSEP)
			if _, ok := arr.arr[key]; ok {
				return numVal(1)
			}
			return numVal(0)
		default:
			key := interp.evalExpr(k, scope).toString()
			if _, ok := arr.arr[key]; ok {
				return numVal(1)
			}
			return numVal(0)
		}

	case *MatchExpr:
		v := interp.evalExpr(ex.Operand, scope)
		matched := matchRegex(ex.Pattern, v.toString())
		if ex.Negate {
			matched = !matched
		}
		return boolVal(matched)
	}

	return &Value{kind: kindUninitialized}
}

func (interp *Interpreter) flattenConcat(e *ConcatExpr, scope *Scope) []*Value {
	var parts []*Value
	if lc, ok := e.Left.(*ConcatExpr); ok {
		parts = interp.flattenConcat(lc, scope)
	} else {
		parts = []*Value{interp.evalExpr(e.Left, scope)}
	}
	parts = append(parts, interp.evalExpr(e.Right, scope))
	return parts
}

func (interp *Interpreter) arrayName(e Expr) string {
	if id, ok := e.(*IdentExpr); ok {
		return id.Name
	}
	return ""
}

func (interp *Interpreter) getOrCreateArray(e Expr, scope *Scope) *Value {
	name := interp.arrayName(e)
	v := interp.getVar(name, scope)
	if v != nil && v.isArray() {
		return v // already an array
	}
	if v == nil {
		v = arrayVal()
		interp.setVar(name, v, scope)
		return v
	}
	// Uninitialized scalar: convert in-place so existing references see the array.
	v.arr = make(map[string]*Value)
	return v
}

func (interp *Interpreter) evalUnary(ex *UnaryExpr, scope *Scope) *Value {
	v := interp.evalExpr(ex.Operand, scope)
	switch ex.Op {
	case "!":
		return boolVal(!v.toBool())
	case "-":
		return numVal(-v.toNumber())
	case "+":
		return numVal(v.toNumber())
	}
	return numVal(0)
}

func (interp *Interpreter) evalBinary(ex *BinaryExpr, scope *Scope) *Value {
	switch ex.Op {
	case "&&":
		if !interp.evalExpr(ex.Left, scope).toBool() {
			return numVal(0)
		}
		if interp.evalExpr(ex.Right, scope).toBool() {
			return numVal(1)
		}
		return numVal(0)
	case "||":
		if interp.evalExpr(ex.Left, scope).toBool() {
			return numVal(1)
		}
		if interp.evalExpr(ex.Right, scope).toBool() {
			return numVal(1)
		}
		return numVal(0)
	case "~", "!~":
		left := interp.evalExpr(ex.Left, scope)
		pattern := interp.extractPattern(ex.Right, scope)
		matched := matchRegex(pattern, left.toString())
		if ex.Op == "!~" {
			matched = !matched
		}
		return boolVal(matched)
	}

	left := interp.evalExpr(ex.Left, scope)
	right := interp.evalExpr(ex.Right, scope)

	switch ex.Op {
	case "+":
		return numVal(left.toNumber() + right.toNumber())
	case "-":
		return numVal(left.toNumber() - right.toNumber())
	case "*":
		return numVal(left.toNumber() * right.toNumber())
	case "/":
		r := right.toNumber()
		if r == 0 {
			panic(runtimeError("division by zero"))
		}
		return numVal(left.toNumber() / r)
	case "%":
		r := right.toNumber()
		if r == 0 {
			panic(runtimeError("modulo by zero"))
		}
		return numVal(math.Mod(left.toNumber(), r))
	case "^":
		return numVal(math.Pow(left.toNumber(), right.toNumber()))
	case "<":
		return boolVal(compare(left, right) < 0)
	case ">":
		return boolVal(compare(left, right) > 0)
	case "<=":
		return boolVal(compare(left, right) <= 0)
	case ">=":
		return boolVal(compare(left, right) >= 0)
	case "==":
		return boolVal(compare(left, right) == 0)
	case "!=":
		return boolVal(compare(left, right) != 0)
	case "in":
		if right.isArray() {
			key := left.toString()
			if _, ok := right.arr[key]; ok {
				return numVal(1)
			}
		}
		return numVal(0)
	}
	return numVal(0)
}

func (interp *Interpreter) extractPattern(e Expr, scope *Scope) string {
	if re, ok := e.(*RegexExpr); ok {
		return re.Pattern
	}
	return interp.evalExpr(e, scope).toString()
}

func extractStaticPattern(e Expr) (string, bool) {
	if re, ok := e.(*RegexExpr); ok {
		return re.Pattern, true
	}
	return "", false
}

func boolVal(b bool) *Value {
	if b {
		return numVal(1)
	}
	return numVal(0)
}

func (interp *Interpreter) evalAssign(ex *AssignExpr, scope *Scope) *Value {
	var newVal *Value
	if ex.Op == "=" {
		newVal = interp.evalExpr(ex.Right, scope)
	} else {
		left := interp.evalExpr(ex.Left, scope)
		right := interp.evalExpr(ex.Right, scope)
		switch ex.Op {
		case "+=":
			newVal = numVal(left.toNumber() + right.toNumber())
		case "-=":
			newVal = numVal(left.toNumber() - right.toNumber())
		case "*=":
			newVal = numVal(left.toNumber() * right.toNumber())
		case "/=":
			r := right.toNumber()
			if r == 0 {
				panic(runtimeError("division by zero"))
			}
			newVal = numVal(left.toNumber() / r)
		case "%=":
			r := right.toNumber()
			if r == 0 {
				panic(runtimeError("modulo by zero"))
			}
			newVal = numVal(math.Mod(left.toNumber(), r))
		case "^=":
			newVal = numVal(math.Pow(left.toNumber(), right.toNumber()))
		}
	}
	interp.assignExpr(ex.Left, newVal, scope)
	return newVal
}

func (interp *Interpreter) assignExpr(lval Expr, v *Value, scope *Scope) {
	switch ex := lval.(type) {
	case *IdentExpr:
		interp.setVar(ex.Name, v, scope)
	case *FieldExpr:
		idx := int(interp.evalExpr(ex.Index, scope).toNumber())
		interp.setField(idx, v)
	case *IndexExpr:
		arr := interp.getOrCreateArray(ex.Array, scope)
		idxVals := make([]*Value, len(ex.Indices))
		for i, idx := range ex.Indices {
			idxVals[i] = interp.evalExpr(idx, scope)
		}
		key := arrayKey(idxVals, interp.SUBSEP)
		arr.arr[key] = v
	default:
		panic(runtimeError("invalid lvalue"))
	}
}

func (interp *Interpreter) evalIncrDecr(ex *IncrDecrExpr, scope *Scope) *Value {
	cur := interp.evalExpr(ex.Operand, scope)
	n := cur.toNumber()
	delta := 1.0
	if ex.Op == "--" {
		delta = -1
	}
	newVal := numVal(n + delta)
	interp.assignExpr(ex.Operand, newVal, scope)
	if ex.Pre {
		return newVal
	}
	return numVal(n)
}

// --- Variable access ---

func (interp *Interpreter) getVar(name string, scope *Scope) *Value {
	if v, ok := interp.getSpecialVar(name); ok {
		return v
	}
	if scope != nil {
		if v := scope.getLocal(name); v != nil {
			return v
		}
	}
	return interp.global.get(name)
}

func (interp *Interpreter) setVar(name string, val *Value, scope *Scope) {
	interp.setSpecialVar(name, val)
	if scope != nil && scope != interp.global && scope.has(name) {
		scope.setLocal(name, val)
		return
	}
	interp.global.setLocal(name, val)
}

// --- Print ---

func (interp *Interpreter) execPrint(st *PrintStmt, scope *Scope) {
	w := interp.getOutput(st.Redir, scope)

	if st.Printf {
		format := ""
		if len(st.Args) > 0 {
			format = interp.evalExpr(st.Args[0], scope).toString()
		}
		args := make([]interface{}, 0, len(st.Args)-1)
		for _, a := range st.Args[1:] {
			args = append(args, interp.evalExpr(a, scope))
		}
		fmt.Fprint(w, awkSprintf(format, args, interp.OFMT))
		return
	}

	if len(st.Args) == 0 {
		// print with no args prints $0 (current match text).
		fmt.Fprint(w, interp.getField(0).toString()+interp.ORS)
		return
	}
	parts := make([]string, len(st.Args))
	for i, arg := range st.Args {
		parts[i] = interp.evalExpr(arg, scope).toStringFmt(interp.OFMT)
	}
	fmt.Fprint(w, strings.Join(parts, interp.OFS)+interp.ORS)
}

// --- Function calls ---

func (interp *Interpreter) evalCall(ex *CallExpr, scope *Scope) *Value {
	if fn, ok := interp.prog.Funcs[ex.Name]; ok {
		return interp.callUserFunc(fn, ex.Args, scope)
	}
	return interp.callBuiltin(ex.Name, ex.Args, scope)
}

func (interp *Interpreter) callUserFunc(fn *FuncDef, args []Expr, scope *Scope) *Value {
	fScope := newScope(interp.global)
	for i, param := range fn.Params {
		var val *Value
		if i < len(args) {
			val = interp.evalExpr(args[i], scope)
		} else {
			val = &Value{kind: kindUninitialized}
		}
		fScope.setLocal(param, val)
	}
	interp.execAction(fn.Body, fScope)
	ret := interp.returnVal
	interp.returning = false
	interp.returnVal = nil
	if ret == nil {
		return &Value{kind: kindUninitialized}
	}
	return ret
}

// --- Getline ---
// Plain getline (from main input) is not supported in stream mode.
// getline from file or pipe is supported.

func (interp *Interpreter) evalGetline(ex *GetlineExpr, scope *Scope) *Value {
	// cmd | getline [var]
	if ex.Cmd != nil {
		cmd := interp.evalExpr(ex.Cmd, scope).toString()
		reader := interp.getPipeReader(cmd)
		if reader == nil {
			return numVal(-1)
		}
		line, err := readLine(reader)
		if err != nil {
			if err == io.EOF {
				return numVal(0)
			}
			return numVal(-1)
		}
		if ex.Var != nil {
			interp.assignExpr(ex.Var, numStr(line), scope)
		} else {
			interp.setDollarZero(line)
		}
		return numVal(1)
	}

	// getline [var] < file
	if ex.File != nil {
		filename := interp.evalExpr(ex.File, scope).toString()
		reader := interp.getFileReader(filename)
		if reader == nil {
			return numVal(-1)
		}
		line, err := readLine(reader)
		if err != nil {
			if err == io.EOF {
				return numVal(0)
			}
			return numVal(-1)
		}
		if ex.Var != nil {
			interp.assignExpr(ex.Var, numStr(line), scope)
		} else {
			interp.setDollarZero(line)
		}
		return numVal(1)
	}

	// Plain getline from main input: not supported.
	panic(runtimeError("plain getline not supported in stream mode; use getline < file or cmd | getline"))
}

// setDollarZero sets $0 to line, mutating the match stack top.
// Used by getline when no variable is specified.
func (interp *Interpreter) setDollarZero(line string) {
	if len(interp.matchStack) > 0 {
		top := &interp.matchStack[len(interp.matchStack)-1]
		top.Text = line
		top.Groups = nil
		top.NamedGroups = nil
	}
}

// --- Utility ---

func readLine(r io.Reader) (string, error) {
	var buf []byte
	b := make([]byte, 1)
	for {
		n, err := r.Read(b)
		if n > 0 {
			if b[0] == '\n' {
				return string(buf), nil
			}
			buf = append(buf, b[0])
		}
		if err != nil {
			if len(buf) > 0 {
				return string(buf), nil
			}
			return "", err
		}
	}
}

func sprintfAWK(format string, args []interface{}, ofmt string) string {
	return awkSprintf(format, args, ofmt)
}

// --- Output management ---

func (interp *Interpreter) getOutput(redir *Redirect, scope *Scope) io.Writer {
	if redir == nil {
		return os.Stdout
	}
	dest := interp.evalExpr(redir.Dest, scope).toString()
	key := redir.Mode + ":" + dest
	if w, ok := interp.files[key]; ok {
		return w.(io.Writer)
	}

	var w io.Writer
	var err error
	switch redir.Mode {
	case ">":
		f, e := os.Create(dest)
		err = e
		w = f
		if f != nil {
			interp.files[key] = f
		}
	case ">>":
		f, e := os.OpenFile(dest, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		err = e
		w = f
		if f != nil {
			interp.files[key] = f
		}
	case "|":
		pw, pe := startPipe(dest)
		err = pe
		w = pw
		if pw != nil {
			interp.files[key] = pw
		}
	default:
		w = os.Stdout
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "cawk: cannot open %s: %v\n", dest, err)
		return os.Stderr
	}
	return w
}

func (interp *Interpreter) closeAll() {
	for _, v := range interp.files {
		if c, ok := v.(io.Closer); ok {
			c.Close()
		}
	}
	for _, v := range interp.pipes {
		if c, ok := v.(io.Closer); ok {
			c.Close()
		}
	}
}

func (interp *Interpreter) getPipeReader(cmd string) io.Reader {
	key := "|:" + cmd
	if r, ok := interp.pipes[key]; ok {
		return r.(io.Reader)
	}
	pr := startPipeReader(cmd)
	if pr != nil {
		interp.pipes[key] = pr
	}
	return pr
}

func (interp *Interpreter) getFileReader(filename string) io.Reader {
	key := "<:" + filename
	if r, ok := interp.files[key]; ok {
		return r.(io.Reader)
	}
	if filename == "-" {
		interp.files[key] = os.Stdin
		return os.Stdin
	}
	f, err := os.Open(filename)
	if err != nil {
		return nil
	}
	interp.files[key] = f
	return f
}

func mapErrno(err error) int {
	if os.IsNotExist(err) {
		return 2
	}
	return 1
}

var rng = rand.New(rand.NewSource(1))

func sortKeys(m map[string]*Value) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func formatInt(n float64) string {
	if n == math.Trunc(n) && !math.IsInf(n, 0) && math.Abs(n) < 1e15 {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatFloat(n, 'g', 6, 64)
}

var _ = sort.Strings // keep import
