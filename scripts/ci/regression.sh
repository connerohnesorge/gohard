#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Parser-regression set (#30, #31). Usage: regression.sh pr|nightly.
# "pr" checks every submodule tagged pr in corpus.yaml, nightly every
# submodule tagged pr or nightly, each judged by the project's own
# expected-failure list. Placeholder until the parser and corpus.yaml
# land; the later stage replaces the body. A missing submodule must then
# fail with a message naming git submodule update --init.
. "$(dirname "$0")/lib.sh"

set_name=${1:?usage: regression.sh pr|nightly}
notice "parser-regression set ($set_name): not wired yet (#31)"
summary "- parser-regression set ($set_name): not wired yet"
