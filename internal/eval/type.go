package eval

import (
	"math"
	"strconv"

	"github.com/connerohnesorge/gohard/internal/token"
)

// Kind is the class of a VHDL type.
type Kind uint8

// Type classes. The zero Kind is not a valid type.
const (
	KindInvalid Kind = iota
	KindEnum
	KindInteger
	KindFloating
	KindPhysical
	KindArray
	KindRecord
	KindAccess
	KindFile
	KindProtected
)

var kindNames = [...]string{
	KindInvalid: "invalid", KindEnum: "enumeration",
	KindInteger: "integer", KindFloating: "floating",
	KindPhysical: "physical", KindArray: "array", KindRecord: "record",
	KindAccess: "access", KindFile: "file", KindProtected: "protected",
}

// String returns the class name, for example "integer".
func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "Kind(" + strconv.Itoa(int(k)) + ")"
}

// IsScalar reports whether k is an enumeration, integer, floating or
// physical type.
func (k Kind) IsScalar() bool {
	return k == KindEnum || k == KindInteger || k == KindFloating ||
		k == KindPhysical
}

// IsDiscrete reports whether k is an enumeration or integer type.
func (k Kind) IsDiscrete() bool { return k == KindEnum || k == KindInteger }

// Dir is the direction of a range.
type Dir uint8

// Directions.
const (
	To Dir = iota
	Downto
)

// String returns "to" or "downto".
func (d Dir) String() string {
	if d == Downto {
		return "downto"
	}
	return "to"
}

// Range is a discrete or physical range: integers, enumeration
// positions, or physical values in primary units.
type Range struct {
	Left  int64
	Right int64
	Dir   Dir
}

// Low returns the smaller bound.
func (r Range) Low() int64 {
	if r.Dir == Downto {
		return r.Right
	}
	return r.Left
}

// High returns the larger bound.
func (r Range) High() int64 {
	if r.Dir == Downto {
		return r.Left
	}
	return r.Right
}

// Null reports whether the range is empty.
func (r Range) Null() bool { return r.Low() > r.High() }

// Len returns the number of values in the range (0 for a null range).
// A range too long to count saturates at math.MaxInt64.
func (r Range) Len() int64 {
	if r.Null() {
		return 0
	}
	d := uint64(r.High()) - uint64(r.Low())
	if d >= math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(d) + 1
}

// CheckedLen returns the number of values in the range, or an overflow
// error when the count does not fit in 64 bits: INTEGER'RANGE of
// VHDL-2019 has 2**64 values, which Len would saturate.
func (r Range) CheckedLen() (int64, error) {
	if r.Null() {
		return 0, nil
	}
	d := uint64(r.High()) - uint64(r.Low())
	if d >= math.MaxInt64 {
		return 0, errf(ErrCodeOverflow, "the length of "+r.String()+
			" does not fit in 64 bits")
	}
	return int64(d) + 1, nil
}

// Contains reports whether v is in the range.
func (r Range) Contains(v int64) bool {
	return v >= r.Low() && v <= r.High()
}

// Offset returns the zero-based position of v from the left bound.
func (r Range) Offset(v int64) int64 {
	if r.Dir == Downto {
		return r.Left - v
	}
	return v - r.Left
}

// At returns the value at zero-based position i from the left bound.
func (r Range) At(i int64) int64 {
	if r.Dir == Downto {
		return r.Left - i
	}
	return r.Left + i
}

// String returns "L to R" or "L downto R".
func (r Range) String() string {
	return strconv.FormatInt(r.Left, 10) + " " + r.Dir.String() + " " +
		strconv.FormatInt(r.Right, 10)
}

// RealRange is a floating-point range.
type RealRange struct {
	Left  float64
	Right float64
	Dir   Dir
}

// Low returns the smaller bound.
func (r RealRange) Low() float64 {
	if r.Dir == Downto {
		return r.Right
	}
	return r.Left
}

// High returns the larger bound.
func (r RealRange) High() float64 {
	if r.Dir == Downto {
		return r.Left
	}
	return r.Right
}

// Contains reports whether v is in the range.
func (r RealRange) Contains(v float64) bool {
	return v >= r.Low() && v <= r.High()
}

// Field is one element of a record type.
type Field struct {
	Name string // lower-case identifier
	Type *Type
}

// Unit is one unit of a physical type: its name and its value in
// primary units.
type Unit struct {
	Name   string // lower-case identifier
	Factor int64
}

