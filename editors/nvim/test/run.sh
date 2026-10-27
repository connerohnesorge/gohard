#!/usr/bin/env bash
# Headless Neovim end-to-end test of editors/nvim (#38). Builds gohard,
# copies a passing and a failing corpus testbench, the cross-file LSP
# fixture and the semantic tokens fixture into a temporary folder and
# runs e2e.lua in `nvim --headless --clean`, then runs it again with
# vim.lsp.enable in a folder with no root markers (#48). Needs go and
# nvim on PATH:
#   nix develop -c bash editors/nvim/test/run.sh
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../../.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

(cd "$root" && go build -o "$tmp/gohard" ./cmd/gohard)
mkdir -p "$tmp/work"
for d in wait_forms assert_fail md_resolve; do
	cp -R "$root/testdata/corpus/$d" "$tmp/work/$d"
	rm -rf "$tmp/work/$d/golden"
done
cp -R "$root/testdata/lsp/diagnostics/cross-file/in" "$tmp/work/cross-file"
cp -R "$root/testdata/lsp/semantic-tokens/full/in" "$tmp/work/semantic-tokens"
# A folder with no gohard.yaml and no .git for vim.lsp.enable (#48).
mkdir -p "$tmp/bare"
cp -R "$root/testdata/corpus/wait_forms" "$tmp/bare/wait_forms"
rm -rf "$tmp/bare/wait_forms/golden"

nvim --version | head -n 1
# A separate XDG tree keeps the user's config, state and shada out.
export XDG_CONFIG_HOME="$tmp/xdg/config" XDG_DATA_HOME="$tmp/xdg/data" \
	XDG_STATE_HOME="$tmp/xdg/state" XDG_CACHE_HOME="$tmp/xdg/cache"
timeout 300 nvim --headless --clean -l "$here/e2e.lua" \
	"$here/.." "$tmp/gohard" "$tmp/work"
# vim.lsp.enable('gohard') without setup() (skipped before 0.11).
timeout 300 nvim --headless --clean -l "$here/e2e.lua" \
	"$here/.." "$tmp/gohard" "$tmp/bare" enable
