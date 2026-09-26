-- Praetor Neovim LSP and Tool Configuration
local lspconfig = require("lspconfig")
local configs = require("lspconfig.configs")

if not configs.standards_lsp then
  configs.standards_lsp = {
    default_config = {
      cmd = { "./bin/standards-lsp" },
      filetypes = { "go" },
      root_dir = function(fname)
        return lspconfig.util.root_pattern(".standards.yaml", "meson.build", "go.mod", ".git")(fname)
      end,
      settings = {
        standards = {
          hissEnforcement = true,
          maxLOC = 60,
          maxStatements = 50,
        },
      },
    },
  }
end

lspconfig.standards_lsp.setup({})

vim.api.nvim_create_user_command("StandardsAudit", function()
  vim.cmd("!praetorctl audit")
end, { desc = "Run the repository command Standards: Audit" })

vim.api.nvim_create_user_command("StandardsCompileContext", function()
  vim.cmd("!praetorctl compile-context")
end, { desc = "Run the repository command Standards: Compile Context" })

vim.api.nvim_create_user_command("StandardsVerifyAll", function()
  vim.cmd("!make verify-all")
end, { desc = "Run the repository command Standards: Verify All" })

vim.api.nvim_create_user_command("StandardsRatchetSweep", function()
  vim.cmd("!praetorctl audit")
end, { desc = "Run the repository command Standards: Ratchet Sweep" })
