package sema

import (
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
)

// Design-unit order rules (#34): what a unit of a design file may see
// of the units declared in the same file.

// analyzedLater reports whether u, the library unit from would see, is
// not yet in its library when from is analyzed: u is from itself, or a
// later unit of from's design file. A design file's units are
// analyzed in textual order and a unit enters its library only once
// its analysis is done (LRM 13.1, 13.5), so neither a unit nor the
// units after it in its file may be named from it. Files have no
// analysis order (#34), so a unit of another file is always analyzed.
func analyzedLater(u, from *Unit) bool {
	if u == nil || from == nil || from.File == nil ||
		from.File.Bundled || u.File != from.File || u.Node.IsZero() ||
		from.Node.IsZero() {
		return false
	}
	return u == from || u.Node.Span().Start > from.Node.Span().Start
}

// seenFrom returns the unit of l that from sees in place of u: u, or
// while u is not yet analyzed when from is (analyzedLater), the unit
// it replaced in the library, which the library still holds then (LRM
// 13.5); false when there is none.
func (l *Library) seenFrom(u, from *Unit) (*Unit, bool) {
	for analyzedLater(u, from) {
		old, ok := l.replaced[u]
		if !ok {
			return nil, false
		}
		u = old
	}
	return u, true
}

// notYet explains a missing unit k of l that the analyzed unit's own
// file declares at or after it (analyzedLater), for
// library/missing-unit; "" otherwise.
func (a *analyzer) notYet(l *Library, k Key) string {
	u, ok := l.Unit(k)
	if !ok || !analyzedLater(u, a.u) {
		return ""
	}
	if u == a.u {
		return "; a unit is in its library only after its analysis, " +
			"LRM 13.5"
	}
	return "; " + u.Label() + " is declared later in this file and " +
		"analyzed after this unit, LRM 13.5"
}

// missingBodies reports each subprogram declaration that has no body
// where the LRM requires one: in the same declarative part (LRM 4.3),
// in the package body of a package that has one (LRM 4.8), or in the
// protected type body of a protected type that has one (LRM 5.6.3). A
// package or protected type with no body at all is an elaboration
// error, not an analysis one, so it is not reported here; nor is a
// subprogram with the FOREIGN attribute, whose body is not VHDL.
func (s *session) missingBodies() {
	if s.base == nil {
		return // the bundle is checked by its own tests
	}
	bodied := map[*Decl]bool{}
	protBodies := map[*Region]bool{}
	nestedBodies := map[*Decl]bool{}
	for _, u := range s.units {
		for _, d := range u.Decls {
			if d.Spec == nil {
				continue
			}
			switch {
			case d.Kind.IsSubprogram():
				bodied[d.Spec] = true
			case d.Kind == DeclPackageBody:
				nestedBodies[d.Spec] = true
			}
		}
		walkRegions(u.Region, func(r *Region) {
			if r.Kind == RegionProtectedBody {
				for _, inc := range r.Includes {
					protBodies[inc] = true
				}
			}
		})
	}
	for _, u := range s.units {
		if u.Duplicate || u.File == nil || u.File.Bundled {
			continue
		}
		foreign := foreignNames(u.Node)
		for _, d := range u.Decls {
			if d.Kind == DeclType && d.Type != nil &&
				d.Type.Class == TypeProtected && d.Type.Members != nil &&
				!protBodies[d.Type.Members] && d.Parent != nil &&
				d.Parent.Kind != RegionProtected &&
				s.needsBody(u, d.Parent, protBodies, nestedBodies) {
				// LRM 5.6.1, 5.6.3: a protected type declaration has its
				// body in the same declarative region (or in the package
				// body of its package).
				u.diags = append(u.diags, at(u.File, d.Span,
					diag.New(diag.SemaError, "missing body for protected "+
						"type "+d.Text)))
				continue
			}
			if !d.Kind.IsSubprogram() || d.Implicit || bodied[d] ||
				d.Node.IsZero() || foreign[d.Name] ||
				!isSubprogramDecl(d.Node) || d.Parent == nil ||
				uninstantiated(d) {
				continue
			}
			if !s.needsBody(u, d.Parent, protBodies, nestedBodies) {
				continue
			}
			u.diags = append(u.diags, at(u.File, d.Span,
				diag.New(diag.SemaError, "missing body for "+
					d.Kind.String()+" "+d.Text+" "+Signature(d))))
		}
	}
}

// needsBody reports whether a subprogram declared in region r of unit
// u must have its body analyzed by now.
func (s *session) needsBody(u *Unit, r *Region,
	protBodies map[*Region]bool, nestedBodies map[*Decl]bool,
) bool {
	switch r.Kind {
	case RegionPackage:
		if r.Owner == nil {
			return false
		}
		if r.Owner == u.Decl && u.Kind == UnitPackage {
			l, ok := s.libs[u.Library]
			if !ok {
				return false
			}
			for _, b := range l.Secondary(u.Name) {
				if b.Kind == UnitPackageBody && !b.Duplicate &&
					!b.Opaque {
					return true
				}
			}
			return false
		}
		return nestedBodies[r.Owner]
	case RegionProtected:
		return protBodies[r]
	case RegionEntity, RegionArchitecture, RegionPackageBody,
		RegionSubprogram, RegionProcess, RegionBlock, RegionGenerate,
		RegionProtectedBody:
		return true
	}
	return false
}

// uninstantiated reports whether d is a generic subprogram (VHDL-2008):
// one with a generic list, called only through an instance (LRM 4.4,
// 9.3.4). findSpec links its body to it through sameGenericProfile,
// the body's generic types read as the declaration's by position.
func uninstantiated(d *Decl) bool {
	if d.Region == nil {
		return false
	}
	for _, x := range d.Region.Decls {
		if isGenericKind(x.Kind) {
			return true
		}
	}
	return false
}

