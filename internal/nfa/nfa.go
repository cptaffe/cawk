// Package nfa provides a Thompson NFA executor that runs multiple regexp
// programs in parallel, anchored at the start of a byte slice, and reports
// which program's InstMatch was reached first (leftmost-first / Perl priority
// semantics — the same "first rule wins" order as AWK).
//
// This is a lightly modified copy of the relevant parts of the Go standard
// library's regexp/exec.go and regexp/regexp.go (BSD licence, Google Inc.).
// The two intentional changes are:
//
//  1. machine.re *Regexp  →  machine.prog *syntax.Prog
//     The Regexp wrapper is not needed; we work with the compiled Prog directly.
//
//  2. In step(), the InstMatch case stores t.inst.Arg in machine.matchedRule.
//     InstMatch.Arg is unused by the standard library.  We repurpose it to
//     carry the rule index set by the caller when building the combined Prog.
//     Everything else in that case is unchanged.
package nfa

import (
	"regexp/syntax"
	"unicode/utf8"
)

const endOfText rune = -1

// --- input ---------------------------------------------------------------

// input abstracts over a byte slice for the NFA executor.
// (The standard library's input interface also supports strings and
// RuneReaders and carries *Regexp for prefix optimisations; we drop all of
// that because we only need bytes and never do prefix scanning.)
type input interface {
	step(pos int) (r rune, width int)
	context(pos int) lazyFlag
}

type inputBytes struct{ str []byte }

func (i *inputBytes) step(pos int) (rune, int) {
	if pos < len(i.str) {
		c := i.str[pos]
		if c < utf8.RuneSelf {
			return rune(c), 1
		}
		return utf8.DecodeRune(i.str[pos:])
	}
	return endOfText, 0
}

func (i *inputBytes) context(pos int) lazyFlag {
	r1, r2 := endOfText, endOfText
	if uint(pos-1) < uint(len(i.str)) {
		r1 = rune(i.str[pos-1])
		if r1 >= utf8.RuneSelf {
			r1, _ = utf8.DecodeLastRune(i.str[:pos])
		}
	}
	if uint(pos) < uint(len(i.str)) {
		r2 = rune(i.str[pos])
		if r2 >= utf8.RuneSelf {
			r2, _ = utf8.DecodeRune(i.str[pos:])
		}
	}
	return newLazyFlag(r1, r2)
}

// --- lazyFlag ------------------------------------------------------------
// Copied verbatim from regexp/exec.go.

type lazyFlag uint64

func newLazyFlag(r1, r2 rune) lazyFlag {
	return lazyFlag(uint64(r1)<<32 | uint64(uint32(r2)))
}

func (f lazyFlag) match(op syntax.EmptyOp) bool {
	if op == 0 {
		return true
	}
	r1 := rune(f >> 32)
	if op&syntax.EmptyBeginLine != 0 {
		if r1 != '\n' && r1 >= 0 {
			return false
		}
		op &^= syntax.EmptyBeginLine
	}
	if op&syntax.EmptyBeginText != 0 {
		if r1 >= 0 {
			return false
		}
		op &^= syntax.EmptyBeginText
	}
	if op == 0 {
		return true
	}
	r2 := rune(f)
	if op&syntax.EmptyEndLine != 0 {
		if r2 != '\n' && r2 >= 0 {
			return false
		}
		op &^= syntax.EmptyEndLine
	}
	if op&syntax.EmptyEndText != 0 {
		if r2 >= 0 {
			return false
		}
		op &^= syntax.EmptyEndText
	}
	if op == 0 {
		return true
	}
	if syntax.IsWordChar(r1) != syntax.IsWordChar(r2) {
		op &^= syntax.EmptyWordBoundary
	} else {
		op &^= syntax.EmptyNoWordBoundary
	}
	return op == 0
}

// --- queue / thread / machine --------------------------------------------
// Copied from regexp/exec.go with two changes noted inline.

type queue struct {
	sparse []uint32
	dense  []entry
}

type entry struct {
	pc uint32
	t  *thread
}

type thread struct {
	inst *syntax.Inst
	cap  []int
}

// machine holds all state during an NFA simulation.
//
// Change 1: re *Regexp replaced by prog *syntax.Prog.
// Change 2: matchedRule int added (populated from InstMatch.Arg in step).
type machine struct {
	prog        *syntax.Prog // ← was: re *Regexp; p *syntax.Prog
	q0, q1      queue
	pool        []*thread
	matched     bool
	matchedRule int   // ← new: which rule's InstMatch fired (stored in Inst.Arg)
	matchcap    []int // submatch indices: [start0,end0, start1,end1, …]
}

func newMachine(prog *syntax.Prog) *machine {
	n := len(prog.Inst)
	m := &machine{
		prog:        prog,
		matchedRule: -1,
		matchcap:    make([]int, prog.NumCap),
		q0:          queue{sparse: make([]uint32, n), dense: make([]entry, 0, n)},
		q1:          queue{sparse: make([]uint32, n), dense: make([]entry, 0, n)},
	}
	return m
}

func (m *machine) alloc(i *syntax.Inst) *thread {
	var t *thread
	if n := len(m.pool); n > 0 {
		t = m.pool[n-1]
		m.pool = m.pool[:n-1]
	} else {
		t = new(thread)
		t.cap = make([]int, len(m.matchcap), cap(m.matchcap))
	}
	t.inst = i
	return t
}

func (m *machine) clear(q *queue) {
	for _, d := range q.dense {
		if d.t != nil {
			m.pool = append(m.pool, d.t)
		}
	}
	q.dense = q.dense[:0]
}

