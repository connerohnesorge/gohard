package elab

import (
	"slices"
	"strconv"

	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/elab/hier"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/sim"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// Stand-in models (#35, #14 entry 5): simulation.models maps an opaque
// unit lib.unit to a plain VHDL model, which sema analyzes in its
// place (the opaque original stays in Result.Units as a Modeled
// duplicate). Elaboration checks the stand-in's ports against the
// original's, marks every process of a stand-in instance (and of the
// instances below it) sim.Process.Modeled and every signal such a
// process drives sim.Signal.Modeled, so the trace marks all of it.

// original returns the opaque unit the entity e stands in for, or nil.
func (d *Design) original(e *sema.Decl) *sema.Unit {
	if e == nil || e.Unit == nil || d.r == nil {
		return nil
	}
	u := e.Unit
	for _, o := range d.r.Units {
		if o != u && o.Modeled && o.Kind == u.Kind &&
			o.Library == u.Library && o.Name == u.Name {
			return o
		}
	}
	return nil
}

// standIn reports whether instance in runs stand-in code: its entity
// is a stand-in, or it is inside an instance that is. The ports of a
// stand-in entity are checked once per entity.
func (d *Design) standIn(in *hier.Instance) bool {
	if v, ok := d.modeled[in]; ok {
		return v
	}
	v := false
	if p := d.parent[in]; p != nil && d.standIn(p) {
		v = true
	}
	if in.Kind == hier.KindEntity {
		if o := d.original(in.Entity); o != nil {
			v = true
			if !slices.Contains(d.checked, in.Entity) {
				d.checked = append(d.checked, in.Entity)
				d.checkPorts(in, o)
			}
		}
	}
	d.modeled[in] = v
	if v && in.Kind == hier.KindEntity {
		d.modeledInsts = append(d.modeledInsts, in)
	}
	return v
}

// checkPorts reports sim/model-ports when the ports of the stand-in
// entity of in differ from those of the opaque original o: in number,
// name, order, mode or subtype (base type and evaluated bounds).
func (d *Design) checkPorts(in *hier.Instance, o *sema.Unit) {
	st := entityPorts(in.Entity.Region)
	var op []*sema.Decl
	if o.Decl != nil {
		op = entityPorts(o.Decl.Region)
	}
	model := ""
	if in.Entity.File != nil {
		model = in.Entity.File.Path
	}
	unit := string(o.Library) + "." + string(o.Name)
	problem := portsProblem(in.Evaluator(), st, op)
	if problem == "" {
		return
	}
	dg := diag.New(diag.SimModelPorts, model, unit, problem)
	if src := d.declSrc(in.Entity); src.Path != "" {
		dg = dg.At(src.Path, src.Line, src.Col)
	}
	d.diags = append(d.diags, dg)
}

// portsProblem describes the first difference between the stand-in
// ports st and the original ports op, or returns "".
func portsProblem(ev *sema.Evaluator, st, op []*sema.Decl) string {
	if len(st) != len(op) {
		return "it has " + strconv.Itoa(len(st)) + " ports, the " +
			"original has " + strconv.Itoa(len(op))
	}
	for i, s := range st {
		x := op[i]
		switch {
		case s.Name != x.Name:
			return "port " + strconv.Itoa(i+1) + " is " + s.Text +
				", the original's is " + x.Text
		case s.Mode != x.Mode:
			return "port " + s.Text + " has mode " + dirOf(s.Mode).
				String() + ", the original's has mode " +
				dirOf(x.Mode).String()
		case !sameSubtype(ev, s, x):
			return "port " + s.Text + " is of subtype " +
				s.Type.Name() + ", the original's is of subtype " +
				x.Type.Name()
		}
	}
	return ""
}

// sameSubtype compares the subtypes of two ports: the same base type
// name and kind and, when both evaluate, the same bounds.
func sameSubtype(ev *sema.Evaluator, a, b *sema.Decl) bool {
	if a.Type == nil || b.Type == nil {
		return a.Type == b.Type
	}
	ba, bb := a.Type.Base(), b.Type.Base()
	if ba != nil && bb != nil && ba.Name() != bb.Name() {
		return false
	}
	ta, ea := ev.Layout(a.Type)
	tb, eb := ev.Layout(b.Type)
	if ea != nil || eb != nil {
		return true // generic-dependent: the names agree
	}
	return sameLayout(ta, tb)
}

func sameLayout(a, b *eval.Type) bool {
	if a.Kind != b.Kind || a.BaseType().Name != b.BaseType().Name ||
		!slices.Equal(a.Bounds, b.Bounds) || a.Range != b.Range {
		return false
	}
	if (a.Elem == nil) != (b.Elem == nil) ||
		a.Elem != nil && !sameLayout(a.Elem, b.Elem) {
		return false
	}
	if len(a.Fields) != len(b.Fields) {
		return false
	}
	for i := range a.Fields {
		if !sameLayout(a.Fields[i].Type, b.Fields[i].Type) {
			return false
		}
	}
	return true
}

// markModeled marks the signals driven by stand-in processes.
func (d *Design) markModeled() {
	for i := range d.Model.Processes {
		p := &d.Model.Processes[i]
		if !p.Modeled {
			continue
		}
		for _, t := range p.Drivers {
			d.markChain(t.Sig)
		}
	}
	// A port of a stand-in instance of mode out, inout or buffer is
	// driven by the stand-in even before its first assignment.
	for _, in := range d.modeledInsts {
		for _, x := range entityPorts(in.Entity.Region) {
			switch dirOf(x.Mode) {
			case trace.Out, trace.Inout, trace.Buffer:
				if id, ok := d.sigs[sigKey{in, x}]; ok {
					d.markChain(id)
				}
			}
		}
	}
}

// markChain marks s modeled and, for a collapsed port, the actual's
// signals it shares its net with.
func (d *Design) markChain(s sim.SigID) {
	for int(s) >= 0 && int(s) < len(d.Model.Signals) {
		d.Model.Signals[s].Modeled = true
		if !d.Model.Signals[s].Collapsed {
			return
		}
		s = d.Model.Signals[s].Of
	}
}
