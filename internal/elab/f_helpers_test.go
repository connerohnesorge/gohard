package elab

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/sim"
	"github.com/connerohnesorge/gohard/v2/internal/token"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// analyze19 analyzes src under VHDL-2019 with the v1.1 constructs of
// #18 let through: until the integrator removes their
// unsupported/construct detectors (sema/unsupported.go), the units
// they make opaque or unchecked are opened again here, in test code
// only, and those two diagnostics are dropped. Any other error fails
// the test.
func analyze19(t *testing.T, src string) *sema.Result {
	t.Helper()
	cfg := config.Defaults()
	cfg.Standard = "2019"
	r := sema.Analyze(sema.Input{Config: cfg, Files: []sema.Source{{
		Path: "t.vhd", Text: []byte(src)}}})
	for _, u := range r.Units {
		u.Opaque, u.Unchecked = false, false
	}
	for _, d := range r.Diagnostics {
		if d.Code == diag.UnsupportedConstruct ||
			d.Code == diag.SemaUnchecked {
			continue
		}
		if d.Severity == diag.Error {
			t.Fatalf("analysis: %s:%d:%d: %s: %s", d.Path, d.Line, d.Col,
				d.Code, d.Message)
		}
	}
	return r
}

// simulate19 elaborates and runs testbench top of src under VHDL-2019
// (analyze19), like simulate.
func simulate19(t *testing.T, src, top string) (run, *Design) {
	t.Helper()
	r := analyze19(t, src)
	var out []string
	code := Interpreter(InterpOptions{
		Output: func(l string) { out = append(out, l) },
		Env:    EnvOptions{Standard: token.Std2019}})
	b := eval.NewBudget(0)
	d, ds := Elaborate(r, Options{Top: top, Budget: b, Code: code})
	if d == nil {
		t.Fatalf("elaboration: %v", ds)
	}
	for _, x := range ds {
		if x.Severity == diag.Error {
			t.Fatalf("elaboration: %s %s", x.Code, x.Message)
		}
	}
	if len(d.Missing) > 0 {
		t.Fatalf("no code for %v", d.Missing)
	}
	rec := trace.NewRecorder(nil)
	k, err := sim.New(d.Model, sim.Options{Budget: b,
		TimeBudget: 1_000_000_000_000, Writer: rec})
	if err != nil {
		t.Fatal(err)
	}
	k.RunAll(64, nil)
	if err := k.Err(); err != nil {
		t.Fatal(err)
	}
	res := run{k: k, diags: k.Diagnostics(), out: out}
	if tr, err := rec.Trace(); err == nil {
		res.text = trace.Encode(tr)
	}
	return res, d
}

// notes returns "<severity> <code> <message>" of each diagnostic of
// a run, in order.
func (r run) notes() []string {
	out := make([]string, 0, len(r.diags))
	for _, d := range r.diags {
		out = append(out, d.Severity.String()+" "+string(d.Code)+" "+
			d.Message)
	}
	return out
}

// wantNotes fails unless the run's diagnostics are exactly want.
func (r run) wantNotes(t *testing.T, want ...string) {
	t.Helper()
	got := r.notes()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("diagnostics:\n%s\nwant:\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
