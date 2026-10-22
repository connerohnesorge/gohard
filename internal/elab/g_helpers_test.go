package elab

import (
	"strings"
	"testing"
	"time"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/token"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// gRun is one VHDL-2019 run of the std.env and std.reflection tests:
// the messages of its reports and failed assertions in order, and its
// error diagnostics.
type gRun struct {
	k    *sim.Kernel
	msgs []string
	errs []string
	text []byte
}

// gSource is one file of a gSim design.
type gSource struct{ path, text string }

// gAnalyze analyzes the files under VHDL-2019. Until the integrator
// retires the v1.1 unsupported/construct detectors (#18), they are
// bypassed here: the units they make opaque are analyzed in full
// anyway, so the test clears the mark and drops those diagnostics.
func gAnalyze(t *testing.T, files []gSource, toolVersion string,
) *sema.Result {
	t.Helper()
	cfg := config.Defaults()
	cfg.Standard = "2019"
	in := sema.Input{Config: cfg, ToolVersion: toolVersion}
	for _, f := range files {
		in.Files = append(in.Files, sema.Source{Path: f.path,
			Text: []byte(f.text)})
	}
	r := sema.Analyze(in)
	for _, u := range r.Units {
		u.Opaque, u.Unchecked = false, false
	}
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error && d.Code != diag.UnsupportedConstruct &&
			d.Code != diag.SemaUnchecked {
			t.Fatalf("analysis: %s:%d:%d: %s", d.Path, d.Line, d.Col,
				d.Message)
		}
	}
	return r
}

// gDesign analyzes the files and elaborates top with env.
func gDesign(t *testing.T, files []gSource, top string, env EnvOptions,
	b *eval.Budget,
) *Design {
	t.Helper()
	env.Standard = token.Std2019
	return gElab(t, gAnalyze(t, files, env.ToolVersion), top,
		Interpreter(InterpOptions{Env: env}), b)
}

// gDesignCode is gDesign with the process code hook code.
func gDesignCode(t *testing.T, files []gSource, top string,
	code func(*Design, *Proc) (sim.Code, error), b *eval.Budget,
) *Design {
	t.Helper()
	return gElab(t, gAnalyze(t, files, ""), top, code, b)
}

// gElab elaborates top of r.
func gElab(t *testing.T, r *sema.Result, top string,
	code func(*Design, *Proc) (sim.Code, error), b *eval.Budget,
) *Design {
	t.Helper()
	d, ds := Elaborate(r, Options{Top: top, Budget: b, Code: code})
	if d == nil {
		t.Fatalf("elaboration: %v", ds)
	}
	for _, x := range ds {
		if x.Severity == diag.Error {
			t.Fatalf("elaboration: %s", x.Message)
		}
	}
	return d
}

// gSim simulates top of the files with env to the end.
func gSim(t *testing.T, files []gSource, top string, env EnvOptions,
) gRun {
	t.Helper()
	b := eval.NewBudget(0)
	d := gDesign(t, files, top, env, b)
	rec := trace.NewRecorder(nil)
	k, err := sim.New(d.Model, sim.Options{Budget: b,
		TimeBudget: 10_000_000_000_000_000, Writer: rec})
	if err != nil {
		t.Fatal(err)
	}
	k.RunAll(64, nil)
	if err := k.Err(); err != nil {
		t.Fatal(err)
	}
	res := gRun{k: k}
	tr, err := rec.Trace()
	if err != nil {
		t.Fatal(err)
	}
	res.text = trace.Encode(tr)
	for _, e := range tr.Events {
		if e.Kind == trace.Report || e.Kind == trace.Assert {
			res.msgs = append(res.msgs, e.Message)
		}
	}
	for _, x := range k.Diagnostics() {
		if x.Severity == diag.Error {
			res.errs = append(res.errs, string(x.Code)+": "+x.Message)
		}
	}
	return res
}

// want fails unless the messages are exactly want and no error was
// reported.
func (r gRun) want(t *testing.T, want ...string) {
	t.Helper()
	for _, e := range r.errs {
		t.Errorf("error: %s", e)
	}
	got := strings.Join(r.msgs, "\n")
	if w := strings.Join(want, "\n"); got != w {
		t.Errorf("messages:\n%s\nwant:\n%s", got, w)
	}
}

// gEpoch is a fixed test epoch.
var gEpoch = time.Date(2001, 9, 9, 1, 46, 40, 0, time.UTC)
