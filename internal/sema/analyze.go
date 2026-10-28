package sema

import (
	"bytes"
	"cmp"
	"slices"
	"sync"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/parser"
	"github.com/connerohnesorge/gohard/internal/span"
	"github.com/connerohnesorge/gohard/internal/token"
)

// Source is one project file to analyze.
type Source struct {
	// Path is the project-relative slash path (as workspace reports
	// it). Paths are unique within an Input.
	Path string
	Text []byte
	// Parse optionally carries the file already parsed with the
	// Input's parse options (the editor keeps trees per document); nil
	// means Analyze parses Text.
	Parse *parser.Result
}

// Input is everything an analysis depends on: the files, the loaded
// configuration (standard, libraries, allow_opaque, psl.*, defines) and
// the tool version seen by conditional analysis. The job runner only
// changes how fast the result comes, never the result.
type Input struct {
	Files       []Source
	Config      config.Config
	ToolVersion string
	// Jobs runs the pure per-file and per-unit jobs; nil means
	// env.Sequential. Limit is -p N (<= 0: the runner's default).
	Jobs  env.Jobs
	Limit int
	// Steps is the static-evaluation budget per evaluation; 0 means
	// eval.EditorSteps (the editor and `gohard check`, #34).
	Steps int64
	// Stop, when set, is polled after parsing and between dependency
	// levels; once it returns true Analyze stops and returns nil (the
	// language server cancels stale analyses this way, P7). A result
	// is never partial.
	Stop func() bool
	// Probe, when set, observes the phases of the analysis (the #19
	// latency benchmark). It never changes the result.
	Probe *Probe
	// Cache, when set, is the on-disk analysis cache (#37): parse
	// results and analyzed units are restored from it and stored in
	// it. It never changes the result (cache.go).
	Cache Cache
	// Prev, when set, is an earlier result whose files, units and
	// libraries Analyze may reuse for what the edit cannot change
	// (incr.go, #53). It is read, never modified, and it never
	// changes the result: Analyze returns what it returns without it.
	Prev *Result
}

// Probe observes an analysis without changing it: Phase is called as
// each phase starts ("units": names, types and overloads of every
// unit; "cross-file": the checks across units and files; "static":
// static evaluation; "done": the result is built), and Overload as
// each outermost overload resolution starts, returning the function
// called when it ends. Overload is called from the job runner's
// workers, so it must be safe for concurrent use. Either may be nil.
type Probe struct {
	Phase    func(name string)
	Overload func() (end func())
}

func (s *session) phase(name string) {
	if s.probe != nil && s.probe.Phase != nil {
		s.probe.Phase(name)
	}
}

// Result is the analyzed workspace: every file, unit, declaration and
// name occurrence, and the diagnostics. It is immutable and a pure
// function of the Input (P1).
type Result struct {
	Standard token.Standard
	// Files are the project files sorted by Path.
	Files []*File
	// Units are the project's design units in canonical order.
	Units []*Unit
	// Libraries are every library (bundled and mapped), sorted by
	// name.
	Libraries []*Library
	// Diagnostics are sorted (diag.Normalize).
	Diagnostics []diag.Diagnostic
	// Incomplete lists what static evaluation could not finish within
	// the step limit, sorted (#20 JSON "incomplete"): unit-level values
	// here; per-instance values come from internal/elab/hier.
	Incomplete []string

	// Bundle is the analyzed std/ieee bundle the result was built on
	// (nil for the bundle itself).
	Bundle *Result

	byPath map[string]*File
	libs   map[Key]*Library
	refsOf map[*Decl][]Ref // lookup only; the bundle's
	// refs indexes a project result's references on first use.
	refs *refIndex `codec:"-"`
	// inc is what a later analysis needs to reuse this one (incr.go);
	// nil for the bundle.
	inc *incState `codec:"-"`
}

// refIndex is the reference index of a project result, built on first
// use: References is a query, and building it on every keystroke
// would cost as much as the rest of an incremental analysis.
type refIndex struct {
	once sync.Once
	m    map[*Decl][]Ref
}

// File returns the project or bundled file at path.
func (r *Result) File(path string) (*File, bool) {
	if r == nil {
		return nil, false
	}
	if f, ok := r.byPath[path]; ok {
		return f, true
	}
	return r.Bundle.File(path)
}

// Library returns the library named k (bundled or mapped).
func (r *Result) Library(k Key) (*Library, bool) {
	if r == nil {
		return nil, false
	}
	l, ok := r.libs[k]
	return l, ok
}

