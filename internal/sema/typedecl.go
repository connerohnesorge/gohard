package sema

import (
	"slices"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/token"
)

// This file builds the type representation from type and subtype
// declarations and subtype indications (LRM 5, 6.3).

// typeDecl analyzes a full type declaration.
func (a *analyzer) typeDecl(td cst.TypeDecl) {
	tok := td.Name()
	def := td.Def().Node
	if def.Kind() == cst.KindProtectedTypeBody {
		a.protectedBody(tok, cst.ProtectedTypeBody{Node: def})
		return
	}
	d := a.newDecl(DeclType, tok, td.Node)
	// A full declaration completes an incomplete one of the same
	// region (LRM 5.4.2): it fills the same Type so access types
	// declared in between see the full type.
	var t *Type
	for _, old := range a.cur.Lookup(d.Name) {
		if old.Kind == DeclType && old.Type != nil &&
			old.Type.Class == TypeIncomplete {
			t, d.Spec = old.Type, old
			a.u.Refs = append(a.u.Refs, Ref{File: a.f, Span: d.Span,
				Decl: old, Kind: RefBody})
			break
		}
	}
	fill := func(nt *Type) *Type {
		nt.Decl = d
		if t != nil {
			*t = *nt
			return t
		}
		return nt
	}
	switch def.Kind() {
	case cst.KindEnumerationTypeDef:
		t = fill(&Type{Class: TypeEnum})
		d.Type = t
		a.declare(d)
		a.stdLayout(t)
		i := 0
		for lit := range (cst.EnumerationTypeDef{Node: def}).Literals() {
			ld := a.newDecl(DeclEnumLiteral, lit, def)
			ld.Full = lit.Span()
			ld.Type, ld.Pos, ld.First = t, i, d.First
			t.Literals = append(t.Literals, ld)
			i++
			if a.inStandard && sameTypeLiteral(a.cur, ld) {
				// The vendored std.standard spells NBSP as ' ' and
				// the soft hyphen as '-' (positions 160 and 173): the
				// literal keeps its position but its name stays the
				// first one's.
				continue
			}
			a.declare(ld)
		}
		a.predefined(t, d)
	case cst.KindRangeTypeDef:
		rc := cst.RangeTypeDef{Node: def}.Range()
		t = fill(a.rangeTypeDef(rc, d))
		d.Type = t
		a.declare(d)
		a.stdLayout(t)
		a.predefined(t, d)
	case cst.KindPhysicalTypeDef:
		t = fill(&Type{Class: TypePhysical})
		d.Type = t
		pd := cst.PhysicalTypeDef{Node: def}
		if !a.implementationDefined(pd.Range().Node) {
			a.typeDef = true
			t.Range = a.rangeConstraint(pd.Range().Range().Node, nil)
			a.typeDef = false
		}
		a.declare(d)
		a.stdLayout(t)
		if p := pd.Primary(); !p.IsZero() {
			ud := a.newDecl(DeclUnit, p.Name(), p.Node)
			ud.Type, ud.First = t, d.First
			t.Units = append(t.Units, ud)
			a.declare(ud)
		}
		for su := range pd.Secondary() {
			ud := a.newDecl(DeclUnit, su.Name(), su.Node)
			ud.Type, ud.First = t, d.First
			ud.Value = a.expr(su.Value().Node, t)
			t.Units = append(t.Units, ud)
			a.declare(ud)
		}
		a.physicalDef(pd, t) // typecheck.go
		a.endClause(pd.End(), d)
		a.predefined(t, d)
	case cst.KindUnboundedArrayDef:
		ad := cst.UnboundedArrayDef{Node: def}
		nt := &Type{Class: TypeArray}
		for ix := range ad.Indexes() {
			nt.Index = append(nt.Index, a.typeMark(ix.TypeMark().Node))
		}
		nt.Elem = a.subtypeIndication(ad.ElementSubtype())
		a.noFileElement(nt.Elem, ad.ElementSubtype().Node)
		t = fill(nt)
		d.Type = t
		a.declare(d)
		a.stdLayout(t)
		a.predefined(t, d)
	case cst.KindConstrainedArrayDef:
		ad := cst.ConstrainedArrayDef{Node: def}
		base := &Type{Class: TypeArray}
		sub := &Type{Class: TypeArray, base: base}
		a.rangeForm(ad.Constraint().Node) // forms.go
		for dr := range ad.Constraint().Ranges() {
			rc := a.discreteRange(dr.Node, nil)
			idx := UnknownType
			if rc != nil && rc.Type != nil {
				idx = rc.Type
			}
			base.Index = append(base.Index, idx)
			sub.IndexCons = append(sub.IndexCons, rc)
		}
		base.Elem = a.subtypeIndication(ad.ElementSubtype())
		a.noFileElement(base.Elem, ad.ElementSubtype().Node)
		t = fill(sub)
		d.Type = t
		a.declare(d)
		a.predefined(base, d)
	case cst.KindRecordTypeDef:
		rd := cst.RecordTypeDef{Node: def}
		t = fill(&Type{Class: TypeRecord})
		d.Type = t
		a.declare(d)
		r := a.push(RegionRecord, d, def)
		_ = r
		for ed := range rd.ElementDecls() {
			et := a.subtypeIndication(ed.Subtype())
			a.noFileElement(et, ed.Subtype().Node)
			if et.Base() == t {
				// The record's name is not visible as an element
				// type inside its own declaration (LRM 12.3): a
				// record cannot contain itself.
				if !a.quiet {
					a.semaError(ed.Subtype().Span(), "record "+
						d.Text+" cannot contain an element of "+
						"its own type")
				}
				et = UnknownType
			}
			for tok := range ed.Names().Names() {
				fd := a.newDecl(DeclElement, tok, ed.Node)
				fd.Type, fd.Pos, fd.First = et, len(t.Fields), d.First
				t.Fields = append(t.Fields, fd)
				a.declare(fd)
			}
		}
		a.pop()
		a.endClause(rd.End(), d)
		a.predefined(t, d)
	case cst.KindAccessTypeDef:
		t = fill(&Type{Class: TypeAccess})
		d.Type = t
		was := a.inAccess
		a.inAccess = true
		t.Designated = a.subtypeIndication(
			cst.AccessTypeDef{Node: def}.Subtype())
		a.inAccess = was
		a.designated(t.Designated,
			cst.AccessTypeDef{Node: def}.Subtype().Node) // typecheck.go
		a.declare(d)
		a.predefined(t, d)
	case cst.KindFileTypeDef:
		t = fill(&Type{Class: TypeFile})
		d.Type = t
		tm := cst.FileTypeDef{Node: def}.TypeMark().Node
		t.Content = a.typeMark(tm)
		a.fileContent(t.Content, tm)
		a.declare(d)
		a.predefined(t, d)
	case cst.KindProtectedTypeDef:
		pd := cst.ProtectedTypeDef{Node: def}
		t = fill(&Type{Class: TypeProtected})
		d.Type = t
		a.declare(d)
		t.Members = a.push(RegionProtected, d, def)
		h := pd.Header()
		a.interfaceList(h.Generics().List(), DeclGeneric)
		a.genericMap(h.GenericMap().List(), nil)
		a.declarativePart(pd.Decls().Node)
		a.pop()
		a.methodTypes(t.Members) // typecheck.go
		a.endClause(pd.End(), d)
	default:
		// VHDL-2019 protected type instantiations and incomplete type
		// definitions of generic types: their structure is only known
		// per instance.
		t = fill(&Type{Class: TypeGeneric})
		d.Type = t
		for c := range def.Nodes() {
			a.lenientName(c)
		}
		a.declare(d)
		a.predefined(t, d)
	}
}

