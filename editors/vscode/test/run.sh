#!/usr/bin/env bash
# End-to-end test of the VS Code extension (#3). Builds gohard, copies
# the cross-file diagnostics fixture and the inlay-hints/all-off fixture
# into a temporary folder and runs out/test/e2e/runTest.js, which
# downloads VS Code (VSCODE_VERSION, default stable) into .vscode-test
# and runs out/test/e2e/suite.js in it. On Linux VS Code runs on a
# virtual display (xvfb-run). VS Code and the gohard it starts run
# under the memory cap of the dev shell's go commands (GOHARD_MEMCAP,
# default 5G, 0 for none) when systemd-run can make a user scope.
# Needs go, node and the npm packages (npm ci); it compiles the
# extension and the tests. scripts/ci/vscode.sh runs it after npm ci:
#   nix develop -c bash scripts/ci/vscode.sh
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
ext=$(cd "$here/.." && pwd)
root=$(cd "$ext/../.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

(cd "$ext" && npm run --silent compile)
(cd "$root" && go build -o "$tmp/gohard" ./cmd/gohard)
mkdir -p "$tmp/work"
cp -R "$root/testdata/lsp/diagnostics/cross-file/in" "$tmp/work/cross-file"
cp -R "$root/testdata/lsp/inlay-hints/all-off/in" "$tmp/work/inlay-hints"

run=()
cap=${GOHARD_MEMCAP:-5G}
if [ "$cap" != 0 ] && command -v systemd-run >/dev/null &&
	systemd-run --user --scope --quiet true 2>/dev/null; then
	run=(systemd-run --user --scope --quiet -p "MemoryMax=$cap"
		-p MemorySwapMax=0)
fi
if [ "$(uname)" = Linux ]; then
	command -v xvfb-run >/dev/null ||
		{ echo "xvfb-run not on PATH; run: nix develop -c bash $0" >&2; exit 1; }
	run+=(xvfb-run -a)
fi
timeout 900 ${run[@]+"${run[@]}"} node "$ext/out/test/e2e/runTest.js" \
	"$tmp/gohard" "$tmp/work"
