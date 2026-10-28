package psl

import (
	"testing"

	"github.com/connerohnesorge/gohard/internal/env"
)

// The engine tests check the derivative matcher and the formula
// progression against direct reference semantics on random sequences,
// properties and traces: the two implementations share no code but
// the generated syntax trees.

type rng struct{ s env.SplitMix64 }

func (r *rng) n(k int) int { return env.Intn(&r.s, k) }

const nBools = 3

// gs is a generated sequence: the reference matches it directly. op
// is one of bool, nbool, true, cat, fuse, or, and, andnl, star, rep,
// goto, ncons and within.
type gs struct {
	op     string
	b      int
	l, r   *gs
	lo, hi int
}

func genS(r *rng, depth int) *gs {
	if depth == 0 || r.n(4) == 0 {
		switch r.n(5) {
		case 0:
			return &gs{op: "true"}
		case 1:
			return &gs{op: "nbool", b: r.n(nBools)}
		}
		return &gs{op: "bool", b: r.n(nBools)}
	}
	ops := []string{"cat", "fuse", "or", "and", "andnl", "star", "rep",
		"goto", "ncons", "within"}
	g := &gs{op: ops[r.n(len(ops))]}
	switch g.op {
	case "star":
		g.l = genS(r, depth-1)
	case "rep":
		g.l = genS(r, depth-1)
		g.lo = r.n(3)
		g.hi = g.lo + r.n(3)
		if r.n(4) == 0 {
			g.hi = -1
		}
	case "goto", "ncons":
		g.b = r.n(nBools)
		g.lo = 1 + r.n(2)
		g.hi = g.lo + r.n(2)
		if r.n(4) == 0 {
			g.hi = -1
		}
	default:
		g.l, g.r = genS(r, depth-1), genS(r, depth-1)
	}
	return g
}

func (g *gs) build() *sere {
	switch g.op {
	case "true":
		return anyL
	case "bool":
		return boolS(g.b, false)
	case "nbool":
		return boolS(g.b, true)
	case "cat":
		return cat(g.l.build(), g.r.build())
	case "fuse":
		return fuse(g.l.build(), g.r.build())
	case "or":
		return or(g.l.build(), g.r.build())
	case "and":
		return and(g.l.build(), g.r.build())
	case "andnl":
		return andNL(g.l.build(), g.r.build())
	case "star":
		return star(g.l.build())
	case "rep":
		return rep(g.l.build(), g.lo, g.hi)
	case "goto":
		return gotoRep(g.b, g.lo, g.hi)
	case "ncons":
		return nonConsec(g.b, g.lo, g.hi)
	case "within":
		return within(g.l.build(), g.r.build())
	}
	panic(g.op)
}

// match reports whether g matches w[i:j] (the reference semantics).
func (g *gs) match(w [][]bool, i, j int) bool {
	switch g.op {
	case "true":
		return j == i+1
	case "bool":
		return j == i+1 && w[i][g.b]
	case "nbool":
		return j == i+1 && !w[i][g.b]
	case "cat":
		for k := i; k <= j; k++ {
			if g.l.match(w, i, k) && g.r.match(w, k, j) {
				return true
			}
		}
		return false
	case "fuse":
		for k := i + 1; k <= j; k++ {
			if g.l.match(w, i, k) && g.r.match(w, k-1, j) {
				return true
			}
		}
		return false
	case "or":
		return g.l.match(w, i, j) || g.r.match(w, i, j)
	case "and":
		return g.l.match(w, i, j) && g.r.match(w, i, j)
	case "andnl":
		pre := func(x *gs) bool {
			for k := i; k <= j; k++ {
				if x.match(w, i, k) {
					return true
				}
			}
			return false
		}
		return g.l.match(w, i, j) && pre(g.r) ||
			g.r.match(w, i, j) && pre(g.l)
	case "star":
		if i == j {
			return true
		}
		for k := i + 1; k <= j; k++ {
			if g.l.match(w, i, k) && g.match(w, k, j) {
				return true
			}
		}
		return false
	case "rep":
		return g.reps(w, i, j, 0)
	case "goto", "ncons":
		n := 0
		for k := i; k < j; k++ {
			if w[k][g.b] {
				n++
			}
		}
		if g.op == "goto" && (j == i || !w[j-1][g.b]) {
			return false
		}
		return n >= g.lo && (g.hi < 0 || n <= g.hi)
	case "within":
		if !g.r.match(w, i, j) {
			return false
		}
		for a := i; a <= j; a++ {
			for b := a; b <= j; b++ {
				if g.l.match(w, a, b) {
					return true
				}
			}
		}
		return false
	}
	panic(g.op)
}

