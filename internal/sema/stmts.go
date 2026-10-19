package sema

import (
	"math"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/token"
)

// concurrentStatements analyzes a statement part and returns its
// statement tree.
func (a *analyzer) concurrentStatements(n cst.Node) []*Stmt {
	var out []*Stmt
	for st := range n.Nodes() {
		if s := a.concStmt(st); s != nil {
			out = append(out, s)
		}
	}
	return out
}

func (a *analyzer) concStmt(st cst.Node) *Stmt {
	if st.Inactive() {
		return nil
	}
	s := &Stmt{Node: st, Label: a.labelOf(st)}
	switch st.Kind() {
	case cst.KindBlockStmt:
		a.blockStmt(cst.BlockStmt{Node: st}, s)
	case cst.KindProcessStmt:
		s.Kind = StmtProcess
		a.processStmt(cst.ProcessStmt{Node: st}, s)
	case cst.KindConcurrentProcedureCallStmt:
		a.procedureCall(cst.ConcurrentProcedureCallStmt{Node: st}.Call().Node)
	case cst.KindConcurrentAssertionStmt:
		s := cst.ConcurrentAssertionStmt{Node: st}
		r, sv := reportSeverity(st)
		a.assertion(s.Condition(), r, sv)
	case cst.KindConcurrentSimpleSignalAssign:
		s := cst.ConcurrentSimpleSignalAssign{Node: st}
		t := a.target(s.Target())
		a.delay(s.Delay())
		a.waveform(s.Waveform(), t)
	case cst.KindConcurrentConditionalSignalAssign:
		s := cst.ConcurrentConditionalSignalAssign{Node: st}
		t := a.target(s.Target())
		a.delay(s.Delay())
		a.whenBranches(s.Waveforms().Branches(), t)
	case cst.KindConcurrentSelectedSignalAssign:
		s := cst.ConcurrentSelectedSignalAssign{Node: st}
		sel := a.typed(s.Selector().Node, nil)
		t := a.target(s.Target())
		a.delay(s.Delay())
		alts := a.selectBranches(s.Waveforms().Branches(), t, sel.Type)
		a.selectCoverage(st, sel, alts)
	case cst.KindComponentInstantiationStmt:
		s.Kind = StmtInstance
		a.instantiation(cst.ComponentInstantiationStmt{Node: st}, s)
	case cst.KindForGenerateStmt:
		s.Kind = StmtForGenerate
		a.forGenerate(cst.ForGenerateStmt{Node: st}, s)
	case cst.KindIfGenerateStmt:
		s.Kind = StmtIfGenerate
		g := cst.IfGenerateStmt{Node: st}
		for br := range g.Branches() {
			var alt Alt
			if c := br.Condition(); !c.IsZero() {
				alt.Cond = a.condition(c.Node)
			}
			a.generateBody(br.Node, br.AltLabel(), br.Body(), s.Label,
				&alt)
			s.Alts = append(s.Alts, alt)
		}
		a.endClause(g.End(), s.Label)
	case cst.KindCaseGenerateStmt:
		s.Kind = StmtCaseGenerate
		g := cst.CaseGenerateStmt{Node: st}
		sel := a.typed(g.Selector().Node, nil)
		s.Selector = sel
		for c := range g.Alternatives() {
			var alt Alt
			a.globalChoices = true
			alt.Choices, alt.Others = a.choices(c.Choices(), sel.Type)
			a.globalChoices = false
			a.generateBody(c.Node, c.AltLabel(), c.Body(), s.Label, &alt)
			s.Alts = append(s.Alts, alt)
		}
		a.endClause(g.End(), s.Label)
	case cst.KindPSLPropertyDecl, cst.KindPSLSequenceDecl,
		cst.KindPSLEndpointDecl:
		a.pslDecl(st)
	case cst.KindErrorNode, cst.KindInactiveBranch:
		return nil
	default:
		// PSL directives and default clock declarations.
		a.psl(st)
	}
	return s
}

func (a *analyzer) blockStmt(b cst.BlockStmt, s *Stmt) {
	s.Kind = StmtBlock
	lbl := s.Label
	s.Region = a.push(RegionBlock, lbl, b.Node)
	if g := b.Guard(); !g.IsZero() {
		a.condition(g.Node)
		guard := &Decl{Kind: DeclSignal, Name: "guard", Text: "GUARD",
			File: a.f, Span: g.Span(), Full: g.Span(), Unit: a.u,
			Type: a.stdType("boolean"), Class: ClassSignal,
			Mode: ModeIn, Pure: true}
		a.implicitDecl(a.cur, guard)
	}
	h := b.Header()
	a.interfaceList(h.Generics().List(), DeclGeneric)
	s.Generics = a.assocMap(h.GenericMap().List(), a.cur, DeclGeneric)
	a.interfaceList(h.Ports().List(), DeclPort)
	s.Ports = a.assocMap(h.PortMap().List(), a.cur, DeclPort)
	a.declareLabels(b.Stmts().Node)
	a.declarativePart(b.Decls().Node)
	s.Body = a.concurrentStatements(b.Stmts().Node)
	a.endClause(b.End(), lbl)
	a.pop()
}

func (a *analyzer) processStmt(p cst.ProcessStmt, s *Stmt) {
	lbl := s.Label
	s.Region = a.push(RegionProcess, lbl, p.Node)
	for n := range p.Sensitivity().Names() {
		a.typed(n.Node, nil)
	}
	savedSens := a.sensitized
	a.sensitized = !p.Sensitivity().IsZero()
	defer func() { a.sensitized = savedSens }()
	a.declareSeqLabels(p.Stmts().Node)
	a.declarativePart(p.Decls().Node)
	s.Seq = a.sequentialStatements(p.Stmts().Node)
	a.endClause(p.End(), lbl)
	a.pop()
}

