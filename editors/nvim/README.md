# GoHard for Neovim

Neovim plugin for `gohard lsp` (#38): lspconfig-style setup, the
simulation code lens (run, re-run with a seed, copy repro) and the
`:GoHard*` commands. Setup, commands and platform notes are in
[docs/editors.md](../../docs/editors.md); `:help gohard` has the same
summary.

* `lua/gohard/init.lua` - `setup()`, `config()`, `client()`
* `lua/gohard/commands.lua` - the commands, lens lookup by title
* `lua/gohard/dap.lua` - the nvim-dap adapter and launch
  configuration of `gohard dap`, the debugger prototype (#68)
* `lsp/gohard.lua` - native config for `vim.lsp.enable('gohard')`
* `plugin/gohard.lua` - defines the commands at startup
* `test/run.sh`, `test/e2e.lua` - the headless end-to-end test (CI)
