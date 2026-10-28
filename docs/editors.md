# Editors

Every editor runs the same thing: `gohard lsp`, the language server
over stdio (#26). It works out the project root from `gohard.yaml`
(see [config.md](config.md)), so editors need no project setup. All
v1.0 features are standard LSP: diagnostics, hover, go-to-definition,
references, rename, completion, document and workspace symbols,
formatting, folding, highlights, the simulation code lens and the
"Open waveform" code action. v2 adds semantic tokens (#55, see
[Semantic tokens](#semantic-tokens)) and signature help and inlay hints
(#56, see [Signature help and inlay hints](#signature-help-and-inlay-hints)).
Neovim is the editor CI checks end to end (#38). Helix works with no
extension. Zed has an extension in `editors/zed` (#65). VS Code support
is planned for v2 (#3). The configs proposed to nvim-lspconfig, Helix
and Zed's extension registry are listed under
[Upstream configs](#upstream-configs).

Install `gohard` on your `PATH` first. Use any of these:

* `go install github.com/connerohnesorge/gohard/cmd/gohard@latest`
  (Go 1.27 or newer; the binary lands in `$(go env GOPATH)/bin`)
* a release binary from the GitHub Releases page: one archive per
  OS (linux, darwin, windows) and arch (amd64, arm64), checked with
  `sha256sum -c checksums.txt --ignore-missing`
* Nix: `nix profile install github:connerohnesorge/gohard`, or
  `nix run github:connerohnesorge/gohard -- version`, or `nix build`
  in a checkout (the flake's `packages.default`, at `./result/bin`)
* a package manager: `brew install --cask connerohnesorge/tap/gohard`,
  `scoop install connerohnesorge/gohard` after
  `scoop bucket add connerohnesorge https://github.com/connerohnesorge/scoop-bucket`,
  or `nix profile install nixpkgs#gohard` ([install.md](install.md))

Check it with `gohard version`.

## Neovim

The plugin is in `editors/nvim/` and needs Neovim 0.10 or later. CI
runs its end-to-end test on 0.10.4 and on the current Neovim. On an
older Neovim the plugin loads nothing and reports the version it
needs. It does three things:

* starts `gohard lsp` for VHDL buffers
* shows the simulation code lens
* adds the GoHard commands

The rest is the built-in LSP client.

With lazy.nvim:

```lua
{
  'connerohnesorge/gohard',
  ft = 'vhdl',
  config = function(plugin)
    vim.opt.rtp:append(plugin.dir .. '/editors/nvim')
    require('gohard').setup()
  end,
}
```

From a clone, without a plugin manager:

```lua
vim.opt.rtp:append('/path/to/gohard/editors/nvim')
require('gohard').setup({
  -- Every key is optional; these are the defaults.
  cmd = { 'gohard', 'lsp' },
  filetypes = { 'vhdl' },
  root_markers = { 'gohard.yaml', '.git' },
  settings = {},    -- editor settings, e.g. { gohard = { ... } }
  codelens = true,  -- show and refresh the code lenses
  keymaps = false,  -- <leader>gr/gs/gc/gw for the commands below
  -- on_attach = function(client, bufnr) end,
  -- capabilities = {},
})
```

On Neovim 0.11 or later you can skip `setup()`. Put `editors/nvim` on
the runtime path, then call `vim.lsp.enable('gohard')`. The plugin's
`lsp/gohard.lua` holds the same configuration that `setup()` uses.

Without the plugin, `editors/upstream/nvim-lspconfig/lsp/gohard.lua`
is the self-contained config proposed to nvim-lspconfig (`cmd`,
`filetypes` and `root_markers` only). Once nvim-lspconfig ships it,
`vim.lsp.enable('gohard')` works with nvim-lspconfig installed and no
GoHard plugin. Lenses then show where Neovim fetches them (on 0.12,
`vim.lsp.codelens.enable()`), and `vim.lsp.codelens.run()` runs one;
the plugin adds the refresh and the `:GoHard*` commands.

The two differ for files with no `gohard.yaml` and no `.git` above
them. There `setup()` sends the file's folder as the workspace folder
and starts one client per such folder. `vim.lsp.enable` sends no
workspace folder, and the server takes the folder of the first file
opened as the project folder (#48), like the CLI run from that folder,
so the testbench lens shows up. Neovim then attaches every other file
without a root marker to that same client, and the server keeps its
first folder: a testbench in a second unmarked folder is checked
against the first folder's project, so an entity defined in a file of
its own folder that is not open does not resolve. To work in several
unmarked folders, use `setup()` or put a `gohard.yaml` in each.

Neovim colours names with the server's semantic tokens on its own.
`setup()` links the two token types LSP has no name for:
`@lsp.type.signal.vhdl` to `@lsp.type.variable` and
`@lsp.type.port.vhdl` to `@lsp.type.parameter` (as defaults, so a
colour scheme or your config can set them). With `vim.lsp.enable`
instead of `setup()`, link them yourself.

Editor settings go under a `gohard` key in `settings`. They use the
`gohard.yaml` keys of [config.md](config.md). Any key that
`gohard.yaml` also sets keeps the `gohard.yaml` value, and the server
logs each overridden key in a `window/logMessage` (#28).

### Simulation code lens and commands

The server puts a "▶ Run tb_top" lens above each testbench (#25).
Running it changes the lens to "◉ Running…" and reports `$/progress`.
When the run ends, the lens shows "▶ Re-run · seed 42" next to a
"Copy repro" lens. The run's assertions and reports show up as
`gohard-sim` diagnostics with the simulated time and seed:

* `error` and `failure` are errors
* `warning` is a warning
* `note` is a hint

A lens runs the same simulation as `gohard lsp sim --tb NAME`.

| Command | Does |
| --- | --- |
| `:GoHardRun` | runs the testbench lens nearest the cursor |
| `:GoHardRerun [SEED]` | re-runs it, with `SEED` (a number or `random`) instead of the last seed when given |
| `:GoHardCopyRepro` | copies the repro command of the last run to the `"` and `+` registers |
| `:GoHardOpenWaveform` | runs the server's "Open waveform" code action |
| `:GoHardLenses` | lists the buffer's lenses |

You can also run any lens with `vim.lsp.codelens.run()`.

The plugin has no viewer option. When you open a waveform, the server
exports the trace to FST and opens it in Surfer if `surfer` is on
`PATH`. Otherwise it opens GTKWave if `gtkwave` is on `PATH`, and if
neither is there it shows you the FST file path (#36).

The plugin finds lenses by their titles, the labels listed above. It
runs a lens command through `workspace/executeCommand` when the server
advertises that command. Otherwise it treats the command as a
client-side copy of its first argument.

### End-to-end test

`editors/nvim/test/run.sh` builds `gohard`, then drives a headless
Neovim (`nvim --headless --clean`) through `editors/nvim/test/e2e.lua`:

1. In the cross-file fixture (`testdata/lsp/diagnostics/cross-file`) it
   checks hover, definition, references and rename for a constant
   declared in another file. Rename has to edit both files, and
   definition has to work again afterwards.
2. It opens the passing corpus testbench (`wait_forms`), runs its
   "▶ Run" lens and waits for "▶ Re-run · seed N".
3. It does the same for the failing one (`assert_fail`).
4. For both testbenches, the `gohard-sim` diagnostics must equal what
   `gohard lsp sim --tb NAME` reports on the same project, and "Copy
   repro" must fill a register.
5. In the semantic tokens fixture (`testdata/lsp/semantic-tokens/full`)
   it checks the token type and modifiers Neovim holds at a signal, a
   port, a function, an enumeration literal, a record element, a
   generic, a comment and a keyword, and that the signal is drawn
   with `@lsp.type.signal.vhdl`, which has a colour.
6. On Neovim 0.11 or later, a second run skips `setup()` and calls
   `vim.lsp.enable('gohard')` with the shipped `lsp/gohard.lua`, in a
   copy of `wait_forms` with no `gohard.yaml` or `.git` above it. The
   client has no root and no workspace folder; the "▶ Run" lens must
   still appear and the run must pass the checks of step 2 and 4.
7. A third run does the same with
   `editors/upstream/nvim-lspconfig/lsp/gohard.lua` as the only
   `lsp/gohard.lua` on the runtime path. The config must load before
   the plugin's Lua modules are reachable, so it cannot depend on
   them (#65). The plugin's commands are used only to drive the test.

CI runs it on Linux (`scripts/ci/nvim.sh`). Run it locally with
`nix develop -c bash editors/nvim/test/run.sh`.

## Helix

Helix connects over stdio with no extension. The change proposed to
Helix's own `languages.toml` (`editors/upstream/helix/gohard.diff`)
defines the `gohard` server and lists it after `vhdl_ls` for VHDL.
Once Helix ships it, installing `gohard` is enough. Until then, add
this to `~/.config/helix/languages.toml`, or to `.helix/languages.toml`
in the project:

```toml
[language-server.gohard]
command = "gohard"
args = ["lsp"]
# Editor settings, the gohard.yaml keys under "gohard"; gohard.yaml wins.
# config = { gohard = { } }

[[language]]
name = "vhdl"
scope = "source.vhdl"
file-types = ["vhd", "vhdl"]
roots = ["gohard.yaml", ".git"]
comment-token = "--"
indent = { tab-width = 2, unit = "  " }
language-servers = ["gohard"]
# Formatting is LSP formatting (:format); set auto-format = true to
# format on save.
auto-format = false
```

The upstream entry sets no `roots`, so Helix sends the folder holding
`.git` as the root, or with no `.git` the folder it was started in,
and the server looks for `gohard.yaml` from there upward. If your
`gohard.yaml` sits in a folder below the `.git` folder, keep the
`roots` line of the snippet.

Run `hx --health vhdl` to check that Helix finds `gohard`. Run a
testbench lens from the picker of `:lsp-workspace-command`, or from
the shell with `gohard lsp sim --tb NAME`. Helix offers "Open
waveform" with the other code actions (`space a`).

CI does not launch Helix. Instead, `TestHelixStdio` in `cmd/gohard`
builds the `gohard` binary and starts it over real OS pipes with the
command and arguments it reads from the upstream entry in
`editors/upstream/helix/gohard.diff`. It sends the initialize
handshake with Helix's own client capabilities, once with the root
and workspace folder Helix sends for a folder holding `.git` and once
with only `rootPath` (no `.git`), and no configuration, as Helix does
for an entry without `config`. It opens a file of a two-file project.
The diagnostics it gets back must equal `gohard check` on the same
project. Then it shuts the server down and checks that it exits 0.

## Zed

The extension in `editors/zed` adds the `gohard` language server to
Zed's VHDL language. The language itself (grammar, file types) comes
from Zed's "vhdl" extension, so install that one first, from
`zed: extensions`. Until the GoHard extension is in Zed's registry,
install it with `zed: install dev extension` and pick `editors/zed` of
a GoHard checkout; Zed builds it, which needs Rust from rustup.

The extension starts `gohard lsp` with the `gohard` it finds on the
`PATH` of the project's shell. Settings go in Zed's `settings.json`:

```json
{
  "lsp": {
    "gohard": {
      // Optional: a gohard that is not on PATH.
      "binary": { "path": "/full/path/to/gohard" },
      // Editor settings, the gohard.yaml keys under "gohard";
      // gohard.yaml wins.
      "settings": { "gohard": {} }
    }
  },
  "languages": {
    // Optional: only GoHard, not vhdl_ls, for VHDL.
    "VHDL": { "language_servers": ["gohard", "!vhdl_ls", "..."] }
  }
}
```

Zed sends the opened folder as the root, and the server looks for
`gohard.yaml` from there upward. Open the folder that holds
`gohard.yaml`, or one below it. Zed shows the testbench lenses when
code lenses are on (`"code_lens": "on"`), and offers "Open waveform"
with the other code actions.

CI does not run Zed. `TestZedStdio` in `cmd/gohard` reads the server
name and arguments from `editors/zed/src/lib.rs` and the VHDL language
ID from `editors/zed/extension.toml`. It starts `gohard` over real OS
pipes and runs Zed's handshake: Zed's client capabilities, which offer
only utf-16 positions, the folder as root and workspace folder, no
initialization options, and an empty `workspace/didChangeConfiguration`
(the extension's configuration when no `lsp.gohard.settings` is set).
The diagnostics of the opened file must equal `gohard check`, as in
`TestHelixStdio`.

## Upstream configs

These are proposed to the editors' own repositories (#65). Each
submission is sent by hand; the pull request links and the upstream
answers are recorded on #65.

| Editor | Proposed config | Upstream |
| --- | --- | --- |
| Neovim | `editors/upstream/nvim-lspconfig/lsp/gohard.lua` | `lsp/gohard.lua` in neovim/nvim-lspconfig |
| Helix | `editors/upstream/helix/gohard.diff` | `languages.toml` in helix-editor/helix |
| Zed | `editors/zed` | the extension `gohard-lsp` in zed-industries/extensions |

`editors/upstream/README.md` describes each submission.

## Semantic tokens

The server answers `textDocument/semanticTokens/full`, `/full/delta`
and `/range` (#55) from the same analysis as hover and definition, in
the negotiated position encoding. A token never spans lines. The
legend:

| Type | Names |
| --- | --- |
| `namespace` | libraries |
| `type` | types and subtypes |
| `class` | entities, architectures, packages, components, configurations, contexts |
| `function` | functions and procedures |
| `parameter` | subprogram parameters |
| `variable` | variables, constants, generics, loop parameters, aliases of objects |
| `property` | record elements |
| `enumMember` | enumeration literals and physical units |
| `signal`, `port` | signals and ports (not LSP standard types) |
| `keyword`, `string`, `number`, `comment` | reserved words, string, bit-string and character literals, numbers, comments |

Modifiers: `declaration` at the name a declaration defines; `readonly`
on constants, generics, loop and generate parameters, `in` ports and
constant parameters; `static` on generics and shared variables;
`defaultLibrary` on `std` and `ieee` names; `inactive` on an inactive
conditional-analysis branch (`` `if ``), whose lines are sent as
`comment` tokens with that modifier so an editor can dim them. Labels,
attributes, groups, mode views, PSL declarations and operators get no
token.

`gohard lsp semantic-tokens --at PATH:1:1 [full|range L:C-L:C]` prints
the full or range result for a file (`--at` names the file).

## Signature help and inlay hints

Signature help (#56) opens on `(` and `,` inside a subprogram call, a
procedure call, a generic map or a port map, and on an operand of an
operator. A call shows every overload the resolver kept: the one the
call resolves to first (the one hover and go to definition give),
then the others, each with why it does not fit. A map shows the
generic or port list of the instantiated entity, component or block.
Each signature carries its parameters with their class, mode, type
and default as written. The active parameter is the formal a named
association names, else the position of a positional one.

Inlay hints come in kinds, each with its own key in `gohard.yaml` or
the editor settings, all off by default:

| Key | Shows |
| --- | --- |
| `inlay_hints.parameter_names` | `formal =>` before each positional actual of a call, generic map or port map |
| `inlay_hints.types` | `: TYPE` after the name of an object alias without a subtype indication and of a loop parameter |
| `inlay_hints.values` | `= VALUE` after the value of a constant or a generic default, when static evaluation succeeds within the editor's limit of 1,000,000 steps; a value written as a literal and a composite value of more than 1,024 scalars get none, and an image longer than 40 bytes is cut |

A changed key takes effect at the next request; a client that
announces `workspace.inlayHint.refreshSupport` is asked to request its
hints again. A result computed for a snapshot an edit has replaced is
never sent. In Neovim, turn the display on with
`vim.lsp.inlay_hint.enable(true)` and the kinds in `settings`:

```lua
settings = { gohard = { inlay_hints = { parameter_names = true } } },
```

In Helix, set `display-inlay-hints = true` under `[editor.lsp]` and
the kinds in `config`.

`gohard lsp signature-help --at PATH:LINE:COL` prints the signature
help at a position, and `gohard lsp inlay-hints --at PATH:LINE:COL
[END_LINE:END_COL]` the hints from the position to the end (the end
of the file without one).

## Platform notes

* **Linux:** the plugin and the Helix snippet work as shown. To open
  waveforms, put `surfer` or `gtkwave` on `PATH`. Your distribution
  may package them, or Nix has both.
* **macOS:** the same. A GUI Neovim (Neovide, VimR) started from the
  Dock may not inherit your shell's `PATH`. If it can't find
  `gohard`, set `cmd = { '/full/path/to/gohard', 'lsp' }`. The same
  applies to finding `surfer`. Homebrew's `gtkwave` cask is also
  found, as long as `gtkwave` is on `PATH`.
* **Windows:** the binary is `gohard.exe`. Neovim, Helix and Zed find
  it on `PATH` without the extension. Paths in diagnostics and
  locations are `file:///C:/...` URIs. To use `Copy repro`, Neovim needs a
  clipboard provider for the `+` register (`win32yank` or the
  built-in one in recent releases). The unnamed register always gets
  the text.
