package elab

// interp_exec.go executes statements: assignments, control flow,
// waits, assertions and subprogram calls.

import (
	"errors"
	"slices"

	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// exec executes one statement; susp reports a wait.
func (m *machine) exec(n *node) (sim.Wait, bool, error) {
	s := n.s
	ev, b := m.ev, m.b
	switch s.Kind {
	case sema.SeqNull:
	case sema.SeqVarAssign:
		v, err := ev.Eval(s.Value, b)
		if err != nil {
			return sim.Wait{}, false, err
		}
		return sim.Wait{}, false, m.assignVar(s.Target, v)
	case sema.SeqCondVarAssign:
		for _, br := range s.Branches {
			ok, err := ev.Cond(br.Cond, b)
			if err != nil {
				return sim.Wait{}, false, err
			}
			if ok {
				if br.Value == nil {
					break
				}
				v, err := ev.Eval(br.Value, b)
				if err != nil {
					return sim.Wait{}, false, err
				}
				return sim.Wait{}, false, m.assignVar(s.Target, v)
			}
		}
	case sema.SeqSelVarAssign:
		i, err := m.choose(s.Selector, altChoices(s.Alts), altOthers(s.Alts),
			s.Matching)
		if err != nil || i < 0 || s.Alts[i].Value == nil {
			return sim.Wait{}, false, err
		}
		v, err := ev.Eval(s.Alts[i].Value, b)
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
				m.push(n.branches[i], nil)
				return sim.Wait{}, false, nil
			}
		}
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

// leave pops a finished subprogram frame: a procedure's out and inout
// variable parameters are assigned to their actuals.
func (m *machine) leave() error {
	fi := len(m.frames) - 1
	f := m.frames[fi]
	m.frames = m.frames[:fi]
	if f.fn {
		// callFunction reads the result.
		m.lastRet, m.lastReturned = f.ret, f.returned
		return nil
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
	for _, d := range r.Decls {
		if err := m.b.Step(1); err != nil {
			return err
		}
		switch d.Kind {
		case sema.DeclVariable, sema.DeclConstant:
			s, err := m.object(d)
			if err != nil {
				return errors.New(d.Text + ": " + err.Error())
			}
			m.frames[len(m.frames)-1].vars[d] = s
		case sema.DeclFile:
			s, err := m.fileObject(d)
			if err != nil {
				return errors.New(d.Text + ": " + err.Error())
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
		if !t.Constrained() && v.Kind() == eval.KindArray {
			t = t.ArraySubtype(v.B...)
		}
		if v, err = eval.Check(v, t); err != nil {
			return slot{}, err
		}
	} else {
		if v, err = eval.Default(m.b, t); err != nil {
			return slot{}, err
		}
	}
	if t.Kind == eval.KindProtected {
		h, err := m.newObject(d.Type)
		if err != nil {
			return slot{}, err
		}
		v = eval.Value{T: t, I: h}
	}
	return slot{v: v, t: t}, nil
}

// shared returns the shared variable or package file d, elaborating
// it on first use.
func (m *machine) shared(d *sema.Decl) (slot, error) {
	if s, ok := m.w.shared[d]; ok {
		return s, nil
	}
	var s slot
	var err error
	if d.Kind == sema.DeclFile {
		s, err = m.fileObject(d)
	} else {
		s, err = m.object(d)
	}
	if err != nil {
		return slot{}, errors.New(d.Text + ": " + err.Error())
	}
	m.w.shared[d] = s
	return s, nil
}

// newObject creates a protected object of type t: the variables of
// its body.
func (m *machine) newObject(t *sema.Type) (int64, error) {
	body := m.w.protectedBody(t)
	if body == nil {
		return 0, errors.New("no protected type body for " + t.Name())
	}
	h := m.w.handle()
	o := &protObj{typ: t, vars: map[*sema.Decl]slot{}}
	m.w.objs[h] = o
	m.frames = append(m.frames, frame{vars: map[*sema.Decl]slot{},
		obj: h})
	defer func() { m.frames = m.frames[:len(m.frames)-1] }()
	for _, d := range body.Decls {
		switch d.Kind {
		case sema.DeclVariable, sema.DeclConstant:
			s, err := m.object(d)
			if err != nil {
				return 0, errors.New(d.Text + ": " + err.Error())
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

// protectedBody finds the body region of protected type t.
func (w *world) protectedBody(t *sema.Type) *sema.Region {
	td := t.Base().Decl
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
			if _, err := m.shared(x); err != nil {
				return place{}, err
			}
			return place{kind: placeShared, d: x}, nil
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
		s, err := m.shared(p.d)
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
		s := m.w.shared[p.d]
		s.v = v
		m.w.shared[p.d] = s
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
			return errors.New("named associations in an aggregate " +
				"target are not supported in simulation")
		}
		if err := set(a.Value, v.E[i]); err != nil {
			return err
		}
	}
	return nil
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
	case sigSelected:
		i, err := m.choose(g.selector, g.choices, g.others, g.matching)
		if err != nil {
			return err
		}
		if i < 0 {
			return errors.New("no choice covers the selector value")
		}
		k = i
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
		v, err := ev.Eval(w.value, b)
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
				return nil, errors.New("named associations in an " +
					"aggregate target are not supported in simulation")
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
