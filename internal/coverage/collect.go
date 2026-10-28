// Package coverage records the code coverage of a simulation run
// (#62): how often each statement of the design ran, which
// alternatives its decisions took, and how often each PSL cover
// directive was hit. It reads, writes and merges coverage as lcov
// tracefiles, the format genhtml and most coverage services read.
//
// The coverable statements are those of the design's units (Units):
// every unit of the elaborated hierarchy, the units they depend on and
// the bodies of those packages, except the bundled std and ieee and
// opaque units. Within them every sequential statement of a process
// or subprogram body, and every concurrent assignment, assertion and
// procedure call, is a statement; the if and case statements and the
// conditional and selected assignments (variable and signal,
// sequential and concurrent) are decisions with one branch per
// alternative (sema.Alternatives); every PSL cover directive is a
// cover. A Collector is the run's sema.StmtHook
// (elab.Design.SetStmtHook): it counts what the run executes, from the
// kernel's initialization on, so evaluations during elaboration
// (generics, constants) are not counted. PSL covers are not counted a
// second time: their hits are the cover-hit events of the run's trace,
// the #18 counters, each the hit of the directive whose process
// emitted it.
//
// In lcov terms a statement is a line (DA): the line's count is the
// largest count of the statements and covers that start on it. A
// decision is a block of branches (BRDA) on the line it starts on;
// the decisions of one line are numbered in source order, and a
// decision that never ran has "-" for every branch. Counting is a
// pure function of the run, so the same run gives the same bytes
// (P1), and merging only adds counts, so its result does not depend on
// the order of its inputs.
package coverage

