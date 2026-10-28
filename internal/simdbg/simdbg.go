// Package simdbg is the simulation debugger (#68): breakpoints,
// stepping forward and back through the statements of one testbench
// run, and the state at each stop (time, the call stack and variables
// of every process, the signals). `gohard dap` serves it over the
// Debug Adapter Protocol (internal/dap).
//
// A position in a run is its statement count: the number of
// statements the interpreter has started, counted from the kernel's
// initialization (elab.DebugHook). A run is a pure function of its
// options and seed (P1), so a count names one point of the run, and
// the session reaches it by replaying the run: it never keeps a run
// suspended inside a process. It keeps checkpoints
// (simrun.Checkpoint) at the start of the kernel cycles it stopped
// in, and every move replays from the nearest checkpoint before its
// target with a hook that counts statements and halts the replay at
// the target (elab.ErrHalted), then throws the halted kernel away. A
// move back is a scan for the earlier count, then a move to it, so it
// lands on the state a move forward to that count gives. The hook
// only counts and halts: a run with breakpoints records the trace a
// run without them records. Every replay runs under the run's own
// budgets, so every move ends (P7).
//
// Statements of pure functions, which the evaluator runs, are not
// stops: a breakpoint there is not verified and stepping steps over
// their calls.
package simdbg

import (
	"maps"
	"slices"

	"github.com/connerohnesorge/gohard/internal/coverage"
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/elab"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/simrun"
)

// Move is a debugger command that moves the run to another stop.
type Move uint8

// The moves. Every move also stops at a statement on a breakpoint
// line it meets first.
const (
	// Continue runs to the next statement on a breakpoint line.
	Continue Move = iota + 1
	// Next runs to the next statement of the stopped process at the
	// same or an outer call depth (step over).
	Next
	// StepIn runs to the next statement of the stopped process.
	StepIn
	// StepOut runs to the next statement of the stopped process in
	// a caller; in the process's own statements it is Next.
	StepOut
	// StepBack goes back to the last earlier statement of the
	// stopped process at the same or an outer call depth (Next
	// backwards), or to the first statement of the run.
	StepBack
	// ReverseContinue goes back to the last earlier statement on a
	// breakpoint line, or to the first statement of the run.
	ReverseContinue
)

// The reasons a move stops, as the Debug Adapter Protocol names them.
const (
	ReasonEntry      = "entry"
	ReasonStep       = "step"
	ReasonBreakpoint = "breakpoint"
	ReasonGoto       = "goto"
)

// MaxCheckpoints is the most checkpoints a session keeps: past it,
// every other one but the first is dropped. Each holds a copy of the
// kernel and of the trace so far, so this bounds the session's memory
// to that many copies of the run's state; fewer checkpoints only make
// replays longer.
const MaxCheckpoints = 16

// Stop is the state of the run at one statement, before it executes.
type Stop struct {
	// Event is the statement count: the statement is the Event-th
	// the run started (1 is the first).
	Event int64
	Time  sim.Time
	Delta uint32
	// Process is the index in the model of the process running the
	// statement, Src the statement's place and Depth the number of
	// frames of the process (1 in its own statements).
	Process int
	Src     sim.SrcRef
	Depth   int
	// Threads are the processes the interpreter runs, in model
	// order, with their call stacks.
	Threads []Thread
	// Signals are the signals with their values (elab.At.Signals).
	Signals []elab.Value
}

// Thread is one process at a stop.
type Thread struct {
	// Process is the index in the model; Path the process's path.
	Process int
	Path    string
	// Frames is the call stack, innermost first; empty before the
	// process starts and after it ends.
	Frames []elab.Frame
}

// Thread returns the thread of process p (nil when the interpreter
// does not run it).
func (s *Stop) Thread(p int) *Thread {
	i, ok := slices.BinarySearchFunc(s.Threads, p, func(t Thread,
		p int,
	) int {
		return t.Process - p
	})
	if !ok {
		return nil
	}
	return &s.Threads[i]
}

