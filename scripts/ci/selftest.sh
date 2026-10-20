#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Self-test of the CI gates themselves (#30): proves that the checks
# fail when they should, on a throwaway clone of HEAD (with this
# checkout's scripts/ci copied over it). Nothing in this checkout and
# nothing remote is touched; nothing is downloaded beyond the Go module
# cache go generate already needs.
#
#   1. a hand-edited generated file fails gen-check.sh (out of sync);
#   2. a renumbered kind (two kinds swapped in kinds.yaml, the
#      generated files regenerated to match) fails gen-check.sh through
#      gen-check.d/10-kind-numbers.sh;
#   3. a renamed keyword fails 10-kind-numbers.sh;
#   4. fuzz.sh on a crash pushes the fuzz/<Target>-<id> branch, opens
#      the pull request with gh, and fails the job;
#   5. when gh may not create the pull request, fuzz.sh still pushes
#      the branch, prints the compare URL and the setting to change,
#      and fails the job.
#
# Go and gh are replaced by fakes in steps 4 and 5; steps 1 and 2 run
# the real go generate.
. "$(dirname "$0")/lib.sh"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
# The scripts under test must behave as a local run would.
unset GITHUB_ACTIONS GITHUB_STEP_SUMMARY GITHUB_BASE_REF GITHUB_REF_NAME

fail=0
check() { # check NAME WANT-EXIT WANT-TEXT LOG EXIT
	if [ "$5" -ne "$2" ] || ! grep -qF -- "$3" "$4"; then
		err "selftest $1: exit $5 (want $2), output lacks '$3':"
		sed 's/^/    /' "$4" >&2
		fail=1
	else
		echo "ok   $1"
	fi
}

# fresh DIR clones HEAD into DIR, overlays scripts/ci and makes the
# append-only base origin/main the clone's HEAD.
fresh() {
	rm -rf "$1"
	git clone -q --shared --no-checkout "$CI_ROOT" "$1"
	git -C "$1" checkout -q --detach "$(git rev-parse HEAD)"
	rm -rf "$1/scripts/ci"
	cp -R "$CI_ROOT/scripts/ci" "$1/scripts/ci"
	git -C "$1" add -A scripts/ci
	git -C "$1" -c user.name=selftest -c user.email=selftest@invalid \
		commit -q --allow-empty -m "selftest: scripts under test"
	git -C "$1" update-ref refs/remotes/origin/main HEAD
}

# --- 1. hand-edited generated file -------------------------------------
r=$tmp/gen
fresh "$r"
gen=internal/token/keywords_gen.go
printf '\n// hand edit\n' >>"$r/$gen"
st=0
(cd "$r" && bash scripts/ci/gen-check.sh) >"$tmp/1.log" 2>&1 || st=$?
check "hand-edited $gen fails gen-check" 1 "$gen" "$tmp/1.log" "$st"
grep -qF "out of sync" "$tmp/1.log" ||
	{ err "selftest 1: no 'out of sync' message"; fail=1; }

# --- 2. renumbered kind ------------------------------------------------
fresh "$r"
k=$r/internal/cst/kinds.yaml
grep -q '^  - {name: Error,' "$k" && grep -q '^  - {name: Unparsed,' "$k" ||
	die "selftest 2: kinds.yaml lacks the Error/Unparsed lines"
awk '/^  - \{name: Error,/ { e = $0; next }
	/^  - \{name: Unparsed,/ { print; print e; next } { print }' \
	"$k" >"$k.new"
mv "$k.new" "$k"
(cd "$r" && go generate ./... >/dev/null)
st=0
(cd "$r" && bash scripts/ci/gen-check.sh) >"$tmp/2.log" 2>&1 || st=$?
check "renumbered kind fails gen-check" 1 "append-only" "$tmp/2.log" "$st"

# --- 3. renamed keyword (hook alone) and a clean control ---------------
fresh "$r"
st=0
(cd "$r" && bash scripts/ci/gen-check.d/10-kind-numbers.sh) \
	>"$tmp/3a.log" 2>&1 || st=$?
echo "clean" >>"$tmp/3a.log"
check "unchanged kinds pass the hook" 0 "clean" "$tmp/3a.log" "$st"
kw=$r/internal/token/keywords.yaml
first=$(grep -m1 -oE '(word): *"?[A-Za-z0-9_]+' "$kw" |
	sed -E 's/^word: *"?//')
[ -n "$first" ] || die "selftest 3: no word in keywords.yaml"
sed -E "0,/word: *\"?$first/s//word: ${first}x/" "$kw" >"$kw.new"
mv "$kw.new" "$kw"
st=0
(cd "$r" && bash scripts/ci/gen-check.d/10-kind-numbers.sh) \
	>"$tmp/3b.log" 2>&1 || st=$?
check "renamed keyword fails the hook" 1 "append-only" "$tmp/3b.log" "$st"

# --- 4 and 5. fuzz crash flow ------------------------------------------
bin=$tmp/bin
mkdir -p "$bin"
cat >"$bin/go" <<'EOF'
#!/usr/bin/env bash
# Fake go test -fuzz: writes a minimized crash input and fails.
target= pkg=
while [ $# -gt 0 ]; do
	case "$1" in
	-fuzz) target=${2#^}; target=${target%\$}; shift ;;
	./*) pkg=$1 ;;
	esac
	shift
done
d=$pkg/testdata/fuzz/$target
mkdir -p "$d"
printf 'go test fuzz v1\n[]byte("x")\n' >"$d/0123456789abcdef0123"
echo "--- FAIL: $target"
exit 1
EOF
cat >"$bin/gh" <<'EOF'
#!/usr/bin/env bash
# Fake gh pr create.
if [ "${FAKE_GH_DENY:-0}" = 1 ]; then
	echo "GraphQL: GitHub Actions is not permitted to create or" \
		"approve pull requests (createPullRequest)" >&2
	exit 1
fi
echo "https://github.com/o/r/pull/7"
EOF
chmod +x "$bin/go" "$bin/gh"

fuzzrun() { # fuzzrun N DENY
	fresh "$r"
	git init -q --bare "$tmp/remote$1.git"
	git -C "$r" remote set-url origin "$tmp/remote$1.git"
	st=0
	(cd "$r" && PATH="$bin:$PATH" GH_TOKEN=x GITHUB_REPOSITORY=o/r \
		GITHUB_REF_NAME=main FAKE_GH_DENY=$2 \
		bash scripts/ci/fuzz.sh FuzzSelftest internal/config root 1s) \
		>"$tmp/f$1.log" 2>&1 || st=$?
	if ! git -C "$tmp/remote$1.git" rev-parse -q --verify \
		refs/heads/fuzz/FuzzSelftest-0123456789abcdef >/dev/null; then
		err "selftest fuzz $1: crash branch was not pushed"
		fail=1
	fi
	if ! git -C "$tmp/remote$1.git" show \
		fuzz/FuzzSelftest-0123456789abcdef:testdata/fuzz/FuzzSelftest/0123456789abcdef0123 \
		>/dev/null 2>&1; then
		err "selftest fuzz $1: crash input missing from the branch"
		fail=1
	fi
}
url="https://github.com/o/r/compare/main...fuzz/FuzzSelftest-0123456789abcdef"
fuzzrun 4 0
check "fuzz crash opens a pull request" 1 "pull/7" "$tmp/f4.log" "$st"
fuzzrun 5 1
check "fuzz crash without PR permission" 1 "$url" "$tmp/f5.log" "$st"
grep -qF "Allow" "$tmp/f5.log" ||
	{ err "selftest 5: no repository-setting hint"; fail=1; }

exit "$fail"
