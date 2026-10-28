package sema

import (
	"cmp"
	"path"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/token"
)

// Work is the library of unmapped files (#34) and the name every unit
// uses for its own library (LRM 13.2).
const Work Key = "work"

// LibraryOf returns the library a project file is analyzed into: the
// gohard.yaml library whose folder contains the file or whose file
// entry is the file (the longest entry wins, so a file entry beats a
// folder holding it; equal entries go to the first library by name),
// else work. p is a project-relative slash path, folders are relative to
// the project root (#28).
func LibraryOf(c config.Config, p string) Key {
	p = path.Clean(p)
	best, bestLen := Work, -1
	for _, l := range c.Libraries {
		for _, f := range l.Folders {
			dir := path.Clean(strings.ReplaceAll(f.Path, `\`, "/"))
			if !inFolder(p, dir) {
				continue
			}
			if n := len(dir); n > bestLen {
				best, bestLen = IdentKey(l.Name), n
			}
		}
	}
	return best
}

func inFolder(p, dir string) bool {
	if dir == "." {
		return !strings.HasPrefix(p, "../")
	}
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// newLibrary returns an empty library named k.
func newLibrary(k Key, bundled bool) *Library {
	l := &Library{Name: k, Bundled: bundled,
		units: map[Key]*Unit{}, secondary: map[Key][]*Unit{}}
	l.Decl = &Decl{Kind: DeclLibrary, Name: k, Text: string(k),
		Implicit: true}
	return l
}

// duplicate records a unit that repeats an earlier unit in its library.
type duplicate struct{ first, second *Unit }

// buildLibraries indexes every unit by library. The bundled libraries
// come from the bundle (shared, never modified); a project library that
// reuses a bundled name gets its own index holding both.
func (s *session) buildLibraries() {
	s.libs = map[Key]*Library{}
	get := func(k Key) *Library {
		if l, ok := s.libs[k]; ok {
			return l
		}
		var l *Library
		if bl, ok := s.base.Library(k); ok {
			l = &Library{Name: k, Decl: bl.Decl, Bundled: true,
				units: map[Key]*Unit{}, secondary: map[Key][]*Unit{}}
			for _, u := range bl.order {
				l.insert(u)
			}
		} else {
			l = newLibrary(k, s.base == nil && isBundledName(k))
		}
		s.libs[k] = l
		return l
	}
	get(Work)
	for _, cl := range s.cfg.Libraries {
		l := get(IdentKey(cl.Name))
		for _, f := range cl.Folders {
			l.Folders = append(l.Folders, f.Path)
		}
	}
	for _, u := range s.units {
		l := get(u.Library)
		first := l.insert(u)
		switch {
		case first == nil:
		case first.File != nil && first.File == u.File &&
			!u.File.Bundled:
			// Within one design file the units are analyzed in
			// textual order, and analyzing a unit replaces the library
			// unit of the same name (LRM 13.1, 13.5): the later one is
			// the visible unit, the earlier one stays analyzed but
			// hidden. Only two files declaring one unit is an error
			// (#34), as files have no analysis order.
			l.replace(first, u)
			first.Duplicate = true
		default:
			u.Duplicate = true
			s.dups = append(s.dups, duplicate{first, u})
		}
	}
	if s.base != nil {
		for _, bl := range s.base.Libraries {
			if _, ok := s.libs[bl.Name]; !ok {
				s.libs[bl.Name] = bl // shared, read-only
			}
		}
	}
	for _, k := range sortedKeys(s.libs) {
		s.libOrder = append(s.libOrder, s.libs[k])
	}
}

func isBundledName(k Key) bool { return k == "std" || k == "ieee" }

// insert adds u to the library and returns the earlier unit it
// duplicates, if any.
func (l *Library) insert(u *Unit) *Unit {
	l.order = append(l.order, u)
	if u.Kind.Primary() {
		if old, ok := l.units[u.Name]; ok {
			return old
		}
		l.units[u.Name] = u
		return nil
	}
	for _, old := range l.secondary[u.Of] {
		if old.Kind == u.Kind && (u.Kind == UnitPackageBody ||
			old.Name == u.Name) {
			return old
		}
	}
	l.secondary[u.Of] = append(l.secondary[u.Of], u)
	return nil
}

// replace makes u, a unit insert found to duplicate old, the visible
// unit in old's place.
func (l *Library) replace(old, u *Unit) {
	if u.Kind.Primary() {
		l.units[u.Name] = u
		if l.replaced == nil {
			l.replaced = map[*Unit]*Unit{}
		}
		l.replaced[u] = old
		return
	}
	for i, x := range l.secondary[u.Of] {
		if x == old {
			l.secondary[u.Of][i] = u
		}
	}
}

// library returns the library named k as seen from unit from (work is
// from's own library).
func (s *session) library(from *Unit, k Key) (*Library, bool) {
	if k == Work && from != nil {
		k = from.Library
	}
	l, ok := s.libs[k]
	return l, ok
}

// linkDeps turns every unit's syntactic references into Deps.
func (s *session) linkDeps() {
	s.vunitBinders = s.findVunitBinders() // vunitbind.go
	for _, u := range s.units {
		var deps []*Unit
		add := func(d *Unit) {
			if d != nil && d != u && !slices.Contains(deps, d) {
				deps = append(deps, d)
			}
		}
		for _, r := range append(slices.Clone(u.uses),
			s.inheritedRefs(u)...) { // units.go
			if r.node.Parent().Kind() == cst.KindVerificationUnitBinding {
				continue // use vunit lib.v: linkVunitDeps
			}
			l, ok := s.library(u, r.lib)
			if !ok {
				continue
			}
			if d, ok := l.UnitFrom(r.unit, u); ok {
				add(d)
			}
		}
		if s.base == nil && !(u.Library == "std" &&
			u.Name == "standard") {
			// In the bundle every unit sees std.standard implicitly.
			if l, ok := s.libs["std"]; ok {
				if d, ok := l.Unit("standard"); ok {
					add(d)
				}
			}
		}
		if !u.Kind.Primary() || u.Kind == UnitConfiguration {
			if l, ok := s.library(u, Work); ok {
				if d, ok := l.UnitFrom(u.Of, u); ok {
					add(d)
				}
			}
		}
		// A verification unit sees its bound unit's names (psl.go).
		e, arch := s.boundUnits(u)
		add(e)
		add(arch)
		slices.SortFunc(deps, func(a, b *Unit) int {
			return cmp.Compare(a.Index, b.Index)
		})
		u.Deps = deps
	}
	s.linkVunitDeps()
	s.linkAllDeps()
}

// linkVunitDeps makes a unit holding verification unit binding
// indications depend on the units they name (vunitbind.go), unless the
// named unit depends on it: an architecture whose configuration
// specification binds a unit to the architecture itself (a recursive
// design) would close an import cycle. Such a unit is analyzed after
// the indication, which then reads only its tree (vunitName). Units
// are linked in canonical order.
func (s *session) linkVunitDeps() {
	for _, u := range s.units {
		added := false
		for _, v := range s.namedVunits(u) {
			if v != u && !slices.Contains(u.Deps, v) && !reaches(v, u) {
				u.Deps = append(u.Deps, v)
				added = true
			}
		}
		if added {
			slices.SortFunc(u.Deps, func(a, b *Unit) int {
				return cmp.Compare(a.Index, b.Index)
			})
		}
	}
}

// linkAllDeps adds the dependencies a use clause L.all implies: each
// primary unit of L the unit names (by an identifier) is potentially
// visible there (LRM 12.4), so it is analyzed first, unless it depends
// on the unit itself: a name alone never makes an import cycle. Units
// are linked in canonical order, so the result does not depend on the
// order of the input files.
func (s *session) linkAllDeps() {
	for _, u := range s.units {
		libs := slices.Clone(u.allLibs)
		if !u.Kind.Primary() || u.Kind == UnitConfiguration {
			if l, ok := s.library(u, Work); ok {
				if p, ok := l.UnitFrom(u.Of, u); ok && p.File != nil &&
					!p.File.Bundled {
					libs = append(libs, p.allLibs...)
				}
			}
		}
		e, arch := s.boundUnits(u)
		for _, p := range []*Unit{e, arch} {
			if p != nil && p.File != nil && !p.File.Bundled {
				libs = append(libs, p.allLibs...)
			}
		}
		if len(libs) == 0 {
			continue
		}
		var cands []*Unit
		for _, k := range memoized(u, "identifiers", func() []Key {
			return identifiers(u.Node)
		}) {
			for _, lk := range libs {
				l, ok := s.libs[lk]
				if !ok {
					continue
				}
				d, ok := l.UnitFrom(k, u)
				if ok && d != u && d.File != nil && !d.File.Bundled &&
					!slices.Contains(u.Deps, d) &&
					!slices.Contains(cands, d) {
					cands = append(cands, d)
				}
			}
		}
		for _, d := range cands {
			if !reaches(d, u) {
				u.Deps = append(u.Deps, d)
			}
		}
		slices.SortFunc(u.Deps, func(a, b *Unit) int {
			return cmp.Compare(a.Index, b.Index)
		})
	}
}

// identifiers returns the keys of the identifiers in n, each once, in
// order of first appearance.
func identifiers(n cst.Node) []Key {
	var out []Key
	seen := map[Key]bool{}
	for t := range n.DescendantTokens() {
		if t.Kind() != token.Identifier {
			continue
		}
		if k := TokenKey(t); !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// reaches reports whether unit from depends on unit to, transitively.
func reaches(from, to *Unit) bool {
	seen := map[*Unit]bool{}
	stack := []*Unit{from}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if x == to {
			return true
		}
		if seen[x] {
			continue
		}
		seen[x] = true
		stack = append(stack, x.Deps...)
	}
	return false
}

// levelize assigns dependency levels with Tarjan's strongly connected
// components: a unit's level is one more than the highest level of its
// dependencies outside its own component. Units of one cycle share a
// level and cannot read each other; each cycle is recorded for
// library/import-cycle (libs.go).
func (s *session) levelize() {
	t := &tarjan{index: map[*Unit]int{}, low: map[*Unit]int{},
		on: map[*Unit]bool{}, s: s}
	for _, u := range s.units {
		if _, ok := t.index[u]; !ok {
			t.visit(u)
		}
	}
}

type tarjan struct {
	s     *session
	next  int
	index map[*Unit]int
	low   map[*Unit]int
	on    map[*Unit]bool
	stack []*Unit
}

func (t *tarjan) local(d *Unit) bool {
	return d.File != nil && !(d.File.Bundled && t.s.base != nil)
}

func (t *tarjan) visit(u *Unit) {
	t.index[u], t.low[u] = t.next, t.next
	t.next++
	t.stack = append(t.stack, u)
	t.on[u] = true
	for _, d := range u.Deps {
		if !t.local(d) {
			continue
		}
		if _, ok := t.index[d]; !ok {
			t.visit(d)
			t.low[u] = min(t.low[u], t.low[d])
		} else if t.on[d] {
			t.low[u] = min(t.low[u], t.index[d])
		}
	}
	if t.low[u] != t.index[u] {
		return
	}
	var comp []*Unit
	for {
		n := len(t.stack) - 1
		w := t.stack[n]
		t.stack = t.stack[:n]
		t.on[w] = false
		comp = append(comp, w)
		if w == u {
			break
		}
	}
	level := 0
	for _, w := range comp {
		for _, d := range w.Deps {
			if t.local(d) && !slices.Contains(comp, d) {
				level = max(level, d.Level+1)
			}
		}
	}
	for _, w := range comp {
		w.Level = level
	}
	selfLoop := len(comp) == 1 && slices.Contains(comp[0].Deps, comp[0])
	if len(comp) > 1 || selfLoop {
		slices.SortFunc(comp, func(a, b *Unit) int {
			return cmp.Compare(a.Index, b.Index)
		})
		t.s.cycles = append(t.s.cycles, comp)
	}
}
