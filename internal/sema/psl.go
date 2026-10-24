package sema

import (
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/token"
)

// pslExpr analyzes n, a VHDL expression inside PSL code (a boolean, a
// clock expression), and records its typed tree for the simulator's
// PSL runner (#35). A name of a PSL sequence or property (a sequence
// or property instance written as a PSL boolean, as in `cover {s}`) is
// no value: it is resolved and left to the runner, untyped.
func (a *analyzer) pslExpr(n cst.Node) *Expr {
	if a.pslDeclName(n) {
		return nil
	}
	a.inPSL++
	e := a.expr(n, nil)
	a.inPSL--
	if a.f.PSL == nil {
		a.f.PSL = map[cst.Range]*Expr{}
	}
	a.f.PSL[n.Span()] = e
	return e
}

// PSLExpr returns the typed expression of the VHDL expression node n
// of the file's PSL code (the operand of a PSL boolean, a clock
// expression), or nil when n is not one.
func (f *File) PSLExpr(n cst.Node) *Expr {
	if f == nil || n.IsZero() {
		return nil
	}
	return f.PSL[n.Span()]
}

// pslDeclName reports whether n is a name, or a call with actuals, of
// a visible PSL declaration (IEEE 1850 6.1.1.1, 6.2.4: a named
// sequence or property is instantiated by its name), recording the
// reference.
func (a *analyzer) pslDeclName(n cst.Node) bool {
	var args cst.AssociationList
	if n.Kind() == cst.KindCallOrIndexName {
		c := cst.CallOrIndexName{Node: n}
		args = c.Args()
		n = c.Prefix().Node
	}
	if n.Kind() != cst.KindSimpleName {
		return false
	}
	tok := cst.SimpleName{Node: n}.Ident()
	k := TokenKey(tok)
	if k == "" {
		return false
	}
	r := a.lookup(k)
	if len(r.Decls) == 0 || r.Decls[0].Kind != DeclPSL {
		return false
	}
	a.ref(tok, r.Decls[0], RefRead)
	a.lenientArgs(args)
	if d := r.Decls[0]; d.Node.Kind() == cst.KindPSLPropertyDecl &&
		sequenceContext(n) {
		a.semaError(n.Span(), tok.Text()+" is a property, and a "+
			"sequence is needed here")
	}
	return true
}

// sequenceContext reports whether the PSL operand n stands where a
// sequence is needed (IEEE 1850 6.1.1, 7.1.4, 7.1.6): in a SERE, or as
// the operand of cover or restrict, so a property instance there is an
// error.
func sequenceContext(n cst.Node) bool {
	for p := n.Parent(); !p.IsZero(); p = p.Parent() {
		switch p.Kind() {
		case cst.KindPSLBoolean, cst.KindParenExpr, cst.KindCallOrIndexName:
			continue
		case cst.KindPSLBracedSere, cst.KindPSLSereConcat,
			cst.KindPSLSereFusion, cst.KindPSLSereOr, cst.KindPSLSereAnd,
			cst.KindPSLSereWithin, cst.KindPSLRepetition, cst.KindPSLCover,
			cst.KindPSLRestrict, cst.KindPSLRestrictGuarantee:
			return true
		}
		return false
	}
	return false
}

// sequenceBody reports a sequence declaration whose body body is a
// parenthesized property, not a sequence (IEEE 1850 6.1.1.1: a
// sequence is a braced, repeated or clocked SERE or an instance).
func (a *analyzer) sequenceBody(decl, body cst.Node) {
	if decl.Kind() == cst.KindPSLSequenceDecl &&
		body.Kind() == cst.KindPSLParenProp {
		a.semaError(body.Span(), "a sequence is a braced SERE, not a "+
			"parenthesized property")
	}
}

// pslBuiltins are the PSL built-in functions sema types (IEEE 1850
// 5.2.3, LRM 2008 PSL flavor): prev(e [, n [, clk]]) is of e's type;
// rose and fell take a BIT, STD_ULOGIC or BOOLEAN; stable any value;
// onehot and onehot0 a one-dimensional array of BIT or STD_ULOGIC;
// ended a sequence. All but prev return BOOLEAN.
var pslBuiltins = map[string]bool{"prev": true, "rose": true,
	"fell": true, "stable": true, "onehot": true, "onehot0": true,
	"ended": true}

