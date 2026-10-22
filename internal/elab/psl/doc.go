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
// declaration; rising_edge(s), falling_edge(s) and s'event and c are
// edges of s, any other boolean ticks when it holds after a change of
// one of its signals.
//
// Invariants (tests): the derivative matcher agrees with a direct
// reference matcher on random sequences and words; progression agrees
// with reference finite-trace semantics (weak operators hold when the
// trace ends first, strong ones fail) on random simple-subset
// properties; the obligations and states of a long run stay bounded;
// psl_simple gives exactly one failing assert, one cover hit and one
// cover miss with identical traces at every seed; on random words with
// ClearPslState at random ticks the PSL state agrees with a reference
// semantics after every tick, also across Kernel.Clone.
package psl
