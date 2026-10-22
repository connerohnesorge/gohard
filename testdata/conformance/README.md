# testdata/conformance

The #18 conformance cases: every v1.1 feature has at least one case
here, and each case checks both the syntax tree round trip and the
simulator output. `TestConformance` in `internal/cli` runs them; the
`conformance` section of `tools/oracle` cross-checks them against NVC
1.23. The shared format code is `internal/corpus/conformance.go`.

## Layout

```text
testdata/conformance/<feature>/<case>.vhd      the design (one file)
testdata/conformance/<feature>/<case>.want     gohard's exact output
testdata/conformance/<feature>/<case>.nvc.txt  NVC's recorded output (optional)
```

`<feature>` is a folder per #18 feature (`int64`, `cond_return`,
`protected`, ...). Every other file in a feature folder is an error.

## The case file

One VHDL file holding the testbench and everything it needs. The
leading comment lines (`--` at column 0, up to the first other line)
are the header. A header line `-- key: value` with one of these keys is
a directive; any other comment line is prose.

| Directive | Meaning |
| --- | --- |
| `std: 2008` | The standard: 1993, 2000, 2002, 2008 or 2019. Default **2019**. The case runs with a `gohard.yaml` of `standard: "<std>"` (none for 2008, GoHard's default) and NVC with `--std=<std>`. |
| `top: name` | The testbench (`--tb`, and NVC's `-e`). Default: the last `entity NAME is` in the file. |
| `pending: construct` | The feature is still behind its `unsupported/construct` detector. While the run reports `unsupported/construct]: <construct> is not supported`, the case is skipped with a log line instead of failing. `construct` is the wording of the detector's message (for example `conditional return ... when`); a pending case that reports another unsupported construct fails, and one that runs is compared like any other case. The integrator removes the line when it removes the detector. |
| `nvc-differs: reason` | NVC's messages or pass/fail are known to differ (an NVC bug or a decided difference); the NVC recording is then only checked for freshness. |

Make the simulator output prove the feature: `report` the values the
feature computes and `assert` the expected ones, so the `.want` shows
them and a wrong value is an error.

## `<case>.want`

The exact result of `gohard check --sim --seed 0 --tb TOP` run on a
fresh folder holding the case file (and its `gohard.yaml`), at `-p 1`
and `-p 16` (they must agree):

```text
# gohard check --sim --seed 0 --tb counter_tb (VHDL-2008)
exit 0
== stdout
counter.vhd:41:5: hint[sim/assert-note]: report note: count = 13
summary: 0 errors, 0 warnings, 1 hints, 1 files
== stderr
gohard: simulated counter_tb (seed 0): quiet
```

A stream that does not end in a newline gets one followed by a
`\ no newline` line. The test also parses the file under its standard
and requires the CST to print back byte for byte, with no syntax
problems (pending cases may have them).

```sh
nix develop -c go test ./internal/cli -run TestConformance          # check
nix develop -c go test ./internal/cli -run TestConformance -update  # rewrite .want
```

Review the `.want` diff by hand: it is the answer key.

## `<case>.nvc.txt`

NVC's run of the case (`nvc --std=STD -a <case>.vhd -e TOP -r` in a
folder holding only the case file), recorded like the other oracle
goldens:

```text
# NVC output of testdata/conformance/protected/counter.vhd
# recorded by: go run ./tools/oracle --update conformance
# nvc: 1.23.0
# command: nvc --std=2008 -a counter.vhd -e counter_tb -r
# source sha256: <sha256 of counter.vhd>
# exit: 0
** Note: 10ns+0: count = 13
   Process :counter_tb:b at counter.vhd:37
```

To record a case, create the file empty and run the oracle in the dev
shell (which pins NVC):

```sh
touch testdata/conformance/<feature>/<case>.nvc.txt
nix develop -c go run ./tools/oracle --update -design <feature>/<case> conformance
```

CI never runs NVC: `go test ./tools/oracle` (`TestRecorded`) checks
that each recording is of the current source (the sha256) and command,
and that NVC's report and assertion messages (severity and text, as a
sorted list, NVC's `FINISH called`/`STOP called` notes left out) and
pass/fail (exit 0 or not) equal those in the `.want`. Pending cases and
cases with `nvc-differs:` are checked for freshness only.

All files here are compared byte for byte; `.gitattributes` turns off
line-ending conversion.