// Result is where a move ended.
type Result struct {
	// Stop is the stop the move reached (nil when it did not stop).
	Stop *Stop
	// Reason is why it stopped (Reason*).
	Reason string
	// Ended reports that the run ended before a stop
	// (Session.Outcome).
	Ended bool
	// Cancelled reports that Options.Cancelled stopped the move: the
	// session is still where it was.
	Cancelled bool
}

// Session is one testbench run under the debugger. It is not safe
// for concurrent use.
type Session struct {
	h         *hook
	model     *sim.Model
	cps       []checkpoint // in cycle order
	stop      *Stop
	out       *simrun.Outcome
	cancelled func() bool
	// bps are the breakpoint lines; bpOf caches, per statement run,
	// whether its line is one (lookup only).
	bps  map[srcLine]bool
	bpOf map[*sema.SeqStmt]bool
	// lines are the lines of the statements that stop, by path.
	lines map[string][]int
}

// checkpoint is a frozen run at the start of kernel cycle cycle,
// after events statements.
type checkpoint struct {
	cp     *simrun.Checkpoint
	events int64
	cycle  int64
}

type srcLine struct {
	path string
	line int
}

// Start elaborates o.Testbench the way simrun.Start does (o.Hook and
// o.Coverage are the session's) and returns the session before the
// run's first statement. When the run ends at once (elaboration
// failed), the session has already ended (Outcome).
func Start(o simrun.Options) *Session {
	s := &Session{cancelled: o.Cancelled, bps: map[srcLine]bool{},
		bpOf: map[*sema.SeqStmt]bool{}}
	s.h = &hook{s: s}
	o.Hook, o.Coverage = s.h, false
	r := simrun.Start(o)
	if r.Done() {
		out := r.Outcome()
		s.out = &out
		return s
	}
	s.model = r.Outcome().Model
	s.lines = statementLines(o.Result, r.Design())
	s.cps = []checkpoint{{cp: r.Checkpoint()}}
	return s
}

// Model is the elaborated model (nil when elaboration failed).
func (s *Session) Model() *sim.Model { return s.model }

// Stop is the current stop (nil before the first one and after the
// run ended).
func (s *Session) Stop() *Stop { return s.stop }

// Outcome is the outcome of the run once it ended.
func (s *Session) Outcome() (simrun.Outcome, bool) {
	if s.out == nil {
		return simrun.Outcome{}, false
	}
	return *s.out, true
}

// Lines returns the lines of path (project-relative, slash) where a
// statement that can stop starts, in order: the sequential statements
// of processes and of the subprograms the interpreter runs, and the
// concurrent assignments, assertions and procedure calls.
func (s *Session) Lines(path string) []int { return s.lines[path] }

// SetBreakpoints replaces the breakpoints of path with lines and
// reports for each whether a statement starts on it (verified).
func (s *Session) SetBreakpoints(path string, lines []int) []bool {
	for k := range s.bps {
		if k.path == path {
			delete(s.bps, k)
		}
	}
	clear(s.bpOf)
	ok := make([]bool, len(lines))
	for i, l := range lines {
		_, ok[i] = slices.BinarySearch(s.lines[path], l)
		if ok[i] {
			s.bps[srcLine{path, l}] = true
		}
	}
	return ok
}

// isBP reports whether the statement st at src is on a breakpoint
// line.
func (s *Session) isBP(st *sema.SeqStmt, src sim.SrcRef) bool {
	if len(s.bps) == 0 {
		return false
	}
	bp, ok := s.bpOf[st]
	if !ok {
		bp = s.bps[srcLine{src.Path, src.Line}]
		s.bpOf[st] = bp
	}
	return bp
}

// pos is the statement count of the current stop (0 before the
// first).
func (s *Session) pos() int64 {
	if s.stop == nil {
		return 0
	}
	return s.stop.Event
}

// Entry moves to the run's first statement.
func (s *Session) Entry() Result {
	return s.forward(0, func(event) (string, bool) {
		return ReasonEntry, true
	})
}

