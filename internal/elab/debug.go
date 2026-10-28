package elab

// debug.go is the interpreter's side of the simulation debugger
// (#68): a DebugHook is told where each statement a process runs, can
// read the state there through At, and can halt the run before the
// statement executes. The debugger never keeps a run suspended inside
// a process: it halts a replay at the statement it wants and throws
// the halted kernel away (internal/simdbg).

import (
	"cmp"
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/sim"
)

// ErrHalted is the error of a process whose DebugHook halted it: the
// kernel ends the run with it as a run-time error, so a halted kernel
// must be thrown away.
var ErrHalted = errors.New("halted by the debugger")

// DebugHook is a sema.StmtHook that also wants to know where each
// statement of the interpreter runs (#68). The interpreter calls
// ProcStmt instead of Stmt for the statements of processes,
// concurrent statements and the subprograms they call that it runs
// itself (procedures, impure functions and functions with signal,
// file, protected, access or out parameters); the pure functions the
// evaluator interprets keep calling Stmt. Like any StmtHook it
// changes no value, trace or step count as long as it never halts.
type DebugHook interface {
	sema.StmtHook
	// ProcStmt is called when the statement s starts, once its step is
	// charged and before it executes; at is valid during the call
	// only. Returning true halts the run there: the process fails
	// with ErrHalted.
	ProcStmt(s *sema.SeqStmt, at *At) bool
}

// At is the place and state of the statement a DebugHook is told of.
type At struct {
	m *machine
	n *node
}

// Process is the index of the running process in Model.Processes.
func (a *At) Process() int { return a.m.idx }

// Depth is the number of frames of the running process: 1 in the
// process's own statements, one more per subprogram call.
func (a *At) Depth() int { return len(a.m.frames) }

// Src is the place of the statement. A statement of a bundled
// std/ieee file has an empty Path and its bundle path in Bundled
// (sim.SrcRef), never a project path.
func (a *At) Src() sim.SrcRef { return a.n.debugSrc() }

// debugSrc is the place of n as a DebugHook is told of it (At.Src).
func (n *node) debugSrc() sim.SrcRef {
	s := n.src
	if n.bundled {
		s.Path, s.Bundled = "", s.Path
	}
	return s
}

// Now is the simulated time of the statement.
func (a *At) Now() sim.Time {
	if a.m.ctx == nil {
		return 0
	}
	return a.m.ctx.Now()
}

// Delta is the delta cycle of the statement.
func (a *At) Delta() uint32 {
	if a.m.ctx == nil {
		return 0
	}
	return a.m.ctx.Delta()
}

// Frame is one activation of a process: its own statement part or a
// subprogram call.
type Frame struct {
	// Name is the process's label (the last part of its path) or the
	// subprogram's name.
	Name string
	// Src is the statement the frame is at: the statement starting in
	// the innermost frame of the running process, the call in a
	// caller, the wait statement of a suspended process. A process
	// waiting on its sensitivity list is at its process statement. A
	// statement of a bundled std/ieee file is named as At.Src names it.
	Src sim.SrcRef
	// Vars are the frame's objects (constants, variables, parameters,
	// loop parameters) in declaration order.
	Vars []Value
}

// Value is one object, its type and its value, the value written as
// messages write it (eval.Value.Image).
type Value struct {
	Name, Type, Value string
}

// Frames returns the call stack of process p, innermost frame first:
// the running process's at this statement, another one's where it is
// suspended. A process that has not started or has ended has none;
// ok is false for a process the interpreter does not run (a PSL
// directive, a stand-in model).
func (a *At) Frames(p int) (frames []Frame, ok bool) {
	ms := a.m.w.gc.machines
	if p < 0 || p >= len(ms) || ms[p] == nil {
		return nil, false
	}
	o := ms[p]
	if p == a.m.idx {
		o = a.m
	}
	if o.dead {
		return nil, true
	}
	procSrc := a.m.w.d.Model.Processes[p].Src
	out := make([]Frame, 0, len(o.frames))
	for i := len(o.frames) - 1; i >= 0; i-- {
		f := &o.frames[i]
		fr := Frame{Src: procSrc}
		if f.sub != nil {
			fr.Name = f.sub.Text
		} else {
			path := a.m.w.d.Model.Processes[p].Path
			fr.Name = path[strings.LastIndexByte(path, '.')+1:]
		}
		if n := len(f.conts); n > 0 {
			if c := f.conts[n-1]; c.i > 0 && c.i <= len(c.ss) {
				fr.Src = c.ss[c.i-1].debugSrc()
			}
		}
		fr.Vars = a.vars(f)
		out = append(out, fr)
	}
	return out, true
}

// vars returns the objects of frame f in declaration order.
func (a *At) vars(f *frame) []Value {
	ds := slices.SortedFunc(maps.Keys(f.vars), func(x,
		y *sema.Decl,
	) int {
		return cmp.Or(cmp.Compare(x.Span.Start, y.Span.Start),
			cmp.Compare(x.Text, y.Text))
	})
	out := make([]Value, 0, len(ds))
	for _, d := range ds {
		sl := f.vars[d]
		v := sl.v
		if sl.sig != nil {
			// A signal parameter: the current value of its actual,
			// read through the running process.
			if sv, err := a.m.sigValue(*sl.sig); err == nil {
				v = sv
			}
		}
		t := sl.t
		if t == nil {
			t = v.T
		}
		out = append(out, Value{Name: d.Text, Type: typeText(t),
			Value: imageOf(v)})
	}
	return out
}

// Signals returns every signal of the design but the implicit ones
// ('stable, 'quiet, 'transaction, 'delayed), in model order, by path.
func (a *At) Signals() []Value {
	sigs := a.m.w.d.Model.Signals
	out := make([]Value, 0, len(sigs))
	for i := range sigs {
		s := &sigs[i]
		if s.Implicit != sim.ImplicitNone {
			continue
		}
		v := s.Init
		if a.m.ctx != nil {
			v = a.m.ctx.Value(sim.SigID(i))
		}
		out = append(out, Value{Name: s.Path, Type: typeText(s.Type),
			Value: imageOf(v)})
	}
	return out
}

// typeText names the type t (its base type's name when it is an
// anonymous subtype).
func typeText(t *eval.Type) string {
	for t != nil && t.Name == "" && t.Base != nil {
		t = t.Base
	}
	if t == nil {
		return ""
	}
	return strings.ToLower(t.Name)
}

// imageOf is v.Image (strings and bit vectors quoted, other
// composites as aggregates), or "" for a value of no type.
func imageOf(v eval.Value) string {
	if v.T == nil {
		return ""
	}
	return v.Image()
}

// Interpreted reports whether the interpreter runs the calls of the
// subprogram x (a declaration or a body) itself, so a DebugHook is
// told of their statements with ProcStmt: procedures and the functions
// runsHere names. The evaluator runs the other functions.
func Interpreted(x *sema.Decl) bool {
	if x.Spec != nil {
		x = x.Spec
	}
	switch x.Kind {
	case sema.DeclProcedure:
		return true
	case sema.DeclFunction:
		return runsHere(x)
	}
	return false
}
