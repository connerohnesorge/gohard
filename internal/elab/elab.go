// Package elab lowers an analyzed design (internal/sema) into the
// simulation model of internal/sim (#35): every signal and port of
// every instance of one testbench with its evaluated subtype, initial
// value and resolution, ports collapsed onto their actuals (mode-view
// ports with a mode per element, #17), and one process per process
// statement or concurrent statement, with instance paths such as
// tb.dut.u0 that match internal/elab/hier, the language server and the
// trace.
//
// The hierarchy comes from hier.Build; elab adds what the simulator
// needs. Process behaviour is not built here: Options.Code is the hook
// the sequential interpreter fills (Proc carries the instance, the
// statement and the instance's evaluator), so elaboration stays one
// pass over the hierarchy whatever executes the processes. Source
// references are file plus byte span (sim.SrcRef), never tree IDs.
//
// Every evaluation charges the run's step budget (Options.Budget,
// simulation.step_budget), hierarchy evaluation included, so an
// elaboration that does not fit gives sim/budget-exceeded before the
// first cycle. Elaborate is a pure function of the analysis and the
// options: signals and processes come in hierarchy (declaration)
// order, which is the kernel's process sequence for seed 0.
//
// Hooks for later work: Proc.Stmt kinds the interpreter does not run
// yet (PSL directives, concurrent procedure calls) simply get no code
// from Options.Code and are listed in Design.Missing; stand-in models
// (simulation.models) mark their processes sim.Process.Modeled; force
// and release are kernel operations (sim.Ctx.Force); external names
// resolve through Design.Lookup by path.
package elab

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/elab/hier"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/token"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// Options configure Elaborate.
type Options struct {
	// Top names the testbench entity or configuration ("tb",
	// "work.tb").
	Top string
	// Budget is the run's step budget (simulation.step_budget); nil
	// means a fresh budget of StepBudget steps (0: unlimited).
	Budget     *eval.Budget
	StepBudget int64
	// Code builds the behaviour of a process; nil, or a nil Code,
	// leaves the process without code (Design.Missing lists them).
	Code func(d *Design, p *Proc) (sim.Code, error)
	// Env is the world the std.env functions called during
	// elaboration see (generics, signal initial values and design
	// constants that call TOOL_NAME, GETENV, FILE_LINE, ...): the
	// interpreter's InterpOptions.Env of the same run.
	Env EnvOptions
}

// Proc is one elaborated process: a process statement or a concurrent
// statement (assignment, assertion, procedure call, PSL directive)
// that runs as one.
type Proc struct {
	// Index is the process's index in Model.Processes.
	Index int
	Path  string
	Inst  *hier.Instance
	Stmt  *sema.Stmt
	// Ev is the instance's evaluator: generics and generate parameters
	// bound.
	Ev   *sema.Evaluator
	File *sema.File
}

// Design is an elaborated testbench.
type Design struct {
	Model *sim.Model
	Top   *hier.Instance
	Procs []*Proc
	// Missing lists the processes Options.Code gave no code, by path.
	Missing []string
	Budget  *eval.Budget

	r       *sema.Result
	sigs    map[sigKey]sim.SigID // lookup only
	byPath  map[string]sim.SigID // lookup only
	parent  map[*hier.Instance]*hier.Instance
	res     []resolverEntry
	modeled map[*hier.Instance]bool // lookup only
	checked []*sema.Decl            // stand-ins whose ports were checked
	// modeledInsts are the entity instances running stand-in code.
	modeledInsts []*hier.Instance
	diags        []diag.Diagnostic
	stopped      bool
	// interp is the world Interpret's processes share.
	interp *world
	// varPorts binds each variable port (VHDL-2019 shared-variable
	// ports of protected type) of an entity instance to the shared
	// object it denotes; lookup only. ownPorts are the ports that are
	// objects of their own (open, or of the top), in elaboration
	// order.
	varPorts map[sigKey]sharedKey
	ownPorts []sharedKey
}

type sigKey struct {
	in *hier.Instance
	d  *sema.Decl
}

