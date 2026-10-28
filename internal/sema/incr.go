package sema

// incr.go reuses an earlier result for what an edit cannot change
// (#53). The editor analyzes the whole project on every keystroke; most
// units are untouched by it, and restoring them from the on-disk cache
// (#37) on every run costs far more than analyzing the edited unit.
//
// Decision (P3): the File, Unit and Library objects of a result are
// immutable once its analysis returns, so a later analysis may share
// them read-only: Input.Prev names the result to reuse, and the
// session that reuses it writes nothing it did not make. Every result
// is still a complete, immutable value of its own snapshot (the server
// never publishes a result for an older snapshot), and it is exactly
// the result of a full analysis of that snapshot: TestIncrementalEdits
// replays random edit sequences on the corpus, NEORV32 and the design
// of TestIncrementalRules, in which each rule of step 3 decides the
// result, and compares the two after every edit.
//
// A run that reuses prev:
//
//  1. Takes prev's File object for every file whose path and text are
//     unchanged, and scans only the others. The set of paths, the
//     configuration, the tool version and the step limit must be
//     prev's, and neither analysis may have an opaque unit.
//  2. Builds the unit graph (libraries, Deps, levels, duplicates,
//     cycles) on fresh shells of every unit and compares it with
//     prev's (graphSig): any difference, such as a unit added or
//     renamed or a context clause changed, falls back to a full
//     analysis.
//  3. Marks dirty the units of the edited files, then, up to a fixed
//     point, every unit that depends on a dirty one: one of its Deps
//     (the closure the #37 cache key rests on), a unit its job read
//     (readable: in every known input such a unit is among Deps
//     already, so this is defence in depth, as the cache's recorded
//     reads are), an architecture its late references looked up, a
//     unit of the same file (a file's units share its File object), or
//     a library rebuilt for holding a dirty unit when the unit's use
//     clauses name the library (use L.all keeps the Library).
//  4. Reuses prev's objects for the clean units and libraries, and
//     makes fresh ones for the rest; a file with a dirty unit gets a
//     fresh File (the same text and tree), so the PSL expressions its
//     units' jobs write (File.PSL, #110) never reach a shared File:
//     every unit of that file is dirty. The dirty units are
//     restored from the on-disk cache or analyzed, level by level, and
//     never stored: entries are written by full analyses only.
//  5. Runs the checks across units on every unit, as a full analysis
//     does: they write only per-session tables (post), never a unit.
//     Static checks run per unit with the unit's own evaluator, so
//     their result is a function of the unit and of what they read;
//     a clean unit whose static reads (package bodies, deferred
//     constants) are clean keeps prev's.
//
// A body-only edit (an architecture or a package body) thus re-checks
// the edited file's units and the few units that point into them; a
// signature edit re-checks every unit that depends on the edited one.

import (
	"bytes"
	"slices"

	"github.com/connerohnesorge/gohard/v2/internal/diag"
)

// incState is what a result keeps for a later analysis to reuse it.
// Per-unit slices are indexed by the unit's position in Result.Units,
// per-file ones by the file's position in Result.Files.
type incState struct {
	// key is the configuration identity, tool version and step limit:
	// a result is reused only under the same.
	key string
	// graph is graphSig of the session.
	graph [32]byte
	// opaque is set when some unit is opaque, unchecked or modeled:
	// such a result is never reused (unsupported.go sets flags on
	// units after their jobs).
	opaque    bool
	fileDiags [][]diag.Diagnostic
	// reads are the project units each unit's job read (readable, and
	// for a unit restored from the on-disk cache the entry's recorded
	// reads); lates the architectures its late references looked up.
	reads [][]int
	lates [][]int
	stat  []unitStatic
	// keys, objs and extra are the on-disk cache state of each unit
	// (cache.go); nil when the session had no cache.
	keys  [][32]byte
	objs  [][]any
	extra [][]int
}

// unitStatic is the result of a unit's static checks: its diagnostics,
// its incomplete entries and the project units the evaluation read
// outside the unit's own objects (package bodies).
type unitStatic struct {
	diags      []diag.Diagnostic
	incomplete []string
	reads      []int
}

