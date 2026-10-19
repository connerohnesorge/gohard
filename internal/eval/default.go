package eval

import "strconv"

// maxElements bounds the number of scalar elements one value built by
// the evaluator may hold, so a huge constraint fails instead of
// allocating (P7).
const maxElements = 1 << 26

// Default returns the default initial value of an object of subtype t
// (LRM 6.4.2.3): T'LEFT for a scalar, null for an access type, and the
// default of every element for a composite. An unconstrained array has
// no default value. Building it charges one step per element.
func Default(b *Budget, t *Type) (Value, error) {
	if t == nil {
		return Value{}, errf(ErrCodeType, "default of no type")
	}
	switch t.Kind {
	case KindInteger, KindEnum, KindPhysical:
		return Value{T: t, I: t.Range.Left}, nil
	case KindFloating:
		return Value{T: t, F: t.Real.Left}, nil
	case KindAccess, KindFile, KindProtected:
		return Value{T: t}, nil
	case KindRecord:
		fs := t.BaseType().Fields
		if len(t.Fields) == len(fs) {
			fs = t.Fields
		}
		out := make([]Value, len(fs))
		for i, f := range fs {
			v, err := Default(b, f.Type)
			if err != nil {
				return Value{}, err
			}
			out[i] = v
		}
		return Value{T: t, E: out}, nil
	case KindArray:
		if t.Bounds == nil {
			return Value{}, NotStatic("the default value of " +
				"unconstrained " + t.Name)
		}
		n := int64(1)
		for _, r := range t.Bounds {
			l := r.Len()
			if l != 0 && n > maxElements/l {
				return Value{}, Unsupported("an array of more than " +
					strconv.Itoa(maxElements) + " elements")
			}
			n *= l
		}
		if err := b.Step(n); err != nil {
			return Value{}, err
		}
		el := t.Elem
		if el == nil {
			el = t.BaseType().Elem
		}
		out := make([]Value, n)
		if n > 0 {
			v, err := Default(b, el)
			if err != nil {
				return Value{}, err
			}
			for i := range out {
				out[i] = v
			}
		}
		return Value{T: t, B: t.Bounds, E: out}, nil
	}
	return Value{}, errf(ErrCodeType, "default of a "+t.Kind.String()+
		" type")
}
