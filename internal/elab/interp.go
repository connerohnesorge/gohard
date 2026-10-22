package elab

// interp.go is the sequential interpreter (#35): the behaviour of every
// process statement and concurrent statement of an elaborated design,
// executed from sema's typed statement trees. Expressions are
// evaluated by the shared evaluator (sema.Evaluator over internal/eval)
// through a sema.Dynamic (interp_dyn.go) that supplies signal and
// variable values, signal attributes, impure and built-in calls; pure
// functions, the ieee package bodies included, are interpreted by that
// evaluator exactly as static evaluation does.
//
// A process is a resumable machine: a stack of frames (the process,
// then one per procedure called), each holding its variables and a
// stack of statement-list cursors. A wait statement anywhere, in a
// procedure too, returns to the kernel with the stack intact; Resume
// continues from it. Functions run to completion inside the expression
// that calls them. Everything charges the run's step budget. The
// machine is a value: Clone deep-copies the stack and the design-wide
// world (shared variables, the heap of allocated objects, files), so
// a forked kernel continues identically.

import (
	"errors"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/token"
)

// InterpOptions configure the sequential interpreter.
type InterpOptions struct {
	// FS reads the files a design opens for reading that it did not
	// write itself, and receives every file it writes when the file
	// is closed (or at the end of the run, never: unclosed files stay
	// in memory). Nil keeps all file contents in memory.
	FS env.FS
	// Output receives each line written to std.textio.OUTPUT (nil:
	// dropped).
	Output func(line string)
	// Input is the content of std.textio.INPUT.
	Input []byte
	// Env is the world the VHDL-2019 std.env natives see (#18). The
	// zero value is a deterministic, empty world.
	Env EnvOptions
}

// EnvOptions is the world of the VHDL-2019 std.env natives (#18):
// tool and version queries, the deterministic clock, environment
// variables and directories. A run reads the outside world only
// through the doors (Clock, Getenv, FS), and only when Real is set;
// otherwise every answer is a function of the other fields and the
// simulated time.
type EnvOptions struct {
	// Standard is the run's VHDL standard. VHDL_VERSION answers the
	// standard of the calling file (the value conditional analysis
	// sees); Standard is used where no file is known.
	Standard token.Standard
	// ToolVersion is TOOL_VERSION, the same value conditional
	// analysis sees.
	ToolVersion string
	// Epoch is simulation.env.epoch: EPOCH, GMTIME and LOCALTIME
	// answer it plus the simulated time, in UTC.
	Epoch time.Time
	// Vars are simulation.env.vars (and --env K=V), the variables
	// GETENV sees when Real is false. Read by key only.
	Vars map[string]string
	// Root is the project root; RunDir the run's own directory (under
	// .gohard/run/), where relative directory operations happen.
	Root   string
	RunDir string
	// Real (--real-env) lets the natives read the real world through
	// the doors below; the run is then marked non-reproducible.
	Real bool
	// Clock is the wall clock read when Real is set.
	Clock env.Clock
	// Getenv reads real environment variables when Real is set.
	Getenv env.Getenv
	// FS is the door of the directory and file natives (DIR_*).
	FS env.FS
}

// Interpret builds the code of process p with default options: no
// file system, OUTPUT dropped, an empty INPUT.
// Every process of d shares one world (shared variables, protected
// objects, the heap and files).
func Interpret(d *Design, p *Proc) (sim.Code, error) {
	if d.interp == nil {
		d.interp = newWorld(d, InterpOptions{})
	}
	return codeIn(d.interp, p)
}

// Interpreter returns an Options.Code hook running processes with o.
// Statements it does not execute (PSL directives) get no code, so
// another hook may supply it.
func Interpreter(o InterpOptions) func(*Design, *Proc) (sim.Code, error) {
	worlds := map[*Design]*world{} // lookup only
	return func(d *Design, p *Proc) (sim.Code, error) {
		w, ok := worlds[d]
		if !ok {
			w = newWorld(d, o)
			worlds[d] = w
		}
		return codeIn(w, p)
	}
}

// codeIn builds the code of p running in world w.
func codeIn(w *world, p *Proc) (sim.Code, error) {
	prog, err := w.program(p)
	if err != nil || prog == nil {
		return nil, err
	}
	m := &machine{prog: prog, w: w, idx: p.Index}
	m.ev = p.Ev.WithDynamic(m)
	return &code{m: m}, nil
}

// code implements sim.Code and sim.Condition.
type code struct{ m *machine }

// Resume implements sim.Code.
func (c *code) Resume(ctx *sim.Ctx) (sim.Wait, error) {
	m := c.m
	m.ctx = ctx
	defer func() { m.ctx, m.b = nil, nil }()
	return metered(ctx.Budget(), func(b *eval.Budget) (sim.Wait, error) {
		m.b = b
		w, err := m.resume()
		if m.native(err) {
			return sim.Wait{}, nil
		}
		return w, err
	})
}

// Close implements sim.Closer: the files still open at the end of the
// run are closed (the first process closes those of the whole world).
func (c *code) Close(*sim.Ctx) error {
	return c.m.w.closeAll()
}

