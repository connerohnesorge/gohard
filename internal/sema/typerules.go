package sema

import (
	"strconv"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/token"
)

// resolutionFunc picks, among the declarations ds a resolution
// function name denotes, the function that resolves t: one parameter
// of a one-dimensional array type whose element is of t's base type,
// and a result of t's base type (LRM 4.6). When functions are named
// and none fits it reports the name and returns nil.
func (a *analyzer) resolutionFunc(ds []*Decl, t *Type, n cst.Node) *Decl {
	b := t.Base()
	if t.Unknown() || b == nil || b.Class == TypeGeneric {
		return nil
	}
	funcs := 0
	for _, d := range ds {
		x := d.Denotes()
		if x.Kind != DeclFunction {
			continue
		}
		funcs++
		if len(x.Params) != 1 || x.Type == nil || x.Params[0].Type == nil {
			continue
		}
		pt := x.Params[0].Type.Base()
		if pt.Unknown() || x.Type.Unknown() {
			return d // not known: no report
		}
		if pt.Class == TypeArray && len(pt.Index) == 1 &&
			pt.Elem != nil && pt.Elem.Base() == b && x.Type.Base() == b {
			return d
		}
	}
	if funcs > 0 && !a.quiet && !a.u.File.Bundled {
		a.semaError(n.Span(), "the resolution function "+a.text(n)+
			" must take a one-dimensional array of "+t.Name()+
			" and return "+t.Name())
	}
	return nil
}

// designatedKind reports an access type whose designated subtype is a
// file type (LRM 5.4.1), or an incomplete type with a constraint (LRM
// 5.4.2: an incomplete type is named only by its type mark).
func (a *analyzer) designatedKind(t *Type, n cst.Node) {
	if a.quiet || t.Unknown() {
		return
	}
	switch b := t.Base(); {
	case b.Class == TypeFile:
		a.semaError(n.Span(), "an access type cannot designate the "+
			"file type "+t.Name())
	case b.Class == TypeIncomplete && t.IsSubtype():
		a.semaError(n.Span(), "the incomplete type "+b.Name()+
			" cannot be constrained before its full declaration")
	}
}

// genericOnly reports an interface type, subprogram or package
// declaration in a port or parameter list: they are generics only
// (LRM 6.5.1, 6.5.6.1).
func (a *analyzer) genericOnly(n cst.Node, k DeclKind) {
	if a.quiet || k == DeclGeneric {
		return
	}
	what := ""
	switch n.Kind() {
	case cst.KindInterfaceTypeDecl:
		what = "type"
	case cst.KindInterfaceSubprogramDecl:
		what = "subprogram"
	case cst.KindInterfacePackageDecl:
		what = "package"
	default:
		return
	}
	list := "port"
	if k == DeclParameter {
		list = "parameter"
	}
	a.semaError(n.Span(), "an interface "+what+" is allowed only in a "+
		"generic list, not in a "+list+" list")
}

// needsActual reports whether the generic d has no default, so every
// instance needs an actual for it (LRM 6.5.6.2): a generic constant
// without a default expression, a generic type, a generic subprogram
// without is name or is <>, a generic package.
func needsActual(d *Decl) bool {
	switch d.Kind {
	case DeclGeneric:
		return d.Value == nil
	case DeclGenericType, DeclGenericPackage:
		return true
	case DeclGenericSubprogram:
		n := d.Node
		for !n.IsZero() && n.Kind() != cst.KindInterfaceSubprogramDecl {
			n = n.Parent()
		}
		return !n.IsZero() &&
			cst.InterfaceSubprogramDecl{Node: n}.Default().IsZero()
	}
	return false
}

// aliasSignature reports an alias of a subprogram or an enumeration
// literal without a signature (LRM 6.6.3 b; lenient analysis, as NVC
// does in every mode, takes the first visible one; GHDL always reports
// it).
func (a *analyzer) aliasSignature(ad cst.AliasDecl, ds []*Decl) {
	if a.quiet || a.s.cfg.Lenient || a.u.File.Bundled ||
		!ad.Signature().IsZero() {
		return
	}
	switch k := ds[0].Denotes().Kind; {
	case k.IsSubprogram() || k == DeclGenericSubprogram:
		a.semaError(ad.Target().Span(), "an alias of a subprogram "+
			"needs a signature")
	case k == DeclEnumLiteral:
		a.semaError(ad.Target().Span(), "an alias of an enumeration "+
			"literal needs a signature")
	}
}

