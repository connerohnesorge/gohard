-- The GoHard commands (#38). Each one works on the code lenses and
-- code actions the server sends, so the plugin knows no command names
-- of its own: a lens is found by its title, the labels of #25.
--
--   :GoHardRun            run the testbench lens nearest the cursor
--   :GoHardRerun [SEED]   re-run it, optionally with another seed
--   :GoHardCopyRepro      copy the repro command of the last run
--   :GoHardOpenWaveform   the server's "Open waveform" code action
--   :GoHardLenses         list the lenses of the buffer

local M = {}

local function gohard()
  return require('gohard')
end

-- Lens kinds by title (#25 code lens labels).
local kinds = {
  { kind = 'rerun', pat = '^▶%s*Re%-run' },
  { kind = 'run', pat = '^▶%s*Run' },
  { kind = 'running', pat = '^◉' },
  { kind = 'repro', pat = '[Cc]opy repro' },
}

--- kind returns 'run', 'rerun', 'running', 'repro' or nil for a lens.
function M.kind(lens)
  local title = lens.command and lens.command.title or ''
  for _, k in ipairs(kinds) do
    if title:find(k.pat) then
      return k.kind
    end
  end
end

--- lenses fetches the code lenses of bufnr synchronously.
---@return lsp.CodeLens[], vim.lsp.Client?
function M.lenses(bufnr)
  bufnr = bufnr or vim.api.nvim_get_current_buf()
  local client = gohard().client(bufnr)
  if not client then
    return {}, nil
  end
  local res = client:request_sync('textDocument/codeLens', {
    textDocument = vim.lsp.util.make_text_document_params(bufnr),
  }, 10000, bufnr)
  if not res or res.err or not res.result then
    return {}, client
  end
  local out = {}
  for _, l in ipairs(res.result) do
    if not l.command and client:supports_method('codeLens/resolve') then
      local r = client:request_sync('codeLens/resolve', l, 10000, bufnr)
      l = r and r.result or l
    end
    table.insert(out, l)
  end
  return out, client
end

--- nearest returns the lens of one of wanted kinds closest to the
--- cursor line (a lens on or above it wins over one below).
function M.nearest(lenses, wanted)
  local row = vim.api.nvim_win_get_cursor(0)[1] - 1
  local best, bestd
  for _, l in ipairs(lenses) do
    if vim.list_contains(wanted, M.kind(l)) then
      local d = row - l.range.start.line
      if d < 0 then
        d = 1e6 - d
      end
      if not bestd or d < bestd then
        best, bestd = l, d
      end
    end
  end
  return best
end

local function notify(msg, level)
  vim.notify('gohard: ' .. msg, level or vim.log.levels.INFO)
end

local function copy(text)
  vim.fn.setreg('"', text)
  pcall(vim.fn.setreg, '+', text)
  notify('copied ' .. text)
end

-- text of a command result or argument worth copying.
local function text_of(v)
  if type(v) == 'string' then
    return v
  end
  if type(v) == 'table' then
    for _, k in ipairs({ 'repro', 'command', 'text', 'path' }) do
      if type(v[k]) == 'string' then
        return v[k]
      end
    end
    if type(v[1]) == 'string' then
      return v[1]
    end
  end
end

--- execute runs a lens command: on the server when it advertises the
--- command, else as a client command (copying its first argument).
---@param on_result? fun(err: any, result: any)
function M.execute(client, bufnr, cmd, on_result)
  local provider = client.server_capabilities.executeCommandProvider
  local known = type(provider) == 'table' and provider.commands or {}
  if not vim.list_contains(known, cmd.command) and
      not vim.lsp.commands[cmd.command] then
    local t = text_of(cmd.arguments and cmd.arguments[1])
    if t then
      copy(t)
    else
      notify('the server does not run ' .. cmd.command,
        vim.log.levels.WARN)
    end
    if on_result then
      on_result(nil, nil) -- already copied
    end
    return
  end
  client:exec_cmd(cmd, { bufnr = bufnr }, function(err, result, ctx)
    if err then
      notify(err.message or tostring(err), vim.log.levels.ERROR)
    end
    gohard().refresh_lenses(bufnr)
    if on_result then
      on_result(err, result, ctx)
    end
  end)
end

local function run_kind(wanted, mutate)
  local bufnr = vim.api.nvim_get_current_buf()
  local lenses, client = M.lenses(bufnr)
  if not client then
    return notify('no gohard client for this buffer', vim.log.levels.WARN)
  end
  local l = M.nearest(lenses, wanted)
  if not l then
    return notify('no ' .. table.concat(wanted, '/') .. ' lens here',
      vim.log.levels.WARN)
  end
  local cmd = vim.deepcopy(l.command)
  if mutate then
    mutate(cmd)
  end
  M.execute(client, bufnr, cmd, function(err, result)
    if not err and wanted[1] == 'repro' then
      local t = text_of(result)
      if t and result ~= nil then
        copy(t)
      end
    end
  end)
end

function M.run()
  run_kind({ 'run', 'rerun' })
end

--- rerun re-runs the testbench; seed (a number or "random") replaces
--- the seed of the lens arguments when given.
function M.rerun(seed)
  run_kind({ 'rerun', 'run' }, function(cmd)
    if seed == nil or seed == '' then
      return
    end
    local s = tonumber(seed) or seed
    cmd.arguments = cmd.arguments or {}
    if type(cmd.arguments[1]) == 'table' then
      cmd.arguments[1].seed = s
    else
      table.insert(cmd.arguments, { seed = s })
    end
  end)
end

function M.copy_repro()
  run_kind({ 'repro' })
end

function M.open_waveform()
  vim.lsp.buf.code_action({
    apply = true,
    filter = function(a)
      return (a.title or ''):find('[Ww]aveform') ~= nil
    end,
  })
end

function M.list()
  local lenses = M.lenses()
  for _, l in ipairs(lenses) do
    print(string.format('%d: %s [%s]', l.range.start.line + 1,
      l.command and l.command.title or '?', M.kind(l) or '-'))
  end
end

local created = false

function M.create()
  if created then
    return
  end
  created = true
  local cmd = vim.api.nvim_create_user_command
  cmd('GoHardRun', M.run, { desc = 'Run the testbench under the cursor' })
  cmd('GoHardRerun', function(o)
    M.rerun(o.args)
  end, { nargs = '?', desc = 'Re-run the testbench [seed|random]' })
  cmd('GoHardCopyRepro', M.copy_repro,
    { desc = 'Copy the repro command of the last run' })
  cmd('GoHardOpenWaveform', M.open_waveform,
    { desc = 'Open the waveform of the last run' })
  cmd('GoHardLenses', M.list, { desc = 'List the GoHard code lenses' })
end

return M
