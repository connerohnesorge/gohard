package sema

// types.go types expressions (LRM 9): literals, aggregates, qualified
// expressions, allocators, conditional expressions, ranges and
// conditions, and decides when a type mismatch is reported. It is the
// extension point for expression typing; operator and call overload
// resolution is overload.go's.

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/token"
)

// pendKind marks an operand whose type is decided by its context: the
// overload resolution that uses it settles it once the candidate is
// chosen (LRM 12.5, the complete context).
type pendKind uint8

const (
	pendNone pendKind = iota
	// pendChar is a character literal; cands are its enumeration
	// literals.
	pendChar
	pendString    // a string or bit-string literal
	pendAggregate // an aggregate
	pendNull      // the literal null
	pendName      // an overloaded name used as a value (cands)
	pendCall      // a call whose result type picks the overload
	pendAlloc     // an allocator: any access type designating made
)

// pending is the unsettled part of an Expr.
type pending struct {
	kind  pendKind
	cands []*Decl
	tok   cst.Token
	// A pending call keeps its actuals and redoes the choice once the
	// context gives the result type.
	acts    []actual
	name    string
	object  *Expr
	fitting []*Decl // the candidates that fit before the context
	made    *Type   // the allocated subtype of an allocator
	// index is the other reading of a pending f(args): an index of
	// the result of a parameterless f (LRM 8.4, 9.3.4).
	index *indexAlt
}

// expr types the expression n against want (nil: no expected type),
// settles it and checks the result against want: the result is final.
// want is the type the context requires (LRM 12.5); a mismatch is
// sema/type-mismatch, reported once.
func (a *analyzer) expr(n cst.Node, want *Type) *Expr {
	e := a.operand(n, want)
	a.settle(e, want)
	if !want.Unknown() && want.Base().Class != TypeGeneric &&
		a.typeMarkValue(e, n) {
		// Generic type actuals have a generic want.
		return e
	}
	a.expect(e, want, n)
	return e
}

// checkReads reports the ports e reads that cannot be read: of mode
// out before VHDL-2008 and of mode linkage (LRM 6.5.2). Attribute
// prefixes are not reads.
func (a *analyzer) checkReads(e *Expr, depth int) {
	if e == nil || a.quiet || depth > 64 {
		return
	}
	switch e.Kind {
	case ExprName, ExprIndex, ExprSlice, ExprSelect:
		if d := actualObject(e); d != nil && d.Kind == DeclPort {
			switch {
			case d.Mode == ModeLinkage:
				a.semaError(e.Node.Span(), "port "+d.Text+" has mode "+
					"linkage and cannot be read")
				return
			case d.Mode == ModeOut && a.std < token.Std2008:
				a.semaError(e.Node.Span(), "port "+d.Text+" has mode "+
					"out and cannot be read before VHDL-2008")
				return
			}
		}
		for x := e; x != nil && (x.Kind == ExprIndex ||
			x.Kind == ExprSlice || x.Kind == ExprSelect); x = x.Prefix {
			for _, arg := range x.Args {
				a.checkReads(arg, depth+1)
			}
		}
	case ExprAttr, ExprCall, ExprQualified, ExprConversion, ExprCond:
		for _, arg := range e.Args {
			a.checkReads(arg, depth+1)
		}
	case ExprAggregate:
		for _, as := range e.Assocs {
			a.checkReads(as.Value, depth+1)
		}
	}
}

// literalLength reports a string or bit-string literal whose length
// differs from that of the constrained one-dimensional array subtype t
// it is a value of, when t's range has literal bounds (LRM 9.3.2).
func (a *analyzer) literalLength(e *Expr, t *Type) {
	if a.quiet || a.sliceValue || t.isDynamic() || t.Dims() != 1 {
		return
	}
	cons := t.Constraints()
	if cons == nil || cons[0] == nil || cons[0].Attr != nil {
		return
	}
	l, ok1 := literalInt(cons[0].Left)
	r, ok2 := literalInt(cons[0].Right)
	if !ok1 || !ok2 {
		return
	}
	want := r - l + 1
	if cons[0].Dir == eval.Downto {
		want = l - r + 1
	}
	want = max(want, 0)
	have, ok := literalLen(e.Lit)
	if !ok || have == want {
		return
	}
	e.illTyped = true
	a.semaError(e.Node.Span(), "the literal has "+strconv.FormatInt(have,
		10)+" elements, but "+t.Name()+" has "+
		strconv.FormatInt(want, 10))
}

