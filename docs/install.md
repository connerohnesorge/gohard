# Installing GoHard

Every install path gives the same `gohard` binary. Check it with
`gohard version`, which prints the version, the commit, the bundled
IEEE library and the Go toolchain.

| Path | Command | Platforms |
| --- | --- | --- |
| Homebrew | `brew install --cask connerohnesorge/tap/gohard` | macOS, Linux |
| Scoop | `scoop bucket add connerohnesorge https://github.com/connerohnesorge/scoop-bucket` then `scoop install connerohnesorge/gohard` | Windows |
| nixpkgs | `nix profile install nixpkgs#gohard` | Linux, macOS |
| Nix flake | `nix profile install github:connerohnesorge/gohard` | Linux, macOS |
| Go | `go install github.com/connerohnesorge/gohard/cmd/gohard@latest` | any Go 1.27 target |
| Release archive | download from [GitHub Releases](https://github.com/connerohnesorge/gohard/releases) | linux, darwin, windows on amd64 and arm64 |

## Homebrew and Scoop

Each release updates the cask in
[connerohnesorge/homebrew-tap](https://github.com/connerohnesorge/homebrew-tap)
and the manifest in
[connerohnesorge/scoop-bucket](https://github.com/connerohnesorge/scoop-bucket)
(#69). Both install the release archive for your OS and arch, and both
check it against the sha256 that goreleaser wrote into the cask or
manifest. `brew upgrade --cask gohard` and `scoop update gohard` move
to the latest release.

The binaries are not signed or notarized by Apple. The cask therefore
removes the `com.apple.quarantine` attribute from the installed binary
after install, so macOS runs it without the "cannot be opened" dialog.

## Nix

`nixpkgs#gohard` is built from the tagged source with `buildGoModule`.
nixpkgs updates it after a release, so it can be a release or two
behind. The package expression is kept in
[packaging/nixpkgs/](../packaging/nixpkgs/README.md). The flake in
this repository always builds the commit you name:
`nix run github:connerohnesorge/gohard -- version`, or
`nix run github:connerohnesorge/gohard/v1.1.1 -- version` for a tag.

The nixpkgs build has no commit to embed, so its `gohard version`
prints `commit unknown`. The flake and the release archives print the
commit.

## Verifying a release archive

`checksums.txt` lists the sha256 of every archive. It shows that an
archive is the one the release published:

```
sha256sum -c checksums.txt --ignore-missing
```

The release workflow also signs a build provenance attestation for
every archive and for `checksums.txt` (#69). An attestation names the
repository, the workflow file, the tag and the commit that built the
file, and is signed with a short-lived Sigstore certificate GitHub
issues to that workflow run. Verify one with the GitHub CLI:

```
gh attestation verify gohard_1.2.0_linux_amd64.tar.gz \
  --repo connerohnesorge/gohard \
  --signer-workflow connerohnesorge/gohard/.github/workflows/release.yml
```

It exits 0 and prints the matched attestation when the archive was
built by `release.yml` in this repository, and fails otherwise.
Verifying `checksums.txt` the same way, then running `sha256sum -c`,
covers every archive with one attestation check. The attestations are
also listed at
<https://github.com/connerohnesorge/gohard/attestations>.

Releases up to v1.1.1 have no attestations; check those with
`checksums.txt` only.
