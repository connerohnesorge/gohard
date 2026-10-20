package sema

import (
	"strconv"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/token"
)

// This file is the visibility machinery of LRM 12: direct visibility
// through the region chain, hiding by homographs, potential visibility
// through use clauses (12.4 rules a to c), and the per-standard views of
// the bundled packages (#33).

// visibleIn reports whether d is visible to a unit analyzed under std:
// declarations newer than the standard are hidden (the bundled views of
// #33 and the VHDL-2008 implicit operations).
func visibleIn(d *Decl, std token.Standard) bool {
	return d.First == 0 || d.First <= std
}

// before reports whether d is declared before offset off in file f; d
// in another file always is.
func before(d *Decl, f *File, off int) bool {
	return d.File != f || d.Implicit || d.Span.Start < off
}

// homographs reports whether a and b are homographs (LRM 4.5.1): the
// same designator and at most one overloadable, or both overloadable
// with the same parameter and result type profile. The designators are
// assumed equal.
func homographs(a, b *Decl) bool {
	if a.Denotes() == b.Denotes() {
		return true
	}
	if !a.Overloadable() || !b.Overloadable() {
		return true
	}
	return sameProfile(a.Denotes(), b.Denotes())
}

// sameProfile compares the parameter and result type profiles of two
// overloadable declarations by base type.
func sameProfile(a, b *Decl) bool {
	pa, pb := profileParams(a), profileParams(b)
	if len(pa) != len(pb) {
		return false
	}
	for i := range pa {
		if !sameBaseOrUnknown(pa[i].Type, pb[i].Type) {
			return false
		}
	}
	ra, rb := resultType(a), resultType(b)
	if (ra == nil) != (rb == nil) {
		return false
	}
	return ra == nil || sameBaseOrUnknown(ra, rb)
}

func sameBaseOrUnknown(a, b *Type) bool {
	if a.Unknown() || b.Unknown() {
		return a.Unknown() && b.Unknown()
	}
	return a.Base() == b.Base()
}

// profileParams returns the parameters of a subprogram (none for an
// enumeration literal).
func profileParams(d *Decl) []*Decl {
	if d.Kind == DeclEnumLiteral {
		return nil
	}
	return d.Params
}

// resultType returns the result type of a function or enumeration
// literal, nil for a procedure.
func resultType(d *Decl) *Type {
	switch d.Kind {
	case DeclEnumLiteral:
		return d.Type
	case DeclFunction, DeclGenericSubprogram:
		return d.Type
	}
	return nil
}

// dropHidden removes, among declarations of one region, implicit
// operations hidden by an explicit homograph (LRM 12.3).
func dropHidden(ds []*Decl) []*Decl {
	if len(ds) < 2 {
		return ds
	}
	var out []*Decl
	for _, d := range ds {
		hidden := false
		if d.Implicit {
			for _, e := range ds {
				if e != d && !e.Implicit && homographs(d, e) {
					hidden = true
					break
				}
			}
		}
		if !hidden {
			out = append(out, d)
		}
	}
	return out
}

// lookupResult is the outcome of a name lookup.
type lookupResult struct {
	// Decls are the visible declarations, inner regions first, then the
	// use-visible ones in use-clause order.
	Decls []*Decl
	// Conflict lists use-visible declarations that hide each other by
	// LRM 12.4 rule c (two packages declaring the same non-overloadable
	// name), for the "not visible" message.
	Conflict []*Decl
}

// lookup finds the declarations named k visible from region r at
// offset off of file f (off < 0: everything declared so far, as during
// analysis).
func lookup(r *Region, f *File, off int, k Key,
	std token.Standard,
) lookupResult {
	var direct, late []*Decl
	stop := false
	for reg := r; reg != nil && !stop; reg = reg.Parent {
		var here []*Decl
		for _, d := range reg.Lookup(k) {
			if !visibleIn(d, std) || off >= 0 && !before(d, f, off) {
				continue
			}
			if d.Kind == DeclArchitecture &&
				reg.Kind == RegionContext && reg.Parent != nil {
				// The architecture's own name sits in its context
				// region, which is searched before the entity's: it
				// must not hide a port or generic of the same name
				// (architecture a of e, port a).
				late = append(late, d)
				continue
			}
			here = append(here, d)
		}
		for _, inc := range reg.Includes {
			for _, d := range inc.Lookup(k) {
				if visibleIn(d, std) {
					here = append(here, d)
				}
			}
		}
		if ld, ok := reg.Libs[k]; ok {
			if reg.Kind == RegionContext && reg.Parent != nil {
				// A secondary unit's library clauses extend the
				// context of its primary unit, whose declarations
				// (an entity's type named work) are inner to it
				// (LRM 12.1, 13.2).
				late = append(late, ld)
			} else {
				here = append(here, ld)
			}
		}
		for _, d := range dropHidden(here) {
			if hiddenBy(d, direct) {
				continue
			}
			direct = append(direct, d)
			if !d.Overloadable() {
				stop = true
			}
		}
	}
	if !stop && len(late) > 0 {
		direct = append(direct, late...)
		stop = true
	}
	if stop {
		return lookupResult{Decls: direct}
	}
	pot := potential(r, f, off, k, std)
	var keep []*Decl
	for _, p := range pot {
		if !hiddenBy(p, direct) { // rule a
			keep = append(keep, p)
		}
	}
	if std >= token.Std2008 { // rule b is VHDL-2008's (typecheck.go)
		keep = ruleB(keep)
	}
	if len(keep) > 1 {
		for _, p := range keep {
			if !p.Overloadable() { // rule c
				return lookupResult{Decls: direct, Conflict: keep}
			}
		}
	}
	return lookupResult{Decls: append(direct, keep...)}
}