// literalLen is the number of elements of a string or bit-string
// literal.
func literalLen(lit cst.Token) (int64, bool) {
	text := lit.Text()
	switch lit.Kind() {
	case token.StringLiteral:
		if len(text) < 2 {
			return 0, false
		}
		body := strings.ReplaceAll(text[1:len(text)-1], `""`, `"`)
		return int64(utf8.RuneCountInString(body)), true
	case token.BitStringLiteral:
		x, ok := ExpandBitString(text, 1<<20)
		return int64(len(x)), ok
	}
	return 0, false
}

// typeMarkValue reports a type mark used where a value is required
// (LRM 9.1: a type mark is no expression) and reports whether e is one.
func (a *analyzer) typeMarkValue(e *Expr, n cst.Node) bool {
	if e == nil || e.Kind != ExprTypeMark {
		return false
	}
	if !a.quiet && !e.illTyped {
		e.illTyped = true
		a.semaError(n.Span(), a.text(n)+" is a type, not a value")
	}
	return true
}

// typed types a top-level expression of a statement or declaration
// and records it in the unit's typed expressions.
func (a *analyzer) typed(n cst.Node, want *Type) *Expr {
	e := a.expr(n, want)
	if want.Unknown() {
		a.typeMarkValue(e, n)
	}
	if !a.mapActual {
		a.checkReads(e, 0)
	}
	if !n.IsZero() {
		a.u.Exprs = append(a.u.Exprs, e)
	}
	return e
}

// operand types n; when its type depends on the context (literals,
// aggregates, overloaded names) and want does not decide it, the
// result stays pending for the caller to settle.
func (a *analyzer) operand(n cst.Node, want *Type) *Expr {
	if n.IsZero() {
		return unknownExpr(n)
	}
	switch n.Kind() {
	case cst.KindSimpleName, cst.KindOperatorSymbol,
		cst.KindCharacterName, cst.KindSelectedName,
		cst.KindCallOrIndexName, cst.KindSliceName,
		cst.KindAttributeName, cst.KindExternalName:
		nm := a.resolveName(n, RefRead, want)
		return a.valueOperand(nm, n, want)
	case cst.KindLiteralExpr:
		return a.literal(cst.LiteralExpr{Node: n}, want)
	case cst.KindPhysicalLiteral:
		return a.physical(cst.PhysicalLiteral{Node: n})
	case cst.KindParenExpr:
		e := a.operand(cst.ParenExpr{Node: n}.Inner().Node, want)
		return e
	case cst.KindAggregate:
		if want.Unknown() {
			e := &Expr{Kind: ExprAggregate, Node: n, Type: UnknownType}
			e.pend = &pending{kind: pendAggregate}
			return e
		}
		return a.aggregate(n, want)
	case cst.KindBinaryExpr:
		b := cst.BinaryExpr{Node: n}
		return a.operator(n, b.Op(), []cst.Node{b.Lhs().Node,
			b.Rhs().Node}, want)
	case cst.KindUnaryExpr:
		u := cst.UnaryExpr{Node: n}
		return a.operator(n, u.Op(), []cst.Node{u.Operand().Node}, want)
	case cst.KindQualifiedExpr:
		return a.qualified(cst.QualifiedExpr{Node: n})
	case cst.KindAllocator:
		return a.allocator(cst.Allocator{Node: n}, want)
	case cst.KindConditionalExpr:
		e := &Expr{Kind: ExprCond, Node: n, Type: orUnknown(want)}
		for br := range (cst.ConditionalExpr{Node: n}).Branches() {
			v := a.expr(br.Value().Node, want)
			e.Args = append(e.Args, v)
			if c := whenCondition(br); !c.IsZero() {
				e.Args = append(e.Args, a.condition(c))
			}
			if want.Unknown() && !v.Type.Unknown() {
				e.Type = v.Type
			}
		}
		return e
	case cst.KindRangeExpr:
		rc := a.discreteRange(n, want)
		return &Expr{Kind: ExprRange, Node: n, Range: rc,
			Type: orUnknown(rc.Type)}
	case cst.KindSubtypeIndication:
		t := a.subtypeIndication(cst.SubtypeIndication{Node: n})
		return &Expr{Kind: ExprTypeMark, Node: n, Type: t}
	case cst.KindOpenExpr:
		return &Expr{Kind: ExprOpen, Node: n, Type: orUnknown(want)}
	case cst.KindOthersChoice:
		return &Expr{Kind: ExprOthers, Node: n, Type: orUnknown(want)}
	case cst.KindInertialActual:
		return a.operand(cst.InertialActual{Node: n}.Value().Node, want)
	case cst.KindBoxExpr, cst.KindUnaffectedExpr, cst.KindErrorNode:
		return unknownExpr(n)
	}
	a.psl(n)
	return unknownExpr(n)
}

