-- Drives one README scene, or the headless probe that checks the
-- language server answered before anything is recorded.
--
-- Scenes: hover, completion, definition, references, signature,
-- symbols, diagnostics. Scene "probe" checks all of them and exits.
-- The symbols scene fills a quickfix list whose lines are the LSP
-- SymbolKind and the declaration name.

local M = {}

local function fail(msg)
  io.stderr:write('demo: ' .. msg .. '\n')
  error(msg, 0)
end

local function wait_until(ms, cond, what)
  local ok = vim.wait(ms, cond, 30)
  if not ok then
    fail(what .. ' timed out')
  end
end

local function client_of(buf)
  return vim.lsp.get_clients({ bufnr = buf, name = 'gohard' })[1]
end

-- edit_vhdl opens path. A :edit during VimEnter can leave 'filetype'
-- unset until the autocmd finishes, so set it explicitly. That is what
-- starts the language server.
local function edit_vhdl(path)
  vim.cmd.edit(vim.fn.fnameescape(path))
  if vim.bo.filetype ~= 'vhdl' then
    vim.bo.filetype = 'vhdl'
  end
end

local function wait_client(buf)
  wait_until(20000, function()
    local c = client_of(buf)
    return c ~= nil and c.initialized
  end, 'gohard attach')
  return client_of(buf)
end

--- place moves the cursor to the nth plain-text occurrence of substr.
--- The column is the start of the match, plus offset.
local function place(substr, nth, offset)
  nth = nth or 1
  offset = offset or 0
  local seen = 0
  local lines = vim.api.nvim_buf_get_lines(0, 0, -1, false)
  for i, line in ipairs(lines) do
    local from = 1
    while true do
      local col = string.find(line, substr, from, true)
      if col == nil then
        break
      end
      seen = seen + 1
      if seen == nth then
        vim.api.nvim_win_set_cursor(0, { i, col - 1 + offset })
        vim.cmd('normal! zz')
        return i
      end
      from = col + 1
    end
  end
  fail('text not in buffer: ' .. substr)
end

local function position_params()
  local cursor = vim.api.nvim_win_get_cursor(0)
  return {
    textDocument = { uri = vim.uri_from_bufnr(0) },
    position = { line = cursor[1] - 1, character = cursor[2] },
  }
end

local function request(method, params)
  local results = vim.lsp.buf_request_sync(0, method, params, 15000)
  if results == nil then
    fail(method .. ' returned nothing')
  end
  for _, r in pairs(results) do
    if r.error then
      fail(method .. ' error: ' .. vim.inspect(r.error))
    end
    return r.result
  end
  fail(method .. ' had no client result')
end

local function float_open()
  for _, win in ipairs(vim.api.nvim_list_wins()) do
    local cfg = vim.api.nvim_win_get_config(win)
    if cfg.relative ~= nil and cfg.relative ~= '' then
      return true
    end
  end
  return false
end