// hiddenBy reports whether d is a homograph of a declaration in inner,
// which then hides it.
func hiddenBy(d *Decl, inner []*Decl) bool {
	for _, e := range inner {
		if homographs(d, e) {
			return true
		}
	}
	return false
}

// ruleB drops implicit declarations that are homographs of explicit
// ones among the potentially visible declarations (LRM 12.4 b).
func ruleB(ps []*Decl) []*Decl {
	return dropHidden(ps)
}

// potential collects the declarations named k made potentially visible
// by the use clauses of r and its parents in effect at off, without
// repeats (two paths to one named entity are one declaration).
func potential(r *Region, f *File, off int, k Key,
	std token.Standard,
) []*Decl {
	var out []*Decl
	add := func(d *Decl) {
		if !visibleIn(d, std) {
			return
		}
		for i, o := range out {
			if o == d || o.Denotes() == d.Denotes() &&
				sameEntity(o, d, std) {
				return
			}
			// An incomplete type declaration and its full
			// declaration declare one type (LRM 5.4.2): the full
			// one stands for both.
			switch {
			case completes(d, o):
				out[i] = d
				return
			case completes(o, d):
				return
			}
		}
		out = append(out, d)
	}
	for reg := r; reg != nil; reg = reg.Parent {
		for _, u := range reg.Uses {
			if off >= 0 && reg.File == f && !u.Node.IsZero() &&
				u.Off >= off {
				continue // not in effect yet at off
			}
			switch {
			case u.All && u.Region != nil:
				for _, d := range dropHidden(u.Region.Lookup(k)) {
					add(d)
				}
			case u.All && u.Library != nil:
				if un, ok := u.Library.Unit(k); ok && un.Decl != nil &&
					!un.Duplicate {
					add(un.Decl)
				}
			default:
				for _, d := range u.Decls {
					if d.Name == k {
						add(d)
					}
				}
			}
		}
	}
	return out
}

// completes reports whether full is the full type declaration of the
// incomplete type declaration inc.
func completes(full, inc *Decl) bool {
	return full.Kind == DeclType && inc.Kind == DeclType &&
		full.Spec == inc
}

// completed reports whether ds holds the full declaration of the
// incomplete type declaration d.
func completed(d *Decl, ds []*Decl) bool {
	for _, e := range ds {
		if completes(e, d) {
			return true
		}
	}
	return false
}

// sameEntity reports whether two potentially visible declarations
// denoting one named entity count as one (LRM 12.4). Before VHDL-2008
// an alias of a type is a declaration of its own, so a type and its
// alias made visible by two use clauses hide each other (rule c), as
// GHDL and NVC report; from 2008 on they are one named entity. Aliases
// of subprograms and literals are overloadable, so rule c never
// applies to them.
func sameEntity(a, b *Decl, std token.Standard) bool {
	if std >= token.Std2008 || a.Kind != DeclAlias && b.Kind != DeclAlias {
		return true
	}
	return !a.Denotes().Kind.IsTypeMark()
}

// lookupIn finds the declarations named k inside a region as an
// expanded name prefix.suffix sees them (LRM 8.3): the region's own
// declarations, explicit ones hiding implicit homographs.
func lookupIn(r *Region, k Key, std token.Standard) []*Decl {
	var out []*Decl
	ds := r.Lookup(k)
	for _, d := range ds {
		if visibleIn(d, std) && !completed(d, ds) {
			out = append(out, d)
		}
	}
	return dropHidden(out)
}

// visibleNames returns every declaration visible from r at off in f,
// for completion: directly visible ones first (inner regions first),
// then use-visible ones, each name once per declaration, in a
// deterministic order.
func visibleNames(r *Region, f *File, off int,
	std token.Standard,
) []*Decl {
	var keys []Key
	seen := map[Key]bool{}
	note := func(k Key) {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for reg := r; reg != nil; reg = reg.Parent {
		for _, d := range reg.Decls {
			note(d.Name)
		}
		for _, inc := range reg.Includes {
			for _, d := range inc.Decls {
				note(d.Name)
			}
		}
		for _, k := range sortedKeys(reg.Libs) {
			note(k)
		}
		for _, u := range reg.Uses {
			switch {
			case u.All && u.Region != nil:
				for _, d := range u.Region.Decls {
					note(d.Name)
				}
			case u.All && u.Library != nil:
				for _, un := range u.Library.Units() {
					if un.Kind.Primary() {
						note(un.Name)
					}
				}
			default:
				for _, d := range u.Decls {
					note(d.Name)
				}
			}
		}
	}
	var out []*Decl
	for _, k := range keys {
		out = append(out, lookup(r, f, off, k, std).Decls...)
	}
	return out
}

// duplicateBody reports the body d of the subprogram declared by spec
// when the current region already has a body of it: a subprogram has
// one body (LRM 4.3; the second is an illegal homograph, 12.3).
func (a *analyzer) duplicateBody(d, spec *Decl) {
	if a.quiet {
		return
	}
	for _, x := range a.cur.Decls {
		if x == d || x.Spec != spec || x.Kind != d.Kind ||
			x.File == nil {
			continue
		}
		l, _ := x.File.Lines.LineCol(x.Span.Start)
		a.report(d.Span, diag.New(diag.SemaError,
			"the body of "+d.Text+" is already defined (line "+
				strconv.Itoa(l)+")").WithRelated(diag.Related{
			Path: x.Path(), Line: l, Col: col(x),
			Message: "first body of " + x.Text}))
		return
	}
}