// valueOperand turns a resolved name into a (possibly pending) value.
func (a *analyzer) valueOperand(nm named, n cst.Node, want *Type) *Expr {
	switch {
	case nm.unknown:
		if nm.expr != nil {
			return nm.expr
		}
		return unknownExpr(n)
	case len(nm.decls) > 0 && nm.decls[0].Overloadable():
		return a.call(n, nm, cst.AssociationList{}, want, false)
	case nm.expr != nil:
		return nm.expr
	case nm.typ != nil:
		return &Expr{Kind: ExprTypeMark, Node: n, Type: nm.typ}
	case nm.rng != nil:
		return &Expr{Kind: ExprRange, Node: n, Range: nm.rng,
			Type: orUnknown(nm.rng.Type)}
	}
	if len(nm.decls) > 0 && !a.quiet {
		d := nm.decls[0].Denotes()
		a.semaError(n.Span(), a.text(n)+" is "+article(d.Kind)+" "+
			d.Kind.String()+", not a value")
	}
	return unknownExpr(n)
}

// value is valueOperand settled against want.
func (a *analyzer) value(nm named, n cst.Node, want *Type) *Expr {
	e := a.valueOperand(nm, n, want)
	a.settle(e, want)
	return e
}

func article(k DeclKind) string {
	switch k.String()[0] {
	case 'a', 'e', 'i', 'o', 'u':
		return "an"
	}
	return "a"
}

// settle decides a pending expression with the type t its context
// requires (nil or unknown: as well as possible) and records the
// references it was waiting for.
func (a *analyzer) settle(e *Expr, t *Type) {
	if e == nil || e.pend == nil {
		return
	}
	p := e.pend
	e.pend = nil
	switch p.kind {
	case pendChar, pendName:
		var fit []*Decl
		for _, c := range p.cands {
			if t.Unknown() || valueFits(c, t) {
				fit = append(fit, c)
			}
		}
		kind := RefRead
		if p.kind == pendName && len(fit) > 0 &&
			fit[0].Denotes().Kind != DeclEnumLiteral {
			kind = RefCall
		}
		o := &Overload{Name: p.tok.Text()}
		for _, c := range p.cands {
			cd := Candidate{Decl: c, Fits: valueFits(c, t) ||
				t.Unknown()}
			if !cd.Fits {
				cd.Reason = "is " + orUnknown(c.Denotes().Type).Name()
			}
			o.Candidates = append(o.Candidates, cd)
		}
		o.Candidates = sortedCandidates(o)
		switch {
		case len(fit) == 1 || (len(fit) > 1 && sameDenotation(fit)):
			o.Chosen = fit[0]
			e.Decl, e.Type = fit[0], orUnknown(fit[0].Denotes().Type)
		case len(fit) == 0:
			if !t.Unknown() && !a.quiet {
				e.illTyped = true
				a.report(p.tok.Span(), diag.New(diag.SemaTypeMismatch,
					t.Name(), p.tok.Text()))
			}
		default:
			if bt := commonType(fit); bt != nil {
				e.Type = bt
			}
		}
		a.refOverload(p.tok, o, kind)
	case pendString:
		if !t.Unknown() {
			e.Type = t
			a.literalLength(e, t)
		}
	case pendNull:
		if !t.Unknown() {
			e.Type = t
		} else {
			e.Type = NullType
		}
	case pendAggregate:
		*e = *a.aggregate(e.Node, t)
	case pendAlloc:
		if !t.Unknown() && t.Base().Class == TypeAccess {
			e.Type = t
			d := t.Base().Designated
			if !a.quiet && !d.Unknown() && !p.made.Unknown() &&
				!Compatible(p.made, d) {
				e.illTyped = true
				a.report(e.Node.Span(), diag.New(diag.SemaTypeMismatch,
					d.Name(), p.made.Name()))
			}
		}
	case pendCall:
		if x := p.index; x != nil && !t.Unknown() &&
			Compatible(x.elem, t) && !fitsResult(p.fitting, t) {
			*e = *a.indexReading(e.Node, p.tok, p.cands, x, p.acts,
				p.object)
			return
		}
		*e = *a.finishCall(e.Node, p.tok, p.name, p.cands, p.acts, t,
			false, p.object, true)
	}
}

// fitsResult reports whether one of ds returns a value of t.
func fitsResult(ds []*Decl, t *Type) bool {
	for _, d := range ds {
		if rt := d.Denotes().Type; rt != nil && Compatible(rt, t) {
			return true
		}
	}
	return false
}

// sameDenotation reports whether every declaration denotes one entity.
func sameDenotation(ds []*Decl) bool {
	for _, d := range ds[1:] {
		if d.Denotes() != ds[0].Denotes() {
			return false
		}
	}
	return true
}

