package elab

// interp_dyn.go implements sema.Dynamic for a running process: names of
// signals and variables, signal attributes, calls the static evaluator
// cannot make, dereferences and allocators.

import (
	"errors"
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/elab/hier"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// References the machine hands to the evaluator.
type (
	// varRef is a variable, constant or parameter of frame frame.
	varRef struct {
		frame int
		d     *sema.Decl
	}
	// objRef is a variable of protected object obj.
	objRef struct {
		obj int64
		d   *sema.Decl
	}
	// sharedRef is a shared variable or a package or architecture
	// file of one instance.
	sharedRef struct{ k sharedKey }
	// sigRef is the current value of a signal part.
	sigRef struct{ p sigPart }
	// attrRef is a signal attribute of a part.
	attrRef struct {
		attr sema.Key
		p    sigPart
		t    *eval.Type
	}
	// implRef is an implicit signal ('stable, 'delayed, ...).
	implRef struct{ id sim.SigID }
)

// Function handles of eval.Call nodes.
type (
	// callFn calls a subprogram the interpreter runs (impure, with
	// signal or file parameters, a protected method) or a built-in.
	callFn struct {
		x     *sema.Decl
		body  *sema.Decl // nil: built-in
		e     *sema.Expr
		parts []*sigPart // per parameter: the signal actual
		// method marks a protected method call: Args[0] is the
		// object.
		method bool
		ret    *eval.Type
	}
	derefFn struct{}
	// methodObjFn is the object of a method alias: its prefix
	// evaluated in the frame of the object Args[0] (0: the current
	// frames).
	methodObjFn struct{ obj *sema.Expr }

	allocFn struct {
		t   *eval.Type // the access type
		def *eval.Type // the designated subtype of `new T`
		// st is def's sema subtype: its protected parts get objects.
		st *sema.Type
	}
)

// sigPart is a part of a signal: its scalar subelements t.First ..
// t.First+t.N-1, shaped like shape (the part of the signal's initial
// value, with its subtypes and index ranges).
type sigPart struct {
	t     sim.Target
	shape eval.Value
}

func (p sigPart) whole(d *Design) bool {
	return p.t.First == 0 && p.t.N == leaves(d.Model.Signals[p.t.Sig].Init)
}

// Ref implements sema.Dynamic.
func (m *machine) Ref(d *sema.Decl) (eval.Ref, bool) {
	switch d.Kind {
	case sema.DeclSignal:
		if p, ok := m.signalOf(d); ok {
			return sigRef{p}, true
		}
		return nil, false
	case sema.DeclPort:
		if d.Class == sema.ClassVariable {
			// A shared-variable port (VHDL-2019): the object of its
			// actual.
			if k, ok := m.w.d.varPortKey(m.inst(), d); ok {
				return sharedRef{k}, true
			}
			return m.varOf(d)
		}
		if p, ok := m.signalOf(d); ok {
			return sigRef{p}, true
		}
		return nil, false
	case sema.DeclVariable, sema.DeclConstant, sema.DeclParameter,
		sema.DeclLoopParam, sema.DeclFile:
		if r, ok := m.varOf(d); ok {
			if v, isVar := r.(varRef); isVar {
				if s := m.frames[v.frame].vars[d]; s.sig != nil {
					return sigRef{*s.sig}, true
				}
			}
			return r, true
		}
		if d.Kind == sema.DeclFile && d.Parent != nil &&
			globalRegion(d.Parent) || m.w.runtimeConst(m.ev, d) ||
			m.envConstant(d) {
			// A design-region file, a constant whose value the
			// interpreter computes (interp_rtconst.go), or a deferred
			// std.env constant the interpreter supplies.
			return sharedRef{m.sharedKeyOf(d)}, true
		}
		return nil, false
	case sema.DeclSharedVariable:
		return sharedRef{m.sharedKeyOf(d)}, true
	case sema.DeclGeneric:
		// A generic constant of a running subprogram instance (an
		// object of its frame); else the instance's binding.
		return m.varOf(d)
	}
	return nil, false
}

// GenericLayout implements sema.GenericDynamic: the actual subtype of
// the generic type d of the innermost running instance binding it.
func (m *machine) GenericLayout(d *sema.Decl) (*eval.Type, bool) {
	for i := len(m.frames) - 1; i >= 0; i-- {
		if s, ok := m.frames[i].vars[d]; ok {
			return s.t, s.t != nil
		}
	}
	return nil, false
}

// GenericSubprogram implements sema.GenericDynamic: the actual of the
// generic subprogram d in the innermost running instance binding it.
func (m *machine) GenericSubprogram(d *sema.Decl) (*sema.Decl, bool) {
	for i := len(m.frames) - 1; i >= 0; i-- {
		f := &m.frames[i]
		if _, ok := f.vars[d]; !ok {
			continue
		}
		if f.call == nil || f.call.Decl == nil {
			return nil, false
		}
		a := m.ev.InstanceSubprogram(f.call.Decl.Denotes(), d)
		return a, a != nil
	}
	return nil, false
}

var _ sema.GenericDynamic = (*machine)(nil)

// Report implements sema.Reporter: an assertion that fails, or a
// report, in a subprogram the static evaluator runs for the process
// (a pure function and the procedures it calls) is reported like the
// process's own (machine.assert), at the statement.
func (m *machine) Report(s *sema.SeqStmt, assertion bool, sev int64,
	msg string,
) error {
	if m.ctx == nil {
		return nil
	}
	return m.ctx.Report(sim.Report{Assertion: assertion,
		Severity: trace.Severity(sev + 1), Message: msg,
		Src: m.w.d.stmtSrc(s.Node)})
}

// elabReporter receives the assertion and report statements run while
// d is elaborated (sema.Evaluator.SetReporter): the kernel reports them
// when it initializes (sim.Model.Init).
type elabReporter struct{ d *Design }

func (r elabReporter) Report(s *sema.SeqStmt, assertion bool, sev int64,
	msg string,
) error {
	r.d.Model.Init = append(r.d.Model.Init, sim.Report{
		Assertion: assertion, Severity: trace.Severity(sev + 1),
		Message: msg, Src: r.d.stmtSrc(s.Node)})
	return nil
}

var _ sema.Reporter = elabReporter{}

// stmtSrc is the source place of the statement node n: its file among
// the design's, or for a statement of a bundled std/ieee body (an
// assertion of numeric_std) its bundle path in Bundled.
func (d *Design) stmtSrc(n cst.Node) sim.SrcRef {
	if n.IsZero() || d.r == nil {
		return sim.SrcRef{}
	}
	root := n
	for p := n.Parent(); !p.IsZero(); p = p.Parent() {
		root = p
	}
	for _, f := range d.r.Files {
		if f.Parse != nil && f.Parse.Root == root.Green() {
			return srcOf(f, n.Span())
		}
	}
	if d.r.Bundle != nil {
		for _, f := range d.r.Bundle.Files {
			if f.Parse != nil && f.Parse.Root == root.Green() {
				s := srcOf(f, n.Span())
				s.Path, s.Bundled = "", f.Path
				return s
			}
		}
	}
	return sim.SrcRef{}
}

var _ sema.Reporter = (*machine)(nil)

// globalRegion reports whether objects of r are elaborated once per
// design (packages) or per instance (architectures, entities, blocks)
// rather than per process or call.
func globalRegion(r *sema.Region) bool {
	switch r.Kind {
	case sema.RegionPackage, sema.RegionPackageBody,
		sema.RegionArchitecture, sema.RegionEntity, sema.RegionBlock,
		sema.RegionGenerate:
		return true
	}
	return false
}

// varOf finds the object d in the frames, innermost first, and in the
// protected object of a method frame.
func (m *machine) varOf(d *sema.Decl) (eval.Ref, bool) {
	for i := len(m.frames) - 1; i >= 0; i-- {
		f := &m.frames[i]
		if _, ok := f.vars[d]; ok {
			return varRef{i, d}, true
		}
		if f.obj != 0 {
			if o := m.w.objs[f.obj]; o != nil {
				if _, ok := o.vars[d]; ok {
					return objRef{f.obj, d}, true
				}
			}
		}
	}
	return nil, false
}

// signalOf returns the whole signal or port d of the process's
// instance.
func (m *machine) signalOf(d *sema.Decl) (sigPart, bool) {
	id, ok := m.w.d.Signal(m.inst(), d)
	if !ok {
		return sigPart{}, false
	}
	init := m.w.d.Model.Signals[id].Init
	return sigPart{t: sim.Target{Sig: id, N: leaves(init)}, shape: init},
		true
}

func (m *machine) inst() *hier.Instance { return m.prog.inst }

// Load implements sema.Dynamic.
func (m *machine) Load(ref eval.Ref) (eval.Value, error) {
	switch r := ref.(type) {
	case varRef:
		if r.frame < len(m.frames) {
			if s, ok := m.frames[r.frame].vars[r.d]; ok {
				return s.v, nil
			}
		}
		return eval.Value{}, errors.New(r.d.Text + " is not elaborated")
	case objRef:
		if o := m.w.objs[r.obj]; o != nil {
			if s, ok := o.vars[r.d]; ok {
				return s.v, nil
			}
		}
		return eval.Value{}, errors.New(r.d.Text + " is not elaborated")
	case sharedRef:
		s, err := m.shared(r.k)
		return s.v, err
	case sigRef:
		return m.sigValue(r.p)
	case attrRef:
		return m.sigAttr(r)
	case implRef:
		if m.ctx == nil {
			return m.w.d.Model.Signals[r.id].Init, nil
		}
		return m.ctx.Value(r.id), nil
	}
	return eval.Value{}, errors.New("unknown reference")
}

// sigValue returns the current value of part p (its initial value
// before the simulation starts).
func (m *machine) sigValue(p sigPart) (eval.Value, error) {
	if m.ctx == nil {
		return p.shape, nil
	}
	if p.whole(m.w.d) {
		return m.ctx.Value(p.t.Sig), nil
	}
	flat, err := m.ctx.PartValues(p.t)
	if err != nil {
		return eval.Value{}, err
	}
	v, _ := rebuild(p.shape, flat)
	return v, nil
}

func (m *machine) sigAttr(r attrRef) (eval.Value, error) {
	if m.ctx == nil {
		return eval.Value{}, eval.NotStatic("'" + string(r.attr))
	}
	c := m.ctx
	switch r.attr {
	case "event":
		ok, err := c.PartEvent(r.p.t)
		return eval.Bool(ok), err
	case "active":
		ok, err := c.PartActive(r.p.t)
		return eval.Bool(ok), err
	case "last_event":
		t, err := c.PartLastEvent(r.p.t)
		return eval.Phys(r.t, t), err
	case "last_active":
		t, err := c.PartLastActive(r.p.t)
		return eval.Phys(r.t, t), err
	case "last_value":
		flat, err := c.PartLastValues(r.p.t)
		if err != nil {
			return eval.Value{}, err
		}
		v, _ := rebuild(r.p.shape, flat)
		return v, nil
	case "driving":
		ok, err := c.PartDriving(r.p.t)
		return eval.Bool(ok), err
	case "driving_value":
		flat, err := c.PartDrivingValues(r.p.t)
		if err != nil {
			return eval.Value{}, err
		}
		v, _ := rebuild(r.p.shape, flat)
		return v, nil
	}
	return eval.Value{}, eval.NotStatic("'" + string(r.attr))
}

// rebuild returns tmpl with its leaves replaced by flat, in order.
func rebuild(tmpl eval.Value, flat []eval.Value) (eval.Value, int) {
	if isLeaf(tmpl) {
		v := flat[0]
		if tmpl.T != nil {
			v.T = tmpl.T
		}
		return v, 1
	}
	out := tmpl
	out.E = make([]eval.Value, len(tmpl.E))
	n := 0
	for i, e := range tmpl.E {
		var used int
		out.E[i], used = rebuild(e, flat[n:])
		n += used
	}
	return out, n
}

// flatten appends the scalar subelements of v.
func flatten(v eval.Value, out []eval.Value) []eval.Value {
	if isLeaf(v) {
		return append(out, v)
	}
	for _, e := range v.E {
		out = flatten(e, out)
	}
	return out
}

// Lower implements sema.Dynamic.
func (m *machine) Lower(ev *sema.Evaluator, e *sema.Expr, b *eval.Budget,
) (eval.Expr, bool, error) {
	switch e.Kind {
	case sema.ExprName:
		if e.Decl != nil || e.Node.Kind() != cst.KindExternalName {
			return nil, false, nil
		}
		x, err := m.external(e)
		if err != nil {
			return nil, true, err
		}
		switch {
		case x.Class == sema.ClassSignal:
			p, ok := m.extSignal(x)
			if !ok {
				return nil, true, errors.New("the external name " +
					e.Node.Text() + " is not elaborated")
			}
			return &eval.Load{Ref: sigRef{p}}, true, nil
		case x.Decl != nil && x.Decl.Kind == sema.DeclSharedVariable:
			return &eval.Load{Ref: sharedRef{sharedKey{x.Inst, x.Decl}}},
				true, nil
		case x.Decl != nil:
			iev := ev
			if x.Inst != nil {
				iev = x.Inst.Evaluator()
			}
			v, err := iev.ConstantIn(x.Decl, b)
			if err != nil {
				return nil, true, err
			}
			return &eval.Const{V: v}, true, nil
		}
		return nil, false, nil
	case sema.ExprAttr:
		return m.lowerAttr(ev, e, b)
	case sema.ExprCall:
		return m.lowerCall(ev, e, b)
	case sema.ExprDeref:
		x, err := ev.Lower(e.Prefix, b)
		if err != nil {
			return nil, true, err
		}
		return &eval.Call{Fn: derefFn{}, Args: []eval.Expr{x}}, true, nil
	case sema.ExprAllocator:
		t, err := ev.LayoutIn(e.Type, b)
		if err != nil {
			return nil, true, err
		}
		fn := allocFn{t: t}
		var args []eval.Expr
		if len(e.Args) > 0 && e.Args[0].Kind != sema.ExprTypeMark {
			x, err := ev.Lower(e.Args[0], b)
			if err != nil {
				return nil, true, err
			}
			args = append(args, x)
		} else {
			// new T (a type mark operand) or new subtype_indication.
			dt := e.Type.Base().Designated
			if len(e.Args) > 0 && !e.Args[0].Type.Unknown() {
				dt = e.Args[0].Type
			}
			if fn.def, err = ev.LayoutIn(dt, b); err != nil {
				return nil, true, err
			}
			fn.st = dt
		}
		return &eval.Call{Fn: fn, Args: args}, true, nil
	}
	return nil, false, nil
}

// signalAttrs are the attributes of signals that read the kernel.
var signalAttrs = map[sema.Key]bool{
	"event": true, "active": true, "last_event": true,
	"last_active": true, "last_value": true, "driving": true,
	"driving_value": true, "stable": true, "quiet": true,
	"transaction": true, "delayed": true,
}

func (m *machine) lowerAttr(ev *sema.Evaluator, e *sema.Expr,
	b *eval.Budget,
) (eval.Expr, bool, error) {
	if e.Attr == "reflect" {
		// VHDL-2019 std.reflection: the mirror (interp_reflect.go).
		return m.reflectValue(ev, e, b)
	}
	if nameAttr(e) {
		return m.lowerName(ev, e) // interp_names.go
	}
	if !signalAttrs[e.Attr] || e.Prefix == nil {
		return nil, false, nil
	}
	p, err := m.sigPart(ev, e.Prefix, b)
	if errors.Is(err, ErrNotSignal) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	switch e.Attr {
	case "stable", "quiet", "transaction", "delayed":
		id, ok := m.prog.implicit[e]
		if !ok {
			// Only a process or concurrent statement has implicit
			// signals: in a subprogram body the attribute is illegal
			// (LRM 4.2.2.3 for a signal parameter; NVC: "implicit
			// signal STABLE cannot be used in a subprogram body").
			return nil, true, errors.New("the implicit signal '" +
				strings.ToUpper(string(e.Attr)) + " cannot be used " +
				"in a subprogram body")
		}
		return &eval.Load{Ref: implRef{id}}, true, nil
	}
	var t *eval.Type
	if !e.Type.Unknown() {
		if t, err = ev.LayoutIn(e.Type, b); err != nil {
			return nil, true, err
		}
	}
	return &eval.Load{Ref: attrRef{attr: e.Attr, p: p, t: t}}, true, nil
}

// runsHere reports whether the interpreter (not the static evaluator)
// runs calls of x: impure functions, protected methods, and functions
// with a signal or file parameter, a parameter of a protected or
// access type (whose objects live in the world), or an out or inout
// variable parameter (written back to its actual).
func runsHere(x *sema.Decl) bool {
	if !sema.Pure(x) {
		return true
	}
	if x.Parent != nil && x.Parent.Kind == sema.RegionProtected {
		return true
	}
	for _, p := range x.Params {
		if p.Class == sema.ClassSignal || p.Class == sema.ClassFile {
			return true
		}
		if p.Mode == sema.ModeOut || p.Mode == sema.ModeInout {
			return true
		}
		if p.Type != nil && !p.Type.Unknown() {
			switch p.Type.Base().Class {
			case sema.TypeProtected, sema.TypeAccess:
				return true
			}
		}
	}
	return false
}

func (m *machine) lowerCall(ev *sema.Evaluator, e *sema.Expr,
	b *eval.Budget,
) (eval.Expr, bool, error) {
	if e.Decl == nil {
		return nil, false, nil
	}
	x := e.Decl.Denotes()
	if x.Kind != sema.DeclFunction || x.Implicit && x.Op != 0 {
		return nil, false, nil
	}
	var body *sema.Decl
	if !x.Implicit {
		body = ev.Body(x)
	}
	if body != nil && !runsHere(x) {
		return nil, false, nil
	}
	fn := &callFn{x: x, body: body, e: e}
	if !e.Type.Unknown() {
		t, err := ev.LayoutIn(e.Type, b)
		if err != nil && !objectResult(e, x) { // interp_call.go
			return nil, true, err
		}
		fn.ret = t
	}
	var acts []*sema.Expr
	if x.Implicit {
		acts = e.Args
	} else {
		var err error
		if acts, err = sema.Actuals(e, x); err != nil {
			return nil, true, err
		}
	}
	var args []eval.Expr
	if mo := methodObj(e.Decl); mo != nil {
		// A method alias (VHDL-2019): the object it names, in the
		// frame of the call's object.
		fn.method = true
		var outer eval.Expr = &eval.Const{V: eval.Value{}}
		if e.Prefix != nil {
			var err error
			if outer, err = ev.Lower(e.Prefix, b); err != nil {
				return nil, true, err
			}
		}
		args = append(args, &eval.Call{Fn: methodObjFn{mo},
			Args: []eval.Expr{outer}})
	} else if e.Prefix != nil && x.Parent != nil &&
		x.Parent.Kind == sema.RegionProtected {
		fn.method = true
		ox, err := ev.Lower(e.Prefix, b)
		if err != nil {
			return nil, true, err
		}
		args = append(args, ox)
	}
	fn.parts = make([]*sigPart, len(acts))
	for i, a := range acts {
		if i < len(x.Params) && x.Params[i].Class == sema.ClassSignal {
			p, err := m.sigPart(ev, a, b)
			if err != nil {
				return nil, true, err
			}
			fn.parts[i] = &p
			args = append(args, &eval.Const{V: eval.Value{}})
			continue
		}
		ax, err := ev.Lower(a, b)
		if err != nil {
			return nil, true, err
		}
		args = append(args, ax)
	}
	return &eval.Call{Fn: fn, Args: args, T: fn.ret}, true, nil
}

// Call implements sema.Dynamic.
func (m *machine) Call(fn eval.Func, args []eval.Value, b *eval.Budget,
) (eval.Value, bool, error) {
	switch f := fn.(type) {
	case derefFn:
		v, err := m.w.deref(args[0])
		return v, true, err
	case allocFn:
		if len(args) > 0 {
			return m.w.alloc(f.t, args[0]), true, nil
		}
		v, err := eval.Default(b, f.def)
		if err != nil {
			return eval.Value{}, true, err
		}
		if m.b == nil {
			m.b = b
		}
		if v, err = m.instantiate(v, f.st); err != nil {
			return eval.Value{}, true, err
		}
		return m.w.alloc(f.t, v), true, nil
	case *callFn:
		v, err := m.callFunction(f, args, b)
		return v, true, err
	case methodObjFn:
		h, err := m.methodObject(args[0].I, f.obj, b)
		return eval.Value{I: h}, true, err
	}
	return eval.Value{}, false, nil
}

// sigPart resolves the name e to the signal part it denotes: a signal,
// a port, a signal parameter or alias, or an indexed, sliced or
// selected name of one. Indices are evaluated by ev now.
func (m *machine) sigPart(ev *sema.Evaluator, e *sema.Expr,
	b *eval.Budget,
) (sigPart, error) {
	if e == nil {
		return sigPart{}, ErrNotSignal
	}
	switch e.Kind {
	case sema.ExprName:
		d := e.Decl
		if d == nil {
			if e.Node.Kind() != cst.KindExternalName {
				return sigPart{}, ErrNotSignal
			}
			x, err := m.external(e)
			if err != nil {
				return sigPart{}, err
			}
			if p, ok := m.extSignal(x); ok &&
				x.Class == sema.ClassSignal {
				return p, nil
			}
			return sigPart{}, ErrNotSignal
		}
		if d.Kind == sema.DeclAlias && d.Value != nil {
			return m.sigPart(ev, d.Value, b)
		}
		x := d.Denotes()
		if x.Kind == sema.DeclAlias && x.Value != nil {
			return m.sigPart(ev, x.Value, b)
		}
		if x.Kind == sema.DeclParameter && x.Class == sema.ClassSignal {
			if r, ok := m.varOf(x); ok {
				if v, isVar := r.(varRef); isVar {
					if s := m.frames[v.frame].vars[x]; s.sig != nil {
						return *s.sig, nil
					}
				}
			}
			return sigPart{}, ErrNotSignal
		}
		if x.Kind != sema.DeclSignal && x.Kind != sema.DeclPort {
			return sigPart{}, ErrNotSignal
		}
		p, ok := m.signalOf(x)
		if !ok {
			return sigPart{}, ErrNotSignal
		}
		return p, nil
	case sema.ExprIndex:
		p, err := m.sigPart(ev, e.Prefix, b)
		if err != nil {
			return p, err
		}
		sh := p.shape
		if sh.Kind() != eval.KindArray || len(e.Args) != len(sh.B) {
			return sigPart{}, errors.New("indexed name of a " +
				"non-array signal")
		}
		lin := int64(0)
		for i, a := range e.Args {
			v, err := ev.Eval(a, b)
			if err != nil {
				return sigPart{}, err
			}
			r := sh.B[i]
			if !r.Contains(v.I) {
				return sigPart{}, eval.IndexError(v.I, r)
			}
			lin = lin*r.Len() + r.Offset(v.I)
		}
		first := p.t.First
		for _, el := range sh.E[:lin] {
			first += leaves(el)
		}
		el := sh.E[lin]
		return sigPart{t: sim.Target{Sig: p.t.Sig, First: first,
			N: leaves(el)}, shape: el}, nil
	case sema.ExprSlice:
		p, err := m.sigPart(ev, e.Prefix, b)
		if err != nil {
			return p, err
		}
		sh := p.shape
		if sh.Kind() != eval.KindArray || len(sh.B) != 1 {
			return sigPart{}, errors.New("slice of a non-array signal")
		}
		r, err := ev.EvalRange(e.Range, b)
		if err != nil {
			return sigPart{}, err
		}
		ar := sh.B[0]
		out := sh
		out.B = []eval.Range{r}
		if r.Null() {
			out.E = nil
			return sigPart{t: sim.Target{Sig: p.t.Sig, First: p.t.First},
				shape: out}, nil
		}
		if r.Dir != ar.Dir || !ar.Contains(r.Left) ||
			!ar.Contains(r.Right) {
			return sigPart{}, eval.SliceError(r, ar)
		}
		lo := ar.Offset(r.Left)
		first := p.t.First
		for _, el := range sh.E[:lo] {
			first += leaves(el)
		}
		out.E = sh.E[lo : lo+r.Len()]
		if out.T != nil && out.T.Bounds != nil {
			out.T = out.T.BaseType().ArraySubtype(r)
		}
		return sigPart{t: sim.Target{Sig: p.t.Sig, First: first,
			N: leaves(out)}, shape: out}, nil
	case sema.ExprSelect:
		p, err := m.sigPart(ev, e.Prefix, b)
		if err != nil {
			return p, err
		}
		sh := p.shape
		if sh.Kind() != eval.KindRecord || e.Decl == nil ||
			e.Decl.Pos < 0 || e.Decl.Pos >= len(sh.E) {
			return sigPart{}, ErrNotSignal
		}
		first := p.t.First
		for _, el := range sh.E[:e.Decl.Pos] {
			first += leaves(el)
		}
		el := sh.E[e.Decl.Pos]
		return sigPart{t: sim.Target{Sig: p.t.Sig, First: first,
			N: leaves(el)}, shape: el}, nil
	}
	return sigPart{}, ErrNotSignal
}

var _ sema.Dynamic = (*machine)(nil)

// external resolves the external name e of the process, once.
func (m *machine) external(e *sema.Expr) (External, error) {
	pc := m.w.prog
	k := extKey{m.prog.inst, e}
	if x, ok := pc.externals[k]; ok {
		return x, nil
	}
	x, err := pc.d.External(m.prog.inst, e)
	if err != nil {
		return External{}, err
	}
	pc.externals[k] = x
	return x, nil
}

type extKey struct {
	in *hier.Instance
	e  *sema.Expr
}

func (m *machine) extSignal(x External) (sigPart, bool) {
	if x.Class != sema.ClassSignal || x.Decl == nil {
		return sigPart{}, false
	}
	if x.Inst == nil {
		return sigPart{}, false
	}
	init := m.w.d.Model.Signals[x.Sig].Init
	return sigPart{t: sim.Target{Sig: x.Sig, N: leaves(init)},
		shape: init}, true
}
