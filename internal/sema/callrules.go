package sema

// callrules.go holds the association rules of subprogram calls that
// overload resolution leaves to a later check (#34): an actual of open
// needs a formal of mode in with a default expression (LRM 4.2.2.1,
// 6.5.6.2), a formal is associated at most once (LRM 6.5.7.1), a
// variable parameter's actual is a variable, not a file (LRM 4.2.2.2),
// and a floating point literal divided by a literal zero has no value
// in REAL, an error of the locally static expression (LRM 5.2.5, 9.4.1).
// An integer division by zero is left to run time, as GHDL and NVC do.

import (
	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// exprRules checks the calls in the typed expression e.
func (a *analyzer) exprRules(e *Expr) {
	if a.checksOff() {
		return
	}
	a.exprRulesAt(e, 0)
}

func (a *analyzer) exprRulesAt(e *Expr, depth int) {
	if e == nil || depth > 64 {
		return
	}
	if e.Kind == ExprCall {
		a.callRules(e)
	}
	if e.Kind != ExprAttr {
		a.exprRulesAt(e.Prefix, depth+1)
	}
	for _, x := range e.Args {
		a.exprRulesAt(x, depth+1)
	}
	for _, as := range e.Assocs {
		a.exprRulesAt(as.Value, depth+1)
	}
}

// callRules checks one call: its association list and its actuals.
func (a *analyzer) callRules(e *Expr) {
	d := e.Decl.Denotes()
	if d == nil || e.illTyped {
		return
	}
	if d.Implicit && d.Name == `"/"` && len(e.Args) == 2 &&
		realLiteral(e.Args[0]) && realLiteral(e.Args[1]) &&
		zeroLiteral(e.Args[1]) {
		a.semaError(e.Node.Span(), "the value of "+a.text(e.Node)+
			" is not a floating point value: division by zero")
		return
	}
	if d.Implicit || !d.Kind.IsSubprogram() {
		return
	}
	a.duplicateFormals(e.Node, d)
	if len(e.Formals) != len(e.Args) {
		return
	}
	for i, f := range e.Formals {
		x := e.Args[i]
		if f == nil || x == nil {
			continue
		}
		switch {
		case x.Kind == ExprOpen && (f.Class == ClassSignal ||
			f.Class == ClassVariable && f.Mode != ModeIn ||
			f.Value == nil):
			a.semaError(x.Node.Span(), "parameter "+f.Text+" of "+
				d.Text+" has no default expression, so its actual "+
				"cannot be open")
		case f.Class == ClassVariable && x.Kind == ExprName &&
			x.Decl != nil && x.Decl.Denotes().Kind == DeclFile:
			a.semaError(x.Node.Span(), "the actual of variable "+
				"parameter "+f.Text+" cannot be the file "+
				x.Decl.Denotes().Text)
		}
	}
}

// duplicateFormals reports a formal named twice, as a whole, in the
// association list of the call n of subprogram d (LRM 6.5.7.1).
func (a *analyzer) duplicateFormals(n cst.Node, d *Decl) {
	c, ok := cst.As[cst.CallOrIndexName](n)
	if !ok {
		return
	}
	seen := map[Key]bool{}
	for item := range c.Args().Items() {
		fn := item.Formal()
		if fn.IsZero() || fn.Kind() != cst.KindSimpleName {
			continue
		}
		k := TokenKey(fn.FirstToken())
		if seen[k] {
			a.semaError(item.Span(), "parameter "+fn.FirstToken().Text()+
				" of "+d.Text+" already has an actual")
			return
		}
		seen[k] = true
	}
}

// realLiteral reports whether e is a floating point literal.
func realLiteral(e *Expr) bool {
	return e != nil && e.Kind == ExprLiteral &&
		e.Lit.Kind() == token.RealLiteral
}

// zeroLiteral reports whether the real literal e is zero.
func zeroLiteral(e *Expr) bool {
	if !realLiteral(e) {
		return false
	}
	switch e.Lit.Kind() {
	case token.RealLiteral:
		for _, r := range e.Lit.Text() {
			switch r {
			case '0', '.', '_':
			case 'e', 'E':
				return true // 0.0e5 is still zero
			default:
				return false
			}
		}
		return true
	}
	return false
}

// unknownFormals reports, at the formal, a named association whose
// formal is no parameter of the only subprogram the name denotes (LRM
// 6.5.7.1): the overload error names the call, this one the formal.
func (a *analyzer) unknownFormals(ds []*Decl, acts []actual) {
	if a.quiet || len(ds) != 1 {
		return
	}
	x := ds[0].Denotes()
	if !x.Kind.IsSubprogram() {
		return
	}
	for _, ac := range acts {
		if ac.formal == "" || ac.ftok.IsZero() {
			continue
		}
		found := false
		for _, p := range x.Params {
			if p.Name == ac.formal {
				found = true
				break
			}
		}
		if !found {
			a.semaError(ac.ftok.Span(), x.Text+" has no parameter "+
				"named "+ac.ftok.Text())
		}
	}
}