func (a *analyzer) forGenerate(g cst.ForGenerateStmt, s *Stmt) {
	lbl := s.Label
	s.Region = a.push(RegionGenerate, lbl, g.Node)
	ps := g.Param()
	rc := a.discreteRange(ps.Range().Node, nil)
	a.discrete(rc)
	s.Range = rc
	d := a.newDecl(DeclGenerateParam, ps.Name(), ps.Node)
	s.Param = d
	d.Class = ClassConstant
	if rc != nil {
		d.Type = rc.Type
	}
	d.Type = orUnknown(d.Type)
	a.declare(d)
	body := g.Body()
	a.declareLabels(body.Stmts().Node)
	a.declarativePart(body.Decls().Node)
	s.Body = a.concurrentStatements(body.Stmts().Node)
	a.endClause(body.End(), lbl)
	a.endClause(g.End(), lbl)
	a.pop()
}

// generateBody analyzes one alternative of an if or case generate.
func (a *analyzer) generateBody(alt cst.Node, l cst.Label,
	body cst.GenerateBody, lbl *Decl, out *Alt,
) {
	owner := lbl
	if !l.IsZero() {
		owner = a.newDecl(DeclLabel, l.Name(), alt)
		a.declare(owner)
		out.Label = owner
	}
	r := a.push(RegionGenerate, nil, alt)
	r.Owner = owner
	out.Region = r
	a.declareLabels(body.Stmts().Node)
	a.declarativePart(body.Decls().Node)
	out.Body = a.concurrentStatements(body.Stmts().Node)
	a.endClause(body.End(), owner)
	a.pop()
}

// instantiation analyzes a component, entity or configuration
// instantiation and its maps.
func (a *analyzer) instantiation(ci cst.ComponentInstantiationStmt,
	s *Stmt,
) {
	iu := ci.Unit()
	name := iu.Name().Node
	var region *Region
	r := a.resolveName(name, RefRead, nil)
	switch {
	case !iu.Node.Token(token.KwEntity, 0).IsZero():
		if len(r.decls) == 1 {
			d := r.decls[0].Denotes()
			if d.Kind == DeclEntity {
				region = d.Region
				s.Entity = d
				if arch := iu.Architecture(); !arch.IsZero() {
					a.lateArch(arch, d)
					s.Arch = TokenKey(arch)
				}
			} else if !a.quiet {
				a.semaError(name.Span(), a.text(name)+" is not an entity")
			}
		}
	case !iu.Node.Token(token.KwConfiguration, 0).IsZero():
		if len(r.decls) == 1 {
			d := r.decls[0].Denotes()
			if d.Kind == DeclConfiguration && d.Spec != nil {
				region = d.Spec.Region
				s.Config, s.Entity = d, d.Spec
			} else if d.Kind != DeclConfiguration && !a.quiet {
				a.semaError(name.Span(), a.text(name)+
					" is not a configuration")
			}
		}
	default:
		if len(r.decls) == 1 {
			d := r.decls[0].Denotes()
			if d.Kind == DeclComponent || d.Kind == DeclEntity {
				region = d.Region
				if d.Kind == DeclComponent {
					s.Component = d
				} else {
					s.Entity = d
				}
			} else if !a.quiet {
				a.semaError(name.Span(), a.text(name)+" is not a component")
			}
		}
	}
	s.Generics = a.assocMap(ci.GenericMap().List(), region, DeclGeneric)
	a.missingGenerics(s.Generics, region, ci.Node)
	s.Ports = a.assocMap(ci.PortMap().List(), region, DeclPort)
}

// assocMap analyzes a generic or port map against the interface
// declarations of kind k in region (nil: the target is unknown, and
// the actuals are typed without an expected type).
func (a *analyzer) assocMap(l cst.AssociationList, region *Region,
	k DeclKind,
) []Association {
	var out []Association
	var formals []*Decl
	if region != nil {
		for _, d := range region.Decls {
			if d.Kind == k || (k == DeclGeneric && isGenericKind(d.Kind)) {
				formals = append(formals, d)
			}
		}
	}
	i := 0
	for item := range l.Items() {
		var f *Decl
		if fn := item.Formal(); !fn.IsZero() {
			tok := leftmostToken(fn.Node)
			key := TokenKey(tok)
			for _, d := range formals {
				if d.Name == key {
					f = d
					break
				}
			}
			conv := false
			if f == nil {
				// conversion(formal) => actual (LRM 6.5.7.1): the
				// formal is the single argument of a function call or
				// type conversion.
				var ctok cst.Token
				f, ctok = conversionFormal(fn.Node, formals)
				if f != nil {
					tok, conv = ctok, true
				}
			}
			switch {
			case f != nil:
				a.ref(tok, f, RefFormal)
			case region != nil && !a.quiet && key != "":
				if at, ok := a.convertedName(fn.Node); ok {
					tok = at // conversion(not_a_port)
				}
				a.semaError(tok.Span(), tok.Text()+" is not a "+
					k.String()+" of "+ownerText(region))
			}
			if conv {
				a.conversionName(fn.Node, f)
			} else if fn.Kind() != cst.KindSimpleName {
				a.lenientFormal(fn.Node)
			}
		} else if i < len(formals) {
			f = formals[i]
		}
		i++
		var want *Type
		if f != nil {
			want = formalType(item.Formal().Node, f)
		}
		as := Association{Formal: f, Node: item.Node}
		v := item.Actual()
		if v.IsZero() || v.Kind() == cst.KindOpenExpr {
			out = append(out, as)
			continue
		}
		if a.nonObjectActual(v.Node, f) {
			out = append(out, as)
			continue
		}
		saved := a.mapActual
		a.mapActual = true // associating a port is no read of it
		e := a.typed(v.Node, want)
		a.mapActual = saved
		if f != nil && f.Kind.IsObject() {
			a.expect(e, want, v.Node)
		}
		as.Actual = e
		if k == DeclPort && fnSimple(item.Formal().Node) {
			a.viewActual(f, e, v.Node) // modeview.go
		}
		out = append(out, as)
	}
	return out
}

