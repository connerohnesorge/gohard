package cli

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

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

// applyEnv sets the std.env environment of the run (#18) on in.cfg:
// --env NAME=VALUE over simulation.env.vars (each override logged),
// then, under --repro, the recorded epoch and variables, so a replay
// sees exactly the recorded world. A malformed --env is cli/usage.
func applyEnv(inv *Invocation, in *simInput) []diag.Diagnostic {
	var args []string
	if inv.IsSet("env") {
		args = inv.Strings("env")
	}
	cfg, logs, ds := config.ApplyEnvVars(in.cfg, args)
	h := in.repro.header
	if h != nil && len(args) > 0 && len(ds) == 0 {
		// The recorded environment wins, like the recorded defines:
		// say so instead of logging overrides that never apply.
		logs = []string{"--env is ignored: --repro replays the " +
			"recorded environment"}
	}
	for _, l := range logs {
		inv.Logf("%s", l)
	}
	if h != nil {
		cfg.Simulation.Env = h.Env.Apply(cfg.Simulation.Env)
	}
	in.cfg = cfg
	return ds
}

// realEnv returns the run environment's doors under --real-env: the
// real environment, and the wall clock frozen at the start of the
// run, so the race re-run at seed 0 sees the same time and reports no
// false race. Under --repro the recorded environment wins (a recording
// made with --real-env is refused before), so --real-env is ignored
// and a reproducible recording is never rewritten as non-reproducible.
func realEnv(inv *Invocation, in simInput) simrun.Env {
	if !inv.IsSet("real-env") || !inv.Bool("real-env") {
		return simrun.Env{}
	}
	if h := in.repro.header; h != nil {
		inv.Logf("%s", "--real-env is ignored: --repro replays the "+
			"recorded environment")
		return simrun.Env{}
	}
	return simrun.Env{Real: true, Getenv: inv.Env.Getenv,
		Clock: frozenClock{inv.Env.Clock.Now()}}
}

// frozenClock is a Clock stopped at t: the --real-env clock of one
// run.
type frozenClock struct{ t time.Time }

func (c frozenClock) Now() time.Time { return c.t }

func (frozenClock) Sleep(ctx context.Context, _ time.Duration) error {
	return ctx.Err()
}

func (c frozenClock) NewTimer(time.Duration) env.Timer {
	return stoppedTimer{}
}

// stoppedTimer never fires.
type stoppedTimer struct{}

func (stoppedTimer) C() <-chan time.Time { return nil }
func (stoppedTimer) Stop() bool          { return false }

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
	r.Diagnostics = append(r.Diagnostics, applyEnv(inv, &in)...)
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
	world := realEnv(inv, in)
	world.ToolVersion, world.Root, world.FS = Version, in.root,
		inv.Env.FS
	for _, tb := range tbs {
		h, herr := header(inv, in, tb, seed, world.Real)
		w := world
		w.RunDir = repro.RunDir(h)
		opts := simrun.Options{Result: in.res,
			Sim: in.cfg.Simulation, Testbench: tb, Seed: seed, Env: w,
			Cancelled: func() bool { return inv.Ctx.Err() != nil }}
		out := simrun.Run(opts)
		h.Inputs = repro.InputFiles(out.Inputs)
		h.Listings = repro.InputFiles(out.Listings)
		r.Diagnostics = append(r.Diagnostics, out.Diagnostics...)
		if !hasTool(out.Diagnostics) {
			r.Diagnostics = append(r.Diagnostics,
				races(inv, opts, out)...)
			r.Diagnostics = append(r.Diagnostics,
				writeRun(inv, in, out, w.RunDir, h, herr)...)
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
// version-mismatched --repro), the files the design wrote (below the
// run directory runDir, #18) and the repro file h of one run; herr is
// the error of building h.
func writeRun(inv *Invocation, in simInput, out simrun.Outcome,
	runDir string, h repro.Header, herr error,
) []diag.Diagnostic {
	fsys := inv.Env.FS
	failed := func(name string, err error) []diag.Diagnostic {
		return []diag.Diagnostic{diag.New(diag.CLIOutputFailed, name,
			errReason(err))}
	}
	if ds := flushRun(inv, in.root, runDir, out); ds != nil {
		return ds
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
	if herr != nil {
		return failed(repro.FileName(out.Testbench, out.Seed), herr)
	}
	if _, err := repro.Write(fsys, in.root, h); err != nil {
		return failed(repro.FileName(out.Testbench, out.Seed), err)
	}
	return nil
}

// flushRun replaces root/runDir with the directories and files the
// design of out created (#18), each file through a temporary file and
// a rename. What an earlier run left there is removed first, so the
// directory holds exactly this run's output. It is called once per
// run: the race re-run and the editor's runs never flush.
func flushRun(inv *Invocation, root, runDir string,
	out simrun.Outcome,
) []diag.Diagnostic {
	fsys := inv.Env.FS
	base := filepath.Join(root, filepath.FromSlash(runDir))
	if err := removeTree(fsys, base); err != nil {
		return []diag.Diagnostic{diag.New(diag.CLIOutputFailed,
			runDir, errReason(err))}
	}
	for _, d := range out.Dirs {
		name := filepath.Join(base, filepath.FromSlash(d))
		if err := fsys.MkdirAll(name, 0o755); err != nil {
			return []diag.Diagnostic{diag.New(diag.CLIOutputFailed,
				runDir+"/"+d, errReason(err))}
		}
	}
	for _, f := range out.Files {
		name := filepath.Join(base, filepath.FromSlash(f.Path))
		if err := trace.WriteFile(fsys, name, f.Data); err != nil {
			return []diag.Diagnostic{diag.New(diag.CLIOutputFailed,
				runDir+"/"+f.Path, errReason(err))}
		}
	}
	return nil
}

// removeTree removes name and everything below it, if it exists. A
// symbolic link is removed, never followed.
func removeTree(fsys env.FS, name string) error {
	fi, err := fsys.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.IsDir() {
		es, err := fsys.ReadDir(name)
		if err != nil {
			return err
		}
		for _, e := range es {
			if err := removeTree(fsys, filepath.Join(name,
				e.Name())); err != nil {
				return err
			}
		}
	}
	return fsys.Remove(name)
}

// header is the repro header of the run of tb at seed (#29), with the
// std.env environment in effect (#18).
func header(inv *Invocation, in simInput, tb string, seed uint64,
	real bool,
) (repro.Header, error) {
	s := in.cfg.Simulation
	b := inv.build()
	h := repro.Header{Version: b.Version, Commit: b.Commit, IEEE: b.IEEE,
		Go: b.Go, Standard: in.cfg.Standard,
		Testbench: strings.ToLower(tb), Seed: seed,
		Defines: map[string]string{}, Models: map[string]string{},
		Env: repro.NewEnv(s.Env, real)}
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
			env.IsRooted(filepath.FromSlash(p)) {
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