// commonType returns the type all of ds return when they agree.
func commonType(ds []*Decl) *Type {
	var t *Type
	for _, d := range ds {
		dt := d.Denotes().Type
		if dt == nil {
			return nil
		}
		if t == nil {
			t = dt
		} else if t.Base() != dt.Base() {
			return nil
		}
	}
	return t
}

// valueFits reports whether the enumeration literal or parameterless
// function d can be a value of type t.
func valueFits(d *Decl, t *Type) bool {
	x := d.Denotes()
	if x.Type == nil {
		return false
	}
	if t.Unknown() {
		return true
	}
	return Compatible(x.Type, t)
}

// literal types a literal.
func (a *analyzer) literal(l cst.LiteralExpr, want *Type) *Expr {
	tok := l.Literal()
	e := &Expr{Kind: ExprLiteral, Node: l.Node, Lit: tok,
		Type: UnknownType}
	switch tok.Kind() {
	case token.IntegerLiteral:
		e.Type = UniversalInteger
	case token.RealLiteral:
		e.Type = UniversalReal
	case token.CharacterLiteral:
		r := a.lookup(TokenKey(tok))
		if len(r.Decls) == 0 {
			if len(r.Conflict) > 0 {
				a.conflict(tok, r.Conflict)
			} else {
				a.undefined(tok)
			}
			return e
		}
		e.Kind = ExprName
		e.pend = &pending{kind: pendChar, cands: r.Decls, tok: tok}
	case token.StringLiteral, token.BitStringLiteral:
		e.pend = &pending{kind: pendString}
	case token.KwNull:
		e.pend = &pending{kind: pendNull}
	}
	if e.pend != nil && !want.Unknown() {
		a.settle(e, want)
	}
	return e
}

// physical types a physical literal such as 10 ns.
func (a *analyzer) physical(p cst.PhysicalLiteral) *Expr {
	e := &Expr{Kind: ExprPhysical, Node: p.Node, Lit: p.Value(),
		Type: UnknownType}
	nm := a.resolveName(p.Unit().Node, RefRead, nil)
	for _, d := range nm.decls {
		if d.Denotes().Kind == DeclUnit {
			e.Decl, e.Type = d, orUnknown(d.Denotes().Type)
			if len(nm.decls) > 1 {
				a.ref(nm.tok, d, RefRead)
			}
			break
		}
	}
	return e
}

// qualified types T'(expr) and T'(aggregate).
func (a *analyzer) qualified(q cst.QualifiedExpr) *Expr {
	t := a.typeMark(q.TypeMark().Node)
	op := qualOperand(q)
	var x *Expr
	if op.Kind() == cst.KindAggregate {
		x = a.aggregate(op, t)
	} else {
		x = a.expr(op, t)
		a.expect(x, t, op)
	}
	return &Expr{Kind: ExprQualified, Node: q.Node, Type: t,
		Args: []*Expr{x}}
}

// conversion types the type conversion T(expr) (LRM 9.3.6). The
// operand's type is decided without the context; a literal, aggregate
// or overloaded name the operand alone does not decide is settled
// with T when it fits, as GHDL and NVC accept. A conversion between
// types that are not closely related is an error.
func (a *analyzer) conversion(n cst.Node, t *Type,
	args cst.AssociationList,
) *Expr {
	var x *Expr
	count := 0
	for item := range args.Items() {
		if count == 0 {
			x = a.operand(item.Actual().Node, nil)
			if x.pend != nil {
				if s, _ := fit(x, t); s > 0 {
					a.settle(x, t)
				} else {
					a.settle(x, nil)
				}
			}
		} else {
			a.expr(item.Actual().Node, nil)
		}
		count++
	}
	e := &Expr{Kind: ExprConversion, Node: n, Type: t}
	if x == nil {
		return e
	}
	e.Args = []*Expr{x}
	switch {
	case count > 1 && !a.quiet:
		a.semaError(n.Span(), "a type conversion takes one operand, not "+
			strconv.Itoa(count))
	case !a.quiet && !x.illTyped && !closelyRelated(x.Type, t, a.std):
		x.illTyped, e.illTyped = true, true
		a.semaError(x.Node.Span(), "cannot convert "+x.Type.Name()+
			" to "+t.Name()+": the types are not closely related")
	}
	return e
}

