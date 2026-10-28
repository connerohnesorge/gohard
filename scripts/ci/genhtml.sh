#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# lcov's genhtml on the coverage GoHard writes (#62): TestGenhtml runs
# genhtml --branch-coverage on every corpus coverage golden, the
# `gohard check --coverage` fixture and a test design, and fails on any
# warning or error. Linux; needs genhtml (nix develop provides lcov).
. "$(dirname "$0")/lib.sh"

command -v genhtml >/dev/null ||
	die "genhtml not on PATH; run: nix develop -c bash scripts/ci/genhtml.sh"

group "genhtml on the coverage files"
GOHARD_REQUIRE_GENHTML=1 go test -count=1 -run '^TestGenhtml$' -v \
	./internal/coverage
endgroup