// Until implements sim.Condition: the condition of the current wait,
// and, for a wait on parts of signals, an event on one of them.
func (c *code) Until(ctx *sim.Ctx) (bool, error) {
	m := c.m
	m.ctx = ctx
	defer func() { m.ctx, m.b = nil, nil }()
	return metered(ctx.Budget(), func(b *eval.Budget) (bool, error) {
		m.b = b
		ok, err := m.until()
		if m.native(err) {
			return false, nil
		}
		return ok, err
	})
}

// native reports err if it is a call of a built-in subprogram without
// a native (*nativeError): the diagnostic unsupported/construct
// (please report) at the statement, or else at the process. The
// process ends and the run fails after the current phase
// (sim.Ctx.Abort); the diagnostic makes the exit code 2.
func (m *machine) native(err error) bool {
	var ne *nativeError
	if err == nil || !errors.As(err, &ne) || m.ctx == nil {
		return false
	}
	var src sim.SrcRef
	if i := m.ctx.Process(); i >= 0 && i < len(m.w.d.Model.Processes) {
		src = m.w.d.Model.Processes[i].Src
	}
	var le *locError
	if errors.As(err, &le) && le.src.Path != "" {
		src = le.src
	}
	m.ctx.Abort(src, diag.NewVariant(diag.UnsupportedConstruct,
		diag.PleaseReport, ne.name))
	m.dead, m.wait = true, nil
	return true
}

// EvalStepsPerStep is the number of evaluator steps (one per IR node
// lowered or evaluated, per interpreted statement and call) that make
// one kernel step of simulation.step_budget. The interpreter and the
// resolution functions run under a scratch budget of the steps left
// and charge the run's budget the rounded-up quotient, so every
// evaluation still counts and a run without end still ends.
const EvalStepsPerStep = 16

// metered runs f under a scratch budget and charges kb for it.
func metered[T any](kb *eval.Budget, f func(*eval.Budget) (T, error),
) (T, error) {
	lim := int64(0)
	if l := kb.Limit(); l > 0 {
		lim = max(1, (l-kb.Used()+1)*EvalStepsPerStep)
	}
	sb := eval.NewBudget(lim)
	sb.SetObserver(kb.Observer())
	v, err := f(sb)
	n := (sb.Used() + EvalStepsPerStep - 1) / EvalStepsPerStep
	if serr := kb.Step(n); serr != nil && err == nil {
		err = serr
	}
	return v, err
}

// Clone implements sim.Code.
func (c *code) Clone() sim.Code {
	return &code{m: c.m.clone()}
}

// machine is the state of one process.
type machine struct {
	prog *program
	w    *world
	idx  int // the process index
	ev   *sema.Evaluator
	ctx  *sim.Ctx
	b    *eval.Budget

	frames  []frame
	started bool
	dead    bool
	// wait is the wait statement the process is suspended in.
	wait *suspended
	// sync counts the functions running: a wait inside one is an
	// error.
	sync int
	// lastRet is the result of the function frame that just returned.
	lastRet      eval.Value
	lastReturned bool
}

// suspended is a pending wait.
type suspended struct {
	parts []sim.Target // non-whole sensitivity, filtered in Until
	cond  *sema.Expr
	ev    *sema.Evaluator // evaluates cond (the waiting frame's view)
}

// frame is one activation: the process or a subprogram call.
type frame struct {
	sub  *sema.Decl // the subprogram body; nil for the process
	vars map[*sema.Decl]slot
	// obj is the protected object of a method call (0: none).
	obj   int64
	conts []cont
	// fn marks a function call; ret its result once returned.
	fn       bool
	ret      eval.Value
	returned bool
	// outs are written back to the caller when a procedure returns.
	outs []outArg
	call *sema.Expr
}

// slot is one object of a frame.
type slot struct {
	v eval.Value
	t *eval.Type
	// sig is the signal a signal parameter denotes.
	sig *sigPart
}

// outArg is an out or inout variable parameter: its final value is
// assigned to dst.
type outArg struct {
	formal *sema.Decl
	dst    place
}

// cont is a cursor in a statement list; loop cursors repeat.
type cont struct {
	ss   []*node
	i    int
	loop *node
	// For loops: the range and the iteration.
	r eval.Range
	k int64
}

func (m *machine) clone() *machine {
	c := *m
	c.ctx, c.b = nil, nil
	c.w = m.w.fork(m.idx)
	c.ev = m.ev.WithDynamic(&c)
	c.frames = make([]frame, len(m.frames))
	for i, f := range m.frames {
		f.vars = maps.Clone(f.vars)
		f.conts = slices.Clone(f.conts)
		f.outs = slices.Clone(f.outs)
		c.frames[i] = f
	}
	if m.wait != nil {
		w := *m.wait
		w.parts = slices.Clone(w.parts)
		if w.ev == m.ev {
			w.ev = c.ev
		} else {
			w.ev = w.ev.WithDynamic(&c)
		}
		c.wait = &w
	}
	return &c
}

