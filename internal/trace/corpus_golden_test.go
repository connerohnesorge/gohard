package trace_test

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// corpusGoldens decodes every testdata/corpus/*/golden/trace.txt. The
// corpus has 15 traced designs (at least the 13 of v1.0).
func corpusGoldens(t *testing.T) map[string]*trace.Trace {
	t.Helper()
	files, err := filepath.Glob("../../testdata/corpus/*/golden/trace.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 13 {
		t.Fatalf("%d corpus trace goldens, want at least 13", len(files))
	}
	out := map[string]*trace.Trace{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		tr, err := trace.Decode(data)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if !bytes.Equal(trace.Encode(tr), data) {
			t.Fatalf("%s: decode/encode is not the identity", f)
		}
		out[filepath.Base(filepath.Dir(filepath.Dir(f)))] = tr
	}
	return out
}

// TestValueLookupCorpus: value lookup by time agrees with a naive scan
// of the change list on every corpus golden: for every signal at its
// first and last change, and at random queries drawn from change times
// (exactly, one before, one after) and from the whole run (#36). A
// design without signals (prot_cond checks variables and a protected
// port only) has nothing to look up; at least 13 goldens must.
func TestValueLookupCorpus(t *testing.T) {
	r := &env.SplitMix64{State: 36}
	deltas := []uint32{0, 1, 2, ^uint32(0)}
	goldens := corpusGoldens(t)
	looked := 0
	defer func() {
		if looked < 13 {
			t.Errorf("only %d goldens with signals and changes", looked)
		}
	}()
	for _, name := range slices.Sorted(maps.Keys(goldens)) {
		tr := goldens[name]
		if len(tr.Signals) == 0 {
			continue
		}
		if len(tr.Changes) == 0 {
			t.Fatalf("%s: signals but no changes", name)
		}
		looked++
		first := map[int]trace.Time{}
		last := map[int]trace.Time{}
		for _, c := range tr.Changes {
			if _, ok := first[c.Signal]; !ok {
				first[c.Signal] = c.Time
			}
			last[c.Signal] = c.Time
		}
		for sig := range tr.Signals {
			for _, d := range deltas {
				lookupAgrees(t, name, tr, sig, first[sig], d)
				lookupAgrees(t, name, tr, sig, last[sig], d)
			}
		}
		queries := 2000
		if len(tr.Changes) > 20000 {
			queries = 100
		}
		for range queries {
			sig := env.Intn(r, len(tr.Signals))
			c := tr.Changes[env.Intn(r, len(tr.Changes))]
			at := c.Time + trace.Time(env.Intn(r, 3)) - 1
			if c.Time == 0 && at > c.Time+1 {
				at = 0
			}
			if env.Intn(r, 3) == 0 {
				at = trace.Time(env.Intn(r, int(tr.Header.End)+3))
			}
			lookupAgrees(t, name, tr, sig, at,
				deltas[env.Intn(r, len(deltas))])
		}
	}
}

func lookupAgrees(t *testing.T, name string, tr *trace.Trace, sig int,
	at trace.Time, delta uint32,
) {
	t.Helper()
	want, wok := naiveAt(tr, sig, at, delta)
	got, c, ok := tr.ValueAtDelta(tr.Signals[sig].Path, at, delta)
	if ok != wok || ok && (!got.Equal(want) || !c.Value.Equal(want) ||
		c.Signal != sig) {
		t.Fatalf("%s: %s at %d+%d: got %v %v, want %v %v", name,
			tr.Signals[sig].Path, at, delta, got, ok, want, wok)
	}
}