// incKey is the part of an incState key every result depends on.
func (s *session) incKey() string {
	w := newKey()
	w.bytes(s.cfg.Identity())
	w.str(s.toolVersion)
	w.int(s.steps)
	k := w.sum()
	return string(k[:])
}

// incState returns the session's state for reuse.
func (s *session) incState() *incState {
	st := &incState{key: s.incKey(), graph: s.graphSig(),
		fileDiags: s.fileDiags, lates: s.lates, stat: s.stat,
		reads: make([][]int, len(s.units))}
	for i, u := range s.units {
		if u.Opaque || u.Unchecked || u.Modeled {
			st.opaque = true
		}
		rs := s.reads[i]
		if s.fresh[i] && s.uc != nil {
			rs = append(slices.Clone(rs), s.uc.extra[i]...)
		}
		if s.fresh[i] {
			slices.Sort(rs)
			rs = slices.Compact(rs)
		}
		st.reads[i] = rs
	}
	if s.uc != nil {
		st.keys, st.objs, st.extra = s.uc.keys, s.uc.objs, s.uc.extra
	}
	return st
}

// analyzeIncremental analyzes srcs reusing prev. ok is false when prev
// cannot be reused; the caller then runs a full analysis. A stopped
// analysis returns nil, true.
func analyzeIncremental(prev *Result, srcs []Source,
	newSession func([]*File) *session,
) (*Result, bool) {
	pi := prev.inc
	if pi == nil || pi.opaque || len(srcs) != len(prev.Files) {
		return nil, false
	}
	s := newSession(nil)
	if s.base != prev.Bundle || s.incKey() != pi.key {
		return nil, false
	}
	// 1. Files: prev's where the text is unchanged.
	changed := make([]bool, len(srcs))
	s.files = make([]*File, len(srcs))
	in := Input{Config: s.cfg, Cache: s.cache}
	for i, src := range srcs {
		pf := prev.Files[i]
		if src.Path != pf.Path {
			return nil, false
		}
		if src.Parse == pf.Parse && src.Parse != nil ||
			bytes.Equal(src.Text, pf.Text) {
			s.files[i] = pf
			continue
		}
		changed[i] = true
		s.files[i] = newFile(in, src, s.popts)
	}
	if s.stopped() {
		return nil, true
	}
	s.phase("units")
	// 2. The unit graph, on shells.
	s.fileDiags = make([][]diag.Diagnostic, len(s.files))
	var fileOf []int
	for i, f := range s.files {
		var us []*Unit
		if changed[i] {
			us = scanFile(f)
			f.Units = us
			s.fileDiags[i] = fileDiagnostics(f, s.std)
		} else {
			for _, u := range f.Units {
				us = append(us, shell(u, f))
			}
			s.fileDiags[i] = pi.fileDiags[i]
		}
		s.units = append(s.units, us...)
		for range us {
			fileOf = append(fileOf, i)
		}
	}
	if len(s.units) != len(prev.Units) {
		return nil, false
	}
	s.indexUnits()
	s.buildLibraries()
	s.applyStandIns()
	s.linkDeps()
	s.levelize()
	if s.graphSig() != pi.graph {
		return nil, false
	}
	// 3. The dirty units.
	dirty := s.dirtyUnits(prev, changed, fileOf)
	// 4. Objects: prev's for clean units, the shells for dirty ones.
	shells := s.units
	final := make([]*Unit, len(shells))
	s.initUnits(false)
	for i := range shells {
		if dirty[i] {
			final[i] = shells[i]
			s.fresh[i] = true
		} else {
			final[i] = prev.Units[i]
		}
	}
	byFile := make([][]*Unit, len(s.files))
	fileDirty := make([]bool, len(s.files))
	for k, fi := range fileOf {
		byFile[fi] = append(byFile[fi], shells[k])
		fileDirty[fi] = fileDirty[fi] || dirty[k]
	}
	for i, f := range s.files {
		if changed[i] || !fileDirty[i] {
			continue // a new File already, or prev's File and units
		}
		nf := &File{Path: f.Path, Bundled: f.Bundled, Library: f.Library,
			Text: f.Text, Parse: f.Parse, Lines: f.Lines,
			Units: byFile[i]}
		for _, u := range nf.Units {
			u.File = nf
		}
		s.files[i] = nf
	}
	remap := func(u *Unit) *Unit {
		if i := s.pos(u); i >= 0 {
			return final[i]
		}
		return u
	}
	for i, u := range shells {
		if dirty[i] {
			for k, d := range u.Deps {
				u.Deps[k] = remap(d)
			}
		}
	}
	for k, d := range s.dups {
		s.dups[k] = duplicate{remap(d.first), remap(d.second)}
	}
	for _, comp := range s.cycles {
		for k, u := range comp {
			comp[k] = remap(u)
		}
	}
	s.reuseLibraries(prev, dirty, remap)
	s.units = final
	for i := range final {
		if !dirty[i] {
			s.reads[i], s.lates[i] = pi.reads[i], pi.lates[i]
		}
	}
	s.prev = pi
	if s.cache != nil && pi.keys != nil {
		s.uc = newUnitCache(s.cache, s, s.popts)
		s.uc.noStore = true
		for i := range final {
			if !dirty[i] {
				s.uc.keys[i], s.uc.objs[i] = pi.keys[i], pi.objs[i]
				s.uc.extra[i] = pi.extra[i]
			}
		}
	}
	// 5. The jobs of the dirty units, then the checks across units.
	if !s.analyzeUnits() {
		return nil, true
	}
	return s.finish()
}