local function open_hover()
  place('sum => added', 1, #'sum => ')
  vim.lsp.buf.hover()
  wait_until(8000, float_open, 'hover window')
  vim.cmd.redraw()
end

local function open_completion()
  local row = place('res <= pick', 1)
  -- A short prefix keeps the menu to the local function. A bare "to_"
  -- also matches every ieee conversion and fills the screen.
  vim.api.nvim_buf_set_lines(0, row - 1, row, false, { '  res <= pic' })
  -- Room under the cursor so the menu is not drawn over the next line
  -- of code.
  vim.api.nvim_buf_set_lines(0, row, row, false, { '', '', '' })
  vim.api.nvim_win_set_cursor(0, { row, #'  res <= pic' })
  local client = client_of(0)
  vim.lsp.completion.enable(true, client.id, 0, { autotrigger = false })
  -- The half-typed name is an error until it is accepted. Leave the
  -- menu as the thing on screen.
  vim.diagnostic.enable(false, { bufnr = 0 })
  -- The buffer change is debounced before it reaches the server.
  vim.wait(400)
  -- startinsert applies only once the call stack is back at the main
  -- loop, so a timer (not vim.schedule) opens the menu.
  vim.cmd('startinsert!')
  vim.defer_fn(function()
    if vim.fn.mode() ~= 'i' then
      fail('not in insert mode (' .. vim.fn.mode() .. ')')
    end
    vim.lsp.completion.get()
    wait_until(8000, function()
      return vim.fn.pumvisible() == 1
    end, 'completion menu')
    vim.cmd.redraw()
  end, 80)
end

local function open_definition()
  place('work.adder', 1, #'work.')
  vim.lsp.buf.definition()
  wait_until(8000, function()
    local name = vim.api.nvim_buf_get_name(0)
    return name:find('adder.vhd', 1, true) ~= nil
      and vim.api.nvim_get_current_line():find('entity adder', 1, true) ~= nil
  end, 'definition jump')
  vim.cmd('normal! zz')
  vim.cmd.redraw()
end

local function open_references()
  place('signal added', 1, #'signal ')
  vim.lsp.buf.references(nil, {
    includeDeclaration = true,
    on_list = function(opts)
      opts.title = 'References'
      vim.fn.setloclist(0, {}, ' ', opts)
      vim.cmd('botright lopen 8')
    end,
  })
  wait_until(8000, function()
    return vim.fn.getloclist(0, { size = 1 }).size > 1
  end, 'references list')
  vim.cmd.redraw()
end

local function open_signature()
  -- The declaration also contains "pick("; the call is the one with
  -- an actual.
  place('pick(op', 1, #'pick(')
  vim.lsp.buf.signature_help()
  wait_until(8000, float_open, 'signature help')
  vim.cmd.redraw()
end

-- LSP SymbolKind names, in protocol order. The outline prints these
-- rather than Neovim's own quickfix text, which leads with the path.
local symbol_kinds = {
  [1] = 'File',
  [2] = 'Module',
  [3] = 'Namespace',
  [4] = 'Package',
  [5] = 'Class',
  [6] = 'Method',
  [7] = 'Property',
  [8] = 'Field',
  [9] = 'Constructor',
  [10] = 'Enum',
  [11] = 'Interface',
  [12] = 'Function',
  [13] = 'Variable',
  [14] = 'Constant',
  [15] = 'String',
  [16] = 'Number',
  [17] = 'Boolean',
  [18] = 'Array',
  [19] = 'Object',
  [20] = 'Key',
  [21] = 'Null',
  [22] = 'EnumMember',
  [23] = 'Struct',
  [24] = 'Event',
  [25] = 'Operator',
  [26] = 'TypeParameter',
}

local function each_symbol(symbols, fn)
  if type(symbols) ~= 'table' then
    return
  end
  for _, s in ipairs(symbols) do
    if type(s) == 'table' and s.name ~= nil then
      fn(s)
      each_symbol(s.children, fn)
    end
  end
end

local function symbol_line(symbol, depth)
  local kind = symbol_kinds[symbol.kind] or ('Kind' .. tostring(symbol.kind))
  return string.format('%s%-12s %s', string.rep('  ', depth), kind, symbol.name)
end

local function symbol_items(symbols, depth, items)
  for _, s in ipairs(symbols) do
    local range = s.selectionRange
    local filename = vim.api.nvim_buf_get_name(0)
    if range == nil and type(s.location) == 'table' then
      range = s.location.range
      if s.location.uri ~= nil then
        filename = vim.uri_to_fname(s.location.uri)
      end
    end
    if type(range) == 'table' and type(range.start) == 'table' then
      items[#items + 1] = {
        filename = filename,
        lnum = range.start.line + 1,
        col = range.start.character + 1,
        text = symbol_line(s, depth),
      }
    end
    if type(s.children) == 'table' then
      symbol_items(s.children, depth + 1, items)
    end
  end
end

-- The quickfix window draws whatever this returns, so the screenshot
-- shows the kind and the name and not the file path.
function _G.gohard_symbol_text(info)
  local query = { id = info.id, items = 1 }
  local list
  if info.quickfix == 1 then
    list = vim.fn.getqflist(query)
  else
    list = vim.fn.getloclist(info.winid, query)
  end
  local lines = {}
  for i = info.start_idx, info.end_idx do
    local item = list.items[i]
    lines[#lines + 1] = (item and item.text) or ''
  end
  return lines
end

local function open_symbols()
  local result = request('textDocument/documentSymbol', {
    textDocument = { uri = vim.uri_from_bufnr(0) },
  })
  if type(result) ~= 'table' or result[1] == nil then
    fail('document symbols empty')
  end
  local items = {}
  symbol_items(result, 0, items)
  local shown = {}
  for _, item in ipairs(items) do
    shown[item.text] = true
  end
  -- These are the kinds the screenshot is for. A wrong mapping fails
  -- here, before the tape records. The line is built the same way as
  -- the quickfix text, so the padding cannot drift.
  local function expect(depth, kind, name)
    local line = symbol_line({ kind = kind, name = name }, depth)
    if shown[line] == nil then
      fail('outline missing "' .. line .. '"\n' .. vim.inspect(items))
    end
  end
  expect(0, 11, 'alu')
  expect(1, 13, 'op')
  expect(1, 13, 'added')
  expect(0, 2, 'rtl')
  expect(1, 12, 'pick')
  expect(1, 19, 'u_add')
  expect(1, 6, 'tick')
  vim.cmd('normal! gg')
  vim.o.quickfixtextfunc = 'v:lua.gohard_symbol_text'
  vim.fn.setqflist({}, ' ', {
    title = 'Document symbols',
    items = items,
  })
  vim.cmd('botright copen ' .. tostring(#items))
  vim.cmd.redraw()
end

local function wait_diags(buf, pred, what)
  local got
  wait_until(15000, function()
    got = vim.diagnostic.get(buf)
    return pred(got)
  end, what)
  return got
end

local function open_diagnostics(demo)
  -- One file carries the lint warnings and an undeclared name.
  edit_vhdl(demo .. '/src/lint_me.vhd')
  local buf = vim.api.nvim_get_current_buf()
  wait_client(buf)
  wait_diags(buf, function(ds)
    local warn, err = false, false
    for _, d in ipairs(ds) do
      if d.severity == vim.diagnostic.severity.WARN then
        warn = true
      end
      if d.severity == vim.diagnostic.severity.ERROR
        and d.message:find('not declared', 1, true) then
        err = true
      end
    end
    return warn and err
  end, 'lint warnings beside an error')
  place('missing', 1)
  -- The file fits the window. zz centered the error and scrolled the
  -- unused-port warning off the top; keep the whole file in view.
  if vim.api.nvim_buf_line_count(0) <= vim.api.nvim_win_get_height(0) then
    vim.fn.winrestview({ topline = 1 })
  end
  vim.cmd.redraw()
end

local scenes = {
  hover = open_hover,
  completion = open_completion,
  definition = open_definition,
  references = open_references,
  signature = open_signature,
  symbols = open_symbols,
}

local function text_of(value)
  if value == nil then
    return ''
  end
  if type(value) == 'string' then
    return value
  end
  return vim.inspect(value)
end

local function probe(demo)
  local failures = {}
  local function check(ok, label, detail)
    if ok then
      io.stdout:write('ok   ' .. label .. '\n')
    else
      local msg = label
      if detail ~= nil and detail ~= '' then
        msg = label .. '\n' .. detail
      end
      failures[#failures + 1] = msg
      io.stderr:write('FAIL ' .. msg .. '\n')
    end
  end

  local function summarize_diags(ds)
    local lines = {}
    for _, d in ipairs(ds) do
      lines[#lines + 1] = string.format('L%d %s %s %s', d.lnum + 1,
        d.severity, tostring(d.code), d.message)
    end
    return table.concat(lines, '\n')
  end

  wait_client(0)
  place('sum => added', 1, #'sum => ')
  local hover = request('textDocument/hover', position_params())
  local hover_text = text_of(hover and hover.contents)
  check(hover_text:find('added', 1, true) ~= nil
    and hover_text:find('UNSIGNED(7 downto 0)', 1, true) ~= nil
    and hover_text:lower():find('sum of the two operands', 1, true) ~= nil,
    'hover on added', hover_text)

  place('work.adder', 1, #'work.')
  local def = request('textDocument/definition', position_params())
  local def_text = text_of(def)
  check(def_text:find('adder.vhd', 1, true) ~= nil,
    'definition of work.adder', def_text)

  place('signal added', 1, #'signal ')
  local refs = request('textDocument/references', vim.tbl_extend('force',
    position_params(), { context = { includeDeclaration = true } }))
  local nref = type(refs) == 'table' and #refs or 0
  check(nref >= 3, 'references of added (' .. nref .. ')', text_of(refs))

  place('pick(op', 1, #'pick(')
  local sig = request('textDocument/signatureHelp', position_params())
  local sig_text = text_of(sig)
  check(sig ~= nil and sig_text:find('pick', 1, true) ~= nil,
    'signature help of pick', sig_text)

  local sym = request('textDocument/documentSymbol', {
    textDocument = { uri = vim.uri_from_bufnr(0) },
  })
  local kinds = {}
  each_symbol(sym, function(s)
    kinds[s.name] = s.kind
  end)
  local function kind_is(name, kind, label)
    check(kinds[name] == kind,
      label .. ' is ' .. (symbol_kinds[kind] or '?'),
      name .. '=' .. tostring(kinds[name]))
  end
  kind_is('alu', 11, 'entity alu')
  kind_is('op', 13, 'port op')
  kind_is('added', 13, 'signal added')
  kind_is('rtl', 2, 'architecture rtl')
  kind_is('pick', 12, 'function pick')
  kind_is('u_add', 19, 'instance u_add')
  kind_is('tick', 6, 'process tick')

  local row = place('res <= pick', 1)
  vim.api.nvim_buf_set_lines(0, row - 1, row, false, { '  res <= pic' })
  vim.cmd.startinsert({ bang = true })
  vim.wait(400)
  local comp = request('textDocument/completion', position_params())
  vim.cmd.stopinsert()
  local labels = {}
  local items = comp and (comp.items or comp) or {}
  if type(items) == 'table' then
    for _, it in ipairs(items) do
      if type(it) == 'table' and it.label ~= nil then
        labels[#labels + 1] = it.label .. ' | ' .. tostring(it.detail)
      end
    end
  end
  local comp_text = table.concat(labels, '\n')
  check(comp_text:find('pick', 1, true) ~= nil and #labels <= 8,
    'completion of pic (' .. #labels .. ')', comp_text)

  edit_vhdl(demo .. '/src/lint_me.vhd')
  wait_client(0)
  local diags = wait_diags(0, function(ds)
    local warn, err = false, false
    for _, d in ipairs(ds) do
      if d.severity == vim.diagnostic.severity.WARN then
        warn = true
      end
      if d.severity == vim.diagnostic.severity.ERROR
        and d.message:find('not declared', 1, true) then
        err = true
      end
    end
    return warn and err
  end, 'lint warnings beside an error')
  local diag_text = summarize_diags(diags)
  check(diag_text:find('lint/latch', 1, true) ~= nil
    and diag_text:find('lint/sensitivity-list', 1, true) ~= nil
    and diag_text:find('lint/unused-signal', 1, true) ~= nil
    and diag_text:find('not declared', 1, true) ~= nil,
    'lint warnings beside an error', diag_text)

  if #failures > 0 then
    os.exit(1)
  end
  io.stdout:write('probe ok\n')
  os.exit(0)
end

function M.run(scene, demo)
  if scene == 'probe' then
    local ok, err = pcall(probe, demo)
    if not ok then
      io.stderr:write('demo probe: ' .. tostring(err) .. '\n')
      os.exit(1)
    end
    return
  end
  if scene == 'diagnostics' then
    open_diagnostics(demo)
    return
  end
  wait_client(0)
  local fn = scenes[scene]
  if fn == nil then
    fail('unknown scene ' .. scene)
  end
  fn()
end

return M
