package sema

import (
	"maps"
	"slices"
	"strconv"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/token"
)

var nameTokens = token.NewSet(token.Identifier, token.ExtendedIdentifier)

// analyzer is the state of one unit job. It writes only its own unit
// (declarations, regions, refs, typed expressions, diagnostics) and
// reads finished units through the session.
type analyzer struct {
	s   *session
	u   *Unit
	f   *File
	std token.Standard
	cur *Region
	// fn is the innermost subprogram body (for return statements).
	fn *Decl
	// loops are the loop statements around the current statement,
	// innermost last (each its label, nil when unlabeled), and
	// sensitized is set in a process with a sensitivity list (LRM
	// 10.2, 10.11, 10.12, 11.3); a subprogram body resets both.
	loops      []*Decl
	sensitized bool
	// inPSL counts the PSL expressions being analyzed (psl.go).
	inPSL int
	// choosing counts the overload resolutions running (Probe).
	choosing int
	// instSubst maps the generic types of the instance whose maps are
	// being analyzed to their actual subtypes (typerules.go).
	instSubst map[*Type]*Type
	// misfits counts the generic map actuals that do not fit their
	// formal (genericSubActual, genericTypeClass), quiet or not: an
	// overloaded call's instance with one is no candidate (instances).
	misfits int
	// asg is the assignment statement being analyzed (assign.go).
	asg *Assign
	// typeDef is set while analyzing the range of an integer, floating
	// or physical type definition, whose bounds may be of different
	// types of one class (LRM 5.2.3.1, 5.2.5.1).
	typeDef bool
	// globalChoices is set while typing a case generate's choices.
	globalChoices bool
	// sliceValue is set while settling a VHDL-2008 aggregate slice
	// association's value against the aggregate's type.
	sliceValue bool
	// declaring are the names of the object, file or subtype
	// declaration being analyzed (LRM 12.3).
	declaring map[Key]bool
	// attrSpecs are the (attribute, named entity) pairs given a value
	// by an attribute specification so far (LRM 7.2).
	attrSpecs map[[2]*Decl]bool
	// mapActual is set while typing a port or generic map actual or
	// an aggregate target, which associate or write their objects
	// rather than read them.
	mapActual bool
	// quietActuals is set while analyzing the maps of a binding
	// indication whose component is unknown (in a configuration):
	// its actuals are typed without reporting (stmts.go).
	quietActuals bool
	// inStandard is set while analyzing std.standard itself.
	inStandard bool
	// labels are the statement labels declared so far.
	labels []*Decl
	// quiet suppresses name diagnostics (lenient names).
	quiet bool
	// inAccess is set while analyzing the designated subtype of an
	// access type definition, the one place an incomplete type may be
	// named (LRM 5.4.2).
	inAccess bool
	// deferred holds std.standard's types until flushStandard.
	deferred []deferredOps
	flushing bool
}

// analyzeUnit is the job for one unit.
func (s *session) analyzeUnit(u *Unit) {
	a := &analyzer{s: s, u: u, f: u.File, std: s.std}
	if u.File.Bundled {
		// The bundled text is VHDL-2019 in every view (#33); views
		// hide names from project units, not from the bundle itself.
		a.std = token.Std2019
	}
	a.inStandard = u.File.Bundled && u.Library == "std" &&
		u.Name == "standard" && u.Kind == UnitPackage
	a.unit()
	u.Refs = sortRefs(u.Refs)
}

// errorAt reports diagnostic d at rg in the analyzed file.
func (a *analyzer) report(rg cst.Range, d diag.Diagnostic) {
	a.u.diags = append(a.u.diags, at(a.f, rg, d))
}

// semaError reports sema/error with problem at rg.
func (a *analyzer) semaError(rg cst.Range, problem string) {
	a.report(rg, diag.New(diag.SemaError, problem))
}

// newDecl returns a declaration of kind k named by tok, declared by
// node, in the current region (not yet added to it).
func (a *analyzer) newDecl(k DeclKind, tok cst.Token, node cst.Node,
) *Decl {
	d := &Decl{Kind: k, Name: TokenKey(tok), Text: tok.Text(),
		File: a.f, Span: cst.Range(tok.Span()), Full: node.Span(),
		Node: node, Unit: a.u, Parent: a.cur, Pure: true}
	if a.u.File.Bundled {
		d.First = a.s.firstStd(d)
	}
	return d
}

// declare adds d to the current region. A non-overloadable homograph
// already declared in the same region is reported, unless d completes
// it (d.Spec, set by the caller for bodies, full types and deferred
// constants).
func (a *analyzer) declare(d *Decl) {
	a.declareIn(a.cur, d)
}

