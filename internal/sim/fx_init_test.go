package sim

// fx_init_test.go: the reports elaboration ran (Model.Init, #18) are
// reported by the kernel when it initializes, before any process, in
// order, and counted like a process's.

import (
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// TestFxInitReports: over generated Init lists, the trace has the
// Init events first, in order, then the process's; every enabled
// assertion among them is counted when the first process runs.
func TestFxInitReports(t *testing.T) {
	for seed := range uint64(30) {
		r := &env.SplitMix64{State: seed}
		n := env.Intn(r, 6)
		var init []Report
		var want [nSeverity]uint64
		for i := range n {
			rep := Report{Assertion: env.Intn(r, 2) == 0,
				Severity: trace.Severity(1 + env.Intn(r, 3)),
				Message:  "init " + string(rune('a'+i))}
			if rep.Assertion {
				want[rep.Severity]++
			}
			init = append(init, rep)
		}
		m := &Model{Testbench: "tb", Init: init}
		var seen [nSeverity]uint64
		m.Processes = []Process{{Path: "tb.p",
			Code: &fn{f: func(c *Ctx, k int) (Wait, error) {
				if k > 0 {
					return forever()
				}
				for s := trace.Note; s <= trace.Failure; s++ {
					seen[s] = c.AssertCount(s)
				}
				err := c.Report(Report{Message: "process"})
				if err != nil {
					return Wait{}, err
				}
				return forever()
			}}}}
		k, rec := run(t, m, Options{StopLevel: trace.Failure})
		if seen != want {
			t.Errorf("seed %d: counts at the first process %v, want %v",
				seed, seen, want)
		}
		if len(rec.events) != n+1 {
			t.Fatalf("seed %d: %d events, want %d", seed, len(rec.events),
				n+1)
		}
		for i, e := range rec.events[:n] {
			if e.Message != init[i].Message || e.Process != 0 {
				t.Errorf("seed %d: event %d %q (process %d), want %q "+
					"outside processes", seed, i, e.Message, e.Process,
					init[i].Message)
			}
		}
		if rec.events[n].Message != "process" {
			t.Errorf("seed %d: last event %q", seed, rec.events[n].Message)
		}
		if k.Asserts().Total() != want[1]+want[2]+want[3]+want[4] {
			t.Errorf("seed %d: asserts %+v, want %v", seed, k.Asserts(),
				want)
		}
	}
}