import (
	"cmp"
	"slices"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/elab"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// point is one coverable statement or cover directive.
type point struct {
	file  *sema.File
	start int // byte offset of the statement in file
	line  int
	count int64
	// alts counts the alternatives a decision took (nil for another
	// statement); taken is their sum.
	alts  []int64
	taken int64
}

type pointKey struct {
	f     *sema.File
	start int
}

// Collector counts the statements and decisions of one run. It
// implements sema.StmtHook; it is not safe for concurrent use (a run is
// single-threaded).
type Collector struct {
	points []point
	// The maps are lookup only: index finds a point by its place,
	// files the project file of a tree root, bundle the roots of the
	// bundled std/ieee trees, seen the point of a statement run before
	// (-1: none), covers the cover directive a process runs, by
	// process path.
	index  map[pointKey]int32
	files  map[*cst.Green]*sema.File
	bundle map[*cst.Green]bool
	seen   map[*sema.SeqStmt]int32
	covers map[string]int32
	// unmatched counts the statements run that are no point although
	// they are not bundled, the branches outside their decision's
	// alternatives and the branches beyond one per run of their
	// decision: always 0 unless Units or the hook calls are wrong.
	unmatched int
}

var _ sema.StmtHook = (*Collector)(nil)

// New returns a collector of the statements of the design d
// elaborated in the analysis r: those of Units(r, d.Top), with the
// processes of d that run their PSL cover directives.
func New(r *sema.Result, d *elab.Design) *Collector {
	c := &Collector{index: map[pointKey]int32{},
		files:  map[*cst.Green]*sema.File{},
		bundle: map[*cst.Green]bool{},
		seen:   map[*sema.SeqStmt]int32{},
		covers: map[string]int32{}}
	if r != nil {
		for _, f := range r.Files {
			if f.Parse != nil {
				c.files[f.Parse.Root] = f
			}
		}
		if r.Bundle != nil {
			for _, f := range r.Bundle.Files {
				if f.Parse != nil {
					c.bundle[f.Parse.Root] = true
				}
			}
		}
	}
	for _, u := range Units(r, d.Top) {
		f := u.File
		if f == nil || f.Lines == nil {
			continue
		}
		c.conc(f, u.Body)
		for _, x := range u.Decls {
			if x.Kind.IsSubprogram() &&
				x.Node.Kind() == cst.KindSubprogramBody {
				c.seq(f, x.Seq)
			}
		}
	}
	for _, p := range d.Procs {
		if p.File == nil || p.Stmt == nil ||
			p.Stmt.Node.Kind() != cst.KindPSLCover {
			continue
		}
		key := pointKey{p.File, p.Stmt.Node.Span().Start}
		if i, ok := c.index[key]; ok {
			c.covers[p.Path] = i
		}
	}
	return c
}

// conc adds the statements of the concurrent statements ss of file f.
func (c *Collector) conc(f *sema.File, ss []*sema.Stmt) {
	for _, s := range ss {
		switch s.Kind {
		case sema.StmtProcess:
			c.seq(f, s.Seq)
			continue
		case sema.StmtOther:
			switch s.Node.Kind() {
			case cst.KindConcurrentSimpleSignalAssign,
				cst.KindConcurrentConditionalSignalAssign,
				cst.KindConcurrentSelectedSignalAssign:
				c.add(f, s.Node, sema.Alternatives(
					&sema.SeqStmt{Kind: sema.SeqSignal, Node: s.Node}))
			case cst.KindConcurrentAssertionStmt,
				cst.KindConcurrentProcedureCallStmt, cst.KindPSLCover:
				c.add(f, s.Node, 0)
			}
			continue
		}
		// Blocks, generates (every alternative) and instances.
		c.conc(f, s.Body)
		for _, a := range s.Alts {
			c.conc(f, a.Body)
		}
	}
}

// seq adds the sequential statements ss of file f and those nested in
// them.
func (c *Collector) seq(f *sema.File, ss []*sema.SeqStmt) {
	for _, s := range ss {
		c.add(f, s.Node, sema.Alternatives(s))
		for _, br := range s.Branches {
			c.seq(f, br.Body)
		}
		c.seq(f, s.Else)
		for _, a := range s.Alts {
			c.seq(f, a.Body)
		}
		c.seq(f, s.Body)
	}
}

func (c *Collector) add(f *sema.File, n cst.Node, alts int) {
	if n.IsZero() {
		return
	}
	start := n.Span().Start
	key := pointKey{f, start}
	if _, dup := c.index[key]; dup {
		return
	}
	line, _ := f.Lines.LineCol(start)
	i := int32(len(c.points))
	c.index[key] = i
	p := point{file: f, start: start, line: line}
	if alts > 0 {
		p.alts = make([]int64, alts)
	}
	c.points = append(c.points, p)
}

// Stmt implements sema.StmtHook.
func (c *Collector) Stmt(s *sema.SeqStmt) {
	if i := c.lookup(s); i >= 0 {
		c.points[i].count++
	}
}

// Branch implements sema.StmtHook.
func (c *Collector) Branch(s *sema.SeqStmt, alt int) {
	i := c.lookup(s)
	if i < 0 {
		return
	}
	p := &c.points[i]
	if alt < 0 || alt >= len(p.alts) || p.taken >= p.count {
		c.unmatched++
		return
	}
	p.alts[alt]++
	p.taken++
}

// lookup returns the point of statement s (-1: none).
func (c *Collector) lookup(s *sema.SeqStmt) int32 {
	if i, ok := c.seen[s]; ok {
		return i
	}
	i := c.resolve(s)
	c.seen[s] = i
	return i
}

// resolve finds the point of s by its file (the root of its syntax
// tree) and its start.
func (c *Collector) resolve(s *sema.SeqStmt) int32 {
	n := s.Node
	if n.IsZero() {
		c.unmatched++
		return -1
	}
	root := n
	for p := n.Parent(); !p.IsZero(); p = p.Parent() {
		root = p
	}
	g := root.Green()
	if c.bundle[g] {
		return -1
	}
	if f := c.files[g]; f != nil {
		if i, ok := c.index[pointKey{f, n.Span().Start}]; ok {
			return i
		}
	}
	c.unmatched++
	return -1
}

// Unmatched is the number of statements the run executed outside the
// coverable ones (not counting bundled std/ieee statements), plus the
// branches reported outside their decision's alternatives or more
// often than the decision ran. It is 0 unless the coverable set misses
// a statement the design runs or a hook call is wrong; the tests
// require 0.
func (c *Collector) Unmatched() int { return c.unmatched }

// Report returns the coverage of the run whose trace is t: the
// statement and branch counts so far, and as the count of each cover
// directive the cover-hit events in t of its processes (t nil: none).
func (c *Collector) Report(t *trace.Trace) Report {
	counts := make([]int64, len(c.points))
	for i, p := range c.points {
		counts[i] = p.count
	}
	if t != nil {
		for _, e := range t.Events {
			if e.Kind != trace.CoverHit || e.Process < 1 ||
				e.Process > len(t.Processes) {
				continue
			}
			if i, ok := c.covers[t.Processes[e.Process-1].Path]; ok {
				counts[i] = add(counts[i], 1)
			}
		}
	}
	order := make([]int32, len(c.points))
	for i := range order {
		order[i] = int32(i)
	}
	slices.SortFunc(order, func(a, b int32) int {
		pa, pb := &c.points[a], &c.points[b]
		return cmp.Or(cmp.Compare(pa.file.Path, pb.file.Path),
			cmp.Compare(pa.start, pb.start))
	})
	var out Report
	var f *File
	block := 0
	for _, i := range order {
		p := &c.points[i]
		if f == nil || f.Path != p.file.Path {
			out.Files = append(out.Files, File{Path: p.file.Path})
			f = &out.Files[len(out.Files)-1]
		}
		if n := len(f.Lines); n > 0 && f.Lines[n-1].Line == p.line {
			f.Lines[n-1].Count = max(f.Lines[n-1].Count, counts[i])
		} else {
			f.Lines = append(f.Lines, Line{Line: p.line, Count: counts[i]})
			block = 0
		}
		if p.alts == nil {
			continue
		}
		for k, taken := range p.alts {
			if counts[i] == 0 {
				taken = NotRun
			}
			f.Branches = append(f.Branches, Branch{Line: p.line,
				Block: block, Branch: k, Taken: taken})
		}
		block++
	}
	return out
}