// conversionFormal returns the formal named by fn when fn is a
// conversion function or type conversion applied to it, f(formal), with
// the formal's name token.
func conversionFormal(fn cst.Node, formals []*Decl) (*Decl, cst.Token) {
	c, ok := cst.As[cst.CallOrIndexName](fn)
	if !ok {
		return nil, cst.Token{}
	}
	var arg cst.Node
	n := 0
	for item := range c.Args().Items() {
		if !item.Formal().IsZero() {
			return nil, cst.Token{}
		}
		arg = item.Actual().Node
		n++
	}
	if n != 1 || arg.IsZero() {
		return nil, cst.Token{}
	}
	tok := leftmostToken(arg)
	key := TokenKey(tok)
	for _, d := range formals {
		if key != "" && d.Name == key {
			return d, tok
		}
	}
	return nil, cst.Token{}
}

// conversionName resolves the function or type mark of the formal
// conversion fn applied to the formal f and records its reference: the
// function is the one taking f's type.
func (a *analyzer) conversionName(fn cst.Node, f *Decl) {
	saved := a.quiet
	a.quiet = true
	r := a.resolveName(cst.CallOrIndexName{Node: fn}.Prefix().Node,
		RefCall, nil)
	a.quiet = saved
	if len(r.decls) == 0 || !r.decls[0].Overloadable() {
		return
	}
	o := &Overload{Name: r.tok.Text()}
	var fit []*Decl
	for _, d := range r.decls {
		ps := profileParams(d.Denotes())
		ok := len(ps) == 1 && SameBase(ps[0].Type, f.Type)
		o.Candidates = append(o.Candidates, Candidate{Decl: d, Fits: ok})
		if ok {
			fit = append(fit, d)
		}
	}
	if len(fit) == 1 {
		o.Chosen = fit[0]
	}
	a.refOverload(r.tok, o, RefCall)
}

// convertedName returns the name token inside fn when fn has the
// shape conversion(name) and its prefix denotes a function or a type:
// the formal meant is that name.
func (a *analyzer) convertedName(fn cst.Node) (cst.Token, bool) {
	c, ok := cst.As[cst.CallOrIndexName](fn)
	if !ok {
		return cst.Token{}, false
	}
	var arg cst.Node
	n := 0
	for item := range c.Args().Items() {
		if !item.Formal().IsZero() {
			return cst.Token{}, false
		}
		arg, n = item.Actual().Node, n+1
	}
	if n != 1 || arg.Kind() != cst.KindSimpleName {
		return cst.Token{}, false
	}
	saved := a.quiet
	a.quiet = true
	r := a.resolveName(c.Prefix().Node, RefCall, nil)
	a.quiet = saved
	if r.typ == nil && (len(r.decls) == 0 || !r.decls[0].Overloadable()) {
		return cst.Token{}, false
	}
	return leftmostToken(arg), true
}

// nonObjectActual analyzes the actual of a generic package or generic
// subprogram, or a name actual whose formal is unknown, and reports
// whether it did.
func (a *analyzer) nonObjectActual(v cst.Node, f *Decl) bool {
	isName := (cst.Name{}).Accepts(v.Kind())
	switch {
	case f != nil && f.Kind == DeclGenericPackage:
		r := a.resolveName(v, RefRead, nil)
		if !r.unknown && (len(r.decls) != 1 ||
			!isPackageKind(r.decls[0].Denotes().Kind)) && !a.quiet {
			a.semaError(v.Span(), a.text(v)+" is not a package")
		}
		return true
	case f != nil && f.Kind == DeclGenericSubprogram:
		r := a.resolveName(v, RefRead, nil)
		if len(r.decls) > 0 && r.decls[0].Overloadable() {
			o := &Overload{Name: r.tok.Text()}
			var fit []*Decl
			for _, d := range r.decls {
				ok := len(profileParams(d.Denotes())) == len(f.Params)
				o.Candidates = append(o.Candidates, Candidate{Decl: d,
					Fits: ok})
				if ok {
					fit = append(fit, d)
				}
			}
			if len(fit) == 1 {
				o.Chosen = fit[0]
			}
			a.refOverload(r.tok, o, RefRead)
		}
		return true
	case f == nil && isName:
		saved := a.quiet
		a.quiet = true
		r := a.resolveName(v, RefRead, nil)
		a.quiet = saved
		if r.expr != nil || (len(r.decls) > 0 &&
			r.decls[0].Overloadable()) {
			a.u.Exprs = append(a.u.Exprs, a.value(r, v, nil))
		}
		return true
	}
	return false
}

