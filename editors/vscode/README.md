# GoHard for VS Code

VS Code extension for [GoHard](https://github.com/connerohnesorge/gohard),
a VHDL language server, checker, formatter and simulator. The
extension starts `gohard lsp` for VHDL files. Diagnostics, hover,
go to definition, references, rename, completion, symbols, formatting,
folding, highlights, semantic tokens, signature help, inlay hints, the
testbench code lenses, the "Open waveform" code action and the code
actions that write instances, component declarations, port maps and
testbenches all come from the server.

The extension adds the VHDL language (`.vhd`, `.vhdl`) with a
TextMate grammar for the reserved words, literals, comments and
operators; the server's semantic tokens colour the names.

## Requirements

Install `gohard` on your `PATH`, or set `gohard.path`. The install
options are in
[docs/install.md](https://github.com/connerohnesorge/gohard/blob/main/docs/install.md).
The extension's version is the version of the GoHard release it was
built with; use a `gohard` of the same release.

## Settings

| Setting | Default | Does |
| --- | --- | --- |
| `gohard.path` | `gohard` | the `gohard` binary to start as `gohard lsp`: a command on `PATH` or an absolute path; a change restarts the server |
| `gohard.trace.server` | `off` | `messages` or `verbose` logs the LSP messages to the GoHard output |
| `gohard.inlay_hints.parameter_names` | `false` | the formal name before each positional actual |
| `gohard.inlay_hints.types` | `false` | the type of an alias without a subtype indication and of a loop parameter |
| `gohard.inlay_hints.values` | `false` | the static value of a constant or a generic default |

The `gohard.inlay_hints.*` settings are the `inlay_hints` keys of
`gohard.yaml`. A key that `gohard.yaml` also sets keeps the
`gohard.yaml` value. The extension sends only the keys you set, so
`gohard.yaml` decides the others. VS Code shows inlay hints when
`editor.inlayHints.enabled` is on (the default).

In a workspace you have not trusted, `gohard.path` comes only from
your user settings.

## Commands

* **GoHard: Restart Server** stops `gohard lsp` and starts it again.
* **GoHard: Show Output** shows the server's log.

The "Copy repro" lens copies the repro command of a run to the
clipboard.

More, including what the server sends and how the project root is
found, is in
[docs/editors.md](https://github.com/connerohnesorge/gohard/blob/main/docs/editors.md#vs-code).
