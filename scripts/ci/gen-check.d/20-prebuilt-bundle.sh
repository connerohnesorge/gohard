#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR/..
# The prebuilt std/ieee bundle (#33): internal/stdlib/prebuilt.bin must be
# exactly what go generate ./internal/sema writes now (the sync check
# above already reran it), and the binary must start from it without
# analyzing std or ieee. The sema tests prove both from the embedded
# bytes: a stale or undecodable file fails here with the command to run.
. "$(dirname "$0")/../lib.sh"

[ -s internal/stdlib/prebuilt.bin ] ||
	die "internal/stdlib/prebuilt.bin is missing or empty;" \
		"run 'go generate ./internal/sema' and commit it"
if ! go test ./internal/sema -count=1 \
	-run '^(TestStartupNoAnalysis|TestPrebuiltUpToDate)$' >/dev/null; then
	die "internal/stdlib/prebuilt.bin is stale or does not decode;" \
		"run 'go generate ./internal/sema' and commit it"
fi
