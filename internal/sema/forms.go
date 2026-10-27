package sema

import (
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/token"
)

// formRules checks the statement and expression forms the parser
// accepts but the grammar or its rules exclude (syntaxChecks calls it
// once per node):
//
//   - a case statement or case generate has an alternative, and a
//     matching case (case?) ends with end case ? (LRM 10.9, 11.8);
//   - block and generate statements are labeled (LRM 11.2, 11.8);
//   - a generic clause precedes the port clause (LRM 3.2.1, 6.8);
//   - an entity statement is a concurrent assertion, a procedure call,
//     a process or a PSL directive (LRM 3.2.4);
//   - before VHDL-2019 the right operand of ** is a primary, not a
//     unary expression (LRM 9.1; the parser reports a misplaced
//     sign);
//   - inertial marks only a port map actual (LRM 6.5.7.1);
//   - a named return subtype is VHDL-2019, and not of an interface
//     function (LRM-2019 4.2.1).
func (a *analyzer) formRules(n cst.Node) {
	switch n.Kind() {
	case cst.KindCaseStmt, cst.KindCaseGenerateStmt:
		a.caseForm(n)
	case cst.KindBlockStmt, cst.KindForGenerateStmt,
		cst.KindIfGenerateStmt:
		if n.Child[cst.Label](0).IsZero() {
			what := "a generate"
			if n.Kind() == cst.KindBlockStmt {
				what = "a block"
			}
			a.semaError(n.Span(), what+" statement needs a label")
		}
	case cst.KindComponentDecl, cst.KindEntityDecl:
		port := false
		for c := range n.Nodes() {
			switch c.Kind() {
			case cst.KindPortClause:
				port = true
			case cst.KindGenericClause:
				if port {
					a.semaError(c.Span(), "the generic clause must "+
						"come before the port clause")
				}
			}
		}
		if n.Kind() == cst.KindEntityDecl {
			a.entityStatements(n)
		}
	case cst.KindBinaryExpr:
		a.operandForm(cst.BinaryExpr{Node: n})
	case cst.KindReturnSpec:
		// return id of T: a function that knows its return subtype is
		// VHDL-2019 (LRM-2019 4.2.1), and never an interface function.
		if !n.Token(token.KwOf, 0).IsZero() {
			switch {
			case a.std < token.Std2019:
				a.semaError(n.Span(), "a named return subtype (return "+
					"id of T) needs VHDL-2019")
			case n.Parent().Parent().Kind() ==
				cst.KindInterfaceSubprogramDecl:
				a.semaError(n.Span(), "an interface function cannot "+
					"name its return subtype")
			}
		}
	case cst.KindConfigurationSpec:
		if end := n.Child[cst.EndClause](0); a.std < token.Std2008 &&
			!end.IsZero() {
			// LRM-2008 7.3.1 adds the optional end for.
			a.semaError(end.Span(), "end for of a configuration "+
				"specification needs VHDL-2008")
		}
	case cst.KindInertialActual:
		if !inPortMap(n) {
			a.semaError(n.Span(), "inertial can only mark an actual of "+
				"a port map")
		}
	}
}

func (a *analyzer) caseForm(n cst.Node) {
	alts := 0
	for c := range n.Nodes() {
		switch c.Kind() {
		case cst.KindCaseAlternative, cst.KindCaseGenerateAlternative:
			alts++
		}
	}
	if alts == 0 {
		a.semaError(n.Span(), "a case statement needs at least one "+
			"alternative")
	}
	if n.Kind() != cst.KindCaseStmt {
		return
	}
	open := !n.Token(token.Question, 0).IsZero()
	end := n.Child[cst.EndClause](0)
	if end.IsZero() {
		return
	}
	if closed := !end.Token(token.Question, 0).IsZero(); closed != open {
		if open {
			a.semaError(end.Span(), "a matching case statement ends "+
				"with end case ?")
		} else {
			a.semaError(end.Span(), "end case ? ends only a matching "+
				"case statement")
		}
	}
}

