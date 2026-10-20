// Package sim is GoHard's simulation kernel (#35): a pure,
// single-threaded discrete-event machine implementing the simulation
// cycle of IEEE 1076-2008 section 14.7.
//
// A Model (built by internal/elab, or by hand in tests) declares the
// signals, the processes and the hierarchy scopes of one elaborated
// design. New builds a Kernel from it; Run advances the kernel by a
// bounded slice of simulation cycles, so a caller (the CLI, the
// language server loop) can cancel between slices. Process behaviour
// is behind the Code interface: the sequential interpreter, stand-in
// models, PSL directives and the tests' tiny process language all
// plug in there, and reach the kernel only through a Ctx.
//
// # Semantics
//
//   - Every scalar subelement of a signal is one net element. A port
//     associated with a signal (or a part of one) without conversion is
//     collapsed onto the actual's net elements (Signal.Of), so ports
//     add no delta and drivers on both sides of the port take part in
//     one resolution.
//   - Drivers hold the current value and the projected output waveform
//     of LRM 10.5.2.2: transport deletes the old transactions at or
//     after the first new one; inertial (with an optional reject
//     limit, the first delay by default) also applies the marking
//     rules of the pulse rejection window.
//   - A resolved net element calls its Resolver on the current values
//     of all of its drivers, in driver creation order (the Resolver
//     must not depend on that order; the tests check that the std_logic
//     table does not).
//   - The queue is ordered by (time, delta, process sequence): a cycle
//     at the current time is a delta cycle; within a delta, resumed
//     processes run in elaboration order, permuted by Options.Order
//     for a non-zero seed. Postponed processes run in the last delta of
//     a time step and may not schedule delta cycles.
//   - wait on/until/for and their combinations, with the timeout
//     counted from the start of the wait; a process with an until
//     clause is resumed only when Condition.Until is true.
//   - The implicit signals 'stable(T), 'quiet(T), 'transaction and
//     'delayed(T) are kernel-driven signals (Model.Implicit); 'event,
//     'active, 'last_event, 'last_active, 'last_value, 'driving and
//     'driving_value are Ctx queries.
//   - force and release (Ctx.Force, Ctx.Release) override the
//     effective value of net elements from the next delta on.
//
// # Budgets
//
// Simulated time beyond Options.TimeBudget, more than
// Options.DeltaBudget deltas at one time, or more than
// Options.StepBudget evaluation steps (elaboration's steps included,
// through Options.Budget) stop the run with sim/budget-exceeded; a
// delta loop is never a hang. A signal that has events within one time
// step but ends the step at the value it started with is a zero-width
// glitch (sim/glitch, once per signal).
//
// # Determinism
//
// The kernel has no goroutines, no maps whose order reaches output and
// no clock: the same model, options and seed give the same trace bytes
// and diagnostics. Within one delta the trace Recorder sorts changes by
// signal path, so a race-free design gives the same trace for every
// seed. Simulated time never decreases. Clone returns an independent
// copy of the whole kernel state (process code included), so a run can
// be forked and both copies continue identically.
package sim
