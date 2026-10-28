package corpus

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/connerohnesorge/gohard/v2/internal/elab"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/simrun"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// interpretedBodies runs the benchmarks with every ieee body
// interpreted from source: the "before" of the native-body measure
// (#59, docs/native-bodies.md).
var interpretedBodies = flag.Bool("corpus.interpreted", false,
	"benchmarks: interpret every ieee body (no native bodies)")

// simRuns returns the main runs of the corpus designs of a reached
// milestone (Release) with their sources.
func simRuns(tb testing.TB) ([]Run, []map[string][]byte) {
	tb.Helper()
	m, read := loadManifest(tb)
	var runs []Run
	var srcs []map[string][]byte
	for _, d := range m.Designs {
		if !Reached(d.Milestone) {
			continue
		}
		r := d.Runs()[0]
		src, err := r.Sources(read)
		if err != nil {
			tb.Fatal(err)
		}
		runs = append(runs, r)
		srcs = append(srcs, src)
	}
	return runs, srcs
}

// interpreter is the registered interpreter, else elab.Interpret.
func interpreter() CodeFunc {
	if simrun.DefaultCode != nil {
		return simrun.DefaultCode
	}
	return elab.Interpret
}

// BenchmarkSim is the simulation throughput benchmark (#35, #14, #59):
// every corpus design of a reached milestone run the way `gohard check
// --sim` runs it, from analysis to the end of the trace, at seed 0.
// ns/op is the whole run; simfs/s is the simulated time per wall second
// of the simulation alone (the kernel run, after analysis and
// elaboration). The nightly job records it (scripts/ci/bench.sh) and
// never fails on it. A design that cannot simulate yet (no interpreter,
// a process without code) is skipped.
func BenchmarkSim(b *testing.B) {
	runs, srcs := simRuns(b)
	for i, r := range runs {
		b.Run(r.Name(), func(b *testing.B) {
			benchRun(b, r, srcs[i], nil)
		})
	}
}

// benchRun runs r on src under b's loop and reports steps/op,
// changes/op and simfs/s; it returns the simulated time per wall
// second. p, when set, profiles the runs.
func benchRun(b *testing.B, r Run, src map[string][]byte,
	p *sema.Profile,
) float64 {
	b.Helper()
	var steps int64
	var changes int
	var simulated trace.Time
	var simWall time.Duration
	for b.Loop() {
		var start time.Duration
		res, err := Execute(r, src, Options{Code: interpreter(),
			Profile: p, Simulating: func() { start = b.Elapsed() },
			InterpretedBodies: *interpretedBodies})
		if err != nil {
			b.Fatal(err)
		}
		if !res.Simulated {
			b.Skipf("not simulated (pending %v)", res.Pending)
		}
		// b.Elapsed reads the running timer: the lap since the
		// kernel started is the simulation's own wall time.
		simWall += b.Elapsed() - start
		simulated += res.StopTime
		steps, changes = res.Steps, len(res.Trace.Changes)
	}
	b.ReportMetric(float64(steps), "steps/op")
	b.ReportMetric(float64(changes), "changes/op")
	rate := 0.0
	if simWall > 0 {
		rate = float64(simulated) / simWall.Seconds()
	}
	b.ReportMetric(rate, "simfs/s")
	return rate
}

// neorv32Budget is the simulated time BenchmarkSimNEORV32 runs: 200
// cycles of the testbench's 100 MHz clock, past the reset and into
// the boot of the processor.
const neorv32Budget = "2 us"

// neorv32Period is the testbench's clock period in femtoseconds.
const neorv32Period = 10_000_000

// neorv32Run returns the NEORV32 testbench (the #19 large project's
// neorv32_tb) as a run with its sources: rtl/core and sim in library
// neorv32, VHDL-2008, the time budget budget. ok false when the
// submodule testdata/neorv32 is not checked out.
func neorv32Run(tb testing.TB, budget string) (Run, map[string][]byte,
	bool,
) {
	tb.Helper()
	root := filepath.Join(repoRoot(tb), "testdata", "neorv32")
	r := Run{Design: Design{Name: "neorv32", Testbench: "neorv32_tb"},
		Standard: "2008"}
	src := map[string][]byte{configKey: []byte(
		"libraries:\n  neorv32: [rtl, sim]\nsimulation:\n" +
			"  time_budget: \"" + budget + "\"\n")}
	for _, dir := range []string{"rtl/core", "sim"} {
		es, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
		if err != nil {
			return r, nil, false
		}
		for _, e := range es {
			if e.IsDir() || filepath.Ext(e.Name()) != ".vhd" {
				continue
			}
			p := path.Join(dir, e.Name())
			text, err := os.ReadFile(filepath.Join(root,
				filepath.FromSlash(p)))
			if err != nil {
				tb.Fatal(err)
			}
			r.Paths = append(r.Paths, p)
			src[p] = text
		}
	}
	if len(r.Paths) == 0 {
		return r, nil, false
	}
	return r, src, true
}

