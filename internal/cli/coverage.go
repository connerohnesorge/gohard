package cli

import (
	"errors"
	"io/fs"
	"path/filepath"
	"slices"

	"github.com/connerohnesorge/gohard/internal/coverage"
	"github.com/connerohnesorge/gohard/internal/diag"
)

func init() { lookup("coverage").Run = runCoverage }

// runCoverage is `gohard coverage merge FILE... [-o OUT]` (#62): it
// reads lcov files (the per-testbench and per-seed files `gohard check
// --sim --coverage` writes, or merged ones) in sorted name order, sums
// them and writes the result to standard output, or with -o/--out to
// OUT through a temp file and a rename, and logs the summary line.
// Summing does not depend on the order, so any order of the same files
// gives the same bytes. A missing file is cli/missing-path; an
// unreadable or invalid one coverage/invalid, both exit 2.
func runCoverage(inv *Invocation) Result {
	if len(inv.Args) < 2 {
		return inv.usage("gohard coverage merge needs at least one FILE")
	}
	files := slices.Clone(inv.Args[1:])
	slices.Sort(files)
	var rs []coverage.Report
	for _, arg := range files {
		shown := filepath.ToSlash(arg)
		data, err := inv.Env.FS.ReadFile(arg)
		if errors.Is(err, fs.ErrNotExist) {
			return inv.Fail(diag.New(diag.CLIMissingPath, shown))
		}
		if err != nil {
			return inv.Fail(diag.New(diag.CoverageInvalid, shown,
				errReason(err)))
		}
		r, err := coverage.Parse(data)
		if err != nil {
			return inv.Fail(diag.New(diag.CoverageInvalid, shown,
				err.Error()))
		}
		rs = append(rs, r)
	}
	m := coverage.Merge(rs...)
	out := coverage.Encode(m)
	if !inv.IsSet("out") {
		inv.Logf("coverage: %s", m.Totals())
		return Result{Stdout: out}
	}
	target := inv.String("out")
	if err := writeOutput(inv.Env.FS, target, out); err != nil {
		return inv.Fail(diag.New(diag.CLIOutputFailed,
			filepath.ToSlash(target), errReason(err)))
	}
	inv.Logf("coverage: %s: %s", filepath.ToSlash(target), m.Totals())
	return Result{}
}
