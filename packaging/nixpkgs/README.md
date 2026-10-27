# nixpkgs package

`package.nix` is the expression for `nix profile install nixpkgs#gohard`
(#69). It builds a tagged release from source with `buildGo127Module`
(`go.mod` needs Go 1.27, newer than the nixpkgs default Go) and runs
`gohard version` as the install check. It lives here until nixpkgs
carries it; after that the copy in nixpkgs is the one that counts, and
nixpkgs' update bot bumps the version and hashes after each release.

Build it against the flake's nixpkgs:

```
nix build --impure --expr 'let pkgs = (builtins.getFlake (toString ./.)).inputs.nixpkgs.legacyPackages.${builtins.currentSystem}; in pkgs.callPackage ./packaging/nixpkgs/package.nix {}'
./result/bin/gohard version
```

To move it to a new release, set `version`, empty `hash` and
`vendorHash` (`""`), build, and paste the hashes the two failures
print (or run `nix-update gohard` in a nixpkgs checkout). `vendorHash`
is the flake's `vendorHash` whenever `go.mod` and `go.sum` match.

Submitting it to nixpkgs:

1. Add yourself to `maintainers/maintainer-list.nix`, in its own
   commit (`maintainers: add <handle>`).
2. Copy `package.nix` to `pkgs/by-name/go/gohard/package.nix`, drop the
   two comment lines at the top, and set `maintainers` to
   `with lib.maintainers; [ <handle> ]`.
3. `nix-build -A gohard` and `nixfmt` on the file, then commit it as
   `gohard: init at <version>` and open the pull request.