// formalType is the type of a formal part (LRM 6.5.7.1): the formal's
// subtype for a simple name or slice, the element subtype for an
// indexed formal (individual association), unknown for a selected or
// converted one.
func formalType(fn cst.Node, f *Decl) *Type {
	switch fn.Kind() {
	case 0, cst.KindSimpleName:
		return f.Type
	case cst.KindSliceName:
		return runTime(f.Type) // the slice's own length
	case cst.KindCallOrIndexName:
		c := cst.CallOrIndexName{Node: fn}
		if c.Prefix().Kind() == cst.KindSimpleName &&
			TokenKey(c.Prefix().FirstToken()) == f.Name {
			if allRanges(c.Args()) {
				return runTime(f.Type) // the slice's own length
			}
			if f.Type.Dims() == 1 {
				return f.Type.ElemType()
			}
		}
	}
	return nil
}

func isGenericKind(k DeclKind) bool {
	return k == DeclGeneric || k == DeclGenericType ||
		k == DeclGenericSubprogram || k == DeclGenericPackage
}

func ownerText(r *Region) string {
	if r.Owner != nil {
		return r.Owner.Text
	}
	return "the instantiated unit"
}

// missingGenerics reports the generic constants of an instantiated
// unit that have no default and no association (LRM 6.5.6.2).
func (a *analyzer) missingGenerics(as []Association, region *Region,
	at cst.Node,
) {
	if a.quiet || region == nil {
		return
	}
	given := map[*Decl]bool{}
	for _, x := range as {
		if x.Formal == nil && x.Node.IsZero() {
			continue
		}
		if x.Formal == nil {
			return // an unknown formal: the error is reported
		}
		given[x.Formal] = true
	}
	for _, d := range region.Decls {
		if d.Kind == DeclGeneric && d.Value == nil && !given[d] {
			a.semaError(at.FirstToken().Span(), "generic "+d.Text+
				" of "+ownerText(region)+" has no default and no actual")
		}
	}
}

// genericMap analyzes a generic map aspect against region's generics.
func (a *analyzer) genericMap(l cst.AssociationList, region *Region) {
	a.assocMap(l, region, DeclGeneric)
}

// target types the target of an assignment (a written name, or a
// VHDL-2008 aggregate of names).
func (a *analyzer) target(t cst.Target) *Type {
	return a.targetExpr(t).Type
}

// targetExpr types the target of an assignment and returns it; the
// type of an aggregate target is anyComposite.
func (a *analyzer) targetExpr(t cst.Target) *Expr {
	n := t.Target().Node
	if (cst.Name{}).Accepts(n.Kind()) {
		nm := a.resolveName(n, RefWrite, nil)
		e := a.value(nm, n, nil)
		if !a.typeMarkValue(e, n) {
			a.targetClass(e, n, t.Node.Parent().Kind())
		}
		a.u.Exprs = append(a.u.Exprs, e)
		a.viewWrite(e, n) // modeview.go
		a.assignTarget(e) // assign.go
		return e
	}
	a.asg = nil
	saved := a.mapActual
	a.mapActual = true // an aggregate target is written, not read
	e := a.typed(n, nil)
	a.mapActual = saved
	if n.Kind() == cst.KindAggregate {
		// The value decides an aggregate target's type (LRM 10.6.2.1):
		// it must be composite.
		return &Expr{Kind: e.Kind, Node: e.Node, Type: anyComposite,
			Assocs: e.Assocs}
	}
	return &Expr{Kind: ExprUnknown, Node: n, Type: UnknownType}
}

// targetClass reports an assignment target that is not an object of
// the statement's class (LRM 10.5, 10.6): a variable assignment writes
// a variable, a signal assignment a signal, and neither writes an
// interface object of mode in. stmt is the assignment's node kind.
func (a *analyzer) targetClass(e *Expr, n cst.Node, stmt cst.Kind) {
	if a.quiet {
		return
	}
	d := actualObject(e)
	if d == nil || d.Mode == ModeView {
		return
	}
	var want string
	switch stmt {
	case cst.KindVariableAssignStmt, cst.KindConditionalVariableAssignStmt,
		cst.KindSelectedVariableAssignStmt:
		want = "variable"
	case cst.KindSignalAssignStmt, cst.KindConditionalSignalAssignStmt,
		cst.KindSelectedSignalAssignStmt, cst.KindForceAssignStmt,
		cst.KindReleaseAssignStmt, cst.KindConcurrentSimpleSignalAssign,
		cst.KindConcurrentConditionalSignalAssign,
		cst.KindConcurrentSelectedSignalAssign:
		want = "signal"
	default:
		return
	}
	have := objectClass(d)
	switch {
	case have == "":
		return
	case have != want:
		a.semaError(n.Span(), d.Kind.String()+" "+d.Text+" is not a "+
			want+" and cannot be the target of a "+want+" assignment")
	case d.Mode == ModeIn && (d.Kind == DeclPort ||
		d.Kind == DeclParameter):
		a.semaError(n.Span(), d.Kind.String()+" "+d.Text+" has mode "+
			"in and cannot be assigned")
	}
}

// objectClass is the object class of declaration d for assignments:
// "signal", "variable", "constant", or "" when d is no object.
func objectClass(d *Decl) string {
	switch d.Kind {
	case DeclSignal:
		return "signal"
	case DeclPort:
		if d.Class == ClassVariable {
			return "variable" // VHDL-2019 variable ports
		}
		return "signal"
	case DeclVariable, DeclSharedVariable:
		return "variable"
	case DeclConstant, DeclGeneric, DeclLoopParam, DeclGenerateParam:
		return "constant"
	case DeclParameter:
		switch d.Class {
		case ClassSignal:
			return "signal"
		case ClassVariable:
			return "variable"
		case ClassConstant:
			return "constant"
		}
	}
	return ""
}

