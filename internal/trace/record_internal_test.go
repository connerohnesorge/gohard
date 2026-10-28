package trace

import (
	"testing"
)

// TestCloneShares: Clone copies none of the entries recorded so far
// (#68). The debugger keeps up to 16 checkpoints, each a clone of the
// one before it, and resumes each by cloning it again; a copy per
// clone held the run's trace once per checkpoint.
func TestCloneShares(t *testing.T) {
	r := NewRecorder(nil)
	d := Design{Types: []Type{{Name: "integer", Kind: Integer,
		Low: -10, High: 1 << 20}},
		Signals: []Signal{{Path: "tb.n", Type: "integer", Dir: Internal}}}
	if err := r.Begin(Header{Testbench: "tb"}, d); err != nil {
		t.Fatal(err)
	}
	for i := range 1000 {
		err := r.Change(Change{Time: Time(i), Signal: 0,
			Value: Value{Int: int64(i)}})
		if err != nil {
			t.Fatal(err)
		}
	}
	first := &r.t.Changes[0]
	c := r.Clone()
	c2 := c.Clone()
	for _, x := range []*Recorder{r, c, c2} {
		if len(x.frozen) != 1 || &x.frozen[0].changes[0] != first ||
			len(x.t.Changes) != 0 {
			t.Fatalf("the clones copied the recorded changes: %d "+
				"segments, %d unshared", len(x.frozen), len(x.t.Changes))
		}
	}
}
