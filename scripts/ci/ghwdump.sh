#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# GHDL's ghwdump on every GHW export (#60): TestGhwdump exports the
# sample, the shapes trace, random traces and every corpus golden, runs
# ghwdump on each and compares its output with the package's own GHW
# reader. Linux; needs ghwdump (nix develop provides GHDL).
. "$(dirname "$0")/lib.sh"

command -v ghwdump >/dev/null ||
	die "ghwdump not on PATH; run: nix develop -c bash scripts/ci/ghwdump.sh"

group "ghwdump on every GHW export"
GOHARD_REQUIRE_GHWDUMP=1 go test -count=1 -run '^TestGhwdump$' -v \
	./internal/trace/ghw
endgroup
