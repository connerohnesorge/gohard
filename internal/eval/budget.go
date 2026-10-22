package eval

import (
	"errors"
	"strconv"
)

// EditorSteps is the fixed static-evaluation limit of the editor and of
// `gohard check` (#34, P7): 1,000,000 evaluation steps, a count and
// never a time. Reaching it is the warning sema/eval-limit.
const EditorSteps = 1_000_000

// Budget counts evaluation steps. Every IR node evaluated, every
// element of an aggregate, concatenation or comparison and every call
// costs steps, so the count depends only on the expression and the
// values. A Budget is not safe for concurrent use; each pure job owns
// its own.
type Budget struct {
	limit int64
	used  int64
	// obs is an observer attached by the caller (a step profile): a
	// StepObserver is told of every charge.
	obs any
}

// SetObserver attaches o to b: whoever charges b may report to it (the
// interpreter's step profile, sema.Profile).
func (b *Budget) SetObserver(o any) {
	if b != nil {
		b.obs = o
	}
}

// StepObserver is an observer told of every charge of the budgets it
// is attached to.
type StepObserver interface {
	Steps(n int64)
}

// Observer returns the observer attached to b (nil when none).
func (b *Budget) Observer() any {
	if b == nil {
		return nil
	}
	return b.obs
}

// NewBudget returns a budget of limit steps; limit <= 0 means
// unlimited (the simulator counts its own budget around the
// evaluator).
func NewBudget(limit int64) *Budget { return &Budget{limit: limit} }

// Step charges n steps. It returns ErrBudget once more than the limit
// has been used; later calls keep failing.
func (b *Budget) Step(n int64) error {
	if b == nil {
		return nil
	}
	b.used += n
	if b.obs != nil {
		if o, ok := b.obs.(StepObserver); ok {
			o.Steps(n)
		}
	}
	if b.limit > 0 && b.used > b.limit {
		return ErrBudget
	}
	return nil
}

// Used returns the steps charged so far.
func (b *Budget) Used() int64 {
	if b == nil {
		return 0
	}
	return b.used
}

// Limit returns the limit (0: unlimited).
func (b *Budget) Limit() int64 {
	if b == nil {
		return 0
	}
	return b.limit
}

// Exhausted reports whether the budget has run out.
func (b *Budget) Exhausted() bool {
	return b != nil && b.limit > 0 && b.used > b.limit
}

// Code classifies evaluation errors.
type Code uint8

// Error codes.
const (
	// ErrCodeRange: a scalar value outside its subtype's range.
	ErrCodeRange Code = iota + 1
	// ErrCodeIndex: an index outside an array's index range.
	ErrCodeIndex
	// ErrCodeLength: arrays whose lengths must match do not (a width
	// mismatch).
	ErrCodeLength
	// ErrCodeDivZero: division or mod/rem by zero.
	ErrCodeDivZero
	// ErrCodeOverflow: an integer or physical result outside 64 bits.
	ErrCodeOverflow
	// ErrCodeBudget: the step budget ran out.
	ErrCodeBudget
	// ErrCodeNotStatic: the value is not known before simulation (a
	// signal, a variable, an impure call) — not an error in the
	// design, the expression is simply not static.
	ErrCodeNotStatic
	// ErrCodeNull: dereference of a null access value.
	ErrCodeNull
	// ErrCodeUnsupported: an operation the evaluator does not implement
	// yet.
	ErrCodeUnsupported
	// ErrCodeType: operands of the wrong class (a lowering bug or an
	// expression sema already reported).
	ErrCodeType
	// ErrCodeValue: the string of a 'VALUE is no literal of the type
	// (a run-time error of the design; analysis, which need not
	// evaluate the call, leaves it to the simulator as GHDL and NVC
	// do). A literal outside the type is ErrCodeRange.
	ErrCodeValue
)

// Error is an evaluation failure.
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// ErrBudget is returned when a Budget runs out.
var ErrBudget = &Error{Code: ErrCodeBudget,
	Msg: "evaluation step budget exhausted"}

// CodeOf returns the code of an evaluation error, or 0.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

func errf(c Code, msg string) error { return &Error{Code: c, Msg: msg} }

// RangeError is the error for v outside the subtype t.
func RangeError(v Value, t *Type) error {
	return errf(ErrCodeRange, "value "+v.Image()+" is out of range "+
		rangeText(t))
}

func rangeText(t *Type) string {
	switch t.Kind {
	case KindFloating:
		return realImage(t.Real.Left) + " " + t.Real.Dir.String() + " " +
			realImage(t.Real.Right)
	case KindEnum:
		lits := t.BaseType().Literals
		img := func(p int64) string {
			if p >= 0 && p < int64(len(lits)) {
				return lits[p]
			}
			return strconv.FormatInt(p, 10)
		}
		return img(t.Range.Left) + " " + t.Range.Dir.String() + " " +
			img(t.Range.Right)
	}
	return t.Range.String()
}

// LengthError is the width-mismatch error: an array of length got where
// want elements are needed.
func LengthError(want, got int64) error {
	return errf(ErrCodeLength, "length mismatch: expected "+
		strconv.FormatInt(want, 10)+" elements, got "+
		strconv.FormatInt(got, 10))
}

// IndexError is the error for index i outside r.
func IndexError(i int64, r Range) error {
	return errf(ErrCodeIndex, "index "+strconv.FormatInt(i, 10)+
		" is out of range "+r.String())
}

// SliceError is the error for a slice r of an array indexed by ar
// whose bounds leave ar or whose direction differs from ar's.
func SliceError(r, ar Range) error {
	if r.Dir != ar.Dir {
		return errf(ErrCodeIndex, "slice direction "+r.Dir.String()+
			" differs from the array's "+ar.Dir.String())
	}
	return errf(ErrCodeIndex, "slice "+r.String()+" is out of range "+
		ar.String())
}

// NotStatic is the error for a value only known during simulation.
func NotStatic(what string) error {
	return errf(ErrCodeNotStatic, what+" is not static")
}

// Unsupported is the error for an operation not implemented yet.
func Unsupported(what string) error {
	return errf(ErrCodeUnsupported, what+" is not supported by the "+
		"evaluator yet")
}

// Check returns v if it belongs to the subtype t (scalar range checks;
// array lengths against constrained bounds), else an Error. A
// constrained array value takes t's bounds (implicit subtype
// conversion, LRM 14.7.3.4).
func Check(v Value, t *Type) (Value, error) {
	if t == nil {
		return v, nil
	}
	switch t.Kind {
	case KindInteger, KindEnum, KindPhysical:
		if !t.Range.Contains(v.I) {
			return v, RangeError(v, t)
		}
		v.T = t
	case KindFloating:
		if !t.Real.Contains(v.F) {
			return v, RangeError(v, t)
		}
		v.T = t
	case KindArray:
		if t.Bounds != nil {
			if len(t.Bounds) != len(v.B) && len(v.B) != 0 {
				return v, LengthError(t.Bounds[0].Len(), v.Len())
			}
			for i, b := range t.Bounds {
				var got int64
				if i < len(v.B) {
					got = v.B[i].Len()
				} else {
					got = int64(len(v.E))
				}
				if got != b.Len() {
					return v, LengthError(b.Len(), got)
				}
			}
			v.B = t.Bounds
		}
		v.T = t
	default:
		v.T = t
	}
	return v, nil
}
