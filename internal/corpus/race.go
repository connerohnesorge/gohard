package corpus

import (
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/simrun"
)

// Races reruns r at each seed through simrun.Races and returns the
// sim/race warning naming the two processes whose in-delta order made
// the traces differ (#29, #35), or nil when they agree.
func Races(r Run, src map[string][]byte, code simrun.CodeFunc,
	seeds []uint64,
) []diag.Diagnostic {
	res, cfg, err := r.Analyze(src)
	if err != nil {
		return nil
	}
	return simrun.Races(simrun.Options{Result: res, Sim: cfg.Simulation,
		Testbench: r.Design.Testbench, Code: code}, seeds)
}
