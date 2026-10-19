#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# go test -race on the packages that own real goroutines (#30, Linux
# only). Later stages append ./internal/jsonrpc2 (stdio reader and
# writer over a real pipe transport) and ./internal/server.
. "$(dirname "$0")/lib.sh"

RACE_PKGS=(
	./internal/env/realenv
)

group "go test -race ${RACE_PKGS[*]}"
go test -race -count=1 "${RACE_PKGS[@]}"
endgroup
