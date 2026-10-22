package elab

// interp_builtin.go routes the calls of subprograms built into the
// kernel (#35 R6) to their natives: NOW (std.standard), std.env
// (interp_env.go), std.textio (interp_textio.go), std.reflection
// (interp_reflect.go) and the implicit operations without an
// evaluator operation (interp_implicit.go). A subprogram without a
// native is the error *nativeError, which the process reports as
// unsupported/construct (please report) and which ends the run.

import (
	"errors"

	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// unitOf returns "lib.unit" of x's design unit.
func unitOf(x *sema.Decl) string {
	if x.Unit == nil {
		return ""
	}
	return string(x.Unit.Library) + "." + string(x.Unit.Name)
}

// nativeError reports a call of a built-in subprogram GoHard has no
// native for: name is "lib.unit.name".
type nativeError struct{ name string }

func (e *nativeError) Error() string {
	return e.name + " is not supported yet, please report"
}

// unsupported is the error of a call of x, which has no native. Only
// an implicit operation or a subprogram of a library GoHard bundles
// (std, ieee) is a missing native, a GoHard gap the user is asked to
// report; a design subprogram without a body (a package body that was
// never written) is the design's error, the v1.0 sim/runtime-error.
func unsupported(x *sema.Decl) error {
	name := unitOf(x) + "." + x.Text
	if !x.Implicit && (x.Unit == nil ||
		!bundled(string(x.Unit.Library))) {
		return errors.New(name + " is not supported in simulation")
	}
	return &nativeError{name: name}
}

// bundled reports whether lib is a library GoHard ships the sources
// of.
func bundled(lib string) bool {
	return lib == "std" || lib == "ieee"
}

// builtinFunc evaluates a built-in function. obj is the protected
// object of a method call (0: none).
func (m *machine) builtinFunc(fn *callFn, obj int64, args []arg,
) (eval.Value, error) {
	x := fn.x
	rt := fn.ret
	if err := m.builtinOuts(fn, args); err != nil {
		return eval.Value{}, err
	}
	switch u := unitOf(x); {
	case x.Name == "now" && u == "std.standard":
		if m.ctx == nil {
			return eval.Phys(rt, 0), nil
		}
		return eval.Phys(rt, int64(m.ctx.Now())), nil
	case x.Implicit:
		// Before the unit cases: the implicit operations of a type
		// std.env or std.reflection declares (TO_STRING of
		// DAYOFWEEK, DIR_OPEN_STATUS, INDEX, ...) are those of any
		// other type.
		return m.implicitFunc(fn, args)
	case u == "std.env":
		return m.envFunc(x, args, rt)
	case u == "std.reflection":
		return m.reflectFunc(x, obj, args, rt)
	case u == "std.textio" && x.Name == "justify":
		if len(args) < 3 {
			return eval.Value{}, unsupported(x)
		}
		return strValue(rt, justify(stringOf(args[0].v),
			args[1].v.I, args[2].v.I)), nil
	}
	return eval.Value{}, unsupported(x)
}

// builtinOuts gives the out and inout variable parameters of the call
// fn of a built-in function (VHDL-2019, as DIR_OPEN's Dir) the places
// of their actuals, resolved in the caller's frame, so the native
// writes them with machine.out.
func (m *machine) builtinOuts(fn *callFn, args []arg) error {
	if fn.e == nil {
		return nil
	}
	var acts []*sema.Expr
	for i, p := range fn.x.Params {
		if i >= len(args) || p.Class == sema.ClassSignal ||
			p.Class == sema.ClassFile ||
			p.Mode != sema.ModeOut && p.Mode != sema.ModeInout {
			continue
		}
		if acts == nil {
			var err error
			if acts, err = actualsOf(fn.e, fn.x); err != nil {
				return err
			}
		}
		if i >= len(acts) || acts[i] == nil || acts[i] == p.Value {
			continue
		}
		dst, err := m.placeOf(acts[i])
		if err != nil {
			return err
		}
		args[i].dst = &dst
	}
	return nil
}

// builtinProc executes a built-in procedure; stop reports
// std.env.stop or finish. obj is the protected object of a method
// call (0: none).
func (m *machine) builtinProc(n *node, x *sema.Decl, obj int64,
	args []arg,
) (stop bool, err error) {
	switch u := unitOf(x); {
	case x.Implicit:
		return false, m.implicitProc(x, args)
	case u == "std.env":
		return m.envProc(n, x, args)
	case u == "std.reflection":
		return false, m.reflectProc(x, obj, args)
	case u == "std.textio":
		return false, m.textio(x, args)
	}
	return false, unsupported(x)
}