// pslBuiltin types n, prefix(args) inside PSL code, as a call of a PSL
// built-in function when prefix is one and no declaration of that name
// is visible; ok is false otherwise.
func (a *analyzer) pslBuiltin(n cst.Node, want *Type) (*Expr, bool) {
	if a.inPSL == 0 || n.Kind() != cst.KindCallOrIndexName {
		return nil, false
	}
	c := cst.CallOrIndexName{Node: n}
	pn := c.Prefix().Node
	if pn.Kind() != cst.KindSimpleName {
		return nil, false
	}
	tok := cst.SimpleName{Node: pn}.Ident()
	name := strings.ToLower(tok.Text())
	if !pslBuiltins[name] || len(a.lookup(TokenKey(tok)).Decls) > 0 {
		return nil, false
	}
	var nodes []cst.Node
	for item := range c.Args().Items() {
		if v := item.Actual(); !v.IsZero() {
			nodes = append(nodes, v.Node)
		}
	}
	return a.pslCall(n, name, nodes), true
}

// pslCall types the call n of the PSL built-in function name with the
// actuals nodes.
func (a *analyzer) pslCall(n cst.Node, name string, nodes []cst.Node,
) *Expr {
	var args []*Expr
	for _, v := range nodes {
		if name == "ended" && a.pslDeclName(v) {
			continue
		}
		args = append(args, a.expr(v, nil))
	}
	e := &Expr{Kind: ExprUnknown, Node: n, Type: a.stdType("boolean")}
	if len(args) == 0 {
		if name != "ended" {
			a.semaError(n.Span(), name+" needs an argument")
		}
		return e
	}
	x := args[0]
	if x.illTyped || x.Type.Unknown() {
		return e
	}
	b := x.Type.Base()
	switch name {
	case "prev":
		e.Type = x.Type
	case "rose", "fell":
		if !a.bitOrULogic(b) && b != a.stdType("boolean").Base() {
			a.semaError(x.Node.Span(), "the argument of "+name+
				" must be BIT, STD_ULOGIC or BOOLEAN, not "+x.Type.Name())
		}
	case "onehot", "onehot0":
		if b.Class != TypeArray || len(b.Index) != 1 ||
			!a.bitOrULogic(b.Elem.Base()) {
			a.semaError(x.Node.Span(), "the argument of "+name+
				" must be a one-dimensional array of BIT or STD_ULOGIC, "+
				"not "+x.Type.Name())
		}
	}
	return e
}

// pslBuiltinCall types a PSL built-in function call that is a PSL
// operand on its own.
func (a *analyzer) pslBuiltinCall(n cst.Node) {
	c := cst.PSLBuiltinCall{Node: n}
	name := strings.ToLower(c.Function().Text())
	if !pslBuiltins[name] {
		for x := range c.Args() {
			a.pslExpr(x.Node)
		}
		return
	}
	var nodes []cst.Node
	for x := range c.Args() {
		nodes = append(nodes, x.Node)
	}
	a.inPSL++
	a.pslCall(n, name, nodes)
	a.inPSL--
}

// pslForm checks the PSL operator rules on the node n after its
// operands are analyzed (psl calls it once per PSL node):
//
//   - the operand of a goto ([->) or non-consecutive ([=) repetition
//     is a Boolean, not a SERE (IEEE 1850 6.1.1.2.2);
//   - the left operand of a suffix implication is a sequence (IEEE
//     1850 6.2.1.6.1);
//   - in the simple subset, which simulation evaluates, the operand of
//     never is a Boolean or a sequence (IEEE 1850 4.4.4);
//   - a range's low bound does not exceed its high bound (IEEE 1850
//     5.3.1, 6.1.1.2);
//   - a clock expression is a Boolean (IEEE 1850 6.1.1.1);
//   - the operands of union are of one type (IEEE 1850 5.1.3).
func (a *analyzer) pslForm(n cst.Node) {
	if a.quiet {
		return
	}
	kids := pslKids(n)
	kid := func(i int) cst.Node {
		if i < len(kids) {
			return kids[i]
		}
		return cst.Node{}
	}
	switch n.Kind() {
	case cst.KindPSLRepetition:
		if !n.Token(token.PSLRepGoto, 0).IsZero() ||
			!n.Token(token.PSLRepEq, 0).IsZero() {
			if op := kid(0); !op.IsZero() &&
				op.Kind() != cst.KindPSLCount &&
				op.Kind() != cst.KindPSLBoolean {
				a.semaError(op.Span(), "the operand of a goto or "+
					"non-consecutive repetition must be a Boolean")
			}
		}
	case cst.KindPSLSuffixImplication:
		if l := kid(0); !l.IsZero() && !pslSequence(l) {
			a.semaError(l.Span(), "the left operand of a suffix "+
				"implication must be a sequence")
		}
	case cst.KindPSLNever:
		if op := kid(0); !op.IsZero() && !pslSequence(op) {
			a.semaError(op.Span(), "the operand of never must be a "+
				"Boolean or a sequence")
		}
	case cst.KindPSLCount:
		a.pslCount(n)
	case cst.KindPSLClockedProp:
		if c := childAfter(n, token.At); !c.IsZero() {
			a.pslClock(c)
		}
	case cst.KindPSLUnion:
		u := cst.PSLUnion{Node: n}
		l, r := a.f.PSLExpr(u.Lhs().Node), a.f.PSLExpr(u.Rhs().Node)
		if l != nil && r != nil && !l.illTyped && !r.illTyped &&
			!l.Type.Unknown() && !r.Type.Unknown() &&
			!Compatible(l.Type, r.Type) && !Compatible(r.Type, l.Type) {
			a.semaError(n.Span(), "the operands of union must be of "+
				"one type, not "+l.Type.Name()+" and "+r.Type.Name())
		}
	}
}

