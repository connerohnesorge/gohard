package sema

// methodsubst.go gives the methods of an object of a generic protected
// type (VHDL-2019, LRM 5.6.2, 6.4.2.4) the profile of the object's
// instance: each generic type of the protected type in a parameter or
// result type is replaced by the actual subtype the object binds. The
// call keeps the method's declaration (its body is the protected
// type's); only the types the overload resolution, the actuals and the
// call's result are checked against are substituted.

// typeSubst maps the generic types of a protected type to their actual
// subtypes in one object's instance; nil substitutes nothing.
type typeSubst map[*Type]*Type

// of returns the actual subtype of t when t is a substituted generic
// type, or t.
func (s typeSubst) of(t *Type) *Type {
	if n, ok := s[t]; ok && t != nil {
		return n
	}
	return t
}

// result returns the result type of the overloadable declaration d
// under s.
func (s typeSubst) result(d *Decl) *Type {
	return s.of(d.Denotes().Type)
}

// methodSubst returns the generic type actuals of the protected object
// a method of which is called with object as its prefix: the generic
// map of the object's variable declaration, of its type's
// instantiation or of its type's header. Nil when object is nil or of
// no instance of a generic protected type with a generic type.
func methodSubst(object *Expr) typeSubst {
	if object == nil || object.Type.Unknown() {
		return nil
	}
	t := object.Type
	if b := t.Base(); b.Class == TypeAccess && b.Designated != nil {
		t = b.Designated
	}
	inst := t.ProtectedInst()
	if inst == nil {
		return nil
	}
	var s typeSubst
	for _, x := range inst.Map {
		f := x.Formal
		if f == nil || f.Kind != DeclGenericType || f.Type == nil ||
			x.Actual == nil || x.Actual.Kind != ExprTypeMark ||
			x.Actual.Type.Unknown() {
			continue
		}
		if s == nil {
			s = typeSubst{}
		}
		s[f.Type] = x.Actual.Type
	}
	return s
}