// noFileElement reports an array or record element of a file type
// (LRM 5.3.1).
func (a *analyzer) noFileElement(t *Type, n cst.Node) {
	if !a.quiet && !t.Unknown() && t.Base().Class == TypeFile {
		a.semaError(n.Span(), "an element of a composite type cannot be "+
			"of file type "+t.Name())
	}
	a.openElement(t, n) // typecheck.go
}

// fileContent checks the type of a file type's values (LRM 5.5.1): not
// a file, access or protected type, no access subelement, and an array
// only of one dimension.
func (a *analyzer) fileContent(t *Type, n cst.Node) {
	if a.quiet || t.Unknown() {
		return
	}
	why := ""
	switch b := t.Base(); {
	case b.Class == TypeFile || b.Class == TypeAccess ||
		b.Class == TypeProtected:
		why = "it is " + classWord(b.Class) + " type"
	case b.Class == TypeArray && len(b.Index) > 1:
		why = "it is a multi-dimensional array type"
	case hasAccessPart(b, 0):
		why = "it has an element of an access type"
	}
	if why != "" {
		a.semaError(n.Span(), t.Name()+" cannot be the type of a "+
			"file's values: "+why)
	}
}

func classWord(c TypeClass) string {
	switch c {
	case TypeFile:
		return "a file"
	case TypeAccess:
		return "an access"
	}
	return "a protected"
}

