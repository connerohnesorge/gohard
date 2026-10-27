# Upstream editor configs

Configs proposed to the editors' own repositories (#65), so that
GoHard works there without copying config by hand. The maintainer
sends each submission by hand; nothing here is sent by automation.
Record each pull request link and the upstream answer on #65.

| File | Goes to | Checked by |
| --- | --- | --- |
| `nvim-lspconfig/lsp/gohard.lua` | `lsp/gohard.lua` in [neovim/nvim-lspconfig](https://github.com/neovim/nvim-lspconfig) | `editors/nvim/test/run.sh`, mode `upstream` (`scripts/ci/nvim.sh`) |
| `helix/gohard.diff` | [helix-editor/helix](https://github.com/helix-editor/helix): `languages.toml` and the generated `book/src/generated/lang-support.md` | `TestHelixStdio` in `cmd/gohard` |
| `../zed` | [zed-industries/extensions](https://github.com/zed-industries/extensions) as the extension `gohard-lsp` | `TestZedStdio` in `cmd/gohard` |

## nvim-lspconfig

`lsp/gohard.lua` is self-contained: `cmd`, `filetypes` and
`root_markers`, no `require('gohard')`. Root markers are ordered by
priority, `gohard.yaml` before `.git`, which is the server's own root
rule (`config.FindRoot`). A folder with neither gets no root; the
server then uses the folder of the first file opened (#48), so the
config needs no `root_dir` function.

Copy the file to `lsp/gohard.lua` of a nvim-lspconfig checkout, run
`make lint`, and open the pull request with the commit message
`feat: gohard`. nvim-lspconfig asks new configs for some evidence that
the server is used (its CONTRIBUTING.md: 100 GitHub stars or similar).

## Helix

`gohard.diff` applies with `git apply` to Helix at
`ba40e547426b0f9896c8bdc699a4ab11f2b37dbc` (master, 2026-09-29). It
adds `gohard = { command = "gohard", args = ["lsp"] }` to
`[language-server]` and `gohard` after `vhdl_ls` in the `vhdl`
language's `language-servers`, and updates the generated language
table (`cargo xtask docgen` writes the same line). Helix skips a
listed server that is not installed, so `vhdl_ls` users see no change.

The diff sets no `roots` for `vhdl`: a root marker there would move
`vhdl_ls`'s root too. Without roots Helix sends the folder holding
`.git` (or, with no `.git`, only `rootPath`, the folder Helix was
started in), and the server looks for `gohard.yaml` from there upward.
A project whose `gohard.yaml` sits below the `.git` folder needs
`roots = ["gohard.yaml"]` in the user's own `languages.toml`
(`docs/editors.md`).

## Zed

The extension is in `editors/zed`. To publish it, in a fork of
zed-industries/extensions:

```
git submodule add https://github.com/connerohnesorge/gohard.git extensions/gohard-lsp
```

check out the commit to publish in the submodule (it must be on a
branch of the gohard repository), add the entry to `extensions.toml`

```toml
[gohard-lsp]
submodule = "extensions/gohard-lsp"
path = "editors/zed"
version = "0.1.0"
```

run `pnpm sort-extensions`, and open the pull request. The `version`
must equal the one in `editors/zed/extension.toml` at that commit. Zed
asks that the extension be tried in Zed at that commit first
(`zed: install dev extension`, then pick `editors/zed`).
