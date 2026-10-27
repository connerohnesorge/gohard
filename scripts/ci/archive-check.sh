#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Lists every release archive goreleaser wrote and fails if one lacks
# the binary or the license files (#40): the IEEE-P1076 LICENSE and
# AUTHORS.md the binary embeds, and the project LICENSE and AUTHORS.md
# once they exist at the repository root (#38). It also fails if an
# entry's file info is not the one .goreleaser.yaml pins for
# reproducible archives (#50): owner root:root (uid and gid 0, tar
# only), mode 0755 for the binary and 0644 for the rest, and the HEAD
# commit time as mtime (to the 2 seconds a zip stores).
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

# badinfo prints each entry of archive $1 whose file info differs from
# the pinned one, with the commit time $2 in Unix seconds.
badinfo() {
	python3 -I -c 'import sys, tarfile, time, zipfile
path, t = sys.argv[1], int(sys.argv[2])
def want_mode(name):
    base = name.rsplit("/", 1)[-1]
    return 0o755 if base in ("gohard", "gohard.exe") else 0o644
def report(name, bad):
    if bad: print("%s: %s" % (name, ", ".join(bad)))
if path.endswith(".zip"):
    dos = time.gmtime(t - t % 2)[:6]
    for z in zipfile.ZipFile(path).infolist():
        bad = []
        if z.date_time != dos: bad.append("mtime %s" % (z.date_time,))
        mode = (z.external_attr >> 16) & 0o7777
        if mode != want_mode(z.filename): bad.append("mode %o" % mode)
        report(z.filename, bad)
else:
    for m in tarfile.open(path):
        bad = []
        if m.mtime != t: bad.append("mtime %d" % m.mtime)
        own = (m.uid, m.gid, m.uname, m.gname)
        if own != (0, 0, "root", "root"):
            bad.append("owner %d:%d %s:%s" % own)
        if m.mode != want_mode(m.name): bad.append("mode %o" % m.mode)
        report(m.name, bad)' "$1" "$2"
}

when=$(git log -1 --format=%ct HEAD)
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
	bad=$(badinfo "$a" "$when")
	if [ -n "$bad" ]; then
		err "$a: entries without the pinned file info:"
		printf '%s\n' "$bad" >&2
		failed=1
	fi
done
[ "$n" -gt 0 ] || die "no archives in $dist (run goreleaser first)"
[ "$failed" -eq 0 ] ||
	die "release archives lack required files or pinned file info"
notice "$n archives contain: $required"
