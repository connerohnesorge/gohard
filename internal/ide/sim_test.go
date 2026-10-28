package ide

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/server"
	"github.com/connerohnesorge/gohard/v2/internal/simrun"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// TestSlicedRunEqualsRun: on every corpus testbench the editor's
// sliced run gives the outcome of simrun.Run (the gohard check --sim
// code path), and its slices stay bounded: one per simrun.Slice
// cycles.
func TestSlicedRunEqualsRun(t *testing.T) {
	if simrun.DefaultCode == nil {
		t.Skip("needs the sequential interpreter")
	}
	ran := 0
	for _, name := range slices.Sorted(maps.Keys(corpus(t))) {
		p := corpus(t)[name]
		snap := snapshotOf(t, p, nil)
		a := New(Options{ToolVersion: "test"})
		an, _ := run(a.Analysis(snap))
		bs, _ := run(a.Testbenches(snap))
		for _, b := range bs {
			if hasErrors(an) {
				continue
			}
			res, n := run(a.Simulate(snap, b.Name, 0))
			cfg := Config(snap)
			out := simrun.Run(simrun.Options{Result: an.Result,
				Sim: cfg.Simulation, Testbench: b.Name})
			var want []byte
			if out.Trace != nil {
				want = trace.Encode(out.Trace)
			}
			if !res.Ran || !bytes.Equal(res.Trace, want) ||
				res.Report.Status != out.Status.String() ||
				len(res.Run) != len(out.Diagnostics) {
				t.Errorf("%s/%s: sliced run differs from simrun.Run",
					name, b.Name)
			}
			if n < 2 {
				t.Errorf("%s/%s: %d slices", name, b.Name, n)
			}
			ran++
		}
	}
	if ran == 0 {
		t.Fatal("no corpus testbench ran")
	}
	t.Logf("%d corpus testbenches", ran)
}

func hasErrors(an *Analysis) bool {
	for _, fd := range an.Diagnostics {
		for _, d := range fd.Diagnostics {
			if d.Severity.String() == "error" {
				return true
			}
		}
	}
	return false
}

// TestSimTaskResumesAnyContinuation: the kernel behind a run is
// mutable, yet every continuation of the task stays a value: resuming
// an older continuation again (a forked server State) gives the same
// result as the first time.
func TestSimTaskResumesAnyContinuation(t *testing.T) {
	if simrun.DefaultCode == nil {
		t.Skip("needs the sequential interpreter")
	}
	p := project{"tb.vhd": []byte(`entity tb is end;
architecture a of tb is
  signal c : integer := 0;
begin
  process
  begin
    for i in 1 to 2000 loop
      wait for 1 ns;
      c <= c + 1;
    end loop;
    report "done";
    wait;
  end process;
end;
`)}
	snap := snapshotOf(t, p, nil)
	a := New(Options{ToolVersion: "test"})
	task := a.Simulate(snap, "tb", 3)
	var conts []server.Task[server.SimResult]
	var first server.SimResult
	for {
		sl := task.Run(server.SliceBudget)
		if sl.Done || sl.Next == nil {
			first = sl.Value
			break
		}
		conts = append(conts, sl.Next)
		task = sl.Next
	}
	if len(conts) < 5 {
		t.Fatalf("only %d slices", len(conts))
	}
	want, _ := json.Marshal(first)
	for _, i := range []int{len(conts) - 1, 0, len(conts) / 2, 1} {
		got, _ := run(conts[i])
		b, _ := json.Marshal(got)
		if !bytes.Equal(b, want) {
			t.Fatalf("continuation %d gives another result", i)
		}
	}
}

// TestTestbenchDeps: a testbench's Deps changes with the text of every
// file it reaches and with nothing else (auto_run_on_save runs exactly
// the candidates whose dependencies changed).
func TestTestbenchDeps(t *testing.T) {
	p := project{
		"pkg.vhd": []byte("package p is constant k : integer := 1; " +
			"end;\n"),
		"dut.vhd": []byte("use work.p.all;\nentity dut is port " +
			"(x : in bit); end;\narchitecture a of dut is begin end;\n"),
		"tb.vhd": []byte("entity tb is end;\narchitecture a of tb is\n" +
			"  signal x : bit;\nbegin\n  u : entity work.dut port " +
			"map (x);\nend;\n"),
		"other_tb.vhd": []byte("entity other_tb is end;\n" +
			"architecture a of other_tb is begin end;\n"),
	}
	deps := func(p project) map[string]string {
		bs, _ := run(New(Options{ToolVersion: "test"}).Testbenches(
			snapshotOf(t, p, nil)))
		out := map[string]string{}
		for _, b := range bs {
			out[b.Name] = b.Deps
			if b.URI == "" || b.Span.End <= b.Span.Start {
				t.Fatalf("%s has no location", b.Name)
			}
		}
		return out
	}
	base := deps(p)
	if len(base) != 2 {
		t.Fatalf("testbenches %v", base)
	}
	for _, c := range []struct {
		file    string
		changed []string
	}{
		{"pkg.vhd", []string{"tb"}},
		{"dut.vhd", []string{"tb"}},
		{"tb.vhd", []string{"tb"}},
		{"other_tb.vhd", []string{"other_tb"}},
	} {
		q := maps.Clone(p)
		q[c.file] = append(slices.Clone(p[c.file]), "-- edit\n"...)
		got := deps(q)
		for _, tb := range slices.Sorted(maps.Keys(base)) {
			if (got[tb] != base[tb]) != slices.Contains(c.changed, tb) {
				t.Errorf("editing %s: %s changed = %v", c.file, tb,
					got[tb] != base[tb])
			}
		}
	}
}