// shell returns a fresh unit with u's header (what scanFile sets) in
// file f.
func shell(u *Unit, f *File) *Unit {
	return &Unit{Kind: u.Kind, Library: u.Library, Name: u.Name, Of: u.Of,
		File: f, Node: u.Node, Lib: u.Lib, uses: u.uses,
		allLibs: u.allLibs}
}

// dirtyUnits returns the units whose objects cannot be reused: those of
// changed files and, up to a fixed point, every unit that depends on a
// dirty one (step 3 above).
func (s *session) dirtyUnits(prev *Result, changed []bool, fileOf []int,
) []bool {
	n := len(s.units)
	pi := prev.inc
	dirty := make([]bool, n)
	for i := range dirty {
		dirty[i] = changed[fileOf[i]]
	}
	// libUnits are the project units of each library.
	libUnits := map[Key][]int{}
	for _, l := range s.libOrder {
		for _, u := range l.order {
			if j := s.pos(u); j >= 0 {
				libUnits[l.Name] = append(libUnits[l.Name], j)
			}
		}
	}
	anyDirty := func(js []int) bool {
		for _, j := range js {
			if dirty[j] {
				return true
			}
		}
		return false
	}
	for more := true; more; {
		more = false
		dirtyLib := map[Key]bool{}
		for _, k := range sortedKeys(libUnits) {
			dirtyLib[k] = anyDirty(libUnits[k])
		}
		dirtyFile := make([]bool, len(s.files))
		for i, d := range dirty {
			if d {
				dirtyFile[fileOf[i]] = true
			}
		}
		for i, u := range s.units {
			if dirty[i] {
				continue
			}
			d := dirtyFile[fileOf[i]] || anyDirty(pi.reads[i]) ||
				anyDirty(pi.lates[i])
			for _, x := range u.Deps {
				if j := s.pos(x); j >= 0 && dirty[j] {
					d = true
				}
			}
			for _, k := range u.allLibs {
				if dirtyLib[k] {
					d = true
				}
			}
			if d {
				dirty[i], more = true, true
			}
		}
	}
	return dirty
}

