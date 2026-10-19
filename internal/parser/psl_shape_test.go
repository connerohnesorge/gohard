package parser

import (
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
)

// pslExprKinds are the Expr group kinds pslShape collapses to "E"
// (SubtypeIndication and the PSL kinds of the group, PSLUnion and
// PSLValueSet, are kept).
var pslExprKinds = slices.DeleteFunc(cst.GroupKinds("Expr"),
	func(k cst.Kind) bool {
		return k == cst.KindSubtypeIndication ||
			k == cst.KindPSLUnion || k == cst.KindPSLValueSet
	})

// pslShape renders the node kinds under n as an S-expression. Expr
// nodes are "E": their inner shape belongs to the expression builder.
func pslShape(n cst.Node) string {
	var b strings.Builder
	var walk func(cst.Node)
	walk = func(n cst.Node) {
		k := n.Kind()
		if slices.Contains(pslExprKinds, k) && k != cst.KindErrorNode {
			b.WriteString("E")
			return
		}
		b.WriteString(k.String())
		first := true
		for c := range n.Nodes() {
			if first {
				b.WriteByte('(')
				first = false
			} else {
				b.WriteByte(' ')
			}
			walk(c)
		}
		if !first {
			b.WriteByte(')')
		}
	}
	walk(n)
	return b.String()
}
