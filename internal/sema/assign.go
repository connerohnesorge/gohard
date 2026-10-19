package sema

import (
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/token"
)

// Assign is one signal or variable assignment statement of an entity,
// architecture, block or generate body (its processes included): the
// target and every value written to it. The static checks (#34)
// compare the statically known widths of target and values and check
// the static indices and slice bounds of the names they contain, with
// the unit (staticChecks) and per instance (internal/elab/hier).
type Assign struct {
	Target *Expr
	Values []*Expr
	// Seq is set for an assignment inside a process, which may sit in
	// a branch the instance's generics never take: it is checked with
	// the unit only.
	Seq bool
}

// assignTarget starts the assignment whose target is e. Assignments
// in subprograms and packages are not recorded: their objects are
// elaborated by each call.
func (a *analyzer) assignTarget(e *Expr) {
	a.asg = nil
	if a.quiet || a.fn != nil {
		return
	}
	seq := false
	for r := a.cur; r != nil; r = r.Parent {
		switch r.Kind {
		case RegionProcess:
			seq = true
			continue
		case RegionLoop, RegionStatement, RegionSeqBlock:
			continue
		case RegionEntity, RegionArchitecture, RegionBlock,
			RegionGenerate:
			if seq && branched(e.Node) {
				return
			}
			a.asg = &Assign{Target: e, Seq: seq}
			r.Assigns = append(r.Assigns, a.asg)
		}
		return
	}
}

// assignValue adds the value e to the assignment being analyzed.
func (a *analyzer) assignValue(e *Expr) {
	if a.asg != nil && e != nil && !a.quiet {
		a.asg.Values = append(a.asg.Values, e)
	}
}

// CheckAssign checks assignment as statically and returns the node of
// the first error: an index or slice outside its prefix's range in the
// target or a value, or a value whose length differs from the
// target's. What is not static is not checked (nil error); the error
// may also be the step limit.
func (ev *Evaluator) CheckAssign(as *Assign) (cst.Node, error) {
	b := eval.NewBudget(ev.steps)
	n, err := ev.checkAssign(as, b)
	ev.used = b.Used()
	return n, err
}

func (ev *Evaluator) checkAssign(as *Assign, b *eval.Budget,
) (cst.Node, error) {
	if as.Target == nil || as.Target.illTyped {
		return cst.Node{}, nil
	}
	if n, err := ev.checkNames(as.Target, b); err != nil {
		return n, err
	}
	tl, tok, err := ev.exprLen(as.Target, b)
	if err != nil {
		return as.Target.Node, err
	}
	for _, v := range as.Values {
		if v == nil || v.illTyped {
			continue
		}
		if n, err := ev.checkNames(v, b); err != nil {
			return n, err
		}
		if !tok {
			continue
		}
		vl, vok, err := ev.exprLen(v, b)
		if err != nil {
			return v.Node, err
		}
		if !vok {
			vl, vok, err = ev.staticLen(v, b)
			if err != nil {
				return v.Node, err
			}
		}
		if vok && vl != tl {
			return v.Node, eval.LengthError(tl, vl)
		}
	}
	return cst.Node{}, nil
}

// checkNames checks every indexed and sliced name in e (not inside a
// conditional expression, whose branch may not be taken).
func (ev *Evaluator) checkNames(e *Expr, b *eval.Budget,
) (cst.Node, error) {
	if e == nil || e.illTyped || e.Kind == ExprCond {
		return cst.Node{}, nil
	}
	if e.Kind == ExprIndex || (e.Kind == ExprSlice &&
		!ev.sliceDirWrong(e, b)) {
		// Only the name's own indices count: an index expression
		// that fails to evaluate (a value out of range) may be in code
		// that never runs, which the tools accept.
		err := ev.checkName(e, b)
		if c := eval.CodeOf(err); c == eval.ErrCodeIndex ||
			c == eval.ErrCodeBudget {
			return e.Node, err
		}
	}
	if n, err := ev.checkNames(e.Prefix, b); err != nil {
		return n, err
	}
	for _, x := range e.Args {
		if n, err := ev.checkNames(x, b); err != nil {
			return n, err
		}
	}
	for _, as := range e.Assocs {
		if n, err := ev.checkNames(as.Value, b); err != nil {
			return n, err
		}
	}
	return cst.Node{}, nil
}

// exprLen returns the static length of a one-dimensional array name or
// slice e; ok is false when it is not known.
func (ev *Evaluator) exprLen(e *Expr, b *eval.Budget,
) (int64, bool, error) {
	switch e.Kind {
	case ExprSlice:
		// Only explicit bounds: a slice by a subtype name or 'RANGE
		// is left to the evaluator's name check.
		if e.Range == nil || e.Range.Attr != nil ||
			e.Range.Left == nil || e.Range.Right == nil {
			return 0, false, nil
		}
		r, err := ev.rangeOf(e.Range, b)
		if err != nil {
			return 0, false, designOrLimit(err)
		}
		return r.Len(), true, nil
	case ExprName, ExprSelect:
		// Not an indexed element: its subtype may take its constraint
		// from the object's declaration.
	default:
		return 0, false, nil
	}
	t := e.Type
	if t == nil || t.Unknown() || t.Base().Class != TypeArray ||
		t.Constraints() == nil {
		return 0, false, nil
	}
	lt, err := ev.layout(t, b)
	if err != nil {
		return 0, false, designOrLimit(err)
	}
	if lt.Kind != eval.KindArray || len(lt.Bounds) != 1 {
		return 0, false, nil
	}
	return lt.Bounds[0].Len(), true, nil
}

// staticLen returns the length of e's value when e is a static
// one-dimensional array expression.
func (ev *Evaluator) staticLen(e *Expr, b *eval.Budget,
) (int64, bool, error) {
	if e.Type == nil || e.Type.Unknown() ||
		e.Type.Base().Class != TypeArray ||
		(e.Kind == ExprLiteral && e.Lit.Kind() == token.KwNull) {
		return 0, false, nil
	}
	v, err := ev.value(e, b)
	if err != nil {
		// A value that fails to evaluate may be in code that never
		// runs: only the step limit is the check's.
		return 0, false, limitOnly(err)
	}
	if v.T == nil || v.T.Kind != eval.KindArray || len(v.B) > 1 {
		return 0, false, nil
	}
	return v.Len(), true, nil
}

// sliceDirWrong reports whether slice e statically runs against its
// prefix's direction, which analysis reports already (a sema/error):
// the assignment check does not repeat it.
func (ev *Evaluator) sliceDirWrong(e *Expr, b *eval.Budget) bool {
	pt := e.Prefix.Type
	if pt == nil || pt.Unknown() || pt.Base().Class != TypeArray ||
		pt.Constraints() == nil || e.Range == nil {
		return false
	}
	lt, err := ev.layout(pt, b)
	if err != nil || len(lt.Bounds) == 0 {
		return false
	}
	r, err := ev.rangeOf(e.Range, b)
	return err == nil && !r.Null() && r.Dir != lt.Bounds[0].Dir
}

// branched reports whether the sequential statement holding n sits in
// an if or case branch or a loop of its process: a static condition
// may never take it (the tools then accept what it would do wrong).
func branched(n cst.Node) bool {
	for p := n.Parent(); !p.IsZero(); p = p.Parent() {
		switch p.Kind() {
		case cst.KindIfStmt, cst.KindCaseStmt, cst.KindLoopStmt:
			return true
		case cst.KindProcessStmt:
			return false
		}
	}
	return false
}
