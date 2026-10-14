{
  description = "Go development shell";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = {
    nixpkgs,
    flake-utils,
    ...
  }:
    flake-utils.lib.eachDefaultSystem (system: let
      pkgs = nixpkgs.legacyPackages.${system};
    in {
      devShells.default = pkgs.mkShell {
        packages = with pkgs; [
          go
          gopls
          gotools
          golangci-lint
          delve
          gofumpt
        ];

        shellHook = ''
          export GOPATH="$PWD/.go"
          export PATH="$GOPATH/bin:$PATH"
          echo "Go $(go version | awk '{print $3}') devshell"
        '';
      };
    });
}
