# GoHard for Zed

Zed extension for `gohard lsp` (#65). It adds the `gohard` language
server to the VHDL language of Zed's "vhdl" extension, which provides
the grammar and file types. Setup and settings are in
[docs/editors.md](../../docs/editors.md#zed).

* `extension.toml` - the manifest: ID `gohard-lsp`, server `gohard`
  for the language `VHDL`
* `src/lib.rs` - finds `gohard` on `PATH` (or `lsp.gohard.binary.path`)
  and passes `lsp.gohard.settings` as the workspace configuration
* `LICENSE` - Apache-2.0; Zed's registry needs a license from its list
  in the extension's own folder

Build check (Zed builds the same target when it installs the
extension; needs rustup with the `wasm32-wasip2` target):

```
cargo build --release --target wasm32-wasip2
```

CI does not run Zed or build this crate. `TestZedStdio` in
`cmd/gohard` reads the server name and arguments from `src/lib.rs` and
the VHDL language ID from `extension.toml`, starts `gohard lsp` the way
Zed does and checks the diagnostics of an opened file.