// pslKids returns the child nodes of n.
func pslKids(n cst.Node) []cst.Node {
	var out []cst.Node
	for c := range n.Nodes() {
		out = append(out, c)
	}
	return out
}

// pslSequence reports whether the PSL operand n is a Boolean or a
// sequence (a braced, repeated or clocked SERE, a sequence instance).
func pslSequence(n cst.Node) bool {
	switch n.Kind() {
	case cst.KindPSLBoolean, cst.KindPSLBracedSere,
		cst.KindPSLRepetition, cst.KindPSLSequenceInstance,
		cst.KindPSLSereConcat, cst.KindPSLSereFusion, cst.KindPSLSereOr,
		cst.KindPSLSereAnd, cst.KindPSLSereWithin, cst.KindPSLBuiltinCall,
		cst.KindErrorNode:
		return true
	case cst.KindPSLParenProp, cst.KindPSLClockedProp:
		for c := range n.Nodes() {
			return pslSequence(c)
		}
		return true
	}
	return false
}

// pslCount reports a PSL range low to high with literal bounds where
// low exceeds high.
func (a *analyzer) pslCount(n cst.Node) {
	if n.Token(token.KwTo, 0).IsZero() {
		return
	}
	var bounds []int64
	for c := range n.Nodes() {
		e := a.f.PSLExpr(c)
		if e == nil {
			return
		}
		v, ok := literalInt(e)
		if !ok {
			return
		}
		bounds = append(bounds, v)
	}
	if len(bounds) == 2 && bounds[0] > bounds[1] {
		a.semaError(n.Span(), "the low bound of a PSL range cannot "+
			"exceed its high bound")
	}
}

// pslClock reports a clock expression that is not a PSL Boolean
// (BOOLEAN, BIT or STD_ULOGIC in the VHDL flavor).
func (a *analyzer) pslClock(c cst.Node) {
	e := a.f.PSLExpr(c)
	if e == nil || e.illTyped || e.Type.Unknown() {
		return
	}
	b := e.Type.Base()
	if b == a.stdType("boolean").Base() || a.bitOrULogic(b) ||
		b.Class == TypeGeneric {
		return
	}
	a.semaError(c.Span(), "a clock expression must be a Boolean, not "+
		e.Type.Name())
}

// vunitArch checks the architecture of a verification unit binding
// vunit v(e(arch)): e's architecture arch exists (IEEE 1850 7.2, LRM
// 2008 Annex: the binding names a design unit), checked once every
// unit is analyzed. It returns the bound region visible from the vunit
// body: the architecture's when found, else the entity's.
func (a *analyzer) vunitArch(h cst.Node) *Region {
	var first cst.Node
	for n := range (cst.PSLHierarchicalName{Node: h}).Names() {
		first = n.Node
		break
	}
	if first.IsZero() {
		return nil
	}
	var arch cst.Token
	after := false
	for e := range h.Elements() {
		switch {
		case !e.IsToken():
			after = after || e.Node.Equal(first)
		case after && e.Token.Kind() == token.Identifier && arch.IsZero():
			arch = e.Token
		}
	}
	if arch.IsZero() {
		return nil
	}
	if first.Kind() != cst.KindSimpleName {
		return nil
	}
	// The unit is named in the working library (the vunit's own).
	l := a.s.libs[a.u.Library]
	if l == nil {
		return nil
	}
	k := TokenKey(cst.SimpleName{Node: first}.Ident())
	if u, ok := l.UnitFrom(k, a.u); ok && u.Decl != nil &&
		u.Decl.Kind == DeclEntity {
		a.lateArch(arch, u.Decl)
		for _, sec := range l.Secondary(u.Name) {
			if sec.Kind == UnitArchitecture && sec.Name == TokenKey(arch) &&
				sec.Region != nil {
				return sec.Region
			}
		}
		return u.Decl.Region
	}
	return nil
}
