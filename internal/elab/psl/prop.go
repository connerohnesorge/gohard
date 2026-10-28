package psl

import (
	"slices"
	"strconv"
	"strings"
)

// A prop is a PSL property as an obligation on the rest of the run.
// Properties are checked by formula progression: prog(p, a) is the
// obligation the ticks after a must meet for p to hold from the tick
// of a on. True and false are the decided obligations; And and Or are
// canonical (flattened, sorted by key, deduplicated), so the
// obligations of `always` stay a bounded set.
type prop struct {
	k       pKind
	b       int // pBool, pAbort, pNextEvent: the boolean
	n       int // pNext: ticks to wait; pNextEvent: ticks of b to wait
	strong  bool
	incl    bool // until_, before_
	overlap bool // |-> (true) or |=> (false)
	l, r    *prop
	args    []*prop // pAnd, pOr
	seq     *sere   // pNever: the sequence; pSuffix, pSere: the start
	ss      []*sere // running sequence states
	key     string
}

type pKind uint8

const (
	pTrue pKind = iota
	pFalse
	pBool
	pNot
	pAnd
	pOr
	pAlways
	pNever
	pEventually // eventually!
	pNext
	pUntil
	pBefore
	pSuffix // ss |-> r or ss |=> r
	pSere   // a sequence used as a property
	pAbort  // l abort b
	// pNextEvent is next_event(b)[n](l): l at the n-th tick on which
	// b holds, counting the current one (#66).
	pNextEvent
)

var (
	pT = &prop{k: pTrue, key: "T"}
	pF = &prop{k: pFalse, key: "F"}
)

func boolP(b int) *prop {
	return &prop{k: pBool, b: b, key: "b" + strconv.Itoa(b)}
}

func notP(p *prop) *prop {
	switch p.k {
	case pTrue:
		return pF
	case pFalse:
		return pT
	case pNot:
		return p.l
	}
	return &prop{k: pNot, l: p, key: "!" + p.key}
}

func junction(k pKind, xs []*prop) *prop {
	unit, zero := pT, pF
	if k == pOr {
		unit, zero = pF, pT
	}
	var args []*prop
	for _, x := range xs {
		switch {
		case x.k == zero.k:
			return zero
		case x.k == unit.k:
		case x.k == k:
			args = append(args, x.args...)
		default:
			args = append(args, x)
		}
	}
	slices.SortFunc(args, func(a, b *prop) int {
		return strings.Compare(a.key, b.key)
	})
	args = slices.CompactFunc(args, func(a, b *prop) bool {
		return a.key == b.key
	})
	switch len(args) {
	case 0:
		return unit
	case 1:
		return args[0]
	}
	op := "&"
	if k == pOr {
		op = "|"
	}
	keys := make([]string, len(args))
	for i, a := range args {
		keys[i] = a.key
	}
	return &prop{k: k, args: args,
		key: "(" + strings.Join(keys, op) + ")"}
}

func andP(xs ...*prop) *prop { return junction(pAnd, xs) }
func orP(xs ...*prop) *prop  { return junction(pOr, xs) }

func alwaysP(p *prop) *prop {
	if p.k == pTrue {
		return pT
	}
	return &prop{k: pAlways, l: p, key: "G" + p.key}
}

func neverP(s *sere, ss []*sere) *prop {
	return &prop{k: pNever, seq: s, ss: ss,
		key: "N" + s.key + stateKey(ss)}
}

func eventuallyP(p *prop) *prop {
	if p.k == pTrue || p.k == pFalse {
		return p
	}
	return &prop{k: pEventually, l: p, strong: true, key: "E" + p.key}
}

func nextP(n int, strong bool, p *prop) *prop {
	if n <= 0 {
		return p
	}
	if p.k == pTrue && !strong {
		return pT
	}
	k := "X" + strconv.Itoa(n)
	if strong {
		k += "!"
	}
	return &prop{k: pNext, n: n, strong: strong, l: p, key: k + p.key}
}

func untilP(l, r *prop, strong, incl bool) *prop {
	return &prop{k: pUntil, l: l, r: r, strong: strong, incl: incl,
		key: "U" + flags(strong, incl) + "(" + l.key + "," + r.key + ")"}
}

func beforeP(l, r *prop, strong, incl bool) *prop {
	return &prop{k: pBefore, l: l, r: r, strong: strong, incl: incl,
		key: "B" + flags(strong, incl) + "(" + l.key + "," + r.key + ")"}
}

