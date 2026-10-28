package lint

import (
	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
)

// latches reports lint/latch: a combinational process (a sensitivity
// list, no clock edge, no wait) that assigns a signal on some paths
// but not on all, so synthesis keeps its old value in a latch.
//
// The analysis errs toward silence: an element or field assignment
// counts as assigning the whole signal, a loop body as always running,
// and a procedure call as assigning the signals it passes to an out or
// inout signal parameter (every signal in its arguments when the call
// is not resolved). An if or case whose conditions or selector read no
// signal, variable or file (a generic, a constant, a pure function of
// them) picks the same path every time the process runs, so it counts
// as assigning what any of its paths assigns.
func latches(res *sema.Result, m *meter, sev diag.Severity) []finding {
	bad := errorFiles(res)
	var out []finding
	for _, u := range res.Units {
		if !trusted(u, bad) {
			continue
		}
		l := latch{u: u, m: m, root: u.File.Root()}
		for _, st := range processes(u.Body) {
			p := cst.ProcessStmt{Node: st.Node}
			if p.Sensitivity().IsZero() || clocked(m, u, p) ||
				waits(st.Seq) {
				continue
			}
			l.some, l.order = map[*sema.Decl]cst.Range{}, nil
			all := l.seq(st.Seq)
			if m.over(u) {
				break
			}
			for _, d := range l.order {
				if all[d] {
					continue
				}
				f := diag.New(diag.LintLatch, d.Text)
				f.Severity = sev
				out = append(out, finding{u, at(u.File, l.some[d], f)})
			}
		}
	}
	return out
}

// processes returns the processes of a statement tree in source order.
func processes(body []*sema.Stmt) []*sema.Stmt {
	var out []*sema.Stmt
	for _, st := range body {
		if st.Kind == sema.StmtProcess {
			out = append(out, st)
		}
		out = append(out, processes(st.Body)...)
		for _, a := range st.Alts {
			out = append(out, processes(a.Body)...)
		}
	}
	return out
}

func waits(seq []*sema.SeqStmt) bool {
	for _, s := range seq {
		if s.Kind == sema.SeqWait || waits(s.Body) || waits(s.Else) {
			return true
		}
		for _, b := range s.Branches {
			if waits(b.Body) {
				return true
			}
		}
		for _, a := range s.Alts {
			if waits(a.Body) {
				return true
			}
		}
	}
	return false
}

// latch is the state of one process's analysis: some holds every
// signal assigned on any path, at its first assignment, and order
// lists them in that order.
type latch struct {
	u     *sema.Unit
	m     *meter
	root  cst.Node
	some  map[*sema.Decl]cst.Range
	order []*sema.Decl
}

// seq returns the signals assigned on every path through seq.
func (l *latch) seq(seq []*sema.SeqStmt) map[*sema.Decl]bool {
	all := map[*sema.Decl]bool{}
	for _, s := range seq {
		if !l.m.spend(l.u, 1) {
			break
		}
		for d := range l.stmt(s) {
			all[d] = true
		}
	}
	return all
}

func (l *latch) stmt(s *sema.SeqStmt) map[*sema.Decl]bool {
	switch s.Kind {
	case sema.SeqSignal, sema.SeqCall:
		return l.assigned(s)
	case sema.SeqIf:
		var bodies [][]*sema.SeqStmt
		static := true
		for _, b := range s.Branches {
			bodies = append(bodies, b.Body)
			static = static && l.static(b.Cond)
		}
		if static {
			return l.join(append(bodies, s.Else))
		}
		all := l.meet(bodies)
		if s.Else == nil {
			return nil // no else: a path assigns nothing
		}
		return intersect(all, l.seq(s.Else))
	case sema.SeqCase:
		var bodies [][]*sema.SeqStmt
		for _, a := range s.Alts {
			bodies = append(bodies, a.Body)
		}
		if l.static(s.Selector) {
			return l.join(bodies)
		}
		return l.meet(bodies)
	case sema.SeqLoop, sema.SeqBlock:
		return l.seq(s.Body)
	}
	return nil
}

// meet returns the signals every body assigns, recording what any
// body assigns.
func (l *latch) meet(bodies [][]*sema.SeqStmt) map[*sema.Decl]bool {
	var all map[*sema.Decl]bool
	for i, b := range bodies {
		got := l.seq(b)
		if i == 0 {
			all = got
		} else {
			all = intersect(all, got)
		}
	}
	return all
}

// join returns the signals any body assigns.
func (l *latch) join(bodies [][]*sema.SeqStmt) map[*sema.Decl]bool {
	all := map[*sema.Decl]bool{}
	for _, b := range bodies {
		for d := range l.seq(b) {
			all[d] = true
		}
	}
	return all
}

func intersect(a, b map[*sema.Decl]bool) map[*sema.Decl]bool {
	out := map[*sema.Decl]bool{}
	for d := range a {
		if b[d] {
			out[d] = true
		}
	}
	return out
}

// static reports whether the condition or selector e reads no signal,
// variable or file and calls no impure function, so it has the same
// value every time the process runs. A missing e (else) is static.
func (l *latch) static(e *sema.Expr) bool {
	if e == nil {
		return true
	}
	for _, r := range refsIn(l.m, l.u, e.Node.Span()) {
		d := object(r.Decl)
		if d == nil {
			continue
		}
		switch d.Kind {
		case sema.DeclSignal, sema.DeclVariable,
			sema.DeclSharedVariable, sema.DeclFile, sema.DeclPort:
			return false
		case sema.DeclParameter:
			if d.Class != sema.ClassConstant {
				return false
			}
		case sema.DeclFunction:
			if !d.Pure {
				return false
			}
		}
	}
	return true
}

// assigned returns the signals an assignment or procedure call drives:
// the written names of its target, or the signals a call passes to
// out and inout signal parameters.
func (l *latch) assigned(s *sema.SeqStmt) map[*sema.Decl]bool {
	out := map[*sema.Decl]bool{}
	add := func(d *sema.Decl, rg cst.Range) {
		out[d] = true
		if _, ok := l.some[d]; !ok {
			l.some[d] = rg
			l.order = append(l.order, d)
		}
	}
	if c := s.Call; s.Kind == sema.SeqCall && c != nil &&
		len(c.Args) > 0 && len(c.Formals) == len(c.Args) {
		for i, a := range c.Args {
			l.m.spend(l.u, 1)
			if f := c.Formals[i]; f != nil &&
				(f.Class != sema.ClassSignal || f.Mode == sema.ModeIn ||
					f.Mode == sema.ModeNone) {
				continue // a formal that cannot drive its actual
			}
			if d := object(named(a)); isSignal(d) && a != nil {
				add(d, a.Node.Span())
			}
		}
		return out
	}
	for _, r := range refsIn(l.m, l.u, s.Node.Span()) {
		d := object(r.Decl)
		if !isSignal(d) {
			continue
		}
		write := r.Kind == sema.RefWrite ||
			r.Kind == sema.RefSelect && targeted(l.root, r.Span)
		if s.Kind == sema.SeqCall {
			write = associated(l.root, r.Span)
		}
		if write {
			add(d, r.Span)
		}
	}
	return out
}
