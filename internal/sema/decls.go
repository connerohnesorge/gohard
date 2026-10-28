package sema

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// declarativePart analyzes the items of a declarative part in order.
func (a *analyzer) declarativePart(n cst.Node) {
	for item := range n.Nodes() {
		a.declItem(item)
	}
}

// declItem analyzes one declarative item.
func (a *analyzer) declItem(n cst.Node) {
	if n.Inactive() {
		return
	}
	switch n.Kind() {
	case cst.KindTypeDecl:
		a.typeDecl(cst.TypeDecl{Node: n})
	case cst.KindIncompleteTypeDecl:
		a.incompleteTypeDecl(cst.IncompleteTypeDecl{Node: n})
	case cst.KindSubtypeDecl:
		a.subtypeDecl(cst.SubtypeDecl{Node: n})
	case cst.KindConstantDecl:
		c := cst.ConstantDecl{Node: n}
		a.objectDecl(n, DeclConstant, c.Names(), c.Subtype())
	case cst.KindSignalDecl:
		c := cst.SignalDecl{Node: n}
		a.objectDecl(n, DeclSignal, c.Names(), c.Subtype())
	case cst.KindVariableDecl:
		c := cst.VariableDecl{Node: n}
		k := DeclVariable
		if !c.Shared().IsZero() {
			k = DeclSharedVariable
		}
		a.objectDecl(n, k, c.Names(), c.Subtype())
	case cst.KindPrivateVariableDecl:
		v := cst.PrivateVariableDecl{Node: n}.Decl()
		a.objectDecl(v.Node, DeclVariable, v.Names(), v.Subtype())
	case cst.KindFileDecl:
		a.fileDecl(cst.FileDecl{Node: n})
	case cst.KindAliasDecl:
		a.aliasDecl(cst.AliasDecl{Node: n})
	case cst.KindAttributeDecl:
		a.packageBodyAttr(n) // forms.go
		ad := cst.AttributeDecl{Node: n}
		d := a.newDecl(DeclAttribute, ad.Name(), n)
		d.Type = a.typeMark(ad.TypeMark().Node)
		a.attrDeclType(ad.TypeMark().Node, d) // names.go
		a.declare(d)
	case cst.KindAttributeSpec:
		a.packageBodyAttr(n) // forms.go
		a.attributeSpec(cst.AttributeSpec{Node: n})
	case cst.KindComponentDecl:
		a.componentDecl(cst.ComponentDecl{Node: n})
	case cst.KindConfigurationSpec:
		cs := cst.ConfigurationSpec{Node: n}
		comp := a.componentSpec(cs.Spec())
		a.bindingIndication(cs.Binding(), comp)
		a.vunitBindings(n)          // vunitbind.go
		a.configSpecRules(cs, comp) // config.go
	case cst.KindDisconnectionSpec:
		ds := cst.DisconnectionSpec{Node: n}
		for s := range ds.Signals() {
			a.disconnectRules(s.Node, a.resolveName(s.Node, RefRead,
				nil)) // config.go
		}
		a.expr(disconnectAfter(ds), a.stdType("time"))
	case cst.KindGroupTemplateDecl:
		gt := cst.GroupTemplateDecl{Node: n}
		a.declare(a.newDecl(DeclGroupTemplate, gt.Name(), n))
	case cst.KindGroupDecl:
		g := cst.GroupDecl{Node: n}
		d := a.newDecl(DeclGroup, g.Name(), n)
		tmpl, parts := groupParts(g)
		a.resolveName(tmpl, RefType, nil)
		for _, c := range parts {
			a.lenientName(c)
		}
		a.declare(d)
	case cst.KindSubprogramDecl:
		a.subprogramDecl(cst.SubprogramDecl{Node: n})
	case cst.KindSubprogramBody:
		a.subprogramBody(cst.SubprogramBody{Node: n})
	case cst.KindSubprogramInstantiation:
		a.subprogramInst(cst.SubprogramInstantiation{Node: n})
	case cst.KindPackageDecl:
		a.packageDecl(cst.PackageDecl{Node: n})
	case cst.KindPackageBody:
		a.packageBody(cst.PackageBody{Node: n}, nil)
	case cst.KindPackageInstantiation:
		a.declare(a.packageInst(cst.PackageInstantiation{Node: n}))
	case cst.KindUseClause:
		a.useClause(cst.UseClause{Node: n})
	case cst.KindModeViewDecl:
		a.modeViewDecl(cst.ModeViewDecl{Node: n}) // modeview.go
	case cst.KindPSLPropertyDecl, cst.KindPSLSequenceDecl,
		cst.KindPSLEndpointDecl:
		a.pslDecl(n)
	case cst.KindPSLDefaultClock:
		a.pslExpr(cst.PSLDefaultClock{Node: n}.Clock().Node)
	}
}

