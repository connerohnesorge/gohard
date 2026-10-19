package sema

// libs.go reports the library and discovery problems of #34 and #20:
// unmapped libraries (with quick-fix data, LibraryFix), missing units
// (with what was scanned), duplicate units (naming both files) and
// import cycles (with the cycle path), and makes simulation.models
// stand-ins the visible units (applyStandIns). Missing or unreadable
// library folders are config.Load's (library/folder-missing, exit 2);
// the project root and file discovery are internal/workspace's.

import (
	"path"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
)

// unmappedLibrary reports a library clause naming a library that is
// neither bundled nor mapped in gohard.yaml, with the quick fix when a
// workspace folder declares the units the design uses from it, and
// declares a placeholder so names through it stay quiet.
func (a *analyzer) unmappedLibrary(tok cst.Token) {
	k := TokenKey(tok)
	dg := diag.New(diag.LibraryUnmapped, tok.Text())
	if fix, ok := libraryFix(a.s.files, a.u, k, tok.Text()); ok {
		dg = dg.WithRelated(fix.related(a.s.cfg))
	}
	a.report(tok.Span(), dg)
	a.cur.Libs[k] = &Decl{Kind: DeclLibrary, Name: k, Text: tok.Text(),
		Implicit: true}
}

// LibraryFix is the quick fix of library/unmapped (#34): the gohard.yaml
// libraries entry mapping the library to a workspace folder that
// declares the units the design uses from it. The search is the
// workspace part of `gohard libs detect` (#1); the language server's
// "library not found" code action applies it.
type LibraryFix struct {
	// Library is the library name as written in the library clause.
	Library string
	// Folder is the project-relative slash path to map.
	Folder string
	// Entry is the line to add under libraries: ("lib: [folder]").
	Entry string
	// Units are the units the folder declares that the referencing
	// unit uses (lib.unit), sorted.
	Units []string
}

// related is the fix as a related location at the libraries key of
// gohard.yaml (or its first line when the key is not set).
func (f LibraryFix) related(c config.Config) diag.Related {
	why := "the folder is named like the library"
	if len(f.Units) > 0 {
		why = f.Folder + " declares " + strings.Join(f.Units, ", ")
	}
	r := diag.Related{Path: config.FileName, Line: 1, Col: 1,
		Message: "quick fix: add `" + f.Entry + "` under libraries: (" +
			why + ")"}
	if c.IsSet("libraries") {
		p := c.Position("libraries")
		r.Line, r.Col = max(p.Line, 1), max(p.Col, 1)
	}
	return r
}

// LibraryFixAt returns the quick fix for the unmapped library name at
// byte offset off of the file at path.
func (r *Result) LibraryFixAt(p string, off int) (LibraryFix, bool) {
	f, ok := r.File(p)
	if !ok || f.Bundled {
		return LibraryFix{}, false
	}
	for _, u := range f.Units {
		sp := u.Node.Span()
		if u.Context == nil || off < sp.Start || off > sp.End {
			continue
		}
		for t := range u.Node.DescendantTokens() {
			rg := t.Span()
			if rg.Start > off || off > rg.End {
				continue
			}
			k := TokenKey(t)
			d, ok := u.Context.Libs[k]
			if !ok || !d.Implicit || t.Parent().Kind() !=
				cst.KindSimpleName {
				return LibraryFix{}, false
			}
			if _, mapped := r.libs[k]; mapped {
				return LibraryFix{}, false
			}
			return libraryFix(r.Files, u, k, t.Text())
		}
	}
	return LibraryFix{}, false
}

