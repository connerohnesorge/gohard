package sema

import (
	"cmp"
	"slices"
	"strconv"

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
	positional := 0
	var ivs []interval
	named := true
	for _, as := range e.Assocs {
		if as.Others {
			others = true
			continue
		}
		if len(as.Choices) == 0 {
			if as.Slice {
				return // a slice's length is the value's
			}
			positional++
			named = false
			continue
		}
		if as.Slice {
			return
		}
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
		case int64(positional) > max(n, 0):
			a.shapeError(e, "the aggregate has "+
				strconv.Itoa(positional)+" positional elements, more "+
				"than the "+strconv.FormatInt(max(n, 0), 10)+" of "+
				want.Name())
		case !others && int64(positional) != max(n, 0):
			a.shapeError(e, "the aggregate has "+
				strconv.Itoa(positional)+" elements, but "+want.Name()+
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
