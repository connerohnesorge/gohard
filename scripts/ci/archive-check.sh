#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Lists every release archive goreleaser wrote and fails if one lacks
# the binary or the license files (#40): the IEEE-P1076 LICENSE and
# AUTHORS.md the binary embeds, and the project LICENSE and AUTHORS.md
# once they exist at the repository root (#38).
#
#   nix run nixpkgs#goreleaser -- release --snapshot --clean
#   bash scripts/ci/archive-check.sh [DIST]   # default dist
. "$(dirname "$0")/lib.sh"

dist=${1:-dist}
required="internal/stdlib/LICENSE internal/stdlib/AUTHORS.md"
for f in LICENSE AUTHORS.md; do
	if [ -f "$f" ]; then required="$required $f"; fi
done

# list prints the file names in archive $1, one per line.
list() {
	case $1 in
	*.tar.gz) tar -tzf "$1" ;;
	*.zip)
		python3 -I -c 'import sys, zipfile
for n in zipfile.ZipFile(sys.argv[1]).namelist(): print(n)' "$1"
		;;
	esac
}

n=0
failed=0
for a in "$dist"/*.tar.gz "$dist"/*.zip; do
	[ -e "$a" ] || continue
	n=$((n + 1))
	group "$a"
	files=$(list "$a" | sed 's#^\./##')
	echo "$files"
	endgroup
	want="$required gohard"
	case $a in *windows*) want="$required gohard.exe" ;; esac
	for f in $want; do
		if ! printf '%s\n' "$files" | grep -qxF "$f"; then
			err "$a: missing $f"
			failed=1
		fi
	done
done
[ "$n" -gt 0 ] || die "no archives in $dist (run goreleaser first)"
[ "$failed" -eq 0 ] || die "release archives lack required files"
notice "$n archives contain: $required"
