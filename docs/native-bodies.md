# Profiling note: the 20% native-body rule (#35, #59)

Every `std`/`ieee` package body runs interpreted from the vendored
source. A package may get a native Go body only when it takes more than
20% of kernel time on a corpus design (or the NEORV32 testbench).
`std_logic_1164` and `numeric_std` passed the rule in 2026-10 and have
native bodies since #59; every other package stays interpreted.

## How it is measured

There are two measures. Both attribute time or steps to the design unit
that declares the running function. Nested calls count for the callee's
package, not the caller's.

- **CPU time**, which is what the rule means. `BenchmarkSimIEEE` in
  `internal/corpus` runs each corpus design, and the NEORV32 testbench
  (`neorv32`, 2 us of simulated time) when `testdata/neorv32` is checked
  out. While it runs, a `sema.Profile` hook labels the goroutine with
  the package that is running (pprof label `unit`). `-` is the
  simulation outside calls: the kernel and the interpreter's own
  statements. Calls the evaluator interprets, calls the interpreter
  runs itself (functions with a signal parameter, such as
  `rising_edge`) and native bodies are all labelled with their
  package. `scripts/ci/bench.sh` profiles each design for 2 s and
  records `go tool pprof -tags`. Analysis carries no label, so the
  labelled samples are the elaboration and simulation time: the kernel
  time. `-corpus.interpreted` runs the same benchmark with every ieee
  body interpreted, so one test binary measures both sides.
- **Evaluation steps**, a deterministic record. `sema.Profile`, attached
  to the run's step budget, sums the steps charged while each package
  runs. `TestIEEEShare` logs the shares and checks that two runs give
  the same profile. The nightly job copies them into the record.
- The two measures can disagree. A memoised pure call is charged its
  full step count but costs almost no CPU, so steps overstate packages
  whose calls hit the memo (`numeric_std` in `budget_hang`: 75% of
  steps, 2% of CPU). A native body charges its own step count (below).
  Only the CPU shares decide.

## The native bodies (#59)

`internal/ieeenative` holds them, keyed by package, function name and
the classes of the parameters and result:

- `std_logic_1164`: `resolved`; `and`, `nand`, `or`, `nor`, `xor`,
  `xnor` on two `STD_ULOGIC` values, on two vectors and as reductions
  of one vector; `not` of a value and of a vector; `To_X01`,
  `To_X01Z` and `To_UX01` of `STD_ULOGIC`, of vectors, of `BIT` and of
  `BIT_VECTOR`; `TO_01`; `rising_edge` and `falling_edge`.
- `numeric_std`: `+` and `-`, and `=`, `/=`, `<`, `<=`, `>`, `>=`, on
  two `UNSIGNED` or two `SIGNED` values and with a `NATURAL` or
  `INTEGER`; `RESIZE`, `TO_INTEGER`, `TO_UNSIGNED`, `TO_SIGNED` and
  `TO_01` of `UNSIGNED` and `SIGNED`.

A native body returns the value of the interpreted body, index range
and subtype included. It covers the common case and declines the rest,
which then runs from source as before: a `numeric_std` operand with a
metavalue or a null operand (the body warns), a conversion that
truncates, vectors of different lengths, a result outside its subtype.
So every assertion keeps its message and its source line.

Where they run: sema's evaluator calls them for pure function calls
when `Evaluator.SetNativeBodies` is on. Elaboration and simulation turn
it on (`hier.Options.NativeBodies`); analysis and the editor's static
evaluation do not. The resolver of `std_logic` nets folds the
resolution table over the drivers without building the array
argument. The interpreter answers `rising_edge` and `falling_edge`
from the signal's `'EVENT`, value and `'LAST_VALUE`, in the body's
short-circuit order. `elab.Options.InterpretedBodies` runs every body
from source.

A native call charges one step plus one per element of its arguments,
which is fewer steps than the interpreted body. Native bodies change
step counts, never a value or a trace: a run that ends on
`simulation.step_budget` stops later with them.

The checks:

- `TestNativeBodiesCorpus` (`internal/corpus`): every run of every
  corpus design, at seeds 0-3 on pull requests and 0-63 nightly
  (`corpus-seeds.sh`), gives the same trace bytes, diagnostics, exit
  code and status with native and with interpreted bodies.
- `TestNativeBodies` (`internal/sema`): on generated arguments (edge
  lengths and values first, metavalues, index ranges of both
  directions), every native body either declines or returns the
  interpreted body's value on a call the body neither fails nor warns
  on, and an evaluator with native bodies returns the same values,
  errors and reports as one without. 91 bodies, 300 calls each, under
  VHDL-2008 and VHDL-2019.