// isSubprogramDecl reports whether n declares a subprogram without
// its body: a subprogram declaration or its specification.
func isSubprogramDecl(n cst.Node) bool {
	switch n.Kind() {
	case cst.KindSubprogramDecl:
		return true
	case cst.KindFunctionSpec, cst.KindProcedureSpec:
		p := n.Parent()
		return p.IsZero() || p.Kind() == cst.KindSubprogramDecl
	}
	return false
}

// foreignNames returns the names that an attribute specification of
// FOREIGN in n decorates.
func foreignNames(n cst.Node) map[Key]bool {
	out := map[Key]bool{}
	for x := range n.Descendants() {
		as, ok := cst.As[cst.AttributeSpec](x)
		if !ok || TokenKey(as.Designator()) != "foreign" {
			continue
		}
		for ed := range as.Entities().Names().Designators() {
			out[TokenKey(ed.Tag())] = true
		}
	}
	return out
}

// walkRegions calls f on r and every region nested in it.
func walkRegions(r *Region, f func(*Region)) {
	if r == nil {
		return
	}
	f(r)
	for _, c := range r.Children {
		walkRegions(c, f)
	}
}

// inheritedRefs returns the selected names L.U of a secondary unit u
// whose prefix L is a library that only u's primary unit declares: the
// context clause of a primary unit applies to its secondary units (LRM
// 13.1), so u depends on those units as on its own references.
func (s *session) inheritedRefs(u *Unit) []unitRef {
	if u.Kind.Primary() && u.Kind != UnitConfiguration {
		return nil
	}
	l, ok := s.library(u, Work)
	if !ok {
		return nil
	}
	p, ok := l.UnitFrom(u.Of, u)
	if !ok || p.Lib.IsZero() || p.File == nil || p.File.Bundled {
		return nil
	}
	own := libraryNames(u.Node)
	extra := map[Key]bool{}
	for k := range libraryNames(p.Node) {
		if !own[k] {
			extra[k] = true
		}
	}
	if len(extra) == 0 {
		return nil
	}
	var out []unitRef
	for n := range u.Node.Descendants() {
		sel, ok := cst.As[cst.SelectedName](n)
		if !ok {
			continue
		}
		lib := simpleKey(sel.Prefix().Node)
		unit := TokenKey(sel.Suffix())
		if extra[lib] && unit != "" && unit != "all" {
			out = append(out, unitRef{lib: lib, unit: unit, node: n})
		}
	}
	return out
}

// libraryNames returns the names of the library clauses in n.
func libraryNames(n cst.Node) map[Key]bool {
	out := map[Key]bool{}
	for x := range n.Descendants() {
		if lc, ok := cst.As[cst.LibraryClause](x); ok {
			for id := range lc.Names() {
				out[TokenKey(id.Ident())] = true
			}
		}
	}
	return out
}

// calledBeforeBody reports a function called while a declarative item
// is elaborated (an object or subtype declaration) before the
// function's body is: a body later in the same design unit is not
// elaborated yet (LRM 14.4.1). A call in a package declaration of a
// function whose body is in the package body is the same error, but it
// is left to elaboration: GoHard's static evaluation reads the body.
func (s *session) calledBeforeBody() {
	if s.base == nil {
		return
	}
	bodyOf := map[*Decl]*Decl{}
	for _, u := range s.units {
		for _, d := range u.Decls {
			if d.Spec != nil && d.Kind.IsSubprogram() {
				bodyOf[d.Spec] = d
			}
		}
	}
	for _, u := range s.units {
		if u.Duplicate || u.File == nil || u.File.Bundled {
			continue
		}
		root := u.File.Root()
		for _, r := range u.Refs {
			d := r.Decl
			if r.Kind != RefCall || d == nil || d.Kind != DeclFunction ||
				d.Implicit || d.Unit == nil || d.File == nil ||
				d.File.Bundled || uninstantiated(d) || d.Parent == nil ||
				d.Parent.Kind == RegionProtected {
				continue
			}
			body := bodyOf[d]
			if d.Node.Kind() == cst.KindSubprogramBody {
				body = d
			}
			if body == nil || body.Unit != u || r.File != u.File ||
				body.Span.Start < r.Span.Start {
				continue // elsewhere, or the body comes first
			}
			if !elaboratedHere(root.TokenAt(r.Span.Start)) {
				continue
			}
			u.diags = append(u.diags, at(u.File, r.Span,
				diag.New(diag.SemaError, "function "+d.Text+" is "+
					"called before its body is elaborated "+
					"(LRM 14.4.1)")))
		}
	}
}

// elaboratedHere reports whether tok is in a declarative item whose
// expressions are evaluated when the declaration is elaborated, and
// not inside a subprogram, a process, an interface list or a
// statement (which run or are elaborated later).
func elaboratedHere(tok cst.Token) bool {
	if tok.IsZero() {
		return false
	}
	found := false
	for x := range tok.Parent().Ancestors() {
		switch x.Kind() {
		case cst.KindConstantDecl, cst.KindSignalDecl,
			cst.KindVariableDecl, cst.KindSubtypeDecl, cst.KindTypeDecl,
			cst.KindFileDecl:
			found = true
		case cst.KindSubprogramBody, cst.KindSubprogramDecl,
			cst.KindProcessStmt, cst.KindInterfaceObjectDecl,
			cst.KindComponentDecl, cst.KindProtectedTypeBody,
			cst.KindPackageInstantiation, cst.KindAttributeSpec:
			return false
		}
		if (cst.ConcStmt{}).Accepts(x.Kind()) &&
			x.Kind() != cst.KindBlockStmt {
			return false
		}
	}
	return found
}
