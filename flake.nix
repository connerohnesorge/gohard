{
  description = "Go development shell";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = {
    self,
    nixpkgs,
    flake-utils,
    ...
  }:
    flake-utils.lib.eachDefaultSystem (system: let
      pkgs = nixpkgs.legacyPackages.${system};
      version = "1.0.0";
      # The source copy has no .git, so the Go build info has no
      # vcs.revision: pass the flake's commit (#39). A dirty tree has
      # only dirtyRev ("<sha>-dirty"); with neither, gohard says unknown.
      commit = self.rev or self.dirtyRev or "";
      gohard = (pkgs.buildGoModule.override {go = pkgs.go_1_27;}) {
        pname = "gohard";
        inherit version;
        # Only the module sources; testdata submodules are not needed.
        src = pkgs.lib.cleanSource ./.;
        subPackages = ["cmd/gohard"];
        vendorHash = "sha256-xmx6COnpMZapnb+73e7LBg4rf7VrDv+Pf3okwfUk5AQ=";
        env.CGO_ENABLED = "0";
        ldflags = [
          "-s"
          "-w"
          "-X github.com/connerohnesorge/gohard/internal/cli.Version=${version}"
          "-X github.com/connerohnesorge/gohard/internal/cli.Commit=${commit}"
        ];
        doCheck = false;
        meta = {
          description = "VHDL language server with a built-in simulator";
          homepage = "https://github.com/connerohnesorge/gohard";
          mainProgram = "gohard";
        };
      };
    in {
      packages.default = gohard;
      packages.gohard = gohard;
      apps.default = {
        type = "app";
        program = "${gohard}/bin/gohard";
      };
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
