#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Nightly: replay every committed LSP session in testdata/seeds/lsp/
# (#30, #29, #25). Each session is replayed twice against the simulated
# Env; both runs must give the same bytes and keep the server
# invariants (internal/server TestSeeds), and `gohard lsp replay` must
# read every one (internal/cli TestLSPReplaySeeds). Plain `go test` on
# pull requests runs the same tests.
. "$(dirname "$0")/lib.sh"

group "LSP session replay (testdata/seeds/lsp)"
go test -count=1 -run '^TestSeeds$' -v ./internal/server
go test -count=1 -run '^TestLSPReplaySeeds$' ./internal/cli
endgroup
seeds=(testdata/seeds/lsp/*.jsonl)
summary "- LSP session replay: ${#seeds[@]} sessions"
