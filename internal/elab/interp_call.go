package elab

// interp_call.go calls subprograms: procedures as frames of the
// process (they may wait), functions to completion inside the
// expression calling them, built-ins (interp_builtin.go) natively.

import (
	"errors"

	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/ieeenative"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
)

// arg is one evaluated actual of a call.
type arg struct {
	formal *sema.Decl
	v      eval.Value // the value (in, inout; out: the actual's)
	dst    *place     // the actual of an out or inout variable
	sig    *sigPart   // the actual of a signal parameter
	given  bool       // associated (not a default)
}

// actualsOf returns the actuals of call e of x in parameter order.
func actualsOf(e *sema.Expr, x *sema.Decl) ([]*sema.Expr, error) {
	acts, err := sema.Actuals(e, x)
	if err != nil && x.Implicit && len(e.Args) <= len(x.Params) {
		acts = make([]*sema.Expr, len(x.Params))
		copy(acts, e.Args)
		for i := len(e.Args); i < len(x.Params); i++ {
			acts[i] = x.Params[i].Value
		}
		return acts, nil
	}
	return acts, err
}

// args evaluates the actuals of a procedure call.
func (m *machine) args(e *sema.Expr, x *sema.Decl) ([]arg, error) {
	acts, err := actualsOf(e, x)
	if err != nil {
		return nil, err
	}
	out := make([]arg, len(x.Params))
	for i, p := range x.Params {
		a := acts[i]
		out[i].formal = p
		out[i].given = a != nil && a != p.Value
		if a == nil {
			continue
		}
		switch {
		case p.Class == sema.ClassSignal:
			sp, err := m.sigPart(m.ev, a, m.b)
			if err != nil {
				return nil, errors.New("the actual of signal parameter " +
					p.Text + ": " + err.Error())
			}
			out[i].sig = &sp
		case p.Mode == sema.ModeOut || p.Mode == sema.ModeInout ||
			p.Mode == sema.ModeBuffer:
			dst, err := m.placeOf(a)
			if err != nil {
				return nil, err
			}
			v, err := m.placeValue(dst)
			if err != nil {
				return nil, err
			}
			out[i].dst, out[i].v = &dst, v
		default:
			v, err := m.ev.Eval(a, m.b)
			if err != nil {
				return nil, err
			}
			out[i].v = v
		}
	}
	return out, nil
}

// procedure executes a procedure call statement.
func (m *machine) procedure(n *node) (sim.Wait, bool, error) {
	e := n.s.Call
	if e == nil || e.Decl == nil {
		return sim.Wait{}, false, errors.New("call of an unknown " +
			"procedure")
	}
	// A generic procedure of a running instance: its actual.
	e, err := m.ev.Dispatch(e)
	if err != nil {
		return sim.Wait{}, false, err
	}
	x := e.Decl.Denotes()
	var body *sema.Decl
	if !x.Implicit {
		body = m.ev.Body(x)
	}
	var obj int64
	if mo := methodObj(e.Decl); mo != nil {
		// A method alias (VHDL-2019): the method of the object the
		// alias names, in the frame of the call's object.
		var outer int64
		if e.Prefix != nil {
			ov, err := m.ev.Eval(e.Prefix, m.b)
			if err != nil {
				return sim.Wait{}, false, err
			}
			outer = ov.I
		}
		if obj, err = m.methodObject(outer, mo, m.b); err != nil {
			return sim.Wait{}, false, err
		}
	} else if e.Prefix != nil && x.Parent != nil &&
		x.Parent.Kind == sema.RegionProtected {
		ov, err := m.ev.Eval(e.Prefix, m.b)
		if err != nil {
			return sim.Wait{}, false, err
		}
		obj = ov.I
	}
	args, err := m.args(e, x)
	if err != nil {
		return sim.Wait{}, false, err
	}
	if body == nil {
		stop, err := m.builtinProc(n, x, obj, args)
		if err != nil || !stop {
			return sim.Wait{}, false, err
		}
		// std.env.stop or finish: the process does not go on.
		m.frames = m.frames[:1]
		m.frames[0].conts = nil
		m.dead = true
		return sim.Wait{}, true, nil
	}
	f, err := m.enter(body, e, obj, args)
	if err != nil {
		return sim.Wait{}, false, err
	}
	for i, a := range args {
		if a.dst != nil {
			f.outs = append(f.outs, outArg{formal: body.Params[i],
				dst: *a.dst})
		}
	}
	return sim.Wait{}, false, nil
}

