#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Generated-file sync check (#30). Reruns every generator through
# go generate ./... and fails if any file in the working tree changed or
# appeared. Then runs every hook in scripts/ci/gen-check.d/*.sh in name
# order; a hook fails the check by exiting non-zero. Later stages add
# hooks for checks go generate cannot express, e.g. "an existing kind
# number in kinds.yaml changed" (#31).
#
# The comparison is a before/after snapshot of content hashes, so it
# also works on a dirty local tree. In CI the tree starts clean, so a
# change means a committed generated file differs from its generator.
. "$(dirname "$0")/lib.sh"

# snapshot prints "<blob hash> <path>" for every tracked file and every
# untracked, non-ignored file (submodules excluded), sorted by path.
snapshot() {
	local f
	while IFS= read -r -d '' f; do
		if [ -f "$f" ] && [ ! -L "$f" ]; then printf '%s\n' "$f"; fi
	done < <(git ls-files -z -c -o --exclude-standard) |
		LC_ALL=C sort -u >"$1.paths"
	git hash-object --stdin-paths <"$1.paths" >"$1.hashes"
	paste -d ' ' "$1.hashes" "$1.paths" >"$1"
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

snapshot "$tmp/before"
group "go generate ./..."
go generate ./...
endgroup
snapshot "$tmp/after"

if ! cmp -s "$tmp/before" "$tmp/after"; then
	err "generated files are out of sync with their generators." \
		"Run 'go generate ./...' and commit the result. Changed:"
	# Paths whose hash changed, appeared or disappeared.
	diff "$tmp/before" "$tmp/after" | sed -n 's/^[<>] [0-9a-f]* //p' |
		LC_ALL=C sort -u | while IFS= read -r p; do
		echo "  $p" >&2
		git --no-pager diff --no-ext-diff -- "$p" 2>/dev/null || true
	done
	exit 1
fi

if in_actions && [ -n "$(git status --porcelain)" ]; then
	err "working tree is not clean after go generate:"
	git status --porcelain >&2
	exit 1
fi

fail=0
for hook in scripts/ci/gen-check.d/*.sh; do
	[ -e "$hook" ] || continue
	group "gen-check hook $hook"
	if ! bash "$hook"; then
		err "generated-file check failed: $hook"
		fail=1
	fi
	endgroup
done
exit "$fail"