// reps: w[i:j] is k or more further repetitions of g.l, done = done
// so far.
func (g *gs) reps(w [][]bool, i, j, done int) bool {
	if i == j && done >= g.lo && (g.hi < 0 || done <= g.hi) {
		return true
	}
	if g.hi >= 0 && done >= g.hi {
		return false
	}
	for k := i; k <= j; k++ {
		if k == i && done >= g.lo {
			continue // an empty repetition adds nothing
		}
		if g.l.match(w, i, k) && g.reps(w, k, j, done+1) {
			return true
		}
	}
	return false
}

func genWord(r *rng, n int) [][]bool {
	w := make([][]bool, n)
	for i := range w {
		w[i] = make([]bool, nBools)
		for b := range w[i] {
			w[i][b] = r.n(2) == 0
		}
	}
	return w
}

// TestDerivativeMatcher: after the letters of w the derivative state
// matches the empty word exactly when the sequence matches w.
func TestDerivativeMatcher(t *testing.T) {
	r := &rng{env.SplitMix64{State: 1}}
	for it := range 4000 {
		g := genS(r, 3)
		s := g.build()
		w := genWord(r, r.n(7))
		st := &stepper{}
		cur := s
		for _, a := range w {
			cur = st.deriv(cur, a)
		}
		if got, want := cur.null, g.match(w, 0, len(w)); got != want {
			t.Fatalf("#%d %s on %v: derivative %v, reference %v", it,
				s.key, w, got, want)
		}
	}
}

// TestDerivativeStatesBounded: the canonical states of a running match
// stay a small set however long the run (finitely many derivatives).
func TestDerivativeStatesBounded(t *testing.T) {
	r := &rng{env.SplitMix64{State: 2}}
	for range 200 {
		s := genS(r, 3).build()
		var ss []*sere
		st := &stepper{}
		for range 200 {
			_, ss = st.advance(append(ss, s), genWord(r, 1)[0])
			if len(ss) > 512 {
				t.Fatalf("%s: %d states", s.key, len(ss))
			}
		}
	}
}

// gp is a generated simple-subset property. op nextevent is
// next_event(b)[n+1](l), nexteventa and nexteventE are next_event_a and
// next_event_e (b)[n+1 to hi+1](l) (#66).
type gp struct {
	op           string
	b, b2        int
	strong, incl bool
	overlap      bool
	n, hi        int
	l, r         *gp
	s            *gs
}

func genP(r *rng, depth int) *gp {
	if depth == 0 || r.n(4) == 0 {
		if r.n(3) == 0 {
			return &gp{op: "nbool", b: r.n(nBools)}
		}
		return &gp{op: "bool", b: r.n(nBools)}
	}
	ops := []string{"and", "or", "always", "never", "eventually", "next",
		"until", "before", "suffix", "abort", "implies", "nextevent",
		"nexteventa", "nexteventE"}
	g := &gp{op: ops[r.n(len(ops))], b: r.n(nBools), b2: r.n(nBools),
		strong: r.n(2) == 0, incl: r.n(2) == 0, overlap: r.n(2) == 0,
		n: r.n(3)}
	g.hi = g.n + r.n(3)
	switch g.op {
	case "and", "or":
		g.l, g.r = genP(r, depth-1), genP(r, depth-1)
	case "always", "next", "abort", "implies", "nextevent", "nexteventa",
		"nexteventE":
		g.l = genP(r, depth-1)
	case "suffix":
		g.s, g.l = genS(r, 2), genP(r, depth-1)
	case "never":
		g.s = genS(r, 2)
	}
	return g
}

