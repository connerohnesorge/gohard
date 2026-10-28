package trace_test

import (
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
	"github.com/connerohnesorge/gohard/v2/internal/trace/tracetest"
)

// feed writes the entries a to b (in stream order) of s to w.
func feed(t *testing.T, s *tracetest.Stream, w trace.Writer, a, b int) {
	t.Helper()
	ci, ei := 0, 0
	for i, ev := range s.Order {
		var err error
		switch {
		case i >= b:
			return
		case ev && i >= a:
			err = w.Event(s.Events[ei])
		case !ev && i >= a:
			err = w.Change(s.Changes[ci])
		}
		if ev {
			ei++
		} else {
			ci++
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestCloneFork is the property the debugger's checkpoints rely on
// (#68): a recorder cloned at any point of a stream, with the clone
// cloned again further on, gives every recorder of the chain that is
// fed the rest of the stream the trace of the whole stream, whichever
// of them ends first. Trace sorts the entries of each delta; the
// recorders share what they recorded before each Clone, and sorting
// one trace must not reorder another's.
func TestCloneFork(t *testing.T) {
	for seed := range uint64(200) {
		s := tracetest.Random(seed, 8, 12)
		want := trace.Encode(record(t, nil, s))
		rng := &env.SplitMix64{State: seed}
		n := len(s.Order)
		r := trace.NewRecorder(nil)
		if err := r.Begin(s.Header, s.Design); err != nil {
			t.Fatal(err)
		}
		// chain[i] has been fed the entries before fed[i]; each is
		// fed some more, then cloned.
		chain := []*trace.Recorder{r}
		fed := []int{0}
		for range 4 {
			last := len(chain) - 1
			// A clone before any entry or after the last one is
			// picked as often as one in between.
			at := fed[last] + env.Intn(rng, n-fed[last]+1)
			switch env.Intn(rng, 3) {
			case 0:
				at = fed[last]
			case 1:
				at = n
			}
			feed(t, s, chain[last], fed[last], at)
			fed[last] = at
			chain = append(chain, chain[last].Clone())
			fed = append(fed, at)
		}
		// End them in a seeded order: each must still see its own
		// entries.
		for _, i := range env.Shuffle(rng.Uint64()|1, 0, len(chain)) {
			cut := fed[i]
			feed(t, s, chain[i], cut, n)
			if err := chain[i].End(s.End); err != nil {
				t.Fatal(err)
			}
			tr, err := chain[i].Trace()
			if err != nil {
				t.Fatalf("seed %d recorder %d: %v", seed, i, err)
			}
			got := trace.Encode(tr)
			if l, a, b, ok := trace.FirstDiff(want, got); ok {
				t.Fatalf("seed %d recorder %d (cloned after %d of %d): "+
					"line %d: %q != %q", seed, i, cut, n, l, a, b)
			}
		}
	}
}