// closelyRelated reports whether a value of type from may be converted
// to type to (LRM 9.3.6): the same base type, two numeric types, or two
// array types of the same dimensionality whose element types are
// closely related (VHDL-2008) or the same (earlier standards). Unknown
// and generic types are related to everything.
func closelyRelated(from, to *Type, std token.Standard) bool {
	if from.Unknown() || to.Unknown() {
		return true
	}
	f, t := from.Base(), to.Base()
	switch {
	case f == t:
		return true
	case f.Class == TypeGeneric || t.Class == TypeGeneric,
		f.Class == TypeIncomplete || t.Class == TypeIncomplete:
		return true
	case f.IsNumeric() && t.IsNumeric():
		return true
	case f.Class == TypeArray && t.Class == TypeArray:
		if len(f.Index) != len(t.Index) {
			return false
		}
		fe, te := f.Elem, t.Elem
		if std >= token.Std2008 {
			return closelyRelated(fe, te, std)
		}
		return fe.Unknown() || te.Unknown() || fe.Base() == te.Base() ||
			fe.Base().Class == TypeGeneric ||
			te.Base().Class == TypeGeneric
	}
	return false
}

// allocator types new T and new T'(expr) (LRM 9.3.7): the context's
// access type decides the result, and its designated type must be the
// allocated one.
func (a *analyzer) allocator(al cst.Allocator, want *Type) *Expr {
	var made *Type
	var args []*Expr
	bad := false
	if op := al.Operand(); !op.IsZero() {
		x := a.expr(op.Node, nil)
		made = x.Type
		args = append(args, x)
	} else {
		made = a.subtypeIndication(al.Subtype())
	}
	t := UnknownType
	if !want.Unknown() && want.Base().Class == TypeAccess {
		t = want
		d := want.Base().Designated
		if !a.quiet && !d.Unknown() && !made.Unknown() &&
			!Compatible(made, d) {
			a.report(al.Node.Span(), diag.New(diag.SemaTypeMismatch,
				d.Name(), made.Name()))
			bad = true
		}
	}
	e := &Expr{Kind: ExprAllocator, Node: al.Node, Type: t, Args: args,
		illTyped: bad}
	if t.Unknown() {
		// The context decides which access type: settle reads it.
		e.pend = &pending{kind: pendAlloc, made: made}
	}
	return e
}

// aggregate types an aggregate against the type its context gives.
func (a *analyzer) aggregate(n cst.Node, want *Type) *Expr {
	e := &Expr{Kind: ExprAggregate, Node: n, Type: orUnknown(want)}
	b := want.Base()
	pos := 0
	var row *Type
	for as := range (cst.Aggregate{Node: n}).Associations() {
		choices := as.Choices()
		var vt, slice *Type
		var assoc Assoc
		positional := choices.IsZero()
		switch {
		case b != nil && b.Class == TypeRecord:
			if positional {
				if pos < len(b.Fields) {
					vt = want.FieldType(b.Fields[pos])
				}
				pos++
			}
			for c := range choices.Items() {
				ce, ft := a.recordChoice(c.Node, want)
				assoc.Choices = append(assoc.Choices, ce)
				if ce.Kind == ExprOthers {
					assoc.Others = true
				}
				if vt == nil {
					vt = ft
				}
			}
		case b != nil && b.Class == TypeArray:
			vt = want.ElemType()
			if b.Dims() > 1 {
				if row == nil {
					row = rowType(want)
				}
				vt = row
			} else if a.std >= token.Std2008 {
				// VHDL-2008 (LRM 9.3.3.3): the value of a positional or
				// range association may also be of the aggregate's type.
				slice = want
			}
			for c := range choices.Items() {
				ce := a.arrayChoice(c.Node, firstIndex(b))
				assoc.Choices = append(assoc.Choices, ce)
				if ce.Kind == ExprOthers {
					assoc.Others = true
					if unconstrainedArray(want) && !a.quiet {
						a.semaError(c.Node.Span(), "others needs an "+
							"index range from the context, and "+
							want.Name()+" is unconstrained")
					}
				}
			}
		default:
			for c := range choices.Items() {
				var ce *Expr
				if c.Kind() == cst.KindSimpleName {
					saved := a.quiet
					a.quiet = true
					ce = a.expr(c.Node, nil)
					a.quiet = saved
				} else {
					ce = a.expr(c.Node, nil)
				}
				assoc.Choices = append(assoc.Choices, ce)
			}
		}
		v := as.Value().Node
		if v.IsZero() {
			// A positional association has no choices: the parser keeps
			// its expression as the only child.
			for c := range as.Nodes() {
				if c.Kind() != cst.KindChoices {
					v = c
				}
			}
		}
		if slice != nil && sliceChoices(assoc.Choices) {
			assoc.Value, assoc.Slice = a.elementOrSlice(v, vt, slice)
		} else {
			assoc.Value = a.expr(v, vt)
		}
		e.Assocs = append(e.Assocs, assoc)
	}
	if b != nil && b.Class == TypeArray {
		a.singleDynamicChoice(e)
		a.mixedAssociations(e)
		a.aggregateShape(e, want) // aggshape.go
	}
	return e
}

