package main

import (
	"fmt"
	"math"
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

// get looks up a variable by name. Returns a pointer to the Value (creating if needed).
func (s *Scope) get(name string) *Value {
	if v, ok := s.vars[name]; ok {
		return v
	}
	if s.parent != nil {
		return s.parent.get(name)
	}
	// Not found: create in this scope
	v := &Value{kind: kindUninitialized}
	s.vars[name] = v
	return v
}

// set sets a variable in the nearest scope that has it, or in this scope.
func (s *Scope) set(name string, val *Value) {
	if _, ok := s.vars[name]; ok {
		s.vars[name] = val
		return
	}
	if s.parent != nil {
		if s.parent.has(name) {
			s.parent.set(name, val)
			return
		}
	}
	s.vars[name] = val
}

// setLocal sets a variable in the current (local) scope only.
func (s *Scope) setLocal(name string, val *Value) {
	s.vars[name] = val
}

// has returns true if this scope (not parents) has the variable.
func (s *Scope) has(name string) bool {
	_, ok := s.vars[name]
	return ok
}

// getLocal gets a variable from this scope only (not parents).
func (s *Scope) getLocal(name string) *Value {
	if v, ok := s.vars[name]; ok {
		return v
	}
	return nil
}

// Interpreter holds the global interpreter state.
type Interpreter struct {
	prog   *Program
	global *Scope

	// Special AWK variables stored directly for fast access
	FS      string  // field separator
	RS      string  // record separator
	OFS     string  // output field separator
	ORS     string  // output record separator
	OFMT    string  // output format for numbers
	CONVFMT string  // conversion format for numbers to strings
	SUBSEP  string  // array subscript separator
	NR      float64 // number of records read
	FNR     float64 // file-relative NR
	NF      float64 // number of fields
	FILENAME string  // current input filename
	ARGC    float64
	ARGV    map[string]*Value

	fields      []*Value // $0, $1, $2, ...
	fieldsDirty bool     // true if $0 needs re-splitting

	// Output
	files   map[string]interface{} // open output files/pipes
	pipes   map[string]interface{} // open pipes

	// Control flow signals
	breaking   bool
	continuing bool
	returning  bool
	returnVal  *Value
	nexting    bool
	nextfiling bool
	exiting    bool
	exitStatus int

	// Structural regex: current text scope (for x/y/g/v)
	currentText string

	// Current input scanner (for plain getline)
	currentScanner interface{ Read([]byte) (int, error) }

	// ERRNO
	ERRNO float64
}

func newInterpreter(prog *Program) *Interpreter {
	interp := &Interpreter{
		prog:    prog,
		global:  newScope(nil),
		FS:      " ",
		RS:      "\n",
		OFS:     " ",
		ORS:     "\n",
		OFMT:    "%.6g",
		CONVFMT: "%.6g",
		SUBSEP:  "\034",
		files:   make(map[string]interface{}),
		pipes:   make(map[string]interface{}),
	}
	interp.fields = []*Value{strVal("")}
	return interp
}

// setVar sets a special variable like NR, FS, etc.
func (interp *Interpreter) setSpecialVar(name string, val *Value) {
	switch name {
	case "FS":
		interp.FS = val.toString()
		// If FS changed, fields may need re-splitting on next record
	case "RS":
		interp.RS = val.toString()
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
	case "NR":
		interp.NR = val.toNumber()
	case "FNR":
		interp.FNR = val.toNumber()
	case "NF":
		// Setting NF truncates or extends fields
		nf := int(val.toNumber())
		interp.setNF(nf)
	case "ERRNO":
		interp.ERRNO = val.toNumber()
	}
}

// getSpecialVar returns the value of a special AWK variable.
func (interp *Interpreter) getSpecialVar(name string) (*Value, bool) {
	switch name {
	case "FS":
		return strVal(interp.FS), true
	case "RS":
		return strVal(interp.RS), true
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
	case "NR":
		return numVal(interp.NR), true
	case "FNR":
		return numVal(interp.FNR), true
	case "NF":
		return numVal(interp.NF), true
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
	case "ERRNO":
		return numVal(interp.ERRNO), true
	}
	return nil, false
}

// setField sets a field value ($n).
func (interp *Interpreter) setField(n int, val *Value) {
	if n < 0 {
		panic(runtimeError("Access to negative field"))
	}
	if n == 0 {
		// Setting $0: re-split fields
		interp.fields[0] = val
		interp.splitRecord(val.toString())
		return
	}
	// Extend fields if necessary
	for len(interp.fields) <= n {
		interp.fields = append(interp.fields, strVal(""))
	}
	interp.fields[n] = val
	// Extend NF if needed
	if float64(n) > interp.NF {
		interp.NF = float64(n)
	}
	// Rebuild $0
	interp.rebuildRecord()
}

// getField gets a field value ($n).
func (interp *Interpreter) getField(n int) *Value {
	if n < 0 {
		panic(runtimeError("Access to negative field"))
	}
	if n == 0 {
		return interp.fields[0]
	}
	if n > int(interp.NF) || n >= len(interp.fields) {
		return strVal("")
	}
	return interp.fields[n]
}

// setNF truncates or extends the field list.
func (interp *Interpreter) setNF(nf int) {
	if nf < 0 {
		nf = 0
	}
	// Truncate
	for int(interp.NF) > nf {
		idx := int(interp.NF)
		if idx < len(interp.fields) {
			interp.fields[idx] = strVal("")
		}
		interp.NF--
	}
	// Extend
	for int(interp.NF) < nf {
		interp.NF++
		for len(interp.fields) <= int(interp.NF) {
			interp.fields = append(interp.fields, strVal(""))
		}
	}
	interp.rebuildRecord()
}

// splitRecord splits $0 into fields according to FS.
func (interp *Interpreter) splitRecord(record string) {
	interp.fields = interp.splitFields(record, interp.FS)
	interp.NF = float64(len(interp.fields) - 1) // fields[0] is $0
}

// splitFields splits a string into fields. Returns slice where [0]=$0, [1]=$1, ...
func (interp *Interpreter) splitFields(s, fs string) []*Value {
	result := []*Value{numStr(s)} // $0

	if s == "" {
		return result
	}

	var parts []string
	if fs == " " {
		// Special: split on runs of whitespace, trim leading/trailing
		parts = strings.Fields(s)
	} else if len(fs) == 1 {
		// Single-character FS: split on that character
		parts = strings.Split(s, fs)
	} else {
		// Regex FS
		re := mustCompileFS(fs)
		parts = re.Split(s, -1)
	}

	for _, p := range parts {
		result = append(result, numStr(p))
	}
	return result
}

// rebuildRecord rebuilds $0 from fields $1..$NF.
func (interp *Interpreter) rebuildRecord() {
	nf := int(interp.NF)
	parts := make([]string, nf)
	for i := 1; i <= nf; i++ {
		if i < len(interp.fields) {
			parts[i-1] = interp.fields[i].toString()
		}
	}
	s := strings.Join(parts, interp.OFS)
	interp.fields[0] = numStr(s)
}

// runtimeError creates a runtime error value for panicking.
type runtimeError string

func (e runtimeError) Error() string { return string(e) }

// formatErrorMsg formats an awk-style error message.
func formatErrorMsg(format string, args ...interface{}) string {
	return fmt.Sprintf(format, args...)
}

// sortedKeys returns the sorted keys of a map (for for-in loops).
func sortedKeys(m map[string]*Value) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// numToInt converts a float64 to int for bitwise operations.
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

// numToUint converts a float64 to uint32 for bitwise operations.
func numToUint(v float64) uint32 {
	if v < 0 {
		return uint32(int32(v))
	}
	if v > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}