// interfaceList declares the interface declarations of a generic,
// port or parameter list in the current region and returns them in
// order.
func (a *analyzer) interfaceList(l cst.InterfaceList, k DeclKind,
) []*Decl {
	var out []*Decl
	for item := range l.Items() {
		if item.Inactive() {
			continue
		}
		a.genericOnly(item.Node, k) // typerules.go
		switch item.Kind() {
		case cst.KindInterfaceObjectDecl:
			out = append(out, a.interfaceObject(
				cst.InterfaceObjectDecl{Node: item.Node}, k)...)
		case cst.KindInterfaceTypeDecl:
			it := cst.InterfaceTypeDecl{Node: item.Node}
			d := a.newDecl(DeclGenericType, it.Name(), item.Node)
			d.Type = &Type{Class: TypeGeneric, Decl: d}
			a.genericArrayClass(it.Def(), d)
			a.declare(d)
			a.predefined(d.Type, d)
			out = append(out, d)
		case cst.KindInterfaceSubprogramDecl:
			is := cst.InterfaceSubprogramDecl{Node: item.Node}
			d := a.subprogramSpec(is.Spec().Node, DeclGenericSubprogram)
			if d != nil {
				a.declare(d)
				if def := is.Default().Name(); !def.IsZero() {
					a.subprogramDefault(def.Node, d) // names.go
				}
				out = append(out, d)
			}
		case cst.KindInterfacePackageDecl:
			ip := cst.InterfacePackageDecl{Node: item.Node}
			d := a.newDecl(DeclGenericPackage, ip.Name(), item.Node)
			r := a.resolveName(ip.Package().Node, RefSelect, nil)
			if len(r.decls) == 1 {
				d.Target = r.decls[0].Denotes()
				d.Region = d.Target.Region
				a.packageInstRules(ip.Package().Node, d.Target, false)
			}
			a.genericMap(ip.GenericMap().List(), d.Region)
			a.defaultMap(ip.GenericMap().List(), d.Target) // typerules.go
			a.declare(d)
			out = append(out, d)
		}
	}
	return out
}

// interfaceObject declares the names of one interface object
// declaration.
func (a *analyzer) interfaceObject(io cst.InterfaceObjectDecl,
	k DeclKind,
) []*Decl {
	class := ClassNone
	switch io.Class().Kind() {
	case token.KwConstant:
		class = ClassConstant
	case token.KwSignal:
		class = ClassSignal
	case token.KwVariable:
		class = ClassVariable
	case token.KwFile:
		class = ClassFile
	}
	mode := ModeNone
	if sm, ok := cst.As[cst.SimpleModeIndication](io.Mode().Node); ok {
		switch sm.Mode().Kind() {
		case token.KwIn:
			mode = ModeIn
		case token.KwOut:
			mode = ModeOut
		case token.KwInout:
			mode = ModeInout
		case token.KwBuffer:
			mode = ModeBuffer
		case token.KwLinkage:
			mode = ModeLinkage
		}
	}
	var t *Type
	var view *View
	switch m := io.Mode().Node; m.Kind() {
	case cst.KindModeViewIndication, cst.KindArrayModeViewIndication:
		view, t = a.viewIndication(m) // modeview.go
		mode = ModeView
		if class != ClassNone && class != ClassSignal && !a.quiet {
			a.semaError(io.Class().Span(), "an interface object of "+
				"mode view must be a signal")
		}
	default:
		t = a.subtypeIndication(io.Subtype())
	}
	explicitMode := mode != ModeNone && mode != ModeView
	if mode == ModeNone && class != ClassFile {
		mode = ModeIn
	}
	if !a.quiet && k == DeclGeneric && class != ClassNone &&
		class != ClassConstant {
		// LRM 6.5.6.2: generics are constants.
		a.semaError(io.Class().Span(), "a "+io.Class().Text()+
			" interface is not allowed in a generic clause")
	}
	if class == ClassNone {
		switch {
		case k == DeclGeneric:
			class = ClassConstant
		case k == DeclPort:
			class = ClassSignal
		case k == DeclParameter && a.std >= token.Std2019 &&
			variableParam(t): // objrules.go
			// LRM 6.5.2: VHDL-2019 assumes class variable for a
			// formal parameter of a protected or access type.
			class = ClassVariable
		case mode == ModeIn:
			class = ClassConstant
		default:
			class = ClassVariable
		}
	}
	if !a.quiet && class == ClassConstant && explicitMode &&
		mode != ModeIn {
		// LRM 6.5.2: an interface constant has mode in.
		a.semaError(io.Mode().Span(), "the mode of an interface "+
			"constant must be in, not "+a.text(io.Mode().Node))
	}
	var def *Expr
	if v := initValue(io.Node); !v.IsZero() {
		def = a.expr(v.Node, t)
	}
	a.interfaceRules(io, k, class, mode, t, def != nil) // objrules.go
	var out []*Decl
	for tok := range io.Names().Names() {
		d := a.newDecl(k, tok, io.Node)
		d.Type, d.Mode, d.Class, d.Value = t, mode, class, def
		d.View = view
		d.Pos = len(out)
		a.declare(d)
		out = append(out, d)
	}
	return out
}