// hasAccessPart reports whether a composite type has a subelement of
// an access type.
func hasAccessPart(b *Type, depth int) bool {
	if b == nil || depth > 16 {
		return false
	}
	switch b.Class {
	case TypeAccess:
		return depth > 0
	case TypeArray:
		return b.Elem != nil && hasAccessPart(b.Elem.Base(), depth+1)
	case TypeRecord:
		for _, f := range b.Fields {
			if f.Type != nil && hasAccessPart(f.Type.Base(), depth+1) {
				return true
			}
		}
	}
	return false
}

// hasProtectedPart reports whether the composite type b has a
// subelement of a protected type, at any depth (VHDL-2019 composites of
// protected types).
func hasProtectedPart(b *Type, depth int) bool {
	if b == nil || depth > 16 {
		return false
	}
	switch b.Class {
	case TypeProtected:
		return depth > 0
	case TypeArray:
		return b.Elem != nil && hasProtectedPart(b.Elem.Base(), depth+1)
	case TypeRecord:
		for _, f := range b.Fields {
			if f.Type != nil &&
				hasProtectedPart(f.Type.Base(), depth+1) {
				return true
			}
		}
	}
	return false
}

// sameTypeLiteral reports whether r already declares a literal of
// ld's type with ld's name.
func sameTypeLiteral(r *Region, ld *Decl) bool {
	for _, d := range r.Lookup(ld.Name) {
		if d.Kind == DeclEnumLiteral && d.Type == ld.Type {
			return true
		}
	}
	return false
}

// protectedBody analyzes `type t is protected body ... end protected
// body;`, whose declarations see the protected type's methods.
func (a *analyzer) protectedBody(tok cst.Token, pb cst.ProtectedTypeBody) {
	var spec *Decl
	for _, d := range a.lookup(TokenKey(tok)).Decls {
		if d.Kind == DeclType && d.Type != nil &&
			d.Type.Class == TypeProtected {
			spec = d
			if x := d.Type.Decl; x != nil && x.Kind == DeclType {
				// An incomplete declaration (type t;) shares the
				// full declaration's Type: the body is the full one's.
				spec = x
			}
			break
		}
	}
	if spec == nil {
		if !tok.IsZero() && !a.quiet {
			a.semaError(tok.Span(), "no protected type "+tok.Text()+
				" is declared for this body")
		}
	} else {
		a.ref(tok, spec, RefBody)
		a.secondBody(tok, spec) // typecheck.go
	}
	r := a.push(RegionProtectedBody, spec, pb.Node)
	if spec != nil {
		r.Owner = spec
		r.Includes = append(r.Includes, spec.Type.Members)
	}
	a.declarativePart(pb.Decls().Node)
	a.pop()
	if spec != nil {
		a.endClause(pb.End(), spec)
	}
}

// rangeTypeDef builds an integer or floating type from its range
// constraint: the class follows the bounds' type.
func (a *analyzer) rangeTypeDef(rc cst.RangeConstraint, d *Decl) *Type {
	if a.implementationDefined(rc.Node) {
		class := TypeInteger
		if d.Name == "real" {
			class = TypeFloating
		}
		return &Type{Class: class}
	}
	a.typeDef = true
	r := a.rangeConstraint(rc.Range().Node, nil)
	a.typeDef = false
	class := TypeInteger
	if r != nil && r.Type != nil && r.Type.Base().Class == TypeFloating {
		class = TypeFloating
	}
	return &Type{Class: class, Range: r}
}