// aliasTypeName reports a nonobject alias whose name is a subtype
// indication with a constraint (unsigned(3 downto 0)): the aliased
// name is a type mark (LRM 6.6.1, 6.6.3).
func (a *analyzer) aliasTypeName(n cst.Node) {
	if a.quiet {
		return
	}
	switch n.Kind() {
	case cst.KindCallOrIndexName, cst.KindSliceName:
		a.semaError(n.Span(), "the name of a type alias is a type mark, "+
			"not a constrained subtype "+a.text(n))
	}
}

// rangeOperand reports a range attribute (T'RANGE, A'REVERSE_RANGE)
// used as an operand of an operator: a range is no value (LRM 9.1,
// 16.2).
func (a *analyzer) rangeOperand(e *Expr, n cst.Node) {
	if a.quiet || e == nil || e.illTyped ||
		n.Kind() != cst.KindAttributeName || e.Kind != ExprRange &&
		(e.Kind != ExprAttr || e.Attr != "range" &&
			e.Attr != "reverse_range") {
		return
	}
	e.illTyped = true
	a.semaError(n.Span(), a.text(n)+" is a range, not a value")
}

// recordGenericActual records the actual subtype e of the generic type
// f of the instance whose maps are analyzed, for the formals after it
// (LRM 6.5.7.2: in the instance the generic type denotes its actual).
func (a *analyzer) recordGenericActual(f *Decl, e *Expr) {
	if f == nil || f.Kind != DeclGenericType || f.Type == nil ||
		e == nil || e.Kind != ExprTypeMark || e.Type.Unknown() {
		return
	}
	if c := e.Type.Base().Class; (c == TypeFile || c == TypeProtected) &&
		!a.quiet && genericTypeClass(f.Type) == "" &&
		!genericPrivate(f) {
		// LRM 6.5.3: a generic type implicitly declares = and /=,
		// which a file or protected type does not have.
		a.semaError(e.Node.Span(), "the actual "+e.Type.Name()+" of "+
			"generic type "+f.Text+" has no predefined = and /=")
	}
	if a.instSubst == nil {
		a.instSubst = map[*Type]*Type{}
	}
	a.instSubst[f.Type] = e.Type
}

// substGeneric returns the actual subtype of the generic type t of the
// instance being associated, or t.
func (a *analyzer) substGeneric(t *Type) *Type {
	if s, ok := a.instSubst[t]; ok && t != nil {
		if notFullyConstrained(s, 0) {
			// Its constraint, if any, is known only when elaborated:
			// the base type is what counts here.
			return runTime(s)
		}
		return s
	}
	return t
}

// instanceUnconstrained reports whether the subtype t, named through
// the package instance in the type mark of si (inst.T), has an
// element of a generic type whose actual in the instance is not fully
// constrained: such an object needs a constraint too (LRM 6.4.2.3,
// 5.1).
func (a *analyzer) instanceUnconstrained(si cst.SubtypeIndication,
	t *Type) bool {
	tm := si.TypeMark().Node
	if tm.Kind() != cst.KindSelectedName || t.Unknown() {
		return false
	}
	p := cst.SelectedName{Node: tm}.Prefix().Node
	if p.Kind() != cst.KindSimpleName {
		return false
	}
	r := a.lookup(TokenKey(cst.SimpleName{Node: p}.Ident()))
	if len(r.Decls) != 1 || len(r.Decls[0].Denotes().instTypes) == 0 {
		return false
	}
	m := r.Decls[0].Denotes().instTypes
	open := func(x *Type) bool {
		if x == nil {
			return false
		}
		s, ok := m[x.Base()]
		if !ok {
			s, ok = m[x]
		}
		return ok && notFullyConstrained(s, 0)
	}
	switch b := t.Base(); b.Class {
	case TypeArray:
		return open(b.Elem)
	case TypeRecord:
		for i, f := range b.Fields {
			if i < len(t.FieldSubs) && t.FieldSubs[i] != nil {
				continue
			}
			if open(f.Type) {
				return true
			}
		}
	}
	return false
}

// packageActual records the generic type actuals of the package
// instance d, the actual of a generic package of the instance being
// associated: a port of type p.t takes the type that d gives t.
func (a *analyzer) packageActual(d *Decl) {
	if len(d.instTypes) == 0 {
		return
	}
	if a.instSubst == nil {
		a.instSubst = map[*Type]*Type{}
	}
	for k, v := range d.instTypes {
		a.instSubst[k] = v
	}
}

