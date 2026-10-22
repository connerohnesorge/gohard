package sema_test

// d_conformance_test.go runs the conformance cases of the static
// evaluator's v1.1 features (testdata/conformance/{int64,cond_return,
// image}) through sema, elab and the kernel with the v1.0 unsupported
// detection bypassed (the units it marks opaque are simulated anyway),
// and compares the report and assertion messages and pass/fail with
// NVC's recording. TestConformance (internal/cli) checks the same cases
// through gohard check --sim once the detectors are gone.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/corpus"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/elab"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/token"
)

// dFeatures are the conformance folders of builder D's features.
var dFeatures = []string{"int64", "cond_return", "image"}

// dWaiting are the cases whose run needs another builder's part of the
// feature (an elab or analyzer change); until it lands they are logged
// instead of failing. A case listed here that passes is an error, so
// the list cannot go stale.
var dWaiting = map[string]string{}

func TestDConformanceBypass(t *testing.T) {
	root := filepath.Join("..", "..", corpus.ConformanceDir)
	for _, feat := range dFeatures {
		files, err := filepath.Glob(filepath.Join(root, feat, "*.vhd"))
		if err != nil || len(files) == 0 {
			t.Fatalf("%s: no cases (%v)", feat, err)
		}
		for _, f := range files {
			name := strings.TrimSuffix(filepath.Base(f), ".vhd")
			t.Run(feat+"/"+name, func(t *testing.T) {
				dRunCase(t, root, feat, name)
			})
		}
	}
}

func dRunCase(t *testing.T, root, feat, name string) {
	src, err := os.ReadFile(filepath.Join(root, feat, name+".vhd"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := corpus.ParseConformanceCase(feat, name, src)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := os.ReadFile(filepath.Join(root, feat, name+".nvc.txt"))
	if err != nil {
		t.Fatalf("every case of these features has an NVC recording: %v",
			err)
	}
	nvc, err := corpus.ParseNVC(rec)
	if err != nil {
		t.Fatal(err)
	}
	got, failed, err := dSimulate(c, src)
	var probs []string
	if err != nil {
		probs = append(probs, err.Error())
	}
	want := corpus.NVCMessages(nvc.Output)
	if !slices.Equal(got, want) {
		probs = append(probs, fmt.Sprintf("messages\n%v\nNVC\n%v", got,
			want))
	}
	if failed != (nvc.Exit != 0) {
		probs = append(probs, fmt.Sprintf("failed %v, NVC exit %d",
			failed, nvc.Exit))
	}
	key := feat + "/" + name
	if why, ok := dWaiting[key]; ok {
		if len(probs) == 0 {
			t.Errorf("%s passes now: remove it from dWaiting", key)
		}
		t.Logf("waiting for %s: %s", why, strings.Join(probs, "; "))
		return
	}
	for _, p := range probs {
		t.Error(p)
	}
}

// dSimulate analyzes, elaborates and runs the case, returning its
// report and assertion messages (sorted, as corpus.GoHardMessages
// gives them) and whether the run failed.
func dSimulate(c corpus.ConformanceCase, src []byte,
) ([]corpus.SimMessage, bool, error) {
	cfg := config.Defaults()
	cfg.Standard = c.Std
	std, _ := token.ParseStandard(c.Std)
	r := sema.Analyze(sema.Input{Config: cfg, Files: []sema.Source{{
		Path: c.File(), Text: src}}})
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error && d.Code != diag.UnsupportedConstruct {
			return nil, true, fmt.Errorf("analysis: %d:%d: %s: %s",
				d.Line, d.Col, d.Code, d.Message)
		}
	}
	for _, u := range r.Units {
		u.Opaque = false // the bypass: simulate what v1.0 flags
	}
	b := eval.NewBudget(100_000_000)
	code := elab.Interpreter(elab.InterpOptions{
		Env: elab.EnvOptions{Standard: std}})
	d, ds := elab.Elaborate(r, elab.Options{Top: c.Top, Budget: b,
		Code: code})
	if d == nil {
		return nil, true, fmt.Errorf("elaboration: %v", ds)
	}
	k, err := sim.New(d.Model, sim.Options{Budget: b,
		TimeBudget: 1_000_000_000_000})
	if err != nil {
		return nil, true, err
	}
	k.RunAll(64, nil)
	failed := k.Err() != nil
	var out strings.Builder
	for _, x := range k.Diagnostics() {
		failed = failed || x.Severity == diag.Error
		fmt.Fprintf(&out, "x: %s[%s]: %s\n", x.Severity, x.Code,
			strings.ReplaceAll(x.Message, "\n", `\n`))
	}
	return corpus.GoHardMessages([]byte(out.String())), failed, nil
}
