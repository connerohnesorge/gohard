package sema

import "github.com/connerohnesorge/gohard/internal/cst"

// notIndexable reports prefix(...) where the prefix p denotes no value
// and no subprogram: a label, a design unit, a package or a component
// (LRM 8.4, 8.5: the prefix of an indexed or slice name is a name of
// an array object or value, or a function call).
func (a *analyzer) notIndexable(p named, n cst.Node, how string) {
	if a.quiet || p.unknown || len(p.decls) == 0 {
		return
	}
	d := p.decls[0].Denotes()
	switch d.Kind {
	case DeclLabel, DeclEntity, DeclArchitecture, DeclPackage,
		DeclPackageInst, DeclConfiguration, DeclContext, DeclComponent,
		DeclPackageBody:
		a.semaError(n.Span(), d.Kind.String()+" "+p.decls[0].Text+
			" cannot be "+how)
	}
}

// conversionPrefix reports a type conversion used as the prefix of an
// indexed or slice name: a conversion is no name and no function call
// (LRM 8.1, 9.3.6).
func (a *analyzer) conversionPrefix(pn cst.Node, v *Expr) {
	if a.quiet || v == nil || v.Kind != ExprConversion {
		return
	}
	a.semaError(pn.Span(), "a type conversion cannot be indexed or "+
		"sliced")
}

// attrNotArray reports a user-defined attribute name with an index
// whose value is not of a one-dimensional array type (LRM 8.4).
func (a *analyzer) attrNotArray(n cst.Node, v *Expr) {
	if a.quiet || v.Type.Unknown() || v.illTyped {
		return
	}
	a.semaError(n.Span(), a.text(n)+" is "+v.Type.Name()+", which "+
		"cannot be indexed")
}

// unitName reports an instantiated unit name that is not a simple or
// expanded name, or that denotes more than one declaration (homographs
// already reported where declared): it denotes no single component,
// entity or configuration (LRM 11.7.1).
func (a *analyzer) unitName(iu, n cst.Node, r named) {
	if a.quiet {
		return
	}
	if n.IsZero() {
		for c := range iu.Nodes() {
			n = c // not a name: the parser kept the node
			break
		}
	}
	switch n.Kind() {
	case cst.KindSimpleName, cst.KindSelectedName:
		if len(r.decls) > 1 && !r.unknown {
			a.semaError(n.Span(), a.text(n)+" denotes more than one "+
				"declaration, not a unit to instantiate")
		}
	case cst.KindErrorNode, 0:
	case cst.KindAggregate:
		// label: (a, b) port map (...) reads as a concurrent signal
		// assignment to an aggregate target whose <= is missing.
		at := n.Span()
		if next := iu.Parent().Child[cst.PortMapAspect](0); !next.IsZero() {
			at = next.FirstToken().Span()
		}
		a.semaError(at, "an aggregate is no instantiated unit; as a "+
			"signal assignment target it needs <= here")
	default:
		a.semaError(n.Span(), a.text(n)+" is not the name of a unit to "+
			"instantiate")
	}
}

// ambiguousSlice reports f(range) where several parameterless calls of
// f return a one-dimensional array: the slice cannot tell which f is
// called (LRM 8.5, 12.5). It reports whether it did.
func (a *analyzer) ambiguousSlice(ds []*Decl, args cst.AssociationList,
	n cst.Node) bool {
	if a.quiet || !allRanges(args) {
		return false
	}
	k := 0
	for range args.Items() {
		k++
	}
	if k != 1 {
		return false
	}
	return a.ambiguousSliced(ds, n)
}

// ambiguousSliced reports n, a slice of the result of a parameterless
// call of the overloaded ds, when more than one of ds can be called so
// and returns a one-dimensional array; it reports whether it did.
func (a *analyzer) ambiguousSliced(ds []*Decl, n cst.Node) bool {
	if a.quiet {
		return false
	}
	fits := 0
	for _, d := range ds {
		x := d.Denotes()
		if x.Kind != DeclFunction || required(x) > 0 || x.Type == nil {
			continue
		}
		at := x.Type
		if b := at.Base(); b.Class == TypeAccess && b.Designated != nil {
			at = b.Designated
		}
		if b := at.Base(); b.Class == TypeArray && len(b.Index) == 1 {
			fits++
		}
	}
	if fits < 2 {
		return false
	}
	a.semaError(n.Span(), a.text(n)+" is ambiguous: more than one "+
		"parameterless "+ds[0].Text+" returns an array to slice")
	return true
}