func suffixP(ss []*sere, overlap bool, r *prop) *prop {
	if len(ss) == 0 {
		return pT
	}
	k := "=>"
	if overlap {
		k = "->"
	}
	return &prop{k: pSuffix, ss: ss, overlap: overlap, r: r,
		key: "S" + stateKey(ss) + k + r.key}
}

func sereP(ss []*sere, strong bool) *prop {
	if len(ss) == 0 {
		return pF
	}
	return &prop{k: pSere, ss: ss, strong: strong,
		key: "Q" + flags(strong, false) + stateKey(ss)}
}

func abortP(p *prop, b int) *prop {
	if p.k == pTrue || p.k == pFalse {
		return p
	}
	return &prop{k: pAbort, l: p, b: b,
		key: "A" + strconv.Itoa(b) + "(" + p.key + ")"}
}

func nextEventP(b, n int, strong bool, p *prop) *prop {
	if p.k == pTrue && !strong {
		return pT
	}
	k := "V" + strconv.Itoa(b) + "," + strconv.Itoa(n)
	if strong {
		k += "!"
	}
	return &prop{k: pNextEvent, b: b, n: n, strong: strong, l: p,
		key: k + "(" + p.key + ")"}
}

func flags(strong, incl bool) string {
	s := ""
	if strong {
		s += "!"
	}
	if incl {
		s += "_"
	}
	return s
}

func stateKey(ss []*sere) string {
	keys := make([]string, len(ss))
	for i, s := range ss {
		keys[i] = s.key
	}
	return "{" + strings.Join(keys, ",") + "}"
}

// prog is the progression of p by the letter a.
func (st *stepper) prog(p *prop, a []bool) *prop {
	st.steps++
	switch p.k {
	case pTrue, pFalse:
		return p
	case pBool:
		if a[p.b] {
			return pT
		}
		return pF
	case pNot:
		return notP(st.prog(p.l, a))
	case pAnd, pOr:
		xs := make([]*prop, len(p.args))
		for i, x := range p.args {
			xs[i] = st.prog(x, a)
		}
		return junction(p.k, xs)
	case pAlways:
		return andP(st.prog(p.l, a), p)
	case pNever:
		hit, ss := st.advance(append(slices.Clone(p.ss), p.seq), a)
		if hit {
			return pF
		}
		return neverP(p.seq, ss)
	case pEventually:
		return orP(st.prog(p.l, a), p)
	case pNext:
		return nextP(p.n-1, p.strong, p.l)
	case pUntil:
		r := st.prog(p.r, a)
		l := st.prog(p.l, a)
		if p.incl {
			r = andP(r, l)
		}
		return orP(r, andP(l, p))
	case pBefore:
		l := st.prog(p.l, a)
		nr := notP(st.prog(p.r, a))
		if p.incl {
			return orP(l, andP(nr, p))
		}
		return orP(andP(l, nr), andP(notP(l), nr, p))
	case pSuffix:
		var obl []*prop
		ds := make([]*sere, 0, len(p.ss))
		for _, s := range p.ss {
			d := st.deriv(s, a)
			if d.null {
				if p.overlap {
					obl = append(obl, st.prog(p.r, a))
				} else {
					obl = append(obl, p.r)
				}
			}
			ds = append(ds, d)
		}
		return andP(append(obl, suffixP(states(ds), p.overlap, p.r))...)
	case pSere:
		hit, ss := st.advance(p.ss, a)
		if hit {
			return pT
		}
		return sereP(ss, p.strong)
	case pAbort:
		if a[p.b] {
			return pT
		}
		return abortP(st.prog(p.l, a), p.b)
	case pNextEvent:
		switch {
		case !a[p.b]:
			return p
		case p.n <= 1:
			return st.prog(p.l, a)
		}
		return nextEventP(p.b, p.n-1, p.strong, p.l)
	}
	return p
}

// pending reports whether p still has a strong obligation open: the
// run ended before p was met.
func pending(p *prop) bool {
	switch p.k {
	case pEventually:
		return true
	case pNext, pUntil, pBefore, pSere, pNextEvent:
		return p.strong
	case pAnd:
		return slices.ContainsFunc(p.args, pending)
	case pOr:
		return !slices.ContainsFunc(p.args, func(x *prop) bool {
			return !pending(x)
		})
	case pAbort:
		return pending(p.l)
	}
	return false
}

// conjuncts returns the top-level conjuncts of p.
func conjuncts(p *prop) []*prop {
	if p.k == pAnd {
		return p.args
	}
	return []*prop{p}
}
