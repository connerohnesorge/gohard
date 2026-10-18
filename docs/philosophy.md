# GoHard development philosophy

GoHard is deterministic by construction. Every run of the server, the CLI
and the simulator is replayable from a seed.

- **P1 Determinism.** The same inputs and seed give byte-identical output.
  Output never depends on map iteration order, memory addresses,
  goroutine scheduling, wall-clock time, `LANG` or `COLUMNS`.
- **P2 Hermetic.** The outside world (filesystem, clock, environment,
  stdin/stdout, randomness) enters only through `internal/env`. Every
  door has a real and a simulated implementation.
- **P3 Pure step cores.** Parser, resolver, elaborator, simulator kernel
  and server core are pure functions or state machines over immutable
  snapshots. Effects happen at the edges.
- **P4 Invariants are the spec.** Each feature states its invariants and
  property or fuzz tests enforce them, starting from edge values.
- **P5 The corpus is the oracle.** `corpus.yaml` designs and committed
  GHDL/NVC hashes are the answer key. Every failure found by a generator
  is committed under `testdata/seeds/` with its seed.
- **P6 No silent failure.** Anything unsupported gives a diagnostic that
  names the construct. Injected faults are limited to those the protocol
  and OS really produce.
- **P7 Bounded work.** Parsing, analysis and simulation run under budgets
  (steps, deltas, simulated time) in cancellable slices. Hitting a budget
  is a diagnostic, never a hang.
- **P8 Measured performance.** Speed is checked by wall-clock benchmarks
  kept separate from determinism tests. Limits are written down.
