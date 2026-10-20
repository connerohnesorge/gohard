package cli

import (
	"bytes"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/repro"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/simrun"
	"github.com/connerohnesorge/gohard/internal/trace"
	"github.com/connerohnesorge/gohard/internal/workspace"
)

// reproRun is the --repro FILE of a check run (#29): the recorded
// header, and the failure that ends the run before analysis.
type reproRun struct {
	file   string // as shown, slash form
	header *repro.Header
	// mismatch is set when another gohard version recorded the run:
	// the new trace is diffed against the recorded one.
	mismatch bool
	fail     *Result
}

// loadRepro reads --repro FILE; an unreadable or invalid file is
// repro/invalid (exit 2).
func loadRepro(inv *Invocation) reproRun {
	if !inv.IsSet("repro") {
		return reproRun{}
	}
	arg := inv.String("repro")
	rp := reproRun{file: filepath.ToSlash(arg)}
	h, err := repro.Read(inv.Env.FS, arg)
	if err != nil {
		res := inv.Fail(repro.Invalid(rp.file,
			errors.New(errReason(err))))
		rp.fail = &res
		return rp
	}
	rp.header = &h
	return rp
}

// apply checks the recorded hashes against the project at l.Root and
// applies the recorded defines. stop means a source changed
// (repro/source-changed, exit 2, listing the files).
func (rp *reproRun) apply(inv *Invocation, l *config.Loaded) (
	[]diag.Diagnostic, bool,
) {
	if rp.header == nil {
		return nil, false
	}
	h := *rp.header
	c, err := repro.Verify(inv.Env.FS, l.Root, h, Version)
	if err != nil {
		return []diag.Diagnostic{repro.Invalid(rp.file,
			errors.New(errReason(err)))}, true
	}
	ds := c.Diagnostics(rp.file)
	if !c.OK() {
		return ds, true
	}
	rp.mismatch = c.VersionMismatch()
	var defs []string
	for _, k := range slices.Sorted(maps.Keys(h.Defines)) {
		defs = append(defs, k+"="+h.Defines[k])
	}
	cfg, _, dds := config.ApplyDefines(l.Config, defs)
	l.Config = cfg
	return append(ds, dds...), false
}

type simInput struct {
	root  string
	cfg   config.Config
	res   *sema.Result
	files []workspace.Source
	repro reproRun
}

// seedFor returns the run's seed: the repro's, else --seed (random
// picks one through env.Rand and logs it), else simulation.seed.
func seedFor(inv *Invocation, in simInput) (uint64, bool) {
	if h := in.repro.header; h != nil {
		return h.Seed, true
	}
	if !inv.IsSet("seed") {
		return in.cfg.Simulation.Seed, true
	}
	seed, random, err := env.ParseSeed(inv.String("seed"),
		inv.Env.Rand)
	if err != nil {
		return 0, false
	}
	if random {
		inv.Logf("%s", simrun.SeedText(seed))
	}
	return seed, true
}

// testbenches returns the testbenches to run: the repro's, --tb, or
// every candidate (#35). An unknown --tb is sim/unknown-testbench.
func testbenches(inv *Invocation, in simInput) ([]string,
	[]diag.Diagnostic,
) {
	cands := simrun.Candidates(in.res, in.cfg.Simulation)
	want := ""
	switch {
	case in.repro.header != nil:
		want = in.repro.header.Testbench
	case inv.IsSet("tb"):
		want = inv.String("tb")
	case len(cands) > 0:
		return cands, nil
	default:
		// --sim with nothing to run must not pass silently.
		want = "(any)"
	}
	if tb, ok := findCandidate(cands, want); ok {
		return []string{tb}, nil
	}
	list := strings.Join(cands, ", ")
	if list == "" {
		list = "none"
	}
	return nil, []diag.Diagnostic{diag.New(diag.SimUnknownTestbench,
		want, list)}
}

// simulate runs the testbenches of a check --sim (or lsp sim) and
// writes every run's trace and repro file under .gohard/traces through
// env.FS. Design errors from analysis skip simulation: the run already
// fails.
func simulate(inv *Invocation, r *diag.Report, in simInput) {
	if in.res == nil || slices.ContainsFunc(r.Diagnostics,
		func(d diag.Diagnostic) bool { return d.Severity == diag.Error }) {
		return
	}
	seed, ok := seedFor(inv, in)
	if !ok {
		return // --seed was already reported as cli/usage
	}
	tbs, ds := testbenches(inv, in)
	r.Diagnostics = append(r.Diagnostics, ds...)
	for _, tb := range tbs {
		opts := simrun.Options{Result: in.res,
			Sim: in.cfg.Simulation, Testbench: tb, Seed: seed,
			Cancelled: func() bool { return inv.Ctx.Err() != nil }}
		out := simrun.Run(opts)
		r.Diagnostics = append(r.Diagnostics, out.Diagnostics...)
		if !hasTool(out.Diagnostics) {
			r.Diagnostics = append(r.Diagnostics,
				races(inv, opts, out)...)
			r.Diagnostics = append(r.Diagnostics,
				writeRun(inv, in, out)...)
			inv.Logf("simulated %s (%s): %s", out.Testbench,
				simrun.SeedText(out.Seed), out.Status)
		}
		if inv.simRuns != nil {
			*inv.simRuns = append(*inv.simRuns, out)
		}
	}
}

