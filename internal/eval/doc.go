// Package eval is GoHard's one expression evaluator (#34, #35): the
// values of every VHDL type, the predefined operations and attributes,
// and an interpreter for a small typed expression IR, all counted in
// evaluation steps against a Budget (P7).
//
// Static evaluation in the editor and in `gohard check` (sema, #34),
// elaboration and the simulator (#35) all evaluate through this
// package, so the editor and the simulator always agree on a value.
// The editor runs with a fixed budget of EditorSteps (1,000,000)
// steps; elaboration and simulation pass their own budget
// (simulation.step_budget).
//
// The package knows nothing about names or the syntax tree. Sema lowers
// typed expressions to the IR (Expr); objects and user subprograms are
// reached through the Env interface, which sema's static evaluator and
// the simulator implement. Everything is a pure function of its
// inputs: no package state, no maps reach output, and the step count of
// an evaluation is a function of the expression and the values alone.
//
// Invariants (eval_test.go):
//
//   - Integer arithmetic either returns the exact mathematical result
//     or an overflow error (checked against math/big).
//   - Compare is a total order on the scalar values of one type and on
//     discrete arrays (lexicographic), consistent with Equal.
//   - Evaluating the same expression twice gives the same value and the
//     same step count; a budget of n steps fails exactly when the
//     evaluation needs more than n steps.
//   - Every scalar result is checked against its subtype: a value
//     outside the range is an Error, never a silent wrap.
package eval
