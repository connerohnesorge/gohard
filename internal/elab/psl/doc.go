// Package psl runs the PSL verification directives of an elaborated
// design in the simulator (#35, #11): each directive is one process
// (Wrap plugs it into elab.Options.Code before the sequential
// interpreter) that waits on its clock and, at every tick, samples the
// directive's booleans through the shared evaluator.
//
//   - A clocked simple-subset assert is checked by formula progression
//     (prop.go); a failure is an assertion report of the directive's
//     severity (default error) at the tick, a trace event tied to the
//     directive's source line. A top-level always keeps checking after
//     a failure. Strong obligations still open when the run ends fail
//     at the end time. As in NVC 1.23 a PSL violation is an assertion
//     like a VHDL one: SetVhdlAssertEnable, SetVhdlAssertFormat and
//     the assert counters (GetVhdlAssertCount, the re-run lens, the
//     status JSON) apply to it.
//   - A cover matches its sequence online by Brzozowski derivatives
//     (sere.go): every hit is a cover-hit trace event; at the end of
//     the run the cover reports sim/cover-hit (a hint, with the count)
//     or sim/cover-miss (a warning, and a cover-miss trace event).
//   - The built-in functions prev, stable, rose and fell read their
//     argument at earlier ticks of the directive's clock, and onehot,
//     onehot0 and isunknown read it at the tick (builtin.go, #111).
//     The runner samples each argument at time 0 (its initial value,
//     as NVC 1.23 does) and at every tick before the booleans; before
//     the n-th sample prev(e, n) is the default value of e's subtype
//     (T'LEFT). The samples are values the design had: ClearPslState
//     keeps them. The samples of one call keep at most 2^20 values
//     (its depth times the size of one value of its argument): past
//     that the call is a run-time error at its first sample (P7). A
//     clock argument, ended and a built-in in a clock expression are
//     unsupported/construct, reported at time 0.
//   - A property or sequence instance is its declaration's body with
//     the formal parameters bound to the actuals, which are read where
//     the instance is written (#66): a boolean, sequence or property
//     formal used as an operand, and a const formal used as a count.
//     A formal inside a larger HDL expression and a recursive instance
//     (sema reports the recursion) are unsupported/construct. An
//     instance in parentheses is the instance, and an assert of a
//     property instance has its body's antecedent. next_event(b)[k](p)
//     holds when p holds at the k-th tick on which b holds, counting
//     the current one; next_event_a and next_event_e over [i to j] are
//     the conjunction and the disjunction of those (an infinite range
//     is unsupported/construct; a count below 1 or an empty range is a
//     run-time error at elaboration). forall, nested clocks and
//     infinite next_a and next_e ranges are unsupported/construct. A
//     directive that expands to more than maxCompile operators
//     (instances expanded, ranges counted per tick) is a run-time error
//     at elaboration, before it is built (P7).
//   - The directives of a PSL verification unit run as processes of
//     every instance the unit is bound to (elab, #66), named under the
//     instance path and the unit's name@library; their default clock
//     is the unit's own.
//   - assume, restrict and fairness do nothing in simulation.
//   - The std.env PSL API (#18) reads the kernel's PSL state
//     (sim.Ctx.PSL*): an enabled violation sets PslAssertFailed; a
//     cover is covered at its first hit, an assert once the left side
//     of its top-level implication held (or at its first tick when it
//     is not an implication), each counted once. ClearPslState
//     advances the kernel's epoch; a runner of an older epoch drops
//     its state (hits, obligations, matches, covered) before it goes
//     on, at its next tick or at the end of the run.
//   - Branching-time operators (AG, EF, A [.. U ..], ...) and directives
//     with no clock (none written, no default clock) report
//     sim/needs-formal once, at time 0, and do not run.
//
// The clock is the directive's @ clause or the innermost default clock
// declaration written before the directive (a `-- psl default clock` in
// the statement part clocks only the later directives, #109);
// rising_edge(s), falling_edge(s) and s'event and c are edges of s, any
// other boolean ticks when it holds after a change of one of its
// signals.
//
// Invariants (tests): the derivative matcher agrees with a direct
// reference matcher on random sequences and words; progression agrees
// with reference finite-trace semantics (weak operators hold when the
// trace ends first, strong ones fail) on random simple-subset
// properties; the obligations and states of a long run stay bounded;
// psl_simple gives exactly one failing assert, one cover hit and one
// cover miss with identical traces at every seed; on random words with
// ClearPslState at random ticks the PSL state agrees with a reference
// semantics after every tick, also across Kernel.Clone; on random
// properties written as VHDL text a directive fails exactly when the
// reference semantics says its property does not hold, and gives the
// same events in a vunit bound inline or by use vunit (also by an
// incremental binding, and next to a block named like the vunit), as a
// property instance (with formals, in parentheses) and with its
// sequences declared (#66); on random sample sequences every
// built-in's value agrees with a reference that indexes the whole
// sequence, also from a cloned state; the #111 directives fail at the
// ticks NVC 1.23.0 gives; a call over the sample bound fails at time 0
// and one just under it runs; a directive past maxCompile fails at
// elaboration and one with 2^8 copies of a property runs; a named
// property's antecedent is its body's.
package psl
