package sim

// interp_ctx.go holds the queries the sequential interpreter
// (internal/elab) makes on parts of signals: a name such as a(3) or
// r.f denotes the scalar subelements t.First .. t.First+N-1 of its
// signal, and its attributes are those of these net elements only.

import (
	"errors"

	"github.com/connerohnesorge/gohard/v2/internal/eval"
)

// partNets returns the net elements of t.
func (k *Kernel) partNets(t Target) ([]int32, error) {
	lo, hi, err := k.span(t)
	if err != nil {
		return nil, err
	}
	return k.sigs[t.Sig].nets[lo:hi], nil
}

// PartValues returns the current effective values of the scalar
// subelements of t, in order.
func (c *Ctx) PartValues(t Target) ([]eval.Value, error) {
	ns, err := c.k.partNets(t)
	if err != nil {
		return nil, err
	}
	out := make([]eval.Value, len(ns))
	for i, n := range ns {
		out[i] = c.k.nets[n].cur
	}
	return out, nil
}

// PartLastValues returns 'LAST_VALUE of each scalar subelement of t.
func (c *Ctx) PartLastValues(t Target) ([]eval.Value, error) {
	ns, err := c.k.partNets(t)
	if err != nil {
		return nil, err
	}
	out := make([]eval.Value, len(ns))
	for i, n := range ns {
		out[i] = c.k.nets[n].last
	}
	return out, nil
}

// PartEvent reports 'EVENT of t: an event on one of its elements in
// the current cycle.
func (c *Ctx) PartEvent(t Target) (bool, error) {
	ns, err := c.k.partNets(t)
	for _, n := range ns {
		if c.k.nets[n].eventCyc == c.k.cyc {
			return true, nil
		}
	}
	return false, err
}

// PartActive reports 'ACTIVE of t.
func (c *Ctx) PartActive(t Target) (bool, error) {
	ns, err := c.k.partNets(t)
	for _, n := range ns {
		if c.k.nets[n].activeCyc == c.k.cyc {
			return true, nil
		}
	}
	return false, err
}

// PartLastEvent returns 'LAST_EVENT of t in femtoseconds (TimeHigh
// when no element ever had an event).
func (c *Ctx) PartLastEvent(t Target) (int64, error) {
	return c.partSince(t, func(n *net) int64 { return n.lastEvent })
}

// PartLastActive returns 'LAST_ACTIVE of t in femtoseconds.
func (c *Ctx) PartLastActive(t Target) (int64, error) {
	return c.partSince(t, func(n *net) int64 { return n.lastActive })
}

func (c *Ctx) partSince(t Target, at func(*net) int64) (int64, error) {
	ns, err := c.k.partNets(t)
	if err != nil {
		return 0, err
	}
	last := int64(-1)
	for _, n := range ns {
		last = max(last, at(&c.k.nets[n]))
	}
	if last < 0 {
		return TimeHigh, nil
	}
	return int64(c.k.now) - last, nil
}

// PartDriving reports 'DRIVING of t for the running process.
func (c *Ctx) PartDriving(t Target) (bool, error) {
	k := c.k
	lo, hi, err := k.span(t)
	if err != nil {
		return false, err
	}
	any := false
	for j := lo; j < hi; j++ {
		d, ok := k.findDriver(k.current, t.Sig, j)
		if !ok {
			return false, errors.New("'driving of " +
				k.m.Signals[t.Sig].Path + ": the process has no driver")
		}
		any = any || !k.drivers[d].null
	}
	return any, nil
}

// PartDrivingValues returns 'DRIVING_VALUE of each scalar subelement
// of t for the running process.
func (c *Ctx) PartDrivingValues(t Target) ([]eval.Value, error) {
	k := c.k
	lo, hi, err := k.span(t)
	if err != nil {
		return nil, err
	}
	out := make([]eval.Value, 0, hi-lo)
	for j := lo; j < hi; j++ {
		d, ok := k.findDriver(k.current, t.Sig, j)
		if !ok {
			return nil, errors.New("'driving_value of " +
				k.m.Signals[t.Sig].Path + ": the process has no driver")
		}
		out = append(out, k.drivers[d].cur)
	}
	return out, nil
}

// Path returns the hierarchical path of the running process.
func (c *Ctx) Path() string {
	if c.k.current < 0 {
		return c.k.m.Testbench
	}
	return c.k.m.Processes[c.k.current].Path
}