// Elaborate builds the design of the testbench o.Top. The diagnostics
// are elaboration-time simulation diagnostics (sim/budget-exceeded,
// sim/runtime-error, unsupported/construct); static design errors are
// sema's and hier's and are not repeated.
func Elaborate(r *sema.Result, o Options) (*Design, []diag.Diagnostic) {
	b := o.Budget
	if b == nil {
		b = eval.NewBudget(o.StepBudget)
	}
	d := &Design{Model: &sim.Model{}, Budget: b, r: r,
		sigs: map[sigKey]sim.SigID{}, byPath: map[string]sim.SigID{},
		parent:   map[*hier.Instance]*hier.Instance{},
		modeled:  map[*hier.Instance]bool{},
		varPorts: map[sigKey]sharedKey{}}
	steps := b.Limit()
	if steps <= 0 {
		steps = eval.EditorSteps
	}
	h := hier.Build(r, hier.Options{Tops: []string{o.Top}, Steps: steps,
		TotalSteps: steps, Natives: d.natives(o.Env),
		Reporter: elabReporter{d: d}})
	if d.charge(h.Steps, sim.SrcRef{}) {
		return d, d.diags
	}
	if len(h.Tops) == 0 {
		return nil, []diag.Diagnostic{diag.New(diag.SimUnknownTestbench,
			o.Top, "none")}
	}
	top := h.Tops[0]
	d.Top = top
	d.Model.Testbench = top.Path
	d.Model.Src = d.declSrc(top.Entity)
	if g := unboundGeneric(top); g != nil {
		// No actual can bind it: the entity is not a testbench as it
		// stands (the design's error, as NVC's "missing value for
		// generic").
		d.runtime(d.declSrc(g), top.Path, errors.New("the generic "+
			g.Text+" of the testbench "+top.Path+" has no default"))
		return d, d.diags
	}
	d.instance(top)
	if d.stopped {
		return d, d.diags
	}
	d.markModeled()
	for _, p := range d.Procs {
		var code sim.Code
		if o.Code != nil {
			c, err := o.Code(d, p)
			if err != nil {
				d.runtime(d.Model.Processes[p.Index].Src, p.Path, err)
				continue
			}
			code = c
		}
		if code == nil {
			d.Missing = append(d.Missing, p.Path)
			continue
		}
		d.Model.Processes[p.Index].Code = code
	}
	return d, d.diags
}

// unboundGeneric returns a generic constant of the top instance top
// that has no default, nil if there is none.
func unboundGeneric(top *hier.Instance) *sema.Decl {
	for _, g := range top.Generics {
		if x := g.Decl; x.Kind == sema.DeclGeneric && x.Value == nil {
			return x
		}
	}
	return nil
}

// Lookup returns the signal with the hierarchical path p (external
// names, the language server's value lookup).
func (d *Design) Lookup(p string) (sim.SigID, bool) {
	id, ok := d.byPath[strings.ToLower(p)]
	return id, ok
}

// Signal returns the signal of the declaration x (a signal or port)
// as seen from instance in: in's own, else an enclosing instance's
// (blocks and generates see their architecture's signals).
func (d *Design) Signal(in *hier.Instance, x *sema.Decl) (sim.SigID,
	bool,
) {
	for ; in != nil; in = d.parent[in] {
		if id, ok := d.sigs[sigKey{in, x}]; ok {
			return id, true
		}
		if in.Kind == hier.KindEntity {
			// Names inside an entity never reach the instantiating
			// architecture.
			break
		}
	}
	return 0, false
}

// VarPort returns the object that the variable port x (a VHDL-2019
// shared-variable port of protected type, LRM 6.5.2) denotes as seen
// from instance in (the entity instance declaring x, or a block or
// generate inside it): the shared variable of its actual, named by
// its declaring instance (nil for a package) and declaration. A port
// whose actual is a variable port of the enclosing entity is followed
// to that port's object; an open port, or a port of the top, is an
// object of its own: (the entity instance, x).
func (d *Design) VarPort(in *hier.Instance, x *sema.Decl) (*hier.Instance,
	*sema.Decl, bool,
) {
	k, ok := d.varPortKey(in, x)
	return k.in, k.d, ok
}

// varPortKey is VarPort's shared object key.
func (d *Design) varPortKey(in *hier.Instance, x *sema.Decl) (sharedKey,
	bool,
) {
	for ; in != nil; in = d.parent[in] {
		if in.Kind == hier.KindEntity {
			k, ok := d.varPorts[sigKey{in, x}]
			return k, ok
		}
	}
	return sharedKey{}, false
}

// Parent returns the instance enclosing in (nil for the top).
func (d *Design) Parent(in *hier.Instance) *hier.Instance {
	return d.parent[in]
}

// charge counts n elaboration steps; it reports whether the budget ran
// out (sim/budget-exceeded at time 0).
func (d *Design) charge(n int64, src sim.SrcRef) bool {
	if d.stopped {
		return true
	}
	if err := d.Budget.Step(n); err != nil {
		d.stopped = true
		dg := diag.New(diag.SimBudgetExceeded, "step",
			strconv.FormatInt(d.Budget.Limit(), 10)+" steps", "0 fs")
		if src.Path != "" {
			dg = dg.At(src.Path, src.Line, src.Col)
		}
		d.diags = append(d.diags, dg)
		return true
	}
	return false
}

