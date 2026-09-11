-- cordanaLLM/praetor Neovim LSP and Tool Configuration
-- Plug-and-play Lua integration for nvim-lspconfig and HISS governance commands.

local M = {}

function M.setup(opts)
  opts = opts or {}
  local bin_path = opts.bin_path or "./bin/standards-lsp"

  local has_lspconfig, lspconfig = pcall(require, "lspconfig")
  if not has_lspconfig then
    vim.notify("[standards] nvim-lspconfig not found. Please install neovim/nvim-lspconfig.", vim.log.levels.WARN)
    return
  end

  local configs = require("lspconfig.configs")

  -- Register standards_lsp custom server definition if not already registered
  if not configs.standards_lsp then
    configs.standards_lsp = {
      default_config = {
        cmd = { bin_path },
        filetypes = { "go" },
        root_dir = function(fname)
          return lspconfig.util.root_pattern(".standards.yaml", "go.mod", ".git")(fname)
            or vim.fn.getcwd()
        end,
        settings = {
          standards = {
            hissEnforcement = true,
            maxCyclomatic = 10,
            maxCognitive = 15,
            maxLOC = 75,
            maxStatements = 50,
          },
        },
      },
    }
  end

  lspconfig.standards_lsp.setup({
    on_attach = function(client, bufnr)
      -- Keymaps for standards diagnostics and quick fixes
      local bufopts = { noremap = true, silent = true, buffer = bufnr }
      vim.keymap.set("n", "<leader>sd", vim.diagnostic.open_float, bufopts)
      vim.keymap.set("n", "[d", vim.diagnostic.goto_prev, bufopts)
      vim.keymap.set("n", "]d", vim.diagnostic.goto_next, bufopts)
    end,
  })

  -- User commands for standards governance
  vim.api.nvim_create_user_command("StandardsAudit", function()
    vim.cmd("!go run ./cmd/standardsctl audit")
  end, { desc = "Audit repository against declared HISS invariants" })

  vim.api.nvim_create_user_command("StandardsCompileContext", function()
    vim.cmd("!go run ./cmd/standardsctl compile-context")
  end, { desc = "Compile AGENTS.md cross-agent contexts" })

  vim.api.nvim_create_user_command("StandardsVerifyAll", function()
    vim.cmd("!make verify-all")
  end, { desc = "Run full standards verification pipeline" })

  vim.api.nvim_create_user_command("StandardsRatchetSweep", function()
    vim.cmd("!go run ./cmd/standardsctl baseline --check")
  end, { desc = "Evaluate technical debt baseline ratchet sweep" })
end

-- Auto-setup with defaults if required directly
M.setup()

return M
