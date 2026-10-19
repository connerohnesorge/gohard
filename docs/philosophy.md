# GoHard development philosophy

GoHard is deterministic by construction: every run of the server, the CLI
and the simulator can be replayed from a seed and its input log. The
principles below are copied verbatim from the wayfinder map (#4), which is
their source of truth; every ticket cites them inline as (P1)…(P8).
Inspiration: Carl Sverre, "Hermetically Sealed Box of Pain"
(<https://youtu.be/L0p39TPUWVI>).

**P1. Deterministic by construction.**
For a given (seed, input log), `gohard` produces byte-identical output: diagnostics, LSP messages, formatted text, and simulation traces. This is a design property of every package, not a test category. If a run can't be replayed, nobody can shrink it, search it, or trust that a fix worked.

**P2. The world enters through named doors.**
The world is reached only through one `Env` value in `internal/env`. Its fields are the doors: `Clock` (wall clock and timers), `Rand`, `FS`, `Transport` (stdio JSON-RPC) and `Executor` (subprocess lookup and launch, used by `libs detect` and the waveform viewer), plus `getenv` and in-delta process order. The real implementation is `internal/env/realenv` and the simulated, seeded one is `internal/env/simenv`. A `detlint` vet analyzer enforces this in CI:
- in core packages, outside the `internal/env` real implementations and `cmd/`, no `time.Now`, `time.Sleep`, timers, `math/rand`, `os.Getenv`, direct `os` file access, `net` or `os/exec`
- no `go` statements or `select` in core packages
- no map-order-dependent output

**P3. Cores are pure state machines; shells are thin.**
The server and the simulator are `Step(State, Event) -> (State, []Effect)`. Requests, edits, timer fires, file results, and simulation slices are all events. Sending, reading, and scheduling are effects carried out by a thin shell. Each core runs on one event loop with a defined queue order: time, then priority, then sequence number. Parallel work is limited to pure jobs (such as parsing files in a snapshot) whose results are merged in canonical order. Snapshots are immutable values with counter-based IDs. A result computed for an older snapshot is never published. State is a copyable value, so forking and replay come cheap later.

**P4. Invariants are the specification.**
Every decision names the properties its code must satisfy, and a feature is done when those properties run in CI. Example tests are documentation and never replace a property. Each subsystem names its oracle: a small, obviously correct reference model or algebraic law, checked against the real code on generated inputs.

**P5. The corpus is the oracle, and every failure joins it.**
GoHard has no outside reference simulator. The self-checking designs and recorded outcomes in `corpus.yaml` are the end-to-end answer key, backed by per-subsystem reference models. Generators try edge values first. Every failure prints one replay seed. The seed and its minimized input are committed under `testdata/seeds/<pkg>/` and replayed by plain `go test`, permanently.

**P6. Faults are ordinary inputs.**
The simulated doors can fail at least as badly as the real ones, at every point the protocol and OS actually allow: short or failing reads, files vanishing mid-walk, cancels that race completion, stale versions, budget exhaustion. Frame-level transport chaos is out of scope because stdio is ordered and reliable. After any fault the outcome is clean: a correct result, a diagnostic, or a prompt cancellation. Never a hang, a panic, or a stale publish.

**P7. Budgets, not hope.**
Every unbounded computation runs under a deterministic budget counted in steps, deltas, or simulated time, never wall time: parsing, elaboration, simulation, and background work. Elaboration and simulation use the same evaluator, and elaboration evaluation steps count against `simulation.step_budget`; the editor's static evaluation has its own fixed 1,000,000-step limit. Running over the budget gives a diagnostic. Cancellation takes effect within a bounded number of steps. Wall time is only a secondary guard in the real shell.

**P8. Name what determinism can't see.**
Each limit gets its own practice:
- **Speed:** wall-clock benchmarks with budgets.
- **Real concurrency in the shell:** the server core is single-threaded and the only goroutines are the stdio reader and writer in `jsonrpc2`; CI runs `go test -race` on `jsonrpc2` and `server` with a real pipe transport on every pull request (Linux) (#11).
- **Liveness:** step-bounded workloads, which show bounds rather than proofs.
- **Shared misreadings of the LRM:** a second review of every LRM-derived table, and cross-checks between oracles.

No claim says "simulation covers it" without naming what it doesn't cover.
