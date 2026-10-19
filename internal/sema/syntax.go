package sema

import (
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/token"
)

// Some generated cst accessors pick the first child of a group that
// also contains an earlier field's kind: ConstantDecl.Value returns the
// SubtypeIndication (SubtypeIndication is in the Expr group),
// SliceName.Range and AttributeName.Arg return the prefix,
// QualifiedExpr.Operand the type mark, WhenBranch.Condition the value,
// DisconnectionSpec.After a signal name. Sema reads those fields by
// position instead, through the helpers below.

// childAfter returns the first child node after the first direct
// token of kind k, or the zero Node.
func childAfter(n cst.Node, k token.Kind) cst.Node {
	seen := false
	for e := range n.Elements() {
		if e.IsToken() {
			if e.Token.Kind() == k {
				seen = true
			}
			continue
		}
		if seen {
			return e.Node
		}
	}
	return cst.Node{}
}

// reportSeverity returns the report and severity expressions of an
// assertion or report statement: the generated Report/Severity
// accessors are positional, so `assert c severity failure` would type
// the severity as the report string.
func reportSeverity(n cst.Node) (report, severity cst.Expr) {
	return cst.Expr{Node: childAfter(n, token.KwReport)},
		cst.Expr{Node: childAfter(n, token.KwSeverity)}
}

// initValue returns the expression after := of an object or interface
// declaration.
func initValue(n cst.Node) cst.Expr {
	return cst.Expr{Node: childAfter(n, token.VarAssign)}
}

// sliceRange returns the discrete range of prefix(range).
func sliceRange(s cst.SliceName) cst.Node {
	return childAfter(s.Node, token.LParen)
}

// attrArg returns the parenthesized argument of prefix'attr(arg).
func attrArg(a cst.AttributeName) cst.Node {
	return childAfter(a.Node, token.LParen)
}

// qualOperand returns the operand of T'(expr) or T'(aggregate).
func qualOperand(q cst.QualifiedExpr) cst.Node {
	return childAfter(q.Node, token.Tick)
}

// whenCondition returns the condition of a when branch.
func whenCondition(b cst.WhenBranch) cst.Node {
	return childAfter(b.Node, token.KwWhen)
}

// disconnectAfter returns the time of a disconnection specification.
func disconnectAfter(d cst.DisconnectionSpec) cst.Node {
	return childAfter(d.Node, token.KwAfter)
}

// fileOpen returns the open kind (or zero) and the logical name of a
// file open information.
func fileOpen(oi cst.FileOpenInformation) (kind, name cst.Node) {
	if oi.Node.Token(token.KwOpen, 0).IsZero() {
		return cst.Node{}, childAfter(oi.Node, token.KwIs)
	}
	return childAfter(oi.Node, token.KwOpen), childAfter(oi.Node,
		token.KwIs)
}

// groupParts returns the template and constituents of a group
// declaration.
func groupParts(g cst.GroupDecl) (template cst.Node, parts []cst.Node) {
	for n := range g.Constituents() {
		if template.IsZero() {
			template = n.Node
			continue
		}
		parts = append(parts, n.Node)
	}
	return template, parts
}

// text returns the source text of n without its leading and trailing
// trivia, for messages.
func (a *analyzer) text(n cst.Node) string {
	sp := n.Span()
	if sp.Start < 0 || sp.End > len(a.f.Text) || sp.Start > sp.End {
		return n.Text()
	}
	return string(a.f.Text[sp.Start:sp.End])
}
