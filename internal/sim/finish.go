package sim

import "github.com/connerohnesorge/gohard/internal/diag"

// Finisher is implemented by Code that reports when a run ends: PSL
// cover misses and strong obligations still pending (#35). The kernel
// calls Finish once per process, in process order, at the end time
// before the trace ends, unless the run was cancelled or failed.
type Finisher interface {
	Finish(c *Ctx) error
}

// Diagnostic records d (located at src when it has a path) as a run
// diagnostic: the hook for PSL cover results and needs-formal reports.
func (c *Ctx) Diagnostic(src SrcRef, d diag.Diagnostic) {
	c.k.report(src, d)
}

// finishCodes runs the Finishers after the run ended with status s.
func (k *Kernel) finishCodes(s Status) {
	if !k.inited || s == Cancelled || s == Failed {
		return
	}
	for i := range k.procs {
		f, ok := k.procs[i].code.(Finisher)
		if !ok {
			continue
		}
		k.current = int32(i)
		err := f.Finish(&k.ctx)
		k.current = -1
		if err != nil {
			p := &k.m.Processes[i]
			k.report(p.Src, diag.New(diag.SimRuntimeError, err.Error(),
				p.Path, timeText(k.now)))
		}
	}
}
