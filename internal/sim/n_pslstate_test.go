package sim

import "testing"

// TestPSLStateKernel (#18): ClearPSL clears the failed flag and both
// covered counts and advances the epoch but keeps the cover-assert
// setting; Clone copies the PSL state, and the copies then evolve
// independently.
func TestPSLStateKernel(t *testing.T) {
	k, err := New(&Model{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	c := &k.ctx
	if c.PSLFailed() || c.PSLCoverAssert() || c.PSLEpoch() != 0 ||
		c.PSLCovered(false) != 0 || c.PSLCovered(true) != 0 {
		t.Fatalf("initial state %+v", k.psl)
	}
	c.PSLFail()
	c.PSLCover(false)
	c.PSLCover(true)
	c.PSLCover(true)
	c.SetPSLCoverAssert(true)
	f := k.Clone()
	fc := &f.ctx
	c.ClearPSL()
	if c.PSLFailed() || !c.PSLCoverAssert() || c.PSLEpoch() != 1 ||
		c.PSLCovered(false) != 0 || c.PSLCovered(true) != 0 {
		t.Fatalf("cleared state %+v", k.psl)
	}
	if !fc.PSLFailed() || !fc.PSLCoverAssert() || fc.PSLEpoch() != 0 ||
		fc.PSLCovered(false) != 1 || fc.PSLCovered(true) != 2 {
		t.Fatalf("clone state %+v", f.psl)
	}
}