func (g *gp) build() *prop {
	switch g.op {
	case "bool":
		return boolP(g.b)
	case "nbool":
		return notP(boolP(g.b))
	case "and":
		return andP(g.l.build(), g.r.build())
	case "or":
		return orP(g.l.build(), g.r.build())
	case "always":
		return alwaysP(g.l.build())
	case "never":
		return neverP(g.s.build(), nil)
	case "eventually":
		return eventuallyP(boolP(g.b))
	case "next":
		return nextP(g.n, g.strong, g.l.build())
	case "until":
		return untilP(boolP(g.b), boolP(g.b2), g.strong, g.incl)
	case "before":
		return beforeP(boolP(g.b), boolP(g.b2), g.strong, g.incl)
	case "suffix":
		return suffixP([]*sere{g.s.build()}, g.overlap, g.l.build())
	case "abort":
		return abortP(g.l.build(), g.b)
	case "implies":
		return orP(notP(boolP(g.b)), g.l.build())
	case "nextevent":
		return nextEventP(g.b, g.n+1, g.strong, g.l.build())
	case "nexteventa", "nexteventE":
		var xs []*prop
		for k := g.n + 1; k <= g.hi+1; k++ {
			xs = append(xs, nextEventP(g.b, k, g.strong, g.l.build()))
		}
		if g.op == "nexteventE" {
			return orP(xs...)
		}
		return andP(xs...)
	}
	panic(g.op)
}

// holds is the reference semantics of g at position i of w[:n] on a
// finite trace: weak operators hold when the trace ends first, strong
// ones fail; on a trace an abort truncated (top) everything holds.
func (g *gp) holds(w [][]bool, i, n int, top bool) bool {
	if i >= n && top {
		return true // a truncated (aborted) trace: everything holds
	}
	if i >= n {
		switch g.op {
		case "and":
			return g.l.holds(w, i, n, top) && g.r.holds(w, i, n, top)
		case "or":
			return g.l.holds(w, i, n, top) || g.r.holds(w, i, n, top)
		case "eventually":
			return false
		case "until", "before", "nextevent", "nexteventa", "nexteventE":
			return !g.strong
		case "next":
			if g.n == 0 {
				return g.l.holds(w, i, n, top)
			}
			return !g.strong
		case "abort":
			return g.l.holds(w, i, n, top)
		}
		return true
	}
	switch g.op {
	case "bool":
		return w[i][g.b]
	case "nbool":
		return !w[i][g.b]
	case "and":
		return g.l.holds(w, i, n, top) && g.r.holds(w, i, n, top)
	case "or":
		return g.l.holds(w, i, n, top) || g.r.holds(w, i, n, top)
	case "implies":
		return !w[i][g.b] || g.l.holds(w, i, n, top)
	case "always":
		for j := i; j < n; j++ {
			if !g.l.holds(w, j, n, top) {
				return false
			}
		}
		return true
	case "never":
		for a := i; a < n; a++ {
			for b := a + 1; b <= n; b++ {
				if g.s.match(w, a, b) {
					return false
				}
			}
		}
		return true
	case "eventually":
		for j := i; j < n; j++ {
			if w[j][g.b] {
				return true
			}
		}
		return top
	case "next":
		if i+g.n > n {
			return !g.strong || top
		}
		return g.l.holds(w, i+g.n, n, top)
	case "until":
		for j := i; j < n; j++ {
			if w[j][g.b2] {
				return !g.incl || w[j][g.b]
			}
			if !w[j][g.b] {
				return false
			}
		}
		return !g.strong || top
	case "before":
		for j := i; j < n; j++ {
			l, r := w[j][g.b], w[j][g.b2]
			if g.incl && l {
				return true
			}
			if !g.incl && (l || r) {
				return l && !r
			}
			if r {
				return false
			}
		}
		return !g.strong || top
	case "suffix":
		for k := i + 1; k <= n; k++ {
			if !g.s.match(w, i, k) {
				continue
			}
			at := k
			if g.overlap {
				at = k - 1
			}
			if !g.l.holds(w, at, n, top) {
				return false
			}
		}
		return true
	case "nextevent", "nexteventa", "nexteventE":
		return g.nextEvent(w, i, n, top)
	case "abort":
		for j := i; j < n; j++ {
			if w[j][g.b] {
				return g.l.holds(w, i, j, true)
			}
		}
		return g.l.holds(w, i, n, top)
	}
	panic(g.op)
}

