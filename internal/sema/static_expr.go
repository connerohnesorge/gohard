package sema

import "github.com/connerohnesorge/gohard/internal/token"

// globallyStatic approximates LRM 9.4.3 globally static expressions:
// literals, generics, generate parameters, constants whose values are
// themselves globally static (a deferred constant counts), enumeration
// literals and units, predefined attributes other than signal
// attributes, and pure calls, aggregates, conversions and qualified
// expressions of such operands. Signals, variables, ports,
// subprogram parameters and loop parameters are not; nor is anything
// unknown (the caller does not report then).
func globallyStatic(e *Expr, depth int) (static, known bool) {
	if e == nil {
		return true, true
	}
	if depth > 32 || e.Type.Unknown() && e.Kind != ExprName &&
		e.Kind != ExprAttr {
		return true, false
	}
	all := func(es ...*Expr) (bool, bool) {
		for _, x := range es {
			s, k := globallyStatic(x, depth+1)
			if !k || !s {
				return s, k
			}
		}
		return true, true
	}
	switch e.Kind {
	case ExprLiteral, ExprPhysical, ExprTypeMark, ExprOthers:
		return true, true
	case ExprName:
		d := e.Decl.Denotes()
		if d == nil {
			return true, false
		}
		switch d.Kind {
		case DeclEnumLiteral, DeclUnit, DeclGeneric, DeclGenerateParam:
			return true, true
		case DeclConstant:
			if d.Value == nil {
				return true, true // deferred: static by its full value
			}
			return globallyStatic(d.Value, depth+1)
		case DeclSignal, DeclPort, DeclVariable, DeclSharedVariable,
			DeclParameter, DeclLoopParam, DeclFile:
			return false, true
		}
		return true, false
	case ExprCall:
		if d := e.Decl.Denotes(); d != nil && d.Kind == DeclEnumLiteral {
			return true, true
		}
		if d := e.Decl.Denotes(); d != nil && d.Kind == DeclFunction &&
			!d.Pure && !d.Implicit {
			return false, true
		}
		return all(e.Args...)
	case ExprAttr:
		if signalAttr(e.Attr) {
			return false, true
		}
		return all(e.Args...)
	case ExprAggregate:
		for _, as := range e.Assocs {
			if s, k := all(append(as.Choices, as.Value)...); !s || !k {
				return s, k
			}
		}
		return true, true
	case ExprQualified, ExprConversion:
		return all(e.Args...)
	case ExprRange:
		if e.Range != nil {
			return all(e.Range.Left, e.Range.Right)
		}
		return true, true
	}
	return true, false
}

// signalAttr reports whether k names a predefined signal attribute.
func signalAttr(k Key) bool {
	return predefinedAttrs[k] == attrSignal
}

// locallyStatic approximates LRM 9.4.2 locally static expressions:
// abstract, character, string and bit-string literals, enumeration
// literals, constants declared with a locally static value (not
// generics, not deferred), predefined operations, qualified
// expressions and conversions of locally static operands, from
// VHDL-2008 calls of the bundled ieee packages' functions, and
// attributes of type marks. known is false when e is not typed well
// enough to tell (nothing is reported then). Physical literals are not
// counted (GHDL and NVC reject them in type definitions).
func locallyStatic(e *Expr, depth int, std token.Standard,
) (static, known bool) {
	if e == nil {
		return true, true
	}
	if depth > 32 {
		return true, false
	}
	all := func(es ...*Expr) (bool, bool) {
		for _, x := range es {
			s, k := locallyStatic(x, depth+1, std)
			if !k || !s {
				return s, k
			}
		}
		return true, true
	}
	switch e.Kind {
	case ExprLiteral, ExprTypeMark, ExprOthers:
		return true, true
	case ExprPhysical:
		return false, true
	case ExprName:
		d := e.Decl.Denotes()
		if d == nil {
			return true, false
		}
		switch d.Kind {
		case DeclEnumLiteral:
			return true, true
		case DeclConstant:
			if d.Value == nil {
				return false, true // deferred
			}
			return locallyStatic(d.Value, depth+1, std)
		case DeclSignal, DeclPort, DeclVariable, DeclSharedVariable,
			DeclParameter, DeclLoopParam, DeclFile, DeclGeneric,
			DeclGenerateParam:
			return false, true
		}
		return true, false
	case ExprCall:
		d := e.Decl.Denotes()
		if d == nil {
			return true, false
		}
		if d.Kind == DeclEnumLiteral {
			return true, true
		}
		if !d.Implicit && !(std >= token.Std2008 && ieeeFunction(d)) {
			return false, true // a user function (LRM 9.4.2)
		}
		return all(e.Args...)
	case ExprAttr:
		switch {
		case signalAttr(e.Attr):
			return false, true
		case e.Decl != nil:
			return true, false // a user attribute: its specification's
		case e.Prefix != nil && e.Prefix.Kind == ExprTypeMark:
			return all(e.Args...)
		case e.Prefix != nil && e.Prefix.Kind == ExprName &&
			e.Prefix.Decl != nil:
			// An array attribute of an object is locally static when
			// the object's subtype is (LRM 9.4.2 g).
			t := e.Prefix.Decl.Denotes().Type
			if t.Unknown() || t.Base().Class != TypeArray {
				return true, false
			}
			cons := t.Constraints()
			if cons == nil || cons[0] == nil {
				return false, true
			}
			if cons[0].Attr != nil {
				return true, false
			}
			return all(cons[0].Left, cons[0].Right)
		}
		return true, false
	case ExprQualified, ExprConversion:
		return all(e.Args...)
	case ExprRange:
		if e.Range != nil && e.Range.Attr == nil {
			return all(e.Range.Left, e.Range.Right)
		}
		return true, false
	}
	return true, false
}

// ieeeFunction reports whether d is a function of a bundled ieee
// package (LRM 9.4.2 n: STD_LOGIC_1164, NUMERIC_BIT, NUMERIC_STD and
// their unsigned variants, and the other IEEE packages GHDL and NVC
// treat alike).
func ieeeFunction(d *Decl) bool {
	return d.Kind == DeclFunction && d.File != nil && d.File.Bundled &&
		d.File.Library == "ieee"
}
