package hier

// checks.go binds generics and runs the per-instance static checks
// (#34): what elaboration evaluates under an instance's generics. An
// error that does not depend on the instance (Evaluator.Track) is the
// unit's and sema reports it once; the others name the instance.

import (
	"strconv"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// defaults binds every generic of a top to its default value.
func (b *builder) defaults(in *Instance) {
	for _, g := range interfaceDecls(in.Entity.Region, true) {
		gen := Generic{Decl: g}
		if g.Kind == sema.DeclGeneric && g.Value != nil {
			b.value(in, &gen, in.ev, in.ev, g.Value, g)
		} else {
			gen.Err = eval.NotStatic("generic " + g.Text)
		}
		if gen.Known {
			in.ev = in.ev.Bind(g, gen.Value)
		}
		in.Generics = append(in.Generics, gen)
	}
}

// generics binds the generics of the instance created by s. The
// actuals of s are evaluated by the parent's evaluator. For a component
// instance the component's generics are bound first (actual or
// component default); the entity's generics then take the binding's
// generic map actual, else the component generic of the same name
// (the default binding, LRM 7.3.3), else their own default.
func (b *builder) generics(in *Instance, s *sema.Stmt, bd *binding,
	parent *sema.Evaluator,
) {
	// partial marks generics associated element by element: their
	// value is not computed here.
	partial := map[*sema.Decl]bool{}
	for _, as := range s.Generics {
		if as.Formal != nil && !wholeFormal(as) {
			partial[as.Formal] = true
		}
	}
	actual := func(g *sema.Decl) *sema.Expr {
		for _, as := range s.Generics {
			if as.Formal == g && !partial[g] {
				return as.Actual
			}
		}
		return nil
	}
	isPartial := func(g *sema.Decl) bool { return partial[g] }
	comp := map[sema.Key]Generic{}
	compEv := b.base.Fresh()
	if s.Component != nil {
		for _, g := range interfaceDecls(s.Component.Region, true) {
			gen := Generic{Decl: g}
			a := actual(g)
			switch {
			case g.Kind == sema.DeclGenericType:
				if a != nil && a.Kind == sema.ExprTypeMark {
					gen.Type = a.Type
					compEv = compEv.BindType(g, a.Type)
				}
			case g.Kind != sema.DeclGeneric:
			case isPartial(g):
				gen.Err = eval.NotStatic("generic " + g.Text)
			case a != nil:
				b.value(in, &gen, parent, compEv, a, g)
			case g.Value != nil:
				b.value(in, &gen, compEv, compEv, g.Value, g)
			default:
				gen.Err = eval.NotStatic("generic " + g.Text)
			}
			if gen.Known {
				compEv = compEv.Bind(g, gen.Value)
			}
			comp[g.Name] = gen
		}
	}
	child := b.base.Fresh()
	for _, g := range interfaceDecls(in.Entity.Region, true) {
		gen := Generic{Decl: g}
		ba := bd.generic(g)
		cg, fromComp := comp[g.Name]
		switch {
		case g.Kind == sema.DeclGenericType:
			var t *sema.Type
			switch {
			case ba != nil && ba.actual.Kind == sema.ExprTypeMark:
				t = ba.actual.Type
			case s.Component == nil:
				if a := actual(g); a != nil &&
					a.Kind == sema.ExprTypeMark {
					t = a.Type
				}
			case fromComp:
				t = cg.Type
			}
			if t != nil {
				gen.Type = t
				child = child.BindType(g, t)
			}
		case g.Kind != sema.DeclGeneric:
		case s.Component == nil && isPartial(g):
			gen.Err = eval.NotStatic("generic " + g.Text)
		case ba != nil:
			b.value(in, &gen, compEv, child, ba.actual, g)
		case s.Component == nil && actual(g) != nil:
			b.value(in, &gen, parent, child, actual(g), g)
		case s.Component != nil && fromComp:
			if cg.Known {
				b.check(in, &gen, child, cg.Value, g)
			} else {
				gen.Err = cg.Err
			}
		case g.Value != nil:
			b.value(in, &gen, child, child, g.Value, g)
		default:
			gen.Err = eval.NotStatic("generic " + g.Text)
		}
		if gen.Known {
			child = child.Bind(g, gen.Value)
		}
		in.Generics = append(in.Generics, gen)
	}
	in.ev = child
}

// value evaluates the actual e of the generic gen with ev and checks
// it against the formal's subtype, which fev evaluates (the instance's
// generics declared before the formal, LRM 6.5.6.2).
func (b *builder) value(in *Instance, gen *Generic, ev, fev *sema.Evaluator,
	e *sema.Expr, formal *sema.Decl,
) {
	if b.skip(in, nil, cst.Node{}, nil) {
		gen.Err = eval.ErrBudget
		return
	}
	// An actual is code of the parent instance, a default of in.
	ctx := in
	if in.parent != nil && ev == in.parent.ev {
		ctx = in.parent
	}
	nev := named(ev, ctx, in, e)
	v, err := nev.Value(e)
	b.spend(nev)
	if err != nil {
		b.genericError(in, gen, err)
		return
	}
	b.check(in, gen, fev, v, formal)
}

// check checks the value v of generic gen against the formal's
// subtype (evaluated by ev) and records it.
func (b *builder) check(in *Instance, gen *Generic, ev *sema.Evaluator,
	v eval.Value, formal *sema.Decl,
) {
	var err error
	if formal != nil {
		if t, terr := ev.Layout(formal.Type); terr == nil {
			v, err = eval.Check(v, t)
		}
	}
	gen.Value, gen.Err, gen.Known = v, err, err == nil
	if err != nil {
		b.genericError(in, gen, err)
	}
}

func (b *builder) genericError(in *Instance, gen *Generic, err error) {
	gen.Err = err
	switch c := eval.CodeOf(err); {
	case c == eval.ErrCodeBudget:
		b.out.Diagnostics = append(b.out.Diagnostics, b.genericAt(in,
			gen, diag.New(diag.SemaEvalLimit, "generic "+gen.Decl.Text+
				" of "+in.Path)))
		b.out.Incomplete = append(b.out.Incomplete, in.Path)
	case sema.DesignError(c):
		b.out.Diagnostics = append(b.out.Diagnostics, b.genericAt(in,
			gen, diag.New(diag.SemaStaticError, "generic "+
				gen.Decl.Text+": "+err.Error(), in.Path)))
	}
}

// genericAt locates a diagnostic about generic gen of instance in: at
// the instantiation, or for a top (no instantiation) at the generic's
// default value, else its declaration, in the entity's file.
func (b *builder) genericAt(in *Instance, gen *Generic,
	d diag.Diagnostic,
) diag.Diagnostic {
	if in.Stmt != nil {
		return b.at(in.Stmt, d)
	}
	g := gen.Decl
	if g == nil || g.File == nil {
		return d
	}
	if g.Value != nil && !g.Value.Node.IsZero() {
		return inFile(g.File, g.Value.Node, d)
	}
	l, c := g.File.Lines.LineCol(g.Span.Start)
	el, ec := g.File.Lines.LineCol(g.Span.End)
	return d.At(g.File.Path, l, c).To(el, ec)
}

// ports checks the port map of the instance created by s: the static
// indices and slices of each actual name against the actual's index
// ranges (under the parent's generics), and the width of each formal
// associated as a whole (under the instance's generics) against its
// actual's (#34).
func (b *builder) ports(in *Instance, s *sema.Stmt, bd *binding,
	parent *sema.Evaluator,
) {
	for _, as := range s.Ports {
		if as.Actual == nil {
			continue
		}
		var err error
		if parent.Track(func() { err = parent.CheckName(as.Actual) }) {
			if b.instanceError(in, as.Actual.Node, err,
				"port map actual of "+in.Path) {
				continue
			}
		}
		if as.Formal == nil || as.Formal.Kind != sema.DeclPort ||
			!wholeFormal(as) || (s.Component != nil && bd.ports) {
			continue
		}
		f := as.Formal
		if s.Component != nil {
			// The default binding: the entity port of the same name.
			f = nil
			for _, p := range interfaceDecls(in.Entity.Region, false) {
				if p.Name == as.Formal.Name {
					f = p
				}
			}
			if f == nil {
				continue
			}
		}
		ft, err := in.ev.Layout(f.Type)
		if err != nil || ft.Kind != eval.KindArray || ft.Bounds == nil {
			continue
		}
		at, err := parent.Layout(as.Actual.Type)
		if err != nil || at.Kind != eval.KindArray || at.Bounds == nil {
			continue
		}
		fw, aw := ft.Bounds[0].Len(), at.Bounds[0].Len()
		if fw == aw {
			continue
		}
		b.out.Diagnostics = append(b.out.Diagnostics,
			atNode(in.Stmt, as.Actual.Node, diag.New(diag.SemaStaticError,
				"width mismatch: port "+f.Text+" is "+itoa(fw)+
					" wide, its actual is "+itoa(aw), in.Describe())))
	}
}

// instanceError reports err (a design error or the step limit) found
// while evaluating at node n for instance in, and reports whether it
// did.
func (b *builder) instanceError(in *Instance, n cst.Node, err error,
	what string,
) bool {
	switch c := eval.CodeOf(err); {
	case c == eval.ErrCodeBudget:
		b.out.Diagnostics = append(b.out.Diagnostics, b.near(in, n,
			diag.New(diag.SemaEvalLimit, what)))
		b.out.Incomplete = append(b.out.Incomplete, in.Path)
		return true
	case sema.DesignError(c):
		b.out.Diagnostics = append(b.out.Diagnostics, b.near(in, n,
			diag.New(diag.SemaStaticError, err.Error(), in.Describe())))
		return true
	}
	return false
}

// near locates d at node n of the file of in's statement (or, for a
// top, leaves it without position).
func (b *builder) near(in *Instance, n cst.Node, d diag.Diagnostic,
) diag.Diagnostic {
	if in.Stmt == nil {
		return d
	}
	return atNode(in.Stmt, n, d)
}

// wholeFormal reports whether an association names its formal as a
// whole (positional or a simple name), not an element or slice of it
// (individual association) or through a conversion.
func wholeFormal(as sema.Association) bool {
	el, ok := cst.As[cst.AssociationElement](as.Node)
	if !ok {
		return false
	}
	f := el.Formal()
	return f.IsZero() || f.Kind() == cst.KindSimpleName
}

// declarations evaluates, for instance in, the constants and the
// subtypes and initial values of the signals declared in region r (an
// entity, architecture, block or generate body): a value out of range,
// a bad index or a width mismatch that depends on the instance is
// reported naming it.
func (b *builder) declarations(in *Instance, r *sema.Region) {
	if r == nil {
		return
	}
	defer b.assigns(in, r)
	ev := in.ev
	for _, d := range r.Decls {
		if d.Implicit || d.File == nil || b.indep[d] {
			continue
		}
		var (
			err  error
			node cst.Node
			dep  bool
		)
		switch d.Kind {
		case sema.DeclConstant:
			if d.Value == nil {
				continue
			}
			node = d.Value.Node
			if b.skip(in, d.File, node, d) {
				continue
			}
			dep = ev.Track(func() {
				_, err = ev.Constant(d)
				b.spend(ev)
			})
		case sema.DeclSignal, sema.DeclSharedVariable:
			node = d.Node
			if b.skip(in, d.File, node, d) {
				continue
			}
			dep = ev.Track(func() {
				var t *eval.Type
				t, err = ev.Layout(d.Type)
				b.spend(ev)
				if err != nil || d.Value == nil {
					return
				}
				node = d.Value.Node
				var v eval.Value
				// The design's elaboration evaluates it again
				// (internal/elab): its reports are made there.
				prev := ev.Mute(true)
				v, err = ev.Value(d.Value)
				ev.Mute(prev)
				b.spend(ev)
				if err == nil {
					_, err = eval.Check(v, t)
				}
			})
		default:
			continue
		}
		if !dep {
			// Instance-independent: sema reports it with the unit,
			// and no other instance needs to evaluate it again.
			b.indep[d] = true
			continue
		}
		if err == nil {
			continue
		}
		if eval.CodeOf(err) == eval.ErrCodeBudget {
			b.limited[d] = true
		}
		switch c := eval.CodeOf(err); {
		case c == eval.ErrCodeBudget:
			b.out.Diagnostics = append(b.out.Diagnostics, inFile(d.File,
				node, diag.New(diag.SemaEvalLimit, d.Kind.String()+" "+
					d.Text+" of "+in.Path)))
			b.out.Incomplete = append(b.out.Incomplete, in.Path)
		case sema.DesignError(c):
			b.out.Diagnostics = append(b.out.Diagnostics, inFile(d.File,
				node, diag.New(diag.SemaStaticError, d.Kind.String()+
					" "+d.Text+": "+err.Error(), in.Describe())))
		}
	}
}

// assigns checks, for instance in, the concurrent assignments of
// region r (sema.Assign): a width mismatch or an index out of range
// that depends on the instance's generics is reported naming it. One
// that does not is sema's (staticChecks); an assignment in a process
// is checked with its unit only, since its branch may not be taken
// (in a generate body, not at all).
func (b *builder) assigns(in *Instance, r *sema.Region) {
	ev := in.ev
	for _, as := range r.Assigns {
		if as.Seq || b.asgDone[as] {
			continue
		}
		f := r.File
		if b.skip(in, f, as.Target.Node, nil) {
			continue
		}
		var (
			n   cst.Node
			err error
		)
		dep := ev.Track(func() {
			n, err = ev.CheckAssign(as)
			b.spend(ev)
		})
		// sema checks the unit's own bodies but not a generate's
		// (only the alternatives and iterations elaborated exist):
		// here an error that does not depend on the instance is
		// reported once, for the first instance that elaborates it.
		gen := r.Kind == sema.RegionGenerate
		if !dep {
			b.asgDone[as] = true
			if !gen {
				continue
			}
		}
		if err == nil {
			continue
		}
		if n.IsZero() {
			n = as.Target.Node
		}
		switch c := eval.CodeOf(err); {
		case c == eval.ErrCodeBudget:
			b.asgDone[as] = true
			b.out.Diagnostics = append(b.out.Diagnostics, inFile(f, n,
				diag.New(diag.SemaEvalLimit, "assignment to "+
					srcText(f, as.Target.Node)+" of "+in.Path)))
			b.out.Incomplete = append(b.out.Incomplete, in.Path)
		case sema.DesignError(c):
			if gen {
				b.asgDone[as] = !dep
			}
			if !gen && b.unitError(ev, as) {
				// Found without the instance: sema reports it once.
				b.asgDone[as] = true
				continue
			}
			b.out.Diagnostics = append(b.out.Diagnostics, inFile(f, n,
				diag.New(diag.SemaStaticError, "assignment to "+
					srcText(f, as.Target.Node)+": "+err.Error(),
					in.Describe())))
		}
	}
}

// unitError reports whether assignment as is wrong without any
// instance's bindings (reading a signal counts as a dependence for
// Track, so the unbound result decides).
func (b *builder) unitError(ev *sema.Evaluator, as *sema.Assign) bool {
	_, err := ev.Fresh().CheckAssign(as)
	return err != nil
}

// skip reports whether the evaluation of declaration d (nil for a
// generic actual) for instance in is skipped: d already exceeded the
// step limit in another instance (one warning stands for all), or the
// whole hierarchy has used its total budget (Options.TotalSteps), in
// which case the first skipped evaluation is warned about at node n
// of file f. A skipped instance is incomplete. This bounds the work of
// one analysis however many instances a design has.
func (b *builder) skip(in *Instance, f *sema.File, n cst.Node,
	d *sema.Decl,
) bool {
	if d != nil && b.limited[d] {
		b.out.Incomplete = append(b.out.Incomplete, in.Path)
		return true
	}
	if b.spent <= b.o.TotalSteps {
		return false
	}
	b.out.Incomplete = append(b.out.Incomplete, in.Path)
	if !b.capped {
		b.capped = true
		w := diag.New(diag.SemaEvalLimit, "the per-instance checks "+
			"from "+in.Path+" on (the hierarchy used its total of "+
			itoa(b.o.TotalSteps)+" steps)")
		if f != nil {
			w = inFile(f, n, w)
		} else {
			w = b.at(in.Stmt, w)
		}
		b.out.Diagnostics = append(b.out.Diagnostics, w)
	}
	return true
}

// spend counts the steps of ev's last evaluation, at most one budget.
func (b *builder) spend(ev *sema.Evaluator) {
	b.spent += min(ev.Used(), b.o.Steps)
}

// srcText returns the source text of n in f without its trivia.
func srcText(f *sema.File, n cst.Node) string {
	sp := n.Span()
	if f == nil || sp.Start < 0 || sp.End > len(f.Text) ||
		sp.Start > sp.End {
		return n.Text()
	}
	return string(f.Text[sp.Start:sp.End])
}