func (a *analyzer) delay(d cst.DelayMechanism) {
	if r := d.Reject(); !r.IsZero() {
		a.typed(r.Node, a.stdType("time"))
	}
}

// waveform types each waveform element against the target type t.
func (a *analyzer) waveform(w cst.Waveform, t *Type) {
	i := 0
	var last int64
	lastKnown := false
	for el := range w.WaveformElements() {
		v := el.Value()
		if !v.IsZero() {
			e := a.typed(v.Node, t)
			a.expect(e, t, v.Node)
			a.assignValue(e)
		}
		af := el.After()
		switch {
		case !af.IsZero():
			at := a.typed(af.Node, a.stdType("time"))
			// LRM 10.5.2.1: the times of a waveform's elements
			// increase.
			if n, ok := staticTime(at); ok {
				if lastKnown && n <= last && !a.quiet {
					a.semaError(af.Span(), "a waveform element's time "+
						"must be greater than the previous element's")
				}
				last, lastKnown = n, true
			} else {
				lastKnown = false
			}
		case i > 0 && !a.quiet && !el.Node.Inactive():
			// Only the first element may omit its time (it is then 0).
			a.semaError(el.Node.Span(), "a waveform element after the "+
				"first needs an after clause")
		default:
			last, lastKnown = 0, true
		}
		i++
	}
}

// staticTime returns the value of a time expression that is one
// integer physical literal, in its type's primary unit.
func staticTime(e *Expr) (int64, bool) {
	return physValue(e, 0)
}

func physValue(e *Expr, depth int) (int64, bool) {
	if e == nil || depth > 16 || e.Kind != ExprPhysical ||
		e.Decl == nil || e.Decl.Kind != DeclUnit {
		return 0, false
	}
	f := int64(1)
	if u := e.Decl; u.Value != nil { // a secondary unit
		n, ok := physValue(u.Value, depth+1)
		if !ok {
			return 0, false
		}
		f = n
	}
	v := int64(1)
	if !e.Lit.IsZero() {
		n, ok := ParseInteger(e.Lit.Text())
		if !ok {
			return 0, false
		}
		v = n
	}
	if f != 0 && (v > math.MaxInt64/f || v < math.MinInt64/f) {
		return 0, false
	}
	return v * f, true
}

// whenValue types the value of a conditional or selected branch: a
// waveform or an expression.
// It returns the typed expression, nil for a waveform or unaffected.
func (a *analyzer) whenValue(v cst.WhenValue, t *Type) *Expr {
	if v.Kind() == cst.KindWaveform {
		a.waveform(cst.Waveform{Node: v.Node}, t)
		return nil
	}
	if v.Kind() == cst.KindUnaffectedExpr || v.IsZero() {
		return nil
	}
	e := a.typed(v.Node, t)
	a.expect(e, t, v.Node)
	a.assignValue(e)
	return e
}

func (a *analyzer) whenBranches(brs func(func(cst.WhenBranch) bool),
	t *Type,
) []SeqBranch {
	// A branch may be one a static condition never takes: its values
	// are not checked against the target (assign.go).
	saved := a.asg
	a.asg = nil
	defer func() { a.asg = saved }()
	var out []SeqBranch
	for br := range brs {
		b := SeqBranch{Value: a.whenValue(br.Value(), t)}
		if c := whenCondition(br); !c.IsZero() {
			b.Cond = a.condition(c)
		}
		out = append(out, b)
	}
	return out
}

// selectCoverage checks the choices of a selected assignment st
// (not a matching select?) like a case statement's.
func (a *analyzer) selectCoverage(st cst.Node, sel *Expr, alts []SeqAlt) {
	if !st.Token(token.Question, 0).IsZero() {
		return
	}
	a.caseCoverage(sel, alts, st.Token(token.KwSelect, 0).Span())
}

func (a *analyzer) selectBranches(brs func(func(cst.SelectBranch) bool),
	t, sel *Type,
) []SeqAlt {
	saved := a.asg // as for whenBranches
	a.asg = nil
	defer func() { a.asg = saved }()
	var out []SeqAlt
	for br := range brs {
		alt := SeqAlt{Value: a.whenValue(br.Value(), t)}
		alt.Choices, alt.Others = a.choices(br.Choices(), sel)
		out = append(out, alt)
	}
	return out
}

// choices types the choices of a case alternative or selected branch
// against the selector type.
func (a *analyzer) choices(c cst.Choices, sel *Type) ([]*Expr, bool) {
	var out []*Expr
	others := false
	for ch := range c.Items() {
		if ch.Kind() == cst.KindOthersChoice {
			others = true
			continue
		}
		if isRangeSyntax(ch.Node) {
			rc := a.discreteRange(ch.Node, sel)
			x := &Expr{Kind: ExprRange, Node: ch.Node, Range: rc,
				Type: sel}
			a.localChoice(x)
			out = append(out, x)
			continue
		}
		e := a.operand(ch.Node, sel)
		if e.Kind != ExprTypeMark {
			a.settle(e, sel)
		}
		a.localChoice(e)
		a.u.Exprs = append(a.u.Exprs, e)
		out = append(out, e)
	}
	return out, others
}

// localChoice reports a choice of a case statement or selected
// assignment that is not locally static (LRM 10.9); a case generate's
// choices need only be globally static (LRM 11.8).
func (a *analyzer) localChoice(e *Expr) {
	if a.quiet || a.globalChoices {
		return
	}
	if st, known := locallyStatic(e, 0, a.std); known && !st {
		a.semaError(e.Node.Span(), "the choice "+a.text(e.Node)+
			" must be locally static")
	}
}