func (a *analyzer) declareIn(r *Region, d *Decl) {
	if d.Name == "" {
		return
	}
	d.Parent = r
	hidden := false
	if d.Spec == nil && !d.Implicit {
		olds := r.Lookup(d.Name)
		own := len(olds)
		if ext := extendedRegion(r); ext != nil {
			// An architecture extends its entity's declarative region,
			// a package body its package's (LRM 12.1).
			olds = append(slices.Clip(olds), ext.Lookup(d.Name)...)
		}
		for i, old := range olds {
			if old.Implicit || old == d || !homographs(old, d) {
				continue
			}
			if old.Kind == DeclType && old.Type != nil &&
				old.Type.Class == TypeIncomplete {
				continue
			}
			// The later of the two in the text is the redeclaration:
			// a statement label is declared before the declarations
			// that precede it (LRM 12.3).
			first, again := old, d
			if old.File == d.File && old.Span.Start > d.Span.Start {
				first, again = d, old
			}
			// A redeclaration of an item of the extended region (an
			// entity's port redeclared in its architecture) does not
			// hide the item: the region is one, and names keep
			// denoting the first declaration, as in GHDL and NVC.
			hidden = i >= own && again == d
			l, _ := first.File.Lines.LineCol(first.Span.Start)
			a.report(again.Span, diag.New(diag.SemaError,
				again.Text+" is already declared in this region (line "+
					strconv.Itoa(l)+")").WithRelated(diag.Related{
				Path: first.Path(), Line: l, Col: col(first),
				Message: "first declaration of " + first.Text}))
			break
		}
	}
	if !hidden {
		r.add(d)
	}
	a.u.Decls = append(a.u.Decls, d)
}

// extendedRegion returns the primary unit region r extends: the
// entity's of an architecture, the package's of a package body.
func extendedRegion(r *Region) *Region {
	if r == nil || r.Owner == nil || r.Owner.Spec == nil {
		return nil
	}
	switch r.Kind {
	case RegionArchitecture, RegionPackageBody:
		return r.Owner.Spec.Region
	}
	return nil
}

func col(d *Decl) int {
	if d.File == nil {
		return 1
	}
	_, c := d.File.Lines.LineCol(d.Span.Start)
	return c
}

// implicitDecl records an implicitly declared entity (predefined
// operations) in the unit without a name check.
func (a *analyzer) implicitDecl(r *Region, d *Decl) {
	d.Implicit = true
	d.Unit = a.u
	if d.File == nil {
		d.File = a.f
	}
	r.add(d)
	a.u.Decls = append(a.u.Decls, d)
}

// push opens a region owned by owner covering node; pop closes it.
func (a *analyzer) push(k RegionKind, owner *Decl, node cst.Node,
) *Region {
	r := &Region{Kind: k, Owner: owner, Parent: a.cur, File: a.f,
		Span: node.Span(), Unit: a.u}
	if a.cur != nil {
		a.cur.Children = append(a.cur.Children, r)
	}
	if owner != nil && owner.Region == nil {
		owner.Region = r
	}
	a.cur = r
	return r
}

func (a *analyzer) pop() {
	if a.cur != nil {
		a.cur = a.cur.Parent
	}
}

// ref records an occurrence of d at tok.
func (a *analyzer) ref(tok cst.Token, d *Decl, k RefKind) {
	if tok.IsZero() || tok.Missing() || d == nil {
		return
	}
	a.u.Refs = append(a.u.Refs, Ref{File: a.f, Span: tok.Span(),
		Decl: d, Kind: k})
}

// refOverload records an occurrence of an overloaded name with its
// overload result (the chosen declaration, if any).
func (a *analyzer) refOverload(tok cst.Token, o *Overload, k RefKind) {
	if tok.IsZero() || tok.Missing() || o == nil {
		return
	}
	a.u.Refs = append(a.u.Refs, Ref{File: a.f, Span: tok.Span(),
		Decl: o.Chosen, Overload: o, Kind: k})
}

// lookup finds the declarations named k visible at the current point.
func (a *analyzer) lookup(k Key) lookupResult {
	return lookup(a.cur, a.f, -1, k, a.std)
}

