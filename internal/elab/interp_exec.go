package elab

// interp_exec.go executes statements: assignments, control flow,
// waits, assertions and subprogram calls.

import (
	"errors"
	"fmt"
	"slices"

	"github.com/connerohnesorge/gohard/v2/internal/elab/hier"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/sim"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// exec executes one statement; susp reports a wait.
func (m *machine) exec(n *node) (sim.Wait, bool, error) {
	if !m.w.eager {
		if err := m.elabShared(); err != nil {
			return sim.Wait{}, false, err
		}
	}
	s := n.s
	ev, b := m.ev, m.b
	switch s.Kind {
	case sema.SeqNull:
	case sema.SeqVarAssign:
		v, err := m.evalFor(s.Value, s.Target)
		if err != nil {
			return sim.Wait{}, false, err
		}
		return sim.Wait{}, false, m.assignVar(s.Target, v)
	case sema.SeqCondVarAssign:
		for i, br := range s.Branches {
			ok, err := ev.Cond(br.Cond, b)
			if err != nil {
				return sim.Wait{}, false, err
			}
			if ok {
				m.branch(s, i)
				if br.Value == nil {
					return sim.Wait{}, false, nil
				}
				v, err := m.evalFor(br.Value, s.Target)
				if err != nil {
					return sim.Wait{}, false, err
				}
				return sim.Wait{}, false, m.assignVar(s.Target, v)
			}
		}
		m.branch(s, len(s.Branches))
	case sema.SeqSelVarAssign:
		i, err := m.choose(s.Selector, altChoices(s.Alts), altOthers(s.Alts),
			s.Matching)
		if err != nil || i < 0 {
			return sim.Wait{}, false, err
		}
		m.branch(s, i)
		if s.Alts[i].Value == nil {
			return sim.Wait{}, false, nil
		}
		v, err := m.evalFor(s.Alts[i].Value, s.Target)
		if err != nil {
			return sim.Wait{}, false, err
		}
		return sim.Wait{}, false, m.assignVar(s.Target, v)
	case sema.SeqSignal:
		return sim.Wait{}, false, m.signal(n)
	case sema.SeqWait:
		return m.waitStmt(n)
	case sema.SeqAssert, sema.SeqReport:
		return sim.Wait{}, false, m.assert(n)
	case sema.SeqCall:
		return m.procedure(n)
	case sema.SeqIf:
		for i, br := range s.Branches {
			ok, err := ev.Cond(br.Cond, b)
			if err != nil {
				return sim.Wait{}, false, err
			}
			if ok {
				m.branch(s, i)
				m.push(n.branches[i], nil)
				return sim.Wait{}, false, nil
			}
		}
		m.branch(s, len(s.Branches))
		m.push(n.els, nil)
	case sema.SeqCase:
		i, err := m.choose(s.Selector, altChoices(s.Alts),
			altOthers(s.Alts), s.Matching)
		if err != nil {
			return sim.Wait{}, false, err
		}
		if i < 0 {
			return sim.Wait{}, false, errors.New("no case choice " +
				"covers the selector value")
		}
		m.branch(s, i)
		m.push(n.alts[i], nil)
	case sema.SeqLoop:
		return sim.Wait{}, false, m.loop(n)
	case sema.SeqNext, sema.SeqExit:
		if s.Cond != nil {
			ok, err := ev.Cond(s.Cond, b)
			if err != nil || !ok {
				return sim.Wait{}, false, err
			}
		}
		return sim.Wait{}, false, m.jump(s.Loop, s.Kind == sema.SeqExit)
	case sema.SeqReturn:
		var v eval.Value
		if s.Value != nil {
			var err error
			if v, err = ev.Eval(s.Value, b); err != nil {
				return sim.Wait{}, false, err
			}
		}
		return sim.Wait{}, false, m.ret(v, s.Value != nil)
	case sema.SeqCondReturn:
		for _, br := range s.Branches {
			ok, err := ev.Cond(br.Cond, b)
			if err != nil {
				return sim.Wait{}, false, err
			}
			if !ok {
				continue
			}
			// VHDL-2019 conditional return (LRM 10.13): the first
			// branch whose condition holds decides. An unaffected
			// branch does nothing; `return when c` of a procedure
			// returns without a value.
			if br.Unaffected {
				break
			}
			if br.Value == nil {
				return sim.Wait{}, false, m.ret(eval.Value{}, false)
			}
			v, err := ev.Eval(br.Value, b)
			if err != nil {
				return sim.Wait{}, false, err
			}
			return sim.Wait{}, false, m.ret(v, true)
		}
	case sema.SeqBlock:
		if s.Region != nil {
			if err := m.declare(s.Region); err != nil {
				return sim.Wait{}, false, err
			}
		}
		m.push(n.body, nil)
	default:
		return sim.Wait{}, false, errors.New("statement not supported " +
			"in simulation")
	}
	return sim.Wait{}, false, nil
}

// branch reports to the design's statement hook (Design.SetStmtHook)
// that the decision s took its alternative i.
func (m *machine) branch(s *sema.SeqStmt, i int) {
	if h := m.w.d.hook; h != nil {
		h.Branch(s, i)
	}
}

func altChoices(as []sema.SeqAlt) [][]*sema.Expr {
	out := make([][]*sema.Expr, len(as))
	for i, a := range as {
		out[i] = a.Choices
	}
	return out
}

func altOthers(as []sema.SeqAlt) []bool {
	out := make([]bool, len(as))
	for i, a := range as {
		out[i] = a.Others
	}
	return out
}

// push enters the statement list ss in the current frame.
func (m *machine) push(ss []*node, loop *node) {
	f := &m.frames[len(m.frames)-1]
	f.conts = append(f.conts, cont{ss: ss, loop: loop})
}

// choose returns the alternative the selector's value picks (-1 for
// none).
func (m *machine) choose(sel *sema.Expr, choices [][]*sema.Expr,
	others []bool, matching bool,
) (int, error) {
	v, err := m.ev.Eval(sel, m.b)
	if err != nil {
		return -1, err
	}
	for i, cs := range choices {
		if others[i] {
			return i, nil
		}
		for _, c := range cs {
			hit, err := m.ev.ChoiceHit(c, v, matching, m.b)
			if err != nil {
				return -1, err
			}
			if hit {
				return i, nil
			}
		}
	}
	return -1, nil
}

func (m *machine) loop(n *node) error {
	s := n.s
	if s.Param == nil {
		if s.Cond != nil {
			ok, err := m.ev.Cond(s.Cond, m.b)
			if err != nil || !ok {
				return err
			}
		}
		m.push(n.body, n)
		return nil
	}
	r, err := m.ev.EvalRange(s.Range, m.b)
	if err != nil {
		return err
	}
	if r.Len() <= 0 {
		return nil
	}
	pt, err := m.ev.LayoutIn(s.Param.Type, m.b)
	if err != nil {
		return err
	}
	f := &m.frames[len(m.frames)-1]
	f.vars[s.Param] = slot{v: eval.Value{T: pt, I: r.At(0)}, t: pt}
	f.conts = append(f.conts, cont{ss: n.body, loop: n, r: r})
	return nil
}

// jump executes next (exit false) or exit of the loop labelled lbl
// (nil: the innermost).
func (m *machine) jump(lbl *sema.Decl, exit bool) error {
	f := &m.frames[len(m.frames)-1]
	for i := len(f.conts) - 1; i >= 0; i-- {
		c := &f.conts[i]
		if c.loop == nil || lbl != nil && c.loop.s.Label != lbl {
			continue
		}
		f.conts = f.conts[:i+1]
		if exit {
			if p := c.loop.s.Param; p != nil {
				delete(f.vars, p)
			}
			f.conts = f.conts[:i]
			return nil
		}
		c.i = len(c.ss) // endCont starts the next iteration
		return nil
	}
	return errors.New("next or exit outside its loop")
}

// ret returns from the current subprogram.
func (m *machine) ret(v eval.Value, has bool) error {
	fi := len(m.frames) - 1
	if fi == 0 {
		return errors.New("return outside a subprogram")
	}
	f := &m.frames[fi]
	if f.fn {
		if !has {
			return errors.New("a function returns no value")
		}
		if f.sub != nil && f.sub.Type != nil {
			t, err := m.ev.LayoutIn(f.sub.Type, m.b)
			if err != nil {
				return err
			}
			if v, err = eval.Check(v, t); err != nil {
				return err
			}
		}
	}
	f.ret, f.returned = v, true
	f.conts = nil
	return nil
}

// leave pops a finished subprogram frame: its out and inout variable
// parameters (a procedure's, or a VHDL-2019 function's) are assigned
// to their actuals.
func (m *machine) leave() error {
	fi := len(m.frames) - 1
	f := m.frames[fi]
	m.frames = m.frames[:fi]
	if f.fn {
		// callFunction reads the result.
		m.lastRet, m.lastReturned = f.ret, f.returned
	}
	for _, o := range f.outs {
		if err := m.assignPlace(o.dst, f.vars[o.formal].v); err != nil {
			return err
		}
	}
	return nil
}

// declare elaborates the declarations of region r into the current
// frame, in order (LRM 14.4).
func (m *machine) declare(r *sema.Region) error {
	if !m.w.eager {
		if err := m.elabShared(); err != nil {
			return err
		}
	}
	for _, d := range r.Decls {
		if err := m.b.Step(1); err != nil {
			return err
		}
		switch d.Kind {
		case sema.DeclVariable, sema.DeclConstant:
			s, err := m.object(d)
			if err != nil {
				return wrapErr(d.Text, err)
			}
			m.frames[len(m.frames)-1].vars[d] = s
		case sema.DeclFile:
			s, err := m.fileObject(d)
			if err != nil {
				return wrapErr(d.Text, err)
			}
			m.frames[len(m.frames)-1].vars[d] = s
		}
	}
	return nil
}

// object elaborates a variable or constant d.
func (m *machine) object(d *sema.Decl) (slot, error) {
	t, err := m.ev.LayoutIn(d.Type, m.b)
	if err != nil {
		return slot{}, err
	}
	var v eval.Value
	if d.Value != nil {
		if v, err = m.ev.Eval(d.Value, m.b); err != nil {
			return slot{}, err
		}
		// The value supplies the open index ranges: a constant's
		// (LRM 6.4.2.2) and, in VHDL-2019, a variable's (inferred
		// constraints, LRM 6.4.2.1), of elements and fields too.
		t = eval.InferSubtype(t, v)
		if v, err = eval.Check(v, t); err != nil {
			return slot{}, err
		}
	} else {
		if v, err = eval.Default(m.b, t); err != nil {
			return slot{}, err
		}
	}
	if v, err = m.instantiate(v, d.Type); err != nil {
		return slot{}, err
	}
	return slot{v: v, t: t}, nil
}

// instantiate returns v, a new object of sema subtype st, with a new
// protected object for each protected leaf: the object itself, the
// elements of an array and the fields of a record (VHDL-2019
// composites of protected types). Access values stay null.
func (m *machine) instantiate(v eval.Value, st *sema.Type,
) (eval.Value, error) {
	if !m.w.holdsProtected(st) {
		return v, nil
	}
	switch b := st.Base(); b.Class {
	case sema.TypeProtected:
		h, err := m.newObject(st)
		if err != nil {
			return eval.Value{}, err
		}
		v.I = h
	case sema.TypeArray:
		et := st.ElemType()
		e := make([]eval.Value, len(v.E))
		for i := range v.E {
			x, err := m.instantiate(v.E[i], et)
			if err != nil {
				return eval.Value{}, err
			}
			e[i] = x
		}
		v.E = e
	case sema.TypeRecord:
		e := slices.Clone(v.E)
		for i, f := range b.Fields {
			if i >= len(e) {
				break
			}
			x, err := m.instantiate(e[i], st.FieldType(f))
			if err != nil {
				return eval.Value{}, err
			}
			e[i] = x
		}
		v.E = e
	}
	return v, nil
}

// holdsProtected reports whether an object of subtype st is or holds
// a protected object (not through an access value).
func (w *world) holdsProtected(st *sema.Type) bool {
	if st == nil || st.Unknown() {
		return false
	}
	if p, ok := w.prot[st]; ok {
		return p
	}
	w.prot[st] = false // a (malformed) cycle ends here
	p := false
	switch b := st.Base(); b.Class {
	case sema.TypeProtected:
		p = true
	case sema.TypeArray:
		p = w.holdsProtected(st.ElemType())
	case sema.TypeRecord:
		for _, f := range b.Fields {
			if w.holdsProtected(st.FieldType(f)) {
				p = true
				break
			}
		}
	}
	w.prot[st] = p
	return p
}

// shared returns the shared variable or global file k, elaborating it
// on first use.
func (m *machine) shared(k sharedKey) (slot, error) {
	if s, ok := m.w.shared[k]; ok {
		return s, nil
	}
	if !m.w.eager {
		if err := m.elabShared(); err != nil {
			return slot{}, err
		}
		if s, ok := m.w.shared[k]; ok {
			return s, nil
		}
	}
	s, err := m.newShared(k)
	if err != nil {
		return slot{}, err
	}
	m.w.shared[k] = s
	return s, nil
}

// newShared elaborates the shared object k with its instance's
// evaluator (generics bound), or the top's for a package object.
func (m *machine) newShared(k sharedKey) (slot, error) {
	in := k.in
	if in == nil {
		in = m.w.d.Top
	}
	if in != nil && in.Evaluator() != nil {
		saved := m.ev
		m.ev = in.Evaluator().WithDynamic(m)
		defer func() { m.ev = saved }()
	}
	saved := m.elab
	m.elab = elabSite{d: k.d, depth: len(m.frames), in: k.in}
	defer func() { m.elab = saved }()
	var s slot
	var err error
	if k.d.Kind == sema.DeclFile {
		s, err = m.fileObject(k.d)
	} else {
		s, err = m.object(k.d)
	}
	if err != nil {
		return slot{}, wrapErr(k.d.Text, err)
	}
	return s, nil
}

// elabShared creates every shared variable of the design before the
// first process runs, in sharedOrder, so their protected objects get
// the same handles whatever the schedule. One that fails is left to
// its first use, which reports the error where the design reads it;
// only a budget error ends the process now.
func (m *machine) elabShared() error {
	if m.w.eager {
		return nil
	}
	m.w.eager = true
	for _, k := range m.w.sharedOrder() {
		if _, ok := m.w.shared[k]; ok {
			continue
		}
		s, err := m.newShared(k)
		if err != nil {
			if eval.CodeOf(err) == eval.ErrCodeBudget {
				return err
			}
			continue
		}
		m.w.shared[k] = s
	}
	// The variable ports that are objects of their own (open, or of
	// the top) come after, in elaboration order.
	for _, k := range m.w.d.ownPorts {
		if _, ok := m.w.shared[k]; ok {
			continue
		}
		s, err := m.newShared(k)
		if err != nil {
			if eval.CodeOf(err) == eval.ErrCodeBudget {
				return err
			}
			continue
		}
		m.w.shared[k] = s
	}
	return nil
}

// sharedKeyOf returns the key of the shared object x as the process
// sees it: the innermost enclosing instance that declares it (nil for
// a package object).
func (m *machine) sharedKeyOf(x *sema.Decl) sharedKey {
	start := m.inst()
	if m.elab.d != nil {
		start = m.elab.in // newShared: the object's own instance
	}
	for in := start; in != nil; in = m.w.d.Parent(in) {
		if declares(in, x.Parent) {
			return sharedKey{in, x}
		}
	}
	return sharedKey{nil, x}
}

// newObject creates a protected object of type t: its variables, in
// declaration order: the private variables of the type declaration
// (VHDL-2019, LRM 5.6.2), then the variables of its body.
func (m *machine) newObject(t *sema.Type) (int64, error) {
	body := m.w.protectedBody(t)
	if body == nil {
		if d := t.Base().Decl; d != nil && d.Unit != nil &&
			bundled(string(d.Unit.Library)) {
			// A bundled protected type without a body (the
			// std.reflection mirrors): only GoHard makes its objects,
			// a gap to report, not the design's error.
			return 0, &nativeError{name: unitOf(d) + "." + d.Text +
				" object declared by the design"}
		}
		return 0, errors.New("no protected type body for " + t.Name())
	}
	h := m.w.handle()
	o := &protObj{typ: t, vars: map[*sema.Decl]slot{}}
	m.w.objs[h] = o
	m.frames = append(m.frames, frame{vars: map[*sema.Decl]slot{},
		obj: h})
	defer func() { m.frames = m.frames[:len(m.frames)-1] }()
	if err := m.bindGenerics(t, o); err != nil {
		delete(m.w.objs, h)
		return 0, err
	}
	var decls []*sema.Decl
	if r := t.Base().Members; r != nil {
		for _, d := range r.Decls {
			if d.Kind == sema.DeclVariable {
				decls = append(decls, d)
			}
		}
	}
	decls = append(decls, body.Decls...)
	for _, d := range decls {
		switch d.Kind {
		case sema.DeclVariable, sema.DeclConstant:
			s, err := m.object(d)
			if err != nil {
				return 0, wrapErr(d.Text, err)
			}
			o.vars[d] = s
		case sema.DeclFile:
			s, err := m.fileObject(d)
			if err != nil {
				return 0, err
			}
			o.vars[d] = s
		}
	}
	return h, nil
}

// bindGenerics binds the generics of the new protected object o of
// subtype t (VHDL-2019, LRM 5.6.2, 6.4.2.4) in the object's frame, on
// top of the stack: the actuals of t's generic map (ProtectedInst),
// evaluated in the context declaring the object, else the defaults,
// which see the generics before them. They become objects of o
// (generic constants, read through objRef) and enter copies them into
// the frame of each method call (objectFrame), where GenericLayout
// finds the generic types: each object reads its own bindings, never
// another's, and a call reading them is never memoized (they are run
// time reads of the simulator's context, sema/memo.go).
func (m *machine) bindGenerics(t *sema.Type, o *protObj) error {
	gens := protectedGenerics(t)
	if len(gens) == 0 {
		return nil
	}
	var as []sema.Association
	if in := t.ProtectedInst(); in != nil {
		as = in.Map
	}
	f := &m.frames[len(m.frames)-1]
	bind := func(d *sema.Decl, s slot) {
		f.vars[d] = s
		o.vars[d] = s
	}
	for _, g := range gens {
		a := genericActual(as, g)
		switch g.Kind {
		case sema.DeclGeneric:
			def := g.Value
			if a != nil {
				def = a
			}
			if def == nil {
				return errors.New("generic " + g.Text + " of " +
					t.Name() + " has no default and no actual")
			}
			v, err := m.ev.Eval(def, m.b)
			if err != nil {
				return wrapErr("generic "+g.Text, err)
			}
			lt, err := m.ev.LayoutIn(g.Type, m.b)
			if err != nil {
				return wrapErr("generic "+g.Text, err)
			}
			lt = eval.InferSubtype(lt, v)
			if v, err = eval.Check(v, lt); err != nil {
				return wrapErr("generic "+g.Text, err)
			}
			bind(g, slot{v: v, t: lt})
		case sema.DeclGenericType:
			if a == nil || a.Type.Unknown() {
				return errors.New("generic type " + g.Text + " of " +
					t.Name() + " has no actual")
			}
			if err := m.bindGenericType(g, a.Type, bind); err != nil {
				return wrapErr("generic type "+g.Text, err)
			}
		case sema.DeclGenericSubprogram:
			return &nativeError{name: cProtGenericSubprogram}
		default:
			return &nativeError{name: cProtGenericPackage}
		}
	}
	return nil
}

// The generics of protected types the simulator does not bind yet:
// reported as unsupported/construct (please report) when an object is
// created.
const (
	cProtGenericSubprogram = "generic subprograms of protected types"
	cProtGenericPackage    = "generic packages of protected types"
)

// bindGenericType binds the generic type g to the layout of its actual
// subtype at and, for a VHDL-2019 array-class generic type (LRM
// 6.5.3), its anonymous index and element types to the actual's.
func (m *machine) bindGenericType(g *sema.Decl, at *sema.Type,
	bind func(*sema.Decl, slot),
) error {
	lt, err := m.ev.LayoutIn(at, m.b)
	if err != nil {
		return err
	}
	bind(g, slot{t: lt})
	gt := g.Type
	if gt == nil || gt.Class != sema.TypeGeneric ||
		at.Base().Class != sema.TypeArray {
		return nil
	}
	for i, x := range gt.Index {
		if i < len(at.Base().Index) && x.Class == sema.TypeGeneric &&
			x.Decl != nil {
			err := m.bindGenericType(x.Decl, at.Base().Index[i], bind)
			if err != nil {
				return err
			}
		}
	}
	if x := gt.Elem; x != nil && x.Class == sema.TypeGeneric &&
		x.Decl != nil && at.ElemType() != nil {
		return m.bindGenericType(x.Decl, at.ElemType(), bind)
	}
	return nil
}

// genericActual returns the actual the generic map as gives the
// generic g, nil when none does.
func genericActual(as []sema.Association, g *sema.Decl) *sema.Expr {
	for _, a := range as {
		if a.Formal == g && a.Part == nil {
			return a.Actual
		}
	}
	return nil
}

// protectedGenerics returns the generics of the protected type of an
// object of subtype t, in declaration order (the uninstantiated
// type's, for an instance).
func protectedGenerics(t *sema.Type) []*sema.Decl {
	spec := protectedSpec(t)
	if spec == nil || spec.Type == nil || spec.Type.Members == nil {
		return nil
	}
	var out []*sema.Decl
	for _, d := range spec.Type.Members.Decls {
		switch d.Kind {
		case sema.DeclGeneric, sema.DeclGenericType,
			sema.DeclGenericSubprogram, sema.DeclGenericPackage:
			out = append(out, d)
		}
	}
	return out
}

// objectFrame copies the generics of protected object h into the
// frame fi of a call of one of its methods (bindGenerics).
func (m *machine) objectFrame(fi int, h int64) {
	o := m.w.objs[h]
	if o == nil {
		return
	}
	for _, g := range protectedGenerics(o.typ) {
		if s, ok := o.vars[g]; ok {
			m.frames[fi].vars[g] = s
		}
	}
}

// protectedSpec returns the declaration of the protected type whose
// body runs the objects of subtype t: the uninstantiated type of a
// protected type instance (VHDL-2019, LRM 5.6.4).
func protectedSpec(t *sema.Type) *sema.Decl {
	b := t.Base()
	if b.Inst != nil && b.Inst.Of != nil {
		return b.Inst.Of
	}
	return b.Decl
}

// protectedBody finds the body region of protected type t.
func (w *world) protectedBody(t *sema.Type) *sema.Region {
	td := protectedSpec(t)
	if td == nil || td.Unit == nil {
		return nil
	}
	units := []*sema.Unit{td.Unit}
	if l, ok := w.d.r.Library(td.Unit.Library); ok {
		units = append(units, l.Secondary(td.Unit.Name)...)
	}
	var find func(r *sema.Region) *sema.Region
	find = func(r *sema.Region) *sema.Region {
		if r == nil {
			return nil
		}
		if r.Kind == sema.RegionProtectedBody && r.Owner == td {
			return r
		}
		for _, c := range r.Children {
			if x := find(c); x != nil {
				return x
			}
		}
		return nil
	}
	for _, u := range units {
		if x := find(u.Region); x != nil {
			return x
		}
	}
	return nil
}

// place is an assignable variable or part of one.
type place struct {
	kind  placeKind
	frame int
	obj   int64
	in    *hier.Instance // placeShared: the declaring instance
	d     *sema.Decl
	path  []step
	alias bool
}

type placeKind uint8

const (
	placeFrame placeKind = iota
	placeObj
	placeShared
	placeHeap
)

// step selects an element (or a slice) of a composite value.
type step struct {
	slice bool
	off   int64
	n     int64
	r     eval.Range // a slice's bounds
}

// placeOf resolves a variable name to its place.
func (m *machine) placeOf(e *sema.Expr) (place, error) {
	switch e.Kind {
	case sema.ExprName:
		d := e.Decl
		if d == nil {
			break
		}
		if d.Kind == sema.DeclAlias && d.Value != nil {
			return m.placeOf(d.Value)
		}
		x := d.Denotes()
		if x.Kind == sema.DeclAlias && x.Value != nil {
			return m.placeOf(x.Value)
		}
		if x.Kind == sema.DeclSharedVariable {
			k := m.sharedKeyOf(x)
			if _, err := m.shared(k); err != nil {
				return place{}, err
			}
			return place{kind: placeShared, in: k.in, d: x}, nil
		}
		if x.Kind == sema.DeclPort && x.Class == sema.ClassVariable {
			if k, ok := m.w.d.varPortKey(m.inst(), x); ok {
				if _, err := m.shared(k); err != nil {
					return place{}, err
				}
				return place{kind: placeShared, in: k.in, d: k.d}, nil
			}
		}
		r, ok := m.varOf(x)
		if !ok {
			break
		}
		switch r := r.(type) {
		case varRef:
			return place{kind: placeFrame, frame: r.frame, d: x}, nil
		case objRef:
			return place{kind: placeObj, obj: r.obj, d: x}, nil
		}
	case sema.ExprDeref:
		p, err := m.ev.Eval(e.Prefix, m.b)
		if err != nil {
			return place{}, err
		}
		if _, err := m.w.deref(p); err != nil {
			return place{}, err
		}
		return place{kind: placeHeap, obj: p.I}, nil
	case sema.ExprIndex, sema.ExprSlice, sema.ExprSelect:
		p, err := m.placeOf(e.Prefix)
		if err != nil {
			return place{}, err
		}
		cur, err := m.placeValue(p)
		if err != nil {
			return place{}, err
		}
		st, err := m.select1(e, cur)
		if err != nil {
			return place{}, err
		}
		p.path = append(slices.Clone(p.path), st)
		return p, nil
	}
	return place{}, errors.New("the target " + firstLine(e.Node.Text()) +
		" is not a variable")
}

// select1 is the step of an indexed name, slice or selection of the
// value cur.
func (m *machine) select1(e *sema.Expr, cur eval.Value) (step, error) {
	switch e.Kind {
	case sema.ExprSelect:
		if cur.Kind() != eval.KindRecord || e.Decl == nil ||
			e.Decl.Pos >= len(cur.E) {
			return step{}, errors.New("selected name of a non-record")
		}
		return step{off: int64(e.Decl.Pos)}, nil
	case sema.ExprIndex:
		if cur.Kind() != eval.KindArray {
			return step{}, errors.New("indexed name of a non-array")
		}
		off := int64(0)
		for d, ie := range e.Args {
			iv, err := m.ev.Eval(ie, m.b)
			if err != nil {
				return step{}, err
			}
			r := dimOf(cur, d)
			if !r.Contains(iv.I) {
				return step{}, eval.IndexError(iv.I, r)
			}
			off = off*r.Len() + r.Offset(iv.I)
		}
		return step{off: off}, nil
	}
	r, err := m.ev.EvalRange(e.Range, m.b)
	if err != nil {
		return step{}, err
	}
	ar := dimOf(cur, 0)
	if r.Null() {
		return step{slice: true, r: r}, nil
	}
	if r.Dir != ar.Dir || !ar.Contains(r.Left) || !ar.Contains(r.Right) {
		return step{}, eval.SliceError(r, ar)
	}
	return step{slice: true, off: ar.Offset(r.Left), n: r.Len(), r: r}, nil
}

func dimOf(a eval.Value, d int) eval.Range {
	if d < len(a.B) {
		return a.B[d]
	}
	return eval.Range{Left: 0, Right: int64(len(a.E)) - 1}
}

// root returns the whole object of place p and its subtype.
func (m *machine) root(p place) (eval.Value, *eval.Type, error) {
	switch p.kind {
	case placeFrame:
		s, ok := m.frames[p.frame].vars[p.d]
		if !ok {
			return eval.Value{}, nil, errors.New(p.d.Text +
				" is not elaborated")
		}
		return s.v, s.t, nil
	case placeObj:
		s := m.w.objs[p.obj].vars[p.d]
		return s.v, s.t, nil
	case placeShared:
		s, err := m.shared(sharedKey{p.in, p.d})
		return s.v, s.t, err
	}
	v, err := m.w.deref(eval.Value{I: p.obj})
	return v, nil, err
}

func (m *machine) setRoot(p place, v eval.Value) {
	switch p.kind {
	case placeFrame:
		s := m.frames[p.frame].vars[p.d]
		s.v = v
		m.frames[p.frame].vars[p.d] = s
	case placeObj:
		o := m.w.objs[p.obj]
		s := o.vars[p.d]
		s.v = v
		o.vars[p.d] = s
	case placeShared:
		k := sharedKey{p.in, p.d}
		s := m.w.shared[k]
		s.v = v
		m.w.shared[k] = s
	case placeHeap:
		m.w.heap[p.obj] = v
	}
}

// placeValue returns the current value at p (a slice for a slice
// step).
func (m *machine) placeValue(p place) (eval.Value, error) {
	v, _, err := m.root(p)
	if err != nil {
		return v, err
	}
	for _, s := range p.path {
		if s.slice {
			out := v
			if s.n == 0 {
				out.E = nil
			} else {
				out.E = v.E[s.off : s.off+s.n]
			}
			out.B = []eval.Range{s.r}
			if out.T != nil && out.T.Bounds != nil {
				out.T = out.T.BaseType().ArraySubtype(s.r)
			}
			v = out
			continue
		}
		if s.off < 0 || s.off >= int64(len(v.E)) {
			return eval.Value{}, errors.New("element outside the object")
		}
		v = v.E[s.off]
	}
	return v, nil
}

// assignPlace assigns v to p (LRM 10.6.2.1).
func (m *machine) assignPlace(p place, v eval.Value) error {
	cur, t, err := m.root(p)
	if err != nil {
		return err
	}
	if err := m.b.Step(1 + int64(len(v.E))); err != nil {
		return err
	}
	nv, err := setAt(cur, t, p.path, v)
	if err != nil {
		return err
	}
	m.setRoot(p, nv)
	return nil
}

// setAt returns cur with the element path selects replaced by v.
func setAt(cur eval.Value, t *eval.Type, path []step, v eval.Value,
) (eval.Value, error) {
	if len(path) == 0 {
		return assignWhole(cur, v, t)
	}
	s := path[0]
	e := slices.Clone(cur.E)
	if s.slice {
		if len(path) > 1 {
			sub := cur
			sub.E = e[s.off : s.off+s.n]
			sub.B = []eval.Range{s.r}
			nv, err := setAt(sub, nil, path[1:], v)
			if err != nil {
				return eval.Value{}, err
			}
			copy(e[s.off:], nv.E)
			cur.E = e
			return cur, nil
		}
		if int64(len(v.E)) != s.n {
			return eval.Value{}, eval.LengthError(s.n, int64(len(v.E)))
		}
		for i := range v.E {
			x, err := assignWhole(e[s.off+int64(i)], v.E[i],
				e[s.off+int64(i)].T)
			if err != nil {
				return eval.Value{}, err
			}
			e[s.off+int64(i)] = x
		}
		cur.E = e
		return cur, nil
	}
	if s.off < 0 || s.off >= int64(len(e)) {
		return eval.Value{}, errors.New("element outside the object")
	}
	x, err := setAt(e[s.off], e[s.off].T, path[1:], v)
	if err != nil {
		return eval.Value{}, err
	}
	e[s.off] = x
	cur.E = e
	return cur, nil
}

// assignWhole is the value an object holding old has after assigning
// v: scalars are checked against the subtype t, arrays must match the
// object's length and keep its bounds.
func assignWhole(old, v eval.Value, t *eval.Type) (eval.Value, error) {
	switch old.Kind() {
	case eval.KindArray:
		if v.Kind() != eval.KindArray {
			return eval.Value{}, errors.New("array assignment of a " +
				"non-array value")
		}
		if len(old.B) > 1 {
			for d := range old.B {
				if d >= len(v.B) || v.B[d].Len() != old.B[d].Len() {
					return eval.Value{}, eval.LengthError(
						old.B[d].Len(), dimOf(v, d).Len())
				}
			}
		} else if len(v.E) != len(old.E) {
			return eval.Value{}, eval.LengthError(int64(len(old.E)),
				int64(len(v.E)))
		}
		out := eval.Value{T: old.T, B: old.B, E: make([]eval.Value,
			len(v.E))}
		for i := range v.E {
			x, err := assignWhole(old.E[i], v.E[i], old.E[i].T)
			if err != nil {
				return eval.Value{}, err
			}
			out.E[i] = x
		}
		return out, nil
	case eval.KindRecord:
		if len(v.E) != len(old.E) {
			return eval.Value{}, errors.New("record assignment of " +
				"another type")
		}
		out := eval.Value{T: old.T, E: make([]eval.Value, len(v.E))}
		for i := range v.E {
			x, err := assignWhole(old.E[i], v.E[i], old.E[i].T)
			if err != nil {
				return eval.Value{}, err
			}
			out.E[i] = x
		}
		return out, nil
	case eval.KindInvalid:
		return v, nil
	}
	if t == nil {
		t = old.T
	}
	if t == nil {
		return v, nil
	}
	return eval.Check(v, t)
}

// assignVar executes a variable assignment to target (a name or an
// aggregate of names).
func (m *machine) assignVar(target *sema.Expr, v eval.Value) error {
	if target.Kind == sema.ExprAggregate {
		return m.assignAggregate(target, v, func(e *sema.Expr,
			x eval.Value,
		) error {
			return m.assignVar(e, x)
		})
	}
	p, err := m.placeOf(target)
	if err != nil {
		return err
	}
	return m.assignPlace(p, v)
}

// assignAggregate splits v over the elements of an aggregate target
// (positional associations, LRM 10.6.2.1).
func (m *machine) assignAggregate(target *sema.Expr, v eval.Value,
	set func(*sema.Expr, eval.Value) error,
) error {
	if v.Kind() != eval.KindArray && v.Kind() != eval.KindRecord {
		return errors.New("aggregate target of a scalar value")
	}
	if len(target.Assocs) != len(v.E) {
		return eval.LengthError(int64(len(target.Assocs)),
			int64(len(v.E)))
	}
	for i, a := range target.Assocs {
		if len(a.Choices) > 0 {
			return sema.NewUnsupported("named associations in "+
				"aggregate targets", nil)
		}
		if err := set(a.Value, v.E[i]); err != nil {
			return err
		}
	}
	return nil
}

// evalFor evaluates the value e assigned to target. An aggregate with
// others has the target's subtype as its context (LRM 9.3.3.3); when
// sema's subtype of the target is unconstrained (an object whose
// constraint is inferred from its initial value, LRM 6.4.2.3) the
// constraint is the object's own, known only now.
func (m *machine) evalFor(e, target *sema.Expr) (eval.Value, error) {
	v, err := m.ev.Eval(e, m.b)
	if err == nil || e == nil || target == nil ||
		e.Kind != sema.ExprAggregate ||
		eval.CodeOf(err) != eval.ErrCodeNotStatic {
		return v, err
	}
	cur, terr := m.ev.Eval(target, m.b)
	if terr != nil || cur.Kind() != eval.KindArray || cur.T == nil {
		return v, err
	}
	t := cur.T
	if !t.Constrained() {
		if len(cur.B) == 0 {
			return v, err
		}
		t = t.ArraySubtype(cur.B...)
	}
	x, lerr := m.ev.Lower(e, m.b)
	agg, ok := x.(*eval.Aggregate)
	if lerr != nil || !ok || agg.T == nil || agg.T.Constrained() {
		return v, err
	}
	c := *agg
	c.T = t
	return m.ev.Machine(m.b).Eval(&c)
}

// signal executes a signal assignment, force or release.
func (m *machine) signal(n *node) error {
	g := n.sig
	ev, b := m.ev, m.b
	switch g.form {
	case sigForce, sigRelease:
		parts, err := m.targetParts(g.target)
		if err != nil {
			return err
		}
		var v eval.Value
		if g.form == sigForce {
			if v, err = ev.Eval(g.value, b); err != nil {
				return err
			}
		}
		flat := flatten(v, nil)
		at := 0
		for _, p := range parts {
			mode := sim.DefaultForceMode(&m.w.d.Model.Signals[p.t.Sig])
			if g.hasMode {
				mode = sim.ForceIn
				if g.out {
					mode = sim.ForceOut
				}
			}
			if g.form == sigRelease {
				err = m.ctx.ReleaseMode(p.t, mode)
			} else {
				pv := v
				if len(parts) > 1 {
					pv = eval.Value{E: flat[at : at+p.t.N]}
					at += p.t.N
				}
				err = m.ctx.ForceMode(p.t, pv, mode)
			}
			if err != nil {
				return err
			}
		}
		return nil
	}
	k := 0
	switch g.form {
	case sigCond:
		k = -1
		for i, c := range g.conds {
			ok, err := ev.Cond(c, b)
			if err != nil {
				return err
			}
			if ok {
				k = i
				break
			}
		}
		if k < 0 {
			m.branch(n.s, len(g.conds))
		} else {
			m.branch(n.s, k)
		}
	case sigSelected:
		i, err := m.choose(g.selector, g.choices, g.others, g.matching)
		if err != nil {
			return err
		}
		if i < 0 {
			return errors.New("no choice covers the selector value")
		}
		k = i
		m.branch(n.s, k)
	}
	if k < 0 || g.unaff[k] {
		return nil
	}
	mech := sim.Mechanism{Transport: g.delay.transport}
	if g.delay.reject != nil {
		r, err := ev.Eval(g.delay.reject, b)
		if err != nil {
			return err
		}
		mech.Reject, mech.HasReject = r.I, true
	}
	ws := make([]sim.WaveElem, len(g.waves[k]))
	for i, w := range g.waves[k] {
		if w.after != nil {
			t, err := ev.Eval(w.after, b)
			if err != nil {
				return err
			}
			ws[i].After = t.I
		}
		if w.null {
			ws[i].Null = true
			continue
		}
		v, err := m.evalFor(w.value, g.target)
		if err != nil {
			return err
		}
		ws[i].Value = v
	}
	parts, err := m.targetParts(g.target)
	if err != nil {
		return err
	}
	if len(parts) == 1 {
		return m.ctx.Assign(parts[0].t, ws, mech)
	}
	flats := make([][]eval.Value, len(ws))
	for i, w := range ws {
		if !w.Null {
			flats[i] = flatten(w.Value, nil)
		}
	}
	at := 0
	for _, p := range parts {
		pw := slices.Clone(ws)
		for i := range pw {
			if !pw[i].Null {
				if at+p.t.N > len(flats[i]) {
					return errors.New("the aggregate target is wider " +
						"than the value")
				}
				pw[i].Value = eval.Value{E: flats[i][at : at+p.t.N]}
			}
		}
		at += p.t.N
		if err := m.ctx.Assign(p.t, pw, mech); err != nil {
			return err
		}
	}
	return nil
}

// targetParts resolves a signal assignment target to its parts.
func (m *machine) targetParts(e *sema.Expr) ([]sigPart, error) {
	if e == nil {
		return nil, errors.New("no target")
	}
	if e.Kind == sema.ExprAggregate {
		var out []sigPart
		for _, a := range e.Assocs {
			if len(a.Choices) > 0 {
				return nil, sema.NewUnsupported("named associations "+
					"in aggregate targets", nil)
			}
			ps, err := m.targetParts(a.Value)
			if err != nil {
				return nil, err
			}
			out = append(out, ps...)
		}
		return out, nil
	}
	p, err := m.sigPart(m.ev, e, m.b)
	if err != nil {
		if errors.Is(err, ErrNotSignal) {
			return nil, errors.New("the target " +
				firstLine(e.Node.Text()) + " is not a signal")
		}
		return nil, err
	}
	return []sigPart{p}, nil
}

// waitStmt executes a wait statement (LRM 10.2).
func (m *machine) waitStmt(n *node) (sim.Wait, bool, error) {
	w := n.wait
	var on []sim.Target
	if w.hasOn {
		for _, e := range w.on {
			p, err := m.sigPart(m.ev, e, m.b)
			if err != nil {
				return sim.Wait{}, false, err
			}
			on = appendTarget(on, p.t)
		}
	} else if w.cond != nil {
		// The signals the condition reads (LRM 10.2).
		var err error
		on, err = m.condSignals(w.cond)
		if err != nil {
			return sim.Wait{}, false, err
		}
	}
	return m.waitOn(on, w.cond, w.timeout, m.ev)
}

// condSignals returns the signals read by the condition e.
func (m *machine) condSignals(e *sema.Expr) ([]sim.Target, error) {
	var out []sim.Target
	var err error
	walkReads([]*node{{s: &sema.SeqStmt{Kind: sema.SeqNull, Cond: e}}},
		func(x *sema.Expr) bool {
			if err != nil {
				return true
			}
			if x.Kind == sema.ExprAttr {
				id, ok := m.prog.implicit[x]
				if ok {
					out = appendTarget(out, sim.Target{Sig: id,
						N: leaves(m.w.d.Model.Signals[id].Init)})
				}
				return ok
			}
			p, perr := m.staticPart(x)
			if perr == nil {
				out = appendTarget(out, p.t)
				return true
			}
			if !errors.Is(perr, ErrNotSignal) {
				err = perr
				return true
			}
			return false
		})
	return out, err
}

// staticPart is the longest static prefix of the signal name e
// (indices that are not constants select the whole prefix).
func (m *machine) staticPart(e *sema.Expr) (sigPart, error) {
	for x := e; x != nil; x = x.Prefix {
		p, err := m.sigPart(m.ev, x, m.b)
		if err == nil {
			return p, nil
		}
		switch x.Kind {
		case sema.ExprIndex, sema.ExprSlice, sema.ExprSelect:
			continue
		}
		return sigPart{}, ErrNotSignal
	}
	return sigPart{}, ErrNotSignal
}

// waitOn suspends the process on the parts on, until cond, for the
// timeout.
func (m *machine) waitOn(on []sim.Target, cond, timeout *sema.Expr,
	ev *sema.Evaluator,
) (sim.Wait, bool, error) {
	if m.sync > 0 {
		return sim.Wait{}, false, errors.New("a wait statement in a " +
			"function call")
	}
	out := sim.Wait{}
	s := &suspended{cond: cond, ev: ev}
	for _, t := range on {
		if !slices.Contains(out.On, t.Sig) {
			out.On = append(out.On, t.Sig)
		}
		if !(sigPart{t: t}).whole(m.w.d) {
			s.parts = append(s.parts, t)
		}
	}
	if len(s.parts) > 0 {
		// The kernel waits on whole signals: Until filters the
		// events of the other elements.
		s.parts = on
	}
	out.Until = cond != nil || len(s.parts) > 0
	if timeout != nil {
		t, err := ev.Eval(timeout, m.b)
		if err != nil {
			return sim.Wait{}, false, err
		}
		if t.I < 0 {
			return sim.Wait{}, false, errors.New("negative timeout in " +
				"a wait statement")
		}
		out.For, out.HasFor = t.I, true
	}
	m.wait = s
	return out, true, nil
}

// assert executes an assertion or report statement.
func (m *machine) assert(n *node) error {
	s := n.s
	ev, b := m.ev, m.b
	if s.Kind == sema.SeqAssert {
		ok, err := ev.Cond(s.Cond, b)
		if err != nil || ok {
			return err
		}
	}
	msg := "Assertion violation."
	if s.Kind == sema.SeqReport {
		msg = ""
	}
	if s.Report != nil {
		v, err := ev.Eval(s.Report, b)
		if err != nil {
			return err
		}
		msg = stringOf(v)
	}
	sev := trace.Error
	if s.Kind == sema.SeqReport {
		sev = trace.Note
	}
	if s.Severity != nil {
		v, err := ev.Eval(s.Severity, b)
		if err != nil {
			return err
		}
		sev = trace.Severity(v.I + 1)
	}
	return m.ctx.Report(sim.Report{Assertion: s.Kind == sema.SeqAssert,
		Severity: sev, Message: msg, Src: n.src})
}

// stringOf returns the text of a STRING value.
func stringOf(v eval.Value) string {
	b := make([]byte, len(v.E))
	for i, e := range v.E {
		b[i] = byte(e.I)
	}
	return string(b)
}

// wrapErr is err in the context of the object what ("what: err"),
// still unwrapping to err, so a missing native (*nativeError) inside
// an object's elaboration stays one.
func wrapErr(what string, err error) error {
	return fmt.Errorf("%s: %w", what, err)
}
