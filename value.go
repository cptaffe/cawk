package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ValueKind tracks how a value was created, for AWK comparison semantics.
type ValueKind int

const (
	kindUninitialized ValueKind = iota
	kindString                  // created from a string literal
	kindNumber                  // created from a numeric literal or operation
	kindNumString               // string from input that looks like a number
)

// Value is the universal AWK runtime value.
type Value struct {
	kind   ValueKind
	numVal float64
	strVal string
	arr    map[string]*Value // non-nil if array
}

var uninitialized = &Value{kind: kindUninitialized}

func numVal(n float64) *Value {
	return &Value{kind: kindNumber, numVal: n}
}

func strVal(s string) *Value {
	return &Value{kind: kindString, strVal: s}
}

// numStr creates a value from input (field splitting, getline) — it's a string
// but if it looks numeric, numeric comparison will be used.
func numStr(s string) *Value {
	return &Value{kind: kindNumString, strVal: s}
}

func arrayVal() *Value {
	return &Value{arr: make(map[string]*Value)}
}

func (v *Value) isArray() bool {
	return v.arr != nil
}

// toNumber converts value to float64.
func (v *Value) toNumber() float64 {
	switch v.kind {
	case kindNumber:
		return v.numVal
	case kindString, kindNumString:
		return parseNumber(v.strVal)
	case kindUninitialized:
		return 0
	}
	return 0
}

// parseNumber parses a string to number (like awk does).
func parseNumber(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	// hex
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		if n, err := strconv.ParseInt(s[2:], 16, 64); err == nil {
			return float64(n)
		}
		if n, err := strconv.ParseUint(s[2:], 16, 64); err == nil {
			return float64(n)
		}
	}
	// Leading 0 but not 0x: check for octal (awk only does this for integer constants, not strings)
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// toString converts value to string using OFMT for numbers (caller provides ofmt).
func (v *Value) toString() string {
	return v.toStringFmt("%.6g")
}

// toStringFmt converts value to string with a given format for floats.
func (v *Value) toStringFmt(ofmt string) string {
	switch v.kind {
	case kindString, kindNumString:
		return v.strVal
	case kindNumber:
		return formatNumber(v.numVal, ofmt)
	case kindUninitialized:
		return ""
	}
	return ""
}

// formatNumber formats a number as awk does: integers without decimal point.
func formatNumber(n float64, ofmt string) string {
	if math.IsInf(n, 1) {
		return "inf"
	}
	if math.IsInf(n, -1) {
		return "-inf"
	}
	if math.IsNaN(n) {
		return "nan"
	}
	if n == math.Trunc(n) && math.Abs(n) < 1e15 {
		return strconv.FormatInt(int64(n), 10)
	}
	return fmt.Sprintf(ofmt, n)
}

// toBool returns the boolean value of v.
func (v *Value) toBool() bool {
	switch v.kind {
	case kindNumber:
		return v.numVal != 0
	case kindString, kindNumString:
		return v.strVal != ""
	case kindUninitialized:
		return false
	}
	return false
}

// isNumeric returns true if the value should be compared numerically.
func (v *Value) isNumeric() bool {
	return v.kind == kindNumber
}

// isNumericString returns true if this is a string from input that looks numeric.
func (v *Value) isNumericString() bool {
	if v.kind != kindNumString {
		return false
	}
	s := strings.TrimSpace(v.strVal)
	if s == "" {
		return false
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// compare compares two values according to AWK semantics.
// Returns -1, 0, or 1.
func compare(a, b *Value) int {
	// If both are numeric or both are numeric strings from input, compare numerically.
	aNum := a.kind == kindNumber || (a.kind == kindNumString && a.isNumericString())
	bNum := b.kind == kindNumber || (b.kind == kindNumString && b.isNumericString())

	if aNum && bNum {
		an, bn := a.toNumber(), b.toNumber()
		if an < bn {
			return -1
		} else if an > bn {
			return 1
		}
		return 0
	}

	// String comparison
	as, bs := a.toString(), b.toString()
	if as < bs {
		return -1
	} else if as > bs {
		return 1
	}
	return 0
}

// arrayKey returns the string key for array indexing.
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

// clone makes a shallow copy of a value (for passing to functions by value).
func (v *Value) clone() *Value {
	return &Value{kind: v.kind, numVal: v.numVal, strVal: v.strVal}
}

// setNumber sets a value to a number (in-place, for variables).
func (v *Value) setNumber(n float64) {
	v.kind = kindNumber
	v.numVal = n
	v.strVal = ""
}

// setString sets a value to a string.
func (v *Value) setString(s string) {
	v.kind = kindString
	v.strVal = s
	v.numVal = 0
}

// setNumStr sets a value to a string-from-input.
func (v *Value) setNumStr(s string) {
	v.kind = kindNumString
	v.strVal = s
	v.numVal = 0
}