// RefAt returns the name occurrence at byte offset off of the file at
// path.
func (r *Result) RefAt(path string, off int) (Ref, bool) {
	f, ok := r.File(path)
	if !ok {
		return Ref{}, false
	}
	for _, u := range f.Units {
		if ref, ok := refAt(u.Refs, off); ok {
			return ref, true
		}
	}
	return Ref{}, false
}

// DeclAt returns the declaration whose defining name is at off in the
// file at path.
func (r *Result) DeclAt(path string, off int) (*Decl, bool) {
	f, ok := r.File(path)
	if !ok {
		return nil, false
	}
	for _, u := range f.Units {
		for _, d := range u.Decls {
			if !d.Implicit && d.File == f && d.Span.Start <= off &&
				off <= d.Span.End && d.Span.Len() > 0 {
				return d, true
			}
		}
	}
	return nil, false
}

// References returns every occurrence of d in the project files,
// sorted by path and position. References from inside the bundle are
// not listed.
func (r *Result) References(d *Decl) []Ref {
	if r == nil || d == nil {
		return nil
	}
	if r.refs == nil {
		return r.refsOf[d]
	}
	r.refs.once.Do(func() { r.refs.m = indexRefs(r.Units) })
	return r.refs.m[d]
}

// indexRefs maps every declaration to its occurrences in us, in unit
// order.
func indexRefs(us []*Unit) map[*Decl][]Ref {
	m := map[*Decl][]Ref{}
	for _, u := range us {
		for _, ref := range u.Refs {
			if ref.Decl != nil {
				m[ref.Decl] = append(m[ref.Decl], ref)
			}
		}
	}
	return m
}

// Analyze analyzes the snapshot in. It never panics on any tree the
// parser produces, and its result does not depend on the order of
// in.Files, map iteration order, the job runner or in.Prev (#34, #53).
func Analyze(in Input) *Result {
	jobs := in.Jobs
	if jobs == nil {
		jobs = env.Sequential{}
	}
	std, ok := token.ParseStandard(in.Config.Standard)
	if !ok {
		std = token.Std2008
	}
	srcs := canonicalSources(in.Files)
	popts := parser.FromConfig(in.Config, in.ToolVersion)
	newSession := func(files []*File) *session {
		return &session{
			std: std, cfg: in.Config, jobs: jobs, limit: in.Limit,
			files: files, base: Bundle(), steps: cmp.Or(in.Steps,
				int64(eval.EditorSteps)), stop: in.Stop,
			probe: in.Probe, cache: in.Cache, popts: popts,
			toolVersion: in.ToolVersion,
		}
	}
	if in.Prev != nil {
		if r, ok := analyzeIncremental(in.Prev, srcs, newSession); ok {
			return r
		}
	}
	files := env.Map(jobs, in.Limit, srcs, func(s Source) *File {
		return newFile(in, s, popts)
	})
	s := newSession(files)
	if s.stopped() {
		return nil
	}
	return s.run()
}

// newFile returns the File of source src, parsing it unless it comes
// parsed.
func newFile(in Input, src Source, popts parser.Options) *File {
	p := src.Parse
	switch {
	case p != nil:
	case in.Cache != nil:
		p = parseCached(in.Cache, src.Path, src.Text, popts)
	default:
		p = parser.Parse(src.Text, popts)
	}
	return &File{Path: src.Path, Text: src.Text, Parse: p,
		Lines:   span.NewIndex(src.Text),
		Library: LibraryOf(in.Config, src.Path)}
}

// stopped reports whether the caller asked to stop.
func (s *session) stopped() bool { return s.stop != nil && s.stop() }

// canonicalSources sorts the sources by path; for a repeated path the
// one with the smallest text is kept, so even a malformed input gives a
// result independent of order.
func canonicalSources(in []Source) []Source {
	srcs := slices.Clone(in)
	slices.SortStableFunc(srcs, func(a, b Source) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path),
			bytes.Compare(a.Text, b.Text))
	})
	return slices.CompactFunc(srcs, func(a, b Source) bool {
		return a.Path == b.Path
	})
}