func (a *analyzer) assertion(cond, report, severity cst.Expr,
) (c, r, s *Expr) {
	if !cond.IsZero() {
		c = a.condition(cond.Node)
	}
	if !report.IsZero() {
		r = a.typed(report.Node, a.stdType("string"))
	}
	if !severity.IsZero() {
		s = a.typed(severity.Node, a.stdType("severity_level"))
	}
	return c, r, s
}

// procedureCall resolves a procedure call statement and returns the
// call (nil when the name is not a procedure).
func (a *analyzer) procedureCall(n cst.Node) *Expr {
	var (
		prefix cst.Node
		args   cst.AssociationList
	)
	if c, ok := cst.As[cst.CallOrIndexName](n); ok {
		prefix, args = c.Prefix().Node, c.Args()
	} else {
		prefix = n
	}
	p := a.resolveName(prefix, RefCall, nil)
	switch {
	case len(p.decls) > 0 && p.decls[0].Overloadable():
		e := a.call(n, p, args, nil, true)
		a.u.Exprs = append(a.u.Exprs, e)
		return e
	case p.unknown:
		a.lenientArgs(args)
	default:
		a.lenientArgs(args)
		if !a.quiet {
			a.semaError(prefix.Span(), a.text(prefix)+
				" is not a procedure")
		}
	}
	return nil
}

// sequentialStatements analyzes a sequence of statements and returns
// its typed tree.
func (a *analyzer) sequentialStatements(n cst.Node) []*SeqStmt {
	var out []*SeqStmt
	for st := range n.Nodes() {
		if s := a.seqStmt(st); s != nil {
			out = append(out, s)
		}
	}
	return out
}

