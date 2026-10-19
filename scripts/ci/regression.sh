#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Parser-regression set (#30, #31). Usage: regression.sh pr|nightly.
# "pr" checks the bundled IEEE packages (internal/stdlib, once it
# exists), the 14 corpus designs and every submodule tagged pr in
# testdata/corpus/corpus.yaml; "nightly" adds the submodules tagged
# nightly. Every .vhd/.vhdl file must round-trip byte for byte, and
# every file the upstream project expects to analyse (not on its
# GHDL/NVC expected-failure list, extracted as corpus.yaml documents)
# must parse without syntax errors. The runner is the Go test
# TestParserRegression in internal/parser; it prints the sorted list of
# failing files. A missing submodule fails with a message naming
# git submodule update --init.
. "$(dirname "$0")/lib.sh"

set_name=${1:?usage: regression.sh pr|nightly}
case "$set_name" in
pr | nightly) ;;
*) die "usage: regression.sh pr|nightly" ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

group "parser-regression set ($set_name)"
status=0
go test -count=1 -timeout 60m -run '^TestParserRegression$' -v \
	./internal/parser -regression="$set_name" 2>&1 |
	tee "$tmp/log" || status=$?
endgroup

summary "### Parser-regression set ($set_name)"
summary ""
# One line per source: files, gated, failing, may fail, ungated.
prefix='^[[:space:]]+regress_test\.go:[0-9]+: '
while IFS= read -r line; do
	summary "- ${line#regression }"
done < <(grep -E "${prefix}regression [a-z]" "$tmp/log" |
	sed -E "s/${prefix}//" || true)
if grep -qE 'regression: [0-9]+ gated files have syntax errors' \
	"$tmp/log"; then
	n=$(grep -oE 'regression: [0-9]+ gated files' "$tmp/log" |
		head -n 1 | grep -oE '[0-9]+')
	summary ""
	summary "**$n gated files have syntax errors** (list in the log)."
fi

if [ "$status" -ne 0 ]; then
	die "parser-regression set ($set_name) failed; see the log above"
fi
summary ""
summary "Every gated file parses without syntax errors."
