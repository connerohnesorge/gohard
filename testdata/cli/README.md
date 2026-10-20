# testdata/cli fixtures

Each directory `testdata/cli/<case>/` is one end-to-end run of the
`gohard` command line (#20). `TestCLIFixtures` in `internal/cli` runs
every case in-process through `cli.Run` with a real Env
(`internal/env/realenv`), once with `-p 1` and once with `-p 16`. Both
runs must give byte-identical exit codes, stdout, stderr and project
trees, and must match the committed files.

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
| `mkdir PATH` | Creates a directory, for example `.git`, which git cannot store. |
| `cwd PATH` | Runs the command from PATH instead of the temp dir. |
| `env NAME=VALUE` | Overrides an environment variable seen through the Env. |
| `rand STATE` | Replaces the Env's random source with `env.SplitMix64{State: STATE}`, so `--seed random` picks the same seed every run. |
| `requires interpreter` | Skips the case while no sequential interpreter is registered (`simrun.DefaultCode` is nil); the case's want files are created with `-update` once it is. |
| `stdout broken` | Standard output fails every write, like a closed pipe (`env.ErrBrokenPipe`): the run keeps its exit code. |
| `stdout full` | Standard output fails every write, like a full disk: the run logs `cli/output-failed` and exits 2. |
| `tamper-stdlib PATH` | The bundled std/ieee file PATH (as `ieee/numeric_std.vhdl`) fails its integrity check, through the `stdlib.WithTamper` context hook (#33): the run reports `stdlib/integrity` and exits 2. |
| `panic-on-read PATH` | Reading a file whose path ends in `/PATH` panics, to reach the crash path. |
| `interrupt SIGINT\|SIGTERM on-read PATH` | Reading a file whose path ends in `/PATH` delivers the signal, as the shell does: the run's context is cancelled with `env.SIGINT` or `env.SIGTERM` as the cause (exit 130 or 143). The read itself succeeds. |

`TestHarness` checks the harness itself: it runs at `-p 1` and `-p 16`,
and it fails cases with a wrong exit code, stdout or stderr, or output
that depends on `-p`.

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