// objectDecl declares constants, signals and variables.
func (a *analyzer) objectDecl(n cst.Node, k DeclKind,
	names cst.IdentifierList, si cst.SubtypeIndication,
) {
	done := a.selfScope(names)
	t := a.subtypeIndication(si)
	value := initValue(n)
	var v *Expr
	if !value.IsZero() {
		v = a.expr(value.Node, t)
		a.u.Exprs = append(a.u.Exprs, v)
		a.deferredUse(v, 0) // forms.go
	}
	done()
	a.objectDeclRules(si, k, t)     // objrules.go
	a.protectedElaborated(si, k, t) // typecheck.go
	if v != nil && (k == DeclVariable || k == DeclSharedVariable) {
		a.protectedCopy(value.Span(), t,
			"cannot have an initial value") // objrules.go
	}
	if k == DeclSharedVariable && a.std >= token.Std2002 && !a.quiet &&
		!t.Unknown() &&
		t.Base().Class != TypeProtected && t.Base().Class != TypeGeneric &&
		!(a.std >= token.Std2019 && hasProtectedPart(t.Base(), 0)) {
		// LRM 6.4.2.4: from VHDL-2002 on a shared variable is of a
		// protected type (VHDL-2019: or a composite of them). Lenient
		// mode (#28) keeps the VHDL-93 rule with a warning, as GHDL's
		// -frelaxed and NVC's --relaxed do.
		problem := "the type of a shared variable must be a " +
			"protected type, not " + t.Name()
		if a.s.cfg.Lenient {
			a.report(si.Span(), diag.New(diag.SemaRelaxed, problem))
		} else {
			a.semaError(si.Span(), problem)
		}
	}
	if value.IsZero() && (k == DeclSignal || k == DeclVariable) &&
		!a.quiet && (notFullyConstrained(t, 0) ||
		a.instanceUnconstrained(si, t)) { // typerules.go
		// LRM 6.4.2.3, 6.4.2.4: an object's subtype is fully
		// constrained (VHDL-2019 may infer it from an initial value).
		a.semaError(si.Span(), "a "+k.String()+" cannot be of the "+
			"unconstrained type "+t.Name())
	}
	if value.IsZero() && k == DeclConstant &&
		a.cur.Kind != RegionPackage && !a.quiet {
		// LRM 6.4.2.2: only a package declaration may defer a
		// constant's value.
		a.semaError(names.Node.Span(), "a constant outside a package "+
			"declaration must have a value")
	}
	dt := t
	if v != nil && a.std >= token.Std2019 &&
		(k == DeclVariable || k == DeclSignal) &&
		notFullyConstrained(t, 0) {
		// LRM 6.4.2.3, 6.4.2.4: VHDL-2019 infers the constraints the
		// subtype leaves open from the initial value.
		dt = inferred(t, v) // typerep.go
	}
	for tok := range names.Names() {
		d := a.newDecl(k, tok, n)
		d.Type, d.Value = dt, v
		switch k {
		case DeclConstant:
			d.Class = ClassConstant
			a.linkDeferred(d)
		case DeclSignal:
			d.Class = ClassSignal
		default:
			d.Class = ClassVariable
		}
		a.declare(d)
	}
}

// privateVariable reports whether d is a VHDL-2019 private variable: a
// variable declared with `private` in a protected type declaration
// (LRM 5.6.2). It is a member of the type's region like the methods,
// but only the type and its body see it; a selected name from outside
// (obj.priv) does not denote it.
func privateVariable(d *Decl) bool {
	return d != nil && d.Kind == DeclVariable && d.Parent != nil &&
		d.Parent.Kind == RegionProtected &&
		d.Node.Parent().Kind() == cst.KindPrivateVariableDecl
}

