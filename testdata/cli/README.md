# testdata/cli fixtures

Each directory `testdata/cli/<case>/` is one end-to-end run of the
`gohard` command line (#20). `TestCLIFixtures` in `internal/cli` runs
every case in-process through `cli.Run` with a real Env
(`internal/env/realenv`), once with `-p 1` and once with `-p 16`, and
each of those with a cold, a warm and a corrupted analysis cache (#37):
cold is a fresh copy of `in/`; warm is a fresh copy plus the
`.gohard/cache` files the cold run wrote; corrupted is the same with
every cache entry damaged (truncated, a bit flipped or emptied, by
turns). A command that writes no cache runs cold only, since its warm
and corrupted runs would be the same run. Every run must give
byte-identical exit codes, stdout, stderr and project trees, and must
match the committed files; the cache files themselves must not depend
on `-p`. Files under a `.gohard/cache/` folder are not part of the
compared project tree.

## Files

| File | Required | Meaning |
| --- | --- | --- |
| `cmd` | yes | The command line. The first non-blank line that does not start with `#` is used. Words are split on spaces and tabs; `'...'` is literal and `"..."` allows `\"` and `\\`. The first word must be `gohard`. No shell expansion happens. |
| `want.exit` | yes | The expected exit code in decimal, followed by a newline. |
| `want.stdout` | yes | The exact expected stdout bytes (may be empty). |
| `want.stderr` | yes | The exact expected stderr bytes (may be empty). |
| `in/` | no | A project tree copied into a fresh temp directory, which becomes the run's current directory. Without it the run starts in an empty temp directory. |
| `stdin` | no | Bytes fed to standard input. |
| `setup` | no | World changes `in/` cannot express, one directive per line (`#` starts a comment); see below. |
| `want/` | no | If present, the whole temp directory after the run must equal this tree (every file, byte for byte). Use it for commands that write files, such as `gohard fmt`. Git does not keep empty directories, so `want/` needs at least one file. |

`setup` directives (paths are slash paths relative to the temp dir):

| Directive | Effect |
| --- | --- |
| `symlink PATH TARGET` | Creates a symbolic link. On Windows, if the OS refuses (no developer mode or elevation), the case is skipped, except under `CI`, where it fails, so CI runs it on every OS. |
| `size PATH BYTES` | Creates or extends PATH to BYTES bytes, sparse where the OS allows (for the 10 MB limit). |
| `aggregate PATH N` | Writes PATH as a package whose constant aggregate has N+1 elements (about 2N syntax-tree nodes in 2N bytes), for the 1,000,000-node limit without committing a large file. |
| `chmod PATH MODE` | Sets PATH's permission bits to the octal MODE (for example `000`, to make a library folder or source file unreadable); they are restored after the run. Skipped on Windows and when running as root, where permission bits cannot deny a read. |
| `mkdir PATH` | Creates a directory, for example `.git`, which git cannot store. |
| `cwd PATH` | Runs the command from PATH instead of the temp dir. |
| `env NAME=VALUE` | Overrides an environment variable seen through the Env. |
| `libs-host OS DIR` | `gohard libs detect` and the "library not found" quick fix search a host of OS (`linux`, `darwin` or `windows`) whose file-system root is the folder DIR: host path `/opt/Xilinx` is read at `DIR/opt/Xilinx` and `C:\Xilinx` at `DIR/C/Xilinx` (`libs.WithHost`, #1). Output shows host paths, so the case gives the same bytes on every OS. |
| `rand STATE` | Replaces the Env's random source with `env.SplitMix64{State: STATE}`, so `--seed random` picks the same seed every run. |
| `viewers [NAME...]` | Replaces the Env's Executor with a simulated one whose `PATH` holds exactly the named programs (none when no name is given), for Open waveform (#36). |
| `requires interpreter` | Skips the case while no sequential interpreter is registered (`simrun.DefaultCode` is nil); the case's want files are created with `-update` once it is. |
| `stdout broken` | Standard output fails every write, like a closed pipe (`env.ErrBrokenPipe`): the run keeps its exit code. |
| `stdout full` | Standard output fails every write, like a full disk: the run logs `cli/output-failed` and exits 2. |
| `build-info [KEY=VALUE...]` | `gohard version` and the repro and session headers describe a binary built with these facts, through the `cli.WithBuildInfo` context hook (#39). `ldflags.version` and `ldflags.commit` are the `-ldflags` values (as goreleaser and Nix set them); `go`, `main.version` and `vcs.revision`, `vcs.modified` (any `vcs.*` key) fill the Go build info, which is absent when none of them is given. Without the directive the run shows the commit and toolchain as `unknown`, so fixtures stay stable across toolchains. |
| `tamper-stdlib PATH` | The bundled std/ieee file PATH (as `ieee/numeric_std.vhdl`) fails its integrity check, through the `stdlib.WithTamper` context hook (#33): the run reports `stdlib/integrity` and exits 2. |
| `stdlib-internal-error PATH` | The bundled file PATH (such as `ieee/numeric_std.vhdl`) reports an analysis error, through the `stdlib.WithInternalError` context hook: the run reports `stdlib/internal-error` and exits 2 (#20, #33). |
| `panic-on-read PATH` | Reading a file whose path ends in `/PATH` panics, to reach the crash path. |
| `interrupt SIGINT\|SIGTERM on-read PATH` | Reading a file whose path ends in `/PATH` delivers the signal, as the shell does: the run's context is cancelled with `env.SIGINT` or `env.SIGTERM` as the cause (exit 130 or 143). The read itself succeeds. |

`TestHarness` checks the harness itself: it runs at `-p 1` and `-p 16`
with each cache state, and it fails cases with a wrong exit code,
stdout or stderr, or output that depends on `-p` or on the cache.

The harness inserts `-p N` right after `gohard`, so a fixture never sets
`-p` itself. Any occurrence of the temp directory's path in stdout or
stderr is replaced by `$WORK`; well-behaved output prints paths relative
to the project root and never needs this.

All files in this tree are compared byte for byte; `.gitattributes`
turns off line-ending conversion so the fixtures pass on Windows.

## Updating

```sh
nix develop -c go test ./internal/cli -run TestCLIFixtures -update
```

`-update` rewrites `want.exit`, `want.stdout`, `want.stderr` (and
`want/` where it exists) from the `-p 1` run; the `-p 16` run must
still match it. Review the diff before committing: until v1.0 ships,
diagnostic codes and wording come only from the `internal/diag` table,
and `-update` is how fixtures follow it.

## Adding a case

```sh
mkdir testdata/cli/check-unmapped-library
echo 'gohard check' > testdata/cli/check-unmapped-library/cmd
mkdir testdata/cli/check-unmapped-library/in   # add the project files
nix develop -c go test ./internal/cli -run TestCLIFixtures -update
```

Name cases `<command>-<situation>` so related cases sort together. #20
requires a fixture for every case of its spec.