func (a *analyzer) seqStmt(st cst.Node) *SeqStmt {
	if st.Inactive() {
		return nil
	}
	q := &SeqStmt{Node: st, Label: a.labelOf(st)}
	switch st.Kind() {
	case cst.KindWaitStmt:
		q.Kind = SeqWait
		a.waitAllowed(st)
		w := cst.WaitStmt{Node: st}
		for n := range w.Sensitivity().List().Names() {
			a.typed(n.Node, nil)
		}
		if c := w.Condition().Condition(); !c.IsZero() {
			q.Cond = a.condition(c.Node)
		}
		if t := w.Timeout().Timeout(); !t.IsZero() {
			q.Value = a.typed(t.Node, a.stdType("time"))
		}
	case cst.KindAssertStmt:
		q.Kind = SeqAssert
		s := cst.AssertStmt{Node: st}
		r, sv := reportSeverity(st)
		q.Cond, q.Report, q.Severity = a.assertion(s.Condition(), r, sv)
	case cst.KindReportStmt:
		q.Kind = SeqReport
		r, sv := reportSeverity(st)
		_, q.Report, q.Severity = a.assertion(cst.Expr{}, r, sv)
	case cst.KindSignalAssignStmt:
		q.Kind = SeqSignal
		s := cst.SignalAssignStmt{Node: st}
		q.Target = a.targetExpr(s.Target())
		a.delay(s.Delay())
		a.waveform(s.Waveform(), q.Target.Type)
	case cst.KindConditionalSignalAssignStmt:
		q.Kind = SeqSignal
		s := cst.ConditionalSignalAssignStmt{Node: st}
		q.Target = a.targetExpr(s.Target())
		a.delay(s.Delay())
		a.whenBranches(s.Waveforms().Branches(), q.Target.Type)
	case cst.KindSelectedSignalAssignStmt:
		q.Kind = SeqSignal
		s := cst.SelectedSignalAssignStmt{Node: st}
		sel := a.typed(s.Selector().Node, nil)
		q.Target = a.targetExpr(s.Target())
		a.delay(s.Delay())
		alts := a.selectBranches(s.Waveforms().Branches(),
			q.Target.Type, sel.Type)
		a.selectCoverage(st, sel, alts)
	case cst.KindForceAssignStmt:
		q.Kind = SeqSignal
		s := cst.ForceAssignStmt{Node: st}
		q.Target = a.targetExpr(s.Target())
		t := q.Target.Type
		if v := s.Value(); !v.IsZero() {
			q.Value = a.typed(v.Node, t)
			a.expect(q.Value, t, v.Node)
		}
	case cst.KindReleaseAssignStmt:
		q.Kind = SeqSignal
		q.Target = a.targetExpr(cst.ReleaseAssignStmt{Node: st}.Target())
	case cst.KindVariableAssignStmt:
		q.Kind = SeqVarAssign
		s := cst.VariableAssignStmt{Node: st}
		q.Target = a.targetExpr(s.Target())
		t := q.Target.Type
		if v := s.Value(); !v.IsZero() {
			q.Value = a.typed(v.Node, t)
			a.expect(q.Value, t, v.Node)
			a.assignValue(q.Value)
		}
	case cst.KindConditionalVariableAssignStmt:
		q.Kind = SeqCondVarAssign
		s := cst.ConditionalVariableAssignStmt{Node: st}
		q.Target = a.targetExpr(s.Target())
		q.Branches = a.whenBranches(s.Values().Branches(),
			q.Target.Type)
	case cst.KindSelectedVariableAssignStmt:
		q.Kind = SeqSelVarAssign
		s := cst.SelectedVariableAssignStmt{Node: st}
		q.Selector = a.typed(s.Selector().Node, nil)
		q.Target = a.targetExpr(s.Target())
		q.Alts = a.selectBranches(s.Values().Branches(), q.Target.Type,
			q.Selector.Type)
		a.selectCoverage(st, q.Selector, q.Alts)
	case cst.KindProcedureCallStmt:
		q.Kind = SeqCall
		q.Call = a.procedureCall(cst.ProcedureCallStmt{Node: st}.Call().Node)
	case cst.KindIfStmt:
		q.Kind = SeqIf
		s := cst.IfStmt{Node: st}
		c := a.condition(s.Condition().Node)
		q.Branches = append(q.Branches, SeqBranch{Cond: c,
			Body: a.sequentialStatements(s.Then().Node)})
		for br := range s.Elsifs() {
			c := a.condition(br.Condition().Node)
			q.Branches = append(q.Branches, SeqBranch{Cond: c,
				Body: a.sequentialStatements(br.Stmts().Node)})
		}
		q.Else = a.sequentialStatements(s.Else().Stmts().Node)
		a.endClause(s.End(), q.Label)
	case cst.KindCaseStmt:
		q.Kind = SeqCase
		s := cst.CaseStmt{Node: st}
		q.Matching = !s.Matching().IsZero()
		q.Selector = a.typed(s.Selector().Node, nil)
		for alt := range s.Alternatives() {
			var sa SeqAlt
			sa.Choices, sa.Others = a.choices(alt.Choices(),
				q.Selector.Type)
			sa.Body = a.sequentialStatements(alt.Stmts().Node)
			q.Alts = append(q.Alts, sa)
		}
		if !q.Matching {
			a.caseCoverage(q.Selector, q.Alts,
				st.Token(token.KwCase, 0).Span())
		}
		a.endClause(s.End(), q.Label)
	case cst.KindLoopStmt:
		q.Kind = SeqLoop
		a.loopStmt(cst.LoopStmt{Node: st}, q)
	case cst.KindNextStmt:
		q.Kind = SeqNext
		s := cst.NextStmt{Node: st}
		q.Loop = a.loopLabel(s.Loop())
		a.inLoop(st, s.Loop(), q.Loop)
		if c := s.Condition(); !c.IsZero() {
			q.Cond = a.condition(c.Node)
		}
	case cst.KindExitStmt:
		q.Kind = SeqExit
		s := cst.ExitStmt{Node: st}
		q.Loop = a.loopLabel(s.Loop())
		a.inLoop(st, s.Loop(), q.Loop)
		if c := s.Condition(); !c.IsZero() {
			q.Cond = a.condition(c.Node)
		}
	case cst.KindReturnStmt:
		q.Kind = SeqReturn
		s := cst.ReturnStmt{Node: st}
		if a.fn == nil && !a.quiet {
			// LRM 10.13: return is a statement of subprogram bodies.
			a.semaError(st.Token(token.KwReturn, 0).Span(), "a return "+
				"statement must be inside a subprogram")
		}
		if v := s.Value(); !v.IsZero() {
			var want *Type
			if a.fn != nil {
				want = a.fn.Type
			}
			q.Value = a.typed(v.Node, want)
			a.expect(q.Value, want, v.Node)
		}
	case cst.KindConditionalReturnStmt:
		q.Kind = SeqCondReturn
		a.asg = nil // the branch values are returned, not assigned
		s := cst.ConditionalReturnStmt{Node: st}
		var want *Type
		if a.fn != nil {
			want = a.fn.Type
		}
		q.Branches = a.whenBranches(s.Values().Branches(), want)
	case cst.KindNullStmt:
		q.Kind = SeqNull
	case cst.KindSequentialBlockStmt:
		q.Kind = SeqBlock
		s := cst.SequentialBlockStmt{Node: st}
		q.Region = a.push(RegionSeqBlock, q.Label, st)
		a.declarativePart(s.Decls().Node)
		q.Body = a.sequentialStatements(s.Stmts().Node)
		a.endClause(s.End(), q.Label)
		a.pop()
	}
	return q
}

func (a *analyzer) loopStmt(l cst.LoopStmt, q *SeqStmt) {
	lbl := q.Label
	q.Region = a.push(RegionLoop, lbl, l.Node)
	switch sc := l.Scheme().Node; sc.Kind() {
	case cst.KindWhileScheme:
		q.Cond = a.condition(cst.WhileScheme{Node: sc}.Condition().Node)
	case cst.KindForScheme:
		ps := cst.ForScheme{Node: sc}.Param()
		rc := a.discreteRange(ps.Range().Node, nil)
		a.discrete(rc)
		q.Range = rc
		d := a.newDecl(DeclLoopParam, ps.Name(), ps.Node)
		q.Param = d
		d.Class = ClassConstant
		if rc != nil {
			d.Type = rc.Type
		}
		d.Type = orUnknown(d.Type)
		a.declare(d)
	}
	a.loops = append(a.loops, lbl)
	q.Body = a.sequentialStatements(l.Stmts().Node)
	a.loops = a.loops[:len(a.loops)-1]
	a.endClause(l.End(), lbl)
	a.pop()
}

// discrete reports a loop or generate parameter range whose type is
// not discrete (LRM 10.10, 11.8).
func (a *analyzer) discrete(rc *RangeCons) {
	if a.quiet || rc == nil || rc.Type.Unknown() || rc.Type.IsDiscrete() ||
		rc.Type.Base().Class == TypeGeneric ||
		rc.Left != nil && rc.Left.illTyped ||
		rc.Right != nil && rc.Right.illTyped {
		return
	}
	a.semaError(rc.Node.Span(), "the range of a loop or generate "+
		"parameter must be discrete, not "+rc.Type.Name())
}

