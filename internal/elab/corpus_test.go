package elab

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/trace"
)

func corpus(t *testing.T, name, std string) *sema.Result {
	t.Helper()
	dir := filepath.Join("../../testdata/corpus", name)
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var srcs []sema.Source
	for _, e := range ents {
		n := e.Name()
		if !strings.HasSuffix(n, ".vhd") || strings.HasSuffix(n,
			"_err.vhd") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		srcs = append(srcs, sema.Source{Path: n, Text: b})
	}
	cfg := config.Defaults()
	cfg.Standard = std
	return sema.Analyze(sema.Input{Files: srcs, Config: cfg})
}

type inert struct{}

func (inert) Clone() sim.Code { return inert{} }
func (inert) Resume(*sim.Ctx) (sim.Wait, error) {
	return sim.Wait{}, nil
}

// TestCorpusElaborate elaborates the v1.0 corpus testbenches: unique
// signal and process paths, no elaboration diagnostics, and a kernel
// that initializes them and writes a valid trace.
func TestCorpusElaborate(t *testing.T) {
	for _, c := range []struct{ name, std string }{
		{"md_resolve", "2008"}, {"wait_forms", "2008"},
		{"postponed_attrs", "2008"}, {"hier_generate", "2008"},
		{"assert_fail", "2008"}, {"budget_hang", "2008"},
		{"overload_ops", "2008"}, {"textio_env", "2008"},
		{"force_ext", "2008"}, {"mode_views", "2019"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := corpus(t, c.name, c.std)
			d, diags := Elaborate(r, Options{Top: c.name + "_tb",
				StepBudget: 50_000_000,
				Code: func(*Design, *Proc) (sim.Code, error) {
					return inert{}, nil
				}})
			for _, dg := range diags {
				t.Errorf("%s:%d: %s %s", dg.Path, dg.Line, dg.Code,
					dg.Message)
			}
			if d == nil {
				return
			}
			var paths []string
			for _, s := range d.Model.Signals {
				paths = append(paths, s.Path)
			}
			for _, p := range d.Model.Processes {
				paths = append(paths, "process "+p.Path)
			}
			sorted := slices.Clone(paths)
			slices.Sort(sorted)
			if len(slices.Compact(sorted)) != len(paths) {
				t.Errorf("duplicate paths: %q", paths)
			}
			if len(d.Model.Processes) == 0 {
				t.Errorf("no processes")
			}
			rec := trace.NewRecorder(nil)
			k, err := sim.New(d.Model, sim.Options{Writer: rec,
				Budget: d.Budget})
			if err != nil {
				t.Fatal(err)
			}
			k.RunAll(1000, nil)
			for _, dg := range k.Diagnostics() {
				if dg.Severity == diag.Error {
					t.Errorf("kernel: %s %s", dg.Code, dg.Message)
				}
			}
			if _, err := rec.Trace(); err != nil {
				t.Errorf("trace: %v", err)
			}
		})
	}
}

// TestCorpusMdResolve checks the md_resolve ports collapse onto the
// testbench nets, so the DUT adds no delta (the design's own note).
func TestCorpusMdResolve(t *testing.T) {
	r := corpus(t, "md_resolve", "2008")
	d, _ := Elaborate(r, Options{Top: "md_resolve_tb"})
	for _, p := range []string{"bus_io", "vec_io", "d0", "d3"} {
		id, ok := d.Lookup("md_resolve_tb.dut." + p)
		if !ok {
			t.Fatalf("no port %s", p)
		}
		s := d.Model.Signals[id]
		if !s.Collapsed || d.Model.Signals[s.Of].Path !=
			"md_resolve_tb."+p {
			t.Errorf("port %s is not collapsed onto its actual", p)
		}
	}
	id, _ := d.Lookup("md_resolve_tb.bus_io")
	if d.Model.Signals[id].Resolve == nil {
		t.Errorf("bus_io is not resolved")
	}
}
