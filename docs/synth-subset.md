# Synthesizable-subset rule list (#67)

This is the list of constructs GoHard will report in RTL code because
synthesis rejects them or builds hardware that does not behave like the
simulation. It is research for v3: the rules are implemented there as
the `synth/` diagnostics of #97, from real hardware inference, and this
list is their test list. #103 adds the checks that need elaboration.
v2 ships no code for it: nothing in `internal/`, no diagnostic codes.
Rule names below name the example directories in `testdata/synth/`;
they are not diagnostic codes. #97 names the codes and their
severities.

## Sources

- IEEE 1076.6-2004, the RTL synthesis subset. It is withdrawn and not
  freely available, so this list cites its clause numbers only and
  quotes nothing. Its clause 6 covers how storage and logic are
  modeled (6.1 edge-sensitive, 6.2 level-sensitive, 6.4 combinational),
  clause 7 covers pragmas (7.2 metacomments), and clause 8 walks the
  language: 8.n follows clause n of IEEE 1076-2002 (8.3 types, 8.4
  declarations, 8.5 specifications, 8.8 sequential statements, 8.9
  concurrent statements, 8.14 the predefined environment). The rules
  are written in GoHard's own words.
- GHDL's synthesis documentation, `testdata/ghdl/doc/using/Synthesis.rst`
  at GHDL v6.0.0, and GHDL 6.0.0 itself (`ghdl --synth`, in the
  devShell, pinned by `flake.lock`).
- Yosys has no VHDL front end of its own in the open-source build; it
  reads VHDL through ghdl-yosys-plugin, which converts the result of
  `ghdl --synth` (same document, "Yosys plugin"). So for VHDL, GHDL's
  answer is also Yosys's answer, and the Yosys/EQY harness of #94 will
  replay these recordings rather than add a second opinion.

## Recordings

Each rule has one or more short examples, each with its own top entity
`top` in `testdata/synth/<example>/top.vhd`; the example is named after
its rule unless the table names it. Examples whose name ends in `-ok`
are the accepted counterparts: code the rules must not report.
`testdata/synth/<example>/ghdl.txt` records the synthesis result: the
exact command, the GHDL version, the exit status, stderr, and stdout
(the netlist when synthesis succeeds). Exit 1 is a rejection with a
message; exit 2 is a GHDL internal error ("GHDL Bug occurred"), which
means GHDL synthesis can't judge the example cleanly. Every command is

```
ghdl --synth --std=08 top.vhd -e top
```

run in the example's directory. To record again (for example after a
GHDL update), run:

```
nix develop -c bash testdata/synth/record.sh
```

