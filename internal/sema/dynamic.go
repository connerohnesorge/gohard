package sema

// dynamic.go is the simulator's door into the shared evaluator (#35):
// the sequential interpreter (internal/elab) evaluates every expression
// of a running process with an instance Evaluator carrying a Dynamic,
// which supplies what static evaluation cannot know: the current values
// of signals and variables, signal attributes, impure and built-in
// calls, dereferences and allocators. Everything else (literals,
// operators, constants, the interpreted bodies of pure functions such
// as the ieee packages) is evaluated exactly as static evaluation does,
// so elaboration, simulation and the editor share one evaluator.

import "github.com/connerohnesorge/gohard/internal/eval"

// Dynamic is the run-time context of an evaluation.
type Dynamic interface {
	// Ref returns the reference of the object d when the context owns
	// it (signals, ports, variables, process and subprogram constants,
	// files, shared variables); ok false leaves d to static rules.
	Ref(d *Decl) (ref eval.Ref, ok bool)
	// Load returns the current value of a reference Ref or Lower made.
	Load(ref eval.Ref) (eval.Value, error)
	// Lower lowers e when the context evaluates it (signal attributes,
	// impure and built-in calls, calls with signal or file
	// parameters, dereferences, allocators); ok false leaves e to
	// static rules. ev is the evaluator lowering e.
	Lower(ev *Evaluator, e *Expr, b *eval.Budget) (x eval.Expr, ok bool,
		err error)
	// Call calls a function handle that Lower put into an eval.Call;
	// ok false for a handle the context does not know.
	Call(fn eval.Func, args []eval.Value, b *eval.Budget) (v eval.Value,
		ok bool, err error)
}

// WithDynamic returns a copy of ev that evaluates through dy. The copy
// shares ev's bindings and caches; values that read dy are never
// cached. It memoizes pure calls (memo.go).
func (ev *Evaluator) WithDynamic(dy Dynamic) *Evaluator {
	c := *ev
	c.dyn = dy
	c.fr = nil
	c.memo = true
	return &c
}

// Dynamic returns the evaluator's dynamic context (nil for static
// evaluation).
func (ev *Evaluator) Dynamic() Dynamic { return ev.dyn }

// Eval evaluates e charging b.
func (ev *Evaluator) Eval(e *Expr, b *eval.Budget) (eval.Value, error) {
	return ev.value(e, b)
}

// Lower lowers e to evaluator IR charging b; the caller evaluates it
// with Machine.
func (ev *Evaluator) Lower(e *Expr, b *eval.Budget) (eval.Expr, error) {
	return ev.lower(e, b)
}

// Machine returns an evaluation machine over ev charging b.
func (ev *Evaluator) Machine(b *eval.Budget) *eval.Machine {
	return &eval.Machine{Env: ev, Budget: b}
}

// EvalRange evaluates the range constraint rc charging b.
func (ev *Evaluator) EvalRange(rc *RangeCons, b *eval.Budget,
) (eval.Range, error) {
	return ev.rangeOf(rc, b)
}

// LayoutIn evaluates the layout of t charging b.
func (ev *Evaluator) LayoutIn(t *Type, b *eval.Budget) (*eval.Type,
	error,
) {
	return ev.layout(t, b)
}

// Cond evaluates a condition charging b: BOOLEAN, or the "??" of BIT
// and STD_ULOGIC ('1' and 'H' are true); nil is true.
func (ev *Evaluator) Cond(e *Expr, b *eval.Budget) (bool, error) {
	return ev.cond(e, b)
}

// Truth is the condition value of v (see Cond).
func Truth(v eval.Value) bool { return truth(v) }

// Matches is the choice match of case? and select? (LRM 10.9).
func Matches(v, w eval.Value) bool { return matches(v, w) }

// ChoiceHit reports whether the case or selected-assignment choice c
// covers the selector value v.
func (ev *Evaluator) ChoiceHit(c *Expr, v eval.Value, matching bool,
	b *eval.Budget,
) (bool, error) {
	return ev.choiceHit(c, v, matching, b)
}

// Body returns the body of the subprogram x (x itself when it is one),
// nil when it has none (a built-in, or a missing body).
func (ev *Evaluator) Body(x *Decl) *Decl { return ev.body(x) }

// Actuals returns the actual of each parameter of x in the call e, in
// parameter order: the associated expression or the default value.
func Actuals(e *Expr, x *Decl) ([]*Expr, error) { return actuals(e, x) }

// ConstantIn evaluates the constant d charging b (deferred constants
// through their package body).
func (ev *Evaluator) ConstantIn(d *Decl, b *eval.Budget) (eval.Value,
	error,
) {
	return ev.constant(d, b)
}

// dynRef returns the dynamic reference of x, counting the read.
func (ev *Evaluator) dynRef(x *Decl) (eval.Expr, bool) {
	if ev.dyn == nil || x == nil {
		return nil, false
	}
	r, ok := ev.dyn.Ref(x)
	if !ok {
		return nil, false
	}
	ev.sh.dynReads++
	return &eval.Load{Ref: r}, true
}

// dynLower offers e to the dynamic context.
func (ev *Evaluator) dynLower(e *Expr, b *eval.Budget) (eval.Expr, bool,
	error,
) {
	if ev.dyn == nil {
		return nil, false, nil
	}
	x, ok, err := ev.dyn.Lower(ev, e, b)
	if ok {
		ev.sh.dynReads++
	}
	return x, ok, err
}
