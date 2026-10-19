#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# go test -race on the packages that own real goroutines (#30, Linux
# only): realenv, jsonrpc2 (the stdio reader and writer) and server,
# whose TestServeRealPipe runs the shell over real OS pipes (#25).
. "$(dirname "$0")/lib.sh"

RACE_PKGS=(
	./internal/env/realenv
	./internal/jsonrpc2
	./internal/server
)

group "go test -race ${RACE_PKGS[*]}"
go test -race -count=1 "${RACE_PKGS[@]}"
endgroup
