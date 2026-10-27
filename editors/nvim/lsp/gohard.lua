-- Native config for Neovim 0.11+: with editors/nvim on 'runtimepath',
-- vim.lsp.enable('gohard') starts the server with these settings. It
-- is the same configuration require('gohard').setup() uses. In a
-- folder with no root marker Neovim sends no workspace folder; the
-- server then uses the folder of the first file opened (#48).
return require('gohard').config()
