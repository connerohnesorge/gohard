package simrun

import (
	"errors"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/sim"
)

// errNoTrace stops race detection when a seed's run gave no trace.
var errNoTrace = errors.New("simrun: a run gave no trace")

// Races runs o at every seed in seeds and compares the traces (#29,
// #35): a race-free design gives a byte-identical trace at every seed,
// so a difference is reported as one sim/race warning naming the two
// processes whose in-delta order decided it. It returns no diagnostic
// when the traces agree, or when a run did not finish with a trace
// (that run's own diagnostics say why).
func Races(o Options, seeds []uint64) []diag.Diagnostic {
	if len(seeds) < 2 {
		return nil
	}
	var model *sim.Model
	run := func(seed uint64) (sim.RaceRun, error) {
		ro := o
		ro.Seed = seed
		ro.Orders = &sim.OrderLog{}
		out := Run(ro)
		if out.Trace == nil || out.Model == nil {
			return sim.RaceRun{}, errNoTrace
		}
		if model == nil {
			model = out.Model
		}
		return sim.RaceRun{Seed: seed, Trace: out.Trace,
			Orders: ro.Orders.Orders}, nil
	}
	first, err := run(seeds[0])
	if err != nil {
		return nil
	}
	ds, err := sim.CheckRaces(model, seeds,
		func(seed uint64) (sim.RaceRun, error) {
			if seed == seeds[0] {
				return first, nil
			}
			return run(seed)
		})
	if err != nil {
		return nil
	}
	return ds
}
