-- Neovim config for the README screenshots. Loaded with
-- `nvim -n --noplugin -i NONE -u docs/demo/init.lua`. It starts the
-- GoHard plugin in this repository against $GOHARD_BIN and, when
-- $GOHARD_SCENE is set, leaves the editor on that scene. The symbols
-- scene lists each declaration with its LSP SymbolKind.

local demo = vim.fs.dirname(debug.getinfo(1, 'S').source:sub(2))
local repo = vim.fs.dirname(vim.fs.dirname(demo))

vim.opt.runtimepath:prepend(repo .. '/editors/nvim')

vim.opt.hidden = true
vim.opt.swapfile = false
vim.opt.backup = false
vim.opt.writebackup = false
vim.opt.undofile = false
vim.opt.shadafile = 'NONE'
vim.opt.shortmess:append('IF')
vim.opt.number = true
vim.opt.signcolumn = 'yes'
vim.opt.cursorline = true
vim.opt.termguicolors = true
vim.opt.background = 'dark'
vim.opt.wrap = false
vim.opt.showmode = true
vim.opt.cmdheight = 1
vim.opt.laststatus = 2
vim.opt.scrolloff = 3
vim.opt.winborder = 'rounded'
vim.opt.fillchars:append({ eob = ' ' })
vim.opt.statusline = ' %{v:lua.gohard_file()} %m%=%{v:lua.gohard_status()} %l:%c '

-- Paths the language server opens are absolute. Show them from the
-- repository root so a screenshot does not depend on the machine.
function _G.gohard_file()
  local name = vim.api.nvim_buf_get_name(0)
  local cwd = vim.fn.getcwd()
  if cwd ~= '' and name:sub(1, #cwd + 1) == cwd .. '/' then
    return name:sub(#cwd + 2)
  end
  return name
end

function _G.gohard_status()
  local c = vim.lsp.get_clients({ bufnr = 0, name = 'gohard' })[1]
  if c ~= nil and c.initialized then
    return 'gohard '
  end
  return ''
end
vim.opt.completeopt = 'menu,menuone,noinsert,noselect'
vim.opt.pumheight = 10
vim.opt.mouse = ''
vim.opt.more = false
vim.g.markdown_fenced_languages = { 'vhdl' }

pcall(vim.cmd.colorscheme, 'catppuccin')
if vim.g.colors_name ~= 'catppuccin' then
  vim.cmd.colorscheme('unokai')
end

vim.diagnostic.config({
  virtual_text = { spacing = 2 },
  signs = {
    text = {
      [vim.diagnostic.severity.ERROR] = 'E',
      [vim.diagnostic.severity.WARN] = 'W',
      [vim.diagnostic.severity.INFO] = 'I',
      [vim.diagnostic.severity.HINT] = 'H',
    },
  },
  underline = true,
  severity_sort = true,
  float = { border = 'rounded', source = true },
})

if vim.lsp.log ~= nil and vim.lsp.log.set_level ~= nil then
  vim.lsp.log.set_level(vim.log.levels.ERROR)
end

local bin = vim.env.GOHARD_BIN
if bin == nil or bin == '' then
  bin = 'gohard'
end
require('gohard').setup({
  cmd = { bin, 'lsp' },
  codelens = false,
  keymaps = false,
})

package.path = demo .. '/?.lua;' .. package.path

local scene = vim.env.GOHARD_SCENE
if scene ~= nil and scene ~= '' then
  -- After VimEnter, so :edit sets 'filetype' and startinsert sticks.
  -- Running the scene inside VimEnter leaves insert mode pending and
  -- the completion menu never opens.
  vim.api.nvim_create_autocmd('VimEnter', {
    once = true,
    callback = function()
      vim.schedule(function()
        require('scene').run(scene, demo)
      end)
    end,
  })
end