Two runs give byte-identical files. The script also writes the
NEORV32 recordings in `testdata/synth/neorv32/` (see
[NEORV32 verdicts](#neorv32-verdicts)).

## Scope

The rules apply to RTL only: the user's units, not the `std` and
`ieee` packages (the body of `rising_edge` reads `'last_value`). Four
scope rules come first. Without the synthesis-off, synthesis
elaboration and testbench rules the NEORV32 verdicts below fail;
without the elaboration-only rule common `clog2` and ROM helpers are
reported:

- **Synthesis-off regions.** Code between `-- rtl_synthesis off` and
  `-- rtl_synthesis on` (1076.6 7.2), or between the GHDL-documented
  pragmas `-- pragma|synopsys|synthesis (synthesis|translate)( |_)(on|off)`
  (for example `-- pragma translate_off`), is not checked. Example:
  `pragma-off-ok` (GHDL exit 0). GoHard does not track these regions
  today; the lexer treats them as comments.
- **Synthesis elaboration.** Checks that need elaboration (#103) start
  from a synthesis top, with synthesis-off regions removed before
  analysis. NEORV32 depends on this: `is_simulation_c` in
  `neorv32_package.vhd` is `false` for synthesis and `true` in
  simulation, because its `or true` sits in a translate-off region, and
  the `if is_simulation_c generate` bodies hold the simulation-only
  logging.
- **Elaboration-only code.** A subprogram whose every call is in a
  constant, generic, range or other static expression runs only while
  elaborating, and synthesis folds it to a value. Its declarations and
  statements are not checked by `time-now`, `access-type`, `file-io`,
  `real-signal` and `dynamic-loop`: a `clog2` helper with a `real` or
  `time` variable or a `while` loop, or a function that reads a ROM
  file, is fine. Examples: `file-init-ok` (a file and a `line`
  variable), `elab-function-ok` (`time` and `real` variables and
  `while` loops), both GHDL exit 0.
- **Testbenches.** Testbenches are not checked: entities listed in
  `simulation.testbenches`, entities without ports, and every unit
  reachable only through them. Ports alone are not enough: NEORV32's
  `sim/sim_uart_rx.vhd` has ports and uses `real` and file output, but
  only `sim/neorv32_tb.vhd` instantiates it.

## The rules

"Clause" is where 1076.6 covers the construct. "GHDL 6.0.0" is the
recorded result for the example. Kinds: **no hardware** (synthesis
can't build it), **differs** (synthesis builds something that does not
behave like the simulation), **cells** (synthesis builds storage
outside plain edge-triggered flip-flops).

| Rule | Clause | Kind | GHDL 6.0.0 on the example |
| --- | --- | --- | --- |
| `wait-for` | 8.8.1 | no hardware | rejected: `expect wait condition` |
| `wait-forms` | 8.8.1, 6.1 | no hardware | rejected: `wait statement not allowed for synthesis` |
| `after-delay` | 8.8.4, 8.9.5 | differs | accepted; both delays dropped |
| `multi-waveform` | 8.8.4 | differs | accepted; only the first element kept |
| `time-now` | 8.3.1.3, 8.14.2 | no hardware | rejected: `unhandled dyn operation: IIR_PREDEFINED_PHYSICAL_GREATER` |
| `sim-control` | none (VHDL-2008) | no hardware | internal error (exit 2) |
| `access-type` | 8.3.3 | no hardware | rejected: `variable with access type is not synthesizable`, then internal error (exit 2) |
| `file-io` | 8.3.4, 8.4.3, 8.14.3 | no hardware | `file-io`: same as `access-type` (the TEXTIO `line`); `file-io-binary`: internal error (exit 2) |
| `real-signal` | 8.3.1.4 | no hardware | rejected: `non-constant floating point operation not supported` |
| `shared-variable` | 8.4.3, 8.3.5 | no hardware | rejected: `protected type not supported` |
| `init-as-reset` | 8.4.3, 6.1 | differs | accepted; initial value kept on the register |
| `guarded` | 8.9.1, 8.4.3, 8.5.3 | differs | `guarded-block`: accepted, guard dropped; `guarded-signal`: internal error (exit 2) |
| `history-attr` | 8.14.1, 6.1 | no hardware | rejected: `last_value attribute not allowed`; `edge-stable`: `signal attribute not supported` |
| `edge-else` | 6.1 | differs | accepted; the edge becomes a data signal (`edge-else`, `edge-else-when`) |
| `two-edges` | 6.1 | cells | accepted; a `gate_mdff` dual-edge cell |
| `latch` | 6.2 | cells | rejected: `latch infered for net "q" (use --latches)` |
| `dynamic-loop` | 8.8.9 | no hardware | rejected: `loop condition must be static` |

Accepted counterparts, all GHDL exit 0: `wait-until-ok`,
`edge-event-ok`, `edge-when-ok`, `file-init-ok` (GHDL builds a ROM
from the file), `elab-function-ok`, `real-const-ok`, `static-loop-ok`,
`static-while-ok`, `pragma-off-ok`.

### wait-for

A `wait for` statement in an RTL process. Hardware has no timer to
wait on. Example: a process that waits 10 ns and samples its input.

### wait-forms

A process with wait statements must have exactly one, a
`wait until <clock edge>`, so that the process describes one
register stage. Reported: several waits in one process (an implicit
state machine), `wait on`, and `wait until` on a condition that is not
a clock edge. Example: two `wait until rising_edge(clk)` in one
process. Not reported: `wait-until-ok`, one edge wait at the top.

### after-delay

An `after` clause or a delay mechanism (`transport`, `inertial`,
`reject`) in a signal assignment, sequential or concurrent. Synthesis
ignores the delay, so the hardware changes at a different time than
the simulation. Example: `q <= d after 2 ns` in a clocked process and
`y <= transport not d after 5 ns`; the recorded netlist has
`n6 <= not wrap_d` and a plain register for `q`.

### multi-waveform

A signal assignment with more than one waveform element
(`p <= '1', '0' after 5 ns`). Synthesis keeps the first element: the
recorded netlist assigns `n6 <= '1'` on every edge, so the pulse is a
constant.

### time-now

A call of `now`, or a signal or variable of type `time` (or another
physical type), in RTL. Constants and generics of type `time` stay
allowed when they are folded while elaborating, for example to compute
a cycle count from a clock period, and so do variables in
elaboration-only subprograms (see [Scope](#scope); `elab-function-ok`
counts clock periods in a `time` variable). Example: a register that
stores `now` and a comparison against `100 ns`. GHDL rejects the
comparison of the stored time; it does not report the call of `now`
itself.

### sim-control

A call of `std.env.stop` or `std.env.finish` in RTL. These are
VHDL-2008 and newer than 1076.6, so no clause covers them. GHDL
synthesis can't judge the example: it stops with an internal error.

### access-type

An access type, an object of an access type, an allocator (`new`), or
`deallocate` in RTL. This includes `line` from TEXTIO. Not reported:
the same inside an elaboration-only subprogram (see [Scope](#scope)),
such as the `line` variable of `file-init-ok`. Example: a
process variable of type `access std_ulogic`. GHDL rejects the
variable, then stops with an internal error.

### file-io

A file type, a file object, or a TEXTIO call that runs after
elaboration in RTL, that is, outside an elaboration-only subprogram
(see [Scope](#scope)). Examples: `file-io` writes a TEXTIO log from a
clocked process (GHDL rejects the `line` variable first, then stops
with an internal error); `file-io-binary` writes a `file of bit` (GHDL
stops with an internal error: it can't judge it). Not reported:
`file-init-ok`, an impure function that reads a file only to compute a
constant while elaborating; GHDL accepts it and reports a ROM.

### real-signal

A signal, variable or port of type `real` (or another floating-point
type) in RTL. Not reported: `real-const-ok`, where `real` appears only
in constant expressions folded while elaborating (`ceil(log2(...))` for
a width), and `elab-function-ok`, where a `real` variable lives in an
elaboration-only `clog2` (see [Scope](#scope)).

### shared-variable

A shared variable outside a testbench. Under VHDL-2008 it has a
protected type; GHDL rejects the protected type. The VHDL-93 form
without a protected type, which vendor RAM templates use, is not
recorded here: `--std=08` rejects it at analysis.

### init-as-reset

A register (a signal assigned only under a clock edge) that has an
initial value in its declaration and no reset assignment. Synthesis
flows differ on initial values: many FPGA flows load them at
configuration, ASIC flows do not. GHDL keeps the value: the recorded
netlist declares the register as `signal n8 : ... := "0101"`. Signals
with an initial value and a reset assignment are not reported. This
rule needs inference (#97).

### guarded

A guarded block, a guarded signal assignment, a signal of kind
`register` or `bus`, or a disconnection specification. Examples:
`guarded-block`, where GHDL accepts the block but drops the guard (the
recorded netlist is `wrap_q <= wrap_d`, a wire instead of a register),
and `guarded-signal`, where GHDL stops with an internal error on the
disconnection specification.

### history-attr

A signal attribute that needs the signal's history in RTL:
`'last_value`, `'last_event`, `'last_active`, `'delayed`, `'stable`,
`'quiet`, `'transaction`, `'active`. Only `'event` is allowed, and only
in a clock-edge condition. Examples: `history-attr` (`'last_value` and
`'stable(5 ns)` in a concurrent assignment) and `edge-stable`
(`not clk'stable and clk = '1'`). 1076.6 6.1 also covers edge
spellings built from `'stable` (to confirm in the second reading, see
[Open items](#open-items)); GHDL rejects them, so this list reports
them too, and `rising_edge` is the fix. Not reported: `edge-event-ok`
(`clk'event and clk = '1'`).

### edge-else

A clock edge (`rising_edge`, `falling_edge`, or the `'event`
spelling) is an edge condition in three places: the condition of an
`if` or `elsif`, the condition of a `wait until`, and the condition
of a conditional signal assignment (`q <= d when rising_edge(clk);`,
the concurrent register form of 1076.6 6.1). Reported:

- an edge condition with a branch after the edge branch: an `else` or
  `elsif` after it in an `if`, or an `else` after it in a conditional
  assignment;
- a clock edge outside these three conditions: assigned to a signal
  or variable, passed as an actual, or as a `case` or
  selected-assignment expression.

Synthesis has no hardware for "not at the edge": GHDL accepts both
examples and turns the edge into a data signal (`edge-else`, an `if`
with `else`: `n2 <= '1' when rising_edge (wrap_clk) else '0'`, then a
mux; `edge-else-when`, `q <= d when rising_edge(clk) else '0'`:
the same shape). Not reported: `wait-until-ok` (`wait until
rising_edge(clk)`), `edge-when-ok` (`q <= d when rising_edge(clk);`,
a plain flip-flop in the netlist), `edge-event-ok`, and the
asynchronous-reset shape, where the edge is the last `elsif`.

### two-edges

One signal assigned on both edges of a clock, or on edges of two
clocks, in one process. GHDL accepts the example and builds a
`gate_mdff` cell, a flip-flop with two clocks that plain FPGA and ASIC
cell libraries don't have. Whether a downstream tool maps it is for
#94 to record.

### latch

A signal that keeps its value on some path through a process without
a clock edge: a level-sensitive latch. 1076.6 6.2 covers latches as a
modeling style, but an inferred latch is most often a missing
assignment, and GHDL rejects it unless `--latches` is given. Example:
`if en = '1' then q <= d; end if;` in a process sensitive to `en, d`.
This rule needs inference (#97). It supersedes `lint/latch` (#54) for
synthesized units once inference lands (#106 Q5).

### dynamic-loop

A loop whose iteration count depends on values that change at run
time: a `while` loop whose condition reads a signal or port, or a
variable that depends on one, or a `for` loop whose range is not
static after elaboration. GHDL unrolls loops and rejects one whose
condition is not a constant on some unrolled iteration. Example: count
trailing zeros with `while i < 8 and x(i) = '0'`. Not reported:
`static-loop-ok`, the same function as a `for` loop over a constant
range with an `exit` that reads the input; `static-while-ok`, a
`while i < W` loop in a process that sets `i` only from constants,
and a `clog2` helper with a `while` loop; and loops in
elaboration-only subprograms (see [Scope](#scope)).

## Left out

- **Incomplete sensitivity lists.** The code is inside the subset:
  synthesis ignores the list and builds the logic for every signal
  read, as GHDL does on `sensitivity` (exit 0, netlist
  `n2 <= wrap_a and wrap_b`). The simulation mismatch stays with
  `lint/sensitivity-list` (#54). NEORV32 has one,
  `rtl/core/neorv32_cpu_alu_bitmanip.vhd:336` in
  `internal/lint/testdata/survey.golden`, and GHDL synthesizes that
  configuration (`neorv32/serial-shift.txt`, exit 0), so a `synth/`
  rule for it would break the NEORV32 verdict.
- **Assertions and `report`.** Synthesis ignores them, or turns them
  into formal properties (GHDL's `--no-formal` and `--assume-asserts`).
- **Multiple drivers.** Tracked as #108 for every unit; resolved nets
  are left to #97, #103 and #106 Q5.
- **Three-state logic** (`'Z'`, 1076.6 6.3). Inside the subset.
- **Synthesis attributes** (1076.6 7.1), RAM and ROM modeling styles,
  vendor libraries and vendor IP. Vendor-specific, out of scope.
- **Not examined yet**, for #97 to record before it adds rules:
  postponed processes, VHDL-93 shared variables without a protected
  type, external names, `force` and `release`.

## NEORV32 verdicts

NEORV32 v1.13.6 (`testdata/neorv32`) synthesizes in practice. The
expected verdict for every rule is **no report** on the files in
`rtl/file_list_core.f`, and the rules are not applied to `sim/`
(testbenches and their models, see [Scope](#scope)).
`rtl/test_setups`, `rtl/system_integration` and `rtl/verilog` are out
of scope.

GHDL 6.0.0 synthesizes `rtl/file_list_core.f` with exit 0 in four
configurations, recorded in `testdata/synth/neorv32/`:

| Recording | Generics |
| --- | --- |
| `default.txt` | defaults |
| `full.txt` | every boolean feature generic on except `RISCV_ISA_E` and `RISCV_ISA_Zmmul`, plus PMP, HPM, GPIO, PWM, GPTMR and trigger counts |
| `rf-latch.txt` | `CPU_RF_ARCH_SEL=3` (latch register file) |
| `serial-shift.txt` | `RISCV_ISA_Zbb=true` with the serial shifter |

So no construct that GHDL rejects or stops on (the "rejected" and
"internal error" results above) is in the synthesized part of these
configurations, with the one GHDL miss described below. For the rules where GHDL accepts wrong hardware,
`neorv32/netlist.txt` counts the netlist shapes those examples
produce: an edge used as data (`edge-else`), `gate_mdff` (`two-edges`)
and an initialized register (`init-as-reset`). All are 0 in all four
configurations. `neorv32/scan.txt` is a text scan for the constructs:

| Construct | core, synthesis-off removed | core, synthesis-off kept | `sim/` |
| --- | ---: | ---: | ---: |
| `wait` | 0 | 0 | 15 |
| `after`, `transport`, `inertial` | 0 | 0 | 3 |
| `now`, `time` | 0 | 1 | 2 |
| access types, `new` | 0 | 0 | 1 |
| files, TEXTIO | 0 | 3 | 5 |
| `real` | 0 | 0 | 8 |
| `shared`, `protected` | 0 | 0 | 0 |
| signal with initial value | 0 | 0 | 21 |
| attributes of `history-attr` | 0 | 0 | 0 |
| `while` | 0 | 0 | 1 |

The scan also counts 4 lines for `stop`/`finish` and 1 for `bus` in
the core; all are names or strings (a signal named `stop` in
`neorv32_tracer.vhd`, "External bus timeout" in a report string), not
the constructs. The "synthesis-off kept" column shows why the scope
rules are needed: the core's TEXTIO logging in `neorv32_uart.vhd` and
`neorv32_cpu_trace.vhd` sits in translate-off regions, and its `line`
variables would also trip `access-type`.

One exception needs approval: with `CPU_RF_ARCH_SEL=3`, NEORV32 builds
its register file from latches on purpose
(`rtl/core/neorv32_cpu_regfile.vhd:211-218`), and `latch` reports
them. GHDL accepts that configuration without `--latches`
(`rf-latch.txt`, exit 0): the register file is one array signal written
slice by slice from 31 generated processes, and GHDL's `--out=vhdl`
netlist turns each slice into a mux that feeds back into itself instead
of reporting a latch. `lint/latch` already reports line 215
(`survey.golden`). The proposal is to keep the report: the latches are
real and the user asked for them, so a suppression is the right answer
there, and the default and full configurations stay clean.

## Open items

- Approval of this list by Conner (#67 "Done when").
- A second reading of the clause numbers against a copy of 1076.6-2004
  (P8: a second review of every LRM-derived table).
- #97 and #103 link here as their test list.
