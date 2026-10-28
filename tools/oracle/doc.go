// Command oracle records GHDL and NVC results for the oracle corpus
// (#8) and checks that the recorded results still match GoHard.
//
// It is developer-only: it is never imported by the gohard binary and
// GHDL and NVC never run in CI. Run it from the Nix dev shell, which
// pins both tools:
//
//	nix develop -c go run ./tools/oracle --update    # run the tools
//	nix develop -c go run ./tools/oracle             # check only
//	go run ./tools/oracle --update format            # one section
//	go run ./tools/oracle --update -design nvc semerr
//	go run ./tools/oracle --update -design md_resolve
//	go run ./tools/oracle --update -design protected/counter conformance
//
// Without --update the command only checks, without running any tool:
// the same check runs in CI as TestRecorded (go test ./tools/oracle).
//
// # Sections
//
// Each kind of recording is one section with a name, the corpus.yaml
// hash field it feeds and the golden files it owns. A section has a
// pure plan (what to record, from corpus.yaml and the GoHard packages),
// a record step (runs the tools, --update only) and a pure check
// (recorded results against the current GoHard output). The shared code
// writes golden files only when their bytes change, hashes them, and
// rewrites only the changed hashes in corpus.yaml.
//
//   - format (#32): every corpus file is formatted with the design's
//     format.* options; GHDL and NVC analyze the original and the
//     formatted files (NVC with the run's defines: the config's, or a
//     variant's `defines` over them), and golden/format.{ghdl,nvc}.txt
//     record that the formatted text adds no errors. Hash:
//     format_oracle_sha256.
//
//   - semerr (#34, R12): the semantic-error oracle. GHDL and NVC
//     analyze every PR-set file (the corpus designs, GHDL's testsuite
//     folder and NVC's test folder) in the analysis groups and order
//     of the projects' own drivers (each testsuite.sh between cleans,
//     the VESTS lists, run_regr.c tests, unit-test blocks; files no
//     driver names alone under 2008), and golden/semerr/*.txt record
//     per analysis each tool's exit code and error lines. The check
//     requires a GoHard error on every line both tools report one on,
//     with the known gaps listed in golden/semerr/gaps.txt (rewritten
//     by go test ./tools/oracle -run TestSemerrGaps -update-gaps; the
//     list only shrinks). Hash: semerr_sha256 in each design and in
//     the ghdl and nvc regression entries. See semerr_check.go for
//     the rules (dependency closure, cascades, syntax recovery,
//     opaque units, semerr_follows, semerr_decisions: a GoHard error
//     in a file both tools accept fails unless recorded).
//
//   - trace (#36, #8): GHDL (--vcd) and NVC (--wave --format=vcd
//     --dump-arrays) run the main run of each design with a trace
//     oracle (the design's `oracle` field), the opaque IEEE 1735
//     files left out (the stand-in replaces them), the design's
//     `inputs` in the work directory (hashed into the sources line),
//     with the design's stop_level as --assert-level/--exit-severity,
//     -fpsl/--psl when it sets psl.comments, and the expected
//     stop_time as --stop-time for a "bounded" tool result. The dumps
//     become golden/oracle.{ghdl,nvc}.txt (corpus.Oracle: settled
//     values per time under GoHard's hierarchical paths, the first
//     corpus.OracleLines changes plus a digest of all). The check
//     flags stale recordings (sources hash), requires GHDL and NVC to
//     agree on every signal both dump except those a disagreements
//     item with `signals:` assigns to one tool, and, once the
//     sequential interpreter is registered, compares GoHard's settled
//     projection (corpus.CompareOracle). Hash: oracle_sha256.
//
//   - tree (#8): the elaboration oracle. hierarchy_oracle: ghdl
//     records GHDL --disp-tree=inst as golden/ghdl.tree.txt and the
//     check compares its instances, generate iterations and blocks
//     with internal/elab/hier; "none, hand-checked" keeps a
//     hand-written golden/hierarchy.txt in hier's Dump form (generic
//     values included), compared exactly for every design of a
//     milestone corpus.Release reached (all since v1.1). Hash:
//     oracle_sha256.
//
//   - golden (#36): GoHard's own canonical trace at seed 0,
//     golden/trace.txt. It has targets only once an interpreter is
//     registered (simrun.DefaultCode); until then trace_sha256 stays
//     pending and internal/corpus reports the designs as pending.
//     Hash: trace_sha256.
//
//   - coverage (#62): GoHard's lcov coverage of the same run,
//     golden/coverage.info, for the designs the golden section
//     records. It runs no external tool; the goldens of wait_forms,
//     hier_generate and psl_simple were checked by hand
//     (testdata/corpus/README.md). Hash: coverage_sha256.
//
//   - stdlib (#33): the declaration sets of GHDL's VHDL-93, 2008 and
//     2019 std/ieee sources (lib/ghdl/src of the shell's ghdl) and of
//     NVC's VHDL-2008 sources (NVC ships them compiled only, so they
//     are read from the pinned testdata/nvc submodule, whose
//     configure.ac version must be the shell's nvc), recorded as
//     internal/stdlib/testdata/{ghdl,nvc}/<std>.txt (stdlib.RefSets).
//     The internal/stdlib tests derive the first-standard tags from
//     them and compare the 2008 view with both 2008 sets; the check
//     here verifies each file's provenance and source list. No hash.
//
//   - conformance (#18): NVC runs each #18 conformance case that has a
//     testdata/conformance/<feature>/<case>.nvc.txt (nvc --std=STD -a
//     <case>.vhd -e TOP -r, in a folder holding only the case), and
//     the file records the command, NVC version, exit code, output and
//     the case source's sha256. The check requires the recording to be
//     of the current source and command, and NVC's report and
//     assertion messages and pass/fail to equal the case's .want
//     (gohard check --sim), unless the case is pending or says
//     nvc-differs. Start a recording by creating the file empty. No
//     hash: the source hash inside each recording makes it stale.
//
// Several sections may feed one hash field (oracle_sha256 covers the
// GHDL/NVC traces and the --disp-tree output): the
// field is the hash over every golden file of every section that names
// it, so a section rewrites the field without knowing the others.
//
// # Hashes
//
// A hash field holds the lower-case hex sha256 over the field's golden
// files in name order, each written as its name, a newline, its length
// in decimal, a newline and its bytes (goldenHash). corpus.yaml is
// edited in place through the positions of its yaml.Node tree: only the
// hash scalar changes (or one line is added after an anchor field), so
// comments and layout are kept byte for byte.
package main