// Goto moves to statement count n, forward or back: the state there
// is the state any move that reaches n gives.
func (s *Session) Goto(n int64) Result {
	if s.out != nil || n < 1 {
		return Result{Ended: s.out != nil}
	}
	return s.gotoEvent(n, ReasonGoto)
}

// Move runs move m from the current stop. Before the first stop,
// Continue runs to the first breakpoint and every other move goes to
// the first statement. After the run ended, it does nothing.
func (s *Session) Move(m Move) Result {
	if s.out != nil {
		return Result{Ended: true}
	}
	if s.stop == nil {
		if m == Continue {
			return s.forward(0, s.bpOnly)
		}
		return s.Entry()
	}
	p, d := s.stop.Process, s.stop.Depth
	if m == StepOut && d <= 1 {
		m = Next
	}
	step := func(e event) (string, bool) {
		ok := false
		switch m {
		case Next, StepBack:
			ok = e.proc == p && e.depth <= d
		case StepIn:
			ok = e.proc == p
		case StepOut:
			ok = e.proc == p && e.depth < d
		}
		if ok {
			return ReasonStep, true
		}
		return s.bpOnly(e)
	}
	switch m {
	case Continue:
		return s.forward(s.pos(), s.bpOnly)
	case Next, StepIn, StepOut:
		return s.forward(s.pos(), step)
	case StepBack:
		return s.backward(step)
	case ReverseContinue:
		return s.backward(s.bpOnly)
	}
	return Result{Stop: s.stop}
}

func (s *Session) bpOnly(e event) (string, bool) {
	return ReasonBreakpoint, e.bp
}

// forward moves to the first statement after count from that pred
// accepts.
func (s *Session) forward(from int64, pred func(event) (string,
	bool),
) Result {
	i := s.before(from)
	s.h.set(s.cps[i].events, modeForward, from, 0, pred)
	return s.replay(i)
}

// gotoEvent moves to statement count n, stopping with reason.
func (s *Session) gotoEvent(n int64, reason string) Result {
	i := s.before(n - 1)
	s.h.set(s.cps[i].events, modeGoto, 0, n, nil)
	s.h.reason = reason
	return s.replay(i)
}

// replay resumes checkpoint i under the hook as set and runs it until
// the hook halts it (a stop) or it ends.
func (s *Session) replay(i int) Result {
	r := s.cps[i].cp.Resume()
	cycles, ok := s.run(r)
	if !ok {
		return Result{Stop: s.stop, Cancelled: true}
	}
	if !s.h.halted {
		out := r.Outcome()
		s.out, s.stop = &out, nil
		return Result{Ended: true}
	}
	stop, reason := s.h.stop, s.h.reason
	s.keep(i, cycles-1)
	s.stop = stop
	return Result{Stop: stop, Reason: reason}
}

// backward moves to the last statement before the current stop that
// pred accepts, or to the first statement when there is none. It
// scans the checkpoint intervals from the latest back, each by a
// replay that halts at the interval's end.
func (s *Session) backward(pred func(event) (string, bool)) Result {
	hi := s.pos() // the scan looks at counts below hi
	for i := s.before(hi - 1); i >= 0 && hi > 1; i-- {
		lo := s.cps[i].events
		if lo >= hi-1 {
			continue // nothing between lo and hi
		}
		s.h.set(lo, modeScan, lo, hi, pred)
		if _, ok := s.run(s.cps[i].cp.Resume()); !ok {
			return Result{Stop: s.stop, Cancelled: true}
		}
		if s.h.last > 0 {
			return s.gotoEvent(s.h.last, s.h.lastReason)
		}
		hi = lo + 1
	}
	return s.gotoEvent(1, ReasonEntry)
}

// run steps r one kernel cycle at a time until it is done, polling
// Cancelled every simrun.Slice cycles. It returns the cycles run and
// false when cancelled.
func (s *Session) run(r *simrun.Running) (int64, bool) {
	var n int64
	for !r.Done() {
		if n%simrun.Slice == 0 && s.cancelled != nil && s.cancelled() {
			return n, false
		}
		r.Step(1)
		n++
	}
	return n, true
}