// unit analyzes the whole design unit.
func (a *analyzer) unit() {
	u := a.u
	ctx := &Region{Kind: RegionContext, File: a.f, Span: u.Node.Span(),
		Unit: u, Libs: map[Key]*Decl{}}
	u.Context = ctx
	var primary *Unit
	if !u.Kind.Primary() || u.Kind == UnitConfiguration {
		primary = a.primaryUnit()
		if primary != nil && primary.Region != nil {
			ctx.Parent = primary.Region
		}
	}
	if u.Kind == UnitVunit {
		ctx.Parent = a.boundRegion() // psl.go
	}
	a.cur = ctx
	a.implicitContext()
	a.contextClause(cst.DesignUnit{Node: u.Node}.Context())
	lu := u.Lib
	switch u.Kind {
	case UnitEntity:
		a.entity(cst.EntityDecl{Node: lu})
	case UnitArchitecture:
		a.architecture(cst.ArchitectureBody{Node: lu}, primary)
	case UnitPackage:
		u.Decl = a.packageDecl(cst.PackageDecl{Node: lu})
		u.Region = u.Decl.Region
	case UnitPackageBody:
		u.Decl = a.packageBody(cst.PackageBody{Node: lu}, primary)
		u.Region = u.Decl.Region
	case UnitPackageInst:
		u.Decl = a.packageInst(cst.PackageInstantiation{Node: lu})
		a.declare(u.Decl)
		u.Region = u.Decl.Region
	case UnitConfiguration:
		a.configuration(cst.ConfigurationDecl{Node: lu}, primary)
	case UnitContext:
		a.contextDecl(cst.ContextDecl{Node: lu})
	case UnitVunit:
		a.vunit(lu)
	}
	a.syntaxChecks(u.Node)
}

// primaryUnit returns the primary unit of a secondary unit (or the
// entity of a configuration), recording a reference from the name in
// the header; nil when it is missing.
func (a *analyzer) primaryUnit() *Unit {
	u := a.u
	var tok cst.Token
	switch u.Kind {
	case UnitArchitecture:
		tok = cst.ArchitectureBody{Node: u.Lib}.Entity().FirstToken()
	case UnitConfiguration:
		tok = cst.ConfigurationDecl{Node: u.Lib}.Entity().FirstToken()
	case UnitPackageBody:
		tok = cst.PackageBody{Node: u.Lib}.Name()
	}
	l, ok := a.s.library(u, Work)
	if !ok {
		return nil
	}
	p, ok := l.UnitFrom(u.Of, u)
	if !ok || !a.s.readable(u, p) || p.Decl == nil {
		if !ok {
			a.missingUnit(l, u.Of, tok)
		}
		return nil
	}
	want := UnitEntity
	if u.Kind == UnitPackageBody {
		want = UnitPackage
	}
	if p.Kind != want {
		a.semaError(tok.Span(), string(u.Of.Upper())+" is a "+
			p.Kind.String()+", not a "+want.String())
		return nil
	}
	kind := RefRead
	if u.Kind == UnitPackageBody {
		kind = RefBody
	}
	a.ref(tok, p.Decl, kind)
	return p
}

// implicitContext declares the libraries std and work and, except in
// std.standard itself, makes std.standard potentially visible (LRM
// 13.2).
func (a *analyzer) implicitContext() {
	ctx := a.cur
	if l, ok := a.s.library(a.u, "std"); ok {
		ctx.Libs["std"] = l.Decl
	}
	if l, ok := a.s.library(a.u, Work); ok {
		ctx.Libs[Work] = l.Decl
	}
	if a.inStandard {
		return
	}
	if r := a.s.standardRegion(); r != nil {
		ctx.Uses = append(ctx.Uses, &Use{All: true, Region: r})
	}
}

// contextClause processes library clauses, use clauses and context
// references into the current (context) region.
func (a *analyzer) contextClause(cc cst.ContextClause) {
	for item := range cc.Items() {
		if item.Inactive() {
			continue
		}
		switch item.Kind() {
		case cst.KindLibraryClause:
			for n := range (cst.LibraryClause{Node: item.Node}).Names() {
				a.libraryName(n)
			}
		case cst.KindUseClause:
			a.useClause(cst.UseClause{Node: item.Node})
		case cst.KindContextReference:
			cr := cst.ContextReference{Node: item.Node}
			for n := range cr.Names() {
				a.contextRef(n.Node)
			}
		}
	}
}

// libraryName declares one library name of a library clause.
func (a *analyzer) libraryName(n cst.SimpleName) {
	tok := n.Ident()
	k := TokenKey(tok)
	if k == "" {
		return
	}
	l, ok := a.s.library(a.u, k)
	if !ok {
		a.unmappedLibrary(tok) // libs.go
		return
	}
	a.cur.Libs[k] = l.Decl
	a.ref(tok, l.Decl, RefRead)
}

