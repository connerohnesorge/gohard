package elab

import "github.com/connerohnesorge/gohard/internal/sema"

// interp_rtconst.go: constants of design regions (packages,
// architectures, entities, blocks) whose initial value only the
// interpreter can compute, because it calls an impure function or a
// native one without a body (std.env's TOOL_NAME, GETENV, FILE_LINE,
// ...), LRM 14.4.2.1. The static evaluator gives up on such a value,
// so these constants are elaborated as world objects, like shared
// variables: once per declaring instance, in sharedOrder, before the
// first process runs.

// runtimeConst reports whether the constant d of a design region gets
// its value from the interpreter. ev is an evaluator of the design
// (subprogram bodies are design-wide).
func (w *world) runtimeConst(ev *sema.Evaluator, d *sema.Decl) bool {
	if d.Kind != sema.DeclConstant || d.Value == nil || d.Parent == nil ||
		!globalRegion(d.Parent) || ev == nil {
		return false
	}
	if v, ok := w.rtConst[d]; ok {
		return v
	}
	w.rtConst[d] = false // a circular reference is not one
	v := w.runtimeExpr(ev, d.Value, 0)
	w.rtConst[d] = v
	return v
}

// maxRuntimeDepth bounds the expression walk of runtimeExpr.
const maxRuntimeDepth = 256

// runtimeExpr reports whether e calls a function the interpreter runs
// or reads a constant that runtimeConst holds.
func (w *world) runtimeExpr(ev *sema.Evaluator, e *sema.Expr, depth int,
) bool {
	if e == nil || depth > maxRuntimeDepth {
		return false
	}
	if e.Decl != nil {
		x := e.Decl.Denotes()
		switch {
		case e.Kind == sema.ExprCall && x.Kind == sema.DeclFunction &&
			!x.Implicit && (ev.Body(x) == nil || runsHere(x)):
			return true
		case e.Kind == sema.ExprName && x.Kind == sema.DeclConstant &&
			(w.runtimeConst(ev, x) || envDeferred(x)):
			return true
		}
	}
	if w.runtimeExpr(ev, e.Prefix, depth+1) {
		return true
	}
	for _, a := range e.Args {
		if w.runtimeExpr(ev, a, depth+1) {
			return true
		}
	}
	for _, as := range e.Assocs {
		if w.runtimeExpr(ev, as.Value, depth+1) {
			return true
		}
		for _, c := range as.Choices {
			if w.runtimeExpr(ev, c, depth+1) {
				return true
			}
		}
	}
	if rc := e.Range; rc != nil {
		return w.runtimeExpr(ev, rc.Left, depth+1) ||
			w.runtimeExpr(ev, rc.Right, depth+1) ||
			w.runtimeExpr(ev, rc.Attr, depth+1)
	}
	return false
}