// entityStatements reports the statements an entity statement part
// cannot hold (LRM 3.2.4).
func (a *analyzer) entityStatements(n cst.Node) {
	sp := n.Child[cst.StatementPart](0)
	if sp.IsZero() {
		return
	}
	for st := range sp.Nodes() {
		switch st.Kind() {
		case cst.KindConcurrentSimpleSignalAssign,
			cst.KindConcurrentConditionalSignalAssign,
			cst.KindConcurrentSelectedSignalAssign,
			cst.KindComponentInstantiationStmt, cst.KindBlockStmt,
			cst.KindForGenerateStmt, cst.KindIfGenerateStmt,
			cst.KindCaseGenerateStmt:
			a.semaError(st.Span(), "an entity statement must be a "+
				"concurrent assertion, a procedure call or a process")
		}
	}
}

func (a *analyzer) operandForm(b cst.BinaryExpr) {
	if a.std >= token.Std2019 {
		return
	}
	rhs := b.Rhs().Node
	if rhs.Kind() != cst.KindUnaryExpr {
		return
	}
	if b.Op().Kind() == token.DoubleStar {
		a.semaError(rhs.Span(), "the right operand of ** is a "+
			"primary: parenthesize "+strings.TrimSpace(rhs.Text()))
	}
}

// inPortMap reports whether the actual n is in a port map aspect.
func inPortMap(n cst.Node) bool {
	for p := n.Parent(); !p.IsZero(); p = p.Parent() {
		switch p.Kind() {
		case cst.KindPortMapAspect:
			return true
		case cst.KindGenericMapAspect, cst.KindCallOrIndexName,
			cst.KindProcedureCallStmt,
			cst.KindConcurrentProcedureCallStmt:
			return false
		}
	}
	return false
}

// integerExponent reports an integer literal with a negative exponent
// (LRM 15.5.2, 15.5.3).
func integerExponent(t cst.Token) bool {
	s := lowerASCII(t.Text())
	if i := strings.LastIndexByte(s, '#'); i >= 0 {
		s = s[i:]
	}
	return strings.Contains(s, "e-")
}

// returnForm reports a return statement st whose value does not fit
// its subprogram fn: a procedure returns no value, a function always
// one (LRM 10.13).
func (a *analyzer) returnForm(st cst.Node, fn *Decl, value bool) {
	if a.quiet || fn == nil {
		return
	}
	kw := st.Token(token.KwReturn, 0).Span()
	switch {
	case fn.Kind == DeclProcedure && value:
		a.semaError(kw, "a return statement in a procedure has no "+
			"value")
	case fn.Kind == DeclFunction && !value:
		a.semaError(kw, "a return statement in a function needs a "+
			"value")
	}
}

// condReturnForm marks the unaffected branches of the VHDL-2019
// conditional return st (brs, one per branch of s) and reports the
// forms LRM 10.13 does not allow in its subprogram: a procedure has
// only the plain `return when condition`, a function's every branch
// has a value or unaffected.
func (a *analyzer) condReturnForm(st cst.Node,
	s cst.ConditionalReturnStmt, brs []SeqBranch,
) {
	i, value, bare := 0, false, false
	for br := range s.Values().Branches() {
		v := branchValue(br) // stmts.go
		if v.Kind() == cst.KindUnaffectedExpr && i < len(brs) {
			brs[i].Unaffected = true
		}
		if v.IsZero() {
			bare = true
		} else {
			value = true
		}
		i++
	}
	fn := a.fn
	if a.quiet || fn == nil {
		return
	}
	kw := st.Token(token.KwReturn, 0).Span()
	switch {
	case fn.Kind == DeclFunction && bare:
		a.returnForm(st, fn, false)
	case fn.Kind == DeclProcedure && value:
		a.returnForm(st, fn, true)
	case fn.Kind == DeclProcedure && len(brs) > 1:
		// LRM 10.13: plain_return_statement ::= return [when
		// condition];
		a.semaError(kw, "a conditional return in a procedure has one "+
			"condition and no else")
	}
}