// races compares a run at a non-zero seed with the declaration-order
// run at seed 0 (#29, #35): a trace that changes with the seed is one
// sim/race warning naming the two processes. Seed 0 is the reference,
// so a default run costs nothing extra.
func races(inv *Invocation, o simrun.Options,
	out simrun.Outcome,
) []diag.Diagnostic {
	if out.Seed == 0 || out.Trace == nil || inv.Ctx.Err() != nil {
		return nil
	}
	return simrun.Races(o, []uint64{0, out.Seed})
}

// writeRun writes the trace (diffed against the recorded one under a
// version-mismatched --repro) and the repro file of one run.
func writeRun(inv *Invocation, in simInput,
	out simrun.Outcome,
) []diag.Diagnostic {
	fsys := inv.Env.FS
	failed := func(name string, err error) []diag.Diagnostic {
		return []diag.Diagnostic{diag.New(diag.CLIOutputFailed, name,
			errReason(err))}
	}
	if out.Trace != nil {
		name := trace.FileName(out.Testbench, out.Seed)
		abs := filepath.Join(in.root, filepath.FromSlash(name))
		var old []byte
		if in.repro.mismatch {
			old, _ = fsys.ReadFile(abs)
		}
		data := trace.Encode(out.Trace)
		if in.repro.mismatch {
			diffTraces(inv, name, old, data)
		}
		if err := trace.WriteFile(fsys, abs, data); err != nil {
			return failed(name, err)
		}
	}
	h, err := header(inv, in, out)
	if err != nil {
		return failed(repro.FileName(out.Testbench, out.Seed), err)
	}
	if _, err := repro.Write(fsys, in.root, h); err != nil {
		return failed(repro.FileName(out.Testbench, out.Seed), err)
	}
	return nil
}

// header is the repro header of one run (#29).
func header(inv *Invocation, in simInput, out simrun.Outcome) (
	repro.Header, error,
) {
	s := in.cfg.Simulation
	b := inv.build()
	h := repro.Header{Version: b.Version, Commit: b.Commit, IEEE: b.IEEE,
		Go: b.Go, Standard: in.cfg.Standard,
		Testbench: out.Testbench, Seed: out.Seed,
		Defines: map[string]string{}, Models: map[string]string{}}
	h.Budgets.Time, h.Budgets.Delta, h.Budgets.Step = simrun.Budgets(s)
	for _, d := range in.cfg.Defines {
		h.Defines[d.Name] = d.Value
	}
	for _, m := range s.Models {
		h.Models[m.Unit] = m.Path
	}
	var paths []string
	for _, f := range in.files {
		p := f.Path
		if p == ".." || strings.HasPrefix(p, "../") ||
			filepath.IsAbs(filepath.FromSlash(p)) {
			continue // a vendor library outside the root
		}
		paths = append(paths, p)
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	var err error
	if h.Sources, err = repro.HashFiles(inv.Env.FS, in.root,
		paths); err != nil {
		return h, err
	}
	h.Config, err = repro.HashConfig(inv.Env.FS, in.root)
	return h, err
}

// diffTraces logs how the new trace differs from the recorded one: the
// first differing line of their canonical text, or that they match.
func diffTraces(inv *Invocation, name string, old, cur []byte) {
	if old == nil {
		inv.Logf("%s: no recorded trace to compare", name)
		return
	}
	if bytes.Equal(old, cur) {
		inv.Logf("%s: trace matches the recorded run", name)
		return
	}
	a := strings.Split(string(old), "\n")
	b := strings.Split(string(cur), "\n")
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	at := func(s []string) string {
		if i < len(s) {
			return strconv.Quote(s[i])
		}
		return "end of trace"
	}
	inv.Logf("%s: trace differs from the recorded run at line %d", name,
		i+1)
	inv.Logf("-%s", at(a))
	inv.Logf("+%s", at(b))
}

// findCandidate matches name case-insensitively; a library prefix
// (work.tb) matches the bare entity name.
func findCandidate(cands []string, name string) (string, bool) {
	n := strings.ToLower(name)
	if slices.Contains(cands, n) {
		return n, true
	}
	if _, after, ok := strings.Cut(n, "."); ok &&
		slices.Contains(cands, after) {
		return after, true
	}
	return "", false
}