// BenchmarkSimNEORV32 is the throughput of a real design (#59): the
// NEORV32 testbench run for neorv32Budget of simulated time, like
// `gohard check --sim --tb neorv32_tb`. Besides BenchmarkSim's metrics
// it reports cycles/s, the testbench clock cycles simulated per wall
// second of the simulation. Skipped when testdata/neorv32 is not
// checked out; recorded nightly, never failing (P8, #14).
func BenchmarkSimNEORV32(b *testing.B) {
	r, src, ok := neorv32Run(b, neorv32Budget)
	if !ok {
		b.Skip("testdata/neorv32 is not checked out " +
			"(git submodule update --init)")
	}
	rate := benchRun(b, r, src, nil)
	b.ReportMetric(rate/neorv32Period, "cycles/s")
}

// BenchmarkSimIEEE runs the corpus designs (and the NEORV32
// testbench, when checked out, as "neorv32") like BenchmarkSim with
// every function call's CPU time labelled by the unit that declares
// the function (pprof label "unit": "ieee.numeric_std", ...; native
// bodies count for their package, #59), and the simulation's time
// outside calls labelled "-", so a CPU profile splits kernel time by
// IEEE package: the measurement of the #35 native-body rule.
// scripts/ci/bench.sh runs it per design with -cpuprofile and records
// `go tool pprof -tags`; -corpus.interpreted measures the interpreted
// bodies instead.
func BenchmarkSimIEEE(b *testing.B) {
	runs, srcs := simRuns(b)
	if r, src, ok := neorv32Run(b, neorv32Budget); ok {
		runs, srcs = append(runs, r), append(srcs, src)
	}
	for i, r := range runs {
		b.Run(r.Name(), func(b *testing.B) {
			ctx := context.Background()
			labels := map[string]context.Context{} // lookup only
			p := sema.NewProfile()
			p.OnSwitch = func(u string) {
				if u == sema.Outside {
					u = "-"
				}
				c, ok := labels[u]
				if !ok {
					c = pprof.WithLabels(ctx, pprof.Labels("unit", u))
					labels[u] = c
				}
				pprof.SetGoroutineLabels(c)
			}
			defer pprof.SetGoroutineLabels(ctx)
			// The kernel's time outside every call is "-" from the
			// start of the simulation, whether or not a call ran yet.
			outside := func() { p.OnSwitch(sema.Outside) }
			for b.Loop() {
				res, err := Execute(r, srcs[i],
					Options{Code: interpreter(), Profile: p,
						Simulating:        outside,
						InterpretedBodies: *interpretedBodies})
				if err != nil {
					b.Fatal(err)
				}
				if !res.Simulated {
					b.Skipf("not simulated (pending %v)", res.Pending)
				}
				pprof.SetGoroutineLabels(ctx)
			}
		})
	}
}

// nativeShare is the #35 native-body trigger: an IEEE package whose
// interpreted bodies take more than this share of a corpus run's
// kernel time may get a native Go body; below it, none.
const nativeShare = 0.20

// TestIEEEShare records, for every v1.0 corpus design that simulates,
// the share of the run's evaluation steps (the deterministic measure
// of kernel time) spent in each ieee package's interpreted bodies, the
// input to the #35 native-body rule: only a package over nativeShare
// on some corpus design may get a native Go body. The shares are
// logged (go test -v -run TestIEEEShare ./internal/corpus, and the
// nightly bench job); the test checks that the profile is a pure
// function of the run (two runs agree). Steps are counted exclusively:
// a numeric_std call's nested std_logic_1164 calls count for
// std_logic_1164; the total includes the kernel's own step charges.
func TestIEEEShare(t *testing.T) {
	runs, srcs := simRuns(t)
	simulated := 0
	for i, r := range runs {
		profile := func() (*sema.Profile, *Result) {
			p := sema.NewProfile()
			res, err := Execute(r, srcs[i],
				Options{Code: interpreter(), Profile: p})
			if err != nil {
				t.Fatal(err)
			}
			return p, res
		}
		p, res := profile()
		if !res.Simulated || p.Total() == 0 {
			continue
		}
		simulated++
		p2, _ := profile()
		if fmt.Sprint(p.Units()) != fmt.Sprint(p2.Units()) {
			t.Errorf("%s: profile differs between runs: %v / %v",
				r.Name(), p.Units(), p2.Units())
		}
		var line []string
		for _, u := range p.Units() {
			if !strings.HasPrefix(u.Unit, "ieee.") {
				continue
			}
			share := float64(u.Steps) / float64(p.Total())
			mark := ""
			if share > nativeShare {
				mark = " (over 20%: native-body candidate)"
			}
			line = append(line, fmt.Sprintf("%s %.1f%%%s",
				strings.TrimPrefix(u.Unit, "ieee."), 100*share, mark))
		}
		if len(line) == 0 {
			line = []string{"no ieee calls"}
		}
		t.Logf("%-16s %10d steps: %s", r.Name(), p.Total(),
			strings.Join(line, ", "))
	}
	if simulated == 0 {
		t.Skip("no corpus design simulates yet (no interpreter)")
	}
}
