package main

import (
	"bytes"
	"fmt"
	"regexp/syntax"

	"cawk/internal/nfa"
)

// ruleEntry holds precompiled info for one rule in the combined NFA.
type ruleEntry struct {
	rule        *Rule
	capOffset   int      // first inner capture's group index in the combined prog
	innerCount  int      // number of inner capturing groups
	isBareBlock bool     // rule.Regex == ""; compiled as [^\n]*\n
	capNames    []string // capNames[k] = name of inner group k (1-indexed; [0]="")
}

// combinedNFA holds the single stitched *syntax.Prog for all main rules
// plus per-rule metadata needed to interpret the match result.
type combinedNFA struct {
	prog    *syntax.Prog // nil if no rules
	entries []ruleEntry
}

// stripCapNames walks the syntax tree, records every named capture's name
// into names[Cap] (growing the slice as needed), and clears re.Name so
// that the compiled program uses only anonymous capturing groups.
// The name→index mapping lives entirely in names; no string ops at
// match time.
func stripCapNames(re *syntax.Regexp, names []string) []string {
	if re.Op == syntax.OpCapture {
		for len(names) <= re.Cap {
			names = append(names, "")
		}
		names[re.Cap] = re.Name
		re.Name = ""
	}
	for _, sub := range re.Sub {
		names = stripCapNames(sub, names)
	}
	return names
}

// offsetCaps adds delta to every explicit capture index (Cap > 0) in the
// syntax tree.  This shifts a rule's capture groups so they don't overlap
// with the groups of earlier rules in the combined program.
// Cap == 0 is the implicit whole-match group managed by the machine itself;
// it is left unchanged so all rules share a single group-0 slot.
func offsetCaps(re *syntax.Regexp, delta int) {
	if re.Op == syntax.OpCapture && re.Cap > 0 {
		re.Cap += delta
	}
	for _, sub := range re.Sub {
		offsetCaps(sub, delta)
	}
}

// buildCombinedNFA compiles all main rules into a single *syntax.Prog.
//
// Each rule's regex is parsed, stripped of named captures (names collected
// into ruleEntry.capNames), and compiled.  The compiled instruction arrays
// are stitched together with an InstAlt chain at the front so the NFA tries
// rules in source order — Go's default leftmost-first (Perl) semantics give
// "first rule wins" for free.
//
// Each rule's InstMatch.Arg is set to the rule's index in entries so that
// nfa.Match can report which rule fired without any subgroup interrogation.
//
// Capture-group indices are offset per rule so they don't collide inside the
// combined matchcap slice.
func buildCombinedNFA(rules []*Rule) *combinedNFA {
	var entries []ruleEntry
	var progs []*syntax.Prog
	totalCaps := 0 // running sum of inner capture counts across rules

	for i, rule := range rules {
		var prog *syntax.Prog
		var capNames []string
		var nInner int

		if rule.Regex == "" {
			// Bare block: [^\n]*\n — no inner captures.
			// $0 will be the matched text minus the trailing \n (AWK compat).
			tree, err := syntax.Parse(`[^\n]*\n`, syntax.Perl|syntax.DotNL)
			if err != nil {
				panic(runtimeError(fmt.Sprintf("bare block compile: %v", err)))
			}
			prog, err = syntax.Compile(tree.Simplify())
			if err != nil {
				panic(runtimeError(fmt.Sprintf("bare block compile: %v", err)))
			}
			nInner = 0
		} else {
			tree, err := syntax.Parse(rule.Regex, syntax.Perl|syntax.DotNL)
			if err != nil {
				panic(runtimeError(fmt.Sprintf("bad regex %q: %v", rule.Regex, err)))
			}
			// Collect named-group info and strip names from the tree.
			capNames = stripCapNames(tree, []string{""}) // [0] placeholder for whole-match
			nInner = tree.MaxCap()
			// Shift this rule's capture indices so they don't overlap with
			// groups from earlier rules.
			if totalCaps > 0 {
				offsetCaps(tree, totalCaps)
			}
			prog, err = syntax.Compile(tree.Simplify())
			if err != nil {
				panic(runtimeError(fmt.Sprintf("bad regex %q: %v", rule.Regex, err)))
			}
		}

		// Tag every InstMatch in this rule's program with the rule index.
		// Inst.Arg is unused for InstMatch in the standard library; we
		// repurpose it so nfa.Match can identify which rule won.
		for j := range prog.Inst {
			if prog.Inst[j].Op == syntax.InstMatch {
				prog.Inst[j].Arg = uint32(i)
			}
		}

		entries = append(entries, ruleEntry{
			rule:        rule,
			capOffset:   totalCaps,
			innerCount:  nInner,
			isBareBlock: rule.Regex == "",
			capNames:    capNames,
		})
		progs = append(progs, prog)
		totalCaps += nInner
	}

	if len(progs) == 0 {
		return &combinedNFA{entries: entries}
	}

	// Stitch individual programs into one, prepending an InstAlt chain.
	combined := stitch(progs)
	// The combined NumCap must cover the whole-match slot (2) plus all
	// inner capture groups from all rules.
	combined.NumCap = 2 + totalCaps*2

	return &combinedNFA{prog: combined, entries: entries}
}