// Type is the evaluated layout of a VHDL subtype: the class, the bounds
// and, for composites, the element types. Sema builds Types from its
// type declarations once the constraints are known (per instance for
// generic-dependent constraints). A Type is immutable once built.
type Type struct {
	Kind Kind
	// Name is the type mark for messages and 'IMAGE errors, for example
	// "INTEGER" or "STD_ULOGIC_VECTOR"; anonymous types use their base
	// type's name.
	Name string
	// Base is the base type; nil means the Type is a base type itself.
	Base *Type

	// Range is the subtype's range for integer, enumeration (positions)
	// and physical (primary units) types.
	Range Range
	// Real is the range of a floating type.
	Real RealRange
	// Literals are the images of the enumeration literals in position
	// order: identifiers in lower case, character literals with their
	// quotes ("'0'").
	Literals []string
	// Units are the units of a physical type, primary unit first, in
	// declaration order.
	Units []Unit

	// Index are the index subtypes of an array type, one per
	// dimension.
	Index []*Type
	// Elem is the element subtype of an array, the designated subtype
	// of an access type, and the type of a file type's values.
	Elem *Type
	// Bounds are the index ranges of a constrained array subtype, one
	// per dimension; nil for an unconstrained array type.
	Bounds []Range

	// Fields are the elements of a record type in declaration order.
	Fields []Field
}

// BaseType returns the base type of t.
func (t *Type) BaseType() *Type {
	for t != nil && t.Base != nil {
		t = t.Base
	}
	return t
}

// SameBase reports whether t and u have the same base type.
func (t *Type) SameBase(u *Type) bool {
	return t != nil && u != nil && t.BaseType() == u.BaseType()
}

// Constrained reports whether an array type has fixed index ranges.
func (t *Type) Constrained() bool {
	return t.Kind != KindArray || t.Bounds != nil
}

// Subtype returns a copy of t's description as a subtype of t with
// range r (scalars).
func (t *Type) Subtype(r Range) *Type {
	s := *t
	s.Base = t.BaseType()
	s.Range = r
	return &s
}

// RealSubtype returns a floating subtype of t with range r.
func (t *Type) RealSubtype(r RealRange) *Type {
	s := *t
	s.Base = t.BaseType()
	s.Real = r
	return &s
}

// ArraySubtype returns the constrained subtype of the array type t with
// the index ranges bounds.
func (t *Type) ArraySubtype(bounds ...Range) *Type {
	s := *t
	s.Base = t.BaseType()
	s.Bounds = append([]Range(nil), bounds...)
	return &s
}

// Universal integer and real types: the types of numeric literals and
// of attributes such as 'POS before conversion (LRM 5.2.3, 5.2.5).
var (
	UniversalInteger = &Type{
		Kind: KindInteger, Name: "universal_integer",
		Range: Range{Left: math.MinInt64, Right: math.MaxInt64},
	}
	UniversalReal = &Type{
		Kind: KindFloating, Name: "universal_real",
		Real: RealRange{Left: -math.MaxFloat64, Right: math.MaxFloat64},
	}
)

// Standard types built the way std.standard declares them. Sema maps
// the declarations of std.standard to these values so every evaluator
// user shares one layout. Integer, Natural and Positive are the 32-bit
// INTEGER of VHDL-1993 to -2008; VHDL-2019 uses Integer64, Natural64
// and Positive64 instead (see StdIntegers). STRING and BIT_VECTOR keep
// their 32-bit index subtypes in every standard so that their identity
// (Str, concatenation) is shared.
var (
	Boolean = &Type{Kind: KindEnum, Name: "BOOLEAN",
		Literals: []string{"false", "true"}, Range: Range{0, 1, To}}
	Bit = &Type{Kind: KindEnum, Name: "BIT",
		Literals: []string{"'0'", "'1'"}, Range: Range{0, 1, To}}
	Character = &Type{Kind: KindEnum, Name: "CHARACTER",
		Literals: characterLiterals(), Range: Range{0, 255, To}}
	SeverityLevel = &Type{Kind: KindEnum, Name: "SEVERITY_LEVEL",
		Literals: []string{"note", "warning", "error", "failure"},
		Range:    Range{0, 3, To}}
	Integer = &Type{Kind: KindInteger, Name: "INTEGER",
		Range: Range{math.MinInt32, math.MaxInt32, To}}
	Real = &Type{Kind: KindFloating, Name: "REAL",
		Real: RealRange{-math.MaxFloat64, math.MaxFloat64, To}}
	Time = &Type{Kind: KindPhysical, Name: "TIME",
		Range: Range{math.MinInt64, math.MaxInt64, To},
		Units: []Unit{
			{"fs", 1}, {"ps", 1e3}, {"ns", 1e6}, {"us", 1e9},
			{"ms", 1e12}, {"sec", 1e15}, {"min", 60e15},
			{"hr", 3600e15},
		}}
	Natural  = Integer.Subtype(Range{0, math.MaxInt32, To})
	Positive = Integer.Subtype(Range{1, math.MaxInt32, To})
	// Integer64 is the INTEGER of VHDL-2019: the full 64-bit range
	// (LRM 5.2.3.2). It is named "INTEGER" like Integer, so messages
	// and traces name it the same way.
	Integer64 = &Type{Kind: KindInteger, Name: "INTEGER",
		Range: Range{math.MinInt64, math.MaxInt64, To}}
	Natural64  = Integer64.Subtype(Range{0, math.MaxInt64, To})
	Positive64 = Integer64.Subtype(Range{1, math.MaxInt64, To})
	String     = &Type{Kind: KindArray, Name: "STRING",
		Index: []*Type{Positive}, Elem: Character}
	BitVector = &Type{Kind: KindArray, Name: "BIT_VECTOR",
		Index: []*Type{Natural}, Elem: Bit}
)

