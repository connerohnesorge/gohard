#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Nightly benchmarks (#19, #22, #30, #35). Runs every Benchmark* that
# exists (BenchmarkSim is the simulation throughput benchmark) and the
# ieee step and CPU shares of the #35 native-body rule, and records the
# numbers in $BENCH_OUT (default bench.txt in the repo root) and the
# job summary. Never fails the job: benchmark numbers are recorded, not
# gated. Later stages only add Benchmark* functions; this script picks
# them up.
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

# The #35 native-body rule input: the share of each corpus run's
# kernel steps spent in each ieee package's interpreted bodies.
group "ieee package step shares (TestIEEEShare)"
go test -count 1 -run '^TestIEEEShare$' -v ./internal/corpus \
	>"$out.ieee" 2>&1
cat "$out.ieee"
grep -E 'steps:|native-body' "$out.ieee" | sed 's/^ *//' >>"$out"
endgroup

# The same rule in CPU time: each design's kernel time split by the
# IEEE package running (pprof label "unit", "-" outside interpreted
# calls), from BenchmarkSimIEEE under -cpuprofile.
group "ieee package CPU shares (BenchmarkSimIEEE)"
prof=$(mktemp -d)
go test -c -o "$prof/corpus.test" ./internal/corpus
designs=$(cd internal/corpus && "$prof/corpus.test" -test.run '^$' \
	-test.bench 'BenchmarkSimIEEE' -test.benchtime 1x 2>/dev/null |
	sed -n 's|^BenchmarkSimIEEE/\([^ -]*\).*|\1|p')
for d in $designs; do
	(cd internal/corpus && "$prof/corpus.test" -test.run '^$' \
		-test.bench "BenchmarkSimIEEE/^$d\$" -test.benchtime 2s \
		-test.cpuprofile "$prof/$d.prof" >/dev/null 2>&1)
	echo "ieee-cpu $d:"
	go tool pprof -tags "$prof/corpus.test" "$prof/$d.prof" 2>/dev/null |
		sed -n '/unit:/,$p'
done | tee -a "$out"
rm -rf "$prof"
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
