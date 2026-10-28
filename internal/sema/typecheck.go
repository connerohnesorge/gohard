package sema

import (
	"strconv"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// stringMismatch reports a string or bit-string literal whose context
// type t is not a one-dimensional array of a character type holding
// its characters (LRM 9.3.2): the literal's type comes from the
// context alone, so "1" is no STD_LOGIC and "tt" no BOOLEAN.
func (a *analyzer) stringMismatch(e *Expr, t *Type) bool {
	if t.Base().Class == TypeGeneric || stringFits(e.Lit, t) {
		return false
	}
	if !a.quiet && !e.illTyped {
		e.illTyped = true
		a.report(e.Node.Span(), diag.New(diag.SemaTypeMismatch, t.Name(),
			"a string literal"))
	}
	return true
}

// aggregateMismatch reports an aggregate whose context type t is not
// composite (LRM 9.3.3.1: an aggregate is a value of an array or a
// record type).
func (a *analyzer) aggregateMismatch(e *Expr, t *Type) {
	if a.quiet || e.illTyped || t.Unknown() {
		return
	}
	switch t.Base().Class {
	case TypeArray, TypeRecord, TypeGeneric, TypeUnknown,
		TypeIncomplete:
		return
	}
	e.illTyped = true
	a.report(e.Node.Span(), diag.New(diag.SemaTypeMismatch, t.Name(),
		"an aggregate"))
}

// contextless reports the pending aggregate or string literal e
// assigned to an aggregate target (t is anyComposite). The target's
// type comes from the value alone (LRM 10.5.2.1, 10.6.2.1), and the
// type of an aggregate or string literal comes from its context alone
// (LRM 9.3.2, 9.3.3.1), so neither has one. e stays unknown.
func (a *analyzer) contextless(e *Expr, t *Type) bool {
	if t != anyComposite || (e.pend.kind != pendAggregate &&
		e.pend.kind != pendString) || otherValueTyped(e.Node) {
		return false
	}
	e.pend = nil
	if e.Kind == ExprAggregate {
		*e = *a.aggregate(e.Node, nil) // for the references inside
	}
	e.Type = UnknownType
	if !a.quiet && !e.illTyped {
		e.illTyped = true
		what := "an aggregate"
		if e.Kind == ExprLiteral {
			what = "a string literal"
		}
		a.semaError(e.Node.Span(), "the type of "+what+" assigned to "+
			"an aggregate target cannot be determined: qualify it")
	}
	return true
}

// rangeMismatch reports a discrete range of type t where the context
// expects a range of type want, such as an index constraint or slice
// of an array indexed by another type (LRM 5.3.2.2, 8.5): the range's
// bounds are of its type, which is the index type.
func (a *analyzer) rangeMismatch(n cst.Node, t, want *Type) {
	if a.quiet || t.Unknown() || want.Unknown() || t.Universal ||
		want.Universal || t.Base().Class == TypeGeneric ||
		want.Base().Class == TypeGeneric || Compatible(t, want) {
		return
	}
	a.report(n.Span(), diag.New(diag.SemaTypeMismatch, want.Name(),
		t.Name()))
}

// otherValueTyped reports whether n is one alternative of a
// conditional or selected assignment another alternative of which is
// neither an aggregate nor a string literal: that one gives the target
// its type (LRM 10.5.3, 10.5.4).
func otherValueTyped(n cst.Node) bool {
	for p := n.Parent(); !p.IsZero(); p = p.Parent() {
		switch p.Kind() {
		case cst.KindParenExpr, cst.KindWaveformElement,
			cst.KindWaveform, cst.KindWhenBranch, cst.KindSelectBranch:
			continue
		case cst.KindConditionalWaveforms, cst.KindSelectedWaveforms,
			cst.KindConditionalExpressions,
			cst.KindSelectedExpressions:
			for c := range p.Nodes() {
				var v cst.Node
				switch c.Kind() {
				case cst.KindWhenBranch:
					v = cst.WhenBranch{Node: c}.Value().Node
				case cst.KindSelectBranch:
					v = cst.SelectBranch{Node: c}.Value().Node
				default:
					continue
				}
				if valueTyped(v) {
					return true
				}
			}
		}
		return false
	}
	return false
}

// valueTyped reports whether the value v of an alternative is neither
// an aggregate nor a string literal (in parentheses or as the first
// element of a waveform).
func valueTyped(v cst.Node) bool {
	for range 16 {
		switch v.Kind() {
		case cst.KindParenExpr:
			v = cst.ParenExpr{Node: v}.Inner().Node
			continue
		case cst.KindWaveform:
			for el := range (cst.Waveform{Node: v}).WaveformElements() {
				return valueTyped(el.Value().Node)
			}
			return false
		case cst.KindAggregate:
			return false
		case cst.KindLiteralExpr:
			k := cst.LiteralExpr{Node: v}.Literal().Kind()
			return k != token.StringLiteral &&
				k != token.BitStringLiteral
		}
		return !v.IsZero()
	}
	return false
}

// caseSelector reports the expression of a case statement or selected
// assignment whose type cannot select (LRM 10.9): it is of a discrete
// type or a one-dimensional array type of a character type; for a
// matching case (case?) BIT, STD_ULOGIC or a one-dimensional array of
// either.
func (a *analyzer) caseSelector(sel *Expr, matching bool) {
	if a.quiet || sel == nil || sel.illTyped || sel.Type.Unknown() {
		return
	}
	b := sel.Type.Base()
	if b.Class == TypeGeneric {
		return
	}
	if matching {
		el := b
		if b.Class == TypeArray && len(b.Index) == 1 {
			el = b.Elem.Base()
		}
		if el == nil || el.Unknown() || a.bitOrULogic(el) {
			return
		}
		sel.illTyped = true
		a.semaError(sel.Node.Span(), "the expression of a matching "+
			"case must be BIT, STD_ULOGIC or a one-dimensional array "+
			"of them, not "+sel.Type.Name())
		return
	}
	if b.IsDiscrete() {
		return
	}
	if b.Class == TypeArray && len(b.Index) == 1 {
		el := b.Elem.Base()
		if el == nil || el.Unknown() || charEnum(el) {
			return
		}
	}
	sel.illTyped = true
	a.semaError(sel.Node.Span(), "the case expression must be of a "+
		"discrete type or a one-dimensional character array type, not "+
		sel.Type.Name())
}

// bitOrULogic reports whether the base type b is BIT or STD_ULOGIC.
func (a *analyzer) bitOrULogic(b *Type) bool {
	d := b.Decl
	if d == nil {
		return false
	}
	return a.isStdType(d, "bit") || d.Unit != nil &&
		d.Unit.Library == "ieee" && d.Unit.Name == "std_logic_1164" &&
		d.Name == "std_ulogic"
}

// aliasSubtype reports an object alias whose subtype indication t has
// another base type than the aliased object (LRM 6.6.2).
func (a *analyzer) aliasSubtype(ad cst.AliasDecl, t *Type, object *Expr) {
	if a.quiet || t.Unknown() || object.illTyped ||
		object.Type.Unknown() || t.Base().Class == TypeGeneric ||
		object.Type.Base().Class == TypeGeneric ||
		SameBase(t, object.Type) {
		return
	}
	a.semaError(ad.Subtype().Span(), "the alias subtype "+t.Name()+
		" is not of the base type of "+a.text(ad.Target().Node)+", "+
		object.Type.Name())
}

// indexesRelated reports whether, before VHDL-2008, the index types
// of the array types f and t are closely related at each position
// (LRM-93 7.3.5; VHDL-2008 dropped the rule, LRM 9.3.6).
func indexesRelated(f, t *Type, std token.Standard) bool {
	for i := range f.Index {
		if !closelyRelated(f.Index[i], t.Index[i], std) {
			return false
		}
	}
	return true
}

// contextOperand reports the operand x of a type conversion whose type
// only a context could give: the operand is a complete context (LRM
// 9.3.6), so it is no string literal or aggregate (LRM 9.3.2, 9.3.3.1)
// and no enumeration literal or parameterless function overloaded on
// its type (LRM 12.5). x is settled without a type.
func (a *analyzer) contextOperand(x *Expr) bool {
	p := x.pend
	if p == nil {
		return false
	}
	what := ""
	switch p.kind {
	case pendString:
		what = "a string literal"
	case pendAggregate:
		what = "an aggregate"
	case pendChar, pendName:
		if commonType(p.cands) != nil || sameDenotation(p.cands) {
			return false
		}
		what = p.tok.Text() + ", which is overloaded,"
	default:
		return false
	}
	a.settle(x, nil)
	if !a.quiet && !x.illTyped {
		x.illTyped = true
		a.semaError(x.Node.Span(), what+" cannot be the operand of a "+
			"type conversion: its type must not depend on the context")
	}
	return true
}

// openAllocation reports an allocator new S whose subtype S leaves an
// index range open: without an initial value the allocated object
// takes its constraint from S alone (LRM 9.3.7), which must be
// constrained, fully so since VHDL-2008.
func (a *analyzer) openAllocation(n cst.Node, s *Type) bool {
	open := unconstrainedArray(s)
	if a.std >= token.Std2008 {
		open = notFullyConstrained(s, 0)
	}
	if a.quiet || !open {
		return false
	}
	a.semaError(n.Span(), "an allocator without an initial "+
		"value needs a constrained subtype, and "+s.Name()+" is not")
	return true
}

// physicalDef checks a physical type definition (LRM 5.2.4.1): its
// range is of an integer type, and the abstract literal of each
// secondary unit is an integer literal.
func (a *analyzer) physicalDef(pd cst.PhysicalTypeDef, t *Type) {
	if a.quiet {
		return
	}
	if r := t.Range; r != nil && !r.Type.Unknown() &&
		r.Type.Base().Class == TypeFloating {
		a.semaError(pd.Range().Span(), "the range of a physical type "+
			"must be of an integer type, not floating point")
	}
	for su := range pd.Secondary() {
		v := su.Value().Node
		if v.Kind() != cst.KindPhysicalLiteral {
			continue
		}
		if lit := (cst.PhysicalLiteral{Node: v}).Value(); !lit.IsZero() &&
			lit.Kind() == token.RealLiteral {
			a.semaError(lit.Span(), "a secondary unit is defined by an "+
				"integer literal, not "+lit.Text())
		}
	}
}

// positionalAfterNamed reports a positional actual of a call after a
// named one (LRM 6.5.7.1: positional associations come first).
func (a *analyzer) positionalAfterNamed(acts []actual) {
	if a.quiet {
		return
	}
	named := false
	for _, ac := range acts {
		switch {
		case ac.formal != "" || !ac.fpart.IsZero():
			named = true
		case named:
			a.semaError(ac.node.Span(), "a positional actual cannot "+
				"follow a named one")
			return
		}
	}
}

// recordOthers types the choice others of a record aggregate of
// subtype want, given the elements covered before it, and returns the
// type of its value: others stands for at least one element, and all
// the elements it stands for are of one type (LRM 9.3.3.2).
func (a *analyzer) recordOthers(c cst.Node, want *Type, covered []bool,
) *Type {
	b := want.Base()
	var t *Type
	n := 0
	for i, f := range b.Fields {
		if i < len(covered) && covered[i] {
			continue
		}
		ft := want.FieldType(f)
		n++
		switch {
		case t == nil:
			t = ft
		case !t.Unknown() && !ft.Unknown() && !SameBase(t, ft):
			if !a.quiet {
				a.semaError(c.Span(), "others stands for elements of "+
					"different types, "+t.Name()+" and "+ft.Name())
			}
			return nil
		}
		if i < len(covered) {
			covered[i] = true
		}
	}
	if n == 0 && !a.quiet && len(b.Fields) > 0 {
		a.semaError(c.Span(), "others stands for no element of "+
			want.Name())
	}
	return t
}

// positionalAfterOthers reports a positional association after the
// choice others in an aggregate (LRM 9.3.3.1: others comes last and
// positional associations first); syntaxchecks.go reports a named
// association after others.
func (a *analyzer) positionalAfterOthers(e *Expr) {
	if a.quiet {
		return
	}
	others := false
	for _, as := range e.Assocs {
		switch {
		case as.Others:
			others = true
		case others && len(as.Choices) == 0 && as.Value != nil:
			a.semaError(as.Value.Node.Span(), "a positional "+
				"association cannot follow others")
			return
		}
	}
}

// constraintFits reports a constraint that cannot apply to the
// subtype base (LRM 6.3, 5.3.2.2, 5.3.3): a range constraint applies
// to a scalar type, an index constraint to an array type (or an access
// type designating one) and gives one range per index, a record
// constraint to a record type; an interface type takes no constraint.
// It returns whether the constraint applies.
func (a *analyzer) constraintFits(c cst.Node, base *Type) bool {
	if base.Unknown() {
		return true
	}
	b := base.Base()
	if c.Kind() == cst.KindArrayConstraint && b.Class == TypeAccess &&
		!b.Designated.Unknown() {
		b = b.Designated.Base()
	}
	what := ""
	switch c.Kind() {
	case cst.KindRangeConstraint:
		if b.IsScalar() || genericScalar(b) {
			return true
		}
		what = "a range constraint"
	case cst.KindArrayConstraint:
		if b.Class == TypeArray || b.Class == TypeGeneric && b.Index != nil {
			n := 0
			for range (cst.ArrayConstraint{Node: c}).Ranges() {
				n++
			}
			if n == len(b.Index) || a.quiet {
				return true
			}
			a.semaError(c.Span(), "the index constraint gives "+
				strconv.Itoa(n)+" ranges, but "+base.Name()+" has "+
				strconv.Itoa(len(b.Index))+" indexes")
			return false
		}
		what = "an index constraint"
	case cst.KindRecordConstraint:
		if b.Class == TypeRecord {
			return true
		}
		what = "a record constraint"
	default:
		return true
	}
	switch b.Class {
	case TypeIncomplete, TypeUnknown:
		return true
	}
	if !a.quiet {
		a.semaError(c.Span(), what+" cannot apply to "+base.Name()+
			", a type of class "+b.Class.String())
	}
	return false
}

// genericScalar reports whether b is a VHDL-2019 generic type of a
// scalar class (type t is <>, is (<>), is range <>, is units <>, is
// range <>.<>; LRM 6.5.3), which takes a range constraint.
func genericScalar(b *Type) bool {
	if b.Class != TypeGeneric || b.Decl == nil || b.Decl.Node.IsZero() {
		return false
	}
	for c := range b.Decl.Node.Nodes() {
		switch c.Kind() {
		case cst.KindScalarIncompleteTypeDef,
			cst.KindDiscreteIncompleteTypeDef,
			cst.KindIntegerIncompleteTypeDef,
			cst.KindPhysicalIncompleteTypeDef,
			cst.KindFloatingIncompleteTypeDef:
			return true
		}
	}
	return false
}

// openElement reports, before VHDL-2008, an array or record element of
// an unconstrained array subtype (LRM-93 3.2.1, 3.2.1.1: an element
// subtype is constrained; VHDL-2008 allows it, LRM 5.3.2.1).
func (a *analyzer) openElement(t *Type, n cst.Node) {
	if a.quiet || a.std >= token.Std2008 || !unconstrainedArray(t) {
		return
	}
	a.semaError(n.Span(), "before VHDL-2008 an element of a composite "+
		"type cannot be of the unconstrained array type "+t.Name())
}

// bodySeen reports whether the body of the protected type declared by
// spec precedes the current point in an enclosing region.
func (a *analyzer) bodySeen(spec *Decl) bool {
	for r := a.cur; r != nil; r = r.Parent {
		for _, c := range r.Children {
			if c.Kind == RegionProtectedBody && c.Owner == spec {
				return true
			}
		}
	}
	return false
}

// secondBody reports a second body of the protected type spec in one
// region (LRM 5.6.3: a protected type has one body).
func (a *analyzer) secondBody(tok cst.Token, spec *Decl) {
	if a.quiet || a.cur == nil {
		return
	}
	for _, c := range a.cur.Children {
		if c.Kind == RegionProtectedBody && c.Owner == spec {
			a.semaError(tok.Span(), "protected type "+spec.Text+
				" already has a body")
			return
		}
	}
}

// protectedElaborated reports a shared variable of a protected type
// whose body is elaborated after it: the body of the type of a
// variable must have been elaborated when the variable is (LRM
// 14.4.2.5), so it precedes the variable in the same unit, or in the
// package body of the variable's package when the type is declared in
// the package.
func (a *analyzer) protectedElaborated(si cst.SubtypeIndication,
	k DeclKind, t *Type,
) {
	if a.quiet || k != DeclSharedVariable || t.Unknown() ||
		t.Base().Class != TypeProtected {
		return
	}
	spec := t.Base().Decl
	if in := t.Base().Inst; in != nil && in.Of != nil {
		spec = in.Of // an instance runs its generic type's body
	}
	if spec == nil || spec.Unit == nil || a.u == nil {
		return
	}
	su := spec.Unit
	ownBody := a.u.Kind == UnitPackageBody && su.Kind == UnitPackage &&
		a.u.Of == su.Name && a.u.Library == su.Library
	if (su != a.u && !ownBody) || a.bodySeen(spec) {
		return
	}
	a.semaError(si.Span(), "shared variable of protected type "+
		spec.Text+" declared before the body of "+spec.Text+
		" is elaborated")
}

// designated reports, before VHDL-2019, an access type designating a
// protected type (LRM-2008 5.4.1; VHDL-2019 allows it).
func (a *analyzer) designated(t *Type, n cst.Node) {
	a.designatedKind(t, n) // typerules.go
	if a.quiet || a.std >= token.Std2019 || !protected(t) {
		return
	}
	a.semaError(n.Span(), "before VHDL-2019 an access type cannot "+
		"designate the protected type "+t.Name())
}

// methodTypes reports, before VHDL-2019, a method of a protected type
// with a parameter or result of an access or file type, or of a
// composite type with an access subelement (LRM-2008 5.6.2; VHDL-2019
// allows them).
func (a *analyzer) methodTypes(r *Region) {
	if a.quiet || a.std >= token.Std2019 || r == nil {
		return
	}
	bad := func(t *Type) bool {
		if t.Unknown() {
			return false
		}
		b := t.Base()
		return b.Class == TypeAccess || b.Class == TypeFile ||
			hasAccessPart(b, 0)
	}
	for _, d := range r.Decls {
		if d.Kind != DeclFunction && d.Kind != DeclProcedure {
			continue
		}
		for _, p := range d.Params {
			if p.Type != nil && (bad(p.Type) || p.Class == ClassFile) {
				a.semaError(p.Span, "before VHDL-2019 a parameter of "+
					"a method cannot be of an access or file type or "+
					"contain an access type")
			}
		}
		if d.Kind == DeclFunction && d.Type != nil && bad(d.Type) {
			a.semaError(d.Span, "before VHDL-2019 a method cannot "+
				"return an access or file type or a type containing "+
				"an access type")
		}
	}
}

// useHomographs reports, before VHDL-2008, an explicit candidate x
// that is a homograph of an implicit candidate of top declared in
// another region, where x is only visible through a use clause:
// potentially visible homographs are then both hidden (LRM-93 10.4),
// so the call is ambiguous. VHDL-2008 lets the explicit declaration
// hide the implicit one (LRM 12.4).
func (a *analyzer) useHomographs(x *candidate, top []*candidate) bool {
	if a.std >= token.Std2008 {
		return false
	}
	xd := x.d.Denotes()
	for r := a.cur; r != nil; r = r.Parent {
		if r == xd.Parent {
			return false // x is declared in an enclosing region
		}
	}
	for _, c := range top {
		cd := c.d.Denotes()
		if c != x && cd.Parent != xd.Parent && homographs(xd, cd) {
			return true
		}
	}
	return false
}

// ambiguousValue reports an enumeration literal or parameterless
// function call that the context type t leaves with more than one
// meaning of one type: homographs of different packages, both
// visible, are both of t (LRM 12.5: the context decides exactly one
// interpretation).
func (a *analyzer) ambiguousValue(e *Expr, o *Overload, t *Type) {
	if a.quiet || t.Unknown() || t.Base().Class == TypeGeneric ||
		e.illTyped || a.brokenUses() {
		return
	}
	var fit []*Decl
	for _, c := range o.Candidates {
		x := c.Decl.Denotes()
		if c.Fits && (x.Kind == DeclEnumLiteral ||
			match(c.Decl, nil, nil, false).score > 0) {
			fit = append(fit, c.Decl)
		}
	}
	if len(fit) < 2 || sameDenotation(fit) {
		return // the others need arguments
	}
	e.illTyped = true
	a.report(e.Node.Span(), diag.New(diag.SemaAmbiguousOverload, o.Name,
		fitting(o)).WithRelated(candidateNotes(o, true)...))
}

// notTypeMark reports a subtype indication whose type mark is an
// expression that is no name, as in `for i in 1 range 1 to 2` (LRM
// 6.3: a type mark names a type or subtype).
func (a *analyzer) notTypeMark(si cst.SubtypeIndication) bool {
	for c := range si.Nodes() {
		k := c.Kind()
		if !(cst.Expr{}).Accepts(k) || (cst.Name{}).Accepts(k) ||
			k == cst.KindErrorNode {
			return false
		}
		if !a.quiet {
			a.semaError(c.Span(), a.text(c)+" is not a type mark")
		}
		return true
	}
	return false
}

// rangeValue reports a range attribute ('RANGE, 'REVERSE_RANGE) where
// the context expects a value (LRM 16.2: they denote ranges, which are
// no expressions, LRM 9.1).
func (a *analyzer) rangeValue(e *Expr, n cst.Node, want *Type) {
	if a.quiet || e == nil || e.illTyped || want.Unknown() ||
		n.Kind() != cst.KindAttributeName || e.Kind != ExprRange &&
		(e.Kind != ExprAttr || e.Attr != "range" &&
			e.Attr != "reverse_range") {
		return
	}
	e.illTyped = true
	a.semaError(n.Span(), a.text(n)+" is a range, not a value")
}