// implementationDefined reports whether n is std.standard's
// `range implementation_defined` placeholder.
func (a *analyzer) implementationDefined(n cst.Node) bool {
	if !a.inStandard {
		return false
	}
	for t := range n.DescendantTokens() {
		if TokenKey(t) == "implementation_defined" {
			return true
		}
	}
	return false
}

// rangeConstraint types the range of a range constraint against want.
func (a *analyzer) rangeConstraint(n cst.Node, want *Type) *RangeCons {
	switch n.Kind() {
	case cst.KindRangeExpr, cst.KindAttributeName, cst.KindErrorNode, 0:
	default:
		// LRM 5.2.1: range ::= range_attribute_name | simple_expression
		// direction simple_expression; (a to b) is an aggregate.
		if !a.quiet {
			a.semaError(n.Span(), a.text(n)+" is not a range")
		}
	}
	return a.discreteRange(n, want)
}

func (a *analyzer) incompleteTypeDecl(it cst.IncompleteTypeDecl) {
	d := a.newDecl(DeclType, it.Name(), it.Node)
	d.Type = &Type{Class: TypeIncomplete, Decl: d}
	a.declare(d)
}

func (a *analyzer) subtypeDecl(sd cst.SubtypeDecl) {
	d := a.newDecl(DeclSubtype, sd.Name(), sd.Node)
	done := a.selfScopeTokens(sd.Name())
	base := a.subtypeIndication(sd.Subtype())
	done()
	t := NewSubtype(base)
	if base.Unknown() {
		t = &Type{Class: TypeUnknown}
	}
	t.Decl = d
	d.Type = t
	a.declare(d)
	a.stdLayout(t)
}

// subtypeIndication returns the subtype an indication denotes: the
// type mark's type when it has no constraint or resolution, else a new
// anonymous subtype.
func (a *analyzer) subtypeIndication(si cst.SubtypeIndication) *Type {
	if si.IsZero() {
		return UnknownType
	}
	if si.TypeMark().IsZero() && a.notTypeMark(si) { // typecheck.go
		return UnknownType
	}
	base := a.typeMark(si.TypeMark().Node)
	if base.Class == TypeIncomplete && !a.inAccess {
		// Before its full declaration an incomplete type may only be
		// the designated type of an access type (LRM 5.4.2).
		if !a.quiet {
			a.semaError(si.Span(), base.Name()+" is an incomplete "+
				"type: before its full declaration it may only be "+
				"the designated type of an access type")
		}
		return UnknownType
	}
	res, cons := si.Resolution(), si.Constraint()
	if res.IsZero() && cons.IsZero() {
		return base
	}
	st := NewSubtype(base)
	if base.Unknown() {
		st = &Type{Class: TypeUnknown}
	}
	if !res.IsZero() {
		st.Resolution = a.resolution(res.Node, base)
	}
	if !cons.IsZero() {
		a.constraint(cons.Node, st, base)
	}
	return st
}

// typeMark resolves a type mark (a name denoting a type or subtype, or
// an attribute such as T'BASE).
func (a *analyzer) typeMark(n cst.Node) *Type {
	if n.IsZero() {
		return UnknownType
	}
	r := a.resolveName(n, RefType, nil)
	if r.typ != nil {
		return r.typ
	}
	if !r.unknown && !a.quiet {
		a.semaError(n.Span(), a.text(n)+" is not a type")
	}
	return UnknownType
}