// mixedAssociations reports an array aggregate with both positional and
// named associations (others aside, LRM 9.3.3.3).
func (a *analyzer) mixedAssociations(e *Expr) {
	if a.quiet {
		return
	}
	pos, named := false, false
	for _, as := range e.Assocs {
		switch {
		case len(as.Choices) == 0:
			pos = true
		case !as.Others:
			named = true
		}
	}
	if pos && named {
		a.semaError(e.Node.Span(), "an array aggregate's associations "+
			"must be all positional or all named")
	}
}

// singleDynamicChoice reports an array aggregate choice that is not
// locally static in an aggregate with more than one association or
// choice (LRM 9.3.3.3).
func (a *analyzer) singleDynamicChoice(e *Expr) {
	if a.quiet {
		return
	}
	n := 0
	for _, as := range e.Assocs {
		n += max(len(as.Choices), 1)
	}
	if n < 2 {
		return
	}
	for _, as := range e.Assocs {
		for _, c := range as.Choices {
			if c.Kind == ExprOthers {
				continue
			}
			if st, known := locallyStatic(c, 0, a.std); known && !st {
				a.semaError(c.Node.Span(), "the choice "+a.text(c.Node)+
					" is not locally static, so it must be the only "+
					"choice of the aggregate")
				return
			}
		}
	}
}

// sliceChoices reports whether an association with these choices may
// give a slice of the aggregate (LRM 9.3.3.3): it is positional or its
// only choice is a discrete range.
func sliceChoices(cs []*Expr) bool {
	return len(cs) == 0 || len(cs) == 1 && cs[0].Kind == ExprRange
}

// elementOrSlice types the value of a VHDL-2008 array aggregate
// association that may be an element (elem) or a slice of the
// aggregate's type (whole). The element wins whenever it fits; the
// result reports whether the value is a slice.
func (a *analyzer) elementOrSlice(n cst.Node, elem, whole *Type,
) (*Expr, bool) {
	v := a.operand(n, nil)
	if elem.Unknown() {
		a.settle(v, elem)
		return v, false
	}
	if s, _ := fit(v, elem); s == 0 {
		if s, _ := fit(v, whole); s > 0 {
			// The slice's length is its choice's, not the
			// aggregate's.
			saved := a.sliceValue
			a.sliceValue = true
			a.settle(v, whole)
			a.sliceValue = saved
			a.expect(v, whole, n)
			return v, true
		}
	}
	a.settle(v, elem)
	a.expect(v, elem, n)
	return v, false
}

// rowType is the type of each element of a multi-dimensional array
// aggregate of type t: an array over the remaining dimensions (LRM
// 9.3.3.3), constrained by t's remaining index constraints.
func rowType(t *Type) *Type {
	b := t.Base()
	base := &Type{Class: TypeArray, Index: b.Index[1:], Elem: b.Elem}
	cons := t.Constraints()
	if len(cons) != len(b.Index) {
		return base
	}
	for _, c := range cons[1:] {
		if c == nil {
			return base
		}
	}
	st := NewSubtype(base)
	st.IndexCons = cons[1:]
	if es := t.ElemType(); es != b.Elem {
		st.ElemSub = es
	}
	return st
}

// recordChoice types one choice of a record aggregate and returns the
// element's type.
func (a *analyzer) recordChoice(c cst.Node, want *Type) (*Expr, *Type) {
	b := want.Base()
	switch c.Kind() {
	case cst.KindOthersChoice:
		return &Expr{Kind: ExprOthers, Node: c, Type: UnknownType}, nil
	case cst.KindSimpleName:
		tok := cst.SimpleName{Node: c}.Ident()
		for _, f := range b.Fields {
			if f.Name == TokenKey(tok) {
				a.ref(tok, f, RefRead)
				ft := want.FieldType(f)
				return &Expr{Kind: ExprName, Node: c, Decl: f,
					Type: orUnknown(ft)}, ft
			}
		}
		if !a.quiet {
			a.semaError(tok.Span(), b.Name()+" has no element "+
				tok.Text())
		}
	}
	return unknownExpr(c), nil
}

// arrayChoice types one choice of an array aggregate: an index, a
// discrete range or others.
func (a *analyzer) arrayChoice(c cst.Node, idx *Type) *Expr {
	if c.Kind() == cst.KindOthersChoice {
		return &Expr{Kind: ExprOthers, Node: c, Type: UnknownType}
	}
	if isRangeSyntax(c) {
		rc := a.discreteRange(c, idx)
		return &Expr{Kind: ExprRange, Node: c, Range: rc,
			Type: orUnknown(rc.Type)}
	}
	e := a.operand(c, idx)
	if e.Kind == ExprTypeMark {
		return &Expr{Kind: ExprRange, Node: c, Type: e.Type,
			Range: &RangeCons{Node: c, Attr: e, Type: e.Type}}
	}
	a.settle(e, idx)
	return e
}

