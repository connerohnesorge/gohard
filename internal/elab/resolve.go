package elab

import (
	"errors"

	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
)

// FuncResolver is a resolution function run by the interpreter on the
// vendored source (ieee.std_logic_1164.resolved, a user function): it
// builds the array of driver values the function takes and calls it
// through the shared evaluator, charging the run's budget.
type FuncResolver struct {
	Fn  *sema.Decl
	ev  *sema.Evaluator
	arr *eval.Type // the parameter's (unconstrained) array type
}

// Resolve implements sim.Resolver.
func (f *FuncResolver) Resolve(b *eval.Budget, drivers []eval.Value) (
	eval.Value, error,
) {
	n := int64(len(drivers))
	r := eval.Range{Left: 0, Right: n - 1, Dir: eval.To}
	if len(f.arr.Index) > 0 && f.arr.Index[0] != nil {
		lo := f.arr.Index[0].Range.Low()
		r = eval.Range{Left: lo, Right: lo + n - 1, Dir: eval.To}
	}
	elems := make([]eval.Value, n)
	for i, v := range drivers {
		v.T = f.arr.Elem
		elems[i] = v
	}
	arg := eval.Array(f.arr.ArraySubtype(r), []eval.Range{r}, elems)
	return metered(b, func(sb *eval.Budget) (eval.Value, error) {
		return f.ev.Call(f.Fn, []eval.Value{arg}, sb)
	})
}

type resolverEntry struct {
	fn *sema.Decl
	r  sim.Resolver
}

// resolver returns the (shared) resolver of resolution function fn.
func (d *Design) resolver(fn *sema.Decl) (sim.Resolver, error) {
	for _, e := range d.res {
		if e.fn == fn {
			return e.r, nil
		}
	}
	if len(fn.Params) != 1 {
		return nil, errors.New("resolution function " + fn.Text +
			" does not take one parameter")
	}
	ev := sema.NewEvaluator(d.r, d.Budget.Limit())
	arr, err := ev.Layout(fn.Params[0].Type)
	if err != nil {
		return nil, err
	}
	if arr.Kind != eval.KindArray || arr.Elem == nil {
		return nil, errors.New("resolution function " + fn.Text +
			" does not take an array")
	}
	fr := &FuncResolver{Fn: fn, ev: ev.WithMemo(), arr: arr}
	// Warm the evaluator's constant cache (the resolution table) at
	// elaboration, so every simulation call costs the same steps and a
	// forked kernel counts like a fresh one.
	init, err := eval.Default(d.Budget, arr.Elem)
	if err == nil {
		_, err = fr.Resolve(d.Budget, []eval.Value{init, init})
	}
	if err != nil {
		return nil, err
	}
	d.res = append(d.res, resolverEntry{fn, fr})
	return fr, nil
}

// resolution sets the resolution of signal s of subtype st: one
// resolver for every scalar subelement when they agree, else one per
// subelement. A resolution function of a whole composite is not
// supported yet.
func (d *Design) resolution(s *sim.Signal, st *sema.Type) {
	var fns []*sema.Decl
	composite := false
	var walk func(t *sema.Type, v eval.Value, inh *sema.Decl)
	walk = func(t *sema.Type, v eval.Value, inh *sema.Decl) {
		var res *sema.Resolution
		if t != nil {
			res = t.ResolutionOf()
		}
		if isLeaf(v) {
			if res != nil && res.Func != nil {
				inh = res.Func
			}
			fns = append(fns, inh)
			return
		}
		var base *sema.Type
		if t != nil {
			base = t.Base()
		}
		switch v.Kind() {
		case eval.KindArray:
			if res != nil {
				if res.Elem != nil && res.Elem.Func != nil {
					inh = res.Elem.Func
				} else if res.Func != nil {
					composite = true
				}
			}
			var et *sema.Type
			if t != nil && t.ElemSub != nil {
				et = t.ElemSub
			} else if base != nil {
				et = base.Elem
			}
			for _, e := range v.E {
				walk(et, e, inh)
			}
		case eval.KindRecord:
			if res != nil && res.Func != nil {
				composite = true
			}
			for i, e := range v.E {
				var ft *sema.Type
				if t != nil && i < len(t.FieldSubs) &&
					t.FieldSubs[i] != nil {
					ft = t.FieldSubs[i]
				} else if base != nil && i < len(base.Fields) {
					ft = base.Fields[i].Type
				}
				fi := inh
				if res != nil && i < len(res.Fields) &&
					res.Fields[i] != nil && res.Fields[i].Func != nil {
					fi = res.Fields[i].Func
				}
				walk(ft, e, fi)
			}
		}
	}
	walk(st, s.Init, nil)
	if composite {
		d.unsupported(s.Src, "a resolution function of a composite "+
			"signal")
		return
	}
	same := true
	for _, f := range fns {
		same = same && f == fns[0]
	}
	if len(fns) == 0 || (same && fns[0] == nil) {
		return
	}
	rs := make([]sim.Resolver, len(fns))
	for i, f := range fns {
		if f == nil {
			continue
		}
		r, err := d.resolver(f)
		if err != nil {
			d.runtime(s.Src, s.Path, err)
			return
		}
		rs[i] = r
	}
	if same {
		s.Resolve = rs[0]
		return
	}
	s.LeafResolve = rs
}