// useClause adds the potential visibility of each name of a use clause
// to the current region.
func (a *analyzer) useClause(uc cst.UseClause) {
	for n := range uc.Names() {
		a.useName(n.Node)
	}
}

func (a *analyzer) useName(n cst.Node) {
	sel, ok := cst.As[cst.SelectedName](n)
	if !ok {
		// `use lib;` is legal and does nothing; anything else is a
		// selected name by syntax.
		a.resolveName(n, RefSelect, nil)
		return
	}
	use := &Use{Node: n, Off: n.Span().End}
	if sel.Suffix().Kind() == token.KwAll {
		p := a.resolveName(sel.Prefix().Node, RefSelect, nil)
		switch {
		case p.lib != nil:
			use.All, use.Library = true, p.lib
		case len(p.decls) == 1 && p.decls[0].Denotes().Region != nil &&
			isPackageKind(p.decls[0].Denotes().Kind):
			if a.uninstantiatedUse(p.decls[0].Denotes(),
				sel.Prefix().Node) { // pkginst.go
				a.brokenUse(use)
				return
			}
			use.All, use.Region = true, p.decls[0].Denotes().Region
		case p.unknown:
			a.brokenUse(use)
			return
		default:
			a.semaError(sel.Prefix().Span(), "use clause: "+
				a.text(sel.Prefix().Node)+" is not a library or a package")
			a.brokenUse(use)
			return
		}
		a.cur.Uses = append(a.cur.Uses, use)
		return
	}
	r := a.resolveName(n, RefSelect, nil)
	if len(r.decls) == 0 {
		a.brokenUse(use)
		return
	}
	use.Decls = r.decls
	if a.std >= token.Std2008 {
		// `use p.T` also makes T's literals, units and predefined
		// operations visible (LRM 12.4, VHDL-2008).
		for _, d := range r.decls {
			if t := d.Denotes(); t.Kind.IsTypeMark() && t.Type != nil {
				b := t.Type.Base()
				use.Decls = append(use.Decls, b.Literals...)
				use.Decls = append(use.Decls, b.Units...)
				use.Decls = append(use.Decls, b.Ops...)
			}
		}
	}
	a.cur.Uses = append(a.cur.Uses, use)
}

// brokenUse records a use clause that names nothing (its error is
// reported): it makes nothing visible, but tells overload resolution
// that the visible candidates may be incomplete (overload.go).
func (a *analyzer) brokenUse(use *Use) {
	use.Broken = true
	a.cur.Uses = append(a.cur.Uses, use)
}

func isPackageKind(k DeclKind) bool {
	return k == DeclPackage || k == DeclPackageInst ||
		k == DeclGenericPackage
}

// contextRef inserts the context clause of a context declaration
// (LRM 13.4).
func (a *analyzer) contextRef(n cst.Node) {
	r := a.resolveName(n, RefSelect, nil)
	if len(r.decls) != 1 {
		return
	}
	d := r.decls[0]
	if d.Kind != DeclContext || d.Unit == nil || d.Unit.Context == nil {
		if !r.unknown {
			a.semaError(n.Span(), a.text(n)+" is not a context")
		}
		return
	}
	src := d.Unit.Context
	off := n.Span().End
	for _, k := range sortedKeys(src.Libs) {
		if k != Work && k != "std" {
			a.cur.Libs[k] = src.Libs[k]
		}
	}
	for _, us := range src.Uses {
		if us.Node.IsZero() {
			continue // the implicit std.standard use is already here
		}
		c := *us
		c.Off = off
		c.Node = n
		a.cur.Uses = append(a.cur.Uses, &c)
	}
}

// endClause checks the designator of an end clause against d and
// records it as a reference.
func (a *analyzer) endClause(e cst.EndClause, d *Decl) {
	tok := e.Name()
	if tok.IsZero() || tok.Missing() || d == nil {
		return
	}
	if TokenKey(tok) != d.Name {
		a.semaError(tok.Span(), "end name "+tok.Text()+
			" does not match "+d.Text)
		return
	}
	a.ref(tok, d, RefEnd)
}

func (a *analyzer) entity(e cst.EntityDecl) {
	d := a.newDecl(DeclEntity, e.Name(), e.Node)
	a.u.Decl = d
	a.declare(d)
	a.u.Region = a.push(RegionEntity, d, e.Node)
	a.interfaceList(e.Generics().List(), DeclGeneric)
	a.interfaceList(e.Ports().List(), DeclPort)
	a.declareLabels(e.Stmts().Node)
	a.declarativePart(e.Decls().Node)
	a.u.Body = a.concurrentStatements(e.Stmts().Node)
	a.endClause(e.End(), d)
	a.pop()
}