// selfScope marks names as being declared until the returned func is
// called.
func (a *analyzer) selfScope(names cst.IdentifierList) func() {
	var toks []cst.Token
	for tok := range names.Names() {
		toks = append(toks, tok)
	}
	return a.selfScopeTokens(toks...)
}

func (a *analyzer) selfScopeTokens(toks ...cst.Token) func() {
	saved := a.declaring
	m := maps.Clone(saved)
	if m == nil {
		m = map[Key]bool{}
	}
	for _, tok := range toks {
		m[TokenKey(tok)] = true
	}
	a.declaring = m
	return func() { a.declaring = saved }
}

// linkDeferred links the full declaration of a deferred constant (in a
// package body) to the deferred declaration in the package.
func (a *analyzer) linkDeferred(d *Decl) {
	if a.cur.Kind != RegionPackageBody || a.cur.Owner == nil ||
		a.cur.Owner.Spec == nil || a.cur.Owner.Spec.Region == nil {
		return
	}
	for _, old := range a.cur.Owner.Spec.Region.Lookup(d.Name) {
		if old.Kind == DeclConstant && old.Value == nil {
			d.Spec = old
			a.u.Refs = append(a.u.Refs, Ref{File: a.f, Span: d.Span,
				Decl: old, Kind: RefBody})
			return
		}
	}
}

func (a *analyzer) fileDecl(fd cst.FileDecl) {
	done := a.selfScope(fd.Names())
	t := a.subtypeIndication(fd.Subtype())
	a.fileDeclRules(fd.Subtype(), t) // objrules.go
	a.pureFile(fd.Node)              // purity.go
	kind, name := fileOpen(fd.OpenInfo())
	if !kind.IsZero() {
		a.expr(kind, a.stdType("file_open_kind"))
	}
	if !name.IsZero() {
		// The logical name is any string expression (LRM 6.4.2.5),
		// evaluated when the declaration is elaborated.
		a.u.Exprs = append(a.u.Exprs, a.expr(name, a.stdType("string")))
	}
	done()
	for tok := range fd.Names().Names() {
		d := a.newDecl(DeclFile, tok, fd.Node)
		d.Type, d.Class = t, ClassFile
		a.declare(d)
	}
}

// aliasDecl declares an object or non-object alias (LRM 6.6).
func (a *analyzer) aliasDecl(ad cst.AliasDecl) {
	d := a.newDecl(DeclAlias, ad.Designator(), ad.Node)
	d.sigAlias = !ad.Signature().IsZero()
	defer a.aliasSelfScope(ad)()
	var t *Type
	if si := ad.Subtype(); !si.IsZero() {
		t = a.subtypeIndication(si)
	}
	target := ad.Target()
	r := a.resolveName(target.Node, RefRead, nil)
	switch {
	case r.view != nil:
		// An alias of a mode view or of V'CONVERSE (modeview.go).
		d.View, d.Type = r.view, r.view.Of
		if len(r.decls) == 1 {
			d.Target = r.decls[0]
		}
	case r.expr != nil && r.expr.Kind != ExprUnknown &&
		len(r.decls) <= 1 && (len(r.decls) == 0 ||
		r.decls[0].Denotes().Kind.IsObject() ||
		r.decls[0].Denotes().Kind == DeclElement):
		// An object alias: it denotes the (sub)element named (a
		// selected record element is an object name, LRM 6.6.2).
		d.Value = r.expr
		d.Type = t
		a.aliasSubtype(ad, t, r.expr) // typecheck.go
		a.aliasRules(ad, r.expr)      // staticrules.go
		if d.Type == nil || notFullyConstrained(t, 0) &&
			SameBase(t, r.expr.Type) &&
			!notFullyConstrained(r.expr.Type, 0) {
			// An unconstrained alias subtype takes the object's
			// constraint (LRM 6.6.2).
			d.Type = r.expr.Type
		}
		d.Class = ClassConstant
		if r.expr.Decl != nil {
			d.Class = r.expr.Decl.Denotes().Class
			if d.Class == ClassNone {
				d.Class = classOf(r.expr.Decl.Denotes().Kind)
			}
		}
	case len(r.decls) > 0:
		if !aliasesObject(r.decls) {
			a.aliasRules(ad, nil) // staticrules.go
		}
		a.aliasSignature(ad, r.decls) // typerules.go
		pick := r.decls
		if sig := ad.Signature(); !sig.IsZero() {
			pick = a.matchSignature(r.decls, sig) // overload.go
			a.signatureMatched(sig, r, pick)      // names.go
		}
		if len(pick) >= 1 {
			// Without a signature an alias of an overloaded name takes
			// the first visible one; matchSignature narrows it.
			d.Target = pick[0]
			d.Type = pick[0].Denotes().Type
			d.Params = pick[0].Denotes().Params
			if x := pick[0].Denotes(); r.expr != nil &&
				x.Parent != nil && x.Parent.Kind == RegionProtected {
				// A method of a named object (obj.m): the alias keeps
				// the object it calls the method on (LRM 6.6.1).
				d.MethodObj = r.expr
			}
		}
		if len(pick) >= 1 {
			a.ref(r.tok, pick[0], RefRead)
		}
	case r.typ != nil:
		a.aliasRules(ad, nil)
		a.aliasTypeName(target.Node) // typerules.go
		d.Type = r.typ
	}
	if a.std >= token.Std2019 && d.Parent != nil &&
		d.Parent.Kind == RegionProtected && d.MethodObj == nil &&
		!r.unknown {
		// LRM 5.6.2 (2019): an alias in a protected type declaration
		// denotes a method of a private variable (NVC alike); older
		// standards report the alias as syntax/standard.
		a.semaError(d.Span, "alias "+d.Text+" in a protected type "+
			"declaration must denote a method of a private variable "+
			"(LRM 5.6.2)")
	}
	a.declare(d)
	if r.typ != nil && !r.typ.Unknown() && r.expr == nil {
		a.typeAliasImplicits(d, r.typ)
	}
}