// typeConvFormal reads the formal part T(x) as a type conversion of
// the formal x when T is a visible type mark and x a formal: GHDL and
// NVC take that reading over an index of a formal named T (LRM
// 6.5.7.1 lists both forms). It returns the formal, its name token and
// T.
func (a *analyzer) typeConvFormal(fn cst.Node, formals []*Decl,
) (*Decl, cst.Token, *Type, bool) {
	c, ok := cst.As[cst.CallOrIndexName](fn)
	if !ok || c.Prefix().Kind() != cst.KindSimpleName {
		return nil, cst.Token{}, nil, false
	}
	f, tok := conversionFormal(fn, formals)
	if f == nil {
		return nil, cst.Token{}, nil, false
	}
	r := a.lookup(TokenKey(cst.SimpleName{Node: c.Prefix().Node}.Ident()))
	if len(r.Decls) != 1 || !r.Decls[0].Denotes().Kind.IsTypeMark() ||
		r.Decls[0].Denotes().Type == nil {
		return nil, cst.Token{}, nil, false
	}
	return f, tok, r.Decls[0].Denotes().Type, true
}

// unconstrainedConversion reports a type conversion actual of an
// unconstrained port whose type mark is unconstrained too: the port
// would get no index range (LRM 6.5.6.3, 6.5.7.1).
func (a *analyzer) unconstrainedConversion(e *Expr, want *Type,
	n cst.Node) {
	if a.quiet || e == nil || e.illTyped || e.Kind != ExprConversion ||
		!unconstrainedArray(want) || !unconstrainedArray(e.Type) {
		return
	}
	a.semaError(n.Span(), "the conversion "+a.text(n)+" of an actual "+
		"for an unconstrained port must have a constrained type")
}

// genericPrivate reports a VHDL-2019 generic type with an incomplete
// type definition (type t is private, is file of ..., ...), which
// declares no equality.
func genericPrivate(f *Decl) bool {
	for c := range f.Node.Nodes() {
		switch c.Kind() {
		case cst.KindPrivateIncompleteTypeDef,
			cst.KindFileIncompleteTypeDef:
			return true
		}
	}
	return false
}

// defaultMap reports generic map (default) of an interface package
// whose package has a generic without a default: default gives every
// generic its default (LRM 6.5.5).
func (a *analyzer) defaultMap(l cst.AssociationList, pkg *Decl) {
	if a.quiet || pkg == nil || pkg.Region == nil {
		return
	}
	n := 0
	def := false
	for item := range l.Items() {
		n++
		v := item.Actual().Node
		def = v.Kind() == cst.KindBoxExpr &&
			!v.Token(token.KwDefault, 0).IsZero()
	}
	if n != 1 || !def {
		return
	}
	for _, g := range pkg.Region.Decls {
		if needsActual(g) {
			a.semaError(l.Span(), "generic map (default) needs a "+
				"default for generic "+g.Text+" of "+pkg.Text)
			return
		}
	}
}

// elementLengths reports, in an array aggregate whose element subtype
// is an unconstrained array (VHDL-2008), an element value whose
// statically known length differs from the first one's: the elements
// of one array value share their subtype (LRM 9.3.3.3, 5.3.2.2).
func (a *analyzer) elementLengths(e *Expr, want *Type) {
	if a.quiet || e.illTyped || want.Unknown() {
		return
	}
	el := want.ElemType()
	if el.Unknown() || el.Base().Class != TypeArray ||
		!unconstrainedArray(el) || el.Dims() != 1 {
		return
	}
	first := int64(-1)
	for _, as := range e.Assocs {
		if as.Slice || as.Value == nil {
			continue
		}
		n, ok := sliceLen(as.Value)
		if !ok {
			continue
		}
		if first < 0 {
			first = n
			continue
		}
		if n != first {
			a.semaError(as.Value.Node.Span(), "the element "+
				a.text(as.Value.Node)+" has "+strconv.FormatInt(n, 10)+
				" elements, the first one "+strconv.FormatInt(first, 10))
			return
		}
	}
}

// conversionSyntax reports whether the actual v reads as a conversion
// function call or type conversion, name(x), whose result type is the
// formal's: the formal conversion's type then says nothing about it.
func (a *analyzer) conversionSyntax(v cst.Node) bool {
	c, ok := cst.As[cst.CallOrIndexName](v)
	if !ok || c.Prefix().Kind() != cst.KindSimpleName {
		return false
	}
	r := a.lookup(TokenKey(cst.SimpleName{Node: c.Prefix().Node}.Ident()))
	for _, d := range r.Decls {
		if x := d.Denotes(); x.Kind.IsTypeMark() || x.Kind.IsSubprogram() {
			return true
		}
	}
	return false
}
