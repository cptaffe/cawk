package main

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// Scope holds a set of variables. Multiple scopes are chained (function locals → global).
type Scope struct {
	vars   map[string]*Value
	parent *Scope
}

func newScope(parent *Scope) *Scope {
	return &Scope{vars: make(map[string]*Value), parent: parent}
}

func (s *Scope) get(name string) *Value {
	if v, ok := s.vars[name]; ok {
		return v
	}
	if s.parent != nil {
		return s.parent.get(name)
	}
	v := &Value{kind: kindUninitialized}
	s.vars[name] = v
	return v
}

func (s *Scope) set(name string, val *Value) {
	if _, ok := s.vars[name]; ok {
		s.vars[name] = val
		return
	}
	if s.parent != nil && s.parent.has(name) {
		s.parent.set(name, val)
		return
	}
	s.vars[name] = val
}

func (s *Scope) setLocal(name string, val *Value) {
	s.vars[name] = val
}

func (s *Scope) has(name string) bool {
	_, ok := s.vars[name]
	return ok
}

func (s *Scope) getLocal(name string) *Value {
	if v, ok := s.vars[name]; ok {
		return v
	}
	return nil
}

// MatchState holds the current match context for one level of x-expression.
// $0 = Text, $1/$2/... = Groups[0]/Groups[1]/..., $name = NamedGroups[name].
type MatchState struct {
	Text        string            // $0 — full matched text
	Groups      []string          // $1, $2, ... stored 0-indexed: Groups[0]=$1
	NamedGroups map[string]string // $name — named capture groups
}

// matchStateFromRegex builds a MatchState from a successful regex match.
// re is the compiled regex (used to extract SubexpNames).
// text is the full string that was matched against.
// m is the result of FindStringSubmatchIndex (or an element of FindAllStringSubmatchIndex).
func matchStateFromRegex(re *regexp.Regexp, text string, m []int) MatchState {
	ms := MatchState{
		Text:        text[m[0]:m[1]],
		NamedGroups: make(map[string]string),
	}
	names := re.SubexpNames()
	for i := 1; i < len(m)/2; i++ {
		s, e := m[2*i], m[2*i+1]
		val := ""
		if s >= 0 {
			val = text[s:e]
		}
		ms.Groups = append(ms.Groups, val)
		if i < len(names) && names[i] != "" {
			ms.NamedGroups[names[i]] = val
		}
	}
	return ms
}

// Interpreter holds the global interpreter state.
type Interpreter struct {
	prog   *Program
	global *Scope

	// Match state stack. Each /re/ { } (top-level or nested) pushes a MatchState
	// on entry and pops on exit. $0/$n/$name read from the top of the stack.
	matchStack []MatchState

	// Output formatting variables.
	OFS     string // output field separator (for print a, b)
	ORS     string // output record separator (appended by print)
	OFMT    string // output format for numbers
	CONVFMT string // conversion format for numbers to strings
	SUBSEP  string // array subscript separator

	FILENAME string
	ARGC     float64
	ARGV     map[string]*Value

	// Output
	files map[string]interface{}
	pipes map[string]interface{}

	// Control flow signals
	breaking   bool
	continuing bool
	returning  bool
	returnVal  *Value
	exiting    bool
	exitStatus int
}

func newInterpreter(prog *Program) *Interpreter {
	interp := &Interpreter{
		prog:    prog,
		global:  newScope(nil),
		OFS:     " ",
		ORS:     "\n",
		OFMT:    "%.6g",
		CONVFMT: "%.6g",
		SUBSEP:  "\034",
		files:   make(map[string]interface{}),
		pipes:   make(map[string]interface{}),
	}
	return interp
}

// pushMatch pushes a new MatchState onto the match stack.
func (interp *Interpreter) pushMatch(ms MatchState) {
	interp.matchStack = append(interp.matchStack, ms)
}

// popMatch pops the top MatchState from the match stack.
func (interp *Interpreter) popMatch() {
	if len(interp.matchStack) > 0 {
		interp.matchStack = interp.matchStack[:len(interp.matchStack)-1]
	}
}

// currentMatch returns the top MatchState, or a zero MatchState if the stack is empty.
// An empty stack occurs in BEGIN/END blocks and before any rule fires.
func (interp *Interpreter) currentMatch() MatchState {
	if len(interp.matchStack) == 0 {
		return MatchState{}
	}
	return interp.matchStack[len(interp.matchStack)-1]
}

// getField returns $n. $0 = full match (Text); $1,$2,... = capture groups (Groups).
func (interp *Interpreter) getField(n int) *Value {
	if n < 0 {
		panic(runtimeError("access to negative field"))
	}
	ms := interp.currentMatch()
	if n == 0 {
		return strVal(ms.Text)
	}
	if n >= 1 && n-1 < len(ms.Groups) {
		return strVal(ms.Groups[n-1])
	}
	return strVal("")
}

// setField sets $n. Mutates the top of the match stack.
// $0 sets Text; $n (n≥1) sets Groups[n-1] (growing the slice if needed).
func (interp *Interpreter) setField(n int, val *Value) {
	if len(interp.matchStack) == 0 {
		return
	}
	top := &interp.matchStack[len(interp.matchStack)-1]
	s := val.toString()
	if n == 0 {
		top.Text = s
	} else {
		for len(top.Groups) < n {
			top.Groups = append(top.Groups, "")
		}
		top.Groups[n-1] = s
	}
}

// Special variable handling.

func (interp *Interpreter) setSpecialVar(name string, val *Value) {
	switch name {
	case "OFS":
		interp.OFS = val.toString()
	case "ORS":
		interp.ORS = val.toString()
	case "OFMT":
		interp.OFMT = val.toString()
	case "CONVFMT":
		interp.CONVFMT = val.toString()
	case "SUBSEP":
		interp.SUBSEP = val.toString()
	}
}

func (interp *Interpreter) getSpecialVar(name string) (*Value, bool) {
	switch name {
	case "OFS":
		return strVal(interp.OFS), true
	case "ORS":
		return strVal(interp.ORS), true
	case "OFMT":
		return strVal(interp.OFMT), true
	case "CONVFMT":
		return strVal(interp.CONVFMT), true
	case "SUBSEP":
		return strVal(interp.SUBSEP), true
	case "FILENAME":
		return strVal(interp.FILENAME), true
	case "ARGC":
		return numVal(interp.ARGC), true
	case "ARGV":
		if v := interp.global.getLocal("ARGV"); v != nil {
			return v, true
		}
		v := arrayVal()
		interp.global.setLocal("ARGV", v)
		return v, true
	case "ENVIRON":
		return interp.global.get("ENVIRON"), true
	}
	return nil, false
}

// runtimeError is the type used for recoverable runtime panics.
type runtimeError string

func (e runtimeError) Error() string { return string(e) }

func formatErrorMsg(format string, args ...interface{}) string {
	return fmt.Sprintf(format, args...)
}

func sortedKeys(m map[string]*Value) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func numToInt(v float64) int64 {
	if v >= 0 {
		if v > math.MaxInt64 {
			return math.MaxInt64
		}
		return int64(v)
	}
	if v < math.MinInt64 {
		return math.MinInt64
	}
	return int64(v)
}

func numToUint(v float64) uint32 {
	if v < 0 {
		return uint32(int32(v))
	}
	if v > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}

// arrayKey builds the multi-dimensional array subscript.
func arrayKey(vals []*Value, subsep string) string {
	if len(vals) == 1 {
		return vals[0].toString()
	}
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = v.toString()
	}
	return strings.Join(parts, subsep)
}
