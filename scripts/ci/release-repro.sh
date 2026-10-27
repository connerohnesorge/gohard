#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Checks that the release archives are reproducible (#50): builds a
# goreleaser snapshot of HEAD in each of two fresh clones and fails
# unless both give the same archive names and byte-identical archives,
# checksums.txt, Homebrew cask and Scoop manifest (#69). The second
# clone is made after the first build, under umask 077, with every
# checked-out file's mtime set to 2000-01-01, so an archive entry that
# keeps its checkout time or mode shows up as a difference. Both dists must also pass archive-check.sh,
# which checks the owner, mode and time of every entry. Nothing in this
# checkout is touched.
#
#   bash scripts/ci/release-repro.sh
#
# GORELEASER names the goreleaser binary (default: goreleaser on PATH);
# locally, GORELEASER=$(nix build nixpkgs#goreleaser --no-link
# --print-out-paths)/bin/goreleaser inside nix develop.
. "$(dirname "$0")/lib.sh"

goreleaser=${GORELEASER:-goreleaser}
command -v "$goreleaser" >/dev/null ||
	die "goreleaser not found (set GORELEASER)"

sha=$(git rev-parse HEAD)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# snapshot DIR clones HEAD into DIR and runs the snapshot release there.
# Extra arguments run in the clone after checkout, before the build.
snapshot() {
	local dir=$1
	shift
	group "clone $sha into $dir"
	git clone -q --no-checkout "$CI_ROOT" "$dir"
	git -C "$dir" checkout -q --detach "$sha"
	if [ $# -gt 0 ]; then (cd "$dir" && "$@"); fi
	endgroup
	group "goreleaser snapshot in $dir"
	(cd "$dir" && "$goreleaser" release --snapshot --clean)
	endgroup
}

# skew moves every checked-out file's mtime to 2000-01-01.
skew() {
	find . -path ./.git -prune -o -type f -exec touch -t 200001010000 {} +
}

snapshot "$tmp/a"
(umask 077 && snapshot "$tmp/b" skew)

# names DIST prints the archive, checksum and package manifest file
# names in DIST.
names() {
	(cd "$1" && ls -1 -- *.tar.gz *.zip checksums.txt \
		homebrew/Casks/gohard.rb scoop/gohard.json 2>/dev/null) || true
}

failed=0
if [ "$(names "$tmp/a/dist")" != "$(names "$tmp/b/dist")" ]; then
	err "the two snapshots wrote different files:"
	diff <(names "$tmp/a/dist") <(names "$tmp/b/dist") >&2 || true
	failed=1
fi
n=0
for f in $(names "$tmp/a/dist"); do
	n=$((n + 1))
	if ! cmp -s "$tmp/a/dist/$f" "$tmp/b/dist/$f"; then
		err "$f differs between the two snapshots"
		failed=1
	fi
done
for d in "$tmp/a/dist" "$tmp/b/dist"; do
	bash "$CI_ROOT/scripts/ci/archive-check.sh" "$d" || failed=1
done
[ "$n" -gt 1 ] || die "no archives in $tmp/a/dist"
[ "$failed" -eq 0 ] || die "release archives differ or fail archive-check.sh"
group checksums.txt
cat "$tmp/a/dist/checksums.txt"
endgroup
notice "$n files byte-identical across two clones of $sha"