// errDead is returned by a machine that already failed.
var errDead = errors.New("the process stopped after an error")

func (m *machine) resume() (sim.Wait, error) {
	if m.dead {
		return sim.Wait{}, nil
	}
	if !m.started {
		m.started = true
		if err := m.start(); err != nil {
			m.dead = true
			return sim.Wait{}, err
		}
	}
	m.wait = nil
	wt, susp, err := m.run(0)
	if err != nil {
		m.dead = true
		return sim.Wait{}, err
	}
	if !susp {
		m.dead = true
		return sim.Wait{}, nil
	}
	return wt, nil
}

func (m *machine) until() (bool, error) {
	if m.dead {
		return false, nil
	}
	w := m.wait
	if w == nil {
		return true, nil
	}
	if len(w.parts) > 0 {
		hit, err := m.partEvent(w.parts)
		if err != nil || !hit {
			return false, err
		}
	}
	if w.cond == nil {
		return true, nil
	}
	return w.ev.Cond(w.cond, m.b)
}

func (m *machine) partEvent(ps []sim.Target) (bool, error) {
	for _, p := range ps {
		hit, err := m.ctx.PartEvent(p)
		if err != nil || hit {
			return hit, err
		}
	}
	return false, nil
}

// start elaborates the process's declarations (LRM 14.4.2) and enters
// its statement part.
func (m *machine) start() error {
	m.frames = []frame{{vars: map[*sema.Decl]slot{}}}
	if r := m.prog.region; r != nil {
		if err := m.declare(r); err != nil {
			return err
		}
	}
	m.frames[0].conts = []cont{{ss: m.prog.body}}
	return nil
}

// run executes statements until the process suspends (susp) or the
// frame stack is back to base frames (a function call returned).
func (m *machine) run(base int) (w sim.Wait, susp bool, err error) {
	for len(m.frames) > base {
		fi := len(m.frames) - 1
		f := &m.frames[fi]
		if len(f.conts) == 0 {
			if fi == 0 {
				w, susp, err := m.endOfProcess()
				if err != nil || susp {
					return w, susp, err
				}
				continue
			}
			if err := m.leave(); err != nil {
				return sim.Wait{}, false, err
			}
			continue
		}
		c := &f.conts[len(f.conts)-1]
		if c.i >= len(c.ss) {
			if err := m.endCont(); err != nil {
				return sim.Wait{}, false, err
			}
			continue
		}
		n := c.ss[c.i]
		c.i++
		if err := m.b.Step(1); err != nil {
			return sim.Wait{}, false, err
		}
		w, susp, err := m.exec(n)
		if err != nil {
			return sim.Wait{}, false, located(n, err)
		}
		if susp {
			return w, true, nil
		}
	}
	return sim.Wait{}, false, nil
}

// endOfProcess runs at the end of the process's statement part: a
// process with a sensitivity list (or a concurrent statement) waits on
// it, then starts over (LRM 11.3).
func (m *machine) endOfProcess() (sim.Wait, bool, error) {
	f := &m.frames[0]
	f.conts = append(f.conts, cont{ss: m.prog.body})
	if !m.prog.sensitive {
		if len(m.prog.body) == 0 {
			return sim.Wait{}, true, nil // nothing to do: wait forever
		}
		return sim.Wait{}, false, nil
	}
	return m.waitOn(m.prog.sens, nil, nil, m.ev)
}

// endCont ends the innermost statement list: a loop iterates.
func (m *machine) endCont() error {
	f := &m.frames[len(m.frames)-1]
	c := &f.conts[len(f.conts)-1]
	if l := c.loop; l != nil {
		s := l.s
		switch {
		case s.Param != nil:
			c.k++
			if c.k < c.r.Len() {
				m.setParam(f, l, c.r.At(c.k))
				c.i = 0
				return nil
			}
			delete(f.vars, s.Param)
		case s.Cond != nil:
			ok, err := m.ev.Cond(s.Cond, m.b)
			if err != nil {
				return located(l, err)
			}
			if ok {
				c.i = 0
				return nil
			}
		default:
			c.i = 0
			return nil
		}
	}
	f.conts = f.conts[:len(f.conts)-1]
	return nil
}

func (m *machine) setParam(f *frame, l *node, v int64) {
	sl := f.vars[l.s.Param]
	sl.v = eval.Value{T: sl.t, I: v}
	f.vars[l.s.Param] = sl
}

// located adds the statement's place to a run-time error.
func located(n *node, err error) error {
	if err == nil || n == nil {
		return err
	}
	var le *locError
	if errors.As(err, &le) || eval.CodeOf(err) == eval.ErrCodeBudget {
		return err
	}
	return &locError{err: err, src: n.src}
}

// locError is a run-time error at a statement.
type locError struct {
	err error
	src sim.SrcRef
}

func (e *locError) Error() string {
	if e.src.Path == "" {
		return e.err.Error()
	}
	return e.src.Path + ":" + strconv.Itoa(e.src.Line) + ":" +
		strconv.Itoa(e.src.Col) + ": " + e.err.Error()
}

func (e *locError) Unwrap() error { return e.err }
