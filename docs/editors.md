# Editors

Every editor runs the same thing: `gohard lsp`, the language server
over stdio (#26). It works out the project root from `gohard.yaml`
(see [config.md](config.md)), so editors need no project setup. All
v1.0 features are standard LSP: diagnostics, hover, go-to-definition,
references, rename, completion, document and workspace symbols,
formatting, folding, highlights, the simulation code lens and the
"Open waveform" code action. v2 adds semantic tokens (#55, see
[Semantic tokens](#semantic-tokens)), signature help and inlay hints
(#56, see [Signature help and inlay hints](#signature-help-and-inlay-hints))
and code actions that write instances, component declarations, port
maps and testbenches (#57, see [Code actions](#code-actions)).
CI checks Neovim (#38) and VS Code (#3) end to end. Helix works with
no extension. Zed and VS Code have extensions, in `editors/zed` (#65)
and `editors/vscode` (#3). The configs proposed to nvim-lspconfig,
Helix and Zed's extension registry are listed under
[Upstream configs](#upstream-configs).

Install `gohard` on your `PATH` first. Use any of these:

* `go install github.com/connerohnesorge/gohard/v2/cmd/gohard@latest`
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

### Debugging (prototype)

`gohard dap` is a simulation debugger that speaks the Debug Adapter
Protocol on stdio (#68). With
[nvim-dap](https://github.com/mfussenegger/nvim-dap) installed,
`editors/nvim/lua/gohard/dap.lua` registers the adapter and a launch
configuration for VHDL buffers:

```lua
require('gohard.dap').setup({
  -- Every key is optional; these are the defaults.
  cmd = { 'gohard', 'dap' },
  testbench = nil,     -- asked for when nil; empty: the only testbench
  seed = 0,            -- the simulation seed, as --seed
  stopOnEntry = false, -- stop at the run's first statement
})
```

The adapter analyzes the project of the configuration's `cwd`
(`${workspaceFolder}`, the folder Neovim was started in) as `gohard
check --sim --tb TB --seed N` does and debugs that run. Set a
breakpoint on a sequential statement (`:DapToggleBreakpoint`) and
start with `:DapContinue`:

* every process is a thread; each frame has the scopes Locals (its
  constants, variables and parameters) and Signals (the signals its
  process sees)
* step over, into and out of procedures, continue, and step back and
  reverse continue (`require('dap').step_back()`,
  `require('dap').reverse_continue()`); the stop's description shows
  the simulated time, the delta and the statement count
* a procedure of the bundled std/ieee libraries that you step into
  (`ieee.math_real`'s `uniform`, say) opens in a buffer of the bundled
  text, which nvim-dap reads from the adapter
* at the end of the run the console shows the report `gohard check
  --sim` gives, and the session ends with its exit code

Statements of pure functions are not stops, a running move cannot be
paused, and values cannot be changed. The debugger replays the run
from checkpoints instead of keeping it suspended, so a run with
breakpoints gives the same trace as one without them; the design is
in [architecture.md](architecture.md#debugger-68).

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

## VS Code

The extension in `editors/vscode` (#3) starts `gohard lsp` for VHDL
files. It adds the VHDL language: the file types `.vhd` and `.vhdl`,
comment and bracket rules, and a TextMate grammar for the reserved
words, literals, comments, operators and attributes. Everything else
comes from the server: diagnostics, navigation, formatting, semantic
tokens (they colour the names), signature help, inlay hints, the
testbench lenses and the "Open waveform" code action. It needs VS Code
1.91 or later; forks such as VSCodium install it from Open VSX.

Install "GoHard" (ID `connerohnesorge.gohard`) from the VS Code
Marketplace or Open VSX. Each GitHub release also carries its
`gohard-VERSION.vsix`, which
`code --install-extension gohard-VERSION.vsix` installs. The
extension's version is the release it comes from; use a `gohard` of
the same release.

| Setting | Default | Does |
| --- | --- | --- |
| `gohard.path` | `gohard` | the `gohard` binary to start as `gohard lsp`: a command on `PATH` or an absolute path; a change restarts the server |
| `gohard.trace.server` | `off` | `messages` or `verbose` logs the LSP messages to the GoHard output |
| `gohard.inlay_hints.parameter_names`, `.types`, `.values` | `false` | the inlay hint kinds of [Signature help and inlay hints](#signature-help-and-inlay-hints) |

The extension sends the `gohard.inlay_hints.*` settings as the
`inlay_hints` keys of the editor settings, in `initializationOptions`
and, when one changes, in `workspace/didChangeConfiguration`. It sends
only the keys set in a settings file, so a key left at its default
does not override anything and `gohard.yaml` decides it; a key both
set keeps the `gohard.yaml` value, as in every editor. In a workspace
you have not trusted, VS Code takes `gohard.path` only from the user
settings.

VS Code sends the opened folders as workspace folders. The server
takes the first one as the project folder and looks for `gohard.yaml`
from there upward, so in a multi-root workspace only the first
folder's project is checked. A file opened with no folder gets the
folder of the first file opened (#48).

Two commands: "GoHard: Restart Server" and "GoHard: Show Output" (the
server's log, and the LSP messages when `gohard.trace.server` is on).
Restarts, by the command or by a change of `gohard.path`, run one at a
time, and each waits until the server before it has answered
`initialize`; a server that never answers holds them until the window
reloads.
The lenses run through the server's commands. The one thing the
extension does with a command's result is "Copy repro": it copies the
command the server returns to the clipboard.

The extension declares the token types `signal`, based on `variable`,
and `port`, based on `parameter`, and the modifier `inactive`. A theme
without rules for them colours signals as variables and ports as
parameters; set your own with
`"editor.semanticTokenColorCustomizations": {"rules": {"signal": "#4fc1ff"}}`.

### End-to-end test

`editors/vscode/test/run.sh` compiles the extension, builds `gohard`
and starts VS Code twice through `@vscode/test-electron`, each time
on a copy of a `testdata/lsp` fixture with `gohard.path` pointing at
that build:

1. In `diagnostics/cross-file` it opens `top.vhd` and waits until VS
   Code's diagnostics of the file equal `gohard lsp diagnostics` on
   the same folder. The file's semantic tokens must equal
   `gohard lsp semantic-tokens`, and VS Code must decode the token at
   the signal `s` as a `signal` with the `declaration` modifier.
2. In `inlay-hints/all-off`, which has no `gohard.yaml`, the file has
   no hint. With `gohard.inlay_hints.parameter_names` turned on in the
   user settings, its hints must equal those of the
   `inlay-hints/parameter-names` fixture (sent with
   `didChangeConfiguration`), and again after "GoHard: Restart
   Server" (sent with `initializationOptions`). After three restarts
   at once, turning the setting off must remove the hints and one
   `gohard lsp` process must be left. "GoHard: Show Output" runs, and
   the `gohard.copyRepro` command leaves its result on the clipboard.

A run that exits without reaching the end of its checks fails.
`npm test` runs the unit tests under Node: the settings the extension
sends, the order of the client's starts and stops, the manifest, and
the grammar with VS Code's TextMate engine
(`vscode-textmate`). Every reserved word of `internal/token/keywords.yaml`
must be a keyword in any letter case and the PSL-only words must not.
`TestVSCodeManifest` in `cmd/gohard` (plain `go test`) checks the
manifest against the server: the token types and modifiers it
declares are those of the legend that LSP lacks, each
`gohard.inlay_hints.*` setting has the key table's type, default and
text, and the version equals the flake's.

CI runs `scripts/ci/vscode.sh` (npm ci, the unit tests, `vsce package`
and the end-to-end test) on Linux on VS Code 1.91.1 and the current
release. Locally, run `nix develop -c bash scripts/ci/vscode.sh`.
`VSCODE_VERSION` picks the VS Code it downloads. Where the download
cannot start, as on NixOS, set `VSCODE_PATH` to the Electron binary of
an installed VS Code (for nixpkgs' `vscode`, `lib/vscode/code`, not
the `bin/code` launcher, which returns at once). VS Code and the
`gohard` it starts run under the dev shell's memory cap
(`GOHARD_MEMCAP`).

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
call resolves to first (the one hover and go to definition give), then
the others, each with why it does not fit. This holds for a call
anywhere, an index constraint or a loop range included. A map shows
the generic or port list of the instantiated entity, component, block
or package. Each signature carries its parameters with their class,
mode, type and default as written. The active parameter is the formal
a named association names, else the position of a positional one.

Inlay hints come in kinds, each with its own key in `gohard.yaml` or
the editor settings, all off by default:

| Key | Shows |
| --- | --- |
| `inlay_hints.parameter_names` | `formal =>` before each positional actual of a call, generic map or port map |
| `inlay_hints.types` | `: TYPE` after the name of an object alias without a subtype indication and of a loop parameter |
| `inlay_hints.values` | `= VALUE` after the value of a constant or a generic default, when static evaluation succeeds within the editor's limit of 1,000,000 steps; the values of one request share that limit in source order, so once it is spent the later values of the file get none; a value written as a literal and a composite value of more than 1,024 scalars get none, and an image longer than 40 bytes is cut |

A changed key takes effect at the next request; a client that
announces `workspace.inlayHint.refreshSupport` is asked to request its
hints again. A result computed for a snapshot an edit has replaced is
never sent. In Neovim, turn the display on with
`vim.lsp.inlay_hint.enable(true)` and the kinds in `settings`:

```lua
settings = { gohard = { inlay_hints = { parameter_names = true } } },
```

In Helix, set `display-inlay-hints = true` under `[editor.lsp]` and
the kinds in `config`. In VS Code, which shows inlay hints by default,
set `gohard.inlay_hints.parameter_names` and the other kinds
([VS Code](#vs-code)).

`gohard lsp signature-help --at PATH:LINE:COL` prints the signature
help at a position, and `gohard lsp inlay-hints --at PATH:LINE:COL
[END_LINE:END_COL]` the hints from the position to the end (the end
of the file without one).

## Code actions

Besides its quick fixes (`quickfix`: add a missing `use`, map an
unmapped library, add a signal to a sensitivity list) and "Open
waveform", the server offers four actions that write VHDL (#57). The
`codeActionKinds` it advertises are `quickfix`, `refactor.rewrite` and
`source`.

| Action | Kind | Where | Does |
| --- | --- | --- | --- |
| Instantiate entity LIB.NAME | `refactor.rewrite` | on a concurrent statement that is only a name, `counter` or `work.counter` (with or without `;`, with or without a label) | replaces the label, the name and its `;` with `u_counter : entity work.counter generic map (...) port map (...);` listing every formal; the statements after a name without `;` stay, and the action is not offered when more than the name is on its line; a name the unit's library does not declare is looked up in the other project libraries, and the unit gets `library LIB;` when it does not name LIB yet |
| Declare component NAME | `refactor.rewrite` | on a component instantiation whose component is not declared and whose entity is in the unit's library | adds the component declaration of the entity of that name, its generics and ports as the entity writes them, at the end of the architecture's declarative part (after the comment on the line of the last declaration), and the `use` and `library` clauses the unit needs for the names they use |
| Complete port map | `refactor.rewrite` | on an instance whose port map leaves formals out | adds the missing formals after the last association, each on a line of its own when the map spans lines |
| Create testbench NAME_tb.vhd | `source` | in an entity's design unit, when the entity has ports | creates `NAME_tb.vhd` next to the entity's file |

Declare component is not offered for an entity of another library:
the default binding of a component instance looks for the entity in
the unit's own library only, so the instance would stay unbound; use
Instantiate entity there. Nor is it offered when a name the generic
and port declarations use denotes something else at the end of the
architecture's declarative part.

Instantiate entity and Complete port map write `formal => formal` when
an object of the formal's name is visible at the instance, else
`formal => open` when the formal has a default or is a port of a mode
other than `in`, else `formal => formal` for you to declare. Every
generated text is laid out by the formatter with the `format.*` keys
of `gohard.yaml`, so `gohard fmt --check` accepts a formatted file
after the edit.

The testbench repeats the entity's context clause and declares a
constant for every generic (its default; without one, the value in the
type's range nearest to 0, or `T'left`, or `""` for a string-like
type), a signal for every port with the port's subtype and default,
and the instance `u_NAME`. A port looks like a clock when it is an
`in` port of type `std_logic` or `std_ulogic` whose name, in any
letter case, is `clk` or `clock` or starts with `clk_` or ends with
`_clk`. Each clock gets a process toggling it with the period
`CLK_PERIOD` (10 ns). The stimulus process waits 100 clock periods,
or 100 ns without a clock, then calls `std.env.finish`. Under
`standard: "1993"` or `"2002"`, which have no `std.env`, the stimulus
sets the signal `done` instead, which stops the clocks, and every
process ends with `wait;`, so the run ends when nothing is left to
do. The testbench has no ports, so it is a testbench without any
`simulation.testbenches` entry. Names the testbench adds get a `_2`
suffix when a port or generic already uses them.

Create testbench is offered only when the client announces
`workspace.workspaceEdit.resourceOperations` with `create`: the edit
is a `CreateFile` operation followed by the file's text. It is not
offered when `NAME_tb.vhd` or a unit `NAME_tb` already exists, when
the new file would not be analyzed into the entity's library (a folder
starting with `.`, a folder outside the project root that `libraries`
does not list, or an entity whose file `libraries` lists as a file
entry), or when the entity has a generic type, subprogram or package,
a generic without a default whose type has no placeholder, or a port
of a mode view, of a shared variable, or of a subtype that is not
fully constrained.

`gohard lsp codeaction --at PATH:LINE:COL` prints the code actions at a
position; with a TITLE argument it applies that action instead,
writing its edits and creating its file, and prints it.
`--no-resource-operations` plays a client without the `create`
resource operation.

## Platform notes

* **Linux:** the plugin and the Helix snippet work as shown. To open
  waveforms, put `surfer` or `gtkwave` on `PATH`. Your distribution
  may package them, or Nix has both.
* **macOS:** the same. A GUI Neovim (Neovide, VimR) started from the
  Dock may not inherit your shell's `PATH`. If it can't find
  `gohard`, set `cmd = { '/full/path/to/gohard', 'lsp' }`. The same
  applies to finding `surfer`. Homebrew's `gtkwave` cask is also
  found, as long as `gtkwave` is on `PATH`.
* **Windows:** the binary is `gohard.exe`. Neovim, Helix, Zed and VS
  Code find it on `PATH` without the extension. Paths in diagnostics and
  locations are `file:///C:/...` URIs. To use `Copy repro`, Neovim needs a
  clipboard provider for the `+` register (`win32yank` or the
  built-in one in recent releases). The unnamed register always gets
  the text.