func (a *analyzer) architecture(b cst.ArchitectureBody, ent *Unit) {
	d := a.newDecl(DeclArchitecture, b.Name(), b.Node)
	if ent != nil {
		d.Spec = ent.Decl
	}
	a.u.Decl = d
	a.declare(d)
	a.u.Region = a.push(RegionArchitecture, d, b.Node)
	a.declareLabels(b.Stmts().Node)
	a.declarativePart(b.Decls().Node)
	a.u.Body = a.concurrentStatements(b.Stmts().Node)
	a.endClause(b.End(), d)
	a.pop()
}

// packageDecl analyzes a package declaration (a library unit or, in
// VHDL-2008, a nested package) and returns its declaration.
func (a *analyzer) packageDecl(p cst.PackageDecl) *Decl {
	d := a.newDecl(DeclPackage, p.Name(), p.Node)
	a.declare(d)
	a.push(RegionPackage, d, p.Node)
	a.interfaceList(p.Generics().List(), DeclGeneric)
	a.genericMap(p.GenericMap().List(), d.Region)
	a.declarativePart(p.Decls().Node)
	if a.inStandard {
		a.flushStandard()
	}
	a.endClause(p.End(), d)
	a.pop()
	return d
}

// packageBody analyzes a package body; pkg is the unit of its package
// (nil for a nested body, found in the current region).
func (a *analyzer) packageBody(p cst.PackageBody, pkg *Unit) *Decl {
	d := a.newDecl(DeclPackageBody, p.Name(), p.Node)
	var spec *Decl
	if pkg != nil {
		spec = pkg.Decl
	} else {
		for _, c := range a.lookup(d.Name).Decls {
			if c.Kind == DeclPackage {
				spec = c
				a.ref(p.Name(), c, RefBody)
				break
			}
		}
	}
	d.Spec = spec
	a.u.Decls = append(a.u.Decls, d)
	r := a.push(RegionPackageBody, d, p.Node)
	if pkg == nil && spec != nil && spec.Region != nil {
		// A nested body sees its package's declarations.
		r.Includes = append(r.Includes, spec.Region)
	}
	a.declarativePart(p.Decls().Node)
	a.endClause(p.End(), spec)
	a.pop()
	return d
}

// packageInst analyzes `package p is new q generic map (...)`. The
// instance shares the uninstantiated package's region: names resolve to
// the generic package's declarations, whose generic types stay generic
// (per-instance actuals are static.go's).
func (a *analyzer) packageInst(p cst.PackageInstantiation) *Decl {
	d := a.newDecl(DeclPackageInst, p.Name(), p.Node)
	r := a.resolveName(p.Package().Node, RefSelect, nil)
	if len(r.decls) == 1 && r.decls[0].Denotes().Kind == DeclPackage {
		d.Target = r.decls[0].Denotes()
		d.Region = d.Target.Region
		a.packageInstRules(p.Package().Node, d.Target, true) // pkginst.go
	} else if !r.unknown && len(r.decls) > 0 {
		a.semaError(p.Package().Span(), a.text(p.Package().Node)+
			" is not an uninstantiated package")
	}
	as := a.assocMap(p.GenericMap().List(), d.Region, DeclGeneric)
	d.instTypes, a.instSubst = a.instSubst, nil
	if d.Target != nil {
		a.mapRules(as, d.Region, DeclGeneric, p.Node) // assocrules.go
		a.missingGenerics(as, d.Region, p.Node)
	}
	return d
}

func (a *analyzer) configuration(c cst.ConfigurationDecl, ent *Unit) {
	d := a.newDecl(DeclConfiguration, c.Name(), c.Node)
	if ent != nil {
		d.Spec = ent.Decl
	}
	a.u.Decl = d
	a.declare(d)
	a.u.Region = a.push(RegionConfiguration, d, c.Node)
	a.declarativePart(c.Decls().Node)
	a.blockConfiguration(c.Block(), ent)
	a.endClause(c.End(), d)
	a.pop()
}

func (a *analyzer) contextDecl(c cst.ContextDecl) {
	d := a.newDecl(DeclContext, c.Name(), c.Node)
	a.u.Decl = d
	a.declare(d)
	a.u.Region = a.cur
	a.contextClause(c.Clause())
	a.endClause(c.End(), d)
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[Key]V) []Key {
	return slices.Sorted(maps.Keys(m))
}