// session is one analysis run: the files, the unit index and the
// dependency levels. Unit jobs of one level run in parallel and only
// read units of lower levels (and the bundle), which are finished.
type session struct {
	std   token.Standard
	cfg   config.Config
	jobs  env.Jobs
	limit int
	steps int64
	files []*File
	units []*Unit
	base  *Result // the bundle; nil while analyzing the bundle

	libs     map[Key]*Library
	libOrder []*Library
	diags    []diag.Diagnostic
	dups     []duplicate
	cycles   [][]*Unit
	// tags are the bundled first-standard tags (bundle session only).
	tags  map[tagKey]token.Standard
	stop  func() bool
	probe *Probe
	// cache and uc are the on-disk analysis cache and its semantic
	// state (cache.go); popts are the parse options of the files.
	cache Cache
	uc    *unitCache
	popts parser.Options
	// pslMu guards File.PSL, which the jobs of one level write and
	// read together when a file has several units with PSL code
	// (vunits): pslExpr writes, pslTyped reads. Each job writes its
	// own spans, so the result is the same in any order.
	pslMu sync.Mutex
	// toolVersion is Input.ToolVersion (part of incState.key).
	toolVersion string
	// vunitBinders binds each unbound PSL verification unit that a
	// verification unit binding indication names (vunitbind.go). It is
	// set before the unit jobs run and only read by them.
	vunitBinders map[*Unit]vunitBinder

	// The per-unit state below is indexed by a unit's position in
	// units (incr.go). fresh marks the units whose objects this
	// session made: the others are reused from prev, an earlier
	// result, and are never written. post holds the diagnostics of
	// the checks across units, which are recomputed every run and so
	// never stored in a unit; stat holds the static checks' results.
	// reads and lates are what a unit's job and its late architecture
	// references looked at, for the next run's reuse.
	prev      *incState
	fresh     []bool
	post      [][]diag.Diagnostic
	stat      []unitStatic
	reads     [][]int
	lates     [][]int
	fileDiags [][]diag.Diagnostic
	// unchecked maps each unchecked unit to the opaque unit it reads
	// (unsupported.go).
	unchecked map[*Unit]*Unit
}

func (s *session) run() *Result {
	s.phase("units")
	for _, f := range s.files {
		f.Units = scanFile(f)
		s.units = append(s.units, f.Units...)
	}
	s.fileDiags = make([][]diag.Diagnostic, len(s.files))
	for i, f := range s.files {
		s.fileDiags[i] = fileDiagnostics(f, s.std)
	}
	s.indexUnits()
	s.buildLibraries()
	s.applyStandIns() // libs.go
	s.linkDeps()
	s.levelize()
	s.initUnits(true)
	if s.cache != nil && s.base != nil {
		s.uc = newUnitCache(s.cache, s, s.popts)
	}
	if !s.analyzeUnits() {
		return nil
	}
	r, _ := s.finish()
	return r
}

// fileDiagnostics are the diagnostics of a file of its own: syntax
// and the conditional-analysis directives.
func fileDiagnostics(f *File, std token.Standard) []diag.Diagnostic {
	return append(f.Parse.Diagnostics(f.Path),
		directiveRules(f, std)...) // lexrules.go
}

// indexUnits numbers the units in canonical order, after the bundle's.
func (s *session) indexUnits() {
	offset := 0
	if s.base != nil {
		offset = len(s.base.allUnits())
	}
	for i, u := range s.units {
		u.Index = offset + i
	}
}

// initUnits allocates the per-unit state of the session, every unit
// marked fresh or none.
func (s *session) initUnits(fresh bool) {
	n := len(s.units)
	s.fresh = make([]bool, n)
	for i := range s.fresh {
		s.fresh[i] = fresh
	}
	s.post = make([][]diag.Diagnostic, n)
	s.stat = make([]unitStatic, n)
	s.reads = make([][]int, n)
	s.lates = make([][]int, n)
}

// pos returns the position of project unit u in s.units, or -1 for a
// bundled unit of a project analysis.
func (s *session) pos(u *Unit) int {
	off := 0
	if s.base != nil {
		off = len(s.base.allUnits())
	}
	if i := u.Index - off; i >= 0 && i < len(s.units) && s.units[i] == u {
		return i
	}
	return -1
}

// note records diagnostic d of a check across units for unit u.
func (s *session) note(u *Unit, d diag.Diagnostic) {
	if i := s.pos(u); i >= 0 {
		s.post[i] = append(s.post[i], d)
	}
}

