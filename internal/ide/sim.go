package ide

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/server"
	"github.com/connerohnesorge/gohard/internal/simrun"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// The Analyzer is the server.Simulator of `gohard lsp` too (#25, #35):
// testbench candidates come from the snapshot's analysis, and a run is
// simrun's code path (Start, then bounded Step slices), the one gohard
// check --sim and gohard lsp sim use.
var _ server.Simulator = (*Analyzer)(nil)

// Testbenches implements server.Simulator: simrun.Candidates of the
// snapshot's analysis, located at their entity names, sorted by name.
func (a *Analyzer) Testbenches(snap *server.Snapshot,
) server.Task[[]server.Testbench] {
	return Query(a, snap, (*Analysis).Testbenches)
}

// Testbenches returns the testbench candidates of the analysis.
func (an *Analysis) Testbenches() []server.Testbench {
	if an.Result == nil {
		return nil
	}
	cfg := Config(an.Snap)
	simKeys, err := json.Marshal(cfg.Simulation)
	if err != nil {
		panic(err) // plain data always encodes
	}
	var out []server.Testbench
	for _, name := range simrun.Candidates(an.Result, cfg.Simulation) {
		b := server.Testbench{Name: name}
		u := an.entity(name)
		if u != nil {
			if uri, ok := an.URI(u.File.Path); ok {
				b.URI, b.Span = uri, Span(u.Decl.Span)
			}
		}
		b.Deps = hash(append(an.depFiles(u), simKeys)...)
		out = append(out, b)
	}
	slices.SortFunc(out, func(x, y server.Testbench) int {
		return strings.Compare(x.Name, y.Name)
	})
	return out
}

// entity is the project entity called name (lower case).
func (an *Analysis) entity(name string) *sema.Unit {
	for _, u := range an.Result.Units {
		if u.Kind == sema.UnitEntity && u.Decl != nil && !u.Duplicate &&
			u.File != nil && !u.File.Bundled &&
			strings.EqualFold(string(u.Name), name) {
			return u
		}
	}
	return nil
}

// depFiles are the path and text of every project file holding a unit
// that u reaches: its dependencies and, for each primary unit, its
// secondary units (architectures, bodies, configurations), sorted by
// path.
func (an *Analysis) depFiles(u *sema.Unit) [][]byte {
	if u == nil {
		return nil
	}
	type key struct{ lib, of sema.Key }
	secondary := map[key][]*sema.Unit{}
	for _, x := range an.Result.Units {
		if x.Of != "" {
			k := key{x.Library, x.Of}
			secondary[k] = append(secondary[k], x)
		}
	}
	seen := map[*sema.Unit]bool{}
	files := map[string][]byte{}
	stack := []*sema.Unit{u}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[x] {
			continue
		}
		seen[x] = true
		if x.File != nil && !x.File.Bundled {
			files[x.File.Path] = x.File.Text
		}
		stack = append(stack, x.Deps...)
		stack = append(stack, secondary[key{x.Library, x.Name}]...)
	}
	var out [][]byte
	for _, p := range slices.Sorted(maps.Keys(files)) {
		out = append(out, []byte(p), files[p])
	}
	return out
}

// Simulate implements server.Simulator: analyze the snapshot, then run
// tb at seed as gohard check --sim --tb tb does. A design with errors
// is not simulated; the report then holds the errors, as gohard lsp
// sim prints it.
func (a *Analyzer) Simulate(snap *server.Snapshot, tb string,
	seed uint64,
) server.Task[server.SimResult] {
	return server.Chain(a.Analysis(snap),
		func(an *Analysis) server.Task[server.SimResult] {
			return an.simulate(tb, seed)
		})
}

func (an *Analysis) simulate(tb string,
	seed uint64,
) server.Task[server.SimResult] {
	var all []diag.Diagnostic
	for _, fd := range an.Diagnostics {
		all = append(all, fd.Diagnostics...)
	}
	notRun := func(ds []diag.Diagnostic) server.Task[server.SimResult] {
		return server.Done(server.SimResult{
			Report: server.NewSimReport("", nil, "", "", ds)})
	}
	if an.Result == nil || slices.ContainsFunc(all,
		func(d diag.Diagnostic) bool { return d.Severity == diag.Error }) {
		return notRun(all)
	}
	cfg := Config(an.Snap)
	cands := simrun.Candidates(an.Result, cfg.Simulation)
	name, ok := candidate(cands, tb)
	if !ok {
		list := strings.Join(cands, ", ")
		if list == "" {
			list = "none"
		}
		return notRun(append(all, diag.New(diag.SimUnknownTestbench,
			tb, list)))
	}
	opts := simrun.Options{Result: an.Result, Sim: cfg.Simulation,
		Testbench: name, Seed: seed}
	return simTask{opts: opts, base: all, sh: &simShared{}}
}

// candidate matches name case-insensitively; a library prefix (work.tb)
// matches the bare entity name, as for gohard check --tb.
func candidate(cands []string, name string) (string, bool) {
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

// simTask is a run in slices: slice 1 elaborates (simrun.Start), each
// later slice runs simrun.Slice kernel cycles. The kernel is mutable,
// so the task keeps it in shared state tagged with the number of
// slices it ran; a continuation that finds the state at another
// position (a forked server State resuming an older continuation)
// rebuilds the run deterministically up to its own position, so every
// continuation computes the same values.
type simTask struct {
	opts simrun.Options
	base []diag.Diagnostic
	sh   *simShared
	pos  int // slices run before this one
}

type simShared struct {
	r   *simrun.Running
	pos int
}

func (t simTask) Run(int) server.Slice[server.SimResult] {
	sh := t.sh
	if t.pos == 0 || sh.r == nil || sh.pos != t.pos {
		sh = &simShared{r: simrun.Start(t.opts), pos: 1}
		for sh.pos < t.pos {
			sh.r.Step(simrun.Slice)
			sh.pos++
		}
	}
	if t.pos > 0 {
		sh.r.Step(simrun.Slice)
		sh.pos++
	}
	if sh.r.Done() {
		return server.Slice[server.SimResult]{Done: true,
			Value: t.result(sh.r.Outcome())}
	}
	msg := t.opts.Testbench + ": elaborated"
	if t.pos > 0 {
		msg = t.opts.Testbench + ": " +
			config.Time(sh.r.Now()).String()
	}
	return server.Slice[server.SimResult]{
		Next:   simTask{opts: t.opts, base: t.base, sh: sh, pos: sh.pos},
		Report: &server.Report{Message: msg, Percentage: -1}}
}

// result is the finished run: the race check of a non-zero seed
// against seed 0 (#29), the report and what to write, as gohard check
// --sim does.
func (t simTask) result(out simrun.Outcome) server.SimResult {
	ds := slices.Clone(out.Diagnostics)
	tool := slices.ContainsFunc(ds, func(d diag.Diagnostic) bool {
		return d.Class() == diag.Tool
	})
	if !tool && out.Seed != 0 && out.Trace != nil {
		ds = append(ds, simrun.Races(t.opts, []uint64{0, out.Seed})...)
	}
	seed := out.Seed
	end := ""
	r := server.SimResult{Ran: true, Run: ds, Write: !tool}
	if out.Trace != nil {
		end = config.Time(out.Trace.Header.End).String()
		r.Trace = trace.Encode(out.Trace)
	}
	r.Report = server.NewSimReport(out.Testbench, &seed,
		out.Status.String(), end, append(slices.Clone(t.base), ds...))
	return r
}
