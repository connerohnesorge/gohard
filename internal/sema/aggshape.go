package sema

import (
	"cmp"
	"slices"
	"strconv"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/eval"
)

// Array aggregate shape (LRM 9.3.3.3): each element of the value an
// array aggregate defines is given once and only once; a named
// aggregate without others defines the elements from its leftmost to
// its rightmost choice; with others its index range is the context's,
// which every choice lies in; and the value has the length of a
// constrained context (the implicit subtype conversion of LRM 14.7.3).
// The check runs only on literal choices and literal bounds, so it
// never guesses.

// aggregateShape checks the one-dimensional (or, by rows, the first
// dimension of a multi-dimensional) array aggregate e of subtype want.
func (a *analyzer) aggregateShape(e *Expr, want *Type) {
	if a.quiet || want.Unknown() || want.Base().Class != TypeArray ||
		e.illTyped {
		return
	}
	b := want.Base()
	ctxLo, ctxHi, ctx := contextBounds(want)
	others := false
	positional := int64(0)
	var ivs []interval
	named := true
	for _, as := range e.Assocs {
		if as.Others {
			others = true
			continue
		}
		if len(as.Choices) == 0 {
			if as.Slice {
				// A slice gives as many elements as the value has.
				n, ok := sliceLen(as.Value)
				if !ok {
					return
				}
				positional += n
				named = false
				continue
			}
			positional++
			named = false
			continue
		}
		// A named slice association (VHDL-2008) gives the elements of
		// its choice's range, like any other named association.
		for _, c := range as.Choices {
			l, h, ok := choiceBounds(c)
			if !ok {
				return
			}
			if l <= h {
				ivs = append(ivs, interval{l, h, c})
			}
		}
	}
	if positional > 0 {
		if !ctx {
			return
		}
		n := ctxHi - ctxLo + 1
		switch {
		case positional > max(n, 0):
			a.shapeError(e, "the aggregate has "+
				strconv.FormatInt(positional, 10)+" positional "+
				"elements, more "+
				"than the "+strconv.FormatInt(max(n, 0), 10)+" of "+
				want.Name())
		case !others && positional != max(n, 0):
			a.shapeError(e, "the aggregate has "+
				strconv.FormatInt(positional, 10)+" elements, but "+
				want.Name()+
				" has "+strconv.FormatInt(max(n, 0), 10))
		}
		return
	}
	if !named || len(ivs) == 0 {
		return
	}
	idx := firstIndex(b)
	lo, hi, ok := int64(0), int64(0), false
	if idx != nil {
		lo, hi, ok = discreteBounds(idx)
	}
	if others && ctx {
		lo, hi, ok = ctxLo, ctxHi, true
	}
	if ok {
		for _, iv := range ivs {
			if iv.lo < lo || iv.hi > hi {
				a.shapeError(iv.at, "the choice "+a.text(iv.at.Node)+
					" is outside the index range of the aggregate")
				return
			}
		}
	}
	slices.SortStableFunc(ivs, func(x, y interval) int {
		return cmp.Compare(x.lo, y.lo)
	})
	total := int64(0)
	for i, iv := range ivs {
		if i > 0 && iv.lo <= ivs[i-1].hi {
			a.shapeError(iv.at, "the choice "+a.text(iv.at.Node)+
				" repeats an element already given")
			return
		}
		if i > 0 && iv.lo > ivs[i-1].hi+1 && !others {
			a.shapeError(e, "the aggregate gives no element for "+
				valueText(idx, ivs[i-1].hi+1, iv.lo-1))
			return
		}
		total += iv.hi - iv.lo + 1
	}
	if others || !ctx {
		return
	}
	if n := max(ctxHi-ctxLo+1, 0); total != n {
		a.shapeError(e, "the aggregate has "+
			strconv.FormatInt(total, 10)+" elements, but "+want.Name()+
			" has "+strconv.FormatInt(n, 10))
	}
}

func (a *analyzer) shapeError(at *Expr, msg string) {
	at.illTyped = true
	a.semaError(at.Node.Span(), msg)
}

// contextBounds returns the literal bounds, as positions, of the first
// index constraint of the array subtype t, if it has one.
func contextBounds(t *Type) (lo, hi int64, ok bool) {
	if t.isDynamic() {
		return 0, 0, false
	}
	cons := t.Constraints()
	if len(cons) == 0 || cons[0] == nil {
		return 0, 0, false
	}
	rc := cons[0]
	if rc.Attr != nil {
		if rc.Attr.Kind == ExprTypeMark && !rc.Attr.Type.Unknown() {
			return discreteBounds(rc.Attr.Type)
		}
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

// assignedToObject reports whether the aggregate n is the value (or
// one of the conditional or selected values) of an assignment whose
// target is a name. LRM 9.3.3.3 c allows others there only when the
// target's declared subtype is constrained; an unconstrained port,
// parameter or alias gets its index ranges from its actual only at
// elaboration, so others there is an error. Lenient analysis allows it
// (GHDL -frelaxed, NVC --relaxed), and calls this.
func assignedToObject(n cst.Node) bool {
	for p := n.Parent(); !p.IsZero(); p = p.Parent() {
		switch p.Kind() {
		case cst.KindParenExpr, cst.KindWaveformElement,
			cst.KindWaveform, cst.KindWhenBranch, cst.KindSelectBranch,
			cst.KindConditionalWaveforms, cst.KindSelectedWaveforms,
			cst.KindConditionalExpressions,
			cst.KindSelectedExpressions, cst.KindConditionalExpr:
			continue
		case cst.KindSignalAssignStmt,
			cst.KindConditionalSignalAssignStmt,
			cst.KindSelectedSignalAssignStmt, cst.KindForceAssignStmt,
			cst.KindVariableAssignStmt,
			cst.KindConditionalVariableAssignStmt,
			cst.KindSelectedVariableAssignStmt,
			cst.KindConcurrentSimpleSignalAssign,
			cst.KindConcurrentConditionalSignalAssign,
			cst.KindConcurrentSelectedSignalAssign:
			for c := range p.Nodes() {
				if c.Kind() == cst.KindTarget {
					t := cst.Target{Node: c}.Target().Node
					return (cst.Name{}).Accepts(t.Kind())
				}
			}
		}
		return false
	}
	return false
}

// sliceLen is the statically known number of elements of the value v
// of a slice association: a string or bit-string literal's, or that
// of an object, slice or qualified expression whose subtype has
// literal bounds.
func sliceLen(v *Expr) (int64, bool) {
	if v == nil || v.illTyped {
		return 0, false
	}
	switch v.Kind {
	case ExprLiteral:
		return literalLen(v.Lit)
	case ExprName, ExprSelect, ExprSlice, ExprQualified:
		// Their subtype is their own; an aggregate or a conditional
		// value takes the context's.
		if lo, hi, ok := contextBounds(v.Type); ok {
			return max(hi-lo+1, 0), true
		}
	}
	return 0, false
}
