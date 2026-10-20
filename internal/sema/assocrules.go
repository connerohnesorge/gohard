package sema

// assocrules.go holds the rules of port and generic maps that need the
// whole association list (#34): ports left without an actual (LRM
// 6.5.6.3), a formal associated twice (LRM 6.5.7.1), and the modes of
// a formal port and of the port that is its actual (LRM 6.5.6.3).

import (
	"strconv"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/token"
)

var modeNames = [...]string{ModeNone: "in", ModeIn: "in", ModeOut: "out",
	ModeInout: "inout", ModeBuffer: "buffer", ModeLinkage: "linkage",
	ModeView: "view"}

func (m Mode) text() string {
	if int(m) < len(modeNames) {
		return modeNames[m]
	}
	return "in"
}

// wholeFormal reports whether the association x names its whole
// formal: positionally or by its simple name.
func wholeFormal(x Association) bool {
	if x.Node.IsZero() {
		return false
	}
	fn := cst.AssociationElement{Node: x.Node}.Formal()
	// conversion(f) => actual associates f as a whole too.
	if fn.IsZero() || fn.Kind() == cst.KindSimpleName {
		return true
	}
	c, ok := cst.As[cst.CallOrIndexName](fn.Node)
	if !ok || x.Formal == nil || c.Prefix().Kind() != cst.KindSimpleName ||
		TokenKey(c.Prefix().FirstToken()) == x.Formal.Name {
		return false
	}
	for item := range c.Args().Items() {
		// conv(f) converts all of f; conv(f(i)) a subelement.
		return item.Actual().Kind() == cst.KindSimpleName
	}
	return false
}

// converted reports whether the formal part of x applies a conversion
// to the formal f, conversion(f) => actual.
func converted(x Association, f *Decl) bool {
	fn := cst.AssociationElement{Node: x.Node}.Formal()
	if fn.IsZero() || fn.Kind() != cst.KindCallOrIndexName {
		return false
	}
	p := cst.CallOrIndexName{Node: fn.Node}.Prefix()
	return p.Kind() != cst.KindSimpleName ||
		TokenKey(p.FirstToken()) != f.Name
}

// mapRules checks the associations as of a generic (k == DeclGeneric)
// or port map of the unit whose interface is in region; at is the
// instance, where an unassociated port is reported.
func (a *analyzer) mapRules(as []Association, region *Region, k DeclKind,
	at cst.Node,
) {
	if a.checksOff() || a.quietActuals || region == nil {
		return
	}
	whole := map[*Decl]bool{}
	given := map[*Decl]bool{}
	for _, x := range as {
		if x.Formal == nil {
			if !x.Node.IsZero() {
				return // an unknown formal: its error is reported
			}
			continue
		}
		f := x.Formal
		if wholeFormal(x) {
			if whole[f] || given[f] {
				// LRM 6.5.7.1: a formal is associated at most once.
				a.semaError(x.Node.Span(), k.String()+" "+f.Text+
					" already has an actual")
			}
			whole[f] = true
		}
		open := x.Actual == nil
		if !open || !wholeFormal(x) {
			given[f] = true
		}
		if k == DeclPort && x.Actual != nil && !converted(x, f) {
			a.portModes(f, x.Actual)
			a.portActualStatic(f, x.Actual)
		}
		if f.Kind.IsObject() && !converted(x, f) {
			if wholeFormal(x) {
				a.mapValue(f, x.Actual)
			} else {
				a.formalIndex(x, f)
			}
		}
		if open && !wholeFormal(x) && !a.s.cfg.Lenient {
			// LRM 6.5.7.1: a formal associated individually has no
			// subelement associated with open (GHDL's -frelaxed
			// allows it).
			a.semaError(x.Node.Span(), "a subelement of "+f.Text+
				" cannot be associated with open")
		}
	}
	if k != DeclPort {
		return
	}
	for _, d := range region.Decls {
		if d.Kind != DeclPort || d.Class != ClassSignal || given[d] {
			continue
		}
		switch {
		case d.Mode == ModeIn && d.Value == nil:
			// LRM 6.5.6.3: an unconnected port of mode in has a
			// default expression.
			a.semaError(at.FirstToken().Span(), "port "+d.Text+" of "+
				ownerText(region)+" has mode in and no default, "+
				"so it must be associated")
		case d.Mode != ModeIn && d.Mode != ModeView &&
			notFullyConstrained(d.Type, 0):
			// LRM 6.5.6.3: an unconnected port of another mode is
			// of a constrained type.
			a.semaError(at.FirstToken().Span(), "port "+d.Text+" of "+
				ownerText(region)+" is of an unconstrained type, so "+
				"it must be associated")
		}
	}
}

