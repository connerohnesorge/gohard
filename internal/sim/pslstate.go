package sim

// pslState is the design-wide PSL state of the VHDL-2019 std.env
// PSL API (#18): PslAssertFailed, PslIsCovered, PslIsAssertCovered,
// Set/GetPslCoverAssert and ClearPslState. The per-directive state
// stays in the PSL runners (internal/elab/psl); the kernel holds what
// the design queries and the epoch that tells every runner a
// ClearPslState happened. Values only, so Kernel.Clone copies it.
type pslState struct {
	// epoch counts the ClearPslState calls of the run.
	epoch uint64
	// failed is set by an enabled PSL assertion violation since the
	// last ClearPslState.
	failed bool
	// coverAssert is SetPslCoverAssert (false until set).
	coverAssert bool
	// covers and asserts count the cover and assert directives
	// covered since the last ClearPslState, each counted once.
	covers, asserts int
}

// PSLEpoch is the number of ClearPslState calls so far: a PSL runner
// whose state is from an older epoch resets it before going on.
func (c *Ctx) PSLEpoch() uint64 { return c.k.psl.epoch }

// ClearPSL is ClearPslState: no PSL assertion has failed and no
// directive is covered any more; the epoch advances. The cover-assert
// setting is kept.
func (c *Ctx) ClearPSL() {
	p := &c.k.psl
	p.epoch++
	p.failed, p.covers, p.asserts = false, 0, 0
}

// PSLFail records an enabled PSL assertion violation (PslAssertFailed).
func (c *Ctx) PSLFail() { c.k.psl.failed = true }

// PSLFailed is PslAssertFailed.
func (c *Ctx) PSLFailed() bool { return c.k.psl.failed }

// PSLCover records that one directive became covered in the current
// epoch: a cover directive (assert false) or an assert directive. A
// runner calls it at most once per directive and epoch.
func (c *Ctx) PSLCover(assert bool) {
	if assert {
		c.k.psl.asserts++
	} else {
		c.k.psl.covers++
	}
}

// PSLCovered is the number of cover (assert false) or assert
// directives covered in the current epoch.
func (c *Ctx) PSLCovered(assert bool) int {
	if assert {
		return c.k.psl.asserts
	}
	return c.k.psl.covers
}

// SetPSLCoverAssert is SetPslCoverAssert.
func (c *Ctx) SetPSLCoverAssert(on bool) { c.k.psl.coverAssert = on }

// PSLCoverAssert is GetPslCoverAssert.
func (c *Ctx) PSLCoverAssert() bool { return c.k.psl.coverAssert }
