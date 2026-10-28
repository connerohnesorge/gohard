package sema

// nativebody.go runs the bundled ieee functions that have a native Go
// body (internal/ieeenative, #59) without interpreting their source,
// when the evaluator's design runs them natively (SetNativeBodies:
// elaboration and simulation). A native body returns the value the
// interpreted body would; a call it declines (a numeric_std metavalue
// that the body warns about, a null argument, ...) is interpreted as
// before. A native call charges ieeenative.Cost steps (a step per
// element of its arguments and result), which differs from the
// interpreted body's count: native bodies change step counts, never a
// value or a trace of a run within its budget.

import (
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/ieeenative"
)

// nbody is the native body of one function declaration and its result
// subtype; a nil fn marks a function without one.
type nbody struct {
	fn     ieeenative.Func
	params []*eval.Type
	rt     *eval.Type
}

// SetNativeBodies makes ev, and every evaluator sharing its design
// state, call the native Go bodies of the ieee functions that have one
// (on true) or interpret every body (on false, the default).
func (ev *Evaluator) SetNativeBodies(on bool) { ev.sh.nativeBodies = on }

// NativeBodies reports whether ev calls native ieee bodies.
func (ev *Evaluator) NativeBodies() bool { return ev.sh.nativeBodies }

// nativeBody calls the native body of the pure function x on args; ok
// false when x has none or it declined the call.
func (ev *Evaluator) nativeBody(x *Decl, args []eval.Value,
	b *eval.Budget,
) (eval.Value, bool, error) {
	nb, err := ev.nativeOf(x, b)
	if err != nil || nb.fn == nil || len(args) != len(nb.params) {
		return eval.Value{}, false, nil
	}
	// A scalar outside its parameter's subtype fails the call: the
	// interpreted body reports it.
	for i, a := range args {
		if pt := nb.params[i]; pt.Kind.IsScalar() &&
			(a.T == nil || !pt.Range.Contains(a.I)) {
			return eval.Value{}, false, nil
		}
	}
	v, ok := nb.fn(args, nb.rt)
	if !ok {
		return eval.Value{}, false, nil
	}
	return v, true, b.Step(ieeenative.Cost(args, v))
}

// nativeOf returns the native body of x, looked up once per
// declaration: only a function of ieee.std_logic_1164 or
// ieee.numeric_std (the bundled packages, never a project unit of the
// same name) whose parameters and result native bodies know.
func (ev *Evaluator) nativeOf(x *Decl, b *eval.Budget) (nbody, error) {
	if nb, ok := ev.sh.nbodies[x]; ok {
		return nb, nil
	}
	nb, err := ev.lookupNative(x, b)
	if err != nil {
		return nbody{}, err
	}
	if ev.sh.nbodies == nil {
		ev.sh.nbodies = map[*Decl]nbody{}
	}
	ev.sh.nbodies[x] = nb
	return nb, nil
}

func (ev *Evaluator) lookupNative(x *Decl, b *eval.Budget) (nbody, error) {
	u := x.Unit
	if u == nil || u.File == nil || !u.File.Bundled || u.Library != "ieee" ||
		u.Name != "std_logic_1164" && u.Name != "numeric_std" ||
		InstanceOf(x) != nil {
		return nbody{}, nil
	}
	params := make([]*eval.Type, len(x.Params))
	for i, p := range x.Params {
		if p.Class != ClassConstant && p.Class != ClassVariable ||
			p.Mode != ModeIn && p.Mode != ModeNone {
			return nbody{}, nil
		}
		t, err := ev.layout(p.Type, b)
		if err != nil {
			return nbody{}, err
		}
		params[i] = t
	}
	rt, err := ev.layout(orUnknown(x.Type), b)
	if err != nil {
		return nbody{}, err
	}
	fn := ieeenative.Lookup(string(u.Name), string(x.Name), params, rt)
	return nbody{fn: fn, params: params, rt: rt}, nil
}
