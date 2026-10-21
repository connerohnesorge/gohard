package sim

import (
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// ForceMode is the mode of a force or release (LRM 10.5.2.1).
type ForceMode uint8

// Force modes. ForceDefault picks the LRM default for the target
// (DefaultForceMode).
const (
	ForceDefault ForceMode = iota
	// ForceIn forces the effective value: what readers of the signal
	// see.
	ForceIn
	// ForceOut forces the driving value: the value the signal's
	// sources produce, which readers see unless a force of mode in is
	// also in effect.
	ForceOut
)

// String returns the VHDL keyword.
func (m ForceMode) String() string {
	switch m {
	case ForceIn:
		return "in"
	case ForceOut:
		return "out"
	}
	return "default"
}

// DefaultForceMode returns the force mode of a force or release
// without an explicit mode (LRM 10.5.2.1): in for a port of mode in and
// for a signal that is not a port, out for a port of mode out, inout or
// buffer.
func DefaultForceMode(s *Signal) ForceMode {
	switch s.Dir {
	case trace.Out, trace.Inout, trace.Buffer:
		return ForceOut
	}
	return ForceIn
}

// ForceMode forces t to v in mode m from the next delta on. A force of
// mode in replaces the effective value; one of mode out replaces the
// driving value (the resolved value of the drivers). Both apply to the
// net element the target shares with its actual: a collapsed port and
// its actual are one net, so a force on either reaches both. A force
// of mode in hides a force of mode out until it is released.
func (c *Ctx) ForceMode(t Target, v eval.Value, m ForceMode) error {
	return c.force(t, v, false, c.out(t, m))
}

// ReleaseMode releases a force of mode m on t. Releasing returns the
// signal to its driving value at the next delta; it does not schedule
// a transaction on any driver. Releasing a part that is not forced
// does nothing.
func (c *Ctx) ReleaseMode(t Target, m ForceMode) error {
	return c.force(t, eval.Value{}, true, c.out(t, m))
}

func (c *Ctx) out(t Target, m ForceMode) bool {
	if m == ForceDefault {
		if int(t.Sig) < 0 || int(t.Sig) >= len(c.k.m.Signals) {
			return false
		}
		m = DefaultForceMode(&c.k.m.Signals[t.Sig])
	}
	return m == ForceOut
}

// Forced reports whether any element of s is under a force of either
// mode.
func (c *Ctx) Forced(s SigID) bool {
	for _, ni := range c.k.sigs[s].nets {
		if f := c.k.nets[ni].forced; f != 0 {
			if nf := &c.k.forced[f-1]; nf.forced || nf.oforced {
				return true
			}
		}
	}
	return false
}
