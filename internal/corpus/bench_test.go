package corpus

import (
	"context"
	"fmt"
	"runtime/pprof"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/elab"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/simrun"
)

// simRuns returns the main runs of the v1.0 corpus designs with their
// sources.
func simRuns(tb testing.TB) ([]Run, []map[string][]byte) {
	tb.Helper()
	m, read := loadManifest(tb)
	var runs []Run
	var srcs []map[string][]byte
	for _, d := range m.Designs {
		if d.Milestone != "v1.0" {
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

// BenchmarkSim is the simulation throughput benchmark (#35, #14): every
// v1.0 corpus design run the way `gohard check --sim` runs it, from
// analysis to the end of the trace, at seed 0. The nightly job records
// it (scripts/ci/bench.sh) and never fails on it. A design that cannot
// simulate yet (no interpreter, a process without code) is skipped.
func BenchmarkSim(b *testing.B) {
	runs, srcs := simRuns(b)
	for i, r := range runs {
		b.Run(r.Name(), func(b *testing.B) {
			var steps int64
			var changes int
			for b.Loop() {
				res, err := Execute(r, srcs[i],
					Options{Code: interpreter()})
				if err != nil {
					b.Fatal(err)
				}
				if !res.Simulated {
					b.Skipf("not simulated (pending %v)", res.Pending)
				}
				steps, changes = res.Steps, len(res.Trace.Changes)
			}
			b.ReportMetric(float64(steps), "steps/op")
			b.ReportMetric(float64(changes), "changes/op")
		})
	}
}

// BenchmarkSimIEEE runs the corpus designs like BenchmarkSim with
// every interpreted call's CPU time labelled by the unit that runs
// (pprof label "unit": "ieee.numeric_std", ..., "-" outside calls),
// so a CPU profile splits kernel time by IEEE package: the measurement
// of the #35 native-body rule. scripts/ci/bench.sh runs it per design
// with -cpuprofile and records `go tool pprof -tags`.
func BenchmarkSimIEEE(b *testing.B) {
	runs, srcs := simRuns(b)
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
			for b.Loop() {
				res, err := Execute(r, srcs[i],
					Options{Code: interpreter(), Profile: p})
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
