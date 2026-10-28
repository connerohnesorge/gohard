# Oracle corpus

The oracle corpus (issue #8) is GoHard's permanent set of small VHDL designs
with self-checking testbenches. It is the answer key for the pure-Go
simulator, the seed input for parser and design generators, and the fixed
workload for language-server sessions. The GitHub issues are the source of
truth: #8 (corpus), #17 (mode views), #18 (v1.1 2019 features), #24
(integration), #30 (CI gates), #35 (kernel), #36 (trace format), #20 (exit
codes, statuses and diagnostics).

## Layout

```
testdata/corpus/
  README.md          this file
  corpus.yaml        manifest: vocabularies, designs, regression submodules
  LIMITS.md          remaining limits (where the corpus is the only oracle,
                     or a semantic is covered thinly)
  <name>/
    *.vhd            design and testbench sources, each tagged in `files`
                     (or in `variants[].files`, or a `models` stand-in)
    *.txt            input files the design reads while it runs, listed
                     in `inputs`
    gohard.yaml      optional design-local project config (`config`)
    golden/          written only by `go run ./tools/oracle --update`
      trace.txt      GoHard's canonical text trace (#36) at seed 0, the
                     golden (section `golden`; needs the interpreter)
      oracle.ghdl.txt, oracle.nvc.txt
                     GHDL/NVC VCD dumps as settled values per time under
                     GoHard's paths (section `trace`, see below)
      ghdl.tree.txt  GHDL --disp-tree=inst (section `tree`)
      format.ghdl.txt  formatter oracle (#32): GHDL analysis of every
      format.nvc.txt   file as written and as formatted (NVC likewise)
      hierarchy.txt  hand-written expected hierarchy and generic values
                     (only when `hierarchy_oracle: "none, hand-checked"`)
```

