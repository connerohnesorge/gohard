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
                 ide (LSP features)                exports: VCD, FST
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
| 0 | `internal/diag` | the diagnostic table, `Diagnostic`, sorting, exit codes, text/JSON/SARIF renderers | #20 |
| 1 | `internal/config` | `gohard.yaml` via `yaml.Node`; project root; the key table that generates the schema and `docs/config.md` | #28 |
| 1 | `internal/text` | immutable document text and incremental edits | #25 |
| 1 | `internal/token` | token kinds; per-standard keyword table (data); PSL flags | #31 |
| 2 | `internal/lexer` | bytes + standard -> tokens with trivia; PSL modes; conditional analysis as a token pass | #31 |
| 2 | `internal/cst` | lossless red/green tree, content-derived IDs, kinds generated from `kinds.yaml` | #31 |
| 3 | `internal/parser` | hand-written error-tolerant recursive descent; `rules.csv` standard gates | #31 |
| 3 | `internal/ast` | typed wrappers over the CST | #31 |
| 4 | `internal/format` | the formatter (#32 rules), whole file and range | #32 |
| 4 | `internal/stdlib` | vendored IEEE-P1076 `1076-2019` `std`/`ieee`, per-standard views, integrity hashes, prebuilt | #33 |
| 4 | `internal/workspace` | file discovery, library mapping, snapshots | #34 |
| 5 | `internal/sema` | scopes, names, types, overloads, static evaluation (shared evaluator) | #34 |
| 5 | `internal/resolve` | queries over sema results: definition, references, hover data | #34 |
| 6 | `internal/elab` | lowers a resolved design to a compact model; instance paths like `tb.dut.u0` | #35 |
| 6 | `internal/sim` | single-threaded discrete-event kernel ordered by (time, delta, process sequence) | #35 |
| 6 | `internal/trace` | lossless native trace, canonical text form, VCD/FST export | #36 |
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
| `unsupported/` | #20 | `unsupported/construct` ("planned for v1.1" / "please report") |
| `format/` | #32 | `format/would-change` |
| `sim/` | #35, #29 | `sim/budget-exceeded`, `sim/assert-error`, `sim/race` |
| `repro/`, `trace/`, `lsp/`, `libs/`, `internal/` | #29, #36, #26, #1, #20 | `repro/source-changed`, `internal/crash` |

A `Diagnostic` has a project-relative `/` path, one-based line and
column (UTF-8 bytes, like `--at`), an exclusive end, severity, code,
message and related locations. Output is sorted by path, position,
severity, code and message.

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
| `gohard check [PATH...]` | parse and analyze; `--sim`, `--tb`, `--seed N\|random`, `--repro FILE`, `--strict`, `--define`, `--allow-empty` | #20, #34, #35, #29 |
| `gohard fmt [--check] [PATH...]` | format in place (temp file + rename) or check | #32 |
| `gohard parse --dump FILE` | stable tree dump | #31 |
| `gohard config check\|schema` | config diagnostics; JSON schema | #28 |
| `gohard lsp` | stdio LSP 3.17 server; `--record DIR` | #25 |
| `gohard lsp <action> --at PATH:LINE:COL` | one LSP action on the server's code path; `lsp sim`, `lsp replay FILE` | #26 |
| `gohard trace export --format vcd\|fst` | export a native trace | #36 |
| `gohard libs detect [--write]` | find vendor library sources | #1 |
| `gohard version`, `gohard help [COMMAND]` | | #20 |

Global flags: `-p/--jobs N`, `--output FILE`, `-h/--help`. There is no
`gohard sim`; `--env` and `--real-env` are v1.1 (#18). A command that is
not built yet exits 2 with `cli/not-implemented`.

## Files GoHard writes

* `.gohard/traces/<tb>-<seed>.trace` and `<tb>-<seed>.repro.json` (#29,
  #36); the folder is not configurable. Source paths in a repro header
  are clean slash paths relative to the project root; a file outside the
  root named on the command line keeps its `../` prefix, as the
  workspace reports it, and `Verify` resolves it against the root.
  Upper-case letters in `<tb>` are escaped like other unsafe bytes
  (`\Foo\` is `%5C%46oo%5C`), so names never collide on a
  case-insensitive file system.
* `.gohard/crash/<hash>.txt` on an internal crash (#20).
* `DIR/<n>.jsonl` for `gohard lsp --record DIR` (#29).

## Test data

| Path | Contents | Owner |
| --- | --- | --- |
| `testdata/cli/<case>/` | `cmd`, `want.exit`, `want.stdout`, `want.stderr`, optional `in/`, `stdin`, `want/`; run in-process at `-p 1` and `-p 16` (see its README) | #20 |
| `testdata/lsp/<action>/<case>/` | `gohard lsp <action>` fixtures | #26 |
| `testdata/parse/<rule>/` | one valid file with a tree dump, one invalid file with an error position | #31 |
| `testdata/corpus/<name>/` + `corpus.yaml` | the 14 oracle designs; goldens under `golden/` | #8 |
| `testdata/fuzz/<Target>/` | saved inputs for `FuzzParse`, `FuzzFormat`, `FuzzLSPReplay`, `FuzzConfig` | #30 |
| `testdata/seeds/<pkg>/` | committed generator failures and LSP sessions, replayed by `go test` | #29 |
| `testdata/libs/` | pinned real libraries (OSVVM, UVVM, VUnit, regression sources) as submodules | #1, #8 |
| `testdata/stubs/` | CI-only vendor stub packages | #1 |

`-update` refreshes goldens. CI (#30) runs build, vet, test, `gofmt`,
`golines -m 80`, generated-file sync, `detlint`, fuzz-corpus replay and
every fixture on Linux, macOS and Windows, plus `go test -race` on
`jsonrpc2` and `server` on Linux. GHDL and NVC are developer-only oracles
in the Nix shell; CI compares their recorded hashes.
