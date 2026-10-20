#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Headless Neovim end-to-end test of editors/nvim (#38, #25): a passing
# and a failing corpus testbench run through the code lens, their
# gohard-sim diagnostics checked, plus cross-file hover, definition,
# references and rename. Linux; needs nvim (nix develop provides it).
. "$(dirname "$0")/lib.sh"

command -v nvim >/dev/null ||
	die "nvim not on PATH; run: nix develop -c bash scripts/ci/nvim.sh"

group "headless Neovim end-to-end test"
bash editors/nvim/test/run.sh
endgroup
