package elab

import (
	"errors"

	"github.com/connerohnesorge/gohard/internal/elab/hier"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
)

// Indexer evaluates the index expressions and slice ranges of a name:
// sema.Evaluator for static names, the interpreter for dynamic ones.
type Indexer interface {
	Value(e *sema.Expr) (eval.Value, error)
	Range(rc *sema.RangeCons) (eval.Range, error)
}

// ErrNotSignal is returned by Target for a name that denotes no
// signal of the design.
var ErrNotSignal = errors.New("not a signal")

// Target resolves the name e (a signal or port, or an indexed, sliced
// or selected name of one) seen from instance in to the signal part it
// denotes, and returns the part's shape (its current subvalue in the
// signal's initial value, with the part's index ranges).
func (d *Design) Target(in *hier.Instance, e *sema.Expr, ix Indexer,
) (sim.Target, eval.Value, error) {
	id, first, shape, err := d.part(in, e, ix)
	if err != nil {
		return sim.Target{}, eval.Value{}, err
	}
	return sim.Target{Sig: id, First: first, N: leaves(shape)}, shape, nil
}

func (d *Design) part(in *hier.Instance, e *sema.Expr, ix Indexer,
) (sim.SigID, int, eval.Value, error) {
	return partFrom(e, ix, func(e *sema.Expr) (sim.SigID, int,
		eval.Value, error,
	) {
		x := e.Decl
		if x == nil {
			return 0, 0, eval.Value{}, ErrNotSignal
		}
		id, ok := d.Signal(in, x.Denotes())
		if !ok {
			return 0, 0, eval.Value{}, ErrNotSignal
		}
		return id, 0, d.Model.Signals[id].Init, nil
	})
}

// partFrom resolves the indexed, sliced and selected name e to its
// first leaf and shape; name resolves the innermost simple name.
func partFrom(e *sema.Expr, ix Indexer,
	name func(*sema.Expr) (sim.SigID, int, eval.Value, error),
) (sim.SigID, int, eval.Value, error) {
	if e == nil {
		return 0, 0, eval.Value{}, ErrNotSignal
	}
	switch e.Kind {
	case sema.ExprName:
		return name(e)
	case sema.ExprIndex:
		id, first, shape, err := partFrom(e.Prefix, ix, name)
		if err != nil {
			return 0, 0, eval.Value{}, err
		}
		if shape.Kind() != eval.KindArray ||
			len(e.Args) != len(shape.B) {
			return 0, 0, eval.Value{}, errors.New("indexed name of a " +
				"non-array signal")
		}
		lin := int64(0)
		for i, a := range e.Args {
			v, err := ix.Value(a)
			if err != nil {
				return 0, 0, eval.Value{}, err
			}
			r := shape.B[i]
			if !r.Contains(v.I) {
				return 0, 0, eval.Value{}, eval.IndexError(v.I, r)
			}
			lin = lin*r.Len() + r.Offset(v.I)
		}
		if lin > 0 {
			first += int(lin) * leaves(shape.E[0])
		}
		return id, first, shape.E[lin], nil
	case sema.ExprSlice:
		id, first, shape, err := partFrom(e.Prefix, ix, name)
		if err != nil {
			return 0, 0, eval.Value{}, err
		}
		if shape.Kind() != eval.KindArray || len(shape.B) != 1 {
			return 0, 0, eval.Value{}, errors.New("slice of a " +
				"non-array signal")
		}
		r, err := ix.Range(e.Range)
		if err != nil {
			return 0, 0, eval.Value{}, err
		}
		ar := shape.B[0]
		out := shape
		out.B = []eval.Range{r}
		if r.Null() {
			out.E = nil
			return id, first, out, nil
		}
		if r.Dir != ar.Dir || !ar.Contains(r.Left) ||
			!ar.Contains(r.Right) {
			return 0, 0, eval.Value{}, eval.SliceError(r, ar)
		}
		lo := ar.Offset(r.Left)
		if lo > 0 {
			first += int(lo) * leaves(shape.E[0])
		}
		out.E = shape.E[lo : lo+r.Len()]
		if out.T != nil && out.T.Bounds != nil {
			out.T = out.T.BaseType().ArraySubtype(r)
		}
		return id, first, out, nil
	case sema.ExprSelect:
		id, first, shape, err := partFrom(e.Prefix, ix, name)
		if err != nil {
			return 0, 0, eval.Value{}, err
		}
		if shape.Kind() != eval.KindRecord || e.Decl == nil ||
			e.Decl.Pos < 0 || e.Decl.Pos >= len(shape.E) {
			return 0, 0, eval.Value{}, errors.New("selected name of a " +
				"non-record signal")
		}
		for _, el := range shape.E[:e.Decl.Pos] {
			first += leaves(el)
		}
		return id, first, shape.E[e.Decl.Pos], nil
	}
	return 0, 0, eval.Value{}, ErrNotSignal
}

// staticTarget is the driver of an assignment target (LRM 14.7.2): the
// longest static prefix, so a name with a dynamic index drives the
// whole prefix.
func (d *Design) staticTarget(in *hier.Instance, e *sema.Expr,
	ev *sema.Evaluator,
) (sim.Target, bool) {
	for e != nil {
		if t, _, err := d.Target(in, e, ev); err == nil {
			return t, true
		} else if errors.Is(err, ErrNotSignal) &&
			e.Kind == sema.ExprName {
			return sim.Target{}, false
		}
		switch e.Kind {
		case sema.ExprIndex, sema.ExprSlice, sema.ExprSelect:
			e = e.Prefix
		default:
			return sim.Target{}, false
		}
	}
	return sim.Target{}, false
}

func isLeaf(v eval.Value) bool {
	if v.T == nil {
		return len(v.E) == 0
	}
	return v.T.Kind != eval.KindArray && v.T.Kind != eval.KindRecord
}

func leaves(v eval.Value) int {
	if isLeaf(v) {
		return 1
	}
	if v.T != nil && v.T.Kind == eval.KindArray {
		// Every element of an array has the element subtype, so the
		// same number of leaves.
		if len(v.E) == 0 {
			return 0
		}
		return len(v.E) * leaves(v.E[0])
	}
	n := 0
	for _, e := range v.E {
		n += leaves(e)
	}
	return n
}
