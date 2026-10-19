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
