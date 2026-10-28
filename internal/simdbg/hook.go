package simdbg

import (
	"github.com/connerohnesorge/gohard/internal/elab"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// event is one statement a replay starts.
type event struct {
	n     int64 // its statement count
	proc  int
	depth int
	bp    bool // on a breakpoint line
}

// mode is what a replay looks for.
type mode uint8

const (
	// modeCount only counts.
	modeCount mode = iota
	// modeForward halts at the first statement after count from that
	// pred accepts.
	modeForward
	// modeGoto halts at count to.
	modeGoto
	// modeScan remembers the last statement between counts from and
	// to (both excluded) that pred accepts, and halts at to.
	modeScan
)

// hook is the session's elab.DebugHook: it counts the statements a
// replay starts and halts it where the mode says, keeping the state
// there. A replay starts at a checkpoint, so count starts at the
// checkpoint's count.
type hook struct {
	s        *Session
	count    int64
	mode     mode
	from, to int64
	pred     func(event) (string, bool)
	// halted is set once the hook halted the replay; stop and reason
	// are where and why (modeForward, modeGoto).
	halted bool
	stop   *Stop
	reason string
	// last and lastReason are what a scan found (0: nothing).
	last       int64
	lastReason string
}

var _ elab.DebugHook = (*hook)(nil)

// set prepares the hook for a replay from a checkpoint after count
// statements.
func (h *hook) set(count int64, m mode, from, to int64,
	pred func(event) (string, bool),
) {
	*h = hook{s: h.s, count: count, mode: m, from: from, to: to,
		pred: pred}
}

// Stmt implements sema.StmtHook: the statements of the pure functions
// the evaluator runs are not stops.
func (h *hook) Stmt(*sema.SeqStmt) {}

// Branch implements sema.StmtHook.
func (h *hook) Branch(*sema.SeqStmt, int) {}

// ProcStmt implements elab.DebugHook.
func (h *hook) ProcStmt(st *sema.SeqStmt, at *elab.At) bool {
	if h.halted {
		// A halted process does not run on; if anything did, it halts
		// again at once.
		return true
	}
	h.count++
	n := h.count
	switch h.mode {
	case modeGoto:
		if n < h.to {
			return false
		}
	case modeForward:
		if n <= h.from {
			return false
		}
		reason, ok := h.pred(h.event(n, st, at))
		if !ok {
			return false
		}
		h.reason = reason
	case modeScan:
		if n >= h.to {
			h.halted = true
			return true
		}
		if n > h.from {
			if reason, ok := h.pred(h.event(n, st, at)); ok {
				h.last, h.lastReason = n, reason
			}
		}
		return false
	default:
		return false
	}
	h.halted = true
	h.stop = h.capture(at)
	return true
}

func (h *hook) event(n int64, st *sema.SeqStmt, at *elab.At) event {
	return event{n: n, proc: at.Process(), depth: at.Depth(),
		bp: h.s.isBP(st, at.Src())}
}

// capture is the state at the statement at.
func (h *hook) capture(at *elab.At) *Stop {
	st := &Stop{Event: h.count, Time: at.Now(), Delta: at.Delta(),
		Process: at.Process(), Src: at.Src(), Depth: at.Depth(),
		Signals: at.Signals()}
	for i, p := range h.s.model.Processes {
		if fr, ok := at.Frames(i); ok {
			st.Threads = append(st.Threads, Thread{Process: i,
				Path: p.Path, Frames: fr})
		}
	}
	return st
}
