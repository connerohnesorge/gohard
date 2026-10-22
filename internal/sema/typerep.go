package sema

import (
	"slices"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/eval"
)

// TypeClass is the class of a type (LRM 5).
type TypeClass uint8

// Type classes. TypeUnknown is the type of an expression sema could not
// type; it is compatible with everything so one error never cascades.
const (
	TypeUnknown TypeClass = iota
	TypeEnum
	TypeInteger
	TypeFloating
	TypePhysical
	TypeArray
	TypeRecord
	TypeAccess
	TypeFile
	TypeProtected
	// TypeIncomplete is an incomplete type declaration not completed
	// yet (LRM 5.4.2); the full declaration fills the same Type.
	TypeIncomplete
	// TypeGeneric is a VHDL-2008 generic type (interface type), whose
	// actual is only known per instance.
	TypeGeneric
	// TypeNull is the type of the literal null before context picks
	// an access type.
	TypeNull
)

var typeClassNames = [...]string{
	TypeUnknown: "unknown", TypeEnum: "enumeration",
	TypeInteger: "integer", TypeFloating: "floating",
	TypePhysical: "physical", TypeArray: "array", TypeRecord: "record",
	TypeAccess: "access", TypeFile: "file", TypeProtected: "protected",
	TypeIncomplete: "incomplete", TypeGeneric: "generic",
	TypeNull: "null",
}

// String returns the class name.
func (c TypeClass) String() string {
	if int(c) < len(typeClassNames) {
		return typeClassNames[c]
	}
	return "unknown"
}

// Type is a type or subtype. A base type has base == nil; a subtype
// points at its base type and adds a constraint and/or a resolution.
// Constraints are typed expressions, evaluated by the static evaluator
// (static.go) per unit or per instance when they depend on generics.
type Type struct {
	Class TypeClass
	// Decl names the type or subtype; nil for anonymous ones.
	Decl *Decl
	base *Type
	// Universal marks universal_integer and universal_real.
	Universal bool

	// Subtype part.
	Resolution *Resolution
	// Range is a scalar subtype's range constraint.
	Range *RangeCons
	// IndexCons are an array subtype's index constraints, one per
	// dimension (an entry may be nil for an open dimension, 2008);
	// nil for an unconstrained array.
	IndexCons []*RangeCons
	// ElemSub refines the element subtype of an array subtype (2008
	// element constraints); nil means the base type's element.
	ElemSub *Type
	// FieldSubs refine the element subtypes of a record subtype (2008
	// record constraints), by element position; nil, or a nil entry,
	// means the element's declared subtype.
	FieldSubs []*Type
	// dynamic marks the 'SUBTYPE of an unconstrained interface object:
	// constrained, by its actual, only at run time.
	dynamic bool

	// Base type part.
	Literals []*Decl // enumeration literals in position order
	Units    []*Decl // physical units, primary first
	Index    []*Type // array index subtypes
	Elem     *Type   // array element subtype
	Fields   []*Decl // record elements
	// Designated is an access type's designated subtype; Content a
	// file type's value type.
	Designated *Type
	Content    *Type
	// Members is a protected type's declaration region.
	Members *Region
	// Inst is set on a protected type instantiation (VHDL-2019,
	// LRM 5.6.4): the uninstantiated protected type and the generic
	// map. Nil for any other type.
	Inst *TypeInst
	// Ops are the implicitly declared operations of a base type
	// (predef.go), which a VHDL-2008 `use p.T` makes visible with T.
	Ops []*Decl

	// Eval is the evaluated layout when it is known without an
	// instance (std.standard types, static user types); see static.go.
	Eval *eval.Type
}

// TypeInst is the instantiation part of a protected type
// instantiation: Of is the uninstantiated protected type's
// declaration and Map the generic associations in written order.
type TypeInst struct {
	Of  *Decl
	Map []Association
}

// RangeCons is a range constraint or a discrete range: Left Dir Right,
// or a range attribute name (X'RANGE) or type mark in Attr.
type RangeCons struct {
	Node        cst.Node
	Left, Right *Expr
	Dir         eval.Dir
	Attr        *Expr
	// Type is the type of the range's bounds.
	Type *Type
}

// Resolution is a resolution indication: a resolution function, or a
// VHDL-2008 element resolution of an array (Elem) or record (Fields).
type Resolution struct {
	Node   cst.Node
	Func   *Decl
	Elem   *Resolution
	Fields []*Resolution
}

