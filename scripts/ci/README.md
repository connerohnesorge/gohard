# CI scripts

The GitHub Actions workflows (`.github/workflows/pr.yml`,
`.github/workflows/nightly.yml`) call only these scripts, so every gate
runs the same way locally:

```
nix develop -c bash scripts/ci/<script>.sh [args]
```

All scripts run under bash 3.2+ on Linux, macOS and Git Bash on Windows
(`shell: bash` in the workflows), work from any directory, and source
`lib.sh` for shared helpers. CI pins Go with `actions/setup-go`
`go-version: '1.27.x'` and `GOTOOLCHAIN=local`.

## Pull-request workflow (`pr.yml`)

Triggers: `pull_request`, pushes to `main` and `v1`, manual dispatch.

| #30 check | Where | Status |
| --- | --- | --- |
| `go build`, `go vet`, `go test ./...` on Linux, macOS, Windows | `build.sh` | wired |
| Fuzz-corpus replay of `testdata/fuzz/<Target>/` | `build.sh` (plain `go test`) | wired; each target reads the root corpus itself |
| `gofmt` and `golines -m 80` (golines pinned to v0.13.0 in `lib.sh`) | `format.sh` | wired |
| Generated-file sync (`go generate ./...` then compare) | `gen-check.sh` | wired; no generators exist yet |
| Kind renumbering | `gen-check.d/*.sh` hook | later: #31 adds `10-kind-numbers.sh` |
| `detlint` | `detlint.sh` (`go run ./tools/detlint ./...`) | wired; needs `tools/detlint` (#30) |
| `go test -race` on Linux | `race.sh` | wired for `internal/env/realenv`, `internal/jsonrpc2` and `internal/server` (real pipe transport) |
| Parser-regression set (`pr` submodules, stdlib, corpus) | `regression.sh pr` | placeholder; later #31 with #8's `corpus.yaml` |
| Corpus designs at seeds 0..3, `prot_cond` exit 2 | not yet | later: #35/#36 with #8 (a step in `pr.yml`) |
| `testdata/cli` fixtures at `-p 1` and `-p 16` | `go test` (`internal/cli`) | wired |
| `testdata/lsp` and `testdata/parse` fixtures | `go test` | `testdata/lsp` wired (#25, see its README); `testdata/parse` later (#31) |
| Liveness workload (60 s virtual, 5 s no-progress rule) | `go test` | wired: `TestLiveness` and `TestNoProgressRule` in `internal/server` |
| `docs/libraries.md` examples, Neovim and Helix tests | new steps | later: #1, #38 |

Formatting scope: every tracked or untracked, non-ignored `*.go` file,
except files under any `testdata/` directory (test inputs, analyzer
fixtures) and files whose header matches
`// Code generated ... DO NOT EDIT.` (those are covered by the sync
check instead). Override the golines binary with `GOLINES=golines`.

Generated-file sync: `gen-check.sh` snapshots content hashes of all
tracked and untracked files, runs `go generate ./...`, and fails naming
each changed or new file with its diff. In CI it also requires a clean
`git status`. Afterwards it runs every `gen-check.d/*.sh` hook (see
`gen-check.d/README.md`); later stages put checks there that
`go generate` cannot express, such as a renumbered kind.

Windows: the workflow sets `core.autocrlf false` before checkout so
fixtures and Go files keep LF bytes.

## Nightly workflow (`nightly.yml`)

Triggers: daily schedule and manual dispatch (input `fuzztime`, default
`10m`).

| #30 item | Where | Status |
| --- | --- | --- |
| Fuzz each target for 10 minutes | `fuzz-targets.sh` (the one list) and `fuzz.sh` (one matrix job per target) | wired for `FuzzConfig` (#28), `FuzzIndex` and `FuzzLSPReplay` (#25); later: #31 `FuzzParse`, #32 `FuzzFormat` append to the list |
| Crash opens a PR adding the minimized input under `testdata/fuzz/<Target>/` | `fuzz.sh` | wired |
| Every corpus design at 64 seeds | `corpus-seeds.sh` | placeholder; later #35/#36 with #8 |
| Full parser-regression list (`pr` and `nightly` submodules) | `regression.sh nightly` | placeholder; later #31 |
| Replay every LSP session in `testdata/seeds/` | `lsp-seeds.sh` | wired (`TestSeeds`, `TestLSPReplaySeeds`) |
| Benchmarks (#19, #22), recorded, never failing | `bench.sh` | wired; runs every `Benchmark*` that exists, uploads `bench.txt` |

Crash pull requests: `fuzz.sh` moves the input Go wrote to
`<pkg>/testdata/fuzz/<Target>/<id>` to the repo-root
`testdata/fuzz/<Target>/<id>` (or keeps it in the package for entries
marked `local`), pushes branch `fuzz/<Target>-<id16>`, opens the PR with
`gh` and fails the job. An existing branch of the same name is not
reopened. The job has `contents: write` and `pull-requests: write`. A PR
opened with the default `GITHUB_TOKEN` does not trigger the `pr`
workflow, so set the secret `FUZZ_PR_TOKEN` (fine-grained, contents and
pull-requests write) for the PR to show its red checks. Local dry run:
`FUZZ_OPEN_PR=0 bash scripts/ci/fuzz.sh FuzzIndex ./internal/span local 30s`.

## Rules (#30)

* CI downloads nothing beyond checking out submodules and the Go module
  cache (the pinned golines comes from the module proxy). GHDL and NVC
  never run in CI; hashes recorded by `tools/oracle` are compared.
* A missing submodule must fail with a message naming
  `git submodule update --init`; nothing is skipped.
* Release gate: a green `pr` run on all three OSes and a green `nightly`
  run on the same commit. Making `pr` required for merging is a branch
  protection setting on GitHub, not a file here.
