package sema

// staticrules.go holds the rules that require a static expression or
// a static name where the LRM does (#34): generate conditions and
// ranges (LRM 11.8), sensitivity lists (LRM 10.2, 11.3), object alias
// names (LRM 6.6.1, 6.6.2), the names of an aggregate target (LRM
// 10.5.2.1, 10.6.2.1) and formal designators (LRM 6.5.7.1).

import (
	"math"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// staticKind selects the static class a rule requires.
type staticKind uint8

const (
	globalStatic staticKind = iota
	localStatic
)

func (a *analyzer) isStatic(e *Expr, k staticKind) (static, known bool) {
	if k == localStatic {
		return locallyStatic(e, 0, a.std)
	}
	return globallyStatic(e, 0)
}

// staticName reports whether the name e is a static name of class k
// (LRM 8.1): every index expression and discrete range in it is a
// static expression of that class, and it dereferences no access
// value. known is false when that cannot be told.
func (a *analyzer) staticName(e *Expr, k staticKind) (static, known bool) {
	for range 64 {
		if e == nil {
			return true, false
		}
		switch e.Kind {
		case ExprName:
			return true, e.Decl != nil
		case ExprDeref:
			return false, true
		case ExprIndex, ExprSlice, ExprSelect:
			if e.Prefix != nil && !e.Prefix.Type.Unknown() &&
				e.Prefix.Type.Base().Class == TypeAccess {
				return false, true // an implicit dereference
			}
			for _, x := range e.Args {
				if s, kn := a.isStatic(x, k); !kn || !s {
					return s, kn
				}
			}
			if r := e.Range; r != nil {
				for _, x := range []*Expr{r.Left, r.Right, r.Attr} {
					if s, kn := a.isStatic(x, k); !kn || !s {
						return s, kn
					}
				}
			}
			e = e.Prefix
		default:
			return true, false
		}
	}
	return true, false
}

// generateCondition reports a generate condition that is not globally
// static (LRM 11.8).
func (a *analyzer) generateCondition(e *Expr) {
	if a.checksOff() || e == nil {
		return
	}
	if s, k := globallyStatic(e, 0); k && !s {
		a.semaError(e.Node.Span(), "the condition of a generate "+
			"statement must be globally static")
	}
}

// generateRange reports a generate parameter range that is not
// globally static (LRM 11.8).
func (a *analyzer) generateRange(rc *RangeCons) {
	if a.checksOff() || rc == nil {
		return
	}
	for _, x := range []*Expr{rc.Left, rc.Right, rc.Attr} {
		if x == nil {
			continue
		}
		if x == rc.Attr && x.Kind == ExprTypeMark {
			continue
		}
		if s, k := globallyStatic(x, 0); k && !s {
			a.semaError(rc.Node.Span(), "the range of a generate "+
				"statement must be globally static")
			return
		}
	}
}

// sensitivityName checks one name of a sensitivity list: it denotes a
// signal and is a static signal name (LRM 10.2, 11.3).
func (a *analyzer) sensitivityName(e *Expr, n cst.Node) {
	if a.checksOff() || e == nil || e.Type.Unknown() &&
		e.Kind != ExprCall {
		return
	}
	switch e.Kind {
	case ExprName, ExprIndex, ExprSlice, ExprSelect:
	case ExprCall:
		if e.Decl != nil && e.Decl.Denotes().Kind != DeclEnumLiteral {
			a.semaError(n.Span(), a.text(n)+" is not a signal name, so "+
				"it cannot be in a sensitivity list")
		}
		return
	default:
		return
	}
	d := actualObject(e)
	if d == nil {
		return
	}
	if c := objectClass(d); c != "signal" {
		if c == "" {
			c = d.Kind.String()
		}
		a.semaError(n.Span(), a.text(n)+" is a "+c+", not a signal, so "+
			"it cannot be in a sensitivity list")
		return
	}
	if s, k := a.staticName(e, globalStatic); k && !s {
		a.semaError(n.Span(), a.text(n)+" in a sensitivity list must "+
			"be a static signal name")
	}
}

// aliasRules checks an alias declaration: a nonobject alias has no
// subtype indication (LRM 6.6.1), the name of an object alias is a
// static name (LRM 6.6.2), and before VHDL-2008 it is not of a
// multidimensional array type (LRM 4.3.3.1 of VHDL-1993).
func (a *analyzer) aliasRules(ad cst.AliasDecl, object *Expr) {
	if a.checksOff() {
		return
	}
	if object == nil {
		if si := ad.Subtype(); !si.IsZero() {
			a.semaError(si.Span(), "a nonobject alias cannot have a "+
				"subtype indication")
		}
		return
	}
	switch object.Kind {
	case ExprCall, ExprAttr:
		if object.Kind == ExprAttr && signalAttr(object.Attr) {
			return
		}
		a.semaError(ad.Target().Span(), "the aliased name "+
			a.text(ad.Target().Node)+" is not the name of an object")
		return
	}
	if s, k := a.staticName(object, globalStatic); k && !s {
		a.semaError(ad.Target().Span(), "the aliased name "+
			a.text(ad.Target().Node)+" must be a static name")
		return
	}
	if a.std < token.Std2008 && !object.Type.Unknown() &&
		object.Type.Base().Class == TypeArray && object.Type.Dims() > 1 {
		a.semaError(ad.Target().Span(), "before VHDL-2008 an object "+
			"alias cannot denote a multidimensional array")
	}
}

// aggregateTarget checks the elements of an aggregate target: each is
// a locally static name, or an aggregate of them (LRM 10.5.2.1,
// 10.6.2.1).
func (a *analyzer) aggregateTarget(e *Expr, depth int, st cst.Node) {
	if a.checksOff() || e == nil || depth > 16 {
		return
	}
	for _, as := range e.Assocs {
		v := as.Value
		if v == nil {
			continue
		}
		if v.Kind == ExprAggregate {
			a.aggregateTarget(v, depth+1, st)
			continue
		}
		switch v.Kind {
		case ExprName, ExprIndex, ExprSlice, ExprSelect:
			a.targetClass(v, v.Node, st) // the class of each element
		}
		if v.Kind == ExprSlice && a.std < token.Std2008 &&
			sliceOfSibling(e, v) {
			// LRM-2002 7.3.2.1: before VHDL-2008 each element of an
			// aggregate is of the element type: a slice whose elements
			// are of another element's type is a VHDL-2008 slice
			// association.
			a.semaError(v.Node.Span(), "before VHDL-2008 an element of "+
				"an aggregate target cannot be the slice "+
				a.text(v.Node))
		}
		if s, k := a.staticName(v, localStatic); k && !s {
			a.semaError(v.Node.Span(), "the element "+a.text(v.Node)+
				" of an aggregate target must be a locally static name")
		}
	}
}

// sliceOfSibling reports whether the slice v, an element of the
// aggregate e, has elements of the type of another, non-slice element
// of e.
func sliceOfSibling(e, v *Expr) bool {
	if v.Type.Unknown() {
		return false
	}
	el := v.Type.ElemType()
	if el.Unknown() {
		return false
	}
	for _, as := range e.Assocs {
		w := as.Value
		if w == nil || w == v || w.Kind == ExprSlice ||
			w.Kind == ExprAggregate || w.Type.Unknown() {
			continue
		}
		if w.Type.Base() == el.Base() {
			return true
		}
	}
	return false
}

// formalStatic checks that the index expressions and discrete ranges
// of the formal designator fn are locally static (LRM 6.5.7.1). They
// are typed where the association is, so a name in them that is not
// declared there is reported too.
func (a *analyzer) formalStatic(fn cst.Node) {
	if a.checksOff() {
		return
	}
	var es []*Expr
	switch fn.Kind() {
	case cst.KindCallOrIndexName:
		c := cst.CallOrIndexName{Node: fn}
		for item := range c.Args().Items() {
			v := item.Actual()
			switch {
			case v.IsZero() || !item.Formal().IsZero():
			case v.Kind() == cst.KindRangeExpr:
				if rc := a.discreteRange(v.Node, nil); rc != nil {
					es = append(es, rc.Left, rc.Right, rc.Attr)
				}
			default:
				es = append(es, a.expr(v.Node, nil))
			}
		}
	case cst.KindSliceName:
		rc := a.discreteRange(sliceRange(cst.SliceName{Node: fn}), nil)
		if rc != nil {
			es = append(es, rc.Left, rc.Right, rc.Attr)
		}
	}
	for _, x := range es {
		if x == nil || x.Kind == ExprTypeMark {
			continue
		}
		if s, k := locallyStatic(x, 0, a.std); k && !s {
			a.semaError(fn.Span(), "the formal designator "+a.text(fn)+
				" must be a locally static name")
			return
		}
	}
}

// aliasesObject reports whether the alias target decls denote an
// object: an object, or an alias of one.
func aliasesObject(decls []*Decl) bool {
	for _, d := range decls {
		if d.Kind.IsObject() || d.Kind == DeclElement ||
			d.Kind == DeclAlias && d.Value != nil ||
			d.Denotes().Kind.IsObject() {
			return true
		}
	}
	return false
}

// localSubtype reports whether the array subtype t is locally static:
// constrained by locally static ranges (LRM 9.4.2).
func localSubtype(t *Type, std token.Standard) (static, known bool) {
	if t.Unknown() || t.isDynamic() || t.Base().Class != TypeArray {
		return true, false
	}
	cons := t.Constraints()
	if cons == nil {
		return false, true
	}
	for _, rc := range cons {
		if rc == nil {
			return false, true
		}
		for _, x := range []*Expr{rc.Left, rc.Right, rc.Attr} {
			if x == nil {
				continue
			}
			if s, k := locallyStatic(x, 0, std); !k || !s {
				return s, k
			}
		}
	}
	return true, true
}

// caseStatic checks the expression of a case statement or selected
// assignment of an array type before VHDL-2008: a name, a call, a
// qualified expression or a conversion whose subtype is locally static
// (LRM 8.8 of VHDL-1993; VHDL-2008 lifted the rule).
func (a *analyzer) caseStatic(sel *Expr) {
	if a.checksOff() || a.std >= token.Std2008 || sel == nil ||
		sel.illTyped || sel.Type.Unknown() ||
		sel.Type.Base().Class != TypeArray {
		return
	}
	t := sel.Type
	switch sel.Kind {
	case ExprName, ExprSelect, ExprIndex, ExprQualified,
		ExprConversion, ExprSlice:
	case ExprCall:
		d := sel.Decl.Denotes()
		if d == nil {
			return
		}
		if d.Implicit && d.Kind == DeclFunction && d.Op != 0 {
			a.semaError(sel.Node.Span(), "an operator result cannot be "+
				"a case expression before VHDL-2008: qualify it with a "+
				"locally static subtype")
			return
		}
		t = d.Type
	default:
		return
	}
	if sel.Kind == ExprSlice && sel.Range != nil {
		for _, x := range []*Expr{sel.Range.Left, sel.Range.Right} {
			if s, k := locallyStatic(x, 0, a.std); k && !s {
				a.caseStaticError(sel)
				return
			}
		}
		return
	}
	if s, k := localSubtype(t, a.std); k && !s {
		a.caseStaticError(sel)
	}
}

func (a *analyzer) caseStaticError(sel *Expr) {
	a.semaError(sel.Node.Span(), "the subtype of the case expression "+
		a.text(sel.Node)+" must be locally static before VHDL-2008")
}

// foldBound folds an integer expression of literals, T'LOW, T'HIGH,
// T'LEFT and T'RIGHT of literal-bounded type marks, and the predefined
// + and - of such. ok is false when it is not of that form; over is set
// when a step leaves 64 bits.
func foldBound(std token.Standard, e *Expr, depth int,
) (v int64, ok, over bool) {
	if e == nil || depth > 16 {
		return 0, false, false
	}
	if v, ok := position(std, e); ok {
		return v, true, false
	}
	if e.Kind != ExprCall || e.Decl == nil || !e.Decl.Implicit {
		return 0, false, false
	}
	switch {
	case len(e.Args) == 1 && e.Decl.Name == `"-"`:
		x, ok, over := foldBound(std, e.Args[0], depth+1)
		if x == math.MinInt64 {
			over = true
		}
		return -x, ok, over
	case len(e.Args) == 2 && (e.Decl.Name == `"+"` ||
		e.Decl.Name == `"-"`):
		x, ok1, o1 := foldBound(std, e.Args[0], depth+1)
		y, ok2, o2 := foldBound(std, e.Args[1], depth+1)
		if !ok1 || !ok2 {
			return 0, false, false
		}
		if e.Decl.Name == `"-"` {
			if y == math.MinInt64 {
				return 0, true, true
			}
			y = -y
		}
		r := x + y
		over := o1 || o2 || (x > 0 && y > 0 && r < 0) ||
			(x < 0 && y < 0 && r >= 0)
		return r, true, over
	}
	return 0, false, false
}

// rangeOverflow reports a bound of a loop or generate range that folds
// to a value outside the range of its type's base type, INTEGER's for
// an integer range (LRM 5.2.3.1, 9.4.1: GHDL and NVC evaluate such
// locally static bounds at analysis).
func (a *analyzer) rangeOverflow(rc *RangeCons) {
	if a.checksOff() || rc == nil || rc.Attr != nil || rc.Type.Unknown() {
		return
	}
	b := rc.Type.Base()
	if b.Class != TypeInteger {
		return
	}
	lo, hi, ok := int64(math.MinInt64), int64(math.MaxInt64), false
	if r, okr := implicitRange(a.std, b); okr {
		lo, hi, ok = min(r.Left, r.Right), max(r.Left, r.Right), true
	} else if l, h, _, okr := litRange(a.std, b.ScalarRange()); okr {
		lo, hi, ok = min(l, h), max(l, h), true
	}
	for _, x := range []*Expr{rc.Left, rc.Right} {
		v, okv, over := foldBound(a.std, x, 0)
		if !okv || !ok && !over {
			continue
		}
		if over || v < lo || v > hi {
			a.semaError(x.Node.Span(), "the bound "+a.text(x.Node)+
				" is outside the range of "+b.Name())
			return
		}
	}
}