// Base returns the base type (t itself for a base type). It is safe on
// nil.
func (t *Type) Base() *Type {
	for range 64 {
		if t == nil || t.base == nil {
			return t
		}
		t = t.base
	}
	return t
}

// IsSubtype reports whether t is a subtype of another type.
func (t *Type) IsSubtype() bool { return t != nil && t.base != nil }

// NewSubtype returns an anonymous subtype of t (to be constrained by
// the caller).
func NewSubtype(t *Type) *Type {
	if t == nil {
		return nil
	}
	return &Type{Class: t.Base().Class, base: t, Eval: nil}
}

// Unknown reports whether t is not known (nil or TypeUnknown).
func (t *Type) Unknown() bool {
	return t == nil || t.Base().Class == TypeUnknown
}

// IsScalar reports whether t is an enumeration, integer, floating or
// physical type.
func (t *Type) IsScalar() bool {
	if t == nil {
		return false
	}
	switch t.Base().Class {
	case TypeEnum, TypeInteger, TypeFloating, TypePhysical:
		return true
	}
	return false
}

// IsDiscrete reports whether t is an enumeration or integer type.
func (t *Type) IsDiscrete() bool {
	if t == nil {
		return false
	}
	c := t.Base().Class
	return c == TypeEnum || c == TypeInteger
}

// IsNumeric reports whether t is an integer, floating or physical type.
func (t *Type) IsNumeric() bool {
	if t == nil {
		return false
	}
	c := t.Base().Class
	return c == TypeInteger || c == TypeFloating || c == TypePhysical
}

// ElemType returns the element subtype of an array (subtype), or nil.
func (t *Type) ElemType() *Type {
	for s := t; s != nil; s = s.base {
		if s.ElemSub != nil {
			return s.ElemSub
		}
		if s.base == nil {
			return s.Elem
		}
	}
	return nil
}

// runTime returns a subtype of t whose constraint is known only at
// run time.
func runTime(t *Type) *Type {
	if t.Unknown() {
		return t
	}
	st := NewSubtype(t)
	st.dynamic = true
	return st
}

// inferred is the subtype of an object declared with the not fully
// constrained subtype t and the initial value v (VHDL-2019 inferred
// constraints, LRM 6.4.2.3, 6.4.2.4): t refined by the constraints of
// v's subtype when that subtype is fully constrained and known during
// analysis, otherwise a subtype of t constrained at run time.
func inferred(t *Type, v *Expr) *Type {
	if t.Unknown() {
		return t
	}
	vt := v.Type
	if vt.Unknown() || vt.isDynamic() || vt.Base() != t.Base() ||
		notFullyConstrained(vt, 0) {
		return runTime(t)
	}
	st := NewSubtype(t)
	switch t.Base().Class {
	case TypeArray:
		st.IndexCons = vt.Constraints()
		if notFullyConstrained(t.ElemType(), 0) {
			st.ElemSub = vt.ElemType()
		}
	case TypeRecord:
		for _, f := range t.Base().Fields {
			st.FieldSubs = append(st.FieldSubs, vt.FieldType(f))
		}
	default:
		return t
	}
	return st
}

// allocated is the subtype of a dereferenced access value: an object
// created by an allocator, whose constraint an unconstrained
// designated array subtype leaves to run time.
func allocated(t *Type) *Type {
	if t.Unknown() || t.Base().Class != TypeArray ||
		t.Constraints() != nil {
		return t
	}
	st := NewSubtype(t)
	st.dynamic = true
	return st
}

// isDynamic reports whether t or a subtype it refines is constrained
// only at run time (obj'SUBTYPE of an unconstrained interface object, an
// allocated object).
func (t *Type) isDynamic() bool {
	for s := t; s != nil; s = s.base {
		if s.dynamic {
			return true
		}
	}
	return false
}

// unconstrainedArray reports whether t is an array subtype whose
// (first) index range is not known from its declaration, so an
// aggregate of it cannot use others (LRM 9.3.3.3).
func unconstrainedArray(t *Type) bool {
	if t.Unknown() || t.Base().Class != TypeArray {
		return false
	}
	if t.isDynamic() {
		return false
	}
	cons := t.Constraints()
	return cons == nil || cons[0] == nil
}

