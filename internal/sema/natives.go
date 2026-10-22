package sema

// natives.go is elaboration's door into static evaluation for the
// functions without a body whose result is fixed for a run: the
// std.env tool and version queries, GETENV, FILE_NAME, FILE_PATH,
// FILE_LINE and the clock (#18). Static evaluation alone cannot know
// them, so analysis (and the editor) leaves such calls not static;
// elaboration installs Natives so generics, signal initial values and
// design constants that call them are evaluated as a simulator does.

import "github.com/connerohnesorge/gohard/internal/eval"

// Natives evaluates the call e of the function x, which has no body,
// on the argument values args (in parameter order) with the result
// subtype rt. ok false leaves the call not static.
type Natives func(e *Expr, x *Decl, args []eval.Value, rt *eval.Type,
) (v eval.Value, ok bool, err error)

// WithNatives returns an evaluator over ev's design, without ev's
// bindings or caches, that evaluates the calls of functions without a
// body through n. Every evaluator derived from it (Fresh, Bind,
// WithDynamic, ...) does too.
func (ev *Evaluator) WithNatives(n Natives) *Evaluator {
	c := newEvaluator(ev.libs, ev.steps)
	c.std = ev.std
	c.sh.natives = n
	return c
}

// nativeCall is the function handle of a call left to Natives.
type nativeCall struct {
	e  *Expr
	x  *Decl
	rt *eval.Type
}

// native lowers the call e of x, a function without a body, to a
// call of the evaluator's Natives; ok false when it has none.
func (ev *Evaluator) native(e *Expr, x *Decl, b *eval.Budget,
) (eval.Expr, bool, error) {
	if ev.sh.natives == nil || x.Kind != DeclFunction || x.Implicit ||
		ev.body(x) != nil {
		return nil, false, nil
	}
	acts, err := actuals(e, x)
	if err != nil {
		return nil, true, err
	}
	args := make([]eval.Expr, 0, len(acts))
	for _, a := range acts {
		ax, err := ev.lower(a, b)
		if err != nil {
			return nil, true, err
		}
		args = append(args, ax)
	}
	rt, err := ev.layout(orUnknown(x.Type), b)
	if err != nil {
		return nil, true, err
	}
	return &eval.Call{Fn: &nativeCall{e: e, x: x, rt: rt}, Args: args,
		T: rt}, true, nil
}

// callNative calls the Natives of the handle n.
func (ev *Evaluator) callNative(n *nativeCall, args []eval.Value,
	b *eval.Budget,
) (eval.Value, error) {
	if err := b.Step(1); err != nil {
		return eval.Value{}, err
	}
	v, ok, err := ev.sh.natives(n.e, n.x, args, n.rt)
	if !ok {
		return eval.Value{}, eval.NotStatic("call of " + n.x.Text)
	}
	return v, err
}