// runtime reports an error of elaboration as sim/runtime-error, or,
// when it is a legal construct GoHard does not evaluate yet
// (*sema.UnsupportedError anywhere in its chain), as
// unsupported/construct (please report, exit 2).
func (d *Design) runtime(src sim.SrcRef, inst string, err error) {
	if what, ok := sema.UnsupportedWhat(err); ok {
		d.unsupported(src, what)
		return
	}
	dg := diag.New(diag.SimRuntimeError, err.Error(), inst, "0 fs")
	if src.Path != "" {
		dg = dg.At(src.Path, src.Line, src.Col)
	}
	d.diags = append(d.diags, dg)
}

func (d *Design) unsupported(src sim.SrcRef, what string) {
	dg := diag.New(diag.UnsupportedConstruct, what)
	if src.Path != "" {
		dg = dg.At(src.Path, src.Line, src.Col)
	}
	d.diags = append(d.diags, dg)
}

func srcOf(f *sema.File, sp cst.Range) sim.SrcRef {
	if f == nil {
		return sim.SrcRef{}
	}
	l, c := f.Lines.LineCol(sp.Start)
	return sim.SrcRef{Path: f.Path, Start: sp.Start, End: sp.End, Line: l,
		Col: c}
}

func (d *Design) declSrc(x *sema.Decl) sim.SrcRef {
	if x == nil {
		return sim.SrcRef{}
	}
	return srcOf(x.File, x.Span)
}

// instance elaborates in and everything below it.
func (d *Design) instance(in *hier.Instance) {
	if d.stopped {
		return
	}
	d.standIn(in)
	switch in.Kind {
	case hier.KindEntity:
		unit := in.Entity.Text
		if in.Arch != nil {
			unit += "(" + string(in.Arch.Name) + ")"
		}
		d.Model.Scopes = append(d.Model.Scopes, trace.Scope{
			Path: in.Path, Kind: trace.Instance,
			Unit: strings.ToLower(unit)})
		eu := in.Entity.Unit
		d.ports(in)
		d.declarations(in, in.Entity.Region)
		d.body(in, eu.Body, eu.File)
		if in.Arch != nil {
			d.declarations(in, in.Arch.Region)
			d.body(in, in.Arch.Body, in.Arch.File)
		}
	case hier.KindBlock:
		d.Model.Scopes = append(d.Model.Scopes, trace.Scope{
			Path: in.Path, Kind: trace.Block})
		d.declarations(in, in.Stmt.Region)
		d.body(in, in.Stmt.Body, d.fileOf(in))
	case hier.KindGenerate:
		kind := trace.ForGenerate
		switch in.Stmt.Kind {
		case sema.StmtIfGenerate:
			kind = trace.IfGenerate
		case sema.StmtCaseGenerate:
			kind = trace.CaseGenerate
		}
		d.Model.Scopes = append(d.Model.Scopes, trace.Scope{
			Path: in.Path, Kind: kind})
		if in.Alt != nil {
			d.declarations(in, in.Alt.Region)
			d.body(in, in.Alt.Body, d.fileOf(in))
		} else {
			d.declarations(in, in.Stmt.Region)
			d.body(in, in.Stmt.Body, d.fileOf(in))
		}
	}
}

// fileOf returns the source file of a block or generate instance.
func (d *Design) fileOf(in *hier.Instance) *sema.File {
	for p := in; p != nil; p = d.parent[p] {
		if p.Kind == hier.KindEntity {
			if p.Arch != nil {
				return p.Arch.File
			}
			return p.Entity.File
		}
	}
	return nil
}

// body elaborates the concurrent statements ss of in: processes in
// order, and the child instances each structural statement created.
func (d *Design) body(in *hier.Instance, ss []*sema.Stmt, f *sema.File) {
	unlabeled := 0
	for _, s := range ss {
		if d.stopped {
			return
		}
		switch s.Kind {
		case sema.StmtProcess, sema.StmtOther:
			name := ""
			if s.Label != nil {
				name = string(s.Label.Name)
			} else {
				// GHDL-style names for unlabeled statements: _p0, _p1
				// in statement order within the region.
				name = "_p" + strconv.Itoa(unlabeled)
				unlabeled++
			}
			d.process(in, s, f, in.Path+"."+name)
		default:
			if s.Label == nil {
				unlabeled++
			}
			for _, c := range in.Children {
				if c.Stmt == s {
					d.parent[c] = in
					d.instance(c)
				}
			}
		}
	}
}

