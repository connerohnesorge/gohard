package elab

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/token"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// gcHeapRun runs the gc_heap corpus design (testdata/corpus/gc_heap,
// #27) at seed with gc.
func gcHeapRun(t *testing.T, seed uint64, gc GCOptions) kRun {
	t.Helper()
	var files []gSource
	for _, n := range []string{"gc_heap.vhd", "gc_heap_tb.vhd"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "testdata",
			"corpus", "gc_heap", n))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, gSource{n, string(b)})
	}
	r := gAnalyze(t, files, "")
	var res kRun
	gc.Done = func(s GCStats) { res.stats = append(res.stats, s) }
	code := Interpreter(InterpOptions{
		Output: func(l string) { res.out = append(res.out, l) },
		Env:    EnvOptions{Standard: token.Std2019}, GC: gc})
	b := eval.NewBudget(50_000_000)
	d := gElab(t, r, "gc_heap_tb", code, b)
	rec := trace.NewRecorder(nil)
	opts := sim.Options{Budget: b, TimeBudget: 1_000_000_000_000_000,
		Writer: rec, Seed: seed}
	if seed != 0 {
		opts.Order = env.SeedOrder{Seed: seed}
	}
	k, err := sim.New(d.Model, opts)
	if err != nil {
		t.Fatal(err)
	}
	k.RunAll(64, nil)
	if err := k.Err(); err != nil {
		t.Fatal(err)
	}
	for _, x := range k.Diagnostics() {
		res.diags = append(res.diags, string(x.Code)+": "+x.Message)
	}
	res.steps = k.Steps()
	tr, err := rec.Trace()
	if err != nil {
		t.Fatal(err)
	}
	res.text = trace.Encode(tr)
	if now := k.Now(); now != 2020_000_000 {
		t.Errorf("stopped at %d fs, want 2020 ns (NVC 1.23)", now)
	}
	if k.Status() != sim.Stopped {
		t.Fatalf("status %v, diags %v", k.Status(), res.diags)
	}
	return res
}

// TestGCHeapCorpus: the gc_heap corpus design passes, its 20,000
// dropped list cells keep the live count bounded (one period plus two
// lists: the one a function is building, where there is no safe point,
// and the one before it), and its trace is the same bytes with the collector
// off, forced at every allocation and at the default period, for
// several seeds.
func TestGCHeapCorpus(t *testing.T) {
	base := gcHeapRun(t, 0, GCOptions{})
	if !slices.Equal(base.out, []string{"gc_heap: queue and lists ok"}) {
		t.Fatalf("output %q", base.out)
	}
	if len(base.diags) != 0 {
		t.Fatalf("diagnostics %v", base.diags)
	}
	s := base.stats[0]
	t.Logf("default: %+v", s)
	if s.Handles < 20000 || s.Collections == 0 ||
		s.Peak > DefaultGCMin+2*500+100 {
		t.Errorf("heap statistics %+v", s)
	}
	seeds := []uint64{0, 1, 5}
	if testing.Short() {
		seeds = seeds[:2]
	}
	for _, seed := range seeds {
		for _, gc := range []GCOptions{{Off: true}, {Every: 1}, {}} {
			if seed == 0 && !gc.Off && gc.Every == 0 {
				continue
			}
			r := gcHeapRun(t, seed, gc)
			if !bytes.Equal(r.text, base.text) ||
				!slices.Equal(r.out, base.out) || r.steps != base.steps {
				t.Errorf("seed %d gc %+v: run differs", seed, gc)
			}
		}
	}
}
