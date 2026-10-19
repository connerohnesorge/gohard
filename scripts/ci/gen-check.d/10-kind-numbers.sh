#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR/..
# Kind numbers are append-only (#31): every token, keyword and node kind
# in internal/cst/kinds.yaml and internal/token/keywords.yaml on the merge
# base keeps its position (its number). New kinds may only be appended
# at the end of their list. The base is the merge base with the PR's
# base branch (origin/$GITHUB_BASE_REF, else origin/main). Without a
# remote base: the first parent when HEAD is a merge commit (a PR merge
# checkout), else HEAD (a local checkout; uncommitted edits are still
# checked). In GitHub Actions a missing base is an error, never a silent
# comparison of HEAD with itself (pr.yml checks out with fetch-depth 0).
# Nothing is downloaded.
. "$(dirname "$0")/../lib.sh"

# names FILE prints the numbered names of a kinds.yaml or keywords.yaml
# text on stdin, one "section name" per line, in file order.
names() {
	awk '
		/^[a-z]+:/ { sec = $1; sub(":", "", sec); next }
		sec != "tokens" && sec != "nodes" && sec != "words" { next }
		/^  - / {
			line = $0
			if (match(line, /(name|word): *"?[A-Za-z0-9_]+/)) {
				v = substr(line, RSTART, RLENGTH)
				sub(/^(name|word): *"?/, "", v)
				print sec, v
			}
		}'
}

base_branch=origin/${GITHUB_BASE_REF:-main}
base=$(git merge-base HEAD "$base_branch" 2>/dev/null || true)
if [ -z "$base" ]; then
	if in_actions; then
		err "kind numbers: no merge base with $base_branch; check out" \
			"with fetch-depth: 0 so the append-only check has a base"
		exit 1
	fi
	if git rev-parse --verify -q 'HEAD^2' >/dev/null; then
		base='HEAD^1'
	else
		base=HEAD
	fi
fi

fail=0
for f in internal/cst/kinds.yaml internal/token/keywords.yaml; do
	old=$(git show "$base:$f" 2>/dev/null | names) || true
	[ -n "$old" ] || continue # new file: nothing to keep
	new=$(names <"$f")
	n=$(printf '%s\n' "$old" | wc -l)
	if [ "$(printf '%s\n' "$new" | head -n "$n")" != "$old" ]; then
		err "$f: an existing kind was renamed, reordered or removed" \
			"(kind numbers are append-only; compare with $base):"
		diff <(printf '%s\n' "$old") <(printf '%s\n' "$new" |
			head -n "$n") >&2 || true
		fail=1
	fi
done
exit "$fail"