func (d *Design) process(in *hier.Instance, s *sema.Stmt, f *sema.File,
	path string,
) {
	p := sim.Process{Path: path, Src: srcOf(f, s.Node.Span()),
		Postponed: !s.Node.Token(token.KwPostponed, 0).IsZero(),
		Modeled:   d.standIn(in)}
	ev := in.Evaluator()
	for _, t := range seqTargets(s.Seq) {
		if tg, ok := d.staticTarget(in, t, ev); ok &&
			!slices.Contains(p.Drivers, tg) {
			p.Drivers = append(p.Drivers, tg)
		}
	}
	idx := len(d.Model.Processes)
	d.Model.Processes = append(d.Model.Processes, p)
	d.Procs = append(d.Procs, &Proc{Index: idx, Path: path, Inst: in,
		Stmt: s, Ev: ev, File: f})
}

// seqTargets returns the signal assignment targets of a statement tree
// in order.
func seqTargets(ss []*sema.SeqStmt) []*sema.Expr {
	var out []*sema.Expr
	var walk func([]*sema.SeqStmt)
	walk = func(ss []*sema.SeqStmt) {
		for _, s := range ss {
			// A force or release defines no driver (LRM 14.7.2).
			k := s.Node.Kind()
			if s.Kind == sema.SeqSignal && s.Target != nil &&
				k != cst.KindForceAssignStmt &&
				k != cst.KindReleaseAssignStmt {
				out = append(out, s.Target)
			}
			for _, b := range s.Branches {
				walk(b.Body)
			}
			walk(s.Else)
			for _, a := range s.Alts {
				walk(a.Body)
			}
			walk(s.Body)
		}
	}
	walk(ss)
	return out
}

// declarations declares the signals of region r in instance in.
func (d *Design) declarations(in *hier.Instance, r *sema.Region) {
	if r == nil {
		return
	}
	for _, x := range r.Decls {
		if d.stopped {
			return
		}
		if x.Kind != sema.DeclSignal {
			continue
		}
		t, init, ok := d.layout(in, x, nil)
		if !ok {
			continue
		}
		sig := sim.Signal{Path: in.Path + "." + string(x.Name), Type: t,
			Init: init, Dir: trace.Internal, Src: d.declSrc(x)}
		d.resolution(&sig, x.Type)
		d.add(in, x, sig)
	}
}

func (d *Design) add(in *hier.Instance, x *sema.Decl, s sim.Signal,
) sim.SigID {
	id := d.Model.AddSignal(s)
	d.sigs[sigKey{in, x}] = id
	d.byPath[strings.ToLower(s.Path)] = id
	return id
}

// layout evaluates the subtype and initial value of the object x in
// instance in. shape, if valid, constrains an unconstrained port.
func (d *Design) layout(in *hier.Instance, x *sema.Decl,
	shape *eval.Value,
) (*eval.Type, eval.Value, bool) {
	ev := in.Evaluator()
	src := d.declSrc(x)
	t, err := ev.Layout(x.Type)
	if d.charge(ev.Used(), src) {
		return nil, eval.Value{}, false
	}
	if err != nil {
		d.runtime(src, in.Path, fmt.Errorf("the subtype of %s: %w",
			x.Text, err))
		return nil, eval.Value{}, false
	}
	if !t.Constrained() && shape != nil && len(shape.B) > 0 {
		t = t.ArraySubtype(shape.B...)
	}
	var v eval.Value
	if x.Value != nil {
		vev := hier.Named(ev, in, x.Value) // 'PATH_NAME and co.
		v, err = vev.Value(x.Value)
		if d.charge(vev.Used(), src) {
			return nil, eval.Value{}, false
		}
		if err == nil && d.infers(x, shape) {
			// VHDL-2019 inferred constraints (LRM 6.4.2.1): the
			// value supplies the open index ranges.
			t = eval.InferSubtype(t, v)
		}
		if err == nil {
			v, err = eval.Check(v, t)
		}
	} else {
		v, err = eval.Default(d.Budget, t)
		if d.Budget.Exhausted() {
			d.charge(0, src)
			return nil, eval.Value{}, false
		}
	}
	if err != nil {
		if shape != nil && !t.Constrained() {
			return t, *shape, true
		}
		d.runtime(src, in.Path, fmt.Errorf("the initial value of %s: %w",
			x.Text, err))
		return nil, eval.Value{}, false
	}
	return t, v, true
}

// infers reports whether the signal or port x takes the open
// constraints of its subtype from its initial value (VHDL-2019
// inferred constraints): a signal always (sema rejects an
// unconstrained signal before 2019), a port only under 2019 and only
// when no actual gives it a shape.
func (d *Design) infers(x *sema.Decl, shape *eval.Value) bool {
	if x.Kind == sema.DeclSignal {
		return true
	}
	return shape == nil && d.r != nil && d.r.Standard >= token.Std2019
}
