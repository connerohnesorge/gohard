-- GoHard simulation debugger for nvim-dap (#68, prototype): registers
-- the `gohard` adapter, which runs `gohard dap` over stdio, and a
-- launch configuration for VHDL buffers. nvim-dap
-- (https://github.com/mfussenegger/nvim-dap) must be installed; this
-- module only configures it.
--
-- Usage:
--   require('gohard.dap').setup()
--   require('gohard.dap').setup({ testbench = 'tb_top', seed = 3 })
--
-- Then :DapToggleBreakpoint on a statement and :DapContinue. Stepping
-- back (:lua require('dap').step_back()) and reverse continue work
-- too. The adapter analyzes the project of the configuration's cwd
-- (the folder Neovim was started in by default), like `gohard check`.

local M = {}

---@class gohard.DapOptions
---@field cmd? string[]          adapter command (default gohard dap)
---@field testbench? string      testbench to run; asked for when nil
---@field seed? integer          simulation seed (default 0)
---@field stopOnEntry? boolean   stop at the first statement (false)

M.defaults = {
  cmd = { 'gohard', 'dap' },
  testbench = nil,
  seed = 0,
  stopOnEntry = false,
}

---@param opts? gohard.DapOptions
function M.setup(opts)
  local ok, dap = pcall(require, 'dap')
  if not ok then
    vim.notify('gohard.dap: nvim-dap is not installed', vim.log.levels.WARN)
    return
  end
  local o = vim.tbl_deep_extend('force', M.defaults, opts or {})
  dap.adapters.gohard = function(callback, config)
    callback({
      type = 'executable',
      command = o.cmd[1],
      args = vim.list_slice(o.cmd, 2),
      options = {
        cwd = config.cwd or vim.fn.getcwd(),
        -- A frame in a bundled std/ieee file has no path; nvim-dap
        -- reads its text from the adapter (a source request).
        source_filetype = 'vhdl',
      },
    })
  end
  local testbench = o.testbench
  if testbench == nil then
    testbench = function()
      -- Empty: the project's only testbench.
      return vim.fn.input('Testbench: ')
    end
  end
  dap.configurations.vhdl = dap.configurations.vhdl or {}
  table.insert(dap.configurations.vhdl, {
    type = 'gohard',
    request = 'launch',
    name = 'GoHard: debug testbench',
    cwd = '${workspaceFolder}',
    testbench = testbench,
    seed = o.seed,
    stopOnEntry = o.stopOnEntry,
  })
end

return M
