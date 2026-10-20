package psl

import (
	"slices"
	"strconv"
	"strings"
)

// A sere is a PSL sequence (SERE) as a regular expression over the
// letters of the run, one letter per clock tick: the values of the
// directive's booleans at that tick. Sequences are matched online by
// Brzozowski derivatives: the derivative of s by letter a matches the
// rest of every word s matches that starts with a. Or is kept in a
// canonical form (flattened, sorted by key, without duplicates), so the
// derivatives of one sequence are finitely many and a running match is
// a small set of sequence states.
type sere struct {
	k    sKind
	b    int  // sBool: the boolean
	neg  bool // sBool: matches when the boolean is false
	l, r *sere
	alts []*sere // sOr
	key  string
	null bool // matches the empty word
}

type sKind uint8

const (
	sNone  sKind = iota // matches nothing
	sEmpty              // matches the empty word only
	sTrue               // any one letter
	sBool               // one letter where boolean b holds (or not)
	sCat                // l ; r
	sFuse               // l : r (the last letter of l is r's first)
	sOr                 // alts
	sAnd                // l && r (same length)
	sAndNL              // l & r (one ends at the other's end or later)
	sStar               // l[*]
)

var (
	none  = &sere{k: sNone, key: "0"}
	empty = &sere{k: sEmpty, key: "e", null: true}
	anyL  = &sere{k: sTrue, key: "t"}
)

func boolS(b int, neg bool) *sere {
	k := "b" + strconv.Itoa(b)
	if neg {
		k = "!" + k
	}
	return &sere{k: sBool, b: b, neg: neg, key: k}
}

func cat(l, r *sere) *sere {
	switch {
	case l.k == sNone || r.k == sNone:
		return none
	case l.k == sEmpty:
		return r
	case r.k == sEmpty:
		return l
	}
	return &sere{k: sCat, l: l, r: r, key: "(" + l.key + ";" + r.key + ")",
		null: l.null && r.null}
}

func fuse(l, r *sere) *sere {
	if l.k == sNone || r.k == sNone {
		return none
	}
	return &sere{k: sFuse, l: l, r: r, key: "(" + l.key + ":" + r.key + ")"}
}

func or(xs ...*sere) *sere {
	var alts []*sere
	for _, x := range xs {
		switch x.k {
		case sNone:
		case sOr:
			alts = append(alts, x.alts...)
		default:
			alts = append(alts, x)
		}
	}
	slices.SortFunc(alts, func(a, b *sere) int {
		return strings.Compare(a.key, b.key)
	})
	alts = slices.CompactFunc(alts, func(a, b *sere) bool {
		return a.key == b.key
	})
	switch len(alts) {
	case 0:
		return none
	case 1:
		return alts[0]
	}
	var sb strings.Builder
	sb.WriteString("(")
	null := false
	for i, a := range alts {
		if i > 0 {
			sb.WriteString("|")
		}
		sb.WriteString(a.key)
		null = null || a.null
	}
	sb.WriteString(")")
	return &sere{k: sOr, alts: alts, key: sb.String(), null: null}
}

func and(l, r *sere) *sere {
	if l.k == sNone || r.k == sNone {
		return none
	}
	if l.key == r.key {
		return l
	}
	if r.key < l.key {
		l, r = r, l
	}
	return &sere{k: sAnd, l: l, r: r, key: "(" + l.key + "&&" + r.key + ")",
		null: l.null && r.null}
}

func andNL(l, r *sere) *sere {
	if l.k == sNone || r.k == sNone {
		return none
	}
	if l.key == r.key {
		return l
	}
	if r.key < l.key {
		l, r = r, l
	}
	return &sere{k: sAndNL, l: l, r: r, key: "(" + l.key + "&" + r.key + ")",
		null: l.null && r.null}
}

func star(s *sere) *sere {
	switch s.k {
	case sNone, sEmpty:
		return empty
	case sStar:
		return s
	}
	return &sere{k: sStar, l: s, key: "(" + s.key + ")*", null: true}
}

// rep is s[*lo to hi]; hi < 0 is inf.
func rep(s *sere, lo, hi int) *sere {
	head := empty
	for range lo {
		head = cat(head, s)
	}
	if hi < 0 {
		return cat(head, star(s))
	}
	alts := []*sere{head}
	cur := head
	for i := lo; i < hi; i++ {
		cur = cat(cur, s)
		alts = append(alts, cur)
	}
	return or(alts...)
}

// gotoRep is b[->lo to hi]: the lo-th to hi-th occurrence of b.
func gotoRep(b int, lo, hi int) *sere {
	one := cat(star(boolS(b, true)), boolS(b, false))
	return rep(one, lo, hi)
}

// nonConsec is b[=lo to hi]: b[->lo to hi] then any non-b letters.
func nonConsec(b int, lo, hi int) *sere {
	return cat(gotoRep(b, lo, hi), star(boolS(b, true)))
}

// within is l within r: l matches inside a match of r.
func within(l, r *sere) *sere {
	return and(cat(star(anyL), cat(l, star(anyL))), r)
}

// stepper takes derivatives under a step budget.
type stepper struct {
	steps int64
}

// deriv is the derivative of s by the letter a.
func (st *stepper) deriv(s *sere, a []bool) *sere {
	st.steps++
	switch s.k {
	case sTrue:
		return empty
	case sBool:
		if a[s.b] != s.neg {
			return empty
		}
		return none
	case sCat:
		d := cat(st.deriv(s.l, a), s.r)
		if s.l.null {
			return or(d, st.deriv(s.r, a))
		}
		return d
	case sFuse:
		dl := st.deriv(s.l, a)
		d := fuse(dl, s.r)
		if ended(dl) {
			return or(d, st.deriv(s.r, a))
		}
		return d
	case sOr:
		ds := make([]*sere, len(s.alts))
		for i, x := range s.alts {
			ds[i] = st.deriv(x, a)
		}
		return or(ds...)
	case sAnd:
		return and(st.deriv(s.l, a), st.deriv(s.r, a))
	case sAndNL:
		dl, dr := st.deriv(s.l, a), st.deriv(s.r, a)
		d := andNL(dl, dr)
		// One operand may already have ended: the other runs alone.
		if s.l.null {
			d = or(d, dr)
		}
		if s.r.null {
			d = or(d, dl)
		}
		return d
	case sStar:
		return cat(st.deriv(s.l, a), s)
	}
	return none
}

// ended reports whether a derivative accepts the empty word: the
// sequence can end at the letter just taken.
func ended(s *sere) bool { return s.null }

// live reports whether s can still match a non-empty continuation.
func live(s *sere) bool { return s.k != sNone && s.k != sEmpty }

// advance takes the derivatives of the states ss by a, returning
// whether one of them matched at a and the live states, canonical.
func (st *stepper) advance(ss []*sere, a []bool) (bool, []*sere) {
	ds := make([]*sere, 0, len(ss))
	hit := false
	for _, s := range ss {
		d := st.deriv(s, a)
		hit = hit || d.null
		ds = append(ds, d)
	}
	return hit, states(ds)
}

// states canonicalises a set of states: live ones, sorted by key,
// without duplicates.
func states(ds []*sere) []*sere {
	out := make([]*sere, 0, len(ds))
	for _, d := range ds {
		if d.k == sOr {
			for _, x := range d.alts {
				if live(x) {
					out = append(out, x)
				}
			}
			continue
		}
		if live(d) {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b *sere) int {
		return strings.Compare(a.key, b.key)
	})
	return slices.CompactFunc(out, func(a, b *sere) bool {
		return a.key == b.key
	})
}
