package sema

import (
	"github.com/connerohnesorge/gohard/internal/cst"
)

// syntaxChecks walks a design unit once for the rules that need only
// the tree's shape, not names or types:
//
//   - others is the only choice of the last alternative of a case
//     statement, case generate or selected assignment, and of the
//     last element association of an aggregate (LRM 9.3.3.3, 10.9).
//   - an interface list has at least one element (LRM 6.5.1): `()` is
//     no empty parameter list.
//
// Inactive conditional-analysis branches are pruned on the way down
// (asking Inactive per node would be quadratic on deep trees).
func (a *analyzer) syntaxChecks(root cst.Node) {
	if a.quiet || a.u.File == nil || a.u.File.Bundled || root.IsZero() ||
		root.Inactive() {
		return
	}
	stack := []cst.Node{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.Kind() == cst.KindInactiveBranch {
			continue
		}
		a.othersLast(n)
		if n.Kind() == cst.KindInterfaceList && emptyList(n) {
			a.semaError(n.Span(), "an interface list cannot be empty")
		}
		for c := range n.Nodes() {
			stack = append(stack, c)
		}
	}
}

// othersLast checks the alternatives (or element associations) that
// are direct children of n, each with a Choices child.
func (a *analyzer) othersLast(n cst.Node) {
	var lists []cst.Node
	for c := range n.Nodes() {
		if ch, ok := choicesOf(c); ok {
			lists = append(lists, ch)
		}
	}
	for i, l := range lists {
		count := 0
		var others cst.Node
		for item := range l.Nodes() {
			count++
			if item.Kind() == cst.KindOthersChoice && others.IsZero() {
				others = item
			}
		}
		if others.IsZero() {
			continue
		}
		switch {
		case i != len(lists)-1:
			a.semaError(others.Span(), "others must be the choice of "+
				"the last alternative")
		case count > 1:
			a.semaError(others.Span(), "others must be the only choice "+
				"of its alternative")
		}
	}
}

// choicesOf returns the Choices child of an alternative or element
// association.
func choicesOf(n cst.Node) (cst.Node, bool) {
	for c := range n.Nodes() {
		if c.Kind() == cst.KindChoices {
			return c, true
		}
	}
	return cst.Node{}, false
}

// emptyList reports whether an interface list has no element (error
// nodes from recovery count as elements: the parser reported them).
func emptyList(n cst.Node) bool {
	for range n.Nodes() {
		return false
	}
	return true
}
