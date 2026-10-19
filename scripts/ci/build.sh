#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# go build ./..., go vet ./..., go test ./... (#30). Plain go test also
# replays every saved fuzz input under testdata/fuzz/<Target>/.
. "$(dirname "$0")/lib.sh"

group "go build ./..."
go build ./...
endgroup

group "go vet ./..."
go vet ./...
endgroup

group "go test ./... (includes fuzz-corpus replay)"
go test -count=1 ./...
endgroup
