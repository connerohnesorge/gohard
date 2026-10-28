package sema

import "github.com/connerohnesorge/gohard/v2/internal/cst"

// StmtKind is the kind of a concurrent statement in a unit's body tree.
type StmtKind uint8

// Concurrent statement kinds.
const (
	StmtOther StmtKind = iota // assignments, assertions, calls, PSL
	StmtInstance
	StmtForGenerate
	StmtIfGenerate
	StmtCaseGenerate
	StmtBlock
	StmtProcess
)

// Stmt is one concurrent statement of an entity, architecture, block or
// generate body: the typed structure elaboration walks (internal/elab,
// #35). Sequential statements are not part of it yet; the simulator
// stage extends the tree.
type Stmt struct {
	Kind  StmtKind
	Node  cst.Node
	Label *Decl
	// Instances: Entity is the entity instantiated (directly, or
	// through a configuration, Config), Arch the architecture named
	// (empty: the default), Component the component of a component
	// instantiation.
	Entity    *Decl
	Arch      Key
	Component *Decl
	Config    *Decl
	Generics  []Association
	Ports     []Association
	// Generates: the for-generate parameter and range, the case
	// selector, and the alternatives (if and case generate).
	Param    *Decl
	Range    *RangeCons
	Selector *Expr
	Alts     []Alt
	// Body are the nested statements of a block or for-generate and
	// Region its declarative region.
	Body   []*Stmt
	Region *Region
	// Seq is the sequential statement tree of a process.
	Seq []*SeqStmt
}

// Alt is one alternative of an if or case generate.
type Alt struct {
	Label   *Decl
	Cond    *Expr   // if generate; nil for the else branch
	Choices []*Expr // case generate
	Others  bool
	Body    []*Stmt
	Region  *Region
}

// Association is one element of a generic or port map, in written
// order: the formal (nil when unknown), the actual (nil when open).
type Association struct {
	Formal *Decl
	Actual *Expr
	Node   cst.Node
	// Part is the formal designator of a subelement association
	// (LRM 6.5.7.1: port(3) => s, port(7 downto 4) => t, port.f => u):
	// an indexed, sliced or selected name whose innermost prefix is an
	// ExprName of Formal. Nil when the whole formal is associated.
	Part *Expr
}
