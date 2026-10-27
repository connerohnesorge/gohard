# testdata/lsp fixtures

Language-server fixtures (#25, #26). Every case is a scripted client
session in and the exact server messages out. All of them run on
every pull request on Linux, macOS and Windows under plain `go test`.

| Path | Run by | What it checks |
| --- | --- | --- |
| `session/<case>/` | `TestLSPSessions` in `internal/server` | one scripted session against the server core |
| `record/<case>/` | `TestLSPRecordReplay` in `internal/cli` | `gohard lsp --record DIR` then `gohard lsp replay DIR/<n>.jsonl`, byte for byte |
| `<action>/<case>/` | `TestLSPActionFixtures` in `internal/cli` | `gohard lsp <action> --at PATH:LINE:COL` fixtures (#26) |

## `session/<case>/`

| File | Required | Meaning |
| --- | --- | --- |
| `client.jsonl` | yes | The client messages, one JSON-RPC body per line, in arrival order. Blank lines and lines starting with `#` are skipped; each line is compacted before it is sent. |
| `in/` | no | The project tree, placed at `/work` of a simulated Env (`internal/env/simenv`); the session's workspace folder is `file:///work`. |
| `analyzer` | no | `nop` (the default), `format` (`server.WithFormatting` over `nop`, the formatting of `gohard lsp`, #32) or `echo`, a test analyzer that makes the server's view visible: it publishes one hint per open document holding its version and text, hovers name the word and byte offset under the cursor, and definition finds `entity <word>` in any document of the snapshot (a cross-file query). |
| `want.jsonl` | yes | Every message the server sent, one body per line, byte for byte as written to the client (without the `Content-Length` framing). |
| `want.exit` | yes | The session's exit code and a newline: 0 after shutdown and exit, else 1. |

The harness builds the session file `gohard lsp --record` would write
(header from `server.SessionHeader`, then a `{"$gohard":"analyzer"}`
line when the case names one, a `{"$gohard":"file"}` line per `in/`
file, then the client messages) and runs
it through `server.Replay`, the same code as `gohard lsp replay`. Next
to the comparison it checks the server invariants on every case: each
request gets at most one response and each request sent before
shutdown exactly one; every response after the initialize result and
every `textDocument/publishDiagnostics` is followed by
`$/gohard/snapshot` naming it; snapshot IDs never decrease; `$/progress`
runs begin, report, end; no response is an internal error.

When a case fails, the harness writes its session to
`testdata/seeds/lsp/<case>.jsonl` and names the file, so the failure
can be replayed with `gohard lsp replay` and, once fixed, committed as
a permanent regression (#29, P5). The seed is self-contained: it
embeds the case's project files, which `server.Replay` uses instead of
the file system, and names its test analyzer (`TestSeeds` uses it;
`gohard lsp replay` notes it and uses the built-in analyzer).
`TestFailingSeedWithFiles` proves this on a case with `in/` files. `TestHarnessWritesFailingSession`
proves this on a deliberately failing case;
`testdata/seeds/lsp/harness-deliberate-failure.jsonl` is its committed
output. `TestSeeds` replays every committed session twice and checks
that both runs agree and keep the invariants; the nightly
`scripts/ci/lsp-seeds.sh` runs the same test.

### Cases

| Case | Shows |
| --- | --- |
| `initialize-utf8` | utf-8 is chosen when offered; positions count bytes |
| `initialize-utf16` | utf-16 is kept when utf-8 is not offered; the same byte offset |
| `incremental-edits` | ranged edits with surrogate pairs, several changes per message, clamping, full replacement, stale versions, edits to unopened documents, close clears diagnostics |
| `cancel` | a waiting request cancelled (`-32800`), an edit before a waiting request starts (`-32801`), cancels of unknown and answered requests ignored |
| `editor-override` | `gohard.yaml` wins over an editor setting, reported in `window/logMessage` (#28) |
| `config-diagnostics` | `gohard.yaml` problems published for `gohard.yaml` |
| `definition-cross-file` | a workspace query answered from a file that is not open |
| `lifecycle` | errors before initialize, a second initialize, invalid JSON-RPC, unknown methods, requests after shutdown |
| `exit-without-shutdown` | exit code 1 |
| `format-document` | whole-file formatting (#32) in a utf-16 session; formatted text gets no edits; a project file that is not open is formatted from disk; a file that does not parse and a document that is not VHDL get no edits |
| `format-range` | range formatting with the `format.*` keys of `gohard.yaml`: edits only inside whole-line and part-line ranges, none for an empty range, the new text after an edit |

## `<action>/<case>/`

Each case is one `gohard lsp <action>` run in the format of
`testdata/cli` (see `testdata/cli/README.md`): `cmd`, `in/`,
`want.exit`, `want.stdout` and `want.stderr`, run by the same harness
at `-p 1` and `-p 16`. The action sends its request through
`server.RunAction`, the server's own code path, and prints the LSP
result; positions are zero-based UTF-8.

| Case | Shows |
| --- | --- |
| `format/whole` | `gohard lsp format`: the whole-file edits |
| `format/clean` | a formatted file gets no edits |
| `format/not-parsed` | a file that does not parse gets no edits |
| `range-format/lines` | `gohard lsp range-format --at PATH:2:1 4:1`: lines 2 and 3 only, with `format.indent` and `format.keyword_case` from `gohard.yaml` |
| `range-format/partial-line` | a range inside one line: the indentation before it is left alone |
| `range-format/bad-end` | a range end that is not LINE:COL is a usage error (exit 2) |
| `diagnostics/cross-file` | `gohard lsp diagnostics --at PATH:1:1`: the syntax and semantic diagnostics published for a file using a package in another file |
| `diagnostics/unsupported-construct` | a construct GoHard does not implement gives the same `unsupported/construct` ("please report") diagnostic as `gohard check` (#25) |
| `completion/record-field` | after `p.` the fields of the record signal `p` |
| `completion/library-units` | after `work.` the primary units of the library and `all` |
| `completion/library-name` | library names (`ieee`) |
| `completion/keywords-and-names` | visible names and the keywords of the selected standard, filtered by the word before the cursor |
| `completion/overload-candidates` | inside an undecided call the candidates come first, each with the reason hover shows (#34) |
| `symbols/document` | `gohard lsp symbols`: the declarations of the file as a tree |
| `workspace-symbols/all` | `gohard lsp workspace-symbols --at PATH:1:1 [QUERY]`: every unit and package declaration of the project |
| `workspace-symbols/query` | only the names containing the query, case insensitive |
| `folding/blocks` | the context clause (`imports`), the architecture, the process (each to the line before its `end`) and a comment run |
| `highlight/signal` | the declaration (text), assignment targets (write) and reads of a signal in the file |
| `codeaction/add-use` | `gohard lsp codeaction`: one "add use" quick fix per bundled package declaring the undefined name |
| `codeaction/add-use-work` | a project package: `use work.util.all;` after the unit's last context clause |
| `codeaction/map-library` | an unmapped library: the `libraries` line written into `gohard.yaml`, with the folder declaring the unit (the search of `gohard libs detect`, #34) |
| `codeaction/map-library-new-yaml` | without `gohard.yaml` the fix adds a `libraries` block |
| `hover/doc-comment` | a signal: its declaration and doc comment |
| `hover/cross-library-call` | a call of a function of another library: the chosen overload, its package and doc comment |
| `hover/bundled-operator` | an operator: the chosen overload from ieee.numeric_std |
| `hover/record-field` | a record field with its doc comment |
| `hover/record-type` | a record type: its declaration with the element list, `from lib.pkg` and its doc comment |
| `hover/enum-type` | an enumeration type: its declaration with the literals, `from lib.pkg` and its doc comment |
| `hover/entity-doc` | a design unit: the doc comment above the entity, after the context clause |
| `hover/enum-literal` | an enumeration literal and the type it belongs to |
| `hover/ambiguous-overload` | an ambiguous call (#34): the diagnostic's message and every candidate with its reason, as in diagnostics/ambiguous-overload |
| `hover/no-overload` | a call no overload fits (#34): every candidate and why it does not fit, as in diagnostics/ambiguous-overload |
| `diagnostics/ambiguous-overload` | the overload diagnostics hover/ambiguous-overload and hover/no-overload show (#34) |
| `definition/use-clause` | a package named in a use clause, in another library |
| `definition/library-unit` | the entity of an entity instantiation, in another library |
| `definition/component` | the component of a component instantiation |
| `definition/port-map-formal` | a port map formal of an entity instantiation: the port in the other file |
| `definition/generic-map-formal` | a generic map formal of an entity instantiation |
| `definition/component-formal` | a port map formal of a component instantiation: the component's port |
| `definition/alias` | an alias used as an actual |
| `definition/selected-name` | the last name of a selected name lib.pkg.name |
| `definition/selected-prefix` | the package prefix of a selected name |
| `definition/record-field` | a record field in a selected name |
| `definition/enum-literal` | an enumeration literal |
| `definition/case-insensitive` | a name written in another letter case (INC) |
| `definition/label` | the loop label of an exit statement |
| `definition/end-label` | the label of an end process |
| `definition/subprogram-end` | the designator of an end function in a package body |
| `definition/bundled-type` | a std_logic_1164 type: the read-only cached copy of the bundled source (a file URI under `$BUNDLE`) |
| `definition/bundled-function` | to_integer: the numeric_std overload chosen, in the bundled source |
| `definition/extended-identifier` | an extended identifier |
| `definition/ambiguous-overload` | an ambiguous call (#34): every candidate that fits |
| `definition/no-overload` | a call no overload fits (#34): every candidate |
| `references/cross-library` | a function across libraries and files: declaration, body, end designator and calls |
| `references/record-field` | a record field: declaration and selected names |
| `references/entity-port` | an entity port: declaration, use in the architecture and the formal in another file |
| `references/enum-literal` | an enumeration literal |
| `references/label` | a loop label with its exit and end label |
| `references/bundled` | a std_logic_1164 type: the bundled declaration and every project use |
| `rename/dry-run` | the edit plan across files and libraries; each use site keeps its letter case (INC becomes STEP) |
| `rename/record-field` | a record field in its declaration and in selected names |
| `rename/entity-port` | a port: declaration, architecture use and the formal of the entity instantiation, not the component's port |
| `rename/apply` | without --dry-run the edits are written to the files |
| `rename/bundled-refused` | a bundled std/ieee name is not renamed |
| `rename/extended-refused` | an extended identifier keeps its spelling: refused |
| `rename/reserved-word` | a reserved word is not a valid new name |
| `rename/to-extended` | renaming to an extended identifier uses it verbatim at every site |
| `rename/opaque-refused` | a declaration of an opaque unit (a construct GoHard does not implement) is not renamed: its references are not known |
| `rename/conflict-region` | a new name already declared in the same region is refused |
| `rename/conflict-port` | an architecture signal may not take a port name of its entity (case-insensitive) |
| `rename/conflict-unit` | an entity may not take the name of another design unit of its library |
| `hover/opaque-unit` | hover still shows a declaration of an opaque unit (a protected type: its declaration text) |
| `hover/vunit-bound-port` | a port used inside a PSL vunit bound to `dut(rtl)` in another file (#110): the port of `dut`; own project (`dut.vhd`, `props.vhd`, `tb.vhd`) |
| `definition/vunit-bound-port` | the same name: the port in `dut.vhd` |

| `run/lens-position` | `gohard lsp run --at PATH:LINE:COL`: the Run testbench code lens (`gohard.runTestbench`) on the entity at the position; the result is `gohard lsp sim`'s JSON, and the run writes its trace and repro file (`want/`) |
| `run/seed` | `gohard lsp run --at PATH:1:1 1`: re-run with a seed; the `sim/race` warning against seed 0, as `gohard lsp sim --seed 1` |
| `run/design-error` | a design with errors is not simulated; the report holds the errors |
| `waveform/no-viewer` | `gohard lsp waveform`: Open waveform (#36) runs the testbench, exports FST next to the trace and, with neither `surfer` nor `gtkwave` on `PATH`, shows the FST path (`want/` holds the three files) |
| `waveform/surfer` | Surfer wins when both viewers are on `PATH` |
| `waveform/gtkwave` | only GTKWave on `PATH` |
| `repro/default` | `gohard lsp repro`: Copy repro, the `gohard check --repro FILE` command |
| `codelens/testbench` | the "▶ Run sev_tb" lens on a portless entity |

The `waveform/` cases replace the Executor with a simulated one through
the `viewers [NAME...]` setup directive (see `testdata/cli/README.md`).
`TestLSPRunSharesSimFixtures` runs every `testdata/cli/lsp-sim-*`
fixture through `gohard lsp run` (the editor's code path) and expects
the same stdout and the same trace and repro bytes.

`gohard lsp diagnostics` sends no request: it prints the last
`textDocument/publishDiagnostics` for the file once the server is idle
after opening it (an empty list when none was published).
`TestLSPDiagnosticsFixturesEqualCheck` checks that every
`diagnostics/` case equals `gohard check` on the same project, and
`TestLSPQuiescentDiagnosticsEqualCheck` checks the same after random
edit sequences over every corpus design.

The navigation cases (`hover/`, `definition/`, `references/`,
`rename/`) share one project: library `util` in `util/` (a package
with doc comments, a record, an enumeration, an alias, a function and
an entity) used from `top.vhd` in `work`. Bundled std/ieee locations
are `file://` URIs of the read-only copies the server writes to its
cache (`$XDG_CACHE_HOME/gohard/bundle/<key>/<lib>/<file>.vhdl`, #33);
the harness points `XDG_CACHE_HOME` at a temp dir and prints that
bundle root as `$BUNDLE`. `gohard lsp rename` writes the edits;
`--dry-run` prints the same WorkspaceEdit and writes nothing; a
refused rename is a usage error (exit 2) with the reason. Use sites
keep their letter case: a site spelled like the declaration gets the
new name as typed, an all upper-case or all lower-case site gets it in
that case. `TestNavCorpus`, `TestNavOSVVM`, `TestNavUVVM` and
`TestNavNEORV32` (`internal/ide`) check definition, references and
hover at every name occurrence, and `TestRenameCorpus` renames every
renamable declaration of the corpus and re-analyzes it.

`gohard lsp range-format --at PATH:LINE:COL END_LINE:END_COL` formats
the half-open range from `--at` to the end position (both one-based,
columns in bytes).

```sh
nix develop -c go test ./internal/cli -run TestLSPActionFixtures -update
```

## Session files

A session file (`gohard lsp --record`, `testdata/seeds/`) is the
header line, then one line per client input. A plain line is a
JSON-RPC body that arrived while the server was idle. Every other
input is an entry, an object with the key `"$gohard"` (documented in
`internal/server/entry.go`): `message` (a body that arrived after
`step` core steps while work was running, or a body that itself has a
`"$gohard"` key), `raw` (a body that is not JSON, base64), `end` (the
input ended while work was running), `file` and `analyzer`. Replay
queues every input after the same number of steps, which is what makes
a live session with cancels and edits during work replay byte for
byte.

## `record/<case>/`

`client.jsonl` and `in/` as above; a line starting with `!raw ` is
sent verbatim (without the prefix), for bodies that are not JSON. The test runs `gohard lsp` (nothing
may be written), then `gohard lsp --record rec` twice (writing
`rec/1.jsonl` and `rec/2.jsonl`), then `gohard lsp replay` on each file:
the replay must print exactly the bytes the live server wrote. Finally
it checks that every message sent is in the recording, changes
`top.vhd` and expects the replay to stop with exit 2 and
`repro/source-changed`. `gohard.yaml` is not checked: the recording
holds every read of it (`{"$gohard":"config"}` entries), which the
replay feeds back. Every case runs from `/elsewhere`, not from the
workspace folder `/work`, so the session folder must come from
initialize. `TestServeRecordsConfigReload` in `internal/server`
covers a `gohard.yaml` edited and saved mid-session, and
`TestServeRejectsSkippedRequest` a request the transport skips.

| Case | Shows |
| --- | --- |
| `editor-session` | an editor session with edits, hover and cancel |
| `request-before-initialize` | a request, a notification and a body that is not JSON before initialize: the header still hashes the folder initialize names, and a save of `gohard.yaml` reloads it from the recording |
| `invalid-body` | bodies that are not JSON-RPC objects (not JSON, a batch, a `"$gohard"` look-alike) are answered live and kept in the recording |

## Updating

```sh
nix develop -c go test ./internal/server -run TestLSPSessions -update
```

`-update` rewrites `want.jsonl` and `want.exit` from the current
server, unless an invariant fails. Review the diff before committing.