// constraint applies a constraint to the subtype st of base.
func (a *analyzer) constraint(c cst.Node, st, base *Type) {
	base = orUnknown(base)
	if !a.constraintFits(c, base) { // typecheck.go
		return
	}
	switch c.Kind() {
	case cst.KindRangeConstraint:
		rn := cst.RangeConstraint{Node: c}.Range().Node
		if rn.IsZero() {
			rn = childAfter(c, token.KwRange) // not a range: reported
		}
		st.Range = a.rangeConstraint(rn, base)
	case cst.KindArrayConstraint:
		ac := cst.ArrayConstraint{Node: c}
		idx := base.Base().Index
		a.reconstrained(ac, base)
		a.rangeForm(c) // forms.go
		i := 0
		for dr := range ac.Ranges() {
			var want *Type
			if i < len(idx) {
				want = idx[i]
			}
			if dr.Kind() == cst.KindOpenExpr {
				st.IndexCons = append(st.IndexCons, nil)
			} else {
				st.IndexCons = append(st.IndexCons,
					a.discreteRange(dr.Node, want))
			}
			i++
		}
		if ec := ac.ElementConstraint(); !ec.IsZero() {
			elem := base.ElemType()
			es := NewSubtype(elem)
			if elem.Unknown() {
				es = &Type{Class: TypeUnknown}
			}
			a.constraint(ec.Node, es, elem)
			st.ElemSub = es
		}
	case cst.KindRecordConstraint:
		rc := cst.RecordConstraint{Node: c}
		for ec := range rc.ElementConstraints() {
			tok := ec.ElementName()
			var ft *Type
			pos := -1
			for _, f := range base.Base().Fields {
				if f.Name == TokenKey(tok) {
					a.ref(tok, f, RefRead)
					ft, pos = base.FieldType(f), f.Pos
				}
			}
			if cc := ec.Constraint(); !cc.IsZero() && ft != nil {
				es := NewSubtype(ft)
				if ft.Unknown() {
					es = &Type{Class: TypeUnknown}
				}
				a.constraint(cc.Node, es, ft)
				if fs := base.Base().Fields; pos >= 0 && pos < len(fs) {
					if st.FieldSubs == nil {
						st.FieldSubs = make([]*Type, len(fs))
					}
					st.FieldSubs[pos] = es
				}
			}
		}
	}
}

// reconstrained reports an index constraint on an array subtype whose
// index ranges are already all constrained (LRM 5.3.2.2, 6.3); open
// keeps the existing ranges.
func (a *analyzer) reconstrained(ac cst.ArrayConstraint, base *Type) {
	if a.quiet || base.Unknown() {
		return
	}
	if b := base.Base(); b.Class == TypeAccess && !b.Designated.Unknown() {
		base = b.Designated // the constraint is the designated's
	}
	if base.Base().Class != TypeArray {
		return
	}
	cons := base.Constraints()
	if cons == nil || slices.Contains(cons, nil) {
		return
	}
	for dr := range ac.Ranges() {
		if dr.Kind() != cst.KindOpenExpr {
			a.semaError(ac.Node.Span(), base.Name()+" is already "+
				"constrained and cannot be constrained again")
			return
		}
	}
}

// resolution resolves a resolution indication for subtypes of t.
func (a *analyzer) resolution(n cst.Node, t *Type) *Resolution {
	t = orUnknown(t)
	switch n.Kind() {
	case cst.KindResolutionFunction:
		fn := cst.ResolutionFunction{Node: n}.Function()
		r := a.resolveName(fn.Node, RefRead, nil)
		res := &Resolution{Node: n}
		res.Func = a.resolutionFunc(r.decls, t, fn.Node) // typerules.go
		for _, d := range r.decls {
			if res.Func != nil {
				break
			}
			if d.Denotes().Kind == DeclFunction &&
				len(d.Denotes().Params) == 1 {
				res.Func = d
				break
			}
		}
		if res.Func == nil && len(r.decls) > 0 {
			res.Func = r.decls[0]
		}
		if res.Func != nil {
			a.ref(r.tok, res.Func, RefRead)
		}
		return res
	case cst.KindElementResolution:
		inner := cst.ElementResolution{Node: n}.Resolution()
		return &Resolution{Node: n,
			Elem: a.resolution(inner.Node, t.ElemType())}
	case cst.KindRecordResolution:
		res := &Resolution{Node: n}
		rr := cst.RecordResolution{Node: n}
		for er := range rr.ElementResolutions() {
			tok := er.ElementName()
			var ft *Type
			for _, f := range t.Base().Fields {
				if f.Name == TokenKey(tok) {
					a.ref(tok, f, RefRead)
					ft = f.Type
				}
			}
			res.Fields = append(res.Fields,
				a.resolution(er.Resolution().Node, ft))
		}
		return res
	}
	return nil
}