// typeAliasImplicits declares the aliases a nonobject alias of a type
// implies (LRM 6.6.3): one for each enumeration literal, physical
// unit and predefined operation of the type, so a use clause of the
// alias's package makes them visible too.
func (a *analyzer) typeAliasImplicits(d *Decl, t *Type) {
	b := t.Base()
	var ds []*Decl
	ds = append(ds, b.Literals...)
	ds = append(ds, b.Units...)
	ds = append(ds, b.Ops...)
	for _, x := range ds {
		if x == nil || x.Name == "" {
			continue
		}
		al := &Decl{Kind: DeclAlias, Name: x.Name, Text: x.Text,
			File: d.File, Span: d.Span, Full: d.Full, Unit: a.u,
			Target: x, Type: x.Type, Params: x.Params, Implicit: true,
			Pure: true, First: d.First}
		a.declare(al)
	}
}

func classOf(k DeclKind) ObjClass {
	switch k {
	case DeclConstant, DeclGeneric:
		return ClassConstant
	case DeclSignal, DeclPort:
		return ClassSignal
	case DeclVariable, DeclSharedVariable:
		return ClassVariable
	case DeclFile:
		return ClassFile
	}
	return ClassNone
}

// attributeSpec analyzes `attribute a of names : class is value;`.
func (a *analyzer) attributeSpec(as cst.AttributeSpec) {
	tok := as.Designator()
	var t *Type
	var attr *Decl
	if r := a.lookup(TokenKey(tok)); len(r.Decls) > 0 {
		for _, d := range r.Decls {
			if d.Denotes().Kind == DeclAttribute {
				a.ref(tok, d, RefRead)
				t, attr = d.Denotes().Type, d.Denotes()
				break
			}
		}
	} else if !tok.IsZero() {
		a.undefined(tok)
	}
	for ed := range as.Entities().Names().Designators() {
		et := ed.Tag()
		k := TokenKey(et)
		if k == "" || k == "others" || k == "all" {
			a.attrOthers(k, attr) // names.go
			continue
		}
		var named *Decl
		if r := a.lookup(k); len(r.Decls) > 0 {
			named = r.Decls[0]
		} else if a.u.Decl != nil && k == a.u.Decl.Name {
			named = a.u.Decl
		}
		a.ref(et, named, RefRead)
		a.attrEntity(as.Entities(), ed, named, attr) // names.go
		if attr != nil && named != nil && named == a.u.Decl &&
			a.u.Kind.Primary() {
			a.u.unitAttrs = append(a.u.unitAttrs, attr)
		}
	}
	if v := as.Value(); !v.IsZero() {
		a.u.Exprs = append(a.u.Exprs, a.expr(v.Node, t))
	}
}

