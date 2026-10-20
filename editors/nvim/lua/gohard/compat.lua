-- Calls into vim.lsp.Client that differ between Neovim 0.10 and 0.11+
-- (#41). On 0.10 the client methods are closures bound to the client
-- (client.request_sync(method, ...)); 0.11 turned them into methods
-- (client:request_sync(method, ...)) and deprecated the closures. The
-- plugin and its test go through here so both work without warnings.

local M = {}

--- The oldest supported Neovim.
M.min_version = '0.10'

--- has_method_api is true from Neovim 0.11 on.
M.has_method_api = vim.fn.has('nvim-0.11') == 1

--- check returns true on a supported Neovim, else reports the error
--- once and returns false.
function M.check()
  if vim.fn.has('nvim-' .. M.min_version) == 1 then
    return true
  end
  local v = vim.version and vim.version() or {}
  vim.notify_once(string.format(
    'gohard: Neovim %s or later is required (this is %s.%s.%s)',
    M.min_version, v.major or '?', v.minor or '?', v.patch or '?'),
    vim.log.levels.ERROR)
  return false
end

---@param client vim.lsp.Client
function M.request_sync(client, method, params, timeout_ms, bufnr)
  if M.has_method_api then
    return client:request_sync(method, params, timeout_ms, bufnr)
  end
  return client.request_sync(method, params, timeout_ms, bufnr)
end

---@param client vim.lsp.Client
function M.request(client, method, params, handler, bufnr)
  if M.has_method_api then
    return client:request(method, params, handler, bufnr)
  end
  return client.request(method, params, handler, bufnr)
end

---@param client vim.lsp.Client
function M.supports_method(client, method, bufnr)
  if M.has_method_api then
    return client:supports_method(method, bufnr)
  end
  return client.supports_method(method, { bufnr = bufnr })
end

---@param client vim.lsp.Client
function M.stop(client, force)
  if M.has_method_api then
    return client:stop(force)
  end
  return client.stop(force)
end

--- exec_cmd runs an LSP command: a client-side handler when one is
--- registered, else workspace/executeCommand on the server. 0.10 has
--- no public Client:exec_cmd, so this is its 0.11 behaviour.
---@param client vim.lsp.Client
---@param command lsp.Command
---@param context? { bufnr?: integer }
---@param handler? lsp.Handler
function M.exec_cmd(client, command, context, handler)
  if M.has_method_api then
    return client:exec_cmd(command, context, handler)
  end
  local ctx = vim.deepcopy(context or {})
  ctx.bufnr = ctx.bufnr or vim.api.nvim_get_current_buf()
  ctx.client_id = client.id
  local fn = (client.commands or {})[command.command] or
      vim.lsp.commands[command.command]
  if fn then
    fn(command, ctx)
    return
  end
  M.request(client, 'workspace/executeCommand', {
    command = command.command,
    arguments = command.arguments,
  }, handler, ctx.bufnr)
end

return M
