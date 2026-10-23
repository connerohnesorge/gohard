package lint

import (
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// latches reports lint/latch: a combinational process (a sensitivity
// list, no clock edge, no wait) that assigns a signal on some paths
// but not on all, so synthesis keeps its old value in a latch.
//
// The analysis errs toward silence: an element or field assignment
// counts as assigning the whole signal, a loop body as always running,
// and a procedure call as assigning every signal in its arguments.
func latches(res *sema.Result, sev diag.Severity) []diag.Diagnostic {
	bad := errorFiles(res)
	var ds []diag.Diagnostic
	for _, u := range res.Units {
		if !trusted(u, bad) {
			continue
		}
		l := latch{u: u, root: u.File.Root()}
		for _, st := range processes(u.Body) {
			p := cst.ProcessStmt{Node: st.Node}
			if p.Sensitivity().IsZero() || clocked(p) || waits(st.Seq) {
				continue
			}
			l.some, l.order = map[*sema.Decl]cst.Range{}, nil
			all := l.seq(st.Seq)
			for _, d := range l.order {
				if all[d] {
					continue
				}
				f := diag.New(diag.LintLatch, d.Text)
				f.Severity = sev
				ds = append(ds, at(u.File, l.some[d], f))
			}
		}
	}
	return ds
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
	root  cst.Node
	some  map[*sema.Decl]cst.Range
	order []*sema.Decl
}

// seq returns the signals assigned on every path through seq.
func (l *latch) seq(seq []*sema.SeqStmt) map[*sema.Decl]bool {
	all := map[*sema.Decl]bool{}
	for _, s := range seq {
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
		for _, b := range s.Branches {
			bodies = append(bodies, b.Body)
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

func intersect(a, b map[*sema.Decl]bool) map[*sema.Decl]bool {
	out := map[*sema.Decl]bool{}
	for d := range a {
		if b[d] {
			out[d] = true
		}
	}
	return out
}

// assigned returns the signals an assignment or procedure call drives:
// the written names of its target, or the signals in its arguments.
func (l *latch) assigned(s *sema.SeqStmt) map[*sema.Decl]bool {
	out := map[*sema.Decl]bool{}
	for _, r := range refsIn(l.u, s.Node.Span()) {
		if r.Decl == nil || !isSignal(r.Decl) {
			continue
		}
		write := r.Kind == sema.RefWrite ||
			r.Kind == sema.RefSelect && targeted(l.root, r.Span)
		if s.Kind == sema.SeqCall {
			write = associated(l.root, r.Span)
		}
		if !write {
			continue
		}
		out[r.Decl] = true
		if _, ok := l.some[r.Decl]; !ok {
			l.some[r.Decl] = r.Span
			l.order = append(l.order, r.Decl)
		}
	}
	return out
}