// matchAnchored runs the NFA anchored at position 0 in the slice.
// It returns true if any rule matched; results are in m.matched,
// m.matchedRule, and m.matchcap.
func (m *machine) matchAnchored(in input) bool {
	startCond := m.prog.StartCond()
	if startCond == ^syntax.EmptyOp(0) {
		return false
	}

	m.matched = false
	m.matchedRule = -1
	for i := range m.matchcap {
		m.matchcap[i] = -1
	}

	runq, nextq := &m.q0, &m.q1
	r, width := in.step(0)
	r1, width1 := endOfText, 0
	if r != endOfText {
		r1, width1 = in.step(width)
	}

	var flag lazyFlag
	flag = newLazyFlag(-1, r) // beginning of text

	pos := 0
	started := false
	for {
		if len(runq.dense) == 0 {
			if m.matched || started {
				// Either we found a match and exhausted all threads,
				// or we started but every thread died — done.
				break
			}
		}

		// Add the start state exactly once (anchored at pos=0).
		if !started {
			started = true
			if len(m.matchcap) > 0 {
				m.matchcap[0] = pos
			}
			m.add(runq, uint32(m.prog.Start), pos, m.matchcap, &flag, nil)
		}

		flag = newLazyFlag(r, r1)
		m.step(runq, nextq, pos, pos+width, r, &flag)
		if width == 0 {
			break
		}
		pos += width
		r, width = r1, width1
		if r != endOfText {
			r1, width1 = in.step(pos + width)
		}
		runq, nextq = nextq, runq
	}
	m.clear(nextq)
	return m.matched
}

// step executes one step of the NFA: process runq, emit into nextq.
// Identical to regexp/exec.go except the InstMatch case, which is annotated.
func (m *machine) step(runq, nextq *queue, pos, nextPos int, c rune, nextCond *lazyFlag) {
	for j := 0; j < len(runq.dense); j++ {
		d := &runq.dense[j]
		t := d.t
		if t == nil {
			continue
		}
		i := t.inst
		add := false
		switch i.Op {
		default:
			panic("bad inst")

		case syntax.InstMatch:
			if len(t.cap) > 0 {
				t.cap[1] = pos
				copy(m.matchcap, t.cap)
			}
			// ← Change 2: record which rule matched via Inst.Arg.
			m.matchedRule = int(i.Arg)
			m.matched = true
			// First-match (Perl) mode: kill all lower-priority threads so
			// that "first rule wins" holds even if a later rule would match
			// more characters.
			for _, d2 := range runq.dense[j+1:] {
				if d2.t != nil {
					m.pool = append(m.pool, d2.t)
				}
			}
			runq.dense = runq.dense[:0]

		case syntax.InstRune:
			add = i.MatchRune(c)
		case syntax.InstRune1:
			add = c == i.Rune[0]
		case syntax.InstRuneAny:
			add = true
		case syntax.InstRuneAnyNotNL:
			add = c != '\n'
		}
		if add {
			t = m.add(nextq, i.Out, nextPos, t.cap, nextCond, t)
		}
		if t != nil {
			m.pool = append(m.pool, t)
		}
	}
	runq.dense = runq.dense[:0]
}

// add adds an entry to q for pc, following ε-transitions.
// Copied verbatim from regexp/exec.go.
func (m *machine) add(q *queue, pc uint32, pos int, cap []int, cond *lazyFlag, t *thread) *thread {
Again:
	if pc == 0 {
		return t
	}
	if j := q.sparse[pc]; j < uint32(len(q.dense)) && q.dense[j].pc == pc {
		return t
	}

	j := uint32(len(q.dense))
	q.dense = q.dense[:j+1]
	d := &q.dense[j]
	d.t = nil
	d.pc = pc
	q.sparse[pc] = j

	i := &m.prog.Inst[pc]
	switch i.Op {
	default:
		panic("unhandled")
	case syntax.InstFail:
		// nothing
	case syntax.InstAlt, syntax.InstAltMatch:
		t = m.add(q, i.Out, pos, cap, cond, t)
		pc = i.Arg
		goto Again
	case syntax.InstEmptyWidth:
		if cond.match(syntax.EmptyOp(i.Arg)) {
			pc = i.Out
			goto Again
		}
	case syntax.InstNop:
		pc = i.Out
		goto Again
	case syntax.InstCapture:
		if int(i.Arg) < len(cap) {
			opos := cap[i.Arg]
			cap[i.Arg] = pos
			m.add(q, i.Out, pos, cap, cond, nil)
			cap[i.Arg] = opos
		} else {
			pc = i.Out
			goto Again
		}
	case syntax.InstMatch, syntax.InstRune, syntax.InstRune1, syntax.InstRuneAny, syntax.InstRuneAnyNotNL:
		if t == nil {
			t = m.alloc(i)
		} else {
			t.inst = i
		}
		if len(cap) > 0 && &t.cap[0] != &cap[0] {
			copy(t.cap, cap)
		}
		d.t = t
		t = nil
	}
	return t
}

// --- Public API ----------------------------------------------------------

// Match runs the combined program anchored at byte offset 0 in b.
//
// On success it returns:
//
//	rule  — the value of InstMatch.Arg for the winning rule (caller-defined)
//	end   — the byte offset one past the last consumed byte
//	cap   — submatch indices: [start0,end0, start1,end1, …] (byte offsets in b)
//	ok    — true
//
// The cap slice is only valid until the next call to Match (it is backed by
// the machine's internal buffer).  Copy it if you need to keep it.
func Match(prog *syntax.Prog, b []byte) (rule, end int, cap []int, ok bool) {
	m := newMachine(prog)
	in := &inputBytes{str: b}
	if !m.matchAnchored(in) {
		return -1, 0, nil, false
	}
	return m.matchedRule, m.matchcap[1], m.matchcap, true
}