// enter pushes the frame of a call of body and elaborates its
// parameters and declarations.
func (m *machine) enter(body *sema.Decl, e *sema.Expr, obj int64,
	args []arg,
) (*frame, error) {
	if len(m.frames) > maxFrames {
		return nil, eval.ErrBudget
	}
	ss, err := m.w.prog.seq(body.File, body.Seq)
	if err != nil {
		return nil, err
	}
	// A subprogram instance: its generics are bound in the caller's
	// frame, then become objects of the call's (LRM 4.4, 9.3.4).
	var in *sema.Instance
	if e != nil && e.Decl != nil && sema.InstanceOf(e.Decl.Denotes()) != nil {
		if in, err = m.ev.Instance(e.Decl.Denotes(), m.b); err != nil {
			return nil, err
		}
	}
	m.frames = append(m.frames, frame{sub: body, call: e, obj: obj,
		vars: map[*sema.Decl]slot{}})
	fi := len(m.frames) - 1
	if obj != 0 {
		// A method: its object's generics (VHDL-2019).
		m.objectFrame(fi, obj)
	}
	if in != nil {
		for _, g := range in.Generics {
			// A generic subprogram's slot marks the frame that binds
			// it (GenericSubprogram).
			m.frames[fi].vars[g.Decl] = slot{v: g.Value, t: g.Layout}
		}
	}
	for i, p := range body.Params {
		if i >= len(args) {
			break
		}
		a := args[i]
		if a.sig != nil {
			m.frames[fi].vars[p] = slot{sig: a.sig}
			continue
		}
		t, err := m.ev.LayoutIn(p.Type, m.b)
		if err != nil {
			m.frames = m.frames[:fi]
			return nil, err
		}
		v := a.v
		if p.Mode == sema.ModeOut {
			// An out parameter starts at its subtype's default,
			// constrained like its actual (LRM 4.2.2.1).
			dt := t
			if !t.Constrained() && v.Kind() == eval.KindArray {
				dt = t.ArraySubtype(v.B...)
			}
			if d, err := eval.Default(m.b, dt); err == nil {
				if d, err = m.instantiate(d, p.Type); err != nil {
					m.frames = m.frames[:fi]
					return nil, err
				}
				v = d
			}
		}
		if p.Class != sema.ClassFile {
			if !t.Constrained() && v.Kind() == eval.KindArray {
				t = t.ArraySubtype(v.B...)
			}
			if v, err = eval.Check(v, t); err != nil {
				m.frames = m.frames[:fi]
				return nil, errors.New("parameter " + p.Text + ": " +
					err.Error())
			}
		}
		m.frames[fi].vars[p] = slot{v: v, t: t}
	}
	if body.Region != nil {
		if err := m.declare(body.Region); err != nil {
			m.frames = m.frames[:fi]
			return nil, err
		}
	}
	m.frames[fi].conts = []cont{{ss: ss}}
	return &m.frames[fi], nil
}

// maxFrames bounds the call depth (P7).
const maxFrames = 2048

// callFunction calls a function the interpreter runs.
func (m *machine) callFunction(fn *callFn, vals []eval.Value,
	b *eval.Budget,
) (eval.Value, error) {
	if m.b == nil {
		m.b = b
	}
	var obj int64
	if fn.method {
		obj, vals = vals[0].I, vals[1:]
	}
	args := make([]arg, len(fn.x.Params))
	for i := range args {
		args[i].formal = fn.x.Params[i]
		if i < len(fn.parts) && fn.parts[i] != nil {
			args[i].sig = fn.parts[i]
		} else if i < len(vals) {
			args[i].v = vals[i]
		}
	}
	if fn.body == nil {
		return m.builtinFunc(fn, obj, args)
	}
	if fn.edge != 0 {
		if v, ok, err := m.edge(fn, args, b); ok {
			return v, err
		}
	}
	outs, err := m.funcOuts(fn)
	if err != nil {
		return eval.Value{}, err
	}
	base := len(m.frames)
	f, err := m.enter(fn.body, fn.e, obj, args)
	if err != nil {
		return eval.Value{}, err
	}
	f.fn = true
	f.outs = outs
	m.sync++
	_, _, err = m.run(base)
	m.sync--
	if err != nil {
		m.frames = m.frames[:base]
		return eval.Value{}, err
	}
	if !m.lastReturned {
		return eval.Value{}, errors.New("function " + fn.x.Text +
			" ended without a return statement")
	}
	v := m.lastRet
	m.lastRet, m.lastReturned = eval.Value{}, false
	return v, nil
}

