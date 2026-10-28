package sema

// purity.go holds the rules on what a function may do through the
// procedures it calls (#34): no procedure with a wait statement has a
// function as its parent (LRM 10.2), a pure function declares no file
// (LRM 4.1), and the procedures a pure function calls reference no
// signal, variable or file declared outside them (LRM 4.1). A callee is
// followed when its body is in the same declarative region as its
// declaration, analyzed before the call; other callees are not judged.

import (
	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// procSummary is what a procedure body does, its callees included.
type procSummary struct {
	wait bool
	// outer is an object declared outside the procedure that it
	// references, or an impure function it calls.
	outer *Decl
}

// bodyOf returns the analyzed body of the subprogram d: d itself when
// it has statements, else the body that names d as its declaration in
// d's region or in the unit u (a package body).
func bodyOf(d *Decl, u *Unit) *Decl {
	if d == nil {
		return nil
	}
	if d.Seq != nil || d.Parent == nil {
		return d
	}
	for _, x := range d.Parent.Decls {
		if x.Spec == d && x.Seq != nil {
			return x
		}
	}
	for _, x := range u.Decls {
		if x.Spec == d && x.Seq != nil {
			return x
		}
	}
	return nil
}

// within reports whether d is declared in region r or a region nested
// in it.
func within(d *Decl, r *Region) bool {
	for p := d.Parent; p != nil; p = p.Parent {
		if p == r {
			return true
		}
	}
	return false
}

// summarize walks the body of procedure p (depth bounds recursion).
func summarize(p *Decl, u *Unit, depth int, seen map[*Decl]bool,
) procSummary {
	var s procSummary
	if p == nil || p.Region == nil || depth > 16 || seen[p] {
		return s
	}
	seen[p] = true
	walkSeq(p.Seq, func(q *SeqStmt) {
		if q.Kind == SeqWait {
			s.wait = true
		}
		if q.Kind == SeqCall && q.Call != nil && q.Call.Decl != nil {
			c := q.Call.Decl.Denotes()
			if c.Kind == DeclProcedure && !c.Implicit {
				cs := summarize(bodyOf(c, u), u, depth+1, seen)
				s.wait = s.wait || cs.wait
				if s.outer == nil && cs.outer != nil &&
					!within(cs.outer, p.Region) {
					s.outer = cs.outer
				}
			}
		}
	}, func(e *Expr) {
		if s.outer != nil || e.Decl == nil {
			return
		}
		d := e.Decl.Denotes()
		if e.Kind == ExprCall && d.Kind == DeclFunction && !d.Pure &&
			!d.Implicit {
			s.outer = d
			return
		}
		if e.Kind != ExprName {
			return
		}
		switch d.Kind {
		case DeclSignal, DeclPort, DeclVariable, DeclSharedVariable,
			DeclFile:
			if !within(d, p.Region) {
				s.outer = d
			}
		}
	})
	return s
}

// walkSeq calls stmt for each statement of seq, nested ones included,
// and expr for each expression they hold and its operands. Attribute
// prefixes are not visited: they name an object without reading it,
// except those of VHDL-2019 'REFLECT and object'IMAGE, which read the
// object's value.
func walkSeq(seq []*SeqStmt, stmt func(*SeqStmt), expr func(*Expr)) {
	var ex func(e *Expr, depth int)
	ex = func(e *Expr, depth int) {
		if e == nil || depth > 64 {
			return
		}
		expr(e)
		if e.Kind != ExprAttr || readsPrefix(e) {
			ex(e.Prefix, depth+1)
		}
		for _, x := range e.Args {
			ex(x, depth+1)
		}
		for _, as := range e.Assocs {
			ex(as.Value, depth+1)
		}
	}
	var st func(qs []*SeqStmt, depth int)
	st = func(qs []*SeqStmt, depth int) {
		if depth > 64 {
			return
		}
		for _, q := range qs {
			if q == nil {
				continue
			}
			stmt(q)
			for _, e := range []*Expr{q.Target, q.Value, q.Cond,
				q.Selector, q.Call, q.Report, q.Severity} {
				ex(e, 0)
			}
			for _, b := range q.Branches {
				ex(b.Cond, 0)
				ex(b.Value, 0)
				st(b.Body, depth+1)
			}
			for _, al := range q.Alts {
				ex(al.Value, 0)
				st(al.Body, depth+1)
			}
			st(q.Else, depth+1)
			st(q.Body, depth+1)
		}
	}
	st(seq, 0)
}

// purityRules checks the calls of the function body fn, and that a
// pure one names no external signal or variable (LRM 4.1; lenient mode
// allows it, as GHDL's -frelaxed does).
func (a *analyzer) purityRules(fn *Decl) {
	if a.checksOff() || fn.Kind != DeclFunction {
		return
	}
	if fn.Pure && !a.s.cfg.Lenient {
		walkSeq(fn.Seq, func(*SeqStmt) {}, func(e *Expr) {
			if e.Kind != ExprName ||
				e.Node.Kind() != cst.KindExternalName {
				return
			}
			c := cst.ExternalName{Node: e.Node}.Class()
			if k := c.Kind(); k == token.KwSignal || k == token.KwVariable {
				a.semaError(e.Node.Span(), "pure function "+fn.Text+
					" cannot reference the external "+c.Text()+" "+
					a.text(e.Node))
			}
		})
	}
	walkSeq(fn.Seq, func(q *SeqStmt) {
		if q.Kind != SeqCall || q.Call == nil || q.Call.Decl == nil {
			return
		}
		c := q.Call.Decl.Denotes()
		if c.Kind != DeclProcedure || c.Implicit || within(c, fn.Region) {
			return
		}
		s := summarize(bodyOf(c, a.u), a.u, 0, map[*Decl]bool{})
		switch {
		case s.wait:
			a.semaError(q.Node.Span(), "function "+fn.Text+" cannot "+
				"call procedure "+c.Text+", which contains a wait "+
				"statement")
		case fn.Pure && s.outer != nil:
			a.semaError(q.Node.Span(), "pure function "+fn.Text+
				" cannot call procedure "+c.Text+", which "+impureUse(s.outer))
		}
	}, func(*Expr) {})
}

// pureFile reports a file declared in a pure function (LRM 4.1);
// lenient mode allows it, as GHDL's -frelaxed does.
func (a *analyzer) pureFile(n cst.Node) {
	if a.checksOff() || a.s.cfg.Lenient || a.fn == nil ||
		a.fn.Kind != DeclFunction || !a.fn.Pure {
		return
	}
	a.semaError(n.Span(), "pure function "+a.fn.Text+" cannot "+
		"declare a file")
}

// impureUse describes what makes a procedure impure.
func impureUse(d *Decl) string {
	if d.Kind == DeclFunction {
		return "calls the impure function " + d.Text
	}
	return "references " + d.Kind.String() + " " + d.Text
}
