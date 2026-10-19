# shellcheck shell=bash
# Shared helpers for scripts/ci/*.sh. Source it; do not run it.
# Works with bash 3.2 (macOS), GNU bash (Linux) and Git Bash (Windows).

set -euo pipefail

# Repository root, independent of the caller's working directory.
CI_ROOT=$(git rev-parse --show-toplevel)
cd "$CI_ROOT"

# GoHard's pinned golines (#30). Bump deliberately, never float.
export GOLINES_VERSION=v0.13.0
export GOLINES_MODULE=github.com/segmentio/golines

in_actions() { [ "${GITHUB_ACTIONS:-}" = "true" ]; }

group() {
	if in_actions; then echo "::group::$*"; else echo "==> $*"; fi
}

endgroup() {
	if in_actions; then echo "::endgroup::"; fi
}

notice() {
	if in_actions; then echo "::notice::$*"; else echo "notice: $*"; fi
}

err() {
	if in_actions; then echo "::error::$*"; else echo "error: $*" >&2; fi
}

die() {
	err "$*"
	exit 1
}

# summary appends Markdown to the job summary (stdout when local).
summary() {
	if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
		printf '%s\n' "$*" >>"$GITHUB_STEP_SUMMARY"
	else
		printf '%s\n' "$*"
	fi
}

# go_sources prints, NUL-separated, every tracked or untracked-but-not-
# ignored .go file that is GoHard's own code: files under any testdata/
# directory (test inputs, analysistest fixtures) and files carrying the
# standard "// Code generated ... DO NOT EDIT." header are excluded.
# Submodule contents are never listed by git ls-files.
go_sources() {
	local f
	while IFS= read -r -d '' f; do
		case "/$f" in
		*/testdata/*) continue ;;
		esac
		[ -f "$f" ] || continue
		if head -n 20 "$f" |
			grep -Eq '^// Code generated .* DO NOT EDIT\.$'; then
			continue
		fi
		printf '%s\0' "$f"
	done < <(git ls-files -z -c -o --exclude-standard -- '*.go')
}
