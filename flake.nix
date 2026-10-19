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
          # Run each go command in its own memory-capped systemd scope so a
          # runaway test is OOM-killed alone, not the whole terminal.
          # Set GOHARD_MEMCAP=0 to disable, or to another size (e.g. 8G).
          if [ "''${GOHARD_MEMCAP:-5G}" != 0 ] &&
            command -v systemd-run >/dev/null &&
            systemd-run --user --scope --quiet true 2>/dev/null; then
            capbin="''${XDG_CACHE_HOME:-$HOME/.cache}/gohard-bin"
            mkdir -p "$capbin"
            realgo="$(command -v go)"
            printf '%s\n' '#!/bin/sh' \
              "exec systemd-run --user --scope --quiet -p MemoryMax=''${GOHARD_MEMCAP:-5G} -p MemorySwapMax=0 $realgo \"\$@\"" \
              > "$capbin/go"
            chmod +x "$capbin/go"
            export PATH="$capbin:$PATH"
          fi
          echo "Go $(go version | awk '{print $3}') devshell"
        '';
      };
    });
}
