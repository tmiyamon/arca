-- Arca LSP configuration for Neovim
-- Add this to your init.lua or lazy.nvim config

vim.api.nvim_create_autocmd("FileType", {
  pattern = "arca",
  callback = function()
    vim.lsp.start({
      name = "arca-lsp",
      cmd = { "arca", "lsp" },
      root_dir = vim.fs.dirname(vim.fs.find({ "main.arca" }, { upward = true })[1]),
    })
  end,
})
