package sema

import "github.com/connerohnesorge/gohard/internal/cst"

// SeqKind is the kind of a sequential statement.
type SeqKind uint8

// Sequential statement kinds.
const (
	// SeqOther is a statement the typed tree does not model yet
	// (PSL, error nodes); evaluating it is not static.
	SeqOther SeqKind = iota
	SeqNull
	// SeqVarAssign: Target := Value.
	SeqVarAssign
	// SeqCondVarAssign: Target := Branches[i].Value when
	// Branches[i].Cond else ... (Cond nil for the last else).
	SeqCondVarAssign
	// SeqSelVarAssign: with Selector select Target := Alts[i].Value
	// when Alts[i].Choices.
	SeqSelVarAssign
	// SeqSignal is a signal assignment, force or release of Target
	// (never static).
	SeqSignal
	SeqWait
	// SeqAssert: assert Cond report Report severity Severity; SeqReport
	// has no Cond.
	SeqAssert
	SeqReport
	// SeqCall is a procedure call statement (Call, an ExprCall).
	SeqCall
	// SeqIf: Branches (if and elsif conditions with their bodies),
	// then Else.
	SeqIf
	// SeqCase: case Selector is Alts.
	SeqCase
	// SeqLoop: for Param in Range, while Cond, or a plain loop; Body.
	SeqLoop
	// SeqNext and SeqExit: Loop is the loop's label (nil: the
	// innermost loop), Cond the optional condition.
	SeqNext
	SeqExit
	// SeqReturn: return Value (nil in a procedure).
	SeqReturn
	// SeqCondReturn: return Branches[i].Value when Branches[i].Cond.
	SeqCondReturn
	// SeqBlock is a VHDL-2019 sequential block: Region, Body.
	SeqBlock
)

// SeqStmt is one sequential statement of a subprogram or process body:
// the typed tree the static evaluator interprets (pure function calls)
// and the simulator executes (#35). Like Expr it is internal and not
// frozen.
type SeqStmt struct {
	Kind  SeqKind
	Node  cst.Node
	Label *Decl

	Target   *Expr
	Value    *Expr
	Cond     *Expr
	Selector *Expr
	Call     *Expr
	Report   *Expr
	Severity *Expr

	Branches []SeqBranch
	Else     []*SeqStmt
	Alts     []SeqAlt
	// Matching marks case? and select? (VHDL-2008).
	Matching bool

	Param  *Decl
	Range  *RangeCons
	Loop   *Decl
	Body   []*SeqStmt
	Region *Region
}

// SeqBranch is one branch of an if statement (Cond, Body) or of a
// conditional assignment or return (Value, Cond; Cond nil for the
// final else). Unaffected marks a branch of a VHDL-2019 conditional
// return whose value is written `unaffected` (LRM 10.13): no return
// happens, which tells it apart from the plain `return when c` of a
// procedure (Value nil, Unaffected false).
type SeqBranch struct {
	Cond       *Expr
	Value      *Expr
	Body       []*SeqStmt
	Unaffected bool
}

// SeqAlt is one alternative of a case statement (Body) or of a
// selected assignment (Value).
type SeqAlt struct {
	Choices []*Expr
	Others  bool
	Value   *Expr
	Body    []*SeqStmt
}