// portModes checks the mode of the formal port f against that of the
// port its actual e names (LRM 6.5.6.3; VHDL-2008 relaxed the table).
func (a *analyzer) portModes(f *Decl, e *Expr) {
	if f.Kind != DeclPort || f.Class != ClassSignal {
		return
	}
	switch e.Kind {
	case ExprName, ExprIndex, ExprSlice, ExprSelect:
	default:
		return
	}
	d := actualObject(e)
	if d == nil || d.Kind != DeclPort || d.Class != ClassSignal ||
		d.Mode == ModeView || f.Mode == ModeView {
		return
	}
	fm, am := f.Mode, d.Mode
	if fm == ModeNone {
		fm = ModeIn
	}
	if am == ModeNone {
		am = ModeIn
	}
	if portModeOK(fm, am, a.std) {
		return
	}
	a.semaError(e.Node.Span(), "port "+d.Text+" of mode "+am.text()+
		" cannot be the actual of formal port "+f.Text+" of mode "+
		fm.text())
}

// portModeOK is the table of LRM 6.5.6.3: the modes an actual port may
// have for a formal port of mode fm. A linkage formal takes any port, a
// linkage actual only a linkage formal. VHDL-2002 lets buffer and out
// or inout ports associate with each other; VHDL-2008, where out ports
// can be read, adds an out actual for an in formal.
func portModeOK(fm, am Mode, std token.Standard) bool {
	if fm == ModeLinkage {
		return true
	}
	v2002, v2008 := std >= token.Std2002, std >= token.Std2008
	switch am {
	case ModeLinkage:
		return false
	case ModeIn:
		return fm == ModeIn
	case ModeOut:
		return fm == ModeOut || v2002 && fm == ModeBuffer ||
			v2008 && fm == ModeIn
	case ModeInout:
		return fm == ModeIn || fm == ModeOut || fm == ModeInout ||
			v2002 && fm == ModeBuffer
	case ModeBuffer:
		return fm == ModeIn || fm == ModeBuffer ||
			v2002 && (fm == ModeOut || fm == ModeInout)
	}
	return true
}

// portActualStatic checks that a signal name associated with a port is
// a static signal name (LRM 6.5.6.3).
func (a *analyzer) portActualStatic(f *Decl, e *Expr) {
	if f.Kind != DeclPort || f.Class != ClassSignal {
		return
	}
	switch e.Kind {
	case ExprName, ExprIndex, ExprSlice, ExprSelect:
	default:
		return
	}
	if d := actualObject(e); d == nil || objectClass(d) != "signal" {
		return
	}
	if s, k := a.staticName(e, globalStatic); k && !s {
		a.semaError(e.Node.Span(), "the actual "+a.text(e.Node)+
			" of port "+f.Text+" must be a static signal name")
	}
}

// litRange returns the bounds and direction of a range with literal
// (or literal type attribute) bounds.
func litRange(rc *RangeCons) (l, r int64, down, ok bool) {
	if rc == nil || rc.Attr != nil {
		return 0, 0, false, false
	}
	l, ok1 := position(rc.Left)
	r, ok2 := position(rc.Right)
	return l, r, rc.Dir == eval.Downto, ok1 && ok2
}

// litLength is the length of the one-dimensional array subtype t when
// its index range has literal bounds.
func litLength(t *Type) (int64, bool) {
	if t.Unknown() || t.isDynamic() || t.Base().Class != TypeArray ||
		t.Dims() != 1 {
		return 0, false
	}
	cons := t.Constraints()
	if cons == nil {
		return 0, false
	}
	l, r, down, ok := litRange(cons[0])
	if !ok {
		return 0, false
	}
	if down {
		l, r = r, l
	}
	return max(r-l+1, 0), true
}

