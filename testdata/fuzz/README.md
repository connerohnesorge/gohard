# Saved fuzz inputs

Committed inputs for GoHard's Go native fuzz targets (#8, #30). Every file
here is replayed by plain `go test ./...` on every pull request, on Linux,
macOS and Windows. Pull requests never fuzz; they only replay.

## Layout

```
testdata/fuzz/
  FuzzParse/<id>       any bytes                          (owner #31)
  FuzzFormat/<id>      any input that parses              (owner #32)
  FuzzLSPReplay/<id>   edit series on one open document   (owner #25)
  FuzzConfig/<id>      any bytes as gohard.yaml           (owner #28)
```

* Each file uses Go's fuzz corpus encoding (first line
  `go test fuzz v1`, then one Go literal per fuzz argument), the same
  bytes `go test -fuzz` writes into a package's own `testdata/fuzz/`.
  `<id>` is the name Go gives the file (a hash of its contents).
* Because Go only auto-loads `testdata/fuzz/` beside the package, each
  `Fuzz*` function reads this repo-root directory for its target and adds
  every file as a seed, so the replay is part of the normal test run.
  A missing or empty target directory is not an error.
* Files are never edited or deleted. Each one is a regression that must
  keep passing.

## Invariants checked on every input

| Target | Invariant |
| --- | --- |
| `FuzzParse` | Never panics or hangs, round-trips byte for byte, every reported error points at a real position in the input. |
| `FuzzFormat` | Formatting twice equals formatting once; only whitespace and keyword case change; literals and extended identifiers are byte-identical; the output parses to the same tree (same node types and token text ignoring spacing, keywords compared case-insensitively, same comment text in the same order). |
| `FuzzLSPReplay` | After a random series of edits and a save, once the server settles, its diagnostics equal `gohard check` on the final text. |
| `FuzzConfig` | Config loading never panics and is deterministic. |

Elaboration and simulation are not fuzz targets until the corpus passes.

## How inputs arrive

The nightly workflow (Linux, scheduled) fuzzes each target for 10 minutes
and never blocks pull requests. For each crash it opens a pull request
that adds the minimized input under `testdata/fuzz/<Target>/`. That pull
request stays red until the fix lands on its branch. A `FuzzLSPReplay`
failure also writes a replayable session file to `testdata/seeds/` (#29).