// reuseLibraries replaces the libraries built on the shells: prev's
// object when none of its units is dirty, else a fresh one holding the
// final units (remap). Every library keeps prev's Decl, which clean
// units' references point at.
func (s *session) reuseLibraries(prev *Result, dirty []bool,
	remap func(*Unit) *Unit,
) {
	for i, l := range s.libOrder {
		if s.base != nil && s.base.libs[l.Name] == l {
			continue // the bundle's, shared
		}
		pl := prev.libs[l.Name]
		clean := true
		for _, u := range l.order {
			if j := s.pos(u); j >= 0 && dirty[j] {
				clean = false
			}
		}
		var nl *Library
		if clean {
			nl = pl
		} else {
			nl = &Library{Name: l.Name, Decl: pl.Decl, Bundled: l.Bundled,
				Folders: l.Folders, units: map[Key]*Unit{},
				secondary: map[Key][]*Unit{}}
			for _, k := range sortedKeys(l.units) {
				nl.units[k] = remap(l.units[k])
			}
			for _, k := range sortedKeys(l.secondary) {
				for _, u := range l.secondary[k] {
					nl.secondary[k] = append(nl.secondary[k], remap(u))
				}
			}
			for _, u := range l.order {
				nl.order = append(nl.order, remap(u))
			}
			if l.replaced != nil {
				nl.replaced = map[*Unit]*Unit{}
				for _, u := range l.order {
					if old, ok := l.replaced[u]; ok {
						nl.replaced[remap(u)] = remap(old)
					}
				}
			}
		}
		s.libOrder[i] = nl
		s.libs[l.Name] = nl
	}
}

// graphSig is the unit graph: everything the session knows before the
// unit jobs run that a job or a check across units can observe, with
// units named by Index. Two sessions with the same graphSig differ at
// most in the text of their files.
func (s *session) graphSig() [32]byte {
	w := newKey()
	w.str("graph")
	fileIdx := make(map[*File]int, len(s.files))
	w.int(int64(len(s.files)))
	for i, f := range s.files {
		fileIdx[f] = i
		w.str(f.Path)
		w.str(string(f.Library))
		w.int(int64(len(f.Units)))
	}
	unit := func(u *Unit) {
		if u == nil {
			w.int(-1)
			return
		}
		w.int(int64(u.Index))
	}
	w.int(int64(len(s.units)))
	for _, u := range s.units {
		w.int(int64(fileIdx[u.File]))
		w.int(int64(u.Kind))
		w.str(string(u.Library))
		w.str(string(u.Name))
		w.str(string(u.Of))
		w.int(int64(u.Index))
		w.int(int64(u.Level))
		w.bool(u.Duplicate)
		w.int(int64(len(u.Deps)))
		for _, d := range u.Deps {
			unit(d)
		}
		w.int(int64(len(u.allLibs)))
		for _, k := range u.allLibs {
			w.str(string(k))
		}
	}
	w.int(int64(len(s.libOrder)))
	for _, l := range s.libOrder {
		w.str(string(l.Name))
		w.bool(l.Bundled)
		w.bool(s.base != nil && s.base.libs[l.Name] == l)
		w.bool(s.base != nil && l.Decl != nil && s.base.libs[l.Name] !=
			nil && s.base.libs[l.Name].Decl == l.Decl)
		w.int(int64(len(l.Folders)))
		for _, f := range l.Folders {
			w.str(f)
		}
		for _, k := range sortedKeys(l.units) {
			w.str(string(k))
			unit(l.units[k])
		}
		w.int(-3)
		for _, k := range sortedKeys(l.secondary) {
			w.str(string(k))
			for _, u := range l.secondary[k] {
				unit(u)
			}
		}
		w.int(-3)
		for _, u := range l.order {
			unit(u)
		}
		w.int(-3)
		for _, u := range l.order {
			if old, ok := l.replaced[u]; ok {
				unit(u)
				unit(old)
			}
		}
		w.int(-3)
	}
	w.int(int64(len(s.dups)))
	for _, d := range s.dups {
		unit(d.first)
		unit(d.second)
	}
	w.int(int64(len(s.cycles)))
	for _, c := range s.cycles {
		w.int(int64(len(c)))
		for _, u := range c {
			unit(u)
		}
	}
	return w.sum()
}

// treeKey keys a fact of a unit's tree in its file's parse memo.
type treeKey struct {
	what string
	off  int
}

// memoized returns f(), a pure function of u's tree, memoized on the
// parse result of u's file, which the editor reuses for an unchanged
// file on every keystroke. The value is shared: callers never modify
// it.
func memoized[T any](u *Unit, what string, f func() T) T {
	if u.File == nil || u.File.Parse == nil {
		return f()
	}
	_, _, off, _ := u.Node.Raw()
	k := treeKey{what, off}
	m := u.File.Parse.Memo()
	if v, ok := m.Load(k); ok {
		return v.(T)
	}
	v := f()
	m.Store(k, v)
	return v
}