// stitch concatenates the instruction arrays of progs into a single Prog,
// adjusting all branch-target offsets (Out and, for Alt/AltMatch, Arg).
// It prepends an InstAlt chain so the NFA tries prog[0] before prog[1], etc.
//
// Index 0 of the combined Prog is always InstFail.  Both syntax.Compile and
// our nfa.add treat pc==0 as "fail/skip", so reserving index 0 for the fail
// instruction is mandatory.  The alt-chain occupies indices 1..nAlts; rule
// sub-programs follow from index nAlts+1 onward.
//
// Only Out and Alt.Arg are instruction indices that need adjustment.
// InstCapture.Arg is a capture-group index (not a branch target) — it must
// NOT be adjusted here; capture renumbering is done before compilation via
// offsetCaps.  InstEmptyWidth.Arg is an EmptyOp bitmask — also not adjusted.
func stitch(progs []*syntax.Prog) *syntax.Prog {
	out := &syntax.Prog{}

	// index 0 = InstFail (invariant: pc==0 means "fail", used by nfa.add).
	// indices 1..nAlts = the alt chain (nAlts = len(progs)-1).
	nAlts := len(progs) - 1
	out.Inst = make([]syntax.Inst, 1+nAlts) // [fail, alt0, alt1, …]
	out.Inst[0] = syntax.Inst{Op: syntax.InstFail}

	// Append each sub-program's instructions, recording the adjusted start.
	starts := make([]uint32, len(progs))
	for pi, p := range progs {
		base := uint32(len(out.Inst))
		starts[pi] = base + uint32(p.Start)

		for _, inst := range p.Inst {
			adj := inst
			// Adjust Out (next instruction) for all ops that use it.
			switch inst.Op {
			case syntax.InstMatch, syntax.InstFail:
				// Out is not a branch target for these.
			default:
				adj.Out += base
			}
			// Adjust Arg only for Alt/AltMatch (it's a branch target there).
			if inst.Op == syntax.InstAlt || inst.Op == syntax.InstAltMatch {
				adj.Arg += base
			}
			out.Inst = append(out.Inst, adj)
		}
	}

	// Fill the alt chain at indices 1..nAlts.
	// alt[i] → starts[i]  (Out) and alt[i+1] or starts[last]  (Arg).
	for i := 0; i < nAlts; i++ {
		var next uint32
		if i == nAlts-1 {
			next = starts[len(progs)-1]
		} else {
			next = uint32(i + 2) // next alt instruction (1-based + 1)
		}
		out.Inst[1+i] = syntax.Inst{
			Op:  syntax.InstAlt,
			Out: starts[i],
			Arg: next,
		}
	}

	if nAlts == 0 {
		out.Start = int(starts[0]) // single rule — jump straight to it
	} else {
		out.Start = 1 // first alt instruction
	}
	return out
}

// buildMatchState constructs a MatchState from a combined-NFA match.
// matchedText is the full matched string (byte-slice cast); cap is the
// matchcap from nfa.Match.
func (e *ruleEntry) buildMatchState(matchedText string, cap []int) MatchState {
	text := matchedText
	if e.isBareBlock && len(text) > 0 && text[len(text)-1] == '\n' {
		text = text[:len(text)-1] // AWK compat: $0 excludes trailing \n
	}
	ms := MatchState{Text: text}

	for k := 1; k <= e.innerCount; k++ {
		gi := e.capOffset + k // group index in the combined prog
		idx := gi * 2
		if idx+1 >= len(cap) {
			break
		}
		lo, hi := cap[idx], cap[idx+1]
		// Populate Groups for $k (always, even if unmatched → empty string).
		var val string
		if lo >= 0 {
			val = matchedText[lo:hi]
		}
		ms.Groups = append(ms.Groups, val)
		// Populate NamedGroups for $name (only when the group actually matched).
		if lo >= 0 && k < len(e.capNames) && e.capNames[k] != "" {
			if ms.NamedGroups == nil {
				ms.NamedGroups = map[string]string{}
			}
			ms.NamedGroups[e.capNames[k]] = val
		}
	}
	return ms
}

// tryAt attempts to match at buf[pos:] using a single NFA pass.
// Returns (advance, entry, matchState, true) on success.
func (cn *combinedNFA) tryAt(buf []byte, pos int, eof bool) (int, *ruleEntry, MatchState, bool) {
	slice := buf[pos:]

	if cn.prog != nil {
		ruleIdx, end, cap, ok := nfa.Match(cn.prog, slice)
		if ok {
			e := &cn.entries[ruleIdx]
			ms := e.buildMatchState(string(slice[:end]), cap)
			adv := end
			if adv == 0 {
				adv = 1
			}
			return adv, e, ms, true
		}
	}

	// EOF pass: bare blocks fire for the last line when there's no trailing \n.
	// The [^\n]*\n pattern can't match without a newline; handle it explicitly.
	if eof && len(slice) > 0 && bytes.IndexByte(slice, '\n') < 0 {
		for i := range cn.entries {
			e := &cn.entries[i]
			if e.isBareBlock {
				return len(slice), e, MatchState{Text: string(slice)}, true
			}
		}
	}

	return 0, nil, MatchState{}, false
}
