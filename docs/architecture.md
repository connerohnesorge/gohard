# GoHard v1 architecture

GoHard is a pure-Go 1.27 VHDL language server with a built-in pure-Go
simulator: one binary, `gohard`, module
`github.com/connerohnesorge/gohard`. This file is the package contract.
The GitHub issues are the source of truth and win where they differ
(#4 holds the principles and settled decisions, #23 tracks v1.0). Every
package follows [philosophy.md](philosophy.md) (P1-P8).

## Data flow

```
bytes ─ lexer ─ tokens+trivia ─ (conditional analysis) ─ parser ─ CST
  CST ─ ast (typed views) ─ sema/resolve ─ elab ─ sim kernel ─ trace
                   │                                        │
                 ide (LSP features)           exports: VCD, FST, GHW
```

* Bytes come only through `env.FS`. Every stage below is a pure function
  of its inputs: the same bytes, config, standard, defines and seed give
  byte-identical diagnostics, trees, traces and LSP messages (P1).
* The server and the simulator are state machines:
  `Step(State, Event) -> (State, []Effect)` (P3). Requests, edits, timer
  fires, file results and simulation slices are events; sending, reading
  and scheduling are effects done by a thin shell. Queue order is time,
  then priority, then sequence number. Snapshots are immutable values with
  counter-based IDs; a result for an older snapshot is never published.
* Parallel work is limited to pure jobs (for example parsing the files of
  a snapshot, `-p N`) run through `env.Jobs`; results are stored per index
  and merged in canonical order, so `-p 1` and `-p 16` give identical
  bytes. If jobs panic, every runner re-raises the panic of the lowest
  panicking index (what `env.Sequential` raises), so a crash and its crash
  file never depend on `-p` or scheduling.
* There is one project-root rule, `config.FindRoot` (probing with
  `Lstat`, so a dangling `gohard.yaml` link is `config/unreadable`, exit
  2), and one config loader, `config.Load` plus `config.ApplyDefines`
  (via `cli.Invocation.LoadConfig`); every command and the server use
  them.
* A diagnostic with a path always has a position: file-level
  diagnostics sit at `1:1`, so text is always `file:line:col`.
* Every unbounded computation runs under a step, delta or simulated-time
  budget, never wall time (P7).

## Packages

Lower layers never import higher ones. `cmd/gohard` only builds the real
Env, maps SIGINT/SIGTERM to 130/143, and calls `cli.Run`.

| Layer | Package | Responsibility | Owner |
| --- | --- | --- | --- |
| 0 | `internal/env` | the `Env` value and its door interfaces; pure helpers (`Shuffle`, `SplitMix64`, `Map`) | #29 |
| 0 | `internal/env/realenv` | the OS doors; with `cmd/` the only code that touches the world | #29 |
| 0 | `internal/env/simenv` | seeded simulated doors with fault injection (P6) | #29 |
| 0 | `internal/span` | file IDs, byte spans, line index, UTF-8/16/32 positions | #20 |
| 0 | `internal/diag` | the diagnostic table, `Diagnostic`, sorting, exit codes, text/JSON/SARIF/JUnit renderers; `diag/junittest` checks JUnit XML against the committed `junit-10.xsd` | #20, #61 |
| 1 | `internal/config` | `gohard.yaml` via `yaml.Node`; project root; the key table that generates the schema and `docs/config.md` | #28 |
| 1 | `internal/text` | immutable document text and incremental edits | #25 |
| 1 | `internal/gen/vhdlgen` | seeded generator of small, legal, bounded VHDL designs (entities, generics, processes, waits, resolved signals) for `FuzzElab`, `FuzzSim` and property tests; a pure function of (seed, bytes) | #58 |
| 1 | `internal/token` | token kinds; per-standard keyword table (data); PSL flags | #31 |
| 2 | `internal/lexer` | bytes + standard -> tokens with trivia; PSL modes; conditional analysis as a token pass | #31 |
| 2 | `internal/cst` | lossless red/green tree, content-derived IDs, kinds generated from `kinds.yaml` | #31 |
| 3 | `internal/parser` | hand-written error-tolerant recursive descent; `rules.csv` standard gates | #31 |
| 3 | `internal/ast` | typed wrappers over the CST | #31 |
| 4 | `internal/format` | the formatter (#32 rules), whole file and range | #32 |
| 4 | `internal/stdlib` | vendored IEEE-P1076 `1076-2019` `std`/`ieee`, per-standard views, integrity hashes, prebuilt | #33 |
| 4 | `internal/workspace` | file discovery, library mapping, snapshots | #34 |
| 4 | `internal/importer` | `gohard import`: pure readers of ModelSim `.do`, Vivado `.xpr` and Quartus `.qsf` files (a Tcl word splitter that evaluates nothing, `encoding/xml`) giving each listed VHDL file with its library and standard, and the planner and writer of the resulting `gohard.yaml`; new formats add a row to `importer.Formats` | #2 |
| 5 | `internal/sema` | scopes, names, types, overloads, static evaluation (shared evaluator) | #34 |
| 4 | `internal/ieeenative` | native Go bodies of the hottest `std_logic_1164` and `numeric_std` functions, which sema's evaluator runs in simulation instead of the vendored source (see [native-bodies.md](native-bodies.md)) | #59 |
| 4 | `internal/libs` | `gohard libs detect`: the read-only search of vendor installs (Vivado, Quartus, Diamond), vendor variables, `PATH` and GHDL library indexes through `env.FS`/`Getenv`; the `gohard.yaml` edits shared with the "library not found" quick fix | #1 |
| 5 | `internal/resolve` | queries over sema results: definition, references, hover data | #34 |
| 5 | `internal/lint` | lint rules over a sema result, run by `gohard check` and the language server after `hier`; each unit's lint runs under a budget of 1,000,000 steps, past which the unit gets `sema/eval-limit` in place of its findings; `lint/clock-edge-style` looks at every unit, so when a unit over budget leaves it undecided, each other unit with a `'event` edge test gets `sema/eval-limit` for that rule | #54 |
| 5 | `internal/cache` | the on-disk analysis cache `.gohard/cache` (#37): whole-cache key, file format and versioning, recovery; sema stores and restores the entries (`sema.Cache`) | #37 |
| 6 | `internal/elab` | lowers a resolved design to a compact model; instance paths like `tb.dut.u0` | #35 |
| 6 | `internal/sim` | single-threaded discrete-event kernel ordered by (time, delta, process sequence) | #35 |
| 6 | `internal/trace` | lossless native trace, `Writer`/`Recorder` fed by the kernel, canonical text form (grammar in `doc.go`), value lookup; exports in `trace/vcd`, `trace/fst` and `trace/ghw` (pure Go, fixed bytes); `trace/waveform` is "Open waveform" | #36, #60 |
| 6 | `internal/coverage` | the coverage of a simulation run: the coverable statements of the design's units, the collector the run installs as its `sema.StmtHook`, lcov encode, parse and merge | #62 |
| 6 | `internal/repro` | repro files and session headers (`"schema":1`) | #29 |
| 7 | `internal/ide` | LSP features as pure functions of a snapshot | #25 |
| 7 | `internal/jsonrpc2` | hand-rolled JSON-RPC 2.0 framing; the only goroutines (stdio reader and writer) | #25 |
| 7 | `internal/protocol` | LSP 3.17 types generated from `metaModel.json` | #25 |
| 8 | `internal/server` | the LSP event-loop state machine | #25 |
| 8 | `internal/cli` | command dispatcher and commands, taking an `env.Env` | #20, #26 |

In S1, `workspace.Discover` walks the project root and the command-line
paths only. Folders listed under `libraries:` in `gohard.yaml` are checked
for existence (`library/folder-missing`) but not yet searched; a library
folder outside the root (`../ext`) is added to discovery by library
mapping (#34), which must also count those files before `check` reports
`cli/no-files`.

`elab`, `sim`, `trace` and `repro` are not in #4's package list; they are
the homes for #35, #36 and #29. Tools live under `tools/`: `tools/detlint`
(#30) and the developer-only `tools/oracle` (#8), plus generators.

## The env doors (#29)

`env.Env` fields: `Clock` (now, sleep, timers), `Rand`, `FS` (read, write,
temp+rename, remove, mkdir, stat/lstat, readlink, sorted readdir, streams
for textio), `Transport` (stdin, stdout, stderr), `Executor` (lookpath,
run, start; for `libs detect` and the waveform viewer), `Getenv`, `Jobs`
(the job runner) and `Order` (in-delta process order). The seed is a
`uint64`; seed 0 is declaration order and any other seed shuffles in-delta
order as a pure function of (seed, delta step).

`detlint` (a `go/analysis` analyzer, #30) reports in every package except
`internal/env/realenv` and `cmd/`: `time.Now`, `time.Sleep`, timers,
`math/rand`, `os.Getenv`, direct `os` file access, `net`, `os/exec`, `go`
and `select` statements, and map iteration whose order reaches output.
The one other exemption is `internal/jsonrpc2` (that package only, its
tests included), which may use `go` and `select` and nothing else
banned: its stdio reader and writer are the only goroutines in GoHard
(#4 P8, #25). The single-threaded server waits for input, timers and
cancellation through `jsonrpc2.Conn.Next`, so it needs no `select`.

## Diagnostics (#20)

`internal/diag/table.go` is the only definition of codes, default
severities (error, warning, hint), exit classes and message templates;
wording is locked when v1.0 ships. Codes are `area/kebab-name`:

| Area | Owner | Examples |
| --- | --- | --- |
| `cli/` | #20 | `cli/usage`, `cli/not-implemented`, `cli/missing-path`, `cli/no-files` |
| `config/` | #28 | `config/unknown-key`, `config/redundant-default`, `config/builtin-define` |
| `workspace/` | #20, #31 | `workspace/file-too-large`, `workspace/symlink-loop` |
| `syntax/` | #31 | `syntax/error`, `syntax/standard`, `syntax/undefined-condition` |
| `library/` | #34 | `library/unmapped`, `library/duplicate-unit`, `library/import-cycle` |
| `stdlib/` | #33 | `stdlib/integrity` |
| `sema/` | #34 | `sema/ambiguous-overload`, `sema/eval-limit`, `sema/unchecked-unit` |
| `lint/` | #54 | `lint/unused-signal`, `lint/sensitivity-list`, `lint/latch`, `lint/clock-edge-style` |
| `unsupported/` | #20 | `unsupported/construct` ("please report") |
| `format/` | #32 | `format/would-change` |
| `sim/` | #35, #29 | `sim/budget-exceeded`, `sim/assert-error`, `sim/race` |
| `import/` | #2 | `import/ignored`, `import/missing-file`, `import/standard` |
| `coverage/` | #62 | `coverage/invalid` |
| `repro/`, `trace/`, `lsp/`, `libs/`, `internal/` | #29, #36, #26, #1, #20 | `repro/source-changed`, `internal/crash` |

A `Diagnostic` has a project-relative `/` path, one-based line and
column (UTF-8 bytes, like `--at`), an exclusive end, severity, code,
message and related locations. Output is sorted by path, position,
severity, code and message.

Every legal construct GoHard does not implement is
`unsupported/construct` with the message "<construct> is not supported
yet, please report" (exit 2, the unit opaque): found during analysis
(`sema/unsupported.go`; today only verification unit bindings in
configurations), or met during elaboration or by a running process
(`sema.UnsupportedError`, for example `'PATH_NAME` of a declaration in
a generic package, named associations in aggregate targets, guarded
signal assignments, an implicit signal of a part of a signal, a PSL
`vunit` bound to an elaborated design entity); the run stops there.
Inside a `vunit`, `vmode` or `vprop` bound to `e(a)` (or `e`) the
names of `e` and `a` are visible, context clauses included (#110), so
`gohard check` analyzes it; only its simulation waits for #66. No
construct fails silently or with a generic "not
static" run-time error. The v1.0 variant naming the v1.1 milestone is
retired: v1.1 delivers every construct it named (#18, #27). Under a
standard before 2019 the VHDL-2019 constructs are not unsupported but
not allowed (`syntax/standard`, exit 1).

Exit codes: 0 clean; 1 design problems (errors, any warning with
`--strict`, unchecked units; hints never fail); 2 tool or setup failure or
an opaque unit, which wins over 1; 130 SIGINT; 143 SIGTERM. No other code
is ever returned. Output formats: text
(`file:line:col: severity[code]: message` plus
`summary: N errors, N warnings, N hints, N files`), JSON (`"schema":1`,
fixed key order: schema, status, exit, summary, diagnostics, incomplete,
error on exit 2) and SARIF 2.1.0 (`executionSuccessful: false` and
`toolExecutionNotifications` on exit 2). Diagnostics go to stdout, logs
to stderr; all output is buffered and written once; `--output FILE`
writes a temp file and renames it. `docs/ci.md` is the documented CI
example: it uploads SARIF only on exit 0 or 1, so an exit-2 run never
wipes existing GitHub alerts.

## Commands

| Command | Purpose | Owner |
| --- | --- | --- |
| `gohard check [PATH...]` | parse and analyze; `--sim`, `--tb`, `--seed N\|random`, `--repro FILE`, `--strict`, `--define`, `--allow-empty`, `--env NAME=VALUE`, `--real-env`, `--coverage` | #20, #34, #35, #29, #18, #62 |
| `gohard fmt [--check] [PATH...]` | format in place (temp file + rename) or check; `gohard fmt [--check] -` formats stdin to stdout with the `gohard.yaml` of the current directory's project | #32, #42 |
| `gohard parse --dump FILE` | stable tree dump | #31 |
| `gohard config check\|schema` | config diagnostics; JSON schema | #28 |
| `gohard lsp` | stdio LSP 3.17 server; `--record DIR` | #25 |
| `gohard lsp <action> --at PATH:LINE:COL` | one LSP action on the server's code path; `lsp sim` (with `--env`, `--real-env`), `lsp replay FILE` | #26, #18 |
| `gohard trace export --format vcd\|fst\|ghw` | export a native trace; see [Trace exports](#trace-exports) | #36, #60 |
| `gohard coverage merge FILE... [-o OUT]` | sum lcov coverage files; see [Coverage](#coverage) | #62 |
| `gohard import FILE` | write `gohard.yaml` from a ModelSim `.do`, Vivado `.xpr` or Quartus `.qsf` file (`--dir`, `--format`) | #2 |
| `gohard libs detect [--write]` | find vendor library sources | #1 |
| `gohard version` | prints `version`, `commit`, `ieee` (bundled IEEE tag and commit) and `go` lines; `unknown` when not known | #20, #39 |
| `gohard help [COMMAND]` | | #20 |

Global flags: `-p/--jobs N`, `--output FILE`, `-h/--help`. There is no
`gohard sim`: simulation is `gohard check --sim` (and `gohard lsp sim`).
Since v1.1, `--env NAME=VALUE` (repeatable) overrides
`simulation.env.vars` and `--real-env` lets the std.env natives read the
real clock and environment (#18); both imply `--sim`, and under
`--repro` both are ignored with a log line (the recorded environment
wins). The `simulation.env.*` keys (#28) take effect since v1.1. A command that is not built yet exits 2 with
`cli/not-implemented`.

### Trace exports

`gohard trace export --format FORMAT TRACE [-o OUT]` converts a native
trace (`.gohard/traces/<tb>-<seed>.trace`) to a viewer format. Every
format writes only the value each signal settles on at each time
(deltas and zero-width glitches do not appear), and the same trace
always gives the same bytes. An unknown format is
`trace/unknown-format`; a trace the format cannot hold is
`trace/invalid`.

| Format | Package | Keeps | Opens in |
| --- | --- | --- | --- |
| `vcd` | `trace/vcd` | four values: 0, 1, x, z (L and H read as 0 and 1; U, X, W and - as x); enumerations as strings | every waveform viewer |
| `fst` | `trace/fst` | all nine std_logic values; VHDL type names and scope kinds as attributes | GTKWave, Surfer; "Open waveform" uses it (#36) |
| `ghw` | `trace/ghw` | VHDL types: enumerations with their literals, all nine std_logic values, integer and physical ranges, arrays with their range, records as record signals | GTKWave, Surfer, GHDL's `ghwdump` (#60) |

GHW is GHDL's format. It has no place for an instance's design unit,
the modeled mark, events or delta cycles; a mode-view record port is an
inout port, a case-generate is an if-generate, and a for-generate whose
index is not an integer is a block. Names with a byte in 0-31 or
128-159, enumerations of more than 256 literals and array bounds beyond
32 bits cannot be written. The package documentation of `trace/ghw`
has the full mapping. "Open waveform" stays FST; GHW is CLI-only.

## Coverage

`gohard check --coverage` (it implies `--sim`) records the coverage
of every testbench it runs (#62) and writes it in lcov's tracefile
format: `.gohard/coverage/<tb>-<seed>.info` per run and
`.gohard/coverage/lcov.info`, the sum of the run's testbenches, with
one log line such as `gohard: coverage: .gohard/coverage/lcov.info: 35
of 36 lines (97.2%), 12 of 13 branches (92.3%)`. A run that stops on
a design error (an assertion, a budget) still writes the coverage of
what it ran; a run with a tool error writes neither its trace nor its
coverage. `gohard coverage merge FILE... [-o OUT]` sums lcov files of
several runs (seeds, testbench subsets) in sorted name order and
prints the same summary; it only
adds, so any order of the same files gives the same bytes. An
unreadable or invalid file is `coverage/invalid`. `SF` paths are
project-relative, so `genhtml --branch-coverage -o html
.gohard/coverage/lcov.info` runs from the project root.

* Coverable: the units of the elaborated design (the entity and
  architecture of every instance, the units they depend on and the
  bodies of those packages), without the bundled std/ieee and opaque
  units. In them every sequential statement of a process or
  subprogram, and every concurrent assignment, assertion and
  procedure call, is a line (`DA`). If and case statements and
  conditional and selected assignments (variable and signal,
  sequential and concurrent) are decisions (`BRDA`): one branch per
  alternative, an if statement's else (written or not) and a
  conditional assignment's "no condition held" included; the branches
  of a decision that never ran are `-`. A PSL `cover` directive is a
  line whose count is the number of `cover-hit` trace events its
  processes emitted: the #18 counters, not a second count. A line's
  count is the largest count of the statements and covers that start
  on it, so two covers on one line do not add up; the decisions of one
  line are numbered in source order.
* The hook: `sema.StmtHook`, installed with `elab.Design.SetStmtHook`
  after elaboration, is told each statement the interpreter runs
  (processes, concurrent statements, the subprograms they call) and
  each statement of the pure functions the evaluator interprets for
  them, plus the alternative every decision takes. It only observes;
  #68 builds on it. While a hook is installed, sema's memo of pure
  calls is off and the kernel calls every initial resolution
  (`sim.Options.Uncached`), so repeated calls and resolution functions
  count every call; both caches charge the steps of the calls they
  replace, so values, traces, diagnostics and step counts are the
  same with coverage on and off. `TestCorpus` checks that on every
  corpus run and `FuzzSim` on generated designs. With coverage off the
  interpreter and the evaluator test one nil hook per statement:
  `BenchmarkSim` stayed within noise (interleaved runs before and after
  the hook on `budget_hang`, `gc_heap` and `wait_forms`, medians -1.0%
  to +0.5%).
* Not counted: evaluation during elaboration (generic values,
  constants, the warm-up call of each resolution function), so a
  function only elaboration calls shows as not run; the native ieee
  bodies. The statements of an if or case generate alternative that
  was not chosen are coverable and show as not run. A PSL `assert`
  directive is not a line. The language server does not show
  coverage yet (the "Show coverage" command of #62's proposal).
* Determinism: counts are a function of the run (P1), so the files are
  byte-identical across reruns and at `-p 1` and `-p 16`
  (`testdata/cli/check-sim-coverage` and its neighbours,
  `TestCoverageReruns`); `gohard coverage merge` of the per-testbench
  files gives the bytes of `lcov.info`. Each seed's run has its own
  file, since a seed that changes the in-delta order of a racy design
  may change its counts.
* Answer key: every simulating corpus design has `golden/coverage.info`
  (seed 0, hash `coverage_sha256`, `tools/oracle` section `coverage`);
  those of `wait_forms`, `hier_generate` and `psl_simple` were checked
  by hand against the sources. `TestFormsCoverage` pins every statement
  form against hand-worked counts, and `TestGenhtml` (CI job `genhtml`)
  has lcov 2's `genhtml --branch-coverage` read the goldens without a
  warning.

## Files GoHard writes

* `.gohard/traces/<tb>-<seed>.trace` and `<tb>-<seed>.repro.json` (#29,
  #36); the folder is not configurable. Source paths in a repro header
  are clean slash paths relative to the project root; a file outside the
  root named on the command line keeps its `../` prefix, as the
  workspace reports it, and `Verify` resolves it against the root.
  Upper-case letters in `<tb>` are escaped like other unsafe bytes
  (`\Foo\` is `%5C%46oo%5C`), so names never collide on a
  case-insensitive file system.
  Since v1.1 a repro header also has `env` (the std.env epoch, the
  variables and `real`) and, when the design read project files,
  `inputs`: the sha256 of every file it read, or `""` for one it looked
  for and did not find, and, when it listed project directories
  (std.env `DIR_*`), `listings`: per directory (`.` the root) the
  sha256 of its sorted entry names (`env.ListingSHA256`, `.gohard`
  left out), or `""` for one that did not exist. `--repro` refuses a
  recording whose inputs or listings differ (`repro/source-changed`)
  or that was made with `--real-env`
  (`repro/non-reproducible`). A `--real-env` trace has the header line
  `reproducible no` after `end` (the magic stays `gohard-trace 1`), and
  `corpus`/`tools/oracle` refuse it as an answer key.
* `.gohard/run/<hash>/` holds the files a design wrote during a
  `gohard check --sim` run (#18), at their project-relative paths.
  `<hash>` is the first 16 hex digits of the sha256 of the run's
  identity (standard, testbench, seed, defines, budgets, models, the
  gohard.yaml and source hashes, the environment), without the gohard
  build, so a `--repro` by another version writes to the same folder.
  The writes stay in memory (`env.Overlay`) until the run ended,
  failed and cancelled runs included (files the design never closed
  are closed implicitly); the folder is then replaced whole, so it
  holds exactly that run's output. The race re-run and editor runs
  never write it.
* The design's file sandbox (#18): a design reads the project below the
  root (its own writes first) and writes only to the run folder. A name
  outside the root, inside `.gohard`, reached through a symbolic link
  that leaves the root or enters `.gohard`, or naming a directory, is
  `NAME_ERROR` at `FILE_OPEN` in every mode. Editor runs read the same
  project view (each file once per run) and drop their writes; `lsp
  replay` runs read no project file.
* `.gohard/coverage/<tb>-<seed>.info` (named like the trace) and
  `.gohard/coverage/lcov.info`, the sum of the run's testbenches, under
  `gohard check --coverage` (#62); the folder is not configurable.
* `.gohard/crash/<hash>.txt` on an internal crash (#20).
* `.gohard/cache/` under the project root: the on-disk analysis cache
  (#37, see below), written by `gohard check` and the language server
  (`gohard lsp` and `gohard lsp <action>`; not `lsp replay`). The
  folder is not configurable.
* `DIR/<n>.jsonl` for `gohard lsp --record DIR` (#29).

## Analysis cache (#37)

One cache folder per project root, `.gohard/cache/`, holds syntax
entries (`syntax/<key>`: a file's parse result) and semantic entries
(`sema/<key>`: what one unit job of sema produced, its declarations,
regions, references, typed expressions, statements and diagnostics),
plus `index`. It only ever changes speed, never results, and prints
nothing.

* The whole-cache key is the GoHard build (version, commit, and a
  SHA-256 of the executable's content from `cmd/gohard`, computed once
  when a command first opens the cache, so two builds with the same
  version, commit, size and time, as in the nix store, never share
  entries; an executable that cannot be read runs without the cache),
  the entry schema
  (`sema.CacheSchema`: the codec schema, the bundled sources, the
  entry format), the VHDL standard, the hash of the configuration sema
  sees (`config.Config.Identity`: every value and the position of
  every key the file set, since diagnostics point at key positions),
  `psl.comments` and the kinds.yaml hash (`cst.KindsHash`, from
  `tools/genkinds`). A mismatch, or any damaged file (each file
  carries the whole-cache key, its own key and a CRC-32C), throws the
  whole cache away; it is rebuilt silently. A folder that cannot be
  written means no cache. Files are written to a temp file and
  renamed; past 4096 entries of a kind, opening removes the older
  half, and a store keeps one entry per file and unit it writes.
* A syntax entry is keyed by the file content, the kinds.yaml hash,
  the standard, the psl settings and the conditional-analysis values.
  A restored tree is used only when it checks out on its own: it is
  consistent (`cst.Green.Consistent`: IDs, widths and kinds match the
  content), spells the file byte for byte and holds offsets inside
  it. Otherwise, or on any decoder panic (also of a semantic entry),
  the cache is thrown away.
* A semantic entry is keyed by the unit's node ID (its tokens, the
  trivia lengths and the line breaks, without comment text), the
  conditional-analysis values and step limit, the unit graph (every
  file, unit and library of the session, with levels and
  dependencies) and the keys of the units it depends on; the entry
  also lists the other units it read (readable lookups and the owners
  of objects it points at) with their keys, which must be unchanged
  for a hit. The soundness argument is the unit's node ID plus the
  keys of its dependency closure (`Unit.Deps`, recomputed every run):
  the tests prove those parts (dropping the node ID's tree or a
  dependency key makes `TestCacheSameLengthCode` and `TestCacheEdits`
  fail). The unit graph and the recorded reads are a second line of
  defence for reads outside the `Deps` closure; no known input needs
  them, and no test proves them necessary. An entry whose stored text
  holds comment text also needs
  the texts of its files unchanged (the text key). Hover and other
  queries read comments from the current tree, so they always see the
  current text.
* Entries are encoded with the bundle codec in a cache mode: objects of
  the bundle, of the session (files, units, libraries) and of other
  entries are references by owner and number, red nodes and tokens
  are paths from their file's root. Restored units go through the same
  phases across units as analyzed ones. Encoding runs in the unit's
  job, after its analysis.
* `testdata/cli` fixtures run with a cold, warm and corrupted cache at
  `-p 1` and `-p 16`; `TestCacheCorpus` and `TestCacheEdits` (sema)
  and `TestDiskCache` (ide) compare cached and uncached results on
  every corpus file.

## Incremental analysis (#53)

The language server analyzes the whole project on every edit and
passes the previous result as `sema.Input.Prev`; `internal/sema/incr.go`
reuses what the edit cannot change.

* P3 decision: the `File`, `Unit` and `Library` objects of a result,
  decoded or analyzed, are immutable once its analysis returns, so
  later results share them read-only. An analysis that reuses an
  earlier result writes only objects it made; every result is still a
  complete value of its own snapshot, and no result for an older
  snapshot is ever published. The peak RSS of the large project's warm
  session (`tools/bench`) may grow at most 1.5x.
* Reuse needs the same file paths, configuration, tool version and
  step limit, the same unit graph (libraries, `Deps`, levels,
  duplicates and cycles, built on fresh shells of every unit and
  compared) and no opaque, unchecked or modeled unit; anything else is
  a full analysis. The units of the edited files are dirty, and so,
  up to a fixed point, is every unit that depends on a dirty one:
  through `Deps`, a unit its job read, an architecture its late
  references looked up, a unit of the same file (a file's units share
  its `File`), or a library its `use L.all` names that holds a dirty
  unit. Clean units, their files and libraries are the previous
  result's objects; dirty units are restored from the on-disk cache or
  analyzed, and not stored (full analyses write the entries).
* The checks across units run on every unit every time and write
  per-session tables, never a unit. Static checks run per unit with
  the unit's own evaluator caches, so a unit's result depends only on
  the unit and what its evaluation read; a clean unit whose static
  reads (package bodies, deferred constants) are clean keeps its
  result.
* A body-only edit (an architecture or a package body) re-checks the
  edited file's units and the few units that point into them; an edit
  of a declaration other units see re-checks those units too.
* `TestIncrementalEdits` (sema) and `TestIncrementalAnalyzer` (ide)
  replay random edit sequences on the corpus and NEORV32 and compare
  every result with a full analysis of the same snapshot (sema:
  diagnostics byte for byte and the whole object graph, and the
  previous result unchanged). `FuzzLSPReplay` compares every file's
  diagnostics after an edit stream; its `sig-*` inputs change
  declarations that other files see.
* `TestIncrementalRules` (sema) has a small design in which each of
  these rules decides the result: a static read of a function body
  and of a deferred constant, a late architecture reference, `use
  work.all`, a unit of the same file, and a vunit, whose job writes
  its file's PSL expressions (#110). `TestIncrementalEdits` also
  replays short random sequences on it; both fail when any one of
  these rules is removed. The rule for a unit's job reads outside its
  `Deps` is defence in depth, as the on-disk cache's recorded reads
  are: no known input needs it.

## Language server (#25, #26)

`internal/server` is `Step(State, Event) -> (State, []Effect)` with a
queue ordered by virtual time, priority and sequence; each Step costs
one virtual millisecond, so the 5 s no-progress rule is a step bound.
`server.Driver` is the shell: it runs queued events one at a time and
performs effects through the env doors; before each step `Serve`
queues every client message already read, so a cancel or an edit
reaches running work at the next slice boundary. A `--record` session
stores each input with the core step it arrived at (plain JSON-RPC
lines for input that arrived while idle, `"$gohard"` entries
otherwise, and for bodies that are not JSON), so the output is a
function of the session file and the project files and `gohard lsp
replay` reproduces it byte for byte. The session also holds every
`gohard.yaml` read (a save reloads it), every reread of the project
files after the first load (a save of a VHDL file, or
`workspace/didChangeWatchedFiles`, which the server registers for
`**/*.vhd`, `**/*.vhdl` and `**/gohard.yaml` when the client supports
dynamic registration) and every request the
transport skipped unread (answered with InvalidRequest), and its
folder is the one the first initialize request names, wherever that
request is. Failing harness sessions embed
their project files and test analyzer, so a seed replays on its own.
Every response and publish is followed by the notification
`$/gohard/snapshot` with the snapshot ID. Features come from a
`server.Analyzer` (pure tasks over a whole-workspace `Snapshot`, byte
offsets, cross-file locations). `internal/ide` implements it on sema
and resolve (`ide.New`, wrapped by `server.WithFormatting`, is the
analyzer of `gohard lsp`): it analyzes each snapshot in memory in
bounded slices (plan, parse, analyze), reuses parse trees and results
across snapshots without changing slice counts or values, and its
`ide.Analysis` is the query layer of every feature. `ide` implements
the interface `server` defines, so it imports `server`; `server` never
imports it. `gohard lsp <action>` runs the action as an in-memory
session on the same code path (`server.RunAction`).

Navigation (hover, definition, references, rename) is `resolve` over
the snapshot's sema result. A declaration and its completions (body,
full constant, package body, and a subprogram's parameters and
generics in both) are one entity for references and rename. Bundled
std/ieee locations carry the core URI `gohard-bundle:///<path>` and
their own line index (`server.Location.Lines`). That URI never reaches
the client: the first outgoing message naming one makes the Driver
write the bundled files, read-only (0444) and checked against their
integrity hashes, to `$XDG_CACHE_HOME/gohard/bundle/<key>/` (falling
back to `~/.cache`, `%LocalAppData%` or `~/Library/Caches`;
`stdlib.CacheRoot`) and send their `file://` URIs, and every client
message naming a file there is mapped back to the bundle URI, so an
opened copy is the bundled unit, never a `work` file. When the cache
cannot be written the client gets one `window/showMessage` error and
bundled locations are left out. A rename is refused (the server
answers RequestFailed, `gohard lsp rename` exits 2) for bundled,
predefined and library names, declarations of opaque units, stand-ins
named like an opaque unit, and extended identifiers; each use site
keeps its letter case.

Semantic tokens (#55) come from `ide.Analysis.SemanticTokens`, a
pure function of the snapshot's analysis: the tokens of a whole
document as byte spans, sorted and not overlapping. The server
splits them per line and encodes them in the negotiated encoding
(`server.EncodeTokens`); a range result is the slice of the full
result that overlaps the range, and a delta is computed against the
last full or delta result of the document, which the State keeps
(result IDs are a counter, dropped on close), so a replay gives the
same IDs and edits.

Signature help and inlay hints (#56) are `resolve` queries, like
navigation.
`resolve.SignatureHelp` takes the overloads of a call from the
reference sema recorded at the call's name, the one hover and
definition answer from, so the three agree; a generic or port map
gives the formal list of its unit. `resolve.Hints` is a pure function
of the sema result and the kinds turned on (`inlay_hints.*` in the
snapshot's configuration): parameter names come from the formals sema
matched to each positional actual, types from the declarations, and
values from static evaluation with a fresh evaluator per declaration.
The values of one request share one budget of 1,000,000 steps (P7),
spent in source order from the start of the file, so a request costs
at most one editor evaluation however many constants the file has, and
a hint does not depend on the range asked for. Each hint sits on a
token boundary. With every kind off the analyzer answers without
waiting for the analysis. A change of the `inlay_hints.*` keys sends
`workspace/inlayHint/refresh` to a client that supports it.

## VHDL-2019 in v1.1 (#18, #27)

v1.1 is the release under test (`corpus.Release`); the corpus designs
`prot_cond` and `gc_heap` (milestone v1.1) run against their
`expected` outcome like the v1.0 designs. Under `standard: "2019"`
GoHard analyzes and simulates:

* 64-bit `INTEGER` (`integer'low` = -2\*\*63, as NVC; older standards
  keep 32 bits), conditional `return ... when`, inferred constraints,
  generic maps on subprogram calls, `'IMAGE` of composite types.
* Protected types: generic clauses (mapped when the variable is
  declared, `type x is new pt generic map (...)`, a header generic map;
  each object binds its own generics), arrays and records of protected
  types and pointers to them, protected types built from others,
  `private variable` and method aliases, protected-type and pointer
  parameters on functions, shared-variable ports.
* `'PATH_NAME`, `'INSTANCE_NAME` and `'SIMPLE_NAME` in simulation (LRM
  16.2.5), protected-object prefixes included (`:tb:sv:m[integer,bit]:`;
  an allocated object is named in the running process, `:tb:_p0::z`).
* std.env 2019: date and time (`simulation.env.epoch` plus simulated
  time, UTC), `GETENV` (`simulation.env.vars`, `--env`), `VHDL_VERSION`
  and `TOOL_*`, `DIR_*` (project-relative, sorted listings), call path
  and `FILE_*`, the VHDL assert counter and format API, and the PSL API
  (`PslAssertFailed`, `PslIsCovered`, `PslIsAssertCovered`,
  `Set/GetPslCoverAssert`, `ClearPslState`, an epoch per clear). PSL
  failures count like VHDL assertions (dropped when their severity is
  disabled, counted by `GetVhdlAssertCount`, in the status JSON
  `asserts` key), as in NVC 1.23. `std.reflection` is complete.
* Garbage collection of unreachable access-type objects (#27); it is not
  visible in VHDL and never changes a trace.

v1.1 changes to the v1.0 locked outputs are additive only: new
diagnostic codes, the `asserts` key and new trace header lines; the
trace magic stays `gohard-trace 1`. Each feature has a case under
`testdata/conformance/<feature>/` (CST round trip and simulator output,
with an NVC 1.23 recording where NVC supports it).

## Test data

| Path | Contents | Owner |
| --- | --- | --- |
| `testdata/cli/<case>/` | `cmd`, `want.exit`, `want.stdout`, `want.stderr`, optional `in/`, `stdin`, `want/`; run in-process at `-p 1` and `-p 16`, each with a cold, warm and corrupted analysis cache (see its README) | #20, #37 |
| `testdata/lsp/<action>/<case>/` | `gohard lsp <action>` fixtures | #26 |
| `testdata/parse/<rule>/` | one valid file with a tree dump, one invalid file with an error position | #31 |
| `testdata/corpus/<name>/` + `corpus.yaml` | the 22 oracle designs (#8, #27, #109, #63); goldens under `golden/` | #8 |
| `testdata/conformance/<feature>/` | the #18 VHDL-2019 cases: `<case>.vhd`, `.want`, optional `.nvc.txt` (see its README) | #18 |
| `testdata/fuzz/<Target>/` | saved inputs for `FuzzParse`, `FuzzFormat`, `FuzzLSPReplay`, `FuzzConfig`; `FuzzElab` and `FuzzSim` keep theirs under `internal/elab` and `internal/simrun` (#58) | #30 |
| `testdata/seeds/<pkg>/` | committed generator failures and LSP sessions, replayed by `go test` | #29 |
| `testdata/libs/` | pinned real libraries (OSVVM, UVVM, VUnit, regression sources) as submodules | #1, #8 |
| `testdata/stubs/` | CI-only vendor stub packages | #1 |
| `testdata/libsguide/` | the `docs/libraries.md` examples as projects CI checks | #1 |
| `testdata/synth/<rule>/` | the examples of the synthesizable-subset rule list ([synth-subset.md](synth-subset.md)): `top.vhd` and its `ghdl --synth` recording `ghdl.txt`; `neorv32/` holds the NEORV32 recordings; `record.sh` writes them all | #67 |

`-update` refreshes goldens. CI (#30) runs build, vet, test, `gofmt`,
`golines -m 80`, generated-file sync, `detlint`, fuzz-corpus replay and
every fixture on Linux, macOS and Windows, plus `go test -race` on
`jsonrpc2` and `server` on Linux. GHDL and NVC are developer-only oracles
in the Nix shell; CI compares their recorded hashes.
