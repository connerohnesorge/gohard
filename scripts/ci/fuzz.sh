#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Nightly fuzzing of one target (#30).
#
# Usage: fuzz.sh <FuzzTarget> <package> [root|local] [fuzztime]
#
# Fuzzes for fuzztime (default $FUZZTIME or 10m). On a crash, Go has
# already minimized the input and written it to
# <package>/testdata/fuzz/<Target>/<id>. With corpus "root" (default)
# this script moves it to the repo-root testdata/fuzz/<Target>/<id>,
# where the target replays it; with "local" it stays where Go put it,
# which go test replays automatically. Then, unless FUZZ_OPEN_PR=0, it
# pushes a branch fuzz/<Target>-<id> and opens a pull request with gh
# (GH_TOKEN must be set). It always prints the compare URL; if gh
# cannot create the pull request (Actions not permitted to create pull
# requests and no FUZZ_PR_TOKEN), the job fails with that URL and the
# setting to change. The pull request
# stays red until the fix lands on its branch. The job itself then
# fails so the crash is visible on the nightly run.
. "$(dirname "$0")/lib.sh"

usage="usage: fuzz.sh <FuzzTarget> <package> [root|local] [fuzztime]"
target=${1:?$usage}
pkg=${2:?$usage}
corpus=${3:-root}
fuzztime=${4:-${FUZZTIME:-10m}}
case "$corpus" in
root | local) ;;
*) die "$usage" ;;
esac
pkg=./${pkg#./}
localdir="$pkg/testdata/fuzz/$target"
rootdir="testdata/fuzz/$target"
if [ "$corpus" = "local" ]; then rootdir=$localdir; fi

# Remember which package-local inputs existed, so only new ones count.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
list_inputs() {
	if [ -d "$localdir" ]; then
		find "$localdir" -type f -print | LC_ALL=C sort
	fi
}
list_inputs >"$tmp/before"

group "go test -fuzz ^$target\$ -fuzztime $fuzztime $pkg"
status=0
go test -run '^$' -fuzz "^$target\$" -fuzztime "$fuzztime" "$pkg" \
	2>&1 | tee "$tmp/log" || status=$?
endgroup

list_inputs >"$tmp/after"
new=$(LC_ALL=C comm -13 "$tmp/before" "$tmp/after")

if [ "$status" -eq 0 ]; then
	summary "- \`$target\` ($pkg): no crash in $fuzztime"
	exit 0
fi
if [ -z "$new" ]; then
	die "$target: go test failed without writing a crash input" \
		"(build error or failing seed); see the log above"
fi

mkdir -p "$rootdir"
added=()
for f in $new; do
	id=$(basename "$f")
	if [ "$corpus" = "root" ]; then mv "$f" "$rootdir/$id"; fi
	added+=("${rootdir#./}/$id")
done
if [ "$corpus" = "root" ]; then
	rmdir "$localdir" "$pkg/testdata/fuzz" "$pkg/testdata" \
		2>/dev/null || true
fi

err "$target crashed; minimized input: ${added[*]}"
summary "- \`$target\` ($pkg): **crash**, input ${added[*]}"

if [ "${FUZZ_OPEN_PR:-1}" = "0" ]; then
	notice "FUZZ_OPEN_PR=0: not opening a pull request"
	exit 1
fi
: "${GH_TOKEN:?GH_TOKEN must be set to open the crash pull request}"

first=$(basename "${added[0]}")
branch="fuzz/$target-${first:0:16}"
if git ls-remote --exit-code --heads origin "$branch" >/dev/null 2>&1
then
	notice "branch $branch already exists; pull request already open"
	exit 1
fi

base=${GITHUB_REF_NAME:-$(git rev-parse --abbrev-ref HEAD)}
git config user.name "github-actions[bot]"
git config user.email \
	"41898282+github-actions[bot]@users.noreply.github.com"
git checkout -b "$branch"
git add -- "${added[@]}"
git commit -q -m "Add $target crash input ${first:0:16}" \
	-m "Found by the nightly fuzz job. This input fails $target;" \
	-m "the pull request stays red until the fix lands on this branch."
git push -q origin "$branch"

body="$tmp/body.md"
{
	echo "The nightly fuzz job found a crash in \`$target\` ($pkg)."
	echo
	echo "Minimized input, replayed by plain \`go test ./...\`:"
	echo
	for a in "${added[@]}"; do echo "- \`$a\`"; done
	echo
	echo "This pull request stays red until the fix lands on its"
	echo "branch (#30). Reproduce with:"
	echo
	echo '```'
	echo "go test -run '^$target$' $pkg"
	echo '```'
	echo
	echo "Failure log (tail):"
	echo
	echo '```'
	tail -n 60 "$tmp/log"
	echo '```'
} >"$body"
# The compare URL opens the same pull request by hand. It is printed
# whatever gh does, so the job log always carries a way to the input.
server=${GITHUB_SERVER_URL:-https://github.com}
repo=${GITHUB_REPOSITORY:-}
if [ -z "$repo" ]; then
	repo=$(git remote get-url origin 2>/dev/null |
		sed -E 's#^.*github\.com[:/]##; s#\.git$##') || true
fi
compare="$server/$repo/compare/$base...$branch?expand=1"
notice "pushed $branch; compare: $compare"
if pr=$(gh pr create --base "$base" --head "$branch" \
	--title "fuzz: $target crash input ${first:0:16}" \
	--body-file "$body" 2>"$tmp/gh.err"); then
	notice "opened crash pull request: $pr"
	summary "  pull request: $pr"
	exit 1
fi
cat "$tmp/gh.err" >&2
err "$target: branch $branch is pushed but gh could not open the" \
	"pull request. If Actions may not create pull requests, enable" \
	"Settings > Actions > General > Workflow permissions > 'Allow" \
	"GitHub Actions to create and approve pull requests', or set the" \
	"FUZZ_PR_TOKEN secret (a token with contents and pull-requests" \
	"write). Open it by hand: $compare"
summary "  pull request NOT opened (gh failed); open it by hand: $compare"
exit 1
