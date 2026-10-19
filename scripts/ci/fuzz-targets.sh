#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# The one list of nightly fuzz targets (#30). Each entry is
# "<FuzzTarget> <package> [root|local]". The third field says where a
# crash input is committed: "root" (default) is the repo-root
# testdata/fuzz/<Target>/ that the #30 targets replay themselves;
# "local" is <package>/testdata/fuzz/<Target>/, which Go replays
# automatically, for helper targets that do not read the root corpus.
# Later stages append (root):
#   FuzzFormat    ./internal/format     (#32)
# Elaboration and simulation are not fuzz targets until the corpus
# passes.
#
# Usage: fuzz-targets.sh         one entry per line
#        fuzz-targets.sh --json  GitHub Actions matrix include list
. "$(dirname "$0")/lib.sh"

TARGETS=(
	"FuzzConfig ./internal/config root"
	"FuzzIndex ./internal/span local"
	"FuzzLSPReplay ./internal/server root"
	"FuzzParse ./internal/parser root"
)

if [ "${1:-}" = "--json" ]; then
	sep=""
	printf '['
	for e in "${TARGETS[@]}"; do
		# shellcheck disable=SC2086 # split the entry into fields
		set -- $e
		printf '%s{"target":"%s","package":"%s","corpus":"%s"}' \
			"$sep" "$1" "$2" "${3:-root}"
		sep=","
	done
	printf ']\n'
else
	printf '%s\n' "${TARGETS[@]}"
fi