* The 14 designs of #8 are `md_resolve`, `wait_forms`, `postponed_attrs`,
  `force_ext`, `textio_env`, `assert_fail`, `budget_hang`, `vhdl93_only`,
  `mode_views`, `prot_cond`, `psl_simple`, `hier_generate`, `overload_ops`,
  `standin_model`. No other design is required for v1.0. The 15th,
  `gc_heap` (milestone v1.1), is the #27 garbage-collection design; the
  16th, `psl_comments` (milestone v1.1), is the #109 `psl.comments`
  design. #63 adds six designs for the gaps `LIMITS.md` listed
  (milestone v1.1): `case_generate`, `delayed_attrs`, `ext_vars`,
  `force_out`, `psl_halt` and `race_order`; it also extends
  `textio_env` (a checked-in input file), `vhdl93_only` (a
  non-protected shared variable and the `shared_2002` variant) and
  `mode_views` (the `--define` variant `define_narrow`). Since the v1.1
  release the v1.1 designs (`prot_cond`, `gc_heap`, `psl_comments` and
  the six #63 designs) are required to pass with `expected`. The 8
  vhdl-examples are demos and are not part of the corpus.
* Simulator work files (GHDL `*.cf`/`*.o`, NVC `work/` libraries, waveform
  dumps) never enter the repo. Run the tools in a `/tmp` work dir.
* If `testdata/corpus/*/golden/` exceeds 50 MB in total, the goldens move
  to `connerohnesorge/gohard-traces` as one pinned submodule with no other
  workflow change. Until then that repo is unused.
* Third-party sources are pinned git submodules under `testdata/` (see
  `regression:` below). There is no fetch script. A missing submodule fails
  the test immediately with a message naming `git submodule update --init`;
  it never skips.

## Fragments and merge

Builders may write fragments; none exist after a merge:

* `testdata/corpus/<name>/entry.yaml`: one design mapping at the top
  level (not a list, no `designs:` key).
* `testdata/corpus/regression.entry.yaml`: a bare list of `regression:`
  entries sorted by `name`.

The corpus gate checks every fragment against this schema and the
vocabularies in `corpus.yaml`, inserts design fragments into `designs:`
and regression items into `regression:` (both sorted by `name`, text
indented as list items so comments are kept), and removes the
fragments. All 14 design fragments and the regression fragment have
been merged (S2a); `corpus.yaml` is now the only source of truth; edit
it directly.

## `corpus.yaml` top level

| Key | Meaning |
| --- | --- |
| `schema` | Manifest schema version, `1`. |
| `vocabulary.features` | Fixed feature ids: the semantics listed in #8 "Done when" plus the ids added by #27, #109 and #63. Every id must appear in at least one design, and a design may name only these ids (`corpus.ParseManifest` checks both). |
| `vocabulary.invariants` | Fixed invariant ids a design may claim. |
| `designs` | The 22 design entries, sorted by `name`. |
| `regression` | Parser-regression submodules, sorted by `name`. |

Unknown keys are an error. Lists keep a fixed order (sorted by `name` or
by `path`) so tools rewrite the file deterministically.

## Design entry

```yaml
- name: md_resolve
  standard: "2008"
  milestone: v1.0
  files:
    - path: md_resolve.vhd
      standard: "2008"
    - path: md_resolve_tb.vhd
      standard: "2008"
  top: md_resolve
  testbench: md_resolve_tb
  defines: {}
  models: {}
  expected:
    exit: 0
    status: clean
    stop_time: 120 ns
    messages: []
  features: [delta_ordering, multi_driver_resolution]
  invariants: [seed_independent, variant_stable, self_stopping]
  oracle: both
  hierarchy_oracle: ghdl
  ghdl:
    version: 6.0.0
    command: ghdl -a --std=08 ...; ghdl --elab-run --std=08 md_resolve_tb
    result: pass
    exit: 0
    stop_time: 120 ns
    notes: finished via std.env.finish
  nvc:
    version: 1.23.0
    command: nvc --std=2008 -a ... -e md_resolve_tb -r
    result: pass
    exit: 0
    stop_time: 120 ns
    notes: same report lines as GHDL
  disagreements: []
  trace_sha256: pending
  oracle_sha256: pending
```

### Fields

| Field | Type | Meaning |
| --- | --- | --- |
| `name` | string | Design name; equals the directory name. |
| `standard` | `"1993"`\|`"2002"`\|`"2008"`\|`"2019"` | Minimum standard of the design (quoted, as in `gohard.yaml`, #28). It is the standard GoHard runs the design under. |
| `milestone` | `v1.0`\|`v1.1` | Release that must pass it. `prot_cond`, `gc_heap`, `psl_comments` and the six #63 designs are `v1.1`; the other 13 are `v1.0`. The release under test is `corpus.Release` (`v1.1`); a design of a later milestone is a manifest error. |
| `files` | list | Every source file, in analysis order, each `{path, standard}`. `path` is relative to the design directory; `standard` is the minimum standard of that file. |
| `top` | string | Design-under-test entity (the unit whose hierarchy and LSP sessions matter). Equals `testbench` when the testbench is the whole design. |
| `testbench` | string | Portless testbench entity that is the elaboration root, run with `gohard check --sim --tb <testbench>` and by the oracles. |
| `defines` | map identifier to string | `defines:` passed to GoHard and the oracles (#28). `{}` except in `mode_views`, which needs one user identifier. Built-in identifiers (`VHDL_VERSION`, `TOOL_*`) are never allowed here. |
| `models` | map `lib.unit` to path | `simulation.models` stand-ins (#35). `{}` except in `standin_model`. Paths are relative to the design directory. A stand-in file is not listed in `files`. |
| `config` | path | Optional. Design-local `gohard.yaml` (#28), relative to the design directory, loaded by GoHard and `tools/oracle` as the project config. Its `defines` and `simulation.models` must equal the entry's `defines` and `models`. Absent means all #28 defaults. Required whenever `defines` or `models` is non-empty, since #31 takes user identifiers only from `gohard.yaml` `defines:` or `--define`. Used by `mode_views` (`defines: {MV_PROFILE: "wide"}`), `psl_comments` (`psl.comments: true`, which `tools/oracle` passes to GHDL as `-fpsl` and to NVC as `-a --psl`), `psl_simple` (`simulation.stop_level: failure`) and `standin_model` (`allow_opaque`, `simulation.models`). |
| `inputs` | list of paths | Optional. Checked-in data files the design reads while it runs (textio), relative to the design directory; variants read them too. The corpus harness serves them to the interpreter as the project files of the run, as `gohard check --sim` run in the design directory reads them, and `tools/oracle` copies them into every GHDL and NVC work directory and hashes them into each recording's `sources` line. Used by `textio_env` (`textio_env_in.txt`, #63). |
| `expected` | mapping | GoHard's outcome (see below). Every design, `prot_cond` and `gc_heap` included, runs against it. |
| `expected_v1_0` | retired | The v1.0 outcome of a v1.1 design (exit 2, `unsupported/construct`) while one of its constructs was not delivered. Retired with the v1.1 release: no design has it, and `corpus.ParseManifest` rejects a manifest that does, since nothing would check it. Its last use is in the git history. |
| `features` | list of ids | Semantics this design exercises, from `vocabulary.features`. May be empty for designs whose purpose is an outcome (`assert_fail`, `budget_hang`). |
| `invariants` | list of ids | From `vocabulary.invariants`. Every design claims `variant_stable`, and exactly one of `seed_independent` and `races_by_design`: the deliberately racy `race_order` claims `races_by_design`, every other design `seed_independent` (`TestManifest` checks it). |
| `oracle` | `ghdl`\|`nvc`\|`both`\|`none, hand-checked` | Which tool results the golden trace is compared with. `none, hand-checked` means the golden trace is hand-written and reviewed once. Quote it in YAML: `oracle: "none, hand-checked"`. |
| `hierarchy_oracle` | `ghdl`\|`none, hand-checked` | Source of the elaboration oracle (hierarchy and generic values, #8). `ghdl`: the `--disp-tree` output recorded by `tools/oracle`. `none, hand-checked`: a hand-written `golden/hierarchy.txt`, reviewed once; required whenever `ghdl.result` is `unsupported`, `analysis-error` or `elab-error` (the 2019 designs `mode_views` and `prot_cond`, and `force_out`, whose force modes GHDL 6.0.0 cannot parse). |
| `ghdl`, `nvc` | mapping | What each tool did (below). Present even when the tool is not the oracle, so the reason is on record. |
| `disagreements` | list | Every point where GHDL and NVC differ (below). Empty when they agree. |
| `variants` | list | Optional. Extra runs of the same design with other files or another standard, each with its own expected outcome (below). |
| `trace_sha256` | hex \| `pending` | sha256 of `golden/trace.txt`. |
| `oracle_sha256` | hex \| `pending` | sha256 over the recorded oracle files in `golden/` (GHDL/NVC traces, `--disp-tree`), or over the hand-checked files for `none, hand-checked`. |
| `format_oracle_sha256` | hex | Formatter oracle (#32): hash over `golden/format.ghdl.txt` and `golden/format.nvc.txt` (see "Formatter oracle" below). Never `pending`; written by `tools/oracle` on the line after `oracle_sha256`. |
| `semerr_sha256` | hex | Semantic-error oracle (#34): hash over `testdata/corpus/golden/semerr/corpus-<name>.txt` (see "Semantic-error oracle" below). Written by `tools/oracle` on the line after `format_oracle_sha256`. |
| `semerr_follows` | list | Optional. Recorded semantic-error disagreements GoHard decides (see "Semantic-error oracle"). |
| `semerr_decisions` | list | Optional. Recorded lines where GoHard decides against both tools, with the LRM section (see "Semantic-error oracle"). |

Every hash field is the lower-case hex sha256 over the field's golden
files in name order, each fed as its name (relative to `golden/`), a
newline, its length in decimal, a newline and its bytes.

`pending` is allowed only until `tools/oracle` and the #36 canonical trace
format exist (S5). After that CI fails on `pending` and on any hash
mismatch, printing the first differing trace line.

### `expected`

| Field | Meaning |
| --- | --- |
| `exit` | `gohard check --sim` exit code (#20): 0 clean, 1 design problems, 2 tool error or unsupported construct. |
| `status` | JSON `status` (#20): `clean`, `problems`, `tool_error` or `interrupted`. |
| `stop_time` | Simulated time at which the run ends, as `<integer> <unit>` (`fs`..`hr`), for example `120 ns`. For `budget_hang` it is the budget that ends it (`1 ms`, the default `simulation.time_budget`). `none` when no simulation runs (analysis or elaboration ends the run, or exit 2 before elaboration). |
| `messages` | Ordered list of expected diagnostics; empty for a clean run. Each item: `severity` (`error`\|`warning`\|`note`\|`hint`), `text` (substring the diagnostic message must contain, normally the VHDL `report` text), optional `code` (an `internal/diag` code such as `sim/budget-exceeded` or `unsupported/construct`), optional `time` (`<integer> <unit>`), optional `path` (a file from `files`, relative to the design directory) and `line` (1-based; required with `path`). `path` and `line` are required on `unsupported/construct` messages. The list is in #20 output order: sorted by `path` (bytewise, so `prot_cond.vhd` sorts before `prot_cond_pkg.vhd`), then `line`; located messages must appear in that order. |

Only the listed messages may appear. A self-checking testbench reports
nothing on success, so `messages: []` is the normal pass.

### `variants`

A variant is a second, independent check of the design: the same
testbench with some files swapped (`hier_generate` `width_err`: one
generate instance wired one bit too narrow) or the same files analysed
under another standard (`vhdl93_only` `std_2002`, `std_2008`,
`shared_2002`: the `standard_gated` rejection), or the same files with
a `--define` (`mode_views` `define_narrow`). Each item has these keys:

| Field | Meaning |
| --- | --- |
| `name` | Variant id, unique within the design. |
| `standard` | Standard the variant runs under. |
| `defines` | Optional map: the `--define NAME=VALUE` arguments of the run. They override the design config's `defines:` the way the command line does (`config.ApplyDefines`); `tools/oracle` passes the result to NVC as `-D` in the format and semerr sections. |
| `files` | Full analysis list for the variant, as in `files`. A file that appears only here (for example `hier_generate_width_err.vhd`) is allowed in the design directory. |
| `expected` | As `expected`. `messages` may list only the first error where parser recovery can add more; the listed ones must appear, in order. |
| `ghdl`, `nvc` | As for the design. |
| `disagreements` | Optional; as for the design. |
| `notes` | Free text. |

Recorded tool results for a variant go in `golden/` as `<variant>.*`
and are covered by the design's `oracle_sha256`.

### `ghdl` / `nvc`

| Field | Meaning |
| --- | --- |
| `version` | Tool version used (`6.0.0` for GHDL, `1.23.0` for NVC, from the nix dev shell). |
| `command` | Exact commands, including `--std` and any bounded `--stop-time`. |
| `result` | `pass` (self-checking pass), `fail` (assertion failure as designed), `bounded` (still running at the bounded `--stop-time`, as `budget_hang` must be), `analysis-error`, `elab-error` or `unsupported` (the tool cannot handle the standard or construct). |
| `exit` | Process exit code of the last command. |
| `stop_time` | Simulated time the tool reported at the end, or `none`. |
| `messages` | Optional. The report/assert lines the tool printed, verbatim minus file paths. |
| `notes` | Free text: anything worth knowing, for example why a tool cannot be the oracle. |

### `disagreements`

Each item: `topic` (what differs), `ghdl` and `nvc` (what each did),
`follows` (`ghdl`, `nvc` or `lrm` when both deviate), `lrm` (the IEEE 1076
section that decides it, for example `14.7.3.4`), `notes`, and optional
`signals` (trace paths): the trace oracle compares those signals with
the followed tool's recording only, and drops them from the other's. The same
rule applies to semantic errors: wherever both tools report an error on a
line of a PR-set file GoHard must report one on that line, and every
disagreement is recorded here.

## `regression:` entries

Parser-regression sources (#8, #30) are read only and never shipped in the
binary. Each is a pinned git submodule under `testdata/`, licence checked
before pinning.

```yaml
- name: ghdl
  path: testdata/ghdl
  url: https://github.com/ghdl/ghdl.git
  commit: <40-hex pinned commit>
  license: GPL-2.0-or-later
  tag: pr
  subfolder: testsuite
  expected_failures:
    - testsuite/gna/*/testsuite.sh
    - testsuite/sanity/*/testsuite.sh
```

| Field | Meaning |
| --- | --- |
| `name` | Short id (`ghdl`, `nvc`, `open-logic`, `poc`, `grlib`, `surf`, `osvvm`, `uvvm`, `vunit`, `neorv32`). |
| `path` | Submodule path from the repo root, as in `.gitmodules`. Libraries go under `testdata/libs/` (OSVVM, UVVM, VUnit). |
| `url` | Upstream clone URL, as in `.gitmodules`. |
| `commit` | Pinned commit SHA; must equal the gitlink in the repo. |
| `license` | SPDX licence expression for the parsed `subfolder` (ids, `LicenseRef-*`, `AND`/`OR`/`WITH`). When files under the subfolder carry other terms (GRLIB vendor files), or the tree has no licence file so the licence is read from headers, the expression must name the extra terms with a `LicenseRef-*` term. |
| `tag` | `pr` (parsed on every pull request) or `nightly` (only in the nightly full run). PR: the GHDL `testsuite/` (incl. VESTS) and NVC `test/` folders. Nightly: everything. |
| `subfolder` | Folder inside the submodule that is parsed (checked out alone for `pr` entries); `.` for the whole tree. |
| `expected_failures` | List of paths or globs, relative to the submodule, of every upstream file that carries the project's own expected-failure data (for GHDL the `testsuite.sh` scripts of `gna/` and `sanity/` plus both VESTS `non_compliant.exp` lists; for NVC `test/regress/testlist.txt` and every `test/test_*.c`). How each kind is read is written as a comment above the entries in `corpus.yaml`. A `.vhd`/`.vhdl` path or glob names source files that are not VHDL (preprocessor templates, documentation snippets) or are invalid upstream: they may fail, and a one-line comment above each such entry gives the reason. `[]` if every file must parse. |

The bundled IEEE packages in `internal/stdlib/` and the 22 corpus designs
are always part of the PR set and need no entry. gohard-old is not used.

## Tooling

GHDL and NVC never run in CI; only results recorded by `tools/oracle` are
compared. To check a design by hand, copy it to a temporary work dir
and run everything inside that dir, so work libraries and files a
testbench writes (`textio_env` writes `textio_env.log` into the current
directory) never land in the repo:

```
w=$(mktemp -d); cp -r testdata/corpus/<name>/. "$w"; cd "$w"
f="a.vhd b_tb.vhd"                              # in `files` order
nix develop <repo> -c ghdl -a --std=08 $f
nix develop <repo> -c ghdl --elab-run --std=08 <testbench>
nix develop <repo> -c nvc --std=2008 -a $f -e <testbench> -r
cd - && rm -rf "$w"
```

The `command` recorded in each `ghdl`/`nvc` block is run exactly this
way, from inside the copied design directory, which also holds the
design's `inputs`.

Use `--std=93`/`--std=1993` or `--std=19`/`--std=2019` as the entry's
`standard` requires. Append `--stop-time=<t>` (after the unit for GHDL,
after `-r` for NVC) for any design that must not terminate on its own.

## Trace and hierarchy oracles

`go run ./tools/oracle --update trace tree` (inside `nix develop`) runs
GHDL (`ghdl -a`, then `ghdl --elab-run <tb> --vcd=dump.vcd
--assert-level=<stop_level>`) and NVC (`nvc -a [-D N=V] ... -e <tb> -r
--wave=dump.vcd --format=vcd --dump-arrays --exit-severity=<stop_level>`),
with `-fpsl` on every GHDL command (`tree` too) and `--psl` after NVC's
`-a` when the design's `gohard.yaml` sets `psl.comments`,
on the main run of every design whose `oracle` names them, in a fresh
`/tmp` work dir that also holds the design's `inputs`, with
`--stop-time=<stop_time>` when the tool's recorded
result is `bounded`. Files carrying an IEEE 1735 envelope are left out
(the `models` stand-in replaces them). Each dump is converted to
`golden/oracle.<tool>.txt`:

```
gohard-oracle 1
tool <tool> <version>
sources <hash of the analysed files and config>
testbench <tb>
signal <path> logic|integer|real <width>      (sorted by path)
<time fs> <path> <value>                       (settled value per time)
digest <changes> <sha256 of all change lines>
```

Paths are the VCD scopes below the testbench joined with `.`, lower
case, without the `[l:r]` range: GoHard's hierarchical paths. Values
are the value each signal settles on at each time (the VCD carries no
deltas): logic characters upper case, vectors left to right, integers in
decimal. Only the first 4096 change lines are kept; the digest covers
all of them (`budget_hang`). Signals a tool cannot dump (width 0) are
left out. The check (`go test ./tools/oracle`, no tool runs) fails on a
stale `sources` line, on a signal both tools dump with different
settled values unless a `disagreements` item lists it in `signals`, and,
once the sequential interpreter is registered, on any difference between
the recording and GoHard's settled projection of its own trace (the
first differing change is shown).

`tree` records `ghdl --elab-run <tb> --disp-tree=inst --stop-time=0fs`
as `golden/ghdl.tree.txt`; the check compares its instances, blocks and
generate iterations, in order, with `internal/elab/hier` (alternative
labels of if/case generates, which GHDL does not print, are dropped).
`--disp-tree` prints no generic values. Designs with `hierarchy_oracle:
"none, hand-checked"` keep a hand-written `golden/hierarchy.txt` in
hier's `Dump` form (generic values included; `#` lines are comments),
compared exactly (every design's milestone is reached since v1.1).
Both files feed
`oracle_sha256`.

`golden/trace.txt` is GoHard's own trace at seed 0 (section `golden`,
`trace_sha256`); it is written once the sequential interpreter exists.

## Corpus harness

`go test ./internal/corpus` runs every design (no skips): the main run
and each `variants` item at seeds 0..3 (`-corpus.seeds=64` nightly via
`scripts/ci/corpus-seeds.sh`), checks `expected`, byte-identical traces across seeds (first differing line shown), and
`golden/trace.txt` with `trace_sha256`. Four generated variants of each
design (#8 `variant_stable`) must give the same outcome with lines
ignored: `layout` (doubled white space and line breaks), `case` (reserved
words and identifiers in upper case), `rename` (every declared signal,
variable and constant gets `_rn`, consistently in all files, `-- psl`
lines included) and `reorder` (the concurrent statements of each
architecture without tool directives, reversed; a `-- psl` statement
moves with its marker, and a `-- psl` default clock, property, sequence
or endpoint declaration stays in place while the statements on each side
of it are reversed, since GHDL and NVC clock only the directives after a
`-- psl default clock`). A design that cannot simulate yet for want of
an interpreter is logged under plain `go test` and fails under
`-corpus.require-sim`.

The racy design (`race_order`, invariant `races_by_design`, #63) is
checked the other way round: at every seed N >= 1 the run must have
`expected` and, compared with seed 0 as `gohard check --sim --seed N`
compares it (`corpus.Races`), give exactly one `sim/race` warning; and
the checked seeds must give at least two different traces.
`TestRacyDetects` shows the check failing a race_order whose writers no
longer race.

## Formatter oracle

`go run ./tools/oracle --update format` (inside `nix develop`) formats
every file of every design with the design's `format.*` options
(`config`, or the #28 defaults), under the standard the file is first
listed with. It then analyzes each run (the design's `files`, with the
`models` stand-ins first, and every variant's `files`, under the run's
standard) twice in fresh `/tmp` work dirs, one `ghdl -a --std=XX
[-fpsl]` or `nvc --std=XXXX -a [--psl] [-D NAME=VALUE]` per file (the
PSL options when the design sets `psl.comments`, which the run line
records as ` psl`): once as written, once as
formatted. Error lines of the formatted text are mapped back to the
original lines, and `golden/format.{ghdl,nvc}.txt` record, per file,
the original and formatted sha256, both exit codes, the original
errors and every `new` (only formatted) or `gone` (only original)
error, ending in `result new=N`.

CI never runs GHDL or NVC. `go test ./tools/oracle` (`TestRecorded`,
the same check as `go run ./tools/oracle` without `--update`)
recomputes the formatter output and fails with "rerun go run
./tools/oracle --update" when a recorded `options`/`run`/`file` line no
longer matches (the formatter, a source file or a design config
changed), when a golden file is missing, or when
`format_oracle_sha256` does not match the golden files. It also fails
while any recording says `result new=` other than 0: fix the formatter
first.

## Semantic-error oracle

`go run ./tools/oracle --update semerr` (inside `nix develop`, about a
minute) runs GHDL and NVC analysis on every PR-set file and records,
per analysis, each tool's exit code and the lines of the file it
reports errors on, in `testdata/corpus/golden/semerr/`:

| File | Runs |
| --- | --- |
| `corpus-<name>.txt` | each corpus design run (the `models` stand-ins, then `files`; each variant), under its standard and `gohard.yaml` defines |
| `ghdl-<folder>.txt` | GHDL `testsuite/<folder>/*/testsuite.sh`: the `analyze`, `analyze_failure`, `elab_simulate` and `synth*` file operands in script order (the shell model of the parser-regression runner), one run per work directory and standard, a `clean` ending the runs; `--work=`, `-frelaxed`, `-fpsl`, `-fsynopsys` and the other analysis options kept; `--std=87` and generated files skipped |
| `ghdl-vests.txt` | each VESTS `.exp` list sourced by `vests/testsuite.sh`, in list order, `LIBRARY=` honoured, under `--std=93c` |
| `nvc-regress.txt` | each `test/regress/testlist.txt` test (`<name>.vhd` alone, its standard, `work=`, `relaxed`, `psl`, `define=`) |
| `nvc-unit.txt` | each `START_TEST` block of `test/test_*.c` per standard it runs under: its `input_from_file` files in order (identical runs once) |
| `ghdl-alone.txt`, `nvc-alone.txt` | every other `.vhd`/`.vhdl` file of the folder, alone, under 2008 |

Each run is analyzed in a fresh work directory per tool, one command
per file (`ghdl -a --std=XX [--work=L] [options] file`,
`nvc --std=XXXX [--work=L] -L . -a --error-limit=0 [--relaxed]
[--psl] [-D N=V] file`). A file line reads `<arg> [lib=L] [options]
ghdl=<exit>[@lines] nvc=<exit>[@lines]`; `~` marks a line whose only
GHDL errors are cascades (`<unit> was not analysed`). The run line
carries a sha256 prefix of the run's sources, so a changed source or
driver makes the recording stale. The hashes are `semerr_sha256` of
each design and of the `ghdl` and `nvc` regression entries.

`go test ./tools/oracle` (`TestRecorded`, a few seconds, no tool runs)
checks that wherever both tools report an error on a line, GoHard
reports an error on that line (an error diagnostic starting there, or
one of at most three lines covering it). GoHard analyzes each file
with the earlier files of its run it may depend on (a lexical scan for
unit names, transitively). Not checked: files whose dependencies one
tool rejected (cascades), GHDL cascade lines, lines after GoHard's
first syntax error (recovery is implementation-defined), and files
GoHard reports opaque (`unsupported/construct`, encrypted IP, exit 2).

Known gaps are listed in `golden/semerr/gaps.txt`, written from
GoHard's results by `go test ./tools/oracle -run TestSemerrGaps
-update-gaps` (which also logs the agreement numbers; add
`-semerr-false` to count GoHard errors in files both tools accept).
The check fails on a gap the list lacks and on a listed gap that is
closed, so the list only shrinks.

Every line only one tool reports is a disagreement; the goldens record
them all. A disagreement GoHard decides is a `semerr_follows` item of
the design or regression entry: `path` (relative to the design
directory or the entry's subfolder), `line`, `follows` (`ghdl` or
`nvc`), `lrm` (the IEEE 1076 section) and `notes`. The check then
requires GoHard to report an error on that line exactly when the
followed tool does.

Where both tools agree and GoHard, for a reason in the LRM, does not,
the line is a `semerr_decisions` item of the entry: `path`, `line`,
`gohard` (`none` on a line both tools reject that GoHard accepts, or
`error` on a line of a file both tools accept that GoHard rejects;
line `0` records every error of the file), `lrm` and `notes`. The
check fails on a GoHard error in a file both tools accept that no item
records, and on an item GoHard no longer matches, so neither a gap
nor a false error is ever waived silently; `gaps.txt` stays empty.
