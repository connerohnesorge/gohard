# gen-check hooks

`scripts/ci/gen-check.sh` runs every `*.sh` file here with bash, in name
order, after `go generate ./...` has passed its sync check. A hook fails
the check by exiting non-zero and should print one clear line naming the
file and what to do.

Hooks run from the repository root and may source `../lib.sh` for
`die`, `err` and `notice`. They must not download anything.

Planned hooks (#30):

* `10-kind-numbers.sh` (#31): every kind number already in
  `internal/cst/kinds.yaml` on the merge base keeps its number; kinds
  are only appended. Compare against
  `git show "$(git merge-base HEAD origin/main)":internal/cst/kinds.yaml`.
  On a shallow checkout, fetch the base first or compare against the
  committed generated table instead.

* `20-prebuilt-bundle.sh` (#33): `internal/stdlib/prebuilt.bin`, the
  std/ieee bundle analyzed at build time, is non-empty, decodes, and is
  byte-identical to a fresh `go generate ./internal/sema` (the sema tests
  `TestStartupNoAnalysis` and `TestPrebuiltUpToDate`).
