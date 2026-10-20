-- Headless Neovim end-to-end test of the GoHard plugin (#38, #25).
-- Run by test/run.sh:
--   nvim --headless --clean -l e2e.lua PLUGIN_DIR GOHARD_BIN WORK
-- WORK holds copies of a passing and a failing corpus testbench
-- (wait_forms, assert_fail) and the cross-file fixture. The test opens
-- each testbench, runs its "▶ Run" code lens, waits for the run to end
-- (the lens turns into "▶ Re-run · seed N") and checks the gohard-sim
-- diagnostics against `gohard lsp sim` on the same project; then it
-- checks hover, definition, references and rename across files, and
-- go-to-definition into the bundled ieee sources (md_resolve).

local plugin, bin, work = arg[1], arg[2], arg[3]
assert(plugin and bin and work, 'usage: e2e.lua PLUGIN_DIR GOHARD_BIN WORK')
vim.opt.runtimepath:prepend(plugin)
vim.opt.swapfile = false
vim.opt.shortmess:append('A')

local failures = 0
local function log(fmt, ...)
  io.stdout:write(string.format(fmt, ...) .. '\n')
end
local function check(ok, fmt, ...)
  if ok then
    log('ok   ' .. fmt, ...)
  else
    failures = failures + 1
    log('FAIL ' .. fmt, ...)
  end
  return ok
end
local function wait(ms, cond)
  return vim.wait(ms, cond, 20)
end

local gohard = require('gohard')
local commands = require('gohard.commands')
local compat = require('gohard.compat')
gohard.setup({ cmd = { bin, 'lsp' } })

--- open edits path and waits for an initialized gohard client.
local function open(path)
  vim.cmd.edit(vim.fn.fnameescape(path))
  local buf = vim.api.nvim_get_current_buf()
  local attached = wait(20000, function()
    local c = gohard.client(buf)
    return c ~= nil and c.initialized
  end)
  check(attached, 'gohard attached to %s', vim.fs.basename(path))
  return buf, gohard.client(buf)
end

local function sim_diags(buf)
  local out = {}
  for _, d in ipairs(vim.diagnostic.get(buf)) do
    local code = d.code and tostring(d.code) or ''
    if d.source == 'gohard-sim' or code:find('^sim/') then
      table.insert(out, string.format('%d:%d %d %s %s', d.lnum + 1,
        d.col + 1, d.severity, code, d.message))
    end
  end
  table.sort(out)
  return out
end

local sev = { error = 1, warning = 2, note = 4, hint = 4, info = 3 }

--- expected runs `gohard lsp sim --tb TB` in dir: the diagnostics the
--- Run code lens must publish (#26: the same run as the lens).
local function expected(dir, tb, file)
  local r = vim.system({ bin, 'lsp', 'sim', '--tb', tb }, { cwd = dir })
    :wait()
  local ok, j = pcall(vim.json.decode, r.stdout or '')
  if not check(ok and j, 'gohard lsp sim --tb %s ran', tb) then
    return {}, -1
  end
  local out = {}
  for _, d in ipairs(j.diagnostics) do
    if d.path == file then
      table.insert(out, string.format('%d:%d %d %s %s', d.line, d.col,
        sev[d.severity] or 0, d.code, d.message))
    end
  end
  table.sort(out)
  return out, r.code
end

local function lens_of(buf, kinds)
  for _, l in ipairs((commands.lenses(buf))) do
    if vim.list_contains(kinds, commands.kind(l)) then
      return l
    end
  end
end

local function testbench(name, want_exit)
  local dir = work .. '/' .. name
  local tb, file = name .. '_tb', name .. '_tb.vhd'
  log('-- testbench %s', tb)
  local buf, client = open(dir .. '/' .. file)
  if not client then
    return
  end
  local run
  wait(20000, function()
    run = lens_of(buf, { 'run' })
    return run ~= nil
  end)
  if not check(run ~= nil, 'a "▶ Run" lens for %s', tb) then
    return
  end
  check(run.command.title:find(tb, 1, true) ~= nil,
    'lens title %q names %s', run.command.title, tb)
  -- Trigger the lens the way :GoHardRun does.
  vim.api.nvim_win_set_cursor(0, { run.range.start.line + 1, 0 })
  vim.cmd('GoHardRun')
  local rerun
  local done = wait(60000, function()
    rerun = lens_of(buf, { 'rerun' })
    return rerun ~= nil
  end)
  check(done, 'the lens turns into "▶ Re-run · seed N" (%s)',
    rerun and rerun.command.title or 'no re-run lens')
  local want, code = expected(dir, tb, file)
  check(code == want_exit, 'gohard lsp sim exit %d, want %d', code,
    want_exit)
  local got
  wait(10000, function()
    got = sim_diags(buf)
    return vim.deep_equal(got, want)
  end)
  check(vim.deep_equal(got, want),
    'gohard-sim diagnostics equal gohard lsp sim:\n  got  %s\n  want %s',
    table.concat(got, '\n       '), table.concat(want, '\n       '))
  if want_exit == 1 then
    local errs = 0
    for _, d in ipairs(vim.diagnostic.get(buf,
      { severity = vim.diagnostic.severity.ERROR })) do
      errs = errs + 1
    end
    check(errs > 0, 'the failing testbench has an error diagnostic')
  end
  -- Copy repro puts the repro command in the unnamed register.
  vim.fn.setreg('"', '')
  if check(lens_of(buf, { 'repro' }) ~= nil, 'a "Copy repro" lens') then
    vim.cmd('GoHardCopyRepro')
    wait(10000, function()
      return vim.fn.getreg('"') ~= ''
    end)
    check(vim.fn.getreg('"') ~= '', 'Copy repro copied %q',
      vim.fn.getreg('"'))
  end
  -- "Open waveform" is offered after a run, as a code action (or
  -- lens) from the server; the plugin has no viewer setting (#36).
  local p = vim.lsp.util.make_range_params(0, 'utf-8')
  p.context = { diagnostics = {} }
  local acts = compat.request_sync(client, 'textDocument/codeAction', p,
    20000, buf)
  local titles = {}
  for _, a in ipairs(acts and acts.result or {}) do
    table.insert(titles, a.title)
  end
  for _, l in ipairs((commands.lenses(buf))) do
    table.insert(titles, l.command and l.command.title or '')
  end
  local has = false
  for _, t in ipairs(titles) do
    has = has or t:find('[Ww]aveform') ~= nil
  end
  check(has, '"Open waveform" is offered (%s)', table.concat(titles, ', '))
  if want_exit ~= 1 then
    return
  end
  -- Re-run with another seed: the lens and diagnostics follow it.
  vim.cmd('GoHardRerun 3')
  local seeded = wait(60000, function()
    local l = lens_of(buf, { 'rerun' })
    return l ~= nil and l.command.title:find('seed 3', 1, true) ~= nil
  end)
  check(seeded, ':GoHardRerun 3 re-runs at seed 3')
  local r = vim.system({ bin, 'lsp', 'sim', '--tb', tb, '--seed', '3' },
    { cwd = dir }):wait()
  local okj, j = pcall(vim.json.decode, r.stdout or '')
  local want3 = {}
  for _, d in ipairs(okj and j.diagnostics or {}) do
    if d.path == file then
      table.insert(want3, string.format('%d:%d %d %s %s', d.line, d.col,
        sev[d.severity] or 0, d.code, d.message))
    end
  end
  table.sort(want3)
  wait(10000, function()
    got = sim_diags(buf)
    return vim.deep_equal(got, want3)
  end)
  check(#want3 > 0 and vim.deep_equal(got, want3),
    'seed 3 diagnostics equal gohard lsp sim --seed 3')
end

local function at(buf, line, col)
  vim.api.nvim_win_set_buf(0, buf)
  vim.api.nvim_win_set_cursor(0, { line, col - 1 })
  return vim.lsp.util.make_position_params(0, 'utf-8')
end

local function request(client, buf, method, params)
  local r = compat.request_sync(client, method, params, 20000, buf)
  if not r then
    return nil, 'timeout'
  end
  return r.result, r.err
end

local function cross_file()
  log('-- cross-file hover, definition, references, rename')
  local dir = work .. '/cross-file'
  local top, client = open(dir .. '/top.vhd')
  if not client then
    return
  end
  -- Wait for the first diagnostics: the workspace is analyzed.
  wait(20000, function()
    return #vim.diagnostic.get(top) > 0
  end)
  -- "width" in `bit_vector(width - 1 downto 0)`, declared in pkg.vhd.
  local line = vim.api.nvim_buf_get_lines(top, 6, 7, false)[1]
  local col = line:find('width', 1, true)
  check(col ~= nil, 'top.vhd line 7 uses width')
  local p = at(top, 7, col + 1)

  local h, err = request(client, top, 'textDocument/hover', p)
  local htext = h and h.contents and (h.contents.value or
    vim.inspect(h.contents)) or ''
  check(err == nil and htext:lower():find('width') ~= nil,
    'hover on width: %s', err and vim.inspect(err) or htext)

  local d
  d, err = request(client, top, 'textDocument/definition', p)
  if d and d.uri then
    d = { d }
  end
  local def = d and d[1]
  local duri = def and (def.uri or def.targetUri) or ''
  local drange = def and (def.range or def.targetSelectionRange)
  check(duri:find('pkg%.vhd$') ~= nil and drange and
    drange.start.line == 1,
    'definition of width is pkg.vhd:2 (%s)',
    err and vim.inspect(err) or vim.inspect(d))

  p.context = { includeDeclaration = true }
  local refs
  refs, err = request(client, top, 'textDocument/references', p)
  local files = {}
  for _, r in ipairs(refs or {}) do
    files[vim.fs.basename(vim.uri_to_fname(r.uri))] = true
  end
  check(files['pkg.vhd'] and files['top.vhd'],
    'references of width span pkg.vhd and top.vhd (%d found%s)',
    #(refs or {}), err and (': ' .. vim.inspect(err)) or '')

  p.context = nil
  local prep
  prep, err = request(client, top, 'textDocument/prepareRename', p)
  check(err == nil and prep ~= nil, 'prepareRename on width')
  p.newName = 'bus_width'
  local edit
  edit, err = request(client, top, 'textDocument/rename', p)
  if not check(err == nil and edit ~= nil, 'rename width to bus_width%s',
    err and (': ' .. vim.inspect(err)) or '') then
    return
  end
  vim.lsp.util.apply_workspace_edit(edit, client.offset_encoding)
  local pkg = vim.fn.bufnr(dir .. '/pkg.vhd')
  local function text(b)
    return table.concat(vim.api.nvim_buf_get_lines(b, 0, -1, false), '\n')
  end
  check(pkg > 0 and text(pkg):find('constant bus_width', 1, true) ~= nil,
    'the declaration in pkg.vhd is renamed')
  check(text(top):find('bit_vector(bus_width - 1', 1, true) ~= nil and
    text(top):find('%f[%w_]width%f[^%w_]') == nil,
    'every use in top.vhd is renamed')
  -- The renamed project still resolves: definition from the new name.
  vim.cmd('silent! wall')
  wait(500, function()
    return false
  end)
  d = request(client, top, 'textDocument/definition', at(top, 7, col + 1))
  if d and d.uri then
    d = { d }
  end
  check(d and d[1] and (d[1].uri or d[1].targetUri):find('pkg%.vhd$'),
    'definition after the rename still finds pkg.vhd')
end

--- bundle: go-to-definition on std_logic opens the read-only cached
--- copy of ieee/std_logic_1164.vhdl (#33), with no error, attached to
--- the same client, and hover inside it is the bundled unit's.
local function bundle()
  log('-- definition into the bundled ieee sources')
  local tb, client = open(work .. '/md_resolve/md_resolve_tb.vhd')
  if not client then
    return
  end
  wait(20000, function()
    return #vim.diagnostic.get(tb) > 0 or false
  end)
  local lines = vim.api.nvim_buf_get_lines(tb, 0, -1, false)
  local ln, col
  for i, l in ipairs(lines) do
    col = l:find(': std_logic :=', 1, true)
    if col then
      ln, col = i, col + 2
      break
    end
  end
  if not check(ln ~= nil, 'md_resolve_tb.vhd declares a std_logic signal') then
    return
  end
  vim.api.nvim_win_set_buf(0, tb)
  vim.api.nvim_win_set_cursor(0, { ln, col })
  vim.v.errmsg = ''
  local ok, e = pcall(vim.lsp.buf.definition)
  local want = 'ieee/std_logic_1164.vhdl'
  local jumped = wait(20000, function()
    local n = vim.api.nvim_buf_get_name(0)
    return n:sub(-#want) == want
  end)
  local name = vim.api.nvim_buf_get_name(0)
  check(ok and jumped and vim.v.errmsg == '',
    'definition of std_logic opens %s%s', name,
    (not ok and (': ' .. tostring(e))) or
    (vim.v.errmsg ~= '' and (': ' .. vim.v.errmsg)) or '')
  if not jumped then
    return
  end
  local cache = vim.env.XDG_CACHE_HOME .. '/gohard/bundle/'
  check(name:sub(1, #cache) == cache, 'it is under %s', cache)
  check(vim.fn.filewritable(name) == 0, 'the cached file is read-only')
  local buf = vim.api.nvim_get_current_buf()
  local attached = wait(20000, function()
    local c = gohard.client(buf)
    return c ~= nil and c.initialized
  end)
  local c = gohard.client(buf)
  check(attached and c.id == client.id,
    'the cached file is on the same gohard client')
  if not attached then
    return
  end
  local blines = vim.api.nvim_buf_get_lines(buf, 0, -1, false)
  local hl, hc
  for i, l in ipairs(blines) do
    hc = l:find('STD_ULOGIC;', 1, true)
    if hc then
      hl = i
      break
    end
  end
  local h, err = request(c, buf, 'textDocument/hover', at(buf, hl, hc + 1))
  local htext = h and h.contents and (h.contents.value or '') or ''
  check(err == nil and htext:find('from ieee.std_logic_1164', 1, true),
    'hover inside the cached file is the bundled unit: %s',
    err and vim.inspect(err) or htext)
end

-- GOHARD_E2E_ONLY=name runs one part: cross-file, bundle, wait_forms or
-- assert_fail. GOHARD_E2E_LOG=1 writes the LSP log at debug level.
local only = vim.env.GOHARD_E2E_ONLY
if vim.env.GOHARD_E2E_LOG == '1' then
  vim.lsp.log.set_level('debug')
  log('LSP log: %s', vim.lsp.log.get_filename())
end
local function part(name, fn, ...)
  if only == nil or only == '' or only == name then
    fn(...)
  end
end
local ok, e = pcall(function()
  part('cross-file', cross_file)
  part('bundle', bundle)
  part('wait_forms', testbench, 'wait_forms', 0)
  part('assert_fail', testbench, 'assert_fail', 1)
end)
check(ok, 'the test ran to the end%s', ok and '' or (': ' .. tostring(e)))

for _, c in ipairs(vim.lsp.get_clients({ name = 'gohard' })) do
  compat.stop(c)
end
wait(5000, function()
  return #vim.lsp.get_clients({ name = 'gohard' }) == 0
end)
log(failures == 0 and 'PASS' or string.format('FAIL (%d)', failures))
os.exit(failures == 0 and 0 or 1)
