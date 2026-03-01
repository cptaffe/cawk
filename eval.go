package main

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
)

// --- Control flow signals ---

type breakSignal struct{}
type continueSignal struct{}
type nextSignal struct{}
type nextfileSignal struct{}
type returnSignal struct{ val *Value }

type exitSignal struct{ code int }

// --- Evaluator entry points ---

// Run executes the program on the given inputs.
func (interp *Interpreter) Run(files []string) error {
	// runPhase runs a function, catching exitSignal to allow END rules to run.
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

	// BEGIN rules
	exited := runPhase(func() {
		for _, rule := range interp.prog.Rules {
			if _, ok := rule.Pattern.(*BeginPattern); ok {
				interp.execAction(rule.Action, interp.global)
			}
		}
	})

	// Input processing (skip if exit was called in BEGIN)
	if !exited {
		exited = runPhase(func() {
			if len(files) == 0 {
				interp.processReader(os.Stdin, "-")
			} else {
				for _, f := range files {
					if f == "-" {
						interp.processReader(os.Stdin, "-")
					} else {
						fh, err := os.Open(f)
						if err != nil {
							fmt.Fprintf(os.Stderr, "cawk: %v\n", err)
							interp.ERRNO = float64(1)
							continue
						}
						interp.processReader(fh, f)
						fh.Close()
					}
				}
			}
		})
	}

	// END rules always run (even after exit), but exit in END exits immediately
	_ = exited
	runPhase(func() {
		for _, rule := range interp.prog.Rules {
			if _, ok := rule.Pattern.(*EndPattern); ok {
				interp.execAction(rule.Action, interp.global)
			}
		}
	})

	interp.closeAll()
	return nil
}

func (interp *Interpreter) processReader(r io.Reader, filename string) {
	interp.FILENAME = filename
	interp.FNR = 0

	// Use a bufio.Reader for record-by-record reading to support plain getline
	br := bufio.NewReader(r)
	interp.currentScanner = br

	rs := interp.RS
	if rs == "" || len(rs) > 1 {
		// Paragraph mode or multi-char/regex RS: need full content
		content, err := io.ReadAll(br)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cawk: read error: %v\n", err)
			interp.currentScanner = nil
			return
		}
		interp.processInput(string(content))
	} else {
		// Record-by-record mode (single char RS, including "\n")
		for {
			line, err := interp.readRecordLine()
			if err == io.EOF {
				break
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "cawk: read error: %v\n", err)
				break
			}
			interp.processRecord(line)
			if interp.nextfiling {
				interp.nextfiling = false
				break
			}
		}
	}
	interp.currentScanner = nil
}