// discreteRange types a range or discrete range: L to R, L downto R, a
// range attribute, or a (constrained) subtype. want is the expected
// type of the bounds (an index subtype), nil if none.
func (a *analyzer) discreteRange(n cst.Node, want *Type) *RangeCons {
	switch n.Kind() {
	case cst.KindRangeExpr:
		re := cst.RangeExpr{Node: n}
		typeDef := a.typeDef
		a.typeDef = false // not for ranges inside the bounds
		l := a.operand(re.Low().Node, want)
		r := a.operand(re.High().Node, want)
		a.typeMarkValue(l, re.Low().Node)
		a.typeMarkValue(r, re.High().Node)
		if typeDef {
			a.settle(l, nil)
			a.settle(r, nil)
			a.typeDefBounds(n, l, r)
		}
		t := a.rangeType(l, r, want)
		if t.Unknown() && want.Unknown() {
			t = a.boundCandidates(n, l, r)
		}
		a.settle(l, t)
		a.settle(r, t)
		if !t.Unknown() && !typeDef {
			// Both bounds are of the range's type (LRM 5.2.1).
			a.expect(l, t, re.Low().Node)
			a.expect(r, t, re.High().Node)
		}
		dir := eval.To
		if re.Direction().Kind() == token.KwDownto {
			dir = eval.Downto
		}
		return &RangeCons{Node: n, Left: l, Right: r, Dir: dir, Type: t}
	case cst.KindSubtypeIndication:
		t := a.subtypeIndication(cst.SubtypeIndication{Node: n})
		return &RangeCons{Node: n, Type: t,
			Attr: &Expr{Kind: ExprTypeMark, Node: n, Type: t}}
	case cst.KindOpenExpr, 0:
		return nil
	case cst.KindErrorNode:
		return &RangeCons{Node: n, Type: UnknownType}
	case cst.KindSimpleName, cst.KindSelectedName,
		cst.KindAttributeName, cst.KindCallOrIndexName,
		cst.KindSliceName:
		nm := a.resolveName(n, RefRead, want)
		switch {
		case nm.rng != nil:
			return nm.rng
		case nm.typ != nil:
			return &RangeCons{Node: n, Type: nm.typ,
				Attr: &Expr{Kind: ExprTypeMark, Node: n, Type: nm.typ}}
		case nm.unknown:
			return &RangeCons{Node: n, Type: UnknownType}
		}
		if !a.quiet {
			a.semaError(n.Span(), a.text(n)+" is not a range")
		}
		return &RangeCons{Node: n, Type: UnknownType}
	}
	e := a.expr(n, want)
	return &RangeCons{Node: n, Type: orUnknown(e.Type)}
}

// typeDefBounds checks the bounds of an integer or floating type
// definition: of integer or floating types both, not necessarily the
// same type (LRM 5.2.3.1, 5.2.5.1).
func (a *analyzer) typeDefBounds(n cst.Node, l, r *Expr) {
	if a.quiet {
		return
	}
	for _, b := range []*Expr{l, r} {
		if nonNumeric(b) {
			a.semaError(b.Node.Span(), "the bounds of a type "+
				"definition must be integer or floating point, not "+
				a.text(b.Node))
			return
		}
	}
	for _, b := range []*Expr{l, r} {
		if st, known := locallyStatic(b, 0, a.std); known && !st {
			// LRM 5.2.1: the range of a type definition is locally
			// static.
			a.semaError(b.Node.Span(), "the bounds of a type "+
				"definition must be locally static")
			return
		}
	}
	lc, rc := numClass(l.Type), numClass(r.Type)
	if lc == 0 || rc == 0 || lc == rc {
		return
	}
	a.semaError(n.Span(), "the bounds of a type definition must both "+
		"be integer or both be floating point")
}

// nonNumeric reports a bound that is certainly not numeric: a string
// or bit-string literal, or a value of a non-numeric type.
func nonNumeric(e *Expr) bool {
	if e.Kind == ExprLiteral {
		switch e.Lit.Kind() {
		case token.StringLiteral, token.BitStringLiteral,
			token.CharacterLiteral:
			return true
		}
	}
	if e.Type.Unknown() {
		return false
	}
	switch e.Type.Base().Class {
	case TypeInteger, TypeFloating, TypeGeneric:
		return false
	}
	return true
}

func numClass(t *Type) TypeClass {
	if t.Unknown() {
		return 0
	}
	switch c := t.Base().Class; c {
	case TypeInteger, TypeFloating:
		return c
	}
	return 0
}