// before returns the latest checkpoint with at most n statements run.
func (s *Session) before(n int64) int {
	for i := len(s.cps) - 1; i > 0; i-- {
		if s.cps[i].events <= n {
			return i
		}
	}
	return 0
}

// keep adds the checkpoint n cycles after checkpoint i (the start of
// the cycle a replay from i halted in), so the next move starts
// there.
func (s *Session) keep(i int, n int64) {
	if n <= 0 {
		return
	}
	base := s.cps[i]
	cycle := base.cycle + n
	j, found := slices.BinarySearchFunc(s.cps, cycle, func(c checkpoint,
		cy int64,
	) int {
		return int(c.cycle - cy)
	})
	if found {
		return
	}
	r := base.cp.Resume()
	s.h.set(base.events, modeCount, 0, 0, nil)
	for k := int64(0); k < n; k++ {
		if k%simrun.Slice == 0 && s.cancelled != nil && s.cancelled() {
			return
		}
		if r.Step(1) {
			return // cannot happen: the replay went further
		}
	}
	cp := checkpoint{cp: r.Checkpoint(), events: s.h.count, cycle: cycle}
	s.cps = slices.Insert(s.cps, j, cp)
	if len(s.cps) > MaxCheckpoints {
		kept := s.cps[:1]
		for k := 2; k < len(s.cps); k += 2 {
			kept = append(kept, s.cps[k])
		}
		s.cps = kept
	}
}

// statementLines returns, by path, the lines where a statement that
// stops starts: those of the units coverage covers (coverage.Units).
func statementLines(r *sema.Result, d *elab.Design) map[string][]int {
	set := map[string]map[int]bool{} // lookup only
	add := func(f *sema.File, n cst.Node) {
		if n.IsZero() {
			return
		}
		l, _ := f.Lines.LineCol(n.Span().Start)
		if set[f.Path] == nil {
			set[f.Path] = map[int]bool{}
		}
		set[f.Path][l] = true
	}
	var seq func(f *sema.File, ss []*sema.SeqStmt)
	seq = func(f *sema.File, ss []*sema.SeqStmt) {
		for _, st := range ss {
			add(f, st.Node)
			for _, br := range st.Branches {
				seq(f, br.Body)
			}
			seq(f, st.Else)
			for _, a := range st.Alts {
				seq(f, a.Body)
			}
			seq(f, st.Body)
		}
	}
	var conc func(f *sema.File, ss []*sema.Stmt)
	conc = func(f *sema.File, ss []*sema.Stmt) {
		for _, st := range ss {
			switch st.Kind {
			case sema.StmtProcess:
				seq(f, st.Seq)
				continue
			case sema.StmtOther:
				switch st.Node.Kind() {
				case cst.KindConcurrentSimpleSignalAssign,
					cst.KindConcurrentConditionalSignalAssign,
					cst.KindConcurrentSelectedSignalAssign,
					cst.KindConcurrentAssertionStmt,
					cst.KindConcurrentProcedureCallStmt:
					add(f, st.Node)
				}
				continue
			}
			conc(f, st.Body)
			for _, a := range st.Alts {
				conc(f, a.Body)
			}
		}
	}
	if d != nil {
		for _, u := range coverage.Units(r, d.Top) {
			f := u.File
			if f == nil || f.Lines == nil {
				continue
			}
			conc(f, u.Body)
			for _, x := range u.Decls {
				if x.Kind.IsSubprogram() &&
					x.Node.Kind() == cst.KindSubprogramBody &&
					elab.Interpreted(x) {
					seq(f, x.Seq)
				}
			}
		}
	}
	out := make(map[string][]int, len(set))
	for _, p := range slices.Sorted(maps.Keys(set)) {
		out[p] = slices.Sorted(maps.Keys(set[p]))
	}
	return out
}
