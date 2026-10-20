package elab

import (
	"slices"

	"github.com/connerohnesorge/gohard/internal/elab/hier"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// charging is an Indexer that charges the design's step budget.
type charging struct {
	d  *Design
	ev *sema.Evaluator
}

func (c charging) Value(e *sema.Expr) (eval.Value, error) {
	v, err := c.ev.Value(e)
	if c.d.charge(c.ev.Used(), sim.SrcRef{}) {
		return v, eval.ErrBudget
	}
	return v, err
}

func (c charging) Range(rc *sema.RangeCons) (eval.Range, error) {
	r, err := c.ev.Range(rc)
	if c.d.charge(c.ev.Used(), sim.SrcRef{}) {
		return r, eval.ErrBudget
	}
	return r, err
}

func dirOf(m sema.Mode) trace.Dir {
	switch m {
	case sema.ModeIn:
		return trace.In
	case sema.ModeOut:
		return trace.Out
	case sema.ModeInout:
		return trace.Inout
	case sema.ModeBuffer:
		return trace.Buffer
	case sema.ModeLinkage:
		return trace.Linkage
	case sema.ModeNone:
		return trace.In // a port without a mode is in
	}
	return trace.Inout
}

// ports declares the ports of an entity instance. A port whose actual
// is a signal name (or an indexed, sliced or selected name of one, with
// static indices) is collapsed onto the actual's elements; an open or
// unassociated port, a port of the top, or a port with an expression
// actual is a signal of its own, initialized from its default or from
// the actual's value.
func (d *Design) ports(in *hier.Instance) {
	parent := d.parent[in]
	for _, x := range in.Entity.Region.Decls {
		if d.stopped {
			return
		}
		if x.Kind != sema.DeclPort || x.Class == sema.ClassConstant ||
			x.Class == sema.ClassVariable || x.Class == sema.ClassFile {
			continue
		}
		if parent != nil {
			if parts := d.partsOf(in, x); parts != nil {
				d.partialPort(in, parent, x, parts)
				continue
			}
		}
		actual := d.actualOf(in, x)
		var tgt sim.Target
		var shape eval.Value
		collapse := false
		if actual != nil && parent != nil &&
			actual.Kind != sema.ExprOpen {
			t, sh, err := d.Target(parent, sameValues(actual),
				charging{d, parent.Evaluator()})
			if d.stopped {
				return
			}
			if err == nil {
				tgt, shape, collapse = t, sh, true
			}
		}
		var shp *eval.Value
		if collapse {
			shp = &shape
		}
		t, init, ok := d.layout(in, x, shp)
		if !ok {
			continue
		}
		sig := sim.Signal{Path: in.Path + "." + string(x.Name), Type: t,
			Init: init, Dir: dirOf(x.Mode), Src: d.declSrc(x)}
		if x.Mode == sema.ModeView && x.View != nil {
			sig.LeafDirs = viewDirs(x.View, init)
			sig.Dir = trace.Inout
		}
		if collapse && leaves(init) == tgt.N {
			sig.Collapsed, sig.Of, sig.Offset = true, tgt.Sig, tgt.First
		} else {
			if collapse {
				d.runtime(sig.Src, in.Path, eval.LengthError(
					int64(tgt.N), int64(leaves(init))))
				continue
			}
			if actual != nil && actual.Kind != sema.ExprOpen &&
				parent != nil {
				// An expression actual (a constant, a function of
				// constants): the port's initial value.
				ev := parent.Evaluator()
				v, err := ev.Value(actual)
				if d.charge(ev.Used(), sig.Src) {
					return
				}
				if err == nil {
					if v, err = eval.Check(v, t); err == nil {
						sig.Init = v
					}
				}
				if err != nil {
					d.unsupported(sig.Src, "port actual "+
						"that is not a signal name or a static value")
				}
			}
			d.resolution(&sig, x.Type)
		}
		d.add(in, x, sig)
	}
}

// sameValues returns the operand of a type conversion actual that
// changes no scalar value, only the subtype (std_logic(u) of a
// std_ulogic u, or between arrays of one element base type): the port
// then shares the operand's net elements (LRM 6.5.7.1, 9.3.6). Any
// other actual is returned as it is.
func sameValues(e *sema.Expr) *sema.Expr {
	for e != nil && e.Kind == sema.ExprConversion && len(e.Args) == 1 &&
		e.Args[0] != nil && sameLeaves(e.Type, e.Args[0].Type) {
		e = e.Args[0]
	}
	return e
}

// sameLeaves reports whether values of a and b have the same scalar
// subelements of the same base types.
func sameLeaves(a, b *sema.Type) bool {
	if a.Unknown() || b.Unknown() {
		return false
	}
	ab, bb := a.Base(), b.Base()
	if ab == bb {
		return true
	}
	if ab.Class != sema.TypeArray || bb.Class != sema.TypeArray ||
		a.Dims() != b.Dims() {
		return false
	}
	return sameLeaves(a.ElemType(), b.ElemType())
}

// partsOf returns the subelement associations of entity port x of in
// (LRM 6.5.7.1), or nil when x is associated as a whole.
func (d *Design) partsOf(in *hier.Instance, x *sema.Decl,
) []sema.Association {
	if in.Stmt == nil {
		return nil
	}
	var out []sema.Association
	for _, a := range in.Stmt.Ports {
		if a.Part != nil && a.Formal != nil && (a.Formal == x ||
			a.Formal.Name == x.Name && a.Formal.Kind == sema.DeclPort) {
			out = append(out, a)
		}
	}
	return out
}

// partialPort declares port x of in, associated subelement by
// subelement: each formal part shares the net elements of its actual,
// so the port is the concatenation of the actuals' parts.
func (d *Design) partialPort(in, parent *hier.Instance, x *sema.Decl,
	parts []sema.Association,
) {
	t, init, ok := d.layout(in, x, nil)
	if !ok {
		return
	}
	src := d.declSrc(x)
	ix := charging{d, parent.Evaluator()}
	type piece struct {
		first int
		tgt   sim.Target
	}
	var ps []piece
	for _, a := range parts {
		_, first, sub, err := partFrom(a.Part, ix,
			func(*sema.Expr) (sim.SigID, int, eval.Value, error) {
				return 0, 0, init, nil
			})
		if d.stopped {
			return
		}
		if err != nil {
			d.runtime(src, in.Path, err)
			return
		}
		if a.Actual == nil || a.Actual.Kind == sema.ExprOpen {
			d.unsupported(src, "port associated in part with open")
			return
		}
		tgt, _, err := d.Target(parent, sameValues(a.Actual), ix)
		if d.stopped {
			return
		}
		if err != nil {
			d.unsupported(src, "port subelement actual that is not "+
				"a signal name")
			return
		}
		if tgt.N != leaves(sub) {
			d.runtime(src, in.Path, eval.LengthError(int64(leaves(sub)),
				int64(tgt.N)))
			return
		}
		ps = append(ps, piece{first, tgt})
	}
	slices.SortStableFunc(ps, func(a, b piece) int {
		return a.first - b.first
	})
	next := 0
	pieces := make([]sim.Target, 0, len(ps))
	for _, p := range ps {
		if p.first != next {
			break
		}
		next += p.tgt.N
		pieces = append(pieces, p.tgt)
	}
	if next != leaves(init) {
		d.unsupported(src, "port whose subelements are not each "+
			"associated exactly once")
		return
	}
	sig := sim.Signal{Path: in.Path + "." + string(x.Name), Type: t,
		Init: init, Dir: dirOf(x.Mode), Src: src, Collapsed: true,
		Of: pieces[0].Sig, Offset: -1, Pieces: pieces}
	d.add(in, x, sig)
}

// actualOf returns the actual associated with entity port x of in, or
// nil. A component instance's formals are the component's ports,
// matched to the entity's by name (default binding).
func (d *Design) actualOf(in *hier.Instance, x *sema.Decl) *sema.Expr {
	if in.Stmt == nil {
		return nil
	}
	ports := entityPorts(in.Entity.Region)
	pos := -1
	for i, p := range ports {
		if p == x {
			pos = i
		}
	}
	formals := ports
	if in.Component != nil && in.Component.Region != nil {
		formals = entityPorts(in.Component.Region)
	}
	for i, a := range in.Stmt.Ports {
		switch {
		case a.Formal == x:
			return a.Actual
		case a.Formal != nil && a.Formal.Name == x.Name &&
			a.Formal.Kind == sema.DeclPort:
			return a.Actual
		case a.Formal == nil && i < len(formals) && i == pos:
			return a.Actual // positional
		}
	}
	return nil
}

func entityPorts(r *sema.Region) []*sema.Decl {
	var out []*sema.Decl
	if r == nil {
		return nil
	}
	for _, x := range r.Decls {
		if x.Kind == sema.DeclPort {
			out = append(out, x)
		}
	}
	return out
}

// viewDirs returns the mode of every scalar subelement of a port of
// mode view v (#17): each record element has its view element's mode,
// a nested view recurses and an array view applies to every element.
// Sema has already applied 'converse to the elements.
func viewDirs(v *sema.View, init eval.Value) []trace.Dir {
	var out []trace.Dir
	var walk func(v *sema.View, val eval.Value)
	walk = func(v *sema.View, val eval.Value) {
		for i, f := range val.E {
			var el *sema.ViewElement
			for _, e := range v.Elements {
				if e.Field != nil && e.Field.Pos == i {
					el = e
				}
			}
			if el == nil {
				for range leaves(f) {
					out = append(out, trace.Inout)
				}
				continue
			}
			if el.Mode == sema.ModeView && el.View != nil {
				if el.Array {
					for _, x := range f.E {
						walk(el.View, x)
					}
				} else {
					walk(el.View, f)
				}
				continue
			}
			m := dirOf(el.Mode)
			for range leaves(f) {
				out = append(out, m)
			}
		}
	}
	if init.Kind() == eval.KindRecord {
		walk(v, init)
	}
	return out
}
