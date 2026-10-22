package psl

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/elab"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
)

// TestPSLNatives (#18): the std.env PSL natives of the sequential
// interpreter, run by the conformance case psl_api/psl_state with the
// real interpreter and PSL runners (its analysis diagnostic, the
// v1.0 unsupported/construct of the PSL API, is tolerated here),
// report the decided semantics after each step of the case, and the
// PSL violation is counted by the std.env assert counters.
func TestPSLNatives(t *testing.T) {
	const path = "../../../testdata/conformance/psl_api/psl_state.vhd"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Standard = "2019"
	r := sema.Analyze(sema.Input{Config: cfg, Jobs: env.Sequential{},
		Files: []sema.Source{{Path: "psl_state.vhd", Text: src}}})
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error && d.Code != diag.UnsupportedConstruct {
			t.Fatalf("analysis: %d: %s", d.Line, d.Message)
		}
	}
	code := Wrap(elab.Interpreter(elab.InterpOptions{}))
	d, ds := elab.Elaborate(r, elab.Options{Top: "psl_state_tb",
		Code: code})
	if d == nil || len(ds) > 0 || len(d.Missing) > 0 {
		t.Fatalf("elaborate: %v", ds)
	}
	k, err := sim.New(d.Model, sim.Options{})
	if err != nil {
		t.Fatal(err)
	}
	k.RunAll(64, nil)
	if k.Err() != nil {
		t.Fatal(k.Err())
	}
	var notes []string
	for _, x := range k.Diagnostics() {
		if x.Code == diag.SimAssertNote {
			_, m, _ := strings.Cut(x.Message, "report note: ")
			notes = append(notes, m)
		}
	}
	row := func(what, failed, covered, asserts, coverAssert,
		warnings string,
	) string {
		return what + ": failed " + failed + ", covered " + covered +
			", asserts covered " + asserts + ", cover asserts " +
			coverAssert + ", warnings " + warnings
	}
	want := []string{
		row("start", "false", "false", "false", "false", "0"),
		row("covered", "false", "true", "true", "false", "0"),
		row("cover asserts", "false", "true", "true", "true", "0"),
		row("cleared", "false", "false", "false", "true", "0"),
		row("failed", "true", "false", "true", "true", "1"),
		row("cleared again", "false", "false", "false", "false", "1"),
	}
	if !slices.Equal(notes, want) {
		t.Fatalf("notes:\n%s\nwant:\n%s", strings.Join(notes, "\n"),
			strings.Join(want, "\n"))
	}
	if a := k.Asserts(); a.Warning != 1 || a.Total() != 1 {
		t.Errorf("asserts %+v, want one warning", a)
	}
	if k.Status() != sim.Stopped {
		t.Errorf("status %v, want stopped", k.Status())
	}
}
