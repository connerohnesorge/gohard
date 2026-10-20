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
	a.choiceLengths(sel, alts)
	if a.quiet || sel == nil || sel.illTyped || sel.Type.Unknown() ||
		!sel.Type.IsDiscrete() {
		return
	}
	st := sel.Type
	if d := actualObject(sel); d != nil && (d.Kind == DeclLoopParam ||
		d.Kind == DeclGenerateParam) {
		// Its subtype is its range: checked when that range is known.
		pst, ok := paramSubtype(d)
		if !ok || sel.Kind != ExprName {
			return
		}
		st = pst
	}
	switch {
	case st != sel.Type:
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
	var covered int64 // the highest value covered so far
	dup := false
	for i, iv := range ivs {
		if i == 0 {
			covered = iv.hi
		}
		if i > 0 && iv.lo <= covered {
			// Every repeating choice is reported (LRM 10.9).
			a.semaError(iv.at.Node.Span(), "the choice "+
				a.text(iv.at.Node)+" repeats a value already covered")
			dup = true
			covered = max(covered, iv.hi)
			continue
		}
		covered = max(covered, iv.hi)
		if dup {
			continue
		}
		if iv.lo > next && !others {
			a.semaError(at, "the choices do not cover "+
				valueText(st, next, iv.lo-1))
			return
		}
		next = iv.hi + 1
	}
	if !dup && next <= hi && !others &&
		(len(ivs) == 0 || ivs[len(ivs)-1].hi < hi) {
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
		l, ok1 := choiceValue(rc.Left)
		r, ok2 := choiceValue(rc.Right)
		if !ok1 || !ok2 {
			return 0, 0, false
		}
		if rc.Dir == eval.Downto {
			l, r = r, l
		}
		return l, r, true
	}
	v, ok := choiceValue(c)
	return v, v, ok
}

// choiceValue is the position of a choice bound: a literal, a type
// mark attribute bound, or + and - of such (choices are locally
// static, LRM 10.9; foldBound).
func choiceValue(e *Expr) (int64, bool) {
	if v, ok := position(e); ok {
		return v, true
	}
	v, ok, over := foldBound(e, 0)
	return v, ok && !over
}

// position is the value of an integer literal, the position of an
// enumeration literal, or T'LOW, T'HIGH, T'LEFT or T'RIGHT of a type
// mark T whose bounds are such.
func position(e *Expr) (int64, bool) {
	return positionAt(e, 0)
}

func positionAt(e *Expr, depth int) (int64, bool) {
	if v, ok := literalInt(e); ok {
		return v, true
	}
	if e != nil && e.Kind == ExprAttr && len(e.Args) == 0 &&
		e.Decl == nil && e.Prefix != nil &&
		e.Prefix.Kind == ExprTypeMark && depth < 8 {
		return attrBound(e.Prefix.Type, e.Attr, depth+1)
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

// attrBound is T'LOW, T'HIGH, T'LEFT or T'RIGHT of the discrete
// subtype t when its bounds are literals or such attributes.
func attrBound(t *Type, attr Key, depth int) (int64, bool) {
	if t.Unknown() || !t.IsDiscrete() {
		return 0, false
	}
	b := t.Base()
	rc := t.ScalarRange()
	var l, r int64
	switch {
	case b.Class == TypeEnum && (rc == nil || t == b):
		if len(b.Literals) == 0 {
			return 0, false
		}
		l, r = 0, int64(len(b.Literals))-1
	case rc == nil && b.Eval != nil && b.Eval.Kind == eval.KindInteger:
		l, r = b.Eval.Range.Left, b.Eval.Range.Right
	case rc == nil || rc.Attr != nil:
		return 0, false
	default:
		var ok1, ok2 bool
		l, ok1 = positionAt(rc.Left, depth)
		r, ok2 = positionAt(rc.Right, depth)
		if !ok1 || !ok2 {
			return 0, false
		}
	}
	lo, hi := min(l, r), max(l, r)
	switch attr {
	case "low":
		return lo, true
	case "high":
		return hi, true
	case "left":
		return l, true
	case "right":
		return r, true
	}
	return 0, false
}

// choiceLengths reports a string or bit-string choice whose length
// differs from that of the array case expression's subtype when it is
// known from literal bounds, or else from that of the first such
// choice: all choices have the same length (LRM 10.9).
func (a *analyzer) choiceLengths(sel *Expr, alts []SeqAlt) {
	if a.quiet || sel == nil || sel.illTyped || sel.Type.Unknown() ||
		sel.Type.Base().Class != TypeArray {
		return
	}
	want, known := litLength(sel.Type)
	what := "the case expression"
	for _, alt := range alts {
		for _, c := range alt.Choices {
			if c == nil || c.Kind != ExprLiteral || c.illTyped {
				continue
			}
			have, ok := literalLen(c.Lit)
			switch {
			case !ok:
			case !known:
				want, known = have, true
				what = "the choice " + a.text(c.Node)
			case have != want:
				a.semaError(c.Node.Span(), "the choice "+a.text(c.Node)+
					" has "+strconv.FormatInt(have, 10)+" elements, "+
					"but "+what+" has "+strconv.FormatInt(want, 10))
			}
		}
	}
}

// paramSubtype returns the subtype of the loop parameter d when its
// range has literal bounds: lo to hi, or the 'RANGE (or
// 'REVERSE_RANGE) of an object whose one-dimensional array subtype
// has a literal index range.
func paramSubtype(d *Decl) (*Type, bool) {
	rc := d.paramRange
	if rc == nil || rc.Type.Unknown() {
		return nil, false
	}
	if rc.Attr != nil {
		at := rc.Attr
		if at.Kind != ExprAttr || at.Prefix == nil || len(at.Args) > 0 ||
			(at.Attr != "range" && at.Attr != "reverse_range") ||
			at.Prefix.Kind == ExprTypeMark {
			return nil, false
		}
		cons := at.Prefix.Type.Constraints()
		if len(cons) != 1 || cons[0] == nil || cons[0].Attr != nil {
			return nil, false
		}
		rc = cons[0]
	}
	if _, ok := position(rc.Left); !ok {
		return nil, false
	}
	if _, ok := position(rc.Right); !ok {
		return nil, false
	}
	st := NewSubtype(rc.Type)
	st.Range = rc
	return st, true
}
