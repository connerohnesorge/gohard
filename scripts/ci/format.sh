#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# gofmt and golines -m 80 on GoHard's own Go code (#30). Any file either
# tool would change fails the job. Excluded, see go_sources in lib.sh:
# files under testdata/ and files with a "Code generated ... DO NOT
# EDIT." header. Set GOLINES to override the binary (default: the
# pinned version via go run).
. "$(dirname "$0")/lib.sh"

files=()
while IFS= read -r -d '' f; do files+=("$f"); done < <(go_sources)
if [ "${#files[@]}" -eq 0 ]; then
	notice "no Go files to check"
	exit 0
fi

if [ -n "${GOLINES:-}" ]; then
	# shellcheck disable=SC2206
	golines=($GOLINES)
else
	golines=(go run "$GOLINES_MODULE@$GOLINES_VERSION")
fi

fail=0
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

group "gofmt -l (${#files[@]} files)"
gofmt -l "${files[@]}" >"$tmp/gofmt" ||
	die "gofmt failed (syntax error?); see the output above"
bad=()
while IFS= read -r f; do bad+=("$f"); done <"$tmp/gofmt"
endgroup
if [ "${#bad[@]}" -gt 0 ]; then
	fail=1
	err "gofmt would reformat these files; run gofmt -w on them:"
	printf '  %s\n' "${bad[@]}" >&2
	gofmt -d "${files[@]}" || true
fi

group "golines -m 80 -l (${#files[@]} files)"
"${golines[@]}" -m 80 -l "${files[@]}" >"$tmp/golines" ||
	die "golines failed; see the output above"
bad=()
while IFS= read -r f; do bad+=("$f"); done <"$tmp/golines"
endgroup
if [ "${#bad[@]}" -gt 0 ]; then
	fail=1
	err "golines -m 80 would rewrite these files;" \
		"run golines -m 80 -w on them:"
	printf '  %s\n' "${bad[@]}" >&2
	"${golines[@]}" -m 80 --dry-run "${bad[@]}" || true
fi

exit "$fail"