// nextEvent is the reference semantics of the next_event family (IEEE
// 1850 6.2.1.6) at position i of w[:n]: the ticks at and after i on
// which b holds are counted from 1; l must hold at the k-th of them
// (next_event), at each of the lo-th to hi-th that the trace reaches
// (next_event_a) or at one of them (next_event_e). A strong form also
// needs the trace to reach the last tick it names (next_event_e: one
// where l holds); a weak one holds when the trace ends first.
func (g *gp) nextEvent(w [][]bool, i, n int, top bool) bool {
	lo, hi := g.n+1, g.hi+1
	if g.op == "nextevent" {
		hi = lo
	}
	var at []int // the lo-th to hi-th ticks of b
	k := 0
	for j := i; j < n && k < hi; j++ {
		if w[j][g.b] {
			k++
			if k >= lo {
				at = append(at, j)
			}
		}
	}
	// ended: the trace ends before the hi-th tick of b; on a truncated
	// (aborted) trace that holds, as everything does.
	ended := k < hi && (top || !g.strong)
	switch g.op {
	case "nextevent", "nexteventa":
		for _, j := range at {
			if !g.l.holds(w, j, n, top) {
				return false
			}
		}
		return k >= hi || ended
	}
	for _, j := range at {
		if g.l.holds(w, j, n, top) {
			return true
		}
	}
	return ended
}

// check runs p over w the way the runner does: it reports whether no
// tick failed and no strong obligation is left open.
func check(p *prop, w [][]bool) bool {
	st := &stepper{}
	obls := []*prop{p}
	for _, a := range w {
		var next []*prop
		for _, o := range obls {
			for _, x := range conjuncts(st.prog(o, a)) {
				switch x.k {
				case pFalse:
					return false
				case pTrue:
				default:
					next = append(next, x)
				}
			}
		}
		obls = next
	}
	for _, o := range obls {
		if pending(o) {
			return false
		}
	}
	return true
}

// TestProgression: formula progression agrees with the reference
// semantics on random simple-subset properties and traces.
func TestProgression(t *testing.T) {
	r := &rng{env.SplitMix64{State: 3}}
	for it := range 6000 {
		g := genP(r, 3)
		p := g.build()
		w := genWord(r, r.n(8))
		if got, want := check(p, w), g.holds(w, 0, len(w), false); got != want {
			t.Fatalf("#%d %s on %v: engine %v, reference %v", it, p.key,
				w, got, want)
		}
	}
}

// TestAlwaysBounded: the obligations of always over a long run stay
// a bounded set (canonical keys deduplicate them).
func TestAlwaysBounded(t *testing.T) {
	r := &rng{env.SplitMix64{State: 4}}
	for range 100 {
		p := alwaysP(genP(r, 2).build())
		st := &stepper{}
		obls := []*prop{p}
		for range 300 {
			a := genWord(r, 1)[0]
			seen := map[string]bool{}
			var next []*prop
			for _, o := range obls {
				for _, x := range conjuncts(st.prog(o, a)) {
					if x.k > pFalse && !seen[x.key] {
						seen[x.key] = true
						next = append(next, x)
					}
				}
			}
			obls = next
		}
		if len(obls) > 1024 {
			t.Fatalf("%s: %d obligations", p.key, len(obls))
		}
	}
}
