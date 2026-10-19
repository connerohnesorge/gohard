package sema

import (
	"github.com/connerohnesorge/gohard/internal/cst"
)

// ExprKind is the kind of a typed expression.
type ExprKind uint8

// Typed expression kinds.
const (
	// ExprUnknown is an expression sema could not type; it never
	// causes a second diagnostic.
	ExprUnknown ExprKind = iota
	// ExprLiteral: Lit is an integer, real, character, string or bit
	// string literal, or null.
	ExprLiteral
	ExprPhysical   // Lit (value) and Decl (the unit)
	ExprName       // Decl: an object, enumeration literal, unit, alias
	ExprCall       // Decl: the function or operator; Args
	ExprIndex      // Prefix(Args)
	ExprSlice      // Prefix(Range)
	ExprSelect     // Prefix.Decl (a record element)
	ExprDeref      // Prefix.all
	ExprAttr       // Prefix'Attr(Args); Decl for user attributes
	ExprAggregate  // Assocs
	ExprQualified  // Type'(Args[0])
	ExprConversion // Type(Args[0])
	ExprAllocator  // new Args[0] or new subtype
	ExprRange      // Range
	ExprTypeMark   // a name denoting a type or subtype (Type)
	ExprOpen       // open
	// ExprCond is a VHDL-2019 conditional expression: Args are value,
	// condition pairs, then the else value.
	ExprCond
	ExprOthers // the others choice
)

// Expr is a typed, resolved expression: the typed-AST view of the
// syntax tree that the static evaluator and the simulator (#35)
// consume. It is internal and not frozen: later stages add fields.
type Expr struct {
	Kind ExprKind
	Node cst.Node
	// Type is the expression's subtype (UnknownType when not known).
	Type *Type
	// Decl is the declaration a name denotes, the subprogram or
	// operator a call invokes, the element a selection picks, the unit
	// of a physical literal, or a user-defined attribute.
	Decl   *Decl
	Prefix *Expr
	// Args are call actuals (in formal order once overloads are
	// resolved), index expressions, a qualified or converted operand.
	Args []*Expr
	// Formals are the formal parameter of each of Args for calls.
	Formals []*Decl
	Assocs  []Assoc
	Range   *RangeCons
	// Attr is the attribute designator of ExprAttr.
	Attr Key
	// Lit is the literal token.
	Lit cst.Token
	// Overload is the editor-mode overload result of a call or
	// operator: the chosen overload and every candidate with the
	// reason it did not fit.
	Overload *Overload

	pend *pending // set while the context has not decided the type
	// illTyped is set once a type error of this expression has been
	// reported: a caller checking it again does not repeat it, and the
	// static evaluator does not evaluate it (no cascade).
	illTyped bool
}

// Assoc is one element association of an aggregate.
type Assoc struct {
	Choices []*Expr // empty for a positional association
	Value   *Expr
	Others  bool
	// Slice marks a VHDL-2008 association whose value is a slice of
	// the aggregate (of the aggregate's type), not one element.
	Slice bool
}

// Overload is the editor-mode result of overload resolution (#34): one
// result feeds diagnostics, hover and completion.
type Overload struct {
	// Name is the designator as written ("+", "to_integer").
	Name string
	// Chosen is the overload picked, nil when none fits or more than
	// one does.
	Chosen *Decl
	// Candidates are every visible overload, each with whether it
	// fits and, if not, why: the fitting ones first, then the nearest
	// misses, ties in canonical order (file, position, signature).
	Candidates []Candidate
}

// Ambiguous reports whether more than one candidate fits.
func (o *Overload) Ambiguous() bool {
	if o == nil {
		return false
	}
	n := 0
	for _, c := range o.Candidates {
		if c.Fits {
			n++
		}
	}
	return n > 1 && o.Chosen == nil
}

// Candidate is one overload considered for a call.
type Candidate struct {
	Decl *Decl
	Fits bool
	// Reason says why the candidate does not fit ("" when it fits),
	// for example "argument 2 is INTEGER, not UNSIGNED".
	Reason string
	// Near is how close a candidate that does not fit came: the
	// number of actuals that fit before the first that did not, one
	// more when only the result type is wrong, 0 for a wrong kind or
	// number of arguments.
	Near int
}

func unknownExpr(n cst.Node) *Expr {
	return &Expr{Kind: ExprUnknown, Node: n, Type: UnknownType}
}
