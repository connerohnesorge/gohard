package sema

// Generic package and subprogram rules (#34, VHDL-2008): what an
// instantiation may name, and where an uninstantiated package may be
// named at all.

import (
	"slices"
	"strconv"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// uninstantiatedPackage reports whether x is a package declaration
// with a generic clause and no generic map aspect (LRM 4.7).
func uninstantiatedPackage(x *Decl) bool {
	if x == nil || x.Kind != DeclPackage || x.Region == nil {
		return false
	}
	if pd, ok := cst.As[cst.PackageDecl](x.Node); ok &&
		!pd.GenericMap().IsZero() {
		return false
	}
	for _, d := range x.Region.Decls {
		if isGenericKind(d.Kind) {
			return true
		}
	}
	return false
}

// insidePackage reports whether the analyzed region is within package
// x or its body.
func (a *analyzer) insidePackage(x *Decl) bool {
	for r := a.cur; r != nil; r = r.Parent {
		if r.Owner == x || r.Owner != nil &&
			r.Owner.Kind == DeclPackageBody && r.Owner.Spec == x {
			return true
		}
	}
	return a.u.Kind == UnitPackageBody && a.u.Decl != nil &&
		a.u.Decl.Spec == x
}

// uninstantiatedPrefix reports, and returns true for, an expanded name
// sel whose prefix x is an uninstantiated package, outside the package
// itself (LRM 8.3: its declarations exist only in instances).
func (a *analyzer) uninstantiatedPrefix(x *Decl, sel cst.SelectedName,
) bool {
	if !uninstantiatedPackage(x) || a.insidePackage(x) {
		return false
	}
	if !a.quiet {
		a.semaError(sel.Suffix().Span(), "cannot reference "+
			sel.Suffix().Text()+" in the uninstantiated package "+x.Text+
			" outside the package itself (LRM 8.3)")
	}
	return true
}

// uninstantiatedUse reports, and returns true for, `use p.all` with p
// an uninstantiated package (LRM 12.4).
func (a *analyzer) uninstantiatedUse(x *Decl, n cst.Node) bool {
	if !uninstantiatedPackage(x) || a.insidePackage(x) {
		return false
	}
	if !a.quiet {
		a.semaError(n.Span(), "a use clause cannot name the "+
			"uninstantiated package "+a.text(n)+" (LRM 12.4)")
	}
	return true
}

// packageInstRules checks the package x that the package name n of a
// package instantiation (instance) or an interface package declaration
// names: it is uninstantiated (LRM 4.9, 6.5.5), and for an instance
// its body, when it needs one, is analyzed before the instance (LRM
// 4.9, 13.5).
func (a *analyzer) packageInstRules(n cst.Node, x *Decl, instance bool) {
	if a.quiet || x == nil || x.Kind != DeclPackage {
		return
	}
	if !uninstantiatedPackage(x) {
		a.semaError(n.Span(), a.text(n)+" is not an uninstantiated "+
			"package (LRM 4.9)")
		return
	}
	if !instance || x.Unit == nil || x.Unit.Decl != x ||
		!needsPackageBody(x) {
		return
	}
	l, ok := a.s.libs[x.Unit.Library]
	if !ok {
		return
	}
	for _, b := range l.Secondary(x.Unit.Name) {
		if b.Kind == UnitPackageBody && !b.Duplicate &&
			!analyzedLater(b, a.u) {
			return
		}
	}
	a.semaError(n.Span(), "package "+x.Text+" cannot be instantiated "+
		"before its body is analyzed (LRM 4.9, 13.5)")
}

// needsPackageBody reports whether package x declares a subprogram,
// whose body only a package body can give (LRM 4.8).
func needsPackageBody(x *Decl) bool {
	for _, d := range x.Region.Decls {
		if d.Kind.IsSubprogram() && !d.Implicit && !d.Node.IsZero() &&
			isSubprogramDecl(d.Node) {
			return true
		}
	}
	return false
}

// subprogramInstRules checks the subprogram g that the subprogram
// instantiation si names: it is uninstantiated (LRM 4.4), and when it
// is declared in the same design unit its body comes before the
// instance.
func (a *analyzer) subprogramInstRules(si cst.SubprogramInstantiation,
	g *Decl,
) {
	if a.quiet || g == nil || !g.Kind.IsSubprogram() || g.Implicit {
		return
	}
	at := si.Subprogram().Span()
	if !uninstantiated(g) {
		a.semaError(at, g.Kind.String()+" "+g.Text+" is not an "+
			"uninstantiated subprogram (LRM 4.4)")
		return
	}
	if g.Unit != a.u || g.Node.Kind() == cst.KindSubprogramBody ||
		g.Parent == nil {
		return
	}
	switch g.Parent.Kind {
	case RegionPackage, RegionProtected:
		return // the body is in a package or protected type body
	}
	start := si.Node.Span().Start
	for _, d := range g.Parent.Decls {
		if d.Name == g.Name && d.Node.Kind() == cst.KindSubprogramBody &&
			d.Node.Span().Start < start {
			return
		}
	}
	a.semaError(at, g.Kind.String()+" "+g.Text+" cannot be "+
		"instantiated before its body (LRM 4.4)")
}

// mappedCall analyzes a VHDL-2019 call with a generic map aspect or a
// parameter map aspect (LRM 9.3.4, 4.4): f generic map (...) (...),
// f generic map (...) parameter map (...), f parameter map (...).
// With a generic map the candidates are the uninstantiated
// subprograms f denotes; each becomes an implicit instance (instance)
// and overload resolution, the one the editor shares (#13), chooses
// among the instances. Actuals written after a parameter map index
// the function's result (LRM 8.4, as NVC reads them); a procedure
// call cannot have both.
func (a *analyzer) mappedCall(c cst.CallOrIndexName, kind RefKind,
	want *Type, proc bool,
) named {
	pn := c.Prefix().Node
	gm := c.GenericMap()
	args, trail := c.Args(), cst.AssociationList{}
	if pm := c.ParameterMap(); !pm.IsZero() {
		args, trail = pm.List(), args
	}
	lenient := func() named {
		a.lenientArgs(gm.List())
		a.lenientArgs(args)
		a.lenientArgs(trail)
		return unknownNamed()
	}
	p := a.resolveName(pn, kind, nil)
	if p.unknown {
		return lenient()
	}
	if len(p.decls) == 0 || !p.decls[0].Overloadable() {
		if !a.quiet {
			what := " is not a subprogram, so it takes no parameter " +
				"map (LRM 9.3.4)"
			if !gm.IsZero() {
				what = " is not an uninstantiated subprogram (LRM 9.3.4)"
			}
			a.semaError(pn.Span(), a.text(pn)+what)
		}
		return lenient()
	}
	decls := p.decls
	var gs []*Decl
	if !gm.IsZero() {
		if decls, gs = a.instances(p, gm.List(), c.Node); decls == nil {
			return lenient()
		}
	}
	e := a.call(c.Node, named{decls: decls, tok: p.tok, expr: p.expr},
		args, want, proc)
	if len(gs) > 1 && e.Decl != nil && implicitInstance(e.Decl) {
		// Overloaded: the instances were made quietly; the chosen
		// one's generic map is analyzed again for its errors and refs.
		as := a.instanceMap(e.Decl.Target, gm.List(), c.Node)
		e.Decl.Generics = as
	}
	if !trail.IsZero() {
		if proc {
			if !a.quiet {
				a.semaError(trail.Span(), "a procedure call cannot "+
					"have both a parameter map aspect and another "+
					"actual parameter part (LRM 9.3.4)")
			}
			a.lenientArgs(trail)
			return named{expr: e}
		}
		return named{expr: a.index(c.Node, e, trail)}
	}
	return named{expr: e}
}

// instances returns the implicit instances, one per uninstantiated
// subprogram p denotes, that the generic map gm of the call at makes,
// and those subprograms; nil (reported) when p denotes none. With
// several candidates the maps are analyzed quietly and the instances
// whose map does not fit are left out (all are kept when none fits).
func (a *analyzer) instances(p named, gm cst.AssociationList,
	at cst.Node,
) (insts, gs []*Decl) {
	for _, d := range p.decls {
		x := d.Denotes()
		if x.Kind.IsSubprogram() && x.Spec != nil && uninstantiated(x.Spec) {
			x = x.Spec // a body: its declaration is the subprogram
		}
		if x.Kind.IsSubprogram() && uninstantiated(x) &&
			!slices.Contains(gs, x) {
			gs = append(gs, x)
		}
	}
	if len(gs) == 0 {
		if !a.quiet {
			x := p.decls[0].Denotes()
			a.semaError(p.tok.Span(), x.Kind.String()+" "+x.Text+
				" is not an uninstantiated subprogram (LRM 9.3.4)")
		}
		return nil, nil
	}
	if len(gs) == 1 {
		return []*Decl{a.instance(gs[0], gm, at, p.tok)}, gs
	}
	var all, fit []*Decl
	for _, g := range gs {
		mark, saved, misfits := len(a.u.Refs), a.quiet, a.misfits
		a.quiet = true
		inst := a.instance(g, gm, at, p.tok)
		a.quiet = saved
		a.u.Refs = a.u.Refs[:mark]
		all = append(all, inst)
		if mapFits(inst.Generics, g.Region) && a.misfits == misfits {
			fit = append(fit, inst)
		}
	}
	if len(fit) == 0 {
		fit = all
	}
	return fit, gs
}

// instance builds the implicit instance of the uninstantiated
// subprogram g that a VHDL-2019 call with the generic map gm makes
// (LRM 9.3.4): an anonymous subprogram declared at the call, as if by
// `function g is new g generic map (...)` (LRM 4.4).
//
// The shape, shared with the 2008 instantiation (subprogramInst):
//   - Kind is g's (function or procedure); Name and Text are g's.
//   - Target is g, the uninstantiated subprogram (its declaration
//     when it has one); its body is the body linked by Spec.
//   - Generics are the associations of the generic map in written
//     order, each Formal one of g.Region's generic declarations
//     (Formal nil for an unknown or extra one); g's generics without
//     an association take their defaults.
//   - Params and Type are g's, with the generic types replaced by
//     their actual subtypes (instanceProfile): copies of g's
//     parameters when a generic type is mapped, with the same Pos.
//   - Node is the call (a CallOrIndexName); File and Unit are the
//     call's, Span its prefix designator. Parent is g's (so messages
//     and hover name where the subprogram comes from). Region is
//     nil. The instance is declared nowhere: only the call's
//     Expr.Decl (and its Overload) holds it, while the refs of the
//     call record g (editorOverload), so hover and definition land on
//     the generic subprogram.
func (a *analyzer) instance(g *Decl, gm cst.AssociationList,
	at cst.Node, tok cst.Token,
) *Decl {
	d := &Decl{Kind: g.Kind, Name: g.Name, Text: g.Text, File: a.f,
		Span: cst.Range(tok.Span()), Full: at.Span(), Node: at,
		Unit: a.u, Parent: g.Parent, Target: g, Type: g.Type,
		Params: g.Params, Pure: g.Pure}
	d.Generics = a.instanceMap(g, gm, at)
	instanceProfile(d, d.Generics)
	return d
}

// instanceMap analyzes the generic map gm of an instance of g made at
// at, reporting the generics left without an actual.
func (a *analyzer) instanceMap(g *Decl, gm cst.AssociationList,
	at cst.Node,
) []Association {
	as := a.assocMap(gm, g.Region, DeclGeneric)
	a.instSubst = nil
	if a.extraGenerics(gm, g) {
		return as
	}
	a.mapRules(as, g.Region, DeclGeneric, at) // assocrules.go
	a.missingGenerics(as, g.Region, at)
	a.instanceObjects(g, as, at)
	if as == nil {
		as = []Association{} // an instance's map is never nil
	}
	return as
}

// instanceObjects reports, for an instance of g made at at with the
// generic map as, a variable or signal of g's body (analyzed already)
// declared of a generic type, without an initial value, whose actual
// is not fully constrained: in the instance the object would be of an
// unconstrained type (LRM 6.4.2.3, 6.4.2.4; NVC alike).
func (a *analyzer) instanceObjects(g *Decl, as []Association,
	at cst.Node,
) {
	if a.quiet || g == nil || g.Region == nil {
		return
	}
	body := g
	if g.Node.Kind() != cst.KindSubprogramBody {
		body = nil
		if g.Parent != nil {
			for _, d := range g.Parent.Decls {
				if d.Spec == g && d.Region != nil {
					body = d
					break
				}
			}
		}
	}
	if body == nil {
		return
	}
	specs, gens := genericDecls(g), genericDecls(body)
	if len(specs) != len(gens) {
		return
	}
	open := map[*Type]*Type{}
	for _, x := range as {
		if x.Formal == nil || x.Formal.Kind != DeclGenericType ||
			x.Actual == nil || x.Actual.Type.Unknown() ||
			!notFullyConstrained(x.Actual.Type, 0) {
			continue
		}
		switch x.Actual.Node.Kind() {
		case cst.KindSimpleName, cst.KindSelectedName:
		default:
			continue // a subtype indication may hold a constraint
		}
		if i := slices.Index(specs, x.Formal); i >= 0 {
			open[gens[i].Type.Base()] = x.Actual.Type
		}
	}
	if len(open) == 0 {
		return
	}
	for _, d := range body.Region.Decls {
		if d.Kind != DeclVariable && d.Kind != DeclSignal ||
			d.Value != nil || d.Type == nil {
			continue
		}
		if t, ok := open[d.Type.Base()]; ok {
			a.semaError(at.FirstToken().Span(), "in this instance of "+
				g.Text+", "+d.Kind.String()+" "+d.Text+" is of the "+
				"unconstrained type "+t.Name()+" (LRM 6.4.2.3)")
			return
		}
	}
}

// extraGenerics reports, and returns true for, a generic map with more
// positional actuals than g has generics (NVC alike).
func (a *analyzer) extraGenerics(gm cst.AssociationList, g *Decl) bool {
	n := len(genericDecls(g))
	i := 0
	for item := range gm.Items() {
		if !item.Formal().IsZero() {
			continue
		}
		if i++; i > n {
			if !a.quiet {
				noun := " generics"
				if n == 1 {
					noun = " generic"
				}
				a.semaError(item.Span(), "found at least "+
					strconv.Itoa(i)+" positional actuals but "+g.Text+
					" has only "+strconv.Itoa(n)+noun)
			}
			return true
		}
	}
	return false
}

// mapFits reports whether the generic associations as name only
// generics of region and give every generic that needs one an actual.
func mapFits(as []Association, region *Region) bool {
	given := map[*Decl]bool{}
	for _, x := range as {
		if x.Formal == nil || given[x.Formal] ||
			x.Formal.Kind == DeclGenericType && objectActual(x.Actual) {
			return false
		}
		given[x.Formal] = true
	}
	for _, d := range region.Decls {
		if needsActual(d) && !given[d] {
			return false
		}
	}
	return true
}

// implicitInstance reports whether d is the implicit instance of a
// VHDL-2019 call with a generic map (instance).
func implicitInstance(d *Decl) bool {
	return d != nil && d.Target != nil && d.Kind.IsSubprogram() &&
		d.Node.Kind() == cst.KindCallOrIndexName
}

// editorOverload returns o as the editor sees it (#13): implicit
// instances read as the generic subprograms they instantiate, which
// are declared somewhere. o itself is returned when it holds none.
func editorOverload(o *Overload) *Overload {
	if o == nil {
		return nil
	}
	has := implicitInstance(o.Chosen)
	for _, c := range o.Candidates {
		has = has || implicitInstance(c.Decl)
	}
	if !has {
		return o
	}
	out := *o
	out.Chosen = instanceTarget(o.Chosen)
	out.Candidates = make([]Candidate, len(o.Candidates))
	for i, c := range o.Candidates {
		c.Decl = instanceTarget(c.Decl)
		out.Candidates[i] = c
	}
	return &out
}

// instanceTarget returns the generic subprogram of an implicit
// instance, else d.
func instanceTarget(d *Decl) *Decl {
	if implicitInstance(d) {
		return d.Target
	}
	return d
}

// instanceFormal returns the parameter of the uninstantiated
// subprogram that the parameter f of the instance d copies
// (instanceProfile), so a named association refers to the declared
// parameter; f itself otherwise.
func instanceFormal(d, f *Decl) *Decl {
	if d == nil || f == nil || d.Target == nil || !d.Kind.IsSubprogram() ||
		d.Generics == nil {
		return f
	}
	g := d.Target
	if f.Pos >= 0 && f.Pos < len(g.Params) && f != g.Params[f.Pos] &&
		g.Params[f.Pos].Name == f.Name {
		return g.Params[f.Pos]
	}
	return f
}

// uninstantiatedCall reports a call of the uninstantiated subprogram d
// itself (LRM 4.4; LRM 9.3.4 in VHDL-2019 needs a generic map), as
// NVC does. Inside the subprogram, its designator denotes the
// instance being executed, so a recursive call is legal.
func (a *analyzer) uninstantiatedCall(tok cst.Token, d *Decl) {
	x := d.Denotes()
	if a.quiet || x == nil || !x.Kind.IsSubprogram() || x.Implicit ||
		implicitInstance(x) || !uninstantiated(x) || a.inside(x) {
		return
	}
	how := "; instantiate it first (LRM 4.4)"
	if a.std >= token.Std2019 {
		how = " without a generic map (LRM 9.3.4)"
	}
	a.semaError(tok.Span(), "cannot call uninstantiated "+
		x.Kind.String()+" "+x.Text+how)
}

// inside reports whether the analysis is within the subprogram x
// (its specification or its body, nested regions included).
func (a *analyzer) inside(x *Decl) bool {
	for r := a.cur; r != nil; r = r.Parent {
		o := r.Owner
		if o != nil && (o == x || o.Spec == x ||
			(x.Spec != nil && o == x.Spec)) {
			return true
		}
	}
	return false
}

// genericSubActual checks that the subprogram r the actual v of the
// generic subprogram f names has f's profile with the generic types
// of the instance replaced by their actuals (LRM 6.5.7.2), as NVC
// does: no visible subprogram of that name conforms is an error.
// The predefined operations of the actual types count as visible
// (Evaluator.instanceSubs finds them there too).
func (a *analyzer) genericSubActual(v cst.Node, r named, f *Decl) {
	if r.unknown || len(r.decls) == 0 {
		return
	}
	cands := slices.Clone(r.decls)
	name := r.decls[0].Denotes().Name
	for _, p := range append(slices.Clone(f.Params), &Decl{Type: f.Type}) {
		t := a.substGeneric(p.Type)
		if t == nil || t.Base() == nil || t.Base().Decl == nil ||
			t.Base().Decl.Parent == nil {
			continue
		}
		cands = append(cands, t.Base().Decl.Parent.Lookup(name)...)
	}
	for _, c := range cands {
		if profileConforms(c.Denotes(), f, a.substGeneric) {
			return
		}
	}
	a.misfits++
	if !a.quiet {
		a.semaError(v.Span(), "no visible subprogram "+a.text(v)+
			" matches the profile of generic subprogram "+f.Text+
			" (LRM 6.5.7.2)")
	}
}

// profileConforms reports whether the subprogram x has the profile of
// the generic subprogram d, d's types mapped by sub: as many
// parameters, the same base types and the same result base type. A
// type still unknown or generic on either side matches (a generic
// type of an enclosing generic unit is known only per instance).
func profileConforms(x, d *Decl, sub func(*Type) *Type) bool {
	if x == nil || !x.Overloadable() || x == d {
		return false
	}
	same := func(h, w *Type) bool {
		w = sub(w)
		if h.Unknown() || w.Unknown() ||
			h.Base().Class == TypeGeneric || w.Base().Class == TypeGeneric {
			return true
		}
		return SameBase(h, w)
	}
	ps := profileParams(x)
	if len(ps) != len(d.Params) {
		return false
	}
	xr, dr := resultType(x), resultType(d)
	if (xr == nil) != (dr == nil) || dr != nil && !same(xr, dr) {
		return false
	}
	for i, p := range ps {
		if !same(p.Type, d.Params[i].Type) {
			return false
		}
	}
	return true
}