// finish runs the checks across units and builds the result; false
// means the analysis cannot reuse prev after all (opaqueUnits).
func (s *session) finish() (*Result, bool) {
	s.phase("cross-file")
	s.resolveLate()        // libs.go
	s.missingBodies()      // units.go
	s.configurationRules() // config.go
	s.calledBeforeBody()   // units.go
	s.libraryDiagnostics() // libs.go
	if !s.opaqueUnits() {  // unsupported.go
		return nil, false
	}
	s.phase("static")
	s.staticChecks() // static.go
	s.phase("done")
	return s.result(), true
}

// analyzeUnits runs the unit jobs level by level, of the units this
// session made; false means the caller stopped the analysis.
func (s *session) analyzeUnits() bool {
	byLevel := map[int][]*Unit{}
	maxLevel := 0
	for i, u := range s.units {
		if !s.fresh[i] {
			continue
		}
		byLevel[u.Level] = append(byLevel[u.Level], u)
		maxLevel = max(maxLevel, u.Level)
	}
	for l := 0; l <= maxLevel; l++ {
		if s.stopped() {
			return false
		}
		us := byLevel[l]
		if s.uc != nil {
			if len(us) > 0 {
				s.analyzeCached(us) // cache.go
			}
			continue
		}
		s.jobs.Run(s.limit, len(us), func(i int) {
			s.analyzeUnit(us[i])
		})
	}
	return true
}

// readable reports whether a unit analyzing at level from may read the
// unit to: bundled units always, project units of lower levels only.
func (s *session) readable(from, to *Unit) bool {
	if to == nil {
		return false
	}
	if to.File != nil && to.File.Bundled {
		return true
	}
	if from == nil {
		return true
	}
	if from.File != nil && from.File.Bundled {
		return to.File != nil && to.File.Bundled && to.Level < from.Level
	}
	if to.Level >= from.Level {
		return false
	}
	if s.uc != nil {
		s.uc.record(from, to)
	}
	if i, j := s.pos(from), s.pos(to); i >= 0 && j >= 0 &&
		s.reads != nil {
		s.reads[i] = append(s.reads[i], j) // only from's job writes it
	}
	return true
}

// allUnits returns the bundle's and the project's units in canonical
// order.
func (r *Result) allUnits() []*Unit {
	if r == nil {
		return nil
	}
	return append(r.Bundle.allUnits(), r.Units...)
}

func (s *session) result() *Result {
	r := &Result{
		Standard: s.std, Files: s.files, Units: s.units,
		Libraries: s.libOrder, Bundle: s.base, libs: s.libs,
		byPath: map[string]*File{},
	}
	for _, f := range s.files {
		r.byPath[f.Path] = f
	}
	ds := slices.Clone(s.diags)
	for _, fd := range s.fileDiags {
		ds = append(ds, fd...)
	}
	var incomplete []string
	for i, u := range s.units {
		ds = append(ds, s.unitDiagnostics(i, u)...)
		incomplete = append(incomplete, s.stat[i].incomplete...)
	}
	if s.base != nil {
		for _, d := range s.base.Diagnostics {
			ds = append(ds, bundledInternal(d))
		}
		r.refs = &refIndex{}
		r.inc = s.incState()
	} else {
		r.refsOf = indexRefs(s.units)
	}
	r.Diagnostics = diag.Normalize(ds)
	slices.Sort(incomplete)
	r.Incomplete = slices.Compact(incomplete)
	return r
}

// unitDiagnostics returns the diagnostics of the unit at position i:
// its job's, those of the checks across units (an opaque unit keeps
// only its opacity, an unchecked one only its syntax and library
// diagnostics, unsupported.go) and its static checks'.
func (s *session) unitDiagnostics(i int, u *Unit) []diag.Diagnostic {
	ds := slices.Concat(u.diags, s.post[i])
	if u.Opaque {
		ds = keepOpaque(ds)
	}
	if u.Unchecked {
		ds = dropSema(ds)
		if o := s.uncheckedBy(u); o != nil {
			ds = append(ds, at(u.File, unitRange(u),
				diag.New(diag.SemaUnchecked, u.Label(), o.Label())))
		}
	}
	return append(ds, s.stat[i].diags...)
}

// at locates d at the byte range rg of file f.
func at(f *File, rg cst.Range, d diag.Diagnostic) diag.Diagnostic {
	if f == nil {
		return d
	}
	l, c := f.Lines.LineCol(rg.Start)
	el, ec := f.Lines.LineCol(max(rg.End, rg.Start))
	return d.At(f.Path, l, c).To(el, ec)
}
