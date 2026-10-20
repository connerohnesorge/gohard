package sema

// Queries over a Result for the language server (internal/resolve):
// the region at an offset and the names visible there.

// ScopeAt returns the innermost declarative region of the file at path
// that contains off, or nil.
func (r *Result) ScopeAt(path string, off int) *Region {
	f, ok := r.File(path)
	if !ok {
		return nil
	}
	var best *Region
	for _, u := range f.Units {
		if u.Context == nil {
			continue
		}
		sp := u.Node.Span()
		if off < sp.Start || off > sp.End {
			continue
		}
		best = u.Context
		if u.Region != nil {
			best = innermost(u.Region, f, off)
		}
	}
	return best
}

// innermost descends from r into the child region containing off.
func innermost(r *Region, f *File, off int) *Region {
	for {
		var next *Region
		for _, c := range r.Children {
			if c.File == f && c.Span.Start <= off && off <= c.Span.End {
				next = c
				break
			}
		}
		if next == nil {
			return r
		}
		r = next
	}
}

// VisibleAt returns every declaration visible at off in the file at
// path, for completion, in a deterministic order.
func (r *Result) VisibleAt(path string, off int) []*Decl {
	reg := r.ScopeAt(path, off)
	if reg == nil {
		return nil
	}
	f, _ := r.File(path)
	return visibleNames(reg, f, off, r.Standard)
}

// LookupAt returns the declarations named k visible at off in the file
// at path.
func (r *Result) LookupAt(path string, off int, k Key) []*Decl {
	reg := r.ScopeAt(path, off)
	if reg == nil {
		return nil
	}
	f, _ := r.File(path)
	return lookup(reg, f, off, k, r.Standard).Decls
}

// AllUnits returns the bundled and project units in canonical order.
func (r *Result) AllUnits() []*Unit { return r.allUnits() }

// Homographs reports whether a and b, assumed to have the same
// designator, are homographs (LRM 4.5.1): at most one is overloadable,
// or both have the same parameter and result type profile. Rename
// uses it to refuse a new name that collides (internal/resolve).
func Homographs(a, b *Decl) bool { return homographs(a, b) }
