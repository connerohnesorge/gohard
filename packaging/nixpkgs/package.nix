# The nixpkgs package for gohard (#69), to submit as
# pkgs/by-name/go/gohard/package.nix. See README.md next to this file.
{
  lib,
  buildGo127Module,
  fetchFromGitHub,
  versionCheckHook,
}:

buildGo127Module (finalAttrs: {
  pname = "gohard";
  version = "1.1.1";

  src = fetchFromGitHub {
    owner = "connerohnesorge";
    repo = "gohard";
    tag = "v${finalAttrs.version}";
    hash = "sha256-V98FW2qzRud43UPgBvjmr5A4L35bMdfGyc7kZkBqt/c=";
  };

  vendorHash = "sha256-xmx6COnpMZapnb+73e7LBg4rf7VrDv+Pf3okwfUk5AQ=";

  subPackages = [ "cmd/gohard" ];

  env.CGO_ENABLED = 0;

  # No commit: the source archive has no .git, and a hash written here
  # would go stale on every automated version bump. `gohard version`
  # then prints "commit unknown".
  ldflags = [
    "-s"
    "-w"
    "-X github.com/connerohnesorge/gohard/internal/cli.Version=${finalAttrs.version}"
  ];

  # The tests read fixtures from git submodules under testdata/, which
  # the release source archive does not contain.
  doCheck = false;

  nativeInstallCheckInputs = [ versionCheckHook ];
  versionCheckProgramArg = "version";
  doInstallCheck = true;

  meta = {
    description = "VHDL language server, checker, formatter and simulator";
    homepage = "https://github.com/connerohnesorge/gohard";
    changelog = "https://github.com/connerohnesorge/gohard/releases/tag/v${finalAttrs.version}";
    # gohard is MPL-2.0; the IEEE-P1076 packages it embeds are Apache-2.0.
    license = with lib.licenses; [
      mpl20
      asl20
    ];
    maintainers = [ ];
    mainProgram = "gohard";
  };
})
