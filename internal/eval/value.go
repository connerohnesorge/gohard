package eval

import (
	"cmp"
	"math"
	"strconv"
	"strings"
)

// Value is one VHDL value. Which fields are used depends on the class
// of T:
//
//   - integer: I; enumeration: I is the position; physical: I in
//     primary units; access: I is the allocation handle, 0 for null;
//   - floating: F;
//   - array: E holds the elements in row-major order and B the index
//     ranges of the value, one per dimension;
//   - record: E holds the fields in declaration order.
//
// Values are immutable: operations return new values and never modify
// their operands' E or B slices.
type Value struct {
	T *Type
	I int64
	F float64
	E []Value
	B []Range
}

// Int returns an integer value of type t.
func Int(t *Type, v int64) Value { return Value{T: t, I: v} }

// Enum returns the enumeration value at position pos of t.
func Enum(t *Type, pos int64) Value { return Value{T: t, I: pos} }

// Bool returns BOOLEAN'(b).
func Bool(b bool) Value {
	if b {
		return Value{T: Boolean, I: 1}
	}
	return Value{T: Boolean}
}

// RealValue returns a floating value of type t.
func RealValue(t *Type, f float64) Value { return Value{T: t, F: f} }

// Phys returns a physical value of type t in primary units.
func Phys(t *Type, v int64) Value { return Value{T: t, I: v} }

// Array returns an array value of type t with index ranges b and the
// elements e in row-major order.
func Array(t *Type, b []Range, e []Value) Value {
	return Value{T: t, B: b, E: e}
}

// Record returns a record value of type t with the fields e.
func Record(t *Type, e []Value) Value { return Value{T: t, E: e} }

// Str returns a STRING value with the bytes of s, indexed 1 to len(s).
func Str(s string) Value {
	e := make([]Value, len(s))
	for i := range len(s) {
		e[i] = Enum(Character, int64(s[i]))
	}
	return Array(String, []Range{{1, int64(len(s)), To}}, e)
}

// Kind returns the class of v's type.
func (v Value) Kind() Kind {
	if v.T == nil {
		return KindInvalid
	}
	return v.T.Kind
}

// Bool returns the value of a BOOLEAN or BIT as a Go bool.
func (v Value) Bool() bool { return v.I != 0 }

// Len returns the number of elements of a one-dimensional array value
// (the length of its first dimension).
func (v Value) Len() int64 {
	if len(v.B) == 0 {
		return int64(len(v.E))
	}
	return v.B[0].Len()
}

// GoString returns the bytes of a one-dimensional array of a character
// type (a STRING), each element's position as one byte.
func (v Value) GoString() string {
	var b strings.Builder
	for _, e := range v.E {
		b.WriteByte(byte(e.I))
	}
	return b.String()
}

// Equal reports whether v and w are the same value: the predefined "="
// (LRM 9.2.3). Arrays are equal when they have the same number of
// elements and matching elements are equal, whatever their bounds.
func (v Value) Equal(w Value) bool {
	switch v.Kind() {
	case KindFloating:
		return v.F == w.F
	case KindArray, KindRecord:
		if len(v.E) != len(w.E) {
			return false
		}
		for i := range v.E {
			if !v.E[i].Equal(w.E[i]) {
				return false
			}
		}
		return true
	}
	return v.I == w.I
}

// Compare orders v and w with the predefined relational operators:
// scalars by value (enumerations by position), one-dimensional arrays
// of discrete elements lexicographically (LRM 9.2.3). It returns -1, 0
// or +1.
func (v Value) Compare(w Value) int {
	switch v.Kind() {
	case KindFloating:
		return cmp.Compare(v.F, w.F)
	case KindArray:
		n := min(len(v.E), len(w.E))
		for i := range n {
			if c := v.E[i].Compare(w.E[i]); c != 0 {
				return c
			}
		}
		return cmp.Compare(len(v.E), len(w.E))
	}
	return cmp.Compare(v.I, w.I)
}

