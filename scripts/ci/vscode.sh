#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# The VS Code extension in editors/vscode (#3): npm ci, the unit tests
# (settings, manifest, TextMate grammar), the VSIX build with vsce, and
# the end-to-end test in VS Code (editors/vscode/test/run.sh), which
# downloads VS Code VSCODE_VERSION (default stable) or runs VSCODE_PATH.
# Linux; needs node and xvfb-run (nix develop provides both).
. "$(dirname "$0")/lib.sh"

command -v npm >/dev/null ||
	die "npm not on PATH; run: nix develop -c bash scripts/ci/vscode.sh"

cd editors/vscode

group "npm ci"
npm ci --no-audit --no-fund
endgroup

group "unit tests"
npm test
endgroup

group "vsce package"
rm -f ./*.vsix
npx vsce package
endgroup

group "VS Code end-to-end test (${VSCODE_PATH:-${VSCODE_VERSION:-stable}})"
bash test/run.sh
endgroup
