#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Nightly: every corpus design at 64 seeds (#30, #8). Placeholder until
# the corpus runner exists (#35, #36 with #8's corpus.yaml): later
# stages replace the body with a run that checks byte-identical traces
# across seeds 0..63 and the trace hashes in corpus.yaml, and fails on
# a mismatch, printing the first differing trace line.
. "$(dirname "$0")/lib.sh"

SEEDS=${SEEDS:-64}
notice "corpus at $SEEDS seeds: not wired yet (needs corpus.yaml" \
	"and the simulator); nothing to run"
summary "- corpus at $SEEDS seeds: not wired yet"
