# Corpus limits

The remaining limits of the oracle corpus (#8). Each entry says where the
corpus is the only oracle or a semantic is covered only thinly, and what
would remove the limit. `corpus.yaml` and `README.md` hold the details.

## Designs without a full GHDL/NVC oracle

| Design | Trace oracle | Hierarchy oracle | Why |
| --- | --- | --- | --- |
| `mode_views` | NVC only | hand-checked `golden/hierarchy.txt` | GHDL 6.0.0 has no conditional analysis (every directive is "unknown tool directive"). With the directives stripped by hand GHDL runs it to the same 165 ns, but that copy is not the design. |
| `prot_cond` | NVC only | hand-checked `golden/hierarchy.txt` | GHDL 6.0.0 has no conditional analysis, no conditional `return ... when` and no `std.reflection`. |

No design uses `oracle: "none, hand-checked"` for its trace. Each of the
two above has exactly one simulator behind it, so an NVC 1.23.0 bug in
these designs would go into the golden unseen. One NVC bug is already
known: `access_value_mirror.get` returns a wrong value, so `prot_cond`
checks only `is_null` for the access reflection class.

The two hand-checked hierarchies (`mode_views`, `prot_cond`) were
written from the VHDL. `mode_views` agrees with GoHard's hierarchy;
`prot_cond`'s is compared only from v1.1, since under v1.0 its units are
opaque.

## Trace oracle limits

* The trace oracle compares settled values per time only: VCD has no
  deltas, so delta order is checked only by the self-checking
  testbenches and by the seed check.
* GHDL dumps no records, strings or non-logic enumerations other than
  boolean; NVC dumps records but not strings or booleans inside them.
  Such signals are compared with whichever tool dumps them, or not at
  all.
* Two NVC 1.23.0 VCD differences are recorded as disagreements that
  follow GHDL: `d'delayed(5 ns)` starts at `'U'` instead of `d`'s
  initial value (`postponed_attrs`, LRM 16.2.3), and a port associated
  with a slice is dumped with the whole actual (`hier_generate`).
* `budget_hang`'s recordings keep the first 4096 settled changes; the
  remaining ~700 000 are covered by a digest only, so a late difference
  is reported without its line.
* GHDL's `--disp-tree` shows no generic values; generic values have an
  oracle only in the hand-checked hierarchies (none of which has
  generics) and in the designs' self-checks.

## Semantics covered thinly

* **Delta ordering.** Covered only by the four-stage chain and the
  one-delta swap in `md_resolve`, plus the delta glitches in
  `postponed_attrs` and the one-delta `wait for 0 ns` check in
  `wait_forms`. Every design is race-free on purpose, so the corpus never
  exercises the #29 race warning; byte-identical traces across seeds are
  the only check on scheduling order.
* **Conditional analysis.** Only in `mode_views`: one built-in identifier
  (`VHDL_VERSION`), one user identifier (`MV_PROFILE` from `defines:`
  in `mode_views/gohard.yaml`; `--define` is not exercised),
  one inactive `` `else `` branch, one `` `elsif `` arm and one
  `` `error `` arm that is never taken. `prot_cond` adds `TOOL_NAME`.
  Only NVC can check either design.
* **Protected-type ports.** One design (`prot_cond`, v1.1), one port, one
  protected type, NVC only. Under v1.0 the design checks only that the
  unsupported-construct diagnostics appear.
* **PSL.** One clocked simple-subset `assert` and two `cover`s. Neither
  tool prints the cover miss during a run (GHDL's `--psl-report` lists it
  as "not covered"), so the miss warning's text and severity rest on #35
  alone. `psl_simple` sets `simulation.stop_level: failure` so that all
  three tools run to 100 ns; GoHard halting on a PSL assert at the
  default `stop_level` is not covered by any design.
* **Stand-in models.** One design (`standin_model`), one opaque unit, one
  stand-in. Neither GHDL nor NVC marks modeled drivers, so the
  `modeled_drivers_marked` invariant has no tool oracle; the marking in
  the golden trace is hand-checked. The opaque unit is a synthetic
  IEEE 1735 envelope with dummy content, not real encrypted IP.
* **Generate hierarchy and per-instance errors.** One design. The outer
  if-generate branch is observable through a single odd-parity stimulus;
  `case generate` is not covered. In the
  `width_err` variant, GHDL reports both bad associations but without the
  instance path, and NVC 1.23.0 reports the first one and then crashes
  (SIGSEGV). So the expected instance path in the message has no tool
  oracle. GHDL's `--disp-tree` does not print if-generate alternative
  labels, which GoHard's hierarchy includes.
* **Overload resolution.** One package (`overload_ops`). It covers choice
  by parameter type, by full package-qualified name and by return type,
  and the wrong-`use`-clause case only by hand mutation.
* **External names and `force`/`release`.** Only `force_ext`: one
  absolute, one relative and one `^.` path, one external constant, and
  one force/release of a signal that is still being driven. Only signal
  and constant external names; no external variable, no package path
  (`@lib.pkg.obj`), no `out`-mode force.
* **`textio`.** Only `textio_env`: write, reopen and read of integers,
  strings, hex, `std_logic_vector` and time, plus one in-memory line and
  one `writeline(output)`. No read from an input file that is checked in.
* **VHDL-1993 gating.** `vhdl93_only` gates only on reserved words
  (`protected` since 2002; `force`, `release` and `context` since 2008).
  No other 1993-only semantic is covered.
* **Signal attributes.** All of them appear in `postponed_attrs`, but
  `'delayed(t)`, `'stable(t)` and `'quiet(t)` are each checked at only a
  few times. `'delayed(t)` is pinned to its transport semantics by one
  short pulse and one same-time preemption (three delta transactions
  maturing at 75 ns give one event); it is never applied to a vector or
  a resolved signal with several drivers.

## Expected outcomes still provisional

These outcomes come from reading the issues, not from a tool or a fixed
table:

* **`standin_model` exits 0 with one hint.** #20 now settles it: an
  `allow_opaque` unit with a `simulation.models` stand-in is checked
  through the stand-in, the run can exit 0, and the downgrade is a
  `sema/opaque-allowed` hint. The entry lists that hint.
* **Message order.** Expected lists follow #20's sorted (path, line)
  order. Only `prot_cond`'s `expected_v1_0` carries `path`/`line`, so
  that order is checked mechanically there; the runtime messages of
  `assert_fail` and `psl_simple` are ordered by hand.
* **Message wording.** The `budget_hang` text ("time budget of 1 ms"), the
  instance path in `hier_generate`'s `width_err` messages, and the names
  of `prot_cond`'s 11 v1.1 constructs are this corpus's wording. The
  `internal/diag` table (#20) must use the same strings, or the entries
  must change with it.
* **PSL severities.** Cover hit as a hint and cover miss as a warning
  with no time are a reading of #35.

## Tool behaviour `tools/oracle` must account for

* GHDL exits 0 after a severity-`error` assertion when the run ends in
  `std.env.finish` or reaches the end of events, and after a PSL
  assertion error. Pass/fail must be judged from the assertion messages,
  not from GHDL's exit code (`assert_fail` uses `--assert-level=error`).
* NVC warns about later-standard reserved words under `--std=1993`;
  GHDL and GoHard stay silent (`vhdl93_only` disagreement).

## Not done yet (S5 and later)

* `trace_sha256` is `pending` in every entry: no sequential interpreter
  is registered yet, so GoHard produces no trace and no
  `golden/trace.txt`. The corpus harness logs every simulating design
  as pending (and fails under `-corpus.require-sim`, as
  `scripts/ci/corpus-seeds.sh` runs it); the generated variants are
  compared up to elaboration only.
* The parser-regression expected-failure lists are recorded as the
  upstream files plus an extraction rule (comment above `regression:` in
  `corpus.yaml`); the PR parser test that applies the rule does not exist
  yet. NVC's `test/test_*.c` expectations also include semantic
  diagnostics, so for the parser gate a listed file only "may fail".

## Third-party sources

* **GRLIB** is pinned from the TUT-ASI automated mirror of Gaisler's
  release tarballs. The tree has no licence file, so the licence comes
  from source headers: 541 of 698 `lib` VHDL files say "GPL version 2";
  143 have no GPL v2 header (Altera megafunction output under Altera's
  proprietary licence, Micron/Cypress/GSI memory models with only a
  vendor copyright, and others). The entry therefore records
  `GPL-2.0-only AND LicenseRef-GRLIB-third-party`. The owner has to accept
  the mirror and these mixed terms, or the submodule is dropped.
* Nested submodules inside VUnit, PoC and Open Logic are not
  initialised. OSVVM is pinned on its own under `testdata/libs/osvvm`.