func (a *analyzer) componentDecl(c cst.ComponentDecl) {
	d := a.newDecl(DeclComponent, c.Name(), c.Node)
	a.declare(d)
	a.push(RegionComponent, d, c.Node)
	a.interfaceList(c.Generics().List(), DeclGeneric)
	a.interfaceList(c.Ports().List(), DeclPort)
	a.endClause(c.End(), d)
	a.pop()
}

// subprogramSpec builds the declaration of a function or procedure
// specification with its parameters (declared in the subprogram's
// region, which stays open only while the spec is analyzed). kind
// overrides the declaration kind (generic subprograms); 0 picks
// function or procedure.
func (a *analyzer) subprogramSpec(spec cst.Node, kind DeclKind) *Decl {
	var (
		tok    cst.Token
		header cst.SubprogramHeader
		params cst.ParameterList
		ret    cst.ReturnSpec
	)
	switch spec.Kind() {
	case cst.KindFunctionSpec:
		fs := cst.FunctionSpec{Node: spec}
		tok, header, params, ret = fs.Designator(), fs.Header(),
			fs.Params(), fs.ReturnType()
		if kind == 0 {
			kind = DeclFunction
		}
	case cst.KindProcedureSpec:
		ps := cst.ProcedureSpec{Node: spec}
		tok, header, params = ps.Designator(), ps.Header(), ps.Params()
		if kind == 0 {
			kind = DeclProcedure
		}
	default:
		return nil
	}
	d := a.newDecl(kind, tok, spec)
	if spec.Kind() == cst.KindFunctionSpec {
		d.Pure = spec.Token(token.KwImpure, 0).IsZero()
	}
	a.push(RegionSubprogram, d, spec)
	a.interfaceList(header.Generics().List(), DeclGeneric)
	a.genericMap(header.GenericMap().List(), nil)
	d.Params = a.interfaceList(params.List(), DeclParameter)
	for i, p := range d.Params {
		p.Pos = i
	}
	if spec.Kind() == cst.KindFunctionSpec && d.Pure {
		a.pureParams(d) // objrules.go
	}
	if !ret.IsZero() {
		d.Type = a.typeMark(ret.TypeMark().Node)
		a.resultRules(ret, d.Type) // objrules.go
		a.returnIdentifier(ret, d.Type)
	}
	a.pop()
	return d
}

// returnIdentifier declares the VHDL-2019 return identifier of
// `return R of T` in the function's region: a subtype of T whose
// constraint is the return value's, known only per call (LRM 4.2.1).
func (a *analyzer) returnIdentifier(ret cst.ReturnSpec, t *Type) {
	if ret.Node.Token(token.KwOf, 0).IsZero() {
		return
	}
	tok := ret.Node.TokenIn(nameTokens, 0)
	if tok.IsZero() || tok.Missing() {
		return
	}
	r := a.newDecl(DeclSubtype, tok, ret.Node)
	st := &Type{Class: TypeUnknown}
	if !t.Unknown() {
		st = runTime(t)
	}
	st.Decl = r
	r.Type = st
	a.declare(r)
}

func (a *analyzer) subprogramDecl(sd cst.SubprogramDecl) {
	if d := a.subprogramSpec(sd.Spec().Node, 0); d != nil {
		a.declare(d)
	}
}

// subprogramBody analyzes a subprogram body, linking it to an earlier
// declaration with the same profile in the same region (or, in a
// package body, in the package).
func (a *analyzer) subprogramBody(sb cst.SubprogramBody) {
	d := a.subprogramSpec(sb.Spec().Node, 0)
	if d == nil {
		return
	}
	d.Full = sb.Span()
	d.Node = sb.Node
	if spec := a.findSpec(d); spec != nil {
		d.Spec = spec
		a.duplicateBody(d, spec) // scope.go
		a.conforms(spec, sb.Spec().Node, d)
		a.u.Refs = append(a.u.Refs, Ref{File: a.f, Span: d.Span,
			Decl: spec, Kind: RefBody})
		// The declaration stays the visible one: calls see its
		// parameter defaults (a body may omit them) and resolve to it.
		d.Parent = a.cur
		a.cur.Decls = append(a.cur.Decls, d)
		a.u.Decls = append(a.u.Decls, d)
	} else {
		a.declare(d)
	}
	// Re-open the subprogram's region for the body: the parameters
	// declared by the spec are visible in it.
	r := d.Region
	r.Span = sb.Span()
	saved, savedFn := a.cur, a.fn
	savedLoops, savedSens := a.loops, a.sensitized
	a.cur, a.fn = r, d
	a.loops, a.sensitized = nil, false
	defer func() { a.loops, a.sensitized = savedLoops, savedSens }()
	a.declareSeqLabels(sb.Stmts().Node)
	a.declarativePart(sb.Decls().Node)
	d.Seq = a.sequentialStatements(sb.Stmts().Node)
	a.purityRules(d) // purity.go
	end := sb.End()
	if tok := end.Name(); !tok.IsZero() && !tok.Missing() {
		if TokenKey(tok) == d.Name {
			a.ref(tok, d, RefEnd)
		} else {
			a.semaError(tok.Span(), "end name "+tok.Text()+
				" does not match "+d.Text)
		}
	}
	a.cur, a.fn = saved, savedFn
}