// libraryFix searches the project files for the folder that declares
// the units u uses from the unmapped library k: the deepest folder
// holding every such unit, or, when u names no unit of k, a folder
// named like the library. A folder holding u's own file, or the whole
// project, is never proposed (mapping it would move u into k).
func libraryFix(files []*File, u *Unit, k Key, text string) (LibraryFix,
	bool,
) {
	var want []Key
	for _, r := range u.uses {
		if r.lib == k && r.unit != "" && !slices.Contains(want, r.unit) {
			want = append(want, r.unit)
		}
	}
	slices.Sort(want)
	var dirs, units []string
	for _, f := range files {
		if f.Bundled {
			continue
		}
		for _, d := range f.Units {
			if d.Kind.Primary() && slices.Contains(want, d.Name) {
				dirs = append(dirs, path.Dir(f.Path))
				units = append(units, string(k)+"."+string(d.Name))
			}
		}
	}
	folder := ""
	if len(dirs) > 0 {
		folder = commonDir(dirs)
	} else if len(want) == 0 {
		folder = namedDir(files, k)
	}
	own := ""
	if u.File != nil {
		own = u.File.Path
	}
	if folder == "" || folder == "." || inFolder(own, folder) {
		return LibraryFix{}, false
	}
	slices.Sort(units)
	units = slices.Compact(units)
	return LibraryFix{Library: text, Folder: folder,
		Entry: text + ": [" + folder + "]", Units: units}, true
}

// commonDir returns the deepest folder containing every dir.
func commonDir(dirs []string) string {
	common := strings.Split(dirs[0], "/")
	for _, d := range dirs[1:] {
		parts := strings.Split(d, "/")
		n := 0
		for n < len(common) && n < len(parts) && common[n] == parts[n] {
			n++
		}
		common = common[:n]
	}
	if len(common) == 0 {
		return "."
	}
	return strings.Join(common, "/")
}

// namedDir returns the shortest (then first) project folder named like
// library k, or "".
func namedDir(files []*File, k Key) string {
	best := ""
	for _, f := range files {
		if f.Bundled {
			continue
		}
		parts := strings.Split(path.Dir(f.Path), "/")
		for i, p := range parts {
			if IdentKey(p) != k {
				continue
			}
			d := strings.Join(parts[:i+1], "/")
			if best == "" || len(d) < len(best) ||
				(len(d) == len(best) && d < best) {
				best = d
			}
			break
		}
	}
	return best
}

// applyStandIns makes each simulation.models stand-in the visible unit
// of its library in place of the unit it stands in for (#20, #35):
// dependents are analyzed against the stand-in's ports. The stand-in
// file declares the same unit, which is not a duplicate; the original
// stays analyzed (it is opaque when encrypted) but hidden (Duplicate).
func (s *session) applyStandIns() {
	if len(s.cfg.Simulation.Models) == 0 {
		return
	}
	var keep []duplicate
	for _, d := range s.dups {
		orig, st := d.first, d.second
		if !s.isStandIn(st, orig) {
			orig, st = st, orig
		}
		if !d.first.Kind.Primary() || !s.isStandIn(st, orig) {
			keep = append(keep, d)
			continue
		}
		if l, ok := s.libs[orig.Library]; ok {
			l.units[orig.Name] = st
		}
		st.Duplicate, orig.Duplicate = false, true
	}
	s.dups = keep
}

// isStandIn reports whether st is in the simulation.models file of
// orig's lib.unit (and orig is not).
func (s *session) isStandIn(st, orig *Unit) bool {
	p, ok := s.modelFor(orig)
	return ok && st.File != nil && orig.File != nil &&
		st.File.Path == p && orig.File.Path != p
}

// missingUnit reports library/missing-unit for lib.k at tok.
func (a *analyzer) missingUnit(l *Library, k Key, tok cst.Token) {
	if a.quiet || l == nil || k == "" {
		return // k is empty after a syntax error
	}
	a.u.missing = append(a.u.missing, unitRef{lib: l.Name, unit: k,
		node: tok.Parent()})
	a.report(tok.Span(), diag.New(diag.LibraryMissingUnit,
		string(l.Name), tok.Text(), a.scanned(l)))
}

// scanned describes where a library's units come from, for
// library/missing-unit.
func (a *analyzer) scanned(l *Library) string {
	switch {
	case len(l.Folders) > 0:
		return strings.Join(l.Folders, ", ")
	case l.Name == Work:
		return "the project files not mapped to a library"
	case l.Bundled:
		return "the bundled " + string(l.Name) + " library for VHDL-" +
			a.std.String()
	}
	return "no folders"
}

