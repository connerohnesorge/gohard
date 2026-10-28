package sema

import "github.com/connerohnesorge/gohard/v2/internal/cst"

// targetForm reports a target name that denotes no object an
// assignment can write (LRM 10.5.2.1, 10.6.2.1): an attribute (an
// implicit signal such as S'STABLE is driven by the simulator alone)
// or a function call.
func (a *analyzer) targetForm(e *Expr, n cst.Node) {
	if a.quiet || e == nil || e.illTyped {
		return
	}
	switch e.Kind {
	case ExprAttr:
		a.semaError(n.Span(), "attribute "+a.text(n)+" cannot be the "+
			"target of an assignment")
	case ExprCall:
		a.semaError(n.Span(), "function call "+a.text(n)+" cannot be "+
			"the target of an assignment")
	}
}

// outsideProcessSignal reports a signal written by a subprogram that
// is not declared within a process, as an assignment target or as the
// actual of an out or inout signal parameter, when the signal is not
// a formal parameter of the subprogram or of a subprogram around it
// (LRM 10.5.2.2, 4.2.2.2): such a subprogram has no driver of its own.
func (a *analyzer) outsideProcessSignal(e *Expr, n cst.Node) {
	if a.quiet || a.fn == nil || e == nil || e.illTyped {
		return
	}
	d := actualObject(e)
	if d == nil || objectClass(d) != "signal" {
		return
	}
	for r := a.cur; r != nil; r = r.Parent {
		if r.Kind == RegionProcess {
			return
		}
	}
	if d.Kind == DeclParameter {
		for r := a.cur; r != nil; r = r.Parent {
			if r == d.Parent {
				return
			}
		}
	}
	a.semaError(n.Span(), "signal "+d.Text+" is not a formal parameter "+
		"and the subprogram is not within a process, so it cannot "+
		"drive the signal")
}

// isSignalAssign reports whether st is a signal assignment statement
// that creates a driver: a force or release creates none (LRM
// 10.5.2.1), so a subprogram outside a process may force any signal.
func isSignalAssign(st cst.Node) bool {
	switch st.Kind() {
	case cst.KindSignalAssignStmt, cst.KindConditionalSignalAssignStmt,
		cst.KindSelectedSignalAssignStmt:
		return true
	}
	return false
}

// implicitOfParam reports 'STABLE, 'QUIET, 'TRANSACTION or 'DELAYED of
// a formal signal parameter read in a subprogram (LRM 4.2.2.2): those
// implicit signals exist for declared signals only.
func (a *analyzer) implicitOfParam(dtok cst.Token, dk string, pv *Expr) {
	if a.quiet || a.fn == nil || pv == nil {
		return
	}
	switch dk {
	case "stable", "quiet", "transaction", "delayed":
	default:
		return
	}
	if d := actualObject(pv); d != nil && d.Kind == DeclParameter &&
		objectClass(d) == "signal" {
		a.semaError(dtok.Span(), "'"+dtok.Text()+" of signal parameter "+
			d.Text+" cannot be read in a subprogram")
	}
}

// outerActuals analyzes a block header's generic or port map: the
// formals are the block's own generics or ports, the actuals names of
// the enclosing region, where the block's interface objects are not
// visible (LRM 12.2: the block's interface is associated from
// outside, as for a component instance; GHDL and NVC agree), so
// port map (clk => clk) maps the outer clk.
func (a *analyzer) outerActuals(l cst.AssociationList, k DeclKind,
) []Association {
	block := a.cur
	if l.IsZero() || block.Parent == nil {
		return a.assocMap(l, block, k)
	}
	a.cur = block.Parent
	defer func() { a.cur = block }()
	return a.assocMap(l, block, k)
}

// ambiguousDeref reports the target f.all of an assignment where f
// names more than one parameterless function returning an access
// value: a target gives no context type to choose by (LRM 12.5, 8.3).
func (a *analyzer) ambiguousDeref(n cst.Node) {
	if a.quiet || n.Kind() != cst.KindSelectedName {
		return
	}
	sel := cst.SelectedName{Node: n}
	if TokenKey(sel.Suffix()) != "all" ||
		sel.Prefix().Kind() != cst.KindSimpleName {
		return
	}
	r := a.lookup(TokenKey(cst.SimpleName{Node: sel.Prefix().Node}.Ident()))
	fits := 0
	for _, d := range r.Decls {
		x := d.Denotes()
		if x.Kind == DeclFunction && required(x) == 0 && x.Type != nil &&
			!x.Type.Unknown() && x.Type.Base().Class == TypeAccess {
			fits++
		}
	}
	if fits > 1 {
		a.semaError(n.Span(), a.text(n)+" is ambiguous: "+
			a.text(sel.Prefix().Node)+" names more than one "+
			"parameterless function returning an access value")
	}
}