// conforms reports a subprogram body whose specification does not
// conform to its declaration's (LRM 4.10): the same sequence of lexical
// elements, identifiers compared without case and abstract literals by
// spelling without underscores.
func (a *analyzer) conforms(spec *Decl, body cst.Node, d *Decl) {
	if a.quiet || spec.File == nil || spec.Node.IsZero() {
		return
	}
	sn := spec.Node
	if sd, ok := cst.As[cst.SubprogramDecl](sn); ok {
		sn = sd.Spec().Node
	}
	lenient := a.s.cfg.Lenient
	x, y := conformTokens(sn, lenient), conformTokens(body, lenient)
	if slices.Equal(x, y) {
		return
	}
	l, _ := spec.File.Lines.LineCol(spec.Span.Start)
	where := "line " + strconv.Itoa(l)
	if spec.File != a.f {
		where = spec.File.Path + ":" + strconv.Itoa(l)
	}
	a.semaError(d.Span, "the body of "+d.Text+" does not conform to "+
		"its declaration at "+where)
}

// conformTokens is the normalized lexical element sequence of a
// subprogram specification.
func conformTokens(n cst.Node, lenient bool) []string {
	// LRM 4.10 allows no other variation, but lenient analysis lets an
	// interface constant that names no mode add or omit the class
	// (GHDL -frelaxed and NVC --relaxed accept `a : bit` against
	// `constant a : bit`).
	drop := map[int]bool{}
	for io := range n.Descendants() {
		if !lenient {
			break
		}
		x, ok := cst.As[cst.InterfaceObjectDecl](io)
		if !ok || !x.Mode().IsZero() ||
			x.Class().Kind() != token.KwConstant {
			continue
		}
		drop[x.Class().Span().Start] = true
	}
	var out []string
	for t := range n.DescendantTokens() {
		if t.Missing() || drop[t.Span().Start] {
			continue
		}
		s := t.Text()
		switch t.Kind() {
		case token.ExtendedIdentifier, token.CharacterLiteral,
			token.StringLiteral:
		case token.IntegerLiteral, token.RealLiteral:
			s = strings.ToLower(strings.ReplaceAll(s, "_", ""))
		default:
			s = strings.ToLower(s)
		}
		out = append(out, s)
	}
	return out
}

// findSpec returns the declaration a subprogram body completes.
func (a *analyzer) findSpec(d *Decl) *Decl {
	try := func(r *Region) *Decl {
		if r == nil {
			return nil
		}
		for _, old := range r.Lookup(d.Name) {
			if !old.Implicit && old.Kind == d.Kind && old.Spec == nil &&
				old.Node.Kind() != cst.KindSubprogramBody &&
				(sameProfile(old, d) || sameGenericProfile(old, d)) {
				return old
			}
		}
		return nil
	}
	if s := try(a.cur); s != nil {
		return s
	}
	if a.cur.Kind == RegionPackageBody {
		if o := a.cur.Owner; o != nil && o.Spec != nil {
			if s := try(o.Spec.Region); s != nil {
				return s
			}
		}
	}
	for _, inc := range a.cur.Includes {
		if s := try(inc); s != nil {
			return s
		}
	}
	return nil
}

