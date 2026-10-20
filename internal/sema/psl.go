package sema

import "github.com/connerohnesorge/gohard/internal/cst"

// pslExpr analyzes n, a VHDL expression inside PSL code (a boolean, a
// clock expression), and records its typed tree for the simulator's
// PSL runner (#35).
func (a *analyzer) pslExpr(n cst.Node) *Expr {
	e := a.expr(n, nil)
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