// readRecordLine reads one record from currentScanner using RS.
func (interp *Interpreter) readRecordLine() (string, error) {
	rs := interp.RS
	if rs == "\n" || rs == "" {
		return readLine(interp.currentScanner)
	}
	// Single char RS
	sep := rs[0]
	var buf []byte
	b := make([]byte, 1)
	for {
		n, err := interp.currentScanner.Read(b)
		if n > 0 {
			if b[0] == sep {
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

func (interp *Interpreter) processInput(text string) {
	rs := interp.RS
	if rs == "" {
		// Paragraph mode: split on blank lines
		interp.processParagraphMode(text)
		return
	}

	// Split by RS
	var records []string
	if len(rs) == 1 {
		records = strings.Split(text, rs)
	} else {
		// RS is a regex
		re := mustCompile(rs)
		records = re.Split(text, -1)
	}

	// Remove the one trailing empty record caused by a trailing RS/newline.
	// AWK treats "line\n" as one record, not two.
	if len(records) > 0 && records[len(records)-1] == "" {
		records = records[:len(records)-1]
	}

	for _, record := range records {
		interp.processRecord(record)
		if interp.nextfiling {
			interp.nextfiling = false
			break
		}
	}
}

func (interp *Interpreter) processParagraphMode(text string) {
	// Split on one or more blank lines
	re := mustCompile(`\n\n+`)
	records := re.Split(strings.TrimLeft(text, "\n"), -1)
	for _, record := range records {
		if record == "" {
			continue
		}
		interp.processRecord(record)
		if interp.nextfiling {
			interp.nextfiling = false
			break
		}
	}
}

func (interp *Interpreter) processRecord(record string) {
	interp.NR++
	interp.FNR++
	interp.currentText = record
	interp.splitRecord(record)
	interp.nexting = false

	func() {
		defer func() {
			if r := recover(); r != nil {
				switch r.(type) {
				case nextSignal:
					// continue to next record
				default:
					panic(r)
				}
			}
		}()
		interp.applyRules(record)
	}()
	// Reset nexting flag after applyRules completes
	interp.nexting = false
}

func (interp *Interpreter) applyRules(record string) {
	for _, rule := range interp.prog.Rules {
		// Check next/nextfile/exit signals
		if interp.nexting || interp.nextfiling || interp.exiting {
			break
		}
		if rule.Pattern == nil {
			// No pattern: match every record
			interp.execAction(rule.Action, interp.global)
			continue
		}
		switch p := rule.Pattern.(type) {
		case *BeginPattern, *EndPattern:
			// handled separately
		case *ExprPattern:
			v := interp.evalExpr(p.Expr, interp.global)
			if v.toBool() {
				interp.execAction(rule.Action, interp.global)
			}
		case *RangePattern:
			interp.applyRangePattern(p, rule.Action)
		case *RegexPattern:
			// Plain regex filter: run action if $0 matches the pattern
			if matchRegex(p.Regex, record) {
				interp.execAction(rule.Action, interp.global)
			}
		case *XPattern:
			interp.structuralX(p.Regex, rule.Action)
		case *YPattern:
			interp.structuralY(p.Regex, rule.Action)
		case *GPattern:
			if matchRegex(p.Regex, interp.currentText) {
				interp.execAction(rule.Action, interp.global)
			}
		case *VPattern:
			if !matchRegex(p.Regex, interp.currentText) {
				interp.execAction(rule.Action, interp.global)
			}
		}
	}
}

// Range pattern state
var rangeActive = make(map[*RangePattern]bool)

func (interp *Interpreter) applyRangePattern(p *RangePattern, action []Stmt) {
	active := rangeActive[p]
	if !active {
		// Check if start matches
		v := interp.evalExpr(p.From, interp.global)
		if v.toBool() {
			rangeActive[p] = true
			active = true
		}
	}
	if active {
		interp.execAction(action, interp.global)
		// Check if end matches
		v := interp.evalExpr(p.To, interp.global)
		if v.toBool() {
			rangeActive[p] = false
		}
	}
}

// structuralX: for each match of regex in currentText, run action with $0=match
func (interp *Interpreter) structuralX(regex string, action []Stmt) {
	interp.execStructural(structuralExtract(regex, interp.currentText), action, interp.global)
}

// structuralY: for each gap between matches in currentText, run action
func (interp *Interpreter) structuralY(regex string, action []Stmt) {
	interp.execStructural(structuralGaps(regex, interp.currentText), action, interp.global)
}

// execAction runs a list of statements.
func (interp *Interpreter) execAction(stmts []Stmt, scope *Scope) {
	for _, s := range stmts {
		interp.execStmt(s, scope)
		if interp.breaking || interp.continuing || interp.returning ||
			interp.nexting || interp.nextfiling || interp.exiting {
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
			cond := interp.evalExpr(st.Cond, scope)
			if !cond.toBool() {
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
			if interp.returning || interp.nexting || interp.nextfiling || interp.exiting {
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
			if interp.returning || interp.nexting || interp.nextfiling || interp.exiting {
				return
			}
			cond := interp.evalExpr(st.Cond, scope)
			if !cond.toBool() {
				break
			}
		}

	case *ForStmt:
		if st.Init != nil {
			interp.execStmt(st.Init, scope)
		}
		for {
			if st.Cond != nil {
				cond := interp.evalExpr(st.Cond, scope)
				if !cond.toBool() {
					break
				}
			}
			interp.execAction(st.Body, scope)
			if interp.breaking {
				interp.breaking = false
				break
			}
			if interp.continuing {
				interp.continuing = false
			}
			if interp.returning || interp.nexting || interp.nextfiling || interp.exiting {
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
		// Collect keys to avoid map modification during iteration
		keys := make([]string, 0, len(arr.arr))
		for k := range arr.arr {
			keys = append(keys, k)
		}
		// Don't sort for natural order (like awk)
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
			if interp.returning || interp.nexting || interp.nextfiling || interp.exiting {
				return
			}
		}

	case *DeleteStmt:
		interp.execDelete(st.Expr, scope)

	case *ReturnStmt:
		var v *Value
		if st.Value != nil {
			v = interp.evalExpr(st.Value, scope)
		} else {
			v = &Value{kind: kindUninitialized}
		}
		interp.returning = true
		interp.returnVal = v

	case *BreakStmt:
		interp.breaking = true

	case *ContinueStmt:
		interp.continuing = true

	case *NextStmt:
		interp.nexting = true

	case *NextfileStmt:
		interp.nextfiling = true

	case *ExitStmt:
		code := 0
		if st.Status != nil {
			code = int(interp.evalExpr(st.Status, scope).toNumber())
		}
		panic(exitSignal{code})

	case *BlockStmt:
		interp.execAction(st.Body, scope)

	// Structural regex statements
	case *XStmt:
		interp.execXStmt(st, scope)
	case *YStmt:
		interp.execYStmt(st, scope)
	case *GStmt:
		if matchRegex(st.Regex, interp.currentText) {
			interp.execAction(st.Body, scope)
		}
	case *VStmt:
		if !matchRegex(st.Regex, interp.currentText) {
			interp.execAction(st.Body, scope)
		}
	}
}

func (interp *Interpreter) execXStmt(st *XStmt, scope *Scope) {
	interp.execStructural(structuralExtract(st.Regex, interp.currentText), st.Body, scope)
}

func (interp *Interpreter) execYStmt(st *YStmt, scope *Scope) {
	interp.execStructural(structuralGaps(st.Regex, interp.currentText), st.Body, scope)
}

// execStructural runs action for each span [start,end) in the current text.
// break/continue are consumed locally (scoped to the match loop).
// next/nextfile/exit propagate outward to the record or program level.
func (interp *Interpreter) execStructural(spans [][]int, action []Stmt, scope *Scope) {
	text := interp.currentText
	saved := interp.savedState()
	for _, sp := range spans {
		segment := text[sp[0]:sp[1]]
		if segment == "" {
			continue
		}
		interp.currentText = segment
		interp.splitRecord(segment)
		interp.execAction(action, scope)
		if interp.breaking {
			interp.breaking = false // consumed: exits match loop, not outer rule
			break
		}
		if interp.continuing {
			interp.continuing = false // consumed: next match
			continue
		}
		if interp.returning || interp.nexting || interp.nextfiling || interp.exiting {
			break // propagate upward
		}
	}
	interp.restoreState(saved)
}

type savedStateT struct {
	text   string
	fields []*Value
	nf     float64
}

func (interp *Interpreter) savedState() savedStateT {
	fs := make([]*Value, len(interp.fields))
	copy(fs, interp.fields)
	return savedStateT{interp.currentText, fs, interp.NF}
}

func (interp *Interpreter) restoreState(s savedStateT) {
	interp.currentText = s.text
	interp.fields = s.fields
	interp.NF = s.nf
	if len(interp.fields) > 0 {
		interp.fields[0] = numStr(s.text)
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
		// Unanchored regex match against $0
		if matchRegex(ex.Pattern, interp.currentText) {
			return numVal(1)
		}
		return numVal(0)

	case *IdentExpr:
		return interp.getVar(ex.Name, scope)

	case *FieldExpr:
		idx := int(interp.evalExpr(ex.Index, scope).toNumber())
		return interp.getField(idx)

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
		cond := interp.evalExpr(ex.Cond, scope)
		if cond.toBool() {
			return interp.evalExpr(ex.Then, scope)
		}
		return interp.evalExpr(ex.Else, scope)

	case *ConcatExpr:
		left := interp.evalExpr(ex.Left, scope)
		right := interp.evalExpr(ex.Right, scope)
		ls := left.toStringFmt(interp.OFMT)
		rs := right.toStringFmt(interp.OFMT)
		return strVal(ls + rs)

	case *CallExpr:
		return interp.evalCall(ex, scope)

	case *GetlineExpr:
		return interp.evalGetline(ex, scope)

	case *InExpr:
		arr := interp.getVar(interp.arrayName(ex.Array), scope)
		if arr == nil || !arr.isArray() {
			return numVal(0)
		}
		var key string
		// Handle multi-dimensional via ConcatExpr
		switch k := ex.Key.(type) {
		case *ConcatExpr:
			// Multi-dimensional: evaluate and join with SUBSEP
			parts := interp.flattenConcat(k, scope)
			keys := make([]string, len(parts))
			for i, p := range parts {
				keys[i] = p.toString()
			}
			key = strings.Join(keys, interp.SUBSEP)
		default:
			key = interp.evalExpr(k, scope).toString()
		}
		if _, ok := arr.arr[key]; ok {
			return numVal(1)
		}
		return numVal(0)
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
	switch ex := e.(type) {
	case *IdentExpr:
		return ex.Name
	}
	return ""
}

func (interp *Interpreter) getOrCreateArray(e Expr, scope *Scope) *Value {
	name := interp.arrayName(e)
	v := interp.getVar(name, scope)
	if v == nil {
		v = arrayVal()
		interp.setVar(name, v, scope)
	} else if !v.isArray() && v.kind == kindUninitialized {
		// Convert the existing Value to an array IN PLACE.
		// This preserves reference-sharing for arrays passed as function parameters.
		v.arr = make(map[string]*Value)
	}
	return v
}

func (interp *Interpreter) evalUnary(ex *UnaryExpr, scope *Scope) *Value {
	v := interp.evalExpr(ex.Operand, scope)
	switch ex.Op {
	case "!":
		if v.toBool() {
			return numVal(0)
		}
		return numVal(1)
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
		left := interp.evalExpr(ex.Left, scope)
		if !left.toBool() {
			return numVal(0)
		}
		right := interp.evalExpr(ex.Right, scope)
		if right.toBool() {
			return numVal(1)
		}
		return numVal(0)
	case "||":
		left := interp.evalExpr(ex.Left, scope)
		if left.toBool() {
			return numVal(1)
		}
		right := interp.evalExpr(ex.Right, scope)
		if right.toBool() {
			return numVal(1)
		}
		return numVal(0)
	}

	// Special case: regex match/nomatch — extract pattern without evaluating RHS as bool
	if ex.Op == "~" || ex.Op == "!~" {
		left := interp.evalExpr(ex.Left, scope)
		pattern := interp.extractPattern(ex.Right, scope)
		if ex.Op == "~" {
			return boolVal(matchRegex(pattern, left.toString()))
		}
		return boolVal(!matchRegex(pattern, left.toString()))
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
	// ~ and !~ handled above with extractPattern
	case "in":
		// key in array
		arr := right
		if !arr.isArray() {
			return numVal(0)
		}
		key := left.toString()
		if _, ok := arr.arr[key]; ok {
			return numVal(1)
		}
		return numVal(0)
	}
	return numVal(0)
}

// extractPattern extracts a regex pattern string from an expr.
// For RegexExpr literals like /foo/, returns the pattern directly.
// For other exprs, evaluates and converts to string.
func (interp *Interpreter) extractPattern(e Expr, scope *Scope) string {
	if re, ok := e.(*RegexExpr); ok {
		return re.Pattern
	}
	return interp.evalExpr(e, scope).toString()
}

// gsub/sub also need to handle regex pattern in first arg
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
	var delta float64
	if ex.Op == "++" {
		delta = 1
	} else {
		delta = -1
	}
	newN := n + delta
	newVal := numVal(newN)
	interp.assignExpr(ex.Operand, newVal, scope)
	if ex.Pre {
		return newVal
	}
	return numVal(n)
}

// --- Variable access ---

func (interp *Interpreter) getVar(name string, scope *Scope) *Value {
	// Check special variables
	if v, ok := interp.getSpecialVar(name); ok {
		return v
	}
	// Check local scope
	if scope != nil {
		if v := scope.getLocal(name); v != nil {
			return v
		}
	}
	// Check global
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
	var parts []string
	if len(st.Args) == 0 {
		parts = []string{interp.getField(0).toString()}
	} else {
		for _, arg := range st.Args {
			v := interp.evalExpr(arg, scope)
			if st.Printf {
				// For printf, handled below
				parts = append(parts, v.toStringFmt(interp.OFMT))
			} else {
				parts = append(parts, v.toStringFmt(interp.OFMT))
			}
		}
	}

	var output string
	if st.Printf {
		if len(parts) == 0 {
			panic(runtimeError("Empty sequence"))
		}
		format := ""
		if len(st.Args) > 0 {
			format = interp.evalExpr(st.Args[0], scope).toString()
		}
		args := make([]interface{}, len(st.Args)-1)
		for i, a := range st.Args[1:] {
			args[i] = interp.evalExpr(a, scope)
		}
		output = sprintfAWK(format, args, interp.OFMT)
	} else {
		output = strings.Join(parts, interp.OFS) + interp.ORS
	}

	w := interp.getOutput(st.Redir, scope)
	fmt.Fprint(w, output)
}

// --- Function calls ---

func (interp *Interpreter) evalCall(ex *CallExpr, scope *Scope) *Value {
	// Check user-defined functions first
	if fn, ok := interp.prog.Funcs[ex.Name]; ok {
		return interp.callUserFunc(fn, ex.Args, scope)
	}
	// Built-in functions
	return interp.callBuiltin(ex.Name, ex.Args, scope)
}

func (interp *Interpreter) callUserFunc(fn *FuncDef, args []Expr, scope *Scope) *Value {
	// Create new scope for the function
	fScope := newScope(interp.global)

	// Bind parameters
	for i, param := range fn.Params {
		var val *Value
		if i < len(args) {
			val = interp.evalExpr(args[i], scope)
		} else {
			val = &Value{kind: kindUninitialized}
		}
		fScope.setLocal(param, val)
	}

	// Execute the function body
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

func (interp *Interpreter) evalGetline(ex *GetlineExpr, scope *Scope) *Value {
	// getline from pipe: cmd | getline [var]
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
			interp.splitRecord(line)
			interp.currentText = line
		}
		interp.NR++
		return numVal(1)
	}

	// getline < file [into var]
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
			interp.ERRNO = 1
			return numVal(-1)
		}
		if ex.Var != nil {
			interp.assignExpr(ex.Var, numStr(line), scope)
		} else {
			interp.splitRecord(line)
			interp.currentText = line
		}
		return numVal(1)
	}

	// Plain getline [var] — reads from current input stream
	if interp.currentScanner == nil {
		return numVal(-1)
	}
	line, err := readLine(interp.currentScanner)
	if err == io.EOF {
		return numVal(0)
	}
	if err != nil {
		interp.ERRNO = 1
		return numVal(-1)
	}
	interp.NR++
	interp.FNR++
	if ex.Var != nil {
		interp.assignExpr(ex.Var, numStr(line), scope)
	} else {
		interp.splitRecord(line)
		interp.currentText = line
	}
	return numVal(1)
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

// sprintfAWK formats a string like AWK's printf.
func sprintfAWK(format string, args []interface{}, ofmt string) string {
	return awkSprintf(format, args, ofmt)
}

// --- Output management ---

type outputDest struct {
	w io.Writer
}

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

// --- Pipe and file input ---

type pipeReader struct {
	reader io.Reader
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

type fileReaderEntry struct {
	r io.Reader
}

func (interp *Interpreter) getFileReader(filename string) io.Reader {
	key := "<:" + filename
	if r, ok := interp.files[key]; ok {
		return r.(io.Reader)
	}
	var r io.Reader
	if filename == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(filename)
		if err != nil {
			interp.ERRNO = float64(mapErrno(err))
			return nil
		}
		r = f
		interp.files[key] = f
	}
	interp.files[key] = r
	return r
}

// --- Misc ---

func mapErrno(err error) int {
	if os.IsNotExist(err) {
		return 2
	}
	return 1
}

// Provide random number for rand() — seeded once
var rng = rand.New(rand.NewSource(1))

// sortKeys returns sorted map keys.
func sortKeys(m map[string]*Value) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// formatInt formats n as an integer if possible.
func formatInt(n float64) string {
	if n == math.Trunc(n) && !math.IsInf(n, 0) && math.Abs(n) < 1e15 {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatFloat(n, 'g', 6, 64)
}

// Needed in scope.go
var _ = sort.Strings
