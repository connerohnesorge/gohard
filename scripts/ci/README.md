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
| Gates fail when they should: a hand-edited generated file and a renumbered kind fail `gen-check.sh`, the fuzz crash flow pushes its branch and opens (or links) the pull request | `selftest.sh` (Linux; throwaway clone, fake `go`/`gh` for the fuzz flow) | wired |
| `detlint` | `detlint.sh` (`go run ./tools/detlint ./...`) | wired; needs `tools/detlint` (#30) |
| `go test -race` on Linux | `race.sh` | wired for `internal/env/realenv`, `internal/jsonrpc2` and `internal/server` (real pipe transport) |
| Parser-regression set (`pr` submodules, stdlib, corpus) | `regression.sh pr` | placeholder; later #31 with #8's `corpus.yaml` |
| Corpus designs at seeds 0..3, including the v1.1 designs `prot_cond` and `gc_heap` | `go test` (`internal/corpus` `TestCorpus`, `-corpus.seeds` defaults to 4); `SEEDS=4 corpus-seeds.sh` adds `-corpus.require-sim` and the oracle check | wired; designs that cannot simulate yet are logged by plain `go test` and fail under `corpus-seeds.sh` until the sequential interpreter registers `simrun.DefaultCode` |
| `testdata/cli` fixtures at `-p 1` and `-p 16` | `go test` (`internal/cli`) | wired |
| `testdata/lsp` and `testdata/parse` fixtures | `go test` | `testdata/lsp` wired (#25, see its README); `testdata/parse` later (#31) |
| Liveness workload (60 s virtual, 5 s no-progress rule) | `go test` | wired: `TestLiveness` and `TestNoProgressRule` in `internal/server` |
| Liveness with the analyzer of `gohard lsp` | `go test` | wired: `TestLSPLivenessRealAnalyzer` in `internal/cli` |
| Headless Neovim end-to-end test (Linux) | `nvim.sh` (job `editors`, `nix develop` provides Neovim) | wired (#38): `editors/nvim/test/run.sh` |
| Helix stdio connection test | `go test` (`TestHelixStdio` in `cmd/gohard`, real pipes) | wired (#38) |
| `docs/libraries.md` examples | new step | later: #1 |

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

Triggers: daily schedule and manual dispatch (inputs `fuzztime`,
default `10m`, and `seed_crash`, default false).

| #30 item | Where | Status |
| --- | --- | --- |
| Fuzz each target for 10 minutes | `fuzz-targets.sh` (the one list) and `fuzz.sh` (one matrix job per target) | wired for `FuzzConfig` (#28), `FuzzIndex` and `FuzzLSPReplay` (#25), `FuzzParse` (#31), `FuzzFormat` (#32), `FuzzAnalyze` (#34), `FuzzElab` and `FuzzSim` (#58) |
| Crash opens a PR adding the minimized input under `testdata/fuzz/<Target>/` | `fuzz.sh` | wired, provable on demand with dispatch input `seed_crash` (see below); pushes `fuzz/<Target>-<id>`, prints the compare URL, then `gh pr create`. GitHub refuses the PR unless Settings > Actions > General > Workflow permissions > "Allow GitHub Actions to create and approve pull requests" is on, or the `FUZZ_PR_TOKEN` secret holds a token with contents and pull-requests write; otherwise the job fails naming that setting and the compare URL. `selftest.sh` covers both paths |
| Every corpus design at 64 seeds | `corpus-seeds.sh` (`SEEDS=64`, the default): `TestCorpus -corpus.seeds=64 -corpus.require-sim`, then `go run ./tools/oracle trace tree golden` | wired; fails while the sequential interpreter is missing |
| Full parser-regression list (`pr` and `nightly` submodules) | `regression.sh nightly` | placeholder; later #31 |
| Replay every LSP session in `testdata/seeds/` | `lsp-seeds.sh` | wired (`TestSeeds`, `TestLSPReplaySeeds`) |
| Benchmarks (#19, #22), recorded, never failing | `bench.sh` | wired; runs every `Benchmark*` that exists and the #19 latency benchmark (`tools/bench`), uploads `bench.txt` |

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

Seeded crash (proving the flow on demand): dispatch `nightly` with
`seed_crash` true (`gh workflow run nightly.yml --ref v1 -f
seed_crash=true -f fuzztime=1m`). The `targets` and `fuzz` jobs then
set `GOHARD_FUZZ_SEEDED_CRASH=1`, so `fuzz-targets.sh` adds one entry,
`FuzzSeededCrash ./tools/fuzzselftest root`, and that matrix job runs
the same `fuzz.sh` path as the real targets: its `cras` seed passes,
the fuzzer finds an input starting with `crash` in under a second, Go
minimizes it, and `fuzz.sh` commits `testdata/fuzz/FuzzSeededCrash/<id>`
on `fuzz/FuzzSeededCrash-<id16>` and opens the pull request. Close that
pull request without merging and delete its branch; the minimized input
is always `crash`, so a later seeded run with the branch still present
only notes that the pull request is already open. The target skips
unless `GOHARD_FUZZ_SEEDED_CRASH=1`, so plain `go test ./...` (and the
crash pull request's own checks) pass, and scheduled runs never list
it. No corpus is ever saved under `testdata/fuzz/FuzzSeededCrash`.
Local dry run:
`GOHARD_FUZZ_SEEDED_CRASH=1 FUZZ_OPEN_PR=0 bash scripts/ci/fuzz.sh FuzzSeededCrash ./tools/fuzzselftest root 1m`
(then delete `testdata/fuzz/FuzzSeededCrash`).

## Release workflow (`release.yml`)

Triggers: `v*` tags. It first installs goreleaser and runs
`release-repro.sh`, which clones HEAD twice and runs `goreleaser
release --snapshot --clean` in each clone. The second clone is made
after the first build, under umask 077, with every file's mtime set to
2000-01-01. The script fails unless both builds write the same archive
names and byte-identical archives and `checksums.txt` (#50), and runs
`archive-check.sh` on both dists. `archive-check.sh` lists every archive
and fails if one lacks the binary, `internal/stdlib/LICENSE` or
`internal/stdlib/AUTHORS.md` (the IEEE-P1076 license the binary embeds,
#40), or the root `LICENSE` and `AUTHORS.md` once those files exist
(#38), or if an entry lacks the file info `.goreleaser.yaml` pins:
owner `root:root` with uid and gid 0 (tar only), mode 0755 for the
binary and 0644 for the rest, and the commit time as mtime. Only then
does it run the real `goreleaser release --clean`. Local check (the
second form builds from the committed HEAD, not the working tree):

```
nix run nixpkgs#goreleaser -- release --snapshot --clean
bash scripts/ci/archive-check.sh
GORELEASER=$(nix build nixpkgs#goreleaser --no-link --print-out-paths)/bin/goreleaser \
  nix develop -c bash scripts/ci/release-repro.sh
```

## Rules (#30)

* CI downloads nothing beyond checking out submodules and the Go module
  cache (the pinned golines comes from the module proxy).
  The one exception is the `editors` job, which installs Nix and
  takes Neovim from the flake's dev shell (#38). GHDL and NVC
  never run in CI; hashes recorded by `tools/oracle` are compared.
* A missing submodule must fail with a message naming
  `git submodule update --init`; nothing is skipped.
* Release gate: a green `pr` run on all three OSes and a green `nightly`
  run on the same commit. Making `pr` required for merging is a branch
  protection setting on GitHub, not a file here.