// Image returns the 'IMAGE of a scalar value (LRM 16.2): integers in
// decimal, enumeration literals as declared (identifiers in lower
// case), physical values in primary units ("10 fs"), floating values in
// the shortest form that reads back the same value. Composite values
// get an aggregate-like form used in messages.
func (v Value) Image() string {
	switch v.Kind() {
	case KindInteger:
		return strconv.FormatInt(v.I, 10)
	case KindEnum:
		lits := v.T.BaseType().Literals
		if v.I >= 0 && v.I < int64(len(lits)) {
			return lits[v.I]
		}
		return strconv.FormatInt(v.I, 10)
	case KindPhysical:
		s := strconv.FormatInt(v.I, 10)
		if u := v.T.BaseType().Units; len(u) > 0 {
			s += " " + u[0].Name
		}
		return s
	case KindFloating:
		return realImage(v.F)
	case KindAccess:
		if v.I == 0 {
			return "null"
		}
		return "access " + strconv.FormatInt(v.I, 10)
	case KindArray, KindRecord:
		if v.Kind() == KindArray && isCharArray(v.T) {
			return strconv.Quote(charText(v))
		}
		parts := make([]string, len(v.E))
		for i, e := range v.E {
			parts[i] = e.Image()
		}
		return "(" + strings.Join(parts, ", ") + ")"
	}
	return "<invalid>"
}

func realImage(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".en") {
		s += ".0"
	}
	return s
}

// charText returns the characters of an array of a character type:
// each element's character literal ('1' of STD_ULOGIC is "1"), or the
// position as a byte for the identifier literals of CHARACTER (NUL).
func charText(v Value) string {
	var b strings.Builder
	for _, e := range v.E {
		var lits []string
		if e.T != nil {
			lits = e.T.BaseType().Literals
		}
		if e.I >= 0 && e.I < int64(len(lits)) && len(lits[e.I]) == 3 &&
			lits[e.I][0] == '\'' {
			b.WriteByte(lits[e.I][1])
		} else {
			b.WriteByte(byte(e.I))
		}
	}
	return b.String()
}

// isCharArray reports whether t is a one-dimensional array of a
// character type: an enumeration type with a character literal (LRM
// 5.2.2.1), such as CHARACTER, BIT or STD_ULOGIC.
func isCharArray(t *Type) bool {
	b := t.BaseType()
	if len(b.Index) != 1 || b.Elem == nil || b.Elem.Kind != KindEnum {
		return false
	}
	for _, l := range b.Elem.BaseType().Literals {
		if strings.HasPrefix(l, "'") {
			return true
		}
	}
	return false
}

// parseValue implements 'VALUE (LRM 16.2.2): the value of t whose
// image is s, ignoring leading and trailing whitespace.
func parseValue(s string, t *Type) (Value, error) {
	s = strings.Trim(s, " \t\n\r\v\f ")
	bad := errf(ErrCodeRange, strconv.Quote(s)+" is not a value of "+
		t.Name)
	switch t.Kind {
	case KindInteger:
		n, err := strconv.ParseInt(strings.ReplaceAll(s, "_", ""), 10,
			64)
		if err != nil {
			return Value{}, bad
		}
		return Check(Int(t, n), t)
	case KindFloating:
		f, err := strconv.ParseFloat(strings.ReplaceAll(s, "_", ""), 64)
		if err != nil {
			return Value{}, bad
		}
		return Check(RealValue(t, f), t)
	case KindEnum:
		for i, l := range t.BaseType().Literals {
			if l == s || (!strings.HasPrefix(l, "'") &&
				strings.EqualFold(l, s)) {
				return Check(Enum(t, int64(i)), t)
			}
		}
	case KindPhysical:
		num, unit, ok := strings.Cut(s, " ")
		if !ok {
			return Value{}, bad
		}
		n, err := strconv.ParseInt(num, 10, 64)
		if err != nil {
			return Value{}, bad
		}
		for _, u := range t.BaseType().Units {
			if strings.EqualFold(u.Name, strings.TrimSpace(unit)) {
				v, err := mul64(n, u.Factor)
				if err != nil {
					return Value{}, bad
				}
				return Check(Phys(t, v), t)
			}
		}
	}
	return Value{}, bad
}
