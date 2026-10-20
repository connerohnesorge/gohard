// Package psl runs the PSL verification directives of an elaborated
// design in the simulator (#35, #11): each directive is one process
// (Wrap plugs it into elab.Options.Code before the sequential
// interpreter) that waits on its clock and, at every tick, samples the
// directive's booleans through the shared evaluator.
//
//   - A clocked simple-subset assert is checked by formula progression
//     (prop.go); a failure is an assertion report of the directive's
//     severity (default error) at the tick, a trace event tied to the
//     directive's source line. Strong obligations still open when the
//     run ends fail at the end time.
//   - A cover matches its sequence online by Brzozowski derivatives
//     (sere.go): every hit is a cover-hit trace event; at the end of
//     the run the cover reports sim/cover-hit (a hint, with the count)
//     or sim/cover-miss (a warning, and a cover-miss trace event).
//   - assume, restrict and fairness do nothing in simulation.
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
// cover miss with identical traces at every seed.
package psl
