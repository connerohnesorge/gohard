# GoHard v1 architecture

Pure-Go 1.27 VHDL language server with a built-in pure-Go simulator.
Module `github.com/connerohnesorge/gohard`. One binary, `gohard`.
Rules from `docs/philosophy.md` (P1-P8) apply to every package.

## Commands

| Command | Purpose |
| --- | --- |
| `gohard lsp` | stdio LSP 3.17 server |
| `gohard lsp <action> --at path:line:col` | one LSP action, same code path, then exit |
| `gohard check [paths]` | parse + sema (+ `--simulate`), exit codes per #20 |
| `gohard fmt [--check] [paths]` | formatter |
| `gohard parse --dump file` | CST dump (golden format) |
| `gohard sim [--top unit] [--seed n]` | elaborate and run testbenches |
| `gohard trace export --format vcd\|fst\|ghw` | export the native trace |
| `gohard config check\|schema\|defaults` | config tooling |

Exit codes: 0 clean, 1 design problems, 2 tool/setup failure (wins over
1), 130 SIGINT, 143 SIGTERM. Full spec: issue #20.

## Packages (`internal/`)

Lower layers never import higher ones.

| Layer | Package | Role |
| --- | --- | --- |
| 0 | `env` | the only door to FS, clock, env vars, stdio, rand (real + sim) |
| 0 | `span` | byte offsets, ranges, line index, utf-8/16/32 position mapping |
| 0 | `diag` | diagnostic type, the single code/severity/exit table, renderers (text, JSON schema 1, SARIF 2.1.0) |
| 1 | `config` | `gohard.yaml` via yaml.Node; schema generation |
| 1 | `text` | immutable document text + incremental edits |
| 1 | `token` | token kinds, per-standard keyword tables, IsPSL |
| 2 | `lexer` | bytes + standard -> tokens with trivia; PSL context; 2019 directives as trivia |
| 2 | `cst` | green/red lossless tree; content-derived node IDs; generated kinds |
| 3 | `parser` | error-tolerant recursive descent; incremental reparse; standard gates from one rule table |
| 3 | `ast` | typed wrappers over the CST |
| 4 | `format` | formatter (`indent`, `keyword_case`, `align_ports`); never touches literals or extended identifiers |
| 4 | `stdlib` | bundled `std`/`ieee` sources per standard with integrity hashes |
| 4 | `workspace` | file discovery, library mapping, snapshots, on-disk cache |
| 5 | `sema` | scopes, name resolution, types, full overload resolution, static evaluation |
| 5 | `resolve` | queries over sema results (definition, references, hover data) |
| 6 | `elab` | lowers a resolved design into a compact model; instance paths like `tb.dut.u0` |
| 6 | `sim` | single-threaded discrete-event kernel; queue ordered by (time, delta, process seq); native IEEE packages; budgets; seeded `Rand` |
| 6 | `trace` | lossless native trace (9 values, deltas, types, drivers), canonical text form, VCD/FST/GHW export |
| 7 | `ide` | LSP features as pure functions of a snapshot |
| 7 | `jsonrpc2` | hand-rolled JSON-RPC 2.0 framing |
| 7 | `protocol` | LSP 3.17 types generated from metaModel.json |
| 8 | `server` | event-loop state machine (requests, edits, timers, sim slices are events) |

Tools: `internal/detlint` (bans `time.Now`, `math/rand`, `os.*` outside
`env`, ranging over maps into output) runs in CI.

## Data contracts

- Diagnostic: file (project-relative, `/`), 1-based line/col, byte span,
  severity, code (from the `diag` table), message, optional seed and
  simulated time. Text form `file:line:col: severity[code]: message`.
- CST node ID: hash of kind + children content. Never used as a source
  reference. Source references are file + byte span.
- Elaborated names = LSP hierarchical names = trace signal names.
- Traces are written to `.gohard/traces/`.

## Testing layout

- `corpus/<design>/` + `corpus.yaml`: oracle designs (issue #8).
- `testdata/cli/<case>/{cmd,want.exit,want.stdout,want.stderr}`.
- `testdata/lsp/<action>/<case>/`: `gohard lsp <action>` fixtures.
- `testdata/seeds/`: committed generator failures.
- `-update` flag regenerates goldens. Generated files have a CI
  staleness check. CI enforces `gofmt`, `golines -m 80`, `go vet`,
  `detlint`, and tests on Linux, macOS and Windows.
- GHDL/NVC (from the flake) are developer-only oracles. Their results are
  committed as hashes. CI only compares hashes.
