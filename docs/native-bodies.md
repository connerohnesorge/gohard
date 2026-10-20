# Profiling note: the 20% native-body rule (#35)

Every `std`/`ieee` package body runs interpreted from the vendored
source. A package may get a native Go body only when it takes more than
20% of kernel time on a corpus design (or the NEORV32 testbench).

## How it is measured

There are two measures. Both attribute time or steps to the design unit
that declares the running subprogram. Nested calls count for the
callee's package, not the caller's.

- **CPU time**, which is what the rule means. `BenchmarkSimIEEE` in
  `internal/corpus` runs each v1.0 corpus design. While it runs, a
  `sema.Profile` hook labels the goroutine with the package that is
  running (pprof label `unit`). `-` means outside interpreted calls:
  the kernel and the interpreter's own statements. `scripts/ci/bench.sh`
  profiles each design for 2 s and records `go tool pprof -tags`.
  Analysis carries no label, so the labelled samples are the
  elaboration and simulation time: the kernel time.
- **Evaluation steps**, a deterministic record. `sema.Profile`, attached
  to the run's step budget, sums the steps charged while each package
  runs. `TestIEEEShare` logs the shares and checks that two runs give
  the same profile. The nightly job copies them into the record.
- The two measures can disagree. A memoised pure call is charged its
  full step count but costs almost no CPU, so steps overstate packages
  whose calls hit the memo (`numeric_std` in `budget_hang`: 75% of
  steps, 2% of CPU). Only the CPU shares decide.

## First measurement (2026-10-19, seed 0, i7-11800H)

Share of kernel CPU time. Samples are 10 ms, so small shares are rough.

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

No other ieee package shows up.

## Reading

- `std_logic_1164` is over 20% on 9 of the 13 designs. Its calls are
  the `resolved` resolution function of every `std_logic` signal and
  the edge and conversion functions (`rising_edge`, `falling_edge`,
  `To_X01`). The profile does not split the share by function.
- `numeric_std` is over 20% on `assert_fail`.
- By the rule, both packages qualify for native bodies. None exist yet.
  Adding one is a separate change, and it must give traces identical to
  the interpreted body on the whole corpus. For `numeric_std`, the
  `math/big` check in `internal/elab/ieeeref` is the reference. Every
  other package stays interpreted.
- The sequential interpreter was still changing when these numbers were
  taken. The nightly record is the current figure.
