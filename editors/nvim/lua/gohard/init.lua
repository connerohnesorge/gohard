-- GoHard for Neovim (#38): starts `gohard lsp` over stdio for VHDL
-- buffers, shows the simulation code lens ("▶ Run tb_top", then
-- "◉ Running…", then "▶ Re-run · seed 42" and "Copy repro") and adds
-- the GoHard commands. Everything else (diagnostics, hover,
-- definition, references, rename, completion, symbols, formatting,
-- folding, highlights, code actions such as "Open waveform") is plain
-- LSP that Neovim already speaks, so the plugin adds no client-side
-- logic for it. There is no viewer option: the server picks the
-- waveform viewer itself (#36).
--
-- Usage:
--   require('gohard').setup()            -- defaults
--   require('gohard').setup({ cmd = { '/path/to/gohard', 'lsp' } })

local M = {}

---@class gohard.Options
---@field cmd? string[]            server command (default gohard lsp)
---@field filetypes? string[]      default { 'vhdl' }
---@field root_markers? string[]   default { 'gohard.yaml', '.git' }
---@field settings? table          editor settings sent to the server
---@field capabilities? table      merged into the client capabilities
---@field on_attach? fun(client: vim.lsp.Client, bufnr: integer)
---@field codelens? boolean        show and refresh code lenses (true)
---@field keymaps? boolean         buffer-local <leader>g… maps (false)

M.defaults = {
  cmd = { 'gohard', 'lsp' },
  filetypes = { 'vhdl' },
  root_markers = { 'gohard.yaml', '.git' },
  settings = {},
  codelens = true,
  keymaps = false,
}

---@type gohard.Options
M.options = vim.deepcopy(M.defaults)

M.name = 'gohard'

local augroup = vim.api.nvim_create_augroup('gohard', { clear = true })

--- Returns the running gohard client of bufnr (current buffer if nil).
---@return vim.lsp.Client?
function M.client(bufnr)
  local clients = vim.lsp.get_clients({
    bufnr = bufnr or vim.api.nvim_get_current_buf(),
    name = M.name,
  })
  return clients[1]
end

local function refresh_lenses(bufnr)
  if not M.options.codelens then
    return
  end
  if vim.lsp.codelens.enable then -- Neovim 0.12+
    vim.lsp.codelens.enable(true, { bufnr = bufnr })
  else
    vim.lsp.codelens.refresh({ bufnr = bufnr })
  end
end
M.refresh_lenses = refresh_lenses

local function on_attach(client, bufnr)
  if client.server_capabilities.codeLensProvider then
    refresh_lenses(bufnr)
    if not vim.lsp.codelens.enable then
      -- Before 0.12 lenses are only fetched on request.
      vim.api.nvim_create_autocmd(
        { 'BufEnter', 'InsertLeave', 'BufWritePost' },
        {
          group = augroup,
          buffer = bufnr,
          callback = function()
            refresh_lenses(bufnr)
          end,
        }
      )
    end
  end
  if M.options.keymaps then
    local map = function(lhs, rhs, desc)
      vim.keymap.set('n', lhs, rhs, { buffer = bufnr, desc = desc })
    end
    map('<leader>gr', '<cmd>GoHardRun<cr>', 'GoHard: run testbench')
    map('<leader>gs', '<cmd>GoHardRerun<cr>', 'GoHard: re-run')
    map('<leader>gc', '<cmd>GoHardCopyRepro<cr>', 'GoHard: copy repro')
    map('<leader>gw', '<cmd>GoHardOpenWaveform<cr>', 'GoHard: waveform')
  end
  if M.options.on_attach then
    M.options.on_attach(client, bufnr)
  end
end

--- The client configuration, lspconfig style.
---@return vim.lsp.ClientConfig
function M.config()
  local o = M.options
  local caps = vim.lsp.protocol.make_client_capabilities()
  -- utf-8 positions are GoHard's native encoding (#25).
  caps.general = caps.general or {}
  caps.general.positionEncodings = { 'utf-8', 'utf-16' }
  caps = vim.tbl_deep_extend('force', caps, o.capabilities or {})
  return {
    name = M.name,
    cmd = o.cmd,
    filetypes = o.filetypes,
    root_markers = o.root_markers,
    settings = o.settings,
    capabilities = caps,
    on_attach = on_attach,
  }
end

local function root_dir(bufnr)
  local fname = vim.api.nvim_buf_get_name(bufnr)
  local found = vim.fs.find(M.options.root_markers, {
    path = vim.fs.dirname(fname),
    upward = true,
  })[1]
  return found and vim.fs.dirname(found) or vim.fs.dirname(fname)
end

--- setup starts gohard for every buffer of a configured filetype.
---@param opts? gohard.Options
function M.setup(opts)
  M.options = vim.tbl_deep_extend('force', vim.deepcopy(M.defaults),
    opts or {})
  require('gohard.commands').create()
  vim.filetype.add({ extension = { vhd = 'vhdl', vhdl = 'vhdl' } })
  local cfg = M.config()
  vim.api.nvim_clear_autocmds({ group = augroup })
  vim.api.nvim_create_autocmd('FileType', {
    group = augroup,
    pattern = M.options.filetypes,
    callback = function(ev)
      local c = vim.deepcopy(cfg)
      c.on_attach = on_attach
      c.root_dir = root_dir(ev.buf)
      c.workspace_folders = {
        { uri = vim.uri_from_fname(c.root_dir), name = c.root_dir },
      }
      vim.lsp.start(c, { bufnr = ev.buf })
    end,
  })
  -- Buffers that were open before setup.
  for _, b in ipairs(vim.api.nvim_list_bufs()) do
    if vim.api.nvim_buf_is_loaded(b) and
        vim.list_contains(M.options.filetypes, vim.bo[b].filetype) then
      vim.api.nvim_exec_autocmds('FileType', {
        group = augroup, buffer = b,
      })
    end
  end
end

return M
