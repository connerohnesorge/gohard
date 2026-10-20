# GoHard

GoHard is a VHDL language server, checker, formatter and simulator in
one pure-Go binary. Runs are deterministic: the same inputs and seed
give the same bytes on every OS.

## Install

Pick one:

* Go 1.27 or newer:
  `go install github.com/connerohnesorge/gohard/cmd/gohard@latest`
* A release binary from
  [GitHub Releases](https://github.com/connerohnesorge/gohard/releases)
  for linux, darwin or windows on amd64 or arm64. Verify the archive
  with `sha256sum -c checksums.txt --ignore-missing`.
* Nix: `nix profile install github:connerohnesorge/gohard`, or
  `nix run github:connerohnesorge/gohard -- version`.

Then run `gohard version`. It prints the version, the commit, the
bundled IEEE library tag and commit, and the Go toolchain:

```
version 1.0.0
commit <40-hex commit>
ieee 1076-2019 16a012320947d378611cc7457f64ed76cb52bac4
go go1.27.2
```

## Use

* `gohard check` checks a project; `gohard fmt` formats it, and
  `gohard fmt -` formats standard input to standard output (exit 1 if it
  does not parse; with `--check`, exit 1 if it would change).
* `gohard check --sim` also runs testbenches; failures print a repro
  command.
* `gohard lsp` is the language server over stdio.

Editor setup (Neovim plugin, Helix snippet) is in
[docs/editors.md](docs/editors.md). Config keys are in
[docs/config.md](docs/config.md).

## Build from source

```sh
nix develop -c go build ./cmd/gohard
nix develop -c go test -p 4 ./...
```

Releases are built by goreleaser on `v*` tags
(`.goreleaser.yaml`, `.github/workflows/release.yml`). Try it locally
with `nix run nixpkgs#goreleaser -- release --snapshot --clean`.

## License

GoHard is licensed under the [Mozilla Public License 2.0](LICENSE).
Contributions use a DCO sign-off; see [CONTRIBUTING.md](CONTRIBUTING.md).
The bundled IEEE packages in `internal/stdlib` are Apache-2.0.
