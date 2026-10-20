-- Loaded at startup: only the user commands, so they exist before
-- setup() runs. Starting the server is opt-in through
-- require('gohard').setup() or vim.lsp.enable('gohard').
if vim.g.loaded_gohard then
  return
end
vim.g.loaded_gohard = 1
if not require('gohard.compat').check() then
  return
end
require('gohard.commands').create()