// mapValue checks the actual e of a whole formal f of a generic or port
// map statically: an array actual has the formal's length, a literal
// scalar actual lies in the formal's range (LRM 6.5.6.2, 6.5.6.3,
// 14.3.5; GHDL and NVC report these at analysis when both are
// literal).
func (a *analyzer) mapValue(f *Decl, e *Expr) {
	if e == nil || e.illTyped || f.Type.Unknown() {
		return
	}
	if want, ok := litLength(f.Type); ok {
		if e.Kind == ExprLiteral || e.Kind == ExprAggregate {
			return // a literal or aggregate takes its context's length
		}
		if have, ok := litLength(e.Type); ok && have != want {
			a.semaError(e.Node.Span(), "the actual of "+f.Text+" has "+
				strconv.FormatInt(have, 10)+" elements, but "+f.Text+
				" has "+strconv.FormatInt(want, 10))
		}
		return
	}
	v, ok := literalInt(e)
	if !ok || !f.Type.IsDiscrete() {
		return
	}
	l, r, _, ok := litRange(f.Type.ScalarRange())
	if !ok {
		return
	}
	if v < min(l, r) || v > max(l, r) {
		a.semaError(e.Node.Span(), "the actual "+a.text(e.Node)+" of "+
			f.Text+" is outside the range "+strconv.FormatInt(l, 10)+
			" to "+strconv.FormatInt(r, 10))
	}
}

// formalIndex checks an indexed formal f(i) => actual and a sliced one
// f(l to r) => actual: a literal index lies in the formal's index range
// and the slice has the actual's length (LRM 6.5.7.1, 8.4, 8.5).
func (a *analyzer) formalIndex(x Association, f *Decl) {
	fn := cst.AssociationElement{Node: x.Node}.Formal()
	if fn.IsZero() || f.Type.Unknown() || f.Type.Dims() != 1 {
		return
	}
	cons := f.Type.Constraints()
	if cons == nil {
		return
	}
	lo, hi, _, ok := litRange(cons[0])
	if !ok {
		return
	}
	lo, hi = min(lo, hi), max(lo, hi)
	saved := a.quiet
	a.quiet = true
	defer func() { a.quiet = saved }()
	switch fn.Kind() {
	case cst.KindCallOrIndexName:
		c := cst.CallOrIndexName{Node: fn.Node}
		n := 0
		var arg cst.Node
		for item := range c.Args().Items() {
			arg, n = item.Actual().Node, n+1
		}
		if n != 1 || arg.Kind() == cst.KindRangeExpr {
			return
		}
		v, ok := literalInt(a.expr(arg, nil))
		if ok && (v < lo || v > hi) {
			a.quiet = saved
			a.semaError(fn.Span(), "the index "+a.text(arg)+" of "+
				f.Text+" is outside its range")
		}
	case cst.KindSliceName:
		rc := a.discreteRange(sliceRange(cst.SliceName{Node: fn.Node}), nil)
		l, r, down, ok := litRange(rc)
		if !ok || x.Actual == nil {
			return
		}
		if down {
			l, r = r, l
		}
		have, ok := litLength(x.Actual.Type)
		if want := max(r-l+1, 0); ok && have != want &&
			x.Actual.Kind != ExprLiteral && x.Actual.Kind != ExprAggregate {
			a.quiet = saved
			a.semaError(x.Actual.Node.Span(), "the actual of "+
				a.text(fn.Node)+" has "+strconv.FormatInt(have, 10)+
				" elements, but the formal has "+
				strconv.FormatInt(want, 10))
		}
	}
}

// bindingGenerics reports the generic constants of the entity a
// binding indication with a generic map aspect binds that have no
// default and no actual in it (LRM 6.5.6.2, 7.3.2.2): an explicit
// generic map replaces the default binding of the component's
// generics. at is the entity aspect.
func (a *analyzer) bindingGenerics(as []Association, region *Region,
	at cst.Node,
) {
	if a.checksOff() || region == nil {
		return
	}
	given := map[*Decl]bool{}
	for _, x := range as {
		if x.Formal != nil {
			given[x.Formal] = true
		}
	}
	for _, d := range region.Decls {
		if d.Kind == DeclGeneric && d.Value == nil && !given[d] {
			a.semaError(at.Span(), "generic "+d.Text+" of "+
				ownerText(region)+" has no default and no actual in "+
				"the binding's generic map")
		}
	}
}

// formalForm reports whether the formal part fn has the form of a
// formal designator, a name or a conversion of one (LRM 6.5.7.1), and
// reports it when it has not: an attribute name, a literal or an
// operator is no formal.
func (a *analyzer) formalForm(fn cst.Node) bool {
	switch fn.Kind() {
	case cst.KindSimpleName, cst.KindOperatorSymbol, cst.KindSelectedName,
		cst.KindCallOrIndexName, cst.KindSliceName, cst.KindErrorNode:
		return true
	}
	if !a.checksOff() {
		a.semaError(fn.Span(), a.text(fn)+" is not a formal designator")
	}
	return false
}
