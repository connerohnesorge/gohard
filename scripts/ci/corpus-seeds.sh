#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Corpus designs at many seeds (#8, #30, #35, #36). Runs the corpus
# harness (internal/corpus TestCorpus): every design of
# testdata/corpus/corpus.yaml, its listed variants and the generated
# layout/case/rename/reorder variants, checking exit, status, messages
# and stop time, byte-identical traces across seeds 0..SEEDS-1 (the
# first differing line is printed), and the golden trace and its
# trace_sha256. Pull requests: SEEDS=4 (the
# plain `go test` default); nightly: SEEDS=64. A design that cannot
# simulate yet (no sequential interpreter) fails here
# (-corpus.require-sim) instead of being skipped. Then the recorded
# GHDL/NVC oracles are checked against GoHard (tools/oracle trace,
# tree and golden sections; no tool runs).
. "$(dirname "$0")/lib.sh"

SEEDS=${SEEDS:-64}
case "$SEEDS" in
'' | *[!0-9]*) die "SEEDS must be a number, got '$SEEDS'" ;;
esac

group "corpus harness at $SEEDS seeds"
go test -count=1 -timeout 60m -run '^TestCorpus$' ./internal/corpus \
	-args -corpus.seeds="$SEEDS" -corpus.require-sim
endgroup

group "recorded trace and hierarchy oracles"
go run ./tools/oracle trace tree golden
endgroup
summary "- corpus at $SEEDS seeds: every design, oracles checked"