// boundCandidates decides the type of a range whose bounds are both
// overloaded (enumeration literals, parameterless functions) and that
// has no expected type, as in `for c in '0' to '1'`: the one base type
// both bounds can have. More than one is an ambiguity, none a
// mismatch (LRM 5.2.1, 12.5); either is reported at the range.
func (a *analyzer) boundCandidates(n cst.Node, l, r *Expr) *Type {
	lt, rt := boundTypes(l), boundTypes(r)
	if lt == nil || rt == nil {
		return UnknownType
	}
	var both []*Type
	for _, x := range lt {
		if slices.Contains(rt, x) {
			both = append(both, x)
		}
	}
	switch {
	case len(both) == 1:
		return both[0]
	case a.quiet:
	case len(both) == 0:
		a.semaError(n.Span(), "the bounds of the range have no "+
			"common type")
	default:
		a.semaError(n.Span(), "the type of the range is ambiguous: "+
			both[0].Name()+" or "+both[1].Name())
	}
	return UnknownType
}

// boundTypes returns the base types a pending overloaded bound may
// have, in candidate order, or nil when e is not such a bound.
func boundTypes(e *Expr) []*Type {
	if e.pend == nil || (e.pend.kind != pendChar &&
		e.pend.kind != pendName) {
		return nil
	}
	var out []*Type
	for _, c := range e.pend.cands {
		t := c.Denotes().Type
		if t == nil {
			return nil
		}
		if b := t.Base(); !slices.Contains(out, b) {
			out = append(out, b)
		}
	}
	return out
}

// rangeType is the type of a range's bounds (LRM 5.3.2.2): a bound's
// own type, else the expected type, and INTEGER for universal integer
// bounds (for loops, generate parameters, index constraints).
func (a *analyzer) rangeType(l, r *Expr, want *Type) *Type {
	known := func(e *Expr) bool {
		return e.pend == nil && !e.Type.Unknown() && !e.Type.Universal
	}
	switch {
	case known(l):
		return l.Type
	case known(r):
		return r.Type
	case !want.Unknown():
		return want
	}
	if (l.Type == UniversalInteger || r.Type == UniversalInteger) &&
		l.Type != UniversalReal && r.Type != UniversalReal {
		if t := a.stdType("integer"); t != nil {
			return t
		}
		return UniversalInteger
	}
	if l.Type == UniversalReal || r.Type == UniversalReal {
		return UniversalReal
	}
	return UnknownType
}

// condition types a condition: BOOLEAN, or since VHDL-2008 any type
// with a visible "??" operator (LRM 9.2.9).
func (a *analyzer) condition(n cst.Node) *Expr {
	boolean := a.stdType("boolean")
	e := a.operand(n, nil)
	if e.pend != nil {
		a.settle(e, boolean)
	}
	a.u.Exprs = append(a.u.Exprs, e)
	if e.Type.Unknown() || boolean == nil ||
		e.Type.Base() == boolean.Base() {
		return e
	}
	if a.std >= token.Std2008 {
		for _, d := range a.lookup(`"??"`).Decls {
			x := d.Denotes()
			if len(x.Params) == 1 && SameBase(x.Params[0].Type, e.Type) {
				return &Expr{Kind: ExprCall, Node: n, Decl: d,
					Type: boolean, Args: []*Expr{e}}
			}
		}
	}
	a.expect(e, boolean, n)
	return e
}

// expect reports sema/type-mismatch when e's type is known and is not
// compatible with want.
func (a *analyzer) expect(e *Expr, want *Type, n cst.Node) {
	if a.quiet || e == nil || e.illTyped || e.pend != nil ||
		want.Unknown() || e.Type.Unknown() || fits(e.Type, want) {
		return
	}
	switch e.Kind {
	case ExprTypeMark, ExprRange, ExprOthers, ExprOpen:
		// Not values: their callers decide what they may be.
		return
	}
	e.illTyped = true
	a.report(n.Span(), diag.New(diag.SemaTypeMismatch, want.Name(),
		e.Type.Name()))
}

// fits is Compatible, where an expected universal type (an attribute
// parameter) also accepts any type of its class.
func fits(have, want *Type) bool {
	if Compatible(have, want) {
		return true
	}
	return want.Universal && have.Base().Class == want.Class
}

// psl walks a PSL property, sequence or directive operand for names.
func (a *analyzer) psl(n cst.Node) {
	for c := range n.Nodes() {
		if (cst.Expr{}).Accepts(c.Kind()) &&
			c.Kind() != cst.KindPSLUnion &&
			c.Kind() != cst.KindPSLValueSet {
			a.pslExpr(c)
			continue
		}
		a.psl(c)
	}
}