// libraryDiagnostics reports duplicate units and import cycles.
func (s *session) libraryDiagnostics() {
	for _, d := range s.dups {
		first, second := d.first, d.second
		fp, sp := pathOf(first), pathOf(second)
		dg := diag.New(diag.LibraryDuplicateUnit, unitName(second),
			string(second.Library), fp, sp)
		if first.Decl != nil && first.File != nil {
			l, c := first.File.Lines.LineCol(first.Decl.Span.Start)
			dg = dg.WithRelated(diag.Related{Path: fp, Line: l, Col: c,
				Message: "first declaration of " + unitName(first)})
		}
		rg := second.Lib.Span()
		if second.Decl != nil {
			rg = second.Decl.Span
		}
		second.diags = append(second.diags, at(second.File, rg, dg))
	}
	for _, comp := range s.cycles {
		path := cyclePath(comp)
		u := comp[0]
		rg := u.Lib.Span()
		if u.Decl != nil {
			rg = u.Decl.Span
		}
		var names []string
		for _, c := range path {
			names = append(names, c.Label())
		}
		u.diags = append(u.diags, at(u.File, rg,
			diag.New(diag.LibraryImportCycle,
				strings.Join(names, " -> "))))
	}
}

func pathOf(u *Unit) string {
	if u.File == nil {
		return ""
	}
	return u.File.Path
}

// unitName is the name of a unit as messages show it.
func unitName(u *Unit) string {
	if u.Decl != nil {
		return u.Decl.Text
	}
	return string(u.Name)
}

// cyclePath returns a cycle through the strongly connected component
// comp, starting and ending at comp[0], following each unit's first
// dependency (in canonical order) that leads back.
func cyclePath(comp []*Unit) []*Unit {
	start := comp[0]
	path := []*Unit{start}
	seen := map[*Unit]bool{start: true}
	var walk func(u *Unit) bool
	walk = func(u *Unit) bool {
		for _, d := range u.Deps {
			if !slices.Contains(comp, d) {
				continue
			}
			if d == start {
				path = append(path, d)
				return true
			}
			if seen[d] {
				continue
			}
			seen[d] = true
			path = append(path, d)
			if walk(d) {
				return true
			}
			path = path[:len(path)-1]
		}
		return false
	}
	walk(start)
	return path
}

// lateRef is an architecture name resolved once every unit is
// analyzed: architectures are not dependencies (a design may
// instantiate itself recursively), so the reference waits.
type lateRef struct {
	tok    cst.Token
	entity *Decl
}

// lateArch defers the reference from tok to an architecture of entity.
func (a *analyzer) lateArch(tok cst.Token, entity *Decl) {
	if tok.IsZero() || tok.Missing() || entity == nil {
		return
	}
	a.u.late = append(a.u.late, lateRef{tok: tok, entity: entity})
}

// resolveLate resolves the deferred architecture references of every
// unit, in canonical order, after all units are analyzed.
func (s *session) resolveLate() {
	for _, u := range s.units {
		if len(u.late) == 0 {
			continue
		}
		for _, lr := range u.late {
			eu := lr.entity.Unit
			if eu == nil {
				continue
			}
			l, ok := s.libs[eu.Library]
			if !ok && s.base != nil {
				l, ok = s.base.libs[eu.Library]
			}
			if !ok {
				continue
			}
			k := TokenKey(lr.tok)
			var found *Unit
			for _, sec := range l.Secondary(eu.Name) {
				if sec.Kind == UnitArchitecture && sec.Name == k &&
					sec.Decl != nil {
					found = sec
					break
				}
			}
			if found == nil {
				u.diags = append(u.diags, at(u.File, lr.tok.Span(),
					diag.New(diag.SemaError, "entity "+
						lr.entity.Text+" has no architecture "+
						lr.tok.Text())))
				continue
			}
			u.Refs = append(u.Refs, Ref{File: u.File,
				Span: lr.tok.Span(), Decl: found.Decl, Kind: RefRead})
		}
		u.Refs = sortRefs(u.Refs)
	}
}
