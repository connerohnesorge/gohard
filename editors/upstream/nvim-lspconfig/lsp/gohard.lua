---@brief
---
--- https://github.com/connerohnesorge/gohard
---
--- GoHard, a VHDL language server, checker, formatter and simulator.
--- Install `gohard` on your `PATH`:
--- ```sh
--- go install github.com/connerohnesorge/gohard/cmd/gohard@latest
--- ```
--- or take a release binary from the GitHub Releases page.
---
--- The project root is the nearest folder holding `gohard.yaml`, else
--- the nearest holding `.git`. In a folder with neither, the server uses
--- the folder of the first file opened.
---
--- Editor settings take the `gohard.yaml` keys under a `gohard` key, and
--- `gohard.yaml` wins over them:
--- ```lua
--- vim.lsp.config('gohard', {
---   settings = { gohard = { standard = '2019' } },
--- })
--- ```
---
--- Each testbench gets a "▶ Run" code lens (`vim.lsp.codelens.run()`).
--- The plugin in `editors/nvim` of the GoHard repository adds lens
--- refresh and `:GoHard*` commands.

---@type vim.lsp.Config
return {
  cmd = { 'gohard', 'lsp' },
  filetypes = { 'vhdl' },
  root_markers = { 'gohard.yaml', '.git' },
}