// subprogramInst analyzes `function f is new g generic map (...)`.
func (a *analyzer) subprogramInst(si cst.SubprogramInstantiation) {
	kind := DeclFunction
	if !si.Node.Token(token.KwProcedure, 0).IsZero() {
		kind = DeclProcedure
	}
	d := a.newDecl(kind, si.Designator(), si.Node)
	r := a.resolveName(si.Subprogram().Node, RefRead, nil)
	pick := r.decls
	if sig := si.Signature(); !sig.IsZero() {
		pick = a.matchSignature(pick, sig)
	}
	if len(pick) >= 1 {
		g := pick[0].Denotes()
		a.subprogramInstRules(si, g) // pkginst.go
		d.Target, d.Type, d.Params = g, g.Type, g.Params
		if g.Kind.IsSubprogram() {
			d.Kind = g.Kind
		}
		a.ref(r.tok, g, RefRead)
		as := a.assocMap(si.GenericMap().List(), g.Region, DeclGeneric)
		a.instSubst = nil
		if g.Kind.IsSubprogram() {
			a.mapRules(as, g.Region, DeclGeneric, si.Node) // assocrules.go
			a.missingGenerics(as, g.Region, si.Node)
			a.instanceObjects(g, as, si.Node) // pkginst.go
		}
		if as == nil {
			as = []Association{}
		}
		d.Generics = as
		instanceProfile(d, as)
	} else {
		a.genericMap(si.GenericMap().List(), nil)
	}
	a.declare(d)
}

// instanceProfile gives the instance d of a generic subprogram its
// parameter and result type profile (LRM 4.4, 4.5.1): each generic
// type of the uninstantiated subprogram is replaced by its actual
// subtype, so instances for different types are no homographs.
func instanceProfile(d *Decl, as []Association) {
	subst := map[*Type]*Type{} // lookup only
	for _, x := range as {
		if x.Formal != nil && x.Formal.Kind == DeclGenericType &&
			x.Actual != nil && !x.Actual.Type.Unknown() {
			subst[x.Formal.Type] = x.Actual.Type
		}
	}
	if len(subst) == 0 {
		return
	}
	sub := func(t *Type) *Type {
		if n, ok := subst[t]; ok {
			return n
		}
		return t
	}
	d.Type = sub(d.Type)
	ps := make([]*Decl, len(d.Params))
	for i, p := range d.Params {
		c := *p
		c.Type = sub(p.Type)
		ps[i] = &c
	}
	d.Params = ps
}

// pslDecl declares a PSL property, sequence or endpoint; its body is
// walked for names only.
func (a *analyzer) pslDecl(n cst.Node) {
	d := a.newDecl(DeclPSL, n.TokenIn(nameTokens, 0), n)
	a.declare(d)
	a.push(RegionStatement, d, n)
	for c := range n.Nodes() {
		if c.Kind() == cst.KindPSLFormalParameterList {
			for p := range (cst.PSLFormalParameterList{Node: c}).Params() {
				for tok := range p.Names() {
					pd := a.newDecl(DeclParameter, tok, p.Node)
					pd.Type = UnknownType
					a.declare(pd)
				}
			}
			continue
		}
		a.sequenceBody(n, c) // psl.go
		a.psl(c)
	}
	a.pop()
}

// declareLabels declares the labels of the concurrent statements of a
// statement part at the start of the enclosing region (LRM 6.? implicit
// label declarations), so attribute specifications and expanded names
// can name them before the statement.
func (a *analyzer) declareLabels(stmts cst.Node) {
	for st := range stmts.Nodes() {
		if st.Inactive() {
			continue
		}
		if l := st.Child[cst.Label](0); !l.IsZero() {
			a.declare(a.labelDecl(l, st))
		}
	}
}

// declareSeqLabels declares every label of the sequential statements
// of a process or subprogram body, nested ones included (LRM 10.1).
func (a *analyzer) declareSeqLabels(stmts cst.Node) {
	for st := range stmts.Descendants() {
		if st.Kind() == cst.KindSubprogramBody ||
			st.Kind() == cst.KindProcessStmt {
			continue
		}
		if !(cst.SeqStmt{}).Accepts(st.Kind()) || st.Inactive() {
			continue
		}
		if l := st.Child[cst.Label](0); !l.IsZero() {
			a.declare(a.labelDecl(l, st))
		}
	}
}

func (a *analyzer) labelDecl(l cst.Label, st cst.Node) *Decl {
	d := a.newDecl(DeclLabel, l.Name(), st)
	a.labels = append(a.labels, d)
	return d
}

// labelOf returns the label declaration of statement st, if any.
func (a *analyzer) labelOf(st cst.Node) *Decl {
	for i := len(a.labels) - 1; i >= 0; i-- {
		if a.labels[i].Node.Equal(st) {
			return a.labels[i]
		}
	}
	return nil
}

// lenientName resolves a name whose meaning sema does not check yet
// (group constituents, configuration items): references are recorded
// where the name resolves, and nothing is reported when it does not.
func (a *analyzer) lenientName(n cst.Node) {
	saved := a.quiet
	a.quiet = true
	a.resolveName(n, RefRead, nil)
	a.quiet = saved
}