// rangeForm reports the discrete ranges of c, a parameter
// specification or an index constraint, that are neither a range nor a
// subtype indication nor a name (of a type, subtype or range
// attribute: the name's resolution checks which) but an expression
// (LRM 5.3.2.1, 10.10, 11.8).
func (a *analyzer) rangeForm(c cst.Node) {
	if c.Kind() == cst.KindParameterSpecification {
		a.discreteRangeForm(childAfter(c, token.KwIn))
		return
	}
	in := false
	for e := range c.Elements() {
		switch {
		case e.IsToken():
			switch e.Token.Kind() {
			case token.LParen:
				in = true
			case token.RParen:
				in = false
			}
		case in:
			a.discreteRangeForm(e.Node)
		}
	}
}

func (a *analyzer) discreteRangeForm(n cst.Node) {
	switch n.Kind() {
	case cst.KindRangeExpr, cst.KindSubtypeIndication, cst.KindSimpleName,
		cst.KindSelectedName, cst.KindAttributeName,
		cst.KindCallOrIndexName, cst.KindSliceName, cst.KindOpenExpr,
		cst.KindErrorNode, 0:
		return
	}
	if !a.quiet {
		a.semaError(n.Span(), strings.TrimSpace(n.Text())+" is not a "+
			"discrete range")
	}
}

// packageBodyAttr reports an attribute declaration or specification in
// a package body before VHDL-2008, whose package body declarative items
// do not include them (LRM-2002 2.6).
func (a *analyzer) packageBodyAttr(n cst.Node) {
	if a.quiet || a.std >= token.Std2008 || a.cur == nil ||
		a.cur.Kind != RegionPackageBody || a.u.File.Bundled {
		return
	}
	a.semaError(n.Span(), "before VHDL-2008 a package body cannot hold "+
		"attribute declarations or specifications")
}

// deferredUse reports a deferred constant of the package being
// declared read in an object's initial value there: in its package
// declaration a deferred constant appears only in default expressions
// of interface declarations (LRM 6.4.2.2).
func (a *analyzer) deferredUse(e *Expr, depth int) {
	if a.quiet || e == nil || depth > 64 || a.cur == nil ||
		a.cur.Kind != RegionPackage {
		return
	}
	if e.Kind == ExprName && e.Decl != nil {
		if d := e.Decl.Denotes(); d.Kind == DeclConstant &&
			d.Value == nil && d.Parent == a.cur {
			a.semaError(e.Node.Span(), "the deferred constant "+d.Text+
				" cannot be read before its full declaration")
			return
		}
	}
	a.deferredUse(e.Prefix, depth+1)
	for _, x := range e.Args {
		a.deferredUse(x, depth+1)
	}
	for _, as := range e.Assocs {
		a.deferredUse(as.Value, depth+1)
	}
}

// nestedBlockSpec reports the block configuration of a component
// configuration whose block specification is not the architecture its
// entity aspect names (LRM 3.4.3, 3.4.2: it configures that
// architecture).
func (s *session) nestedBlockSpec(u *Unit, cc cst.ComponentConfiguration) {
	nb := cc.Block()
	if nb.IsZero() {
		return
	}
	ea := cc.Binding().Entity()
	if ea.IsZero() {
		return
	}
	arch := childAfter(ea.Node, token.LParen)
	var archTok cst.Token
	for e := range ea.Node.Elements() {
		if e.IsToken() && e.Token.Kind() == token.Identifier &&
			arch.IsZero() {
			archTok = e.Token // entity lib.e(arch): a direct token
		}
	}
	if !arch.IsZero() {
		archTok = arch.FirstToken()
	}
	if archTok.IsZero() {
		return
	}
	spec := nb.Spec().Node
	tok := spec.FirstToken()
	if spec.Kind() != cst.KindSimpleName || TokenKey(tok) == "" ||
		TokenKey(tok) == TokenKey(archTok) {
		return
	}
	s.note(u, at(u.File, tok.Span(),
		diag.New(diag.SemaError, "the block specification "+tok.Text()+
			" is not the architecture "+archTok.Text()+" the entity "+
			"aspect binds")))
}
