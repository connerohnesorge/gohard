#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Nightly benchmarks (#19, #22, #30). Runs every Benchmark* that exists
# and records the numbers in $BENCH_OUT (default bench.txt in the repo
# root) and the job summary. Never fails the job: benchmark numbers are
# recorded, not gated. Later stages only add Benchmark* functions; this
# script picks them up.
. "$(dirname "$0")/lib.sh"
set +e

out=${BENCH_OUT:-bench.txt}
benchtime=${BENCHTIME:-1s}

group "go test -run ^\$ -bench . -benchmem -benchtime $benchtime ./..."
go test -run '^$' -bench . -benchmem -benchtime "$benchtime" \
	-count 1 ./... >"$out" 2>&1
status=$?
cat "$out"
endgroup

n=$(grep -c '^Benchmark' "$out")
{
	echo "### Benchmarks"
	echo
	if [ "$n" -eq 0 ]; then
		echo "No benchmarks exist yet."
	else
		echo '```'
		grep -E '^(Benchmark|goos|goarch|cpu|pkg)' "$out"
		echo '```'
	fi
} | while IFS= read -r line; do summary "$line"; done
if [ "$status" -ne 0 ]; then
	notice "go test -bench exited $status; recorded, not failing"
fi
exit 0
