package sema

import (
	"cmp"
	"slices"
	"strconv"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/eval"
)

// Case choice coverage (LRM 10.9): the choices of a case statement over
// a discrete selector cover each value of the selector's subtype once,
// unless others is given; no choice lies outside it. The check runs
// only when every choice and the subtype's bounds are literals
// (integer or enumeration), so it never guesses.

type interval struct {
	lo, hi int64
	at     *Expr
}

// caseCoverage checks the choices alts of a case statement or selected
// assignment whose selector is sel; missing values are reported at.
func (a *analyzer) caseCoverage(sel *Expr, alts []SeqAlt, at cst.Range) {
	if a.quiet || sel == nil || sel.illTyped || sel.Type.Unknown() ||
		!sel.Type.IsDiscrete() {
		return
	}
	if d := actualObject(sel); d != nil && (d.Kind == DeclLoopParam ||
		d.Kind == DeclGenerateParam) {
		return // its subtype is its range, which sema does not keep
	}
	st := sel.Type
	switch {
	case sel.Kind == ExprQualified || sel.Kind == ExprConversion:
		// LRM 10.9: the type mark's subtype.
	case sel.Kind != ExprName || actualObject(sel) == nil:
		st = st.Base() // another expression covers its base type
	}
	lo, hi, ok := discreteBounds(st)
	if !ok {
		return
	}
	others := false
	var ivs []interval
	for _, alt := range alts {
		others = others || alt.Others
		for _, c := range alt.Choices {
			l, h, ok := choiceBounds(c)
			if !ok {
				return
			}
			if l > h {
				continue // a null range covers nothing
			}
			ivs = append(ivs, interval{l, h, c})
		}
	}
	for _, iv := range ivs {
		if iv.lo < lo || iv.hi > hi {
			a.semaError(iv.at.Node.Span(), "the choice "+
				a.text(iv.at.Node)+" is outside the range of "+st.Name())
			return
		}
	}
	slices.SortStableFunc(ivs, func(x, y interval) int {
		return cmp.Compare(x.lo, y.lo)
	})
	next := lo
	for i, iv := range ivs {
		if i > 0 && iv.lo <= ivs[i-1].hi {
			a.semaError(iv.at.Node.Span(), "the choice "+
				a.text(iv.at.Node)+" repeats a value already covered")
			return
		}
		if iv.lo > next && !others {
			a.semaError(at, "the choices do not cover "+
				valueText(st, next, iv.lo-1))
			return
		}
		next = iv.hi + 1
	}
	if next <= hi && !others && (len(ivs) == 0 || ivs[len(ivs)-1].hi < hi) {
		a.semaError(at, "the choices do not cover "+
			valueText(st, next, hi))
	}
}

// discreteBounds returns the literal bounds of a discrete subtype as
// positions.
func discreteBounds(t *Type) (lo, hi int64, ok bool) {
	b := t.Base()
	rc := t.ScalarRange()
	if b.Class == TypeEnum && (rc == nil || t == b) {
		return 0, int64(len(b.Literals)) - 1, len(b.Literals) > 0
	}
	if rc == nil && b.Eval != nil && b.Eval.Kind == eval.KindInteger {
		// std.standard's implementation-defined INTEGER.
		r := b.Eval.Range
		return min(r.Left, r.Right), max(r.Left, r.Right), true
	}
	if rc == nil || rc.Attr != nil {
		return 0, 0, false
	}
	l, ok1 := position(rc.Left)
	r, ok2 := position(rc.Right)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	if l > r {
		l, r = r, l
	}
	return l, r, true
}

// choiceBounds returns the positions a choice covers.
func choiceBounds(c *Expr) (lo, hi int64, ok bool) {
	if c.Kind == ExprRange {
		rc := c.Range
		if rc == nil || rc.Attr != nil {
			return 0, 0, false
		}
		l, ok1 := position(rc.Left)
		r, ok2 := position(rc.Right)
		if !ok1 || !ok2 {
			return 0, 0, false
		}
		if rc.Dir == eval.Downto {
			l, r = r, l
		}
		return l, r, true
	}
	v, ok := position(c)
	return v, v, ok
}

// position is the value of an integer literal or the position of an
// enumeration literal.
func position(e *Expr) (int64, bool) {
	if v, ok := literalInt(e); ok {
		return v, true
	}
	if e == nil || e.Decl == nil {
		return 0, false
	}
	if d := e.Decl.Denotes(); d.Kind == DeclEnumLiteral &&
		(e.Kind == ExprName || e.Kind == ExprCall) && len(e.Args) == 0 {
		return int64(d.Pos), true
	}
	return 0, false
}

// valueText names the values lo to hi of t.
func valueText(t *Type, lo, hi int64) string {
	name := func(v int64) string {
		b := t.Base()
		if b.Class == TypeEnum && v >= 0 && v < int64(len(b.Literals)) {
			return b.Literals[v].Text
		}
		return strconv.FormatInt(v, 10)
	}
	if lo == hi {
		return name(lo)
	}
	return name(lo) + " to " + name(hi)
}
