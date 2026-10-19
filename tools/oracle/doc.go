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
//     formatted files, and golden/format.{ghdl,nvc}.txt record that the
//     formatted text adds no errors. Hash: format_oracle_sha256.
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
//     opaque units, semerr_follows).
//
// Planned sections, added by later stages with the same shape:
//
//   - trace (S5, #36): canonical GoHard traces (golden/trace.txt,
//     trace_sha256) and GHDL/NVC VCD dumps converted to the canonical
//     form (golden/ghdl.trace.txt, golden/nvc.trace.txt, oracle_sha256).
//   - tree (S5, #8): GHDL --disp-tree hierarchy recordings
//     (golden/ghdl.tree.txt, oracle_sha256).
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