// notFullyConstrained reports whether a composite subtype leaves an
// index range open, at any element level, or has a record element that
// does (LRM 5.1 fully constrained); run-time constrained subtypes and
// unknown types count as constrained.
func notFullyConstrained(t *Type, depth int) bool {
	if t.Unknown() || depth > 8 || t.isDynamic() {
		return false
	}
	switch b := t.Base(); b.Class {
	case TypeArray:
		cs := t.Constraints()
		if cs == nil || slices.Contains(cs, nil) {
			return true
		}
		return notFullyConstrained(t.ElemType(), depth+1)
	case TypeRecord:
		for _, f := range b.Fields {
			if notFullyConstrained(t.FieldType(f), depth+1) {
				return true
			}
		}
	}
	return false
}

// FieldType returns the subtype of record element f in a value of
// subtype t: the nearest record constraint's, else f's own.
func (t *Type) FieldType(f *Decl) *Type {
	for s := t; s != nil; s = s.base {
		if f.Pos < len(s.FieldSubs) && s.FieldSubs[f.Pos] != nil {
			return s.FieldSubs[f.Pos]
		}
	}
	return f.Type
}

// Dims returns the number of dimensions of an array type (0 for
// others).
func (t *Type) Dims() int {
	if b := t.Base(); b != nil && b.Class == TypeArray {
		return len(b.Index)
	}
	return 0
}

// Constraints returns the nearest index constraints of an array
// subtype, or nil when unconstrained.
func (t *Type) Constraints() []*RangeCons {
	var out []*RangeCons
	for s := t; s != nil; s = s.base {
		if s.IndexCons == nil {
			continue
		}
		if out == nil {
			if !slices.Contains(s.IndexCons, nil) {
				return s.IndexCons
			}
			out = slices.Clone(s.IndexCons)
			continue
		}
		// A VHDL-2008 open dimension keeps the range of the subtype
		// it refines.
		for i, rc := range out {
			if rc == nil && i < len(s.IndexCons) {
				out[i] = s.IndexCons[i]
			}
		}
		if !slices.Contains(out, nil) {
			break
		}
	}
	return out
}

// ScalarRange returns the nearest range constraint of a scalar subtype,
// or nil for an unconstrained base type.
func (t *Type) ScalarRange() *RangeCons {
	for s := t; s != nil; s = s.base {
		if s.Range != nil {
			return s.Range
		}
	}
	return nil
}

// ResolutionOf returns the nearest resolution indication of t.
func (t *Type) ResolutionOf() *Resolution {
	for s := t; s != nil; s = s.base {
		if s.Resolution != nil {
			return s.Resolution
		}
	}
	return nil
}

// Name returns the type's name for messages: the nearest named subtype
// as written, or a description of an anonymous type.
func (t *Type) Name() string {
	for s := t; s != nil; s = s.base {
		if s.Decl != nil {
			return s.Decl.Text
		}
		if s.Universal {
			if s.Class == TypeInteger {
				return "universal_integer"
			}
			return "universal_real"
		}
	}
	if t == nil || t.Class == TypeUnknown {
		return "unknown type"
	}
	return "anonymous " + t.Base().Class.String() + " type"
}

// SameBase reports whether t and u have the same base type.
func SameBase(t, u *Type) bool {
	return t != nil && u != nil && t.Base() == u.Base()
}

// Compatible reports whether a value of type have may be used where
// want is expected without an explicit conversion: the same base type,
// a universal numeric literal for a type of its class (implicit
// conversion, LRM 9.3.6), null for an access type, and anything when
// either side is unknown or generic.
func Compatible(have, want *Type) bool {
	if have.Unknown() || want.Unknown() {
		return true
	}
	h, w := have.Base(), want.Base()
	if h == w {
		return true
	}
	switch {
	case h.Class == TypeGeneric || w.Class == TypeGeneric:
		return true
	case h.Universal && h.Class == w.Class:
		return true
	case h.Class == TypeNull && w.Class == TypeAccess:
		return true
	case h.Class == TypeIncomplete || w.Class == TypeIncomplete:
		return h.Decl != nil && w.Decl != nil && h.Decl.Name == w.Decl.Name
	}
	return false
}

// The universal types (LRM 5.2.3.1, 5.2.5.1) and the unknown type,
// shared by every analysis.
var (
	UniversalInteger = &Type{Class: TypeInteger, Universal: true,
		Eval: eval.UniversalInteger}
	UniversalReal = &Type{Class: TypeFloating, Universal: true,
		Eval: eval.UniversalReal}
	UnknownType = &Type{Class: TypeUnknown}
	NullType    = &Type{Class: TypeNull}
	// anyComposite is the expected type of the value assigned to an
	// aggregate target: unknown, except that it must be composite.
	anyComposite = &Type{Class: TypeUnknown}
)
