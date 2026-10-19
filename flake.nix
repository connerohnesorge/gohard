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
          go_1_27
          gopls
          gotools
          golangci-lint
          delve
          gofumpt
          golines
          ghdl
          nvc
          neovim
          helix
        ];

        shellHook = ''
          export GOPATH="''${XDG_CACHE_HOME:-$HOME/.cache}/gohard-go"
          export PATH="$GOPATH/bin:$PATH"
          echo "Go $(go version | awk '{print $3}') devshell"
        '';
      };
    });
}
