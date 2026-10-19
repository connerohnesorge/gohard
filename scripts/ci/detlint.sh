#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# detlint over the whole module (#30): banned world access, goroutines,
# select and map order reaching output, outside internal/env/realenv
# and cmd/.
. "$(dirname "$0")/lib.sh"

[ -d tools/detlint ] ||
	die "tools/detlint is missing; the detlint gate cannot run"
group "go run ./tools/detlint ./..."
go run ./tools/detlint ./...
endgroup
