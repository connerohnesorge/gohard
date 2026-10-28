# Corpus limits

The remaining limits of the oracle corpus (#8). Each entry says where the
corpus is the only oracle or a semantic is covered only thinly, and what
would remove the limit. `corpus.yaml` and `README.md` hold the details.

## Designs without a full GHDL/NVC oracle

| Design | Trace oracle | Hierarchy oracle | Why |
| --- | --- | --- | --- |
| `mode_views` | NVC only | hand-checked `golden/hierarchy.txt` | GHDL 6.0.0 has no conditional analysis (every directive is "unknown tool directive"). With the directives stripped by hand GHDL runs it to the same 165 ns, but that copy is not the design. |
| `prot_cond` | NVC only | hand-checked `golden/hierarchy.txt` | GHDL 6.0.0 has no conditional analysis, no conditional `return ... when` and no `std.reflection`. |
| `ext_vars` | GHDL only | GHDL | NVC 1.23.0 rejects every method call through an external variable name ("protected type COUNTER_T has no method named GET"). |
| `force_out` | NVC only | hand-checked `golden/hierarchy.txt` | GHDL 6.0.0 parses no force mode (`force out`, `release out`). |

No design uses `oracle: "none, hand-checked"` for its trace. Each of the
four above has exactly one simulator behind it, so a bug of that
simulator in these designs would go into the golden unseen. One NVC bug
is already known: `access_value_mirror.get` returns a wrong value, so
`prot_cond` checks only `is_null` for the access reflection class. The
`mode_views` variant `define_narrow` has an NVC result only, like every
variant (no trace recording).

The three hand-checked hierarchies (`mode_views`, `prot_cond`,
`force_out`) were written from the VHDL. `mode_views` and `force_out`
agree with GoHard's hierarchy; `prot_cond`'s is compared only from
v1.1, since under v1.0 its units are opaque.

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
  initial value (`postponed_attrs`, and `delayed_attrs` for a vector
  and a resolved signal, LRM 16.2.3), and a port associated with a
  slice is dumped with the whole actual (`hier_generate`).
* Three more dump differences follow GHDL. GHDL does not dump a signal
  declared in a case generate alternative and NVC dumps it under the
  alternative label without the generate label, so `case_generate`'s
  `t` is compared with neither tool (the port it drives is). GHDL dumps
  nothing of the time step in which an assertion halts the run and NVC
  dumps its clock edge, so `psl_halt`'s `clk` is compared with GHDL
  only. `race_order`'s `order` is the race itself: GHDL runs the
  writers in reverse order, NVC and GoHard at seed 0 in declaration
  order, and it is compared with NVC only.
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
  `wait_forms`. Every design but `race_order` is race-free on purpose;
  byte-identical traces across seeds are the only check on their
  scheduling order. `race_order` (#63) exercises the #29 race warning
  with one race: eight processes of one simulation cycle writing a
  shared protected log. It reports `sim/race` at every seed after 0
  (`races_by_design`); which two writers the warning names depends on
  the seed. A race through a file has no design.
* **Conditional analysis.** Only in `mode_views`: one built-in identifier
  (`VHDL_VERSION`), one user identifier (`MV_PROFILE` from `defines:`
  in `mode_views/gohard.yaml`, and from `--define` in the variant
  `define_narrow`, #63, which makes the `` `elsif `` arm the active
  one), one inactive `` `else `` branch and one `` `error `` arm that is
  never taken. `prot_cond` adds `TOOL_NAME`. Only NVC can check either
  design.
* **Protected-type ports.** One design (`prot_cond`, v1.1), one port, one
  protected type, NVC only. Under v1.0 the design checks only that the
  unsupported-construct diagnostics appear.
* **PSL.** `psl_simple`: one clocked simple-subset `assert` and two
  `cover`s. Neither tool prints the cover miss during a run (GHDL's
  `--psl-report` lists it as "not covered"), so the miss warning's text
  and severity rest on #35 alone. `psl_simple` sets
  `simulation.stop_level: failure` so that all three tools run to
  100 ns. `psl_halt` (#63) covers GoHard halting on a PSL assert at the
  default `stop_level`: one clocked `req -> next ack` assert fails at
  65 ns after one cover hit, and GHDL (`--assert-level=error`) and NVC
  (`--exit-severity=error`) halt there too. `psl_comments` (#109) covers
  `psl.comments: true`: a `-- psl` default clock in the statement part,
  one `-- psl` assert that holds and one `-- psl` cover, each on one
  line. A directive continued on further `--` lines is covered only by
  the lexer and parser tests, and no design has a `-- psl` property or
  sequence declaration (NVC 1.23.0 rejects one in the statement part).
  GHDL and NVC continue a `-- psl` directive across a blank or non-comment
  line up to its `;`; GoHard ends it at the first line that does not
  start with `--`, so a design that relies on that fails in GoHard.
* **Stand-in models.** One design (`standin_model`), one opaque unit, one
  stand-in. Neither GHDL nor NVC marks modeled drivers, so the
  `modeled_drivers_marked` invariant has no tool oracle; the marking in
  the golden trace is hand-checked. The opaque unit is a synthetic
  IEEE 1735 envelope with dummy content, not real encrypted IP.
* **Generate hierarchy and per-instance errors.** One design. The outer
  if-generate branch is observable through a single odd-parity stimulus.
  `case_generate` (#63) adds one case generate: a single choice, a
  choice list, a range and `others`, one alternative with a signal of
  its own, each taken by one instance. In the
  `width_err` variant, GHDL reports both bad associations but without the
  instance path, and NVC 1.23.0 reports the first one and then crashes
  (SIGSEGV). So the expected instance path in the message has no tool
  oracle. GHDL's `--disp-tree` does not print if-generate alternative
  labels, which GoHard's hierarchy includes.
* **Overload resolution.** One package (`overload_ops`). It covers choice
  by parameter type, by full package-qualified name and by return type,
  and the wrong-`use`-clause case only by hand mutation.
* **External names and `force`/`release`.** `force_ext`: one absolute,
  one relative and one `^.` path, one external constant, and one
  force/release of a signal that is still being driven. `ext_vars`
  (#63): an external variable through an absolute path and through a
  package path (`@work.ext_vars_obj.total`), and a package-path
  constant; methods called through the names change the objects.
  `force_out` (#63): `force out`, `release out` and a force without a
  mode on a port of mode out. Not covered: a package-path signal, since
  GoHard does not simulate signals declared in a package yet
  (`unsupported/construct`, "evaluating ... in simulation is not
  supported yet") while GHDL and NVC run them; and `force in` at a port
  of mode out, which in NVC and GoHard gives the same values as
  `force out` at `force_out`'s actual and port, so the design does not
  tell the two modes apart.
* **`textio`.** Only `textio_env`: write, reopen and read of integers,
  strings, hex, `std_logic_vector` and time, plus one in-memory line and
  one `writeline(output)`, and (#63) the checked-in input file
  `textio_env_in.txt` (`inputs`): a comment line skipped, then three
  lines read with `hread` and `read` that drive the stimuli and the
  expected outputs. `std.textio.input` is never read.
* **VHDL-1993 gating.** `vhdl93_only` gates on reserved words
  (`protected` since 2002; `force`, `release` and `context` since 2008)
  and, since #63, on one semantic rule: the shared variable of type
  `natural` in `vhdl93_only_count.vhd`, which the variant `shared_2002`
  rejects (LRM 6.4.2.4). No other 1993-only semantic is covered, for
  example `std_logic_vector` as a type of its own rather than a subtype
  of `std_ulogic_vector`.
* **Signal attributes.** All of them appear in `postponed_attrs`, but
  `'delayed(t)`, `'stable(t)` and `'quiet(t)` are each checked at only a
  few times. `'delayed(t)` is pinned to its transport semantics by one
  short pulse and one same-time preemption (three delta transactions
  maturing at 75 ns give one event). `delayed_attrs` (#63) applies it
  to a 4-bit vector (whole-vector changes, one element moving alone and
  a 1 ns pulse on one element) and to a `std_logic` with two drivers
  (`'X'` while both drive), each checked between events.

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
  `assert_fail`, `psl_simple` and `psl_halt` are ordered by hand.
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
* A PSL assertion of severity error halts GHDL only under
  `--assert-level=error` and NVC only under `--exit-severity=error`,
  which `tools/oracle` passes for the default `stop_level` (`psl_halt`);
  the commands recorded for `psl_simple` have neither, and both tools
  run on to the finish.
* GHDL elaborates only the packages a design unit uses, so a package
  path names a package only when a `use` clause brings it in
  (`ext_vars_tb` uses `ext_vars_obj` for that).

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