// funcOuts returns the out and inout variable parameters of the
// function call fn (VHDL-2019) with their actuals' places, resolved
// in the caller's frame before the call; leave assigns them when the
// function returns.
func (m *machine) funcOuts(fn *callFn) ([]outArg, error) {
	var outs []outArg
	var acts []*sema.Expr
	for i, p := range fn.x.Params {
		if p.Class == sema.ClassSignal || p.Class == sema.ClassFile ||
			p.Mode != sema.ModeOut && p.Mode != sema.ModeInout {
			continue
		}
		if acts == nil {
			var err error
			if acts, err = actualsOf(fn.e, fn.x); err != nil {
				return nil, err
			}
		}
		if i >= len(acts) || acts[i] == nil || acts[i] == p.Value ||
			i >= len(fn.body.Params) {
			continue
		}
		dst, err := m.placeOf(acts[i])
		if err != nil {
			return nil, err
		}
		outs = append(outs, outArg{formal: fn.body.Params[i], dst: dst})
	}
	return outs, nil
}

// objectResult reports whether the result subtype of the call e of
// function x is known only to x's object: x is a method of a generic
// protected type called on an object (VHDL-2019), so its result may
// be of a generic type the object binds (bindGenerics). The method's
// frame checks the result (ret).
func objectResult(e *sema.Expr, x *sema.Decl) bool {
	if e.Prefix == nil || x.Parent == nil ||
		x.Parent.Kind != sema.RegionProtected {
		return false
	}
	for _, d := range x.Parent.Decls {
		if d.Kind == sema.DeclGenericType {
			return true
		}
	}
	return false
}

// methodObj returns the object prefix of the method alias d (or of the
// alias it names), nil when d is no method alias.
func methodObj(d *sema.Decl) *sema.Expr {
	for range 64 {
		if d == nil || d.Kind != sema.DeclAlias {
			return nil
		}
		if d.MethodObj != nil {
			return d.MethodObj
		}
		if d.Target == d {
			return nil
		}
		d = d.Target
	}
	return nil
}

// methodObject evaluates the object prefix mo of a method alias in the
// frame of the protected object outer (0: the current frames, a call
// in the protected body itself) and returns the object's handle.
func (m *machine) methodObject(outer int64, mo *sema.Expr, b *eval.Budget,
) (int64, error) {
	if outer != 0 {
		m.frames = append(m.frames, frame{obj: outer,
			vars: map[*sema.Decl]slot{}})
		defer func(n int) { m.frames = m.frames[:n] }(len(m.frames) - 1)
		m.objectFrame(len(m.frames)-1, outer)
	}
	v, err := m.ev.Eval(mo, b)
	if err != nil {
		return 0, err
	}
	if v.I == 0 {
		return 0, errors.New("the object of method alias " +
			mo.Node.Text() + " is not elaborated")
	}
	return v.I, nil
}

// edgeOf returns the native body of x: +1 for ieee.std_logic_1164's
// rising_edge, -1 for its falling_edge (as bundled), else 0.
func edgeOf(x *sema.Decl) int8 {
	u := x.Unit
	if u == nil || u.File == nil || !u.File.Bundled ||
		u.Library != "ieee" || u.Name != "std_logic_1164" ||
		len(x.Params) != 1 || x.Params[0].Class != sema.ClassSignal {
		return 0
	}
	switch x.Name {
	case "rising_edge":
		return 1
	case "falling_edge":
		return -1
	}
	return 0
}

// edge is the native body of rising_edge (fn.edge +1) and falling_edge
// (-1) on the signal part of args[0] (#59): s'event and To_X01(s) =
// '1' and To_X01(s'last_value) = '0' (for falling_edge '0' and '1'),
// evaluated left to right with short-circuit and. ok false declines
// (no running process, a value outside STD_ULOGIC, an error), and the
// body is then interpreted.
func (m *machine) edge(fn *callFn, args []arg, b *eval.Budget,
) (eval.Value, bool, error) {
	if m.ctx == nil || len(args) != 1 || args[0].sig == nil ||
		fn.ret == nil {
		return eval.Value{}, false, nil
	}
	p := *args[0].sig
	ev, err := m.ctx.PartEvent(p.t)
	if err != nil {
		return eval.Value{}, false, nil
	}
	if err := b.Step(1); err != nil {
		return eval.Value{}, true, err
	}
	if !ev {
		return eval.Enum(fn.ret, 0), true, nil
	}
	now, want := int64(3), int64(2) // '1', then '0' (rising_edge)
	if fn.edge < 0 {
		now, want = 2, 3
	}
	v, err := m.sigValue(p)
	x, ok := ieeenative.ToX01(v)
	if err != nil || !ok {
		return eval.Value{}, false, nil
	}
	if x != now {
		return eval.Enum(fn.ret, 0), true, nil
	}
	flat, err := m.ctx.PartLastValues(p.t)
	if err != nil || len(flat) != 1 {
		return eval.Value{}, false, nil
	}
	x, ok = ieeenative.ToX01(flat[0])
	if !ok {
		return eval.Value{}, false, nil
	}
	if x == want {
		return eval.Enum(fn.ret, 1), true, nil
	}
	return eval.Enum(fn.ret, 0), true, nil
}
