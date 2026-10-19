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
    gohard.yaml      optional design-local project config (`config`)
    golden/          written only by `go run ./tools/oracle --update`
      trace.txt      canonical text trace (#36), the golden
      ghdl.*         recorded GHDL results (trace, semantic errors,
                     --disp-tree hierarchy)
      nvc.*          recorded NVC results
      hierarchy.txt  hand-written expected hierarchy and generic values
                     (only when `hierarchy_oracle: "none, hand-checked"`)
```

* The 14 designs are `md_resolve`, `wait_forms`, `postponed_attrs`,
  `force_ext`, `textio_env`, `assert_fail`, `budget_hang`, `vhdl93_only`,
  `mode_views`, `prot_cond`, `psl_simple`, `hier_generate`, `overload_ops`,
  `standin_model`. No other design is required for v1.0. The 8
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
| `vocabulary.features` | Fixed feature ids. Exactly the semantics listed in #8 "Done when"; every id must appear in at least one design. |
| `vocabulary.invariants` | Fixed invariant ids a design may claim. |
| `designs` | The 14 design entries, sorted by `name`. |
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
| `milestone` | `v1.0`\|`v1.1` | Release that must pass it. `prot_cond` is `v1.1`; the other 13 are `v1.0`. |
| `files` | list | Every source file, in analysis order, each `{path, standard}`. `path` is relative to the design directory; `standard` is the minimum standard of that file. |
| `top` | string | Design-under-test entity (the unit whose hierarchy and LSP sessions matter). Equals `testbench` when the testbench is the whole design. |
| `testbench` | string | Portless testbench entity that is the elaboration root, run with `gohard check --sim --tb <testbench>` and by the oracles. |
| `defines` | map identifier to string | `defines:` passed to GoHard and the oracles (#28). `{}` except in `mode_views`, which needs one user identifier. Built-in identifiers (`VHDL_VERSION`, `TOOL_*`) are never allowed here. |
| `models` | map `lib.unit` to path | `simulation.models` stand-ins (#35). `{}` except in `standin_model`. Paths are relative to the design directory. A stand-in file is not listed in `files`. |
| `config` | path | Optional. Design-local `gohard.yaml` (#28), relative to the design directory, loaded by GoHard and `tools/oracle` as the project config. Its `defines` and `simulation.models` must equal the entry's `defines` and `models`. Absent means all #28 defaults. Required whenever `defines` or `models` is non-empty, since #31 takes user identifiers only from `gohard.yaml` `defines:` or `--define`. Used by `mode_views` (`defines: {MV_PROFILE: "wide"}`), `psl_simple` (`simulation.stop_level: failure`) and `standin_model` (`allow_opaque`, `simulation.models`). |
| `expected` | mapping | GoHard's outcome once `milestone` is reached (see below). |
| `expected_v1_0` | mapping | Only for `milestone: v1.1`. The outcome under v1.0: exit 2, status `tool_error`, one `unsupported/construct` message per v1.1 construct, each saying "planned for v1.1". |
| `features` | list of ids | Semantics this design exercises, from `vocabulary.features`. May be empty for designs whose purpose is an outcome (`assert_fail`, `budget_hang`, `vhdl93_only`). |
| `invariants` | list of ids | From `vocabulary.invariants`. Every design claims `variant_stable`; every design except a deliberately racy one claims `seed_independent`. |
| `oracle` | `ghdl`\|`nvc`\|`both`\|`none, hand-checked` | Which tool results the golden trace is compared with. `none, hand-checked` means the golden trace is hand-written and reviewed once. Quote it in YAML: `oracle: "none, hand-checked"`. |
| `hierarchy_oracle` | `ghdl`\|`none, hand-checked` | Source of the elaboration oracle (hierarchy and generic values, #8). `ghdl`: the `--disp-tree` output recorded by `tools/oracle`. `none, hand-checked`: a hand-written `golden/hierarchy.txt`, reviewed once; required whenever `ghdl.result` is `unsupported`, `analysis-error` or `elab-error` (the 2019 designs `mode_views` and `prot_cond`). |
| `ghdl`, `nvc` | mapping | What each tool did (below). Present even when the tool is not the oracle, so the reason is on record. |
| `disagreements` | list | Every point where GHDL and NVC differ (below). Empty when they agree. |
| `variants` | list | Optional. Extra runs of the same design with other files or another standard, each with its own expected outcome (below). |
| `trace_sha256` | hex \| `pending` | sha256 of `golden/trace.txt`. |
| `oracle_sha256` | hex \| `pending` | sha256 over the recorded oracle files in `golden/` (GHDL/NVC traces, semantic errors, `--disp-tree`), or over the hand-checked files for `none, hand-checked`. |

`pending` is allowed only until `tools/oracle` and the #36 canonical trace
format exist (S5). After that CI fails on `pending` and on any hash
mismatch, printing the first differing trace line.

### `expected` and `expected_v1_0`

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
under another standard (`vhdl93_only` `std_2002`, `std_2008`: the
`standard_gated` rejection). Each item has exactly these keys:

| Field | Meaning |
| --- | --- |
| `name` | Variant id, unique within the design. |
| `standard` | Standard the variant runs under. |
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
section that decides it, for example `14.7.3.4`), `notes`. The same
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
| `expected_failures` | List of paths or globs, relative to the submodule, of every upstream file that carries the project's own expected-failure data (for GHDL the `testsuite.sh` scripts of `gna/` and `sanity/` plus both VESTS `non_compliant.exp` lists; for NVC `test/regress/testlist.txt` and every `test/test_*.c`). How each kind is read is written as a comment above the entries in `corpus.yaml`. `[]` if every file must parse. |

The bundled IEEE packages in `internal/stdlib/` and the 14 corpus designs
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
way, from inside the copied design directory.

Use `--std=93`/`--std=1993` or `--std=19`/`--std=2019` as the entry's
`standard` requires. Append `--stop-time=<t>` (after the unit for GHDL,
after `-r` for NVC) for any design that must not terminate on its own.