// StdIntegers returns the layouts of std.standard's INTEGER, NATURAL
// and POSITIVE under the standard std: the 64-bit triple for VHDL-2019
// and later, the 32-bit one before.
func StdIntegers(std token.Standard) (integer, natural, positive *Type) {
	if std >= token.Std2019 {
		return Integer64, Natural64, Positive64
	}
	return Integer, Natural, Positive
}

// InferSubtype returns the subtype of an object of subtype t whose
// constraint comes from its value v (VHDL-2019 inferred constraints,
// LRM 6.4.2.1): the index ranges of an unconstrained array from v's
// bounds, and recursively the open element constraints of arrays (from
// the first element) and of record fields (from each field). A fully
// constrained t, or a v that cannot supply a constraint (a null array
// for its element subtype, a value of another class), leaves that part
// as it is; t itself is returned when nothing is inferred. Checking v
// against the result stays the caller's job (Check).
func InferSubtype(t *Type, v Value) *Type {
	if t == nil || FullyConstrained(t) || v.Kind() != t.Kind {
		return t
	}
	switch t.Kind {
	case KindArray:
		s := *t
		s.Base = t.BaseType()
		if s.Bounds == nil {
			n := len(t.BaseType().Index)
			if len(v.B) == n {
				s.Bounds = append([]Range(nil), v.B...)
			} else if n == 1 && len(v.B) == 0 {
				s.Bounds = []Range{{Left: 0,
					Right: int64(len(v.E)) - 1}}
			}
		}
		if s.Elem != nil && len(v.E) > 0 {
			s.Elem = InferSubtype(s.Elem, v.E[0])
		}
		return &s
	case KindRecord:
		if len(v.E) != len(t.Fields) {
			return t
		}
		s := *t
		s.Base = t.BaseType()
		s.Fields = make([]Field, len(t.Fields))
		for i, f := range t.Fields {
			s.Fields[i] = Field{Name: f.Name,
				Type: InferSubtype(f.Type, v.E[i])}
		}
		return &s
	}
	return t
}

// FullyConstrained reports whether t has no open constraint anywhere:
// arrays with index ranges and fully constrained elements, records
// whose fields are all fully constrained, and every scalar, access,
// file and protected type.
func FullyConstrained(t *Type) bool {
	switch t.Kind {
	case KindArray:
		return t.Bounds != nil &&
			(t.Elem == nil || FullyConstrained(t.Elem))
	case KindRecord:
		for _, f := range t.Fields {
			if f.Type != nil && !FullyConstrained(f.Type) {
				return false
			}
		}
	}
	return true
}

// characterLiterals returns the 256 images of CHARACTER in position
// order, as std.standard declares them (ISO 8859-1).
func characterLiterals() []string {
	ctrl := []string{
		"nul", "soh", "stx", "etx", "eot", "enq", "ack", "bel",
		"bs", "ht", "lf", "vt", "ff", "cr", "so", "si",
		"dle", "dc1", "dc2", "dc3", "dc4", "nak", "syn", "etb",
		"can", "em", "sub", "esc", "fsp", "gsp", "rsp", "usp",
	}
	out := make([]string, 256)
	for i := range out {
		switch {
		case i < 32:
			out[i] = ctrl[i]
		case i == 127:
			out[i] = "del"
		case i >= 128 && i < 160:
			out[i] = "c" + strconv.Itoa(i)
		default:
			// The raw byte, as in the ISO 8859-1 source text.
			out[i] = string([]byte{'\'', byte(i), '\''})
		}
	}
	return out
}
