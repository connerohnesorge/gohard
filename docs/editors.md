# Editors

Every editor runs the same thing: `gohard lsp`, the language server
over stdio (#26). It works out the project root from `gohard.yaml`
(see [config.md](config.md)), so editors need no project setup. All
v1.0 features are standard LSP: diagnostics, hover, go-to-definition,
references, rename, completion, document and workspace symbols,
formatting, folding, highlights, the simulation code lens and the
"Open waveform" code action. Neovim is the editor CI checks end to end
(#38). Helix works with no extension. VS Code support is planned for
v2 (#3).

Install `gohard` on your `PATH` first. Use any of these:

* `go install github.com/connerohnesorge/gohard/cmd/gohard@latest`
  (Go 1.27 or newer; the binary lands in `$(go env GOPATH)/bin`)
* a release binary from the GitHub Releases page: one archive per
  OS (linux, darwin, windows) and arch (amd64, arm64), checked with
  `sha256sum -c checksums.txt --ignore-missing`
* Nix: `nix profile install github:connerohnesorge/gohard`, or
  `nix run github:connerohnesorge/gohard -- version`, or `nix build`
  in a checkout (the flake's `packages.default`, at `./result/bin`)

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

The two differ only in a folder with no `gohard.yaml` and no `.git`
above the file. There `setup()` sends the file's folder as the
workspace folder. `vim.lsp.enable` sends no workspace folder, and the
server then takes the folder of the first file opened as the project
folder (#48). Either way the project is the same, like the CLI run
from that folder, and the testbench lens shows up.

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
5. On Neovim 0.11 or later, a second run skips `setup()` and calls
   `vim.lsp.enable('gohard')` with the shipped `lsp/gohard.lua`, in a
   copy of `wait_forms` with no `gohard.yaml` or `.git` above it. The
   client has no root and no workspace folder; the "▶ Run" lens must
   still appear and the run must pass the checks of step 2 and 4.

CI runs it on Linux (`scripts/ci/nvim.sh`). Run it locally with
`nix develop -c bash editors/nvim/test/run.sh`.

## Helix

Helix connects over stdio with no extension. Add this to
`~/.config/helix/languages.toml`, or to `.helix/languages.toml` in the
project:

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

Run `hx --health vhdl` to check that Helix finds `gohard`. Run a
testbench lens from the picker of `:lsp-workspace-command`, or from
the shell with `gohard lsp sim --tb NAME`. Helix offers "Open
waveform" with the other code actions (`space a`).

CI does not launch Helix. Instead, `TestHelixStdio` in `cmd/gohard`
builds the `gohard` binary and starts `gohard lsp` over real OS pipes.
It sends the initialize handshake with Helix's own client capabilities
and opens a file of a two-file project. The diagnostics it gets back
must equal `gohard check` on the same project. Then it shuts the
server down and checks that it exits 0.

## Platform notes

* **Linux:** the plugin and the Helix snippet work as shown. To open
  waveforms, put `surfer` or `gtkwave` on `PATH`. Your distribution
  may package them, or Nix has both.
* **macOS:** the same. A GUI Neovim (Neovide, VimR) started from the
  Dock may not inherit your shell's `PATH`. If it can't find
  `gohard`, set `cmd = { '/full/path/to/gohard', 'lsp' }`. The same
  applies to finding `surfer`. Homebrew's `gtkwave` cask is also
  found, as long as `gtkwave` is on `PATH`.
* **Windows:** the binary is `gohard.exe`. Neovim and Helix both find
  it on `PATH` without the extension. Paths in diagnostics and
  locations are `file:///C:/...` URIs. To use `Copy repro`, Neovim needs a
  clipboard provider for the `+` register (`win32yank` or the
  built-in one in recent releases). The unnamed register always gets
  the text.