// waitAllowed reports a wait statement in a function or in a process
// with a sensitivity list (LRM 4.3, 11.3).
func (a *analyzer) waitAllowed(st cst.Node) {
	switch {
	case a.quiet:
	case a.fn != nil && a.fn.Kind == DeclFunction:
		a.semaError(st.Token(token.KwWait, 0).Span(), "function "+
			a.fn.Text+" cannot contain a wait statement")
	case a.fn == nil && a.sensitized:
		a.semaError(st.Token(token.KwWait, 0).Span(), "a process "+
			"with a sensitivity list cannot contain a wait statement")
	}
}

// inLoop reports a next or exit statement outside a loop, or naming a
// label that is not an enclosing loop's (LRM 10.11, 10.12).
func (a *analyzer) inLoop(st cst.Node, tok cst.Token, lbl *Decl) {
	if a.quiet {
		return
	}
	kw := st.Token(token.KwNext, 0)
	if st.Kind() == cst.KindExitStmt {
		kw = st.Token(token.KwExit, 0)
	}
	if len(a.loops) == 0 {
		a.semaError(kw.Span(), kw.Text()+" must be inside a loop")
		return
	}
	if lbl == nil {
		return
	}
	for _, l := range a.loops {
		if l == lbl {
			return
		}
	}
	a.semaError(tok.Span(), tok.Text()+" is not the label of an "+
		"enclosing loop")
}

// loopLabel resolves the loop label of next or exit.
func (a *analyzer) loopLabel(tok cst.Token) *Decl {
	if tok.IsZero() || tok.Missing() {
		return nil
	}
	for _, d := range a.lookup(TokenKey(tok)).Decls {
		if d.Kind == DeclLabel {
			a.ref(tok, d, RefLabel)
			return d
		}
	}
	a.undefined(tok)
	return nil
}

// blockConfiguration walks a configuration's block configuration. The
// architecture it configures is not a dependency of the configuration
// (it may come later in the design order), so its items are resolved
// leniently; elaboration (#35) checks the bindings.
func (a *analyzer) blockConfiguration(bc cst.BlockConfiguration,
	ent *Unit,
) {
	if bc.IsZero() {
		return
	}
	if spec := bc.Spec(); !spec.IsZero() && ent != nil && ent.Decl != nil {
		if tok := spec.FirstToken(); spec.Kind() == cst.KindSimpleName {
			a.lateArch(tok, ent.Decl)
		}
	}
	for uc := range bc.Uses() {
		a.useClause(uc)
	}
	for item := range bc.Items() {
		switch item.Kind() {
		case cst.KindBlockConfiguration:
			a.blockConfiguration(cst.BlockConfiguration{Node: item.Node},
				nil)
		case cst.KindComponentConfiguration:
			cc := cst.ComponentConfiguration{Node: item.Node}
			a.componentSpec(cc.Spec())
			a.bindingIndication(cc.Binding())
			a.blockConfiguration(cc.Block(), nil)
		}
	}
}

// componentSpec resolves `labels : component` leniently.
func (a *analyzer) componentSpec(cs cst.ComponentSpecification) {
	for tok := range cs.Instances().Labels() {
		for _, d := range a.lookup(TokenKey(tok)).Decls {
			if d.Kind == DeclLabel {
				a.ref(tok, d, RefLabel)
			}
		}
	}
	a.lenientName(cs.Component().Node)
}

// bindingIndication resolves `use entity lib.e(a)` and its maps.
func (a *analyzer) bindingIndication(b cst.BindingIndication) {
	if b.IsZero() {
		return
	}
	var region *Region
	if u := b.Entity().Unit(); !u.IsZero() {
		n := u.Node
		var arch cst.Token
		if c, ok := cst.As[cst.CallOrIndexName](n); ok {
			n = c.Prefix().Node
			for item := range c.Args().Items() {
				arch = leftmostToken(item.Actual().Node)
				break
			}
		}
		r := a.resolveName(n, RefRead, nil)
		if len(r.decls) == 1 {
			d := r.decls[0].Denotes()
			switch d.Kind {
			case DeclEntity:
				region = d.Region
				if !arch.IsZero() {
					a.lateArch(arch, d)
				}
			case DeclConfiguration:
				if d.Spec != nil {
					region = d.Spec.Region
				}
			}
		}
	}
	a.assocMap(b.GenericMap().List(), region, DeclGeneric)
	a.assocMap(b.PortMap().List(), region, DeclPort)
}

// vunit analyzes a PSL verification unit.
func (a *analyzer) vunit(lu cst.Node) {
	d := a.newDecl(DeclVunit, lu.TokenIn(nameTokens, 0), lu)
	a.u.Decl = d
	a.declare(d)
	a.u.Region = a.push(RegionVunit, d, lu)
	for c := range lu.Nodes() {
		switch {
		case c.Kind() == cst.KindPSLHierarchicalName:
			for n := range (cst.PSLHierarchicalName{Node: c}).Names() {
				a.lenientName(n.Node)
			}
		case c.Kind() == cst.KindPSLInheritSpec:
			for n := range (cst.PSLInheritSpec{Node: c}).Names() {
				a.lenientName(n.Node)
			}
		case (cst.Decl{}).Accepts(c.Kind()):
			a.declItem(c)
		case (cst.ConcStmt{}).Accepts(c.Kind()):
			if l := c.Child[cst.Label](0); !l.IsZero() {
				a.declare(a.labelDecl(l, c))
			}
			a.concStmt(c)
		default:
			a.psl(c)
		}
	}
	a.pop()
}
