package sema

// Generic package and subprogram rules (#34, VHDL-2008): what an
// instantiation may name, and where an uninstantiated package may be
// named at all.

import (
	"github.com/connerohnesorge/gohard/internal/cst"
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
