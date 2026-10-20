package elab

import (
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
		actual := d.actualOf(in, x)
		var tgt sim.Target
		var shape eval.Value
		collapse := false
		if actual != nil && parent != nil &&
			actual.Kind != sema.ExprOpen {
			t, sh, err := d.Target(parent, actual,
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
