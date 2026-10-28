package sema

import (
	"strconv"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/parser"
	"github.com/connerohnesorge/gohard/v2/internal/span"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// File is one analyzed source file: a project file or a bundled std/ieee
// file.
type File struct {
	// Path is the project-relative slash path of a project file, or the
	// bundle path ("ieee/numeric_std.vhdl") of a bundled file.
	Path string
	// Bundled marks the read-only std/ieee files (#33).
	Bundled bool
	// Library is the library the file's units are analyzed into
	// (lower-case key).
	Library Key
	Text    []byte
	Parse   *parser.Result
	Lines   *span.Index
	// Units are the design units of the file in source order.
	Units []*Unit
	// PSL holds the typed VHDL expressions inside the file's PSL code
	// (booleans, clocks), by node span; see File.PSLExpr (psl.go).
	PSL map[cst.Range]*Expr
}

// Root returns the red root of the file's tree.
func (f *File) Root() cst.Node { return f.Parse.Tree() }

// UnitKind is the kind of a design unit.
type UnitKind uint8

// Design unit kinds.
const (
	UnitEntity UnitKind = iota + 1
	UnitArchitecture
	UnitPackage
	UnitPackageBody
	UnitPackageInst
	UnitConfiguration
	UnitContext
	UnitVunit // PSL vunit, vmode, vprop
)

var unitKindNames = [...]string{
	UnitEntity: "entity", UnitArchitecture: "architecture",
	UnitPackage: "package", UnitPackageBody: "package body",
	UnitPackageInst: "package", UnitConfiguration: "configuration",
	UnitContext: "context", UnitVunit: "vunit",
}

// String returns the VHDL word for the kind.
func (k UnitKind) String() string {
	if int(k) < len(unitKindNames) && unitKindNames[k] != "" {
		return unitKindNames[k]
	}
	return "UnitKind(" + strconv.Itoa(int(k)) + ")"
}

// Primary reports whether units of kind k are primary units (named in
// their library); architectures and package bodies are secondary.
func (k UnitKind) Primary() bool {
	return k != UnitArchitecture && k != UnitPackageBody
}

// Unit is one design unit.
type Unit struct {
	Kind    UnitKind
	Library Key
	// Name is the unit's own name; Of is the primary unit a secondary
	// unit belongs to (the entity of an architecture, the package of a
	// body) and the entity of a configuration.
	Name Key
	Of   Key
	File *File
	// Index is the unit's position in the canonical unit order of the
	// analysis (bundled units first, then project units by path and
	// source order).
	Index int
	// Node is the DesignUnit node; Lib is its library unit node.
	Node cst.Node
	Lib  cst.Node
	// Decl is the unit's own declaration.
	Decl *Decl
	// Context is the region of the context clause (library names and
	// use clauses); Region is the unit's declarative region, a child
	// of Context (or, for secondary units, of the primary unit's
	// region, after Context).
	Context *Region
	Region  *Region
	// Deps are the units this one depends on, sorted by Index.
	Deps []*Unit
	// Level is the dependency level: a unit is analyzed after every
	// unit of a lower level and may read only those.
	Level int
	// Decls are every declaration the unit makes, in creation order.
	Decls []*Decl
	// Refs are the name occurrences in the unit, sorted by position.
	Refs []Ref
	// Exprs are the typed top-level expressions of the unit in source
	// order (initial values, conditions, waveforms, ...).
	Exprs []*Expr
	// Body is the concurrent statement tree of an entity or
	// architecture.
	Body []*Stmt
	// Opaque is set for an encrypted unit or one using a construct
	// GoHard does not implement (unsupported.go); Unchecked for a unit
	// that depends on an opaque unit.
	Opaque    bool
	Unchecked bool
	// Modeled is set for an opaque unit allowed by allow_opaque that
	// has a simulation.models stand-in (#20).
	Modeled bool
	// Duplicate is set for a unit that repeats an earlier unit's name
	// in its library; it is analyzed but not visible to other units.
	Duplicate bool

	uses []unitRef // syntactic library-unit references
	// allLibs are the libraries of the unit's use clauses L.all.
	allLibs []Key
	diags   []diag.Diagnostic // the unit job's diagnostics
	// missing records lib.unit names that did not resolve, for
	// library/missing-unit (libs.go).
	missing []unitRef
	// late are architecture references resolved after every unit.
	late []lateRef
	// unitAttrs are the attributes the unit's attribute
	// specifications give the unit itself (`attribute A of e :
	// entity is ...`), for lib.e'A where A is not visible (names.go).
	unitAttrs []*Decl
	// othersAttrs are the attributes an attribute specification of
	// the unit gives with the entity name list others or all.
	othersAttrs []*Decl
}

// Label returns "lib.name" or, for secondary units, "lib.of(name)" /
// "lib.of body".
func (u *Unit) Label() string {
	switch u.Kind {
	case UnitArchitecture:
		return string(u.Library) + "." + string(u.Of) + "(" +
			string(u.Name) + ")"
	case UnitPackageBody:
		return string(u.Library) + "." + string(u.Name) + " body"
	}
	return string(u.Library) + "." + string(u.Name)
}

// DeclKind is the kind of a named entity.
type DeclKind uint8

// Declaration kinds.
const (
	DeclLibrary DeclKind = iota + 1
	DeclEntity
	DeclArchitecture
	DeclPackage
	DeclPackageBody
	DeclPackageInst
	DeclConfiguration
	DeclContext
	DeclVunit
	DeclType
	DeclSubtype
	DeclConstant
	DeclSignal
	DeclVariable
	DeclSharedVariable
	DeclFile
	DeclAlias
	DeclAttribute
	DeclComponent
	DeclFunction
	DeclProcedure
	DeclEnumLiteral
	DeclUnit    // a physical unit
	DeclElement // a record element
	DeclGeneric // a generic constant
	DeclGenericType
	DeclGenericSubprogram
	DeclGenericPackage
	DeclPort
	DeclParameter
	DeclLabel // a statement label
	DeclLoopParam
	DeclGenerateParam
	DeclGroup
	DeclGroupTemplate
	DeclModeView
	DeclPSL // PSL property, sequence or endpoint
)

var declKindNames = [...]string{
	DeclLibrary: "library", DeclEntity: "entity",
	DeclArchitecture: "architecture", DeclPackage: "package",
	DeclPackageBody: "package body", DeclPackageInst: "package",
	DeclConfiguration: "configuration", DeclContext: "context",
	DeclVunit: "vunit", DeclType: "type", DeclSubtype: "subtype",
	DeclConstant: "constant", DeclSignal: "signal",
	DeclVariable: "variable", DeclSharedVariable: "shared variable",
	DeclFile: "file", DeclAlias: "alias", DeclAttribute: "attribute",
	DeclComponent: "component", DeclFunction: "function",
	DeclProcedure: "procedure", DeclEnumLiteral: "enumeration literal",
	DeclUnit: "unit", DeclElement: "element", DeclGeneric: "generic",
	DeclGenericType:       "generic type",
	DeclGenericSubprogram: "generic subprogram",
	DeclGenericPackage:    "generic package", DeclPort: "port",
	DeclParameter: "parameter", DeclLabel: "label",
	DeclLoopParam:     "loop parameter",
	DeclGenerateParam: "generate parameter", DeclGroup: "group",
	DeclGroupTemplate: "group template", DeclModeView: "view",
	DeclPSL: "PSL declaration",
}

// String returns the VHDL word for the kind ("signal", "function").
func (k DeclKind) String() string {
	if int(k) < len(declKindNames) && declKindNames[k] != "" {
		return declKindNames[k]
	}
	return "DeclKind(" + strconv.Itoa(int(k)) + ")"
}

// IsObject reports whether declarations of kind k are objects
// (constants, signals, variables, files, and the interface objects).
func (k DeclKind) IsObject() bool {
	switch k {
	case DeclConstant, DeclSignal, DeclVariable, DeclSharedVariable,
		DeclFile, DeclGeneric, DeclPort, DeclParameter, DeclLoopParam,
		DeclGenerateParam:
		return true
	}
	return false
}

// IsSubprogram reports whether k is a function or procedure.
func (k DeclKind) IsSubprogram() bool {
	return k == DeclFunction || k == DeclProcedure
}

// IsTypeMark reports whether k declares a type or subtype.
func (k DeclKind) IsTypeMark() bool {
	return k == DeclType || k == DeclSubtype || k == DeclGenericType
}

// Mode is an interface object's mode.
type Mode uint8

// Modes. ModeNone is used where no mode applies.
const (
	ModeNone Mode = iota
	ModeIn
	ModeOut
	ModeInout
	ModeBuffer
	ModeLinkage
	ModeView // VHDL-2019 mode view
)

// ObjClass is the object class of an interface object.
type ObjClass uint8

// Object classes.
const (
	ClassNone ObjClass = iota
	ClassConstant
	ClassSignal
	ClassVariable
	ClassFile
)

// Decl is one named entity: a declaration in the source, an implicitly
// declared operation, or a library. Declarations are created by one
// unit's analysis job and never modified after the job ends, so later
// units read them without locks.
type Decl struct {
	Kind DeclKind
	// Name is the designator key; Text is its spelling at the
	// declaration.
	Name Key
	Text string
	// File and Span locate the defining occurrence of the name (the
	// selection range); Full covers the whole declaration. File is nil
	// for libraries and universal types, and implicit operations sit
	// at their type's name.
	File *File
	Span cst.Range
	Full cst.Range
	// Node is the declaration's node (zero for implicit ones).
	Node cst.Node
	// Unit is the design unit that declares it (nil for libraries).
	Unit *Unit
	// Parent is the region the declaration is in; Region is the region
	// it opens (units, subprograms, records, protected types, blocks,
	// processes, loops, generates, components).
	Parent *Region
	Region *Region
	// Type is the subtype of an object, element, alias or enumeration
	// literal; the type itself for a type or subtype declaration; the
	// return type of a function; the physical type of a unit.
	Type *Type
	// Params are a subprogram's parameters (DeclParameter) in order;
	// for a component, entity or block, its generics and ports are in
	// Region.
	Params []*Decl
	Mode   Mode
	Class  ObjClass
	// View is the VHDL-2019 mode view of a view declaration, of an
	// alias of a view, and of a port or parameter of mode view
	// (modeview.go).
	View *View
	// Value is the initial or default value of an object, a physical
	// unit's definition, an attribute specification's value.
	Value *Expr
	// Seq is the sequential statement tree of a subprogram body (its
	// declarations are in Region, after the parameters).
	Seq []*SeqStmt
	// Target is what an alias denotes; Spec links a subprogram body to
	// its declaration, a deferred constant's full declaration to the
	// deferred one, a full type to its incomplete declaration, a
	// package body to its package.
	Target *Decl
	Spec   *Decl
	// Implicit marks predefined operations (LRM 5, 9.2) and the
	// implicit library std, work and std.standard declarations.
	Implicit bool
	// Op is the evaluator operation of a predefined operator.
	Op uint8
	// Pure is false for impure functions.
	Pure bool
	// Pos is an enumeration literal's position, a record element's
	// index, a parameter's index.
	Pos int
	// First is the first standard that has the declaration: the
	// bundled tags of #33 for std/ieee, the LRM for implicit
	// operations, 0 (always) otherwise. A declaration newer than the
	// analyzed standard is not visible.
	First token.Standard
	// Generics are the generic associations, in written order, of a
	// subprogram instance: an implicit instance of a VHDL-2019 call
	// with a generic map (LRM 9.3.4) or a VHDL-2008 subprogram
	// instantiation declaration (LRM 4.4). Nil otherwise.
	Generics []Association
	// MethodObj is the object prefix of an alias of a protected
	// method (VHDL-2019 aliases in a protected declaration, LRM
	// 6.6.1): the object whose method the alias denotes. Nil
	// otherwise.
	MethodObj *Expr

	// sigAlias marks an alias with a signature: it denotes a
	// subprogram or literal, so it is overloadable even when its target
	// did not resolve.
	sigAlias bool
	// instTypes maps, for a package instance, the generic types of
	// the uninstantiated package to their actual subtypes
	// (typerules.go).
	instTypes map[*Type]*Type
	// paramRange is the discrete range of a loop parameter, whose
	// subtype it is (LRM 10.10; coverage.go).
	paramRange *RangeCons
	// attrs are the attributes an attribute specification of its own
	// unit gives the declaration (names.go).
	attrs []*Decl
}

// Overloadable reports whether d may be overloaded: subprograms,
// enumeration literals and aliases of them (LRM 4.5.1).
func (d *Decl) Overloadable() bool {
	switch d.Kind {
	case DeclFunction, DeclProcedure, DeclEnumLiteral,
		DeclGenericSubprogram:
		return true
	case DeclAlias:
		return d.sigAlias || (d.Target != nil && d.Target != d &&
			d.Target.Overloadable())
	}
	return false
}

// Denotes returns the named entity d denotes: an alias's target
// (transitively), else d.
func (d *Decl) Denotes() *Decl {
	for range 64 { // alias chains are short; never loop forever
		if d == nil || d.Kind != DeclAlias || d.Target == nil ||
			d.Target == d {
			return d
		}
		d = d.Target
	}
	return d
}

// IsFunction reports whether d denotes a function or an enumeration
// literal (a parameterless function, LRM 5.2.2.1).
func (d *Decl) IsFunction() bool {
	switch x := d.Denotes(); x.Kind {
	case DeclFunction, DeclEnumLiteral:
		return true
	case DeclGenericSubprogram:
		return x.Type != nil
	}
	return false
}

// Path returns the path of the declaring file, or "".
func (d *Decl) Path() string {
	if d.File == nil {
		return ""
	}
	return d.File.Path
}

// RegionKind is the kind of a declarative region.
type RegionKind uint8

// Region kinds.
const (
	RegionContext RegionKind = iota + 1
	RegionEntity
	RegionArchitecture
	RegionPackage
	RegionPackageBody
	RegionConfiguration
	RegionVunit
	RegionSubprogram
	RegionProcess
	RegionBlock
	RegionGenerate
	RegionLoop
	RegionRecord
	RegionProtected
	RegionProtectedBody
	RegionComponent
	RegionSeqBlock
	RegionStatement // other labelled statements (if, case, ...)
)

// Region is a declarative region (LRM 12.1). Declarations are added in
// order while the owning unit is analyzed; afterwards the region is
// immutable.
type Region struct {
	Kind RegionKind
	// Owner is the declaration that opens the region (nil for a
	// context region).
	Owner  *Decl
	Parent *Region
	File   *File
	// Span is the region's extent in File.
	Span cst.Range
	// Decls are the region's declarations in order.
	Decls []*Decl
	// Uses are the use clauses of the region in order.
	Uses []*Use
	// Children are the nested regions in source order.
	Children []*Region
	// Libs maps the library names of the context clause to their
	// library declarations (context regions only).
	Libs map[Key]*Decl
	// Includes are regions whose declarations are part of this one
	// (LRM 12.1): a protected type body sees its type's declarations,
	// a nested package body its package's, as directly visible.
	Includes []*Region
	// Unit is the design unit the region belongs to.
	Unit *Unit
	// Assigns are the signal and variable assignments of an entity,
	// architecture, block or generate body, its processes included, in
	// source order (assign.go): their static widths and indices are
	// checked with the unit and per instance.
	Assigns []*Assign

	names map[Key][]*Decl // lookup only; never iterated for output
}

// Lookup returns the declarations named k made in the region itself,
// in declaration order.
func (r *Region) Lookup(k Key) []*Decl {
	if r == nil {
		return nil
	}
	return r.names[k]
}

func (r *Region) add(d *Decl) {
	r.Decls = append(r.Decls, d)
	if r.names == nil {
		r.names = map[Key][]*Decl{}
	}
	r.names[d.Name] = append(r.names[d.Name], d)
}

// Use is one use clause item (or the implicit `use std.standard.all`).
type Use struct {
	// Node is the use clause name ("ieee.std_logic_1164.all"); zero for
	// implicit uses. Off is where the use takes effect.
	Node cst.Node
	Off  int
	// All marks `.all`: Region is the package region whose
	// declarations become potentially visible, or Library is the
	// library whose primary units do. Otherwise Decls are the named
	// declarations made potentially visible.
	All     bool
	Region  *Region
	Library *Library
	Decls   []*Decl
	// Broken marks a use clause whose name did not resolve (reported
	// where it is): it makes nothing visible, and an overload that
	// fails where it is in effect is not reported, since the missing
	// unit may have declared the overload.
	Broken bool
}

// Library is a design library: its primary units by name and its
// secondary units by primary unit.
type Library struct {
	Name Key
	Decl *Decl
	// Bundled marks std and ieee.
	Bundled bool
	// Folders are the gohard.yaml folders of a mapped library.
	Folders []string

	units     map[Key]*Unit   // primary units
	secondary map[Key][]*Unit // by primary unit name, sorted by Index
	order     []*Unit         // every unit in canonical order
	// replaced maps a primary unit to the earlier unit of its file it
	// replaced (library.go, replace).
	replaced map[*Unit]*Unit
}

// Unit returns the primary unit named k.
func (l *Library) Unit(k Key) (*Unit, bool) {
	if l == nil {
		return nil, false
	}
	u, ok := l.units[k]
	return u, ok
}

// UnitFrom returns the primary unit named k as unit from sees it: the
// visible unit, or, for a unit of the same design file before the unit
// that replaced it there, the earlier one (LRM 13.5: units of a file
// are analyzed in textual order).
func (l *Library) UnitFrom(k Key, from *Unit) (*Unit, bool) {
	u, ok := l.Unit(k)
	if !ok || from == nil || from.File == nil || from.Node.IsZero() {
		return u, ok
	}
	at := from.Node.Span().Start
	for u.File == from.File && !u.Node.IsZero() &&
		u.Node.Span().Start > at {
		old, ok := l.replaced[u]
		if !ok {
			break
		}
		u = old
	}
	return l.seenFrom(u, from) // units.go
}

// Secondary returns the architectures of entity k (or the body of
// package k) in canonical order.
func (l *Library) Secondary(k Key) []*Unit {
	if l == nil {
		return nil
	}
	return l.secondary[k]
}

// Units returns every unit of the library in canonical order.
func (l *Library) Units() []*Unit {
	if l == nil {
		return nil
	}
	return l.order
}
