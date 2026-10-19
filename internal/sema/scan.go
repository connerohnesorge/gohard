package sema

import (
	"github.com/connerohnesorge/gohard/internal/cst"
)

// unitRef is a syntactic reference lib.unit found in a design unit: a
// use clause, a context reference, an expanded name or an entity
// instantiation. Node is the selected name, for diagnostics.
type unitRef struct {
	lib  Key
	unit Key
	node cst.Node
}

// scanFile lists the design units of f with their kind, names and
// syntactic references, without resolving anything. It is a pure
// function of the tree.
func scanFile(f *File) []*Unit {
	var out []*Unit
	for du := range (cst.DesignFile{Node: f.Root()}).Units() {
		lu := du.Unit().Node
		if lu.IsZero() || lu.Inactive() {
			continue
		}
		u := &Unit{File: f, Library: f.Library, Node: du.Node, Lib: lu}
		if !unitHeader(u, lu) {
			continue
		}
		u.uses = unitRefs(du.Node, f.Library)
		out = append(out, u)
	}
	return out
}

// unitHeader fills the kind and names of u from its library unit;
// false means the unit has no usable name (a parse error).
func unitHeader(u *Unit, lu cst.Node) bool {
	var name cst.Token
	switch lu.Kind() {
	case cst.KindEntityDecl:
		u.Kind, name = UnitEntity, cst.EntityDecl{Node: lu}.Name()
	case cst.KindArchitectureBody:
		a := cst.ArchitectureBody{Node: lu}
		u.Kind, name = UnitArchitecture, a.Name()
		u.Of = simpleKey(a.Entity().Node)
	case cst.KindPackageDecl:
		u.Kind, name = UnitPackage, cst.PackageDecl{Node: lu}.Name()
	case cst.KindPackageBody:
		u.Kind, name = UnitPackageBody, cst.PackageBody{Node: lu}.Name()
		u.Of = TokenKey(name)
	case cst.KindPackageInstantiation:
		u.Kind = UnitPackageInst
		name = cst.PackageInstantiation{Node: lu}.Name()
	case cst.KindConfigurationDecl:
		c := cst.ConfigurationDecl{Node: lu}
		u.Kind, name = UnitConfiguration, c.Name()
		u.Of = simpleKey(c.Entity().Node)
	case cst.KindContextDecl:
		u.Kind, name = UnitContext, cst.ContextDecl{Node: lu}.Name()
	case cst.KindPSLVunit, cst.KindPSLVmode, cst.KindPSLVprop:
		u.Kind = UnitVunit
		name = lu.TokenIn(nameTokens, 0)
	default:
		return false
	}
	u.Name = TokenKey(name)
	if u.Name == "" || (!u.Kind.Primary() && u.Of == "") {
		return false
	}
	return true
}

// simpleKey returns the key of a simple name node, or "".
func simpleKey(n cst.Node) Key {
	if sn, ok := cst.As[cst.SimpleName](n); ok {
		return TokenKey(sn.Ident())
	}
	return ""
}

// unitRefs finds every selected name L.U in the design unit du whose
// prefix L is a library name the unit declares (its library clauses,
// those of a context declaration included, std and work). own is the
// unit's library, which work stands for.
func unitRefs(du cst.Node, own Key) []unitRef {
	libs := map[Key]bool{"std": true, Work: true}
	for n := range du.Descendants() {
		if lc, ok := cst.As[cst.LibraryClause](n); ok {
			for n := range lc.Names() {
				libs[TokenKey(n.Ident())] = true
			}
		}
	}
	var out []unitRef
	for n := range du.Descendants() {
		sel, ok := cst.As[cst.SelectedName](n)
		if !ok {
			continue
		}
		lib := simpleKey(sel.Prefix().Node)
		if lib == "" || !libs[lib] {
			continue
		}
		unit := TokenKey(sel.Suffix())
		if unit == "" || unit == "all" {
			continue
		}
		if lib == Work {
			lib = own
		}
		out = append(out, unitRef{lib: lib, unit: unit, node: n})
	}
	return out
}