- `TestNumericStdNativeAgainstBig` (`internal/elab/ieeeref`): the
  `math/big` model of `numeric_std` against the native bodies.
- `TestNativeResolver` and `TestNativeEdgesAndResolution`
  (`internal/elab`): the resolver on every list of up to three drivers
  and 500 longer ones, and the edge functions and a two-driver net
  through all 81 `STD_ULOGIC` transitions at seeds 0-3.

## Measurement after #59 (2026-10-27, seed 0, i7-11800H)

Share of kernel CPU time, interpreted (`-corpus.interpreted`) and
native, from one test binary. Samples are 10 ms, so small shares are
rough. The interpreted column is higher than the first measurement
below because `rising_edge` and `falling_edge` now count for
`std_logic_1164`.

| design | numeric_std interpreted | native | std_logic_1164 interpreted | native |
| --- | ---: | ---: | ---: | ---: |
| assert_fail | **39.5%** | 3.8% | **34.2%** | 3.8% |
| budget_hang | 2.5% | 1.8% | **29.9%** | 9.2% |
| force_ext | 2.1% | - | **42.6%** | 4.7% |
| gc_heap | - | - | 3.5% | - |
| hier_generate | - | - | **39.3%** | 7.7% |
| md_resolve | - | - | **34.1%** | 2.5% |
| mode_views | 7.8% | - | **35.9%** | 7.0% |
| overload_ops | - | - | - | - |
| postponed_attrs | - | - | 3.0% | 4.4% |
| prot_cond | - | - | - | - |
| psl_simple | - | - | **25.5%** | 8.0% |
| standin_model | - | - | **48.9%** | 4.2% |
| textio_env | - | - | **27.7%** | 11.1% |
| vhdl93_only | - | - | **36.4%** | - |
| wait_forms | - | - | 10.9% | 1.9% |
| neorv32 | 2.5% | 1.1% | 10.0% | 1.3% |

Throughput, same machine at a load average under 3:

| run | interpreted | native |
| --- | ---: | ---: |
| `BenchmarkSimNEORV32` (first 2 us), cycles/s | 19.4-20.0 | 22.3-23.5 |
| `gohard check --sim --tb neorv32_tb`, 1 us to 10 us, cycles/s | 28 | 33 |
| `BenchmarkSim/budget_hang`, simulated us per wall second | 244 | 394 |
| `BenchmarkSim/budget_hang`, steps | 21,266,676 | 4,700,078 |
| `BenchmarkSim/assert_fail`, simulated us per wall second | 33 | 162 |

The `gohard check` row is the rate between a 1 us and a 10 us run (the
#59 method; `main` before #59 against this change, which also writes
the 94 MB trace the older build dropped). The other corpus designs
simulate for microseconds of wall time, so their run time is mostly
analysis; `BenchmarkSim` records them nightly.

## Reading

- With native bodies, no ieee package is over 20% on any corpus design
  or on NEORV32. What `std_logic_1164` keeps is mostly the call itself
  (lowering the call, the profile's label switch) and the calls that
  decline.
- NEORV32 gains 15 to 18%: the ieee packages were 12.5% of its kernel
  time. The rest is the kernel and the sequential interpreter. In the
  native CPU profile of `BenchmarkSimNEORV32` the kernel's process
  wake-up (`Kernel.wake`, mostly sorting the woken processes) is about
  a third of the simulation goroutine's time, garbage collection
  about 35% of all CPU samples (on other cores), and the interpreter
  (`machine.run`, with the lowering of each expression before it is
  evaluated) about 22% of all samples.

## First measurement (2026-10-19, seed 0, i7-11800H)

Share of kernel CPU time. Samples are 10 ms, so small shares are rough.
Functions the interpreter runs itself (`rising_edge`) counted as `-`.

| design | numeric_std | std_logic_1164 |
| --- | ---: | ---: |
| assert_fail | **49.6%** | **23.9%** |
| budget_hang | 2.3% | 10.7% |
| force_ext | 3.4% | **34.8%** |
| hier_generate | - | **47.5%** |
| md_resolve | - | **30.5%** |
| mode_views | 13.3% | **25.5%** |
| overload_ops | - | - |
| postponed_attrs | - | 7.8% |
| psl_simple | - | **20.5%** |
| standin_model | - | **26.5%** |
| textio_env | - | **26.3%** |
| vhdl93_only | - | **28.9%** |
| wait_forms | - | 2.9% |

No other ieee package showed up. `std_logic_1164` was over 20% on 9 of
the 13 designs and `numeric_std` on `assert_fail`, so both qualified.
