package sema

import (
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/token"
)

// This file declares the implicitly defined operations of every type
// (LRM 5.2.6, 5.3.2.4, 5.4.3, 5.5.2, 6.5.3, 9.2, 16.?): the predefined
// operators, MINIMUM/MAXIMUM, TO_STRING, DEALLOCATE, the file
// operations and, for std.standard's types, the operations the
// standard lists in comments. predef_test.go checks the generated
// profiles of std.standard against those comments (the reference
// model).

// opSpec is one implicit operation to declare.
type opSpec struct {
	name   Key
	op     eval.Op
	params []*Type
	ret    *Type // nil for procedures
	first  token.Standard
	// pnames optionally names the parameters (else l, r / anonymous).
	pnames []string
	// modes and classes of the parameters for subprograms such as
	// FILE_OPEN; zero means in constant.
	modes   []Mode
	classes []ObjClass
}

var logicOps = []struct {
	name Key
	op   eval.Op
}{
	{`"and"`, eval.OpAnd}, {`"or"`, eval.OpOr}, {`"nand"`, eval.OpNand},
	{`"nor"`, eval.OpNor}, {`"xor"`, eval.OpXor}, {`"xnor"`, eval.OpXnor},
}

var relOps = []struct {
	name Key
	op   eval.Op
}{
	{`"<"`, eval.OpLt}, {`"<="`, eval.OpLe}, {`">"`, eval.OpGt},
	{`">="`, eval.OpGe},
}

var matchOps = []struct {
	name Key
	op   eval.Op
}{
	{`"?="`, eval.OpMatchEq}, {`"?/="`, eval.OpMatchNe},
	{`"?<"`, eval.OpMatchLt}, {`"?<="`, eval.OpMatchLe},
	{`"?>"`, eval.OpMatchGt}, {`"?>="`, eval.OpMatchGe},
}

var shiftOps = []struct {
	name Key
	op   eval.Op
}{
	{`"sll"`, eval.OpSll}, {`"srl"`, eval.OpSrl}, {`"sla"`, eval.OpSla},
	{`"sra"`, eval.OpSra}, {`"rol"`, eval.OpRol}, {`"ror"`, eval.OpRor},
}

// predefined declares the implicit operations of the type t declared
// by d, right after d in the current region. t is the base type (for a
// constrained array declaration, the anonymous base).
func (a *analyzer) predefined(t *Type, d *Decl) {
	if a.inStandard && !a.flushing {
		// std.standard declares BOOLEAN after DIRECTION and STRING
		// after INTEGER: its operations are declared once every type
		// exists (flushStandard).
		a.deferred = append(a.deferred, deferredOps{t, d})
		return
	}
	ops := a.implicitOps(t.Base(), d)
	for _, o := range ops {
		a.declareOp(o, d, t.Base())
	}
	if a.inStandard {
		a.standardExtras(d)
	}
}

func (a *analyzer) declareOp(o opSpec, d *Decl, t *Type) *Decl {
	kind := DeclFunction
	if o.ret == nil {
		kind = DeclProcedure
	}
	f := &Decl{Kind: kind, Name: o.name, Text: string(o.name),
		File: d.File, Span: d.Span, Full: d.Full, Unit: a.u,
		Type: o.ret, Op: uint8(o.op), Pure: true, First: o.first}
	// An operation is as old as its rule, not as its type: a view that
	// hides numeric_std's UNRESOLVED_UNSIGNED (2008) still gives 1993
	// designs "=" on UNSIGNED, a subtype of it (#33 hides names).
	if !o.name.IsOperator() {
		f.Text = o.name.Upper()
	}
	for i, pt := range o.params {
		name := "anonymous"
		switch {
		case i < len(o.pnames):
			name = o.pnames[i]
		case len(o.params) == 2:
			name = []string{"l", "r"}[i]
		}
		p := &Decl{Kind: DeclParameter, Name: IdentKey(name), Text: name,
			File: d.File, Span: d.Span, Unit: a.u, Type: pt, Pos: i,
			Mode: ModeIn, Class: ClassConstant, Implicit: true,
			Pure: true}
		if i < len(o.modes) && o.modes[i] != 0 {
			p.Mode = o.modes[i]
		}
		if i < len(o.classes) && o.classes[i] != 0 {
			p.Class = o.classes[i]
		}
		f.Params = append(f.Params, p)
	}
	a.implicitDecl(a.cur, f)
	if !t.Universal { // the universal types are shared values
		t.Ops = append(t.Ops, f)
	}
	return f
}

// deferredOps is a std.standard type whose operations wait for
// flushStandard.
type deferredOps struct {
	t *Type
	d *Decl
}

// flushStandard declares the operations of every std.standard type.
func (a *analyzer) flushStandard() {
	a.flushing = true
	for _, x := range a.deferred {
		a.predefined(x.t, x.d)
	}
	a.deferred, a.flushing = nil, false
}

// implicitOps lists the implicit operations of the base type t.
func (a *analyzer) implicitOps(t *Type, d *Decl) []opSpec {
	boolean := a.stdType("boolean")
	integer := a.stdType("integer")
	str := a.stdType("string")
	var out []opSpec
	add := func(o opSpec) { out = append(out, o) }
	bin := func(name Key, op eval.Op, l, r, ret *Type,
		first token.Standard) {
		add(opSpec{name: name, op: op, params: []*Type{l, r}, ret: ret,
			first: first})
	}
	un := func(name Key, op eval.Op, x, ret *Type, first token.Standard) {
		add(opSpec{name: name, op: op, params: []*Type{x}, ret: ret,
			first: first})
	}
	if t.Class != TypeFile && t.Class != TypeProtected {
		bin(`"="`, eval.OpEq, t, t, boolean, 0)
		bin(`"/="`, eval.OpNe, t, t, boolean, 0)
	}
	isBit := a.isStdType(d, "bit")
	isBool := a.isStdType(d, "boolean")
	isULogic := d.Unit != nil && d.Unit.Library == "ieee" &&
		d.Unit.Name == "std_logic_1164" && d.Name == "std_ulogic"
	switch t.Class {
	case TypeEnum, TypeInteger, TypeFloating, TypePhysical:
		for _, r := range relOps {
			bin(r.name, r.op, t, t, boolean, 0)
		}
		if isBit || isBool {
			for _, l := range logicOps {
				bin(l.name, l.op, t, t, t, 0)
			}
			un(`"not"`, eval.OpNot, t, t, 0)
		}
		if isBit || isULogic {
			for _, m := range matchOps {
				bin(m.name, m.op, t, t, t, token.Std2008)
			}
		}
		if isBit {
			un(`"??"`, eval.OpCondition, t, boolean, token.Std2008)
		}
		if t.Class != TypeEnum {
			un(`"+"`, eval.OpPlus, t, t, 0)
			un(`"-"`, eval.OpNeg, t, t, 0)
			un(`"abs"`, eval.OpAbs, t, t, 0)
			bin(`"+"`, eval.OpAdd, t, t, t, 0)
			bin(`"-"`, eval.OpSub, t, t, t, 0)
		}
		switch t.Class {
		case TypeInteger:
			bin(`"*"`, eval.OpMul, t, t, t, 0)
			bin(`"/"`, eval.OpDiv, t, t, t, 0)
			bin(`"mod"`, eval.OpMod, t, t, t, 0)
			bin(`"rem"`, eval.OpRem, t, t, t, 0)
			bin(`"**"`, eval.OpPow, t, integer, t, 0)
		case TypeFloating:
			bin(`"*"`, eval.OpMul, t, t, t, 0)
			bin(`"/"`, eval.OpDiv, t, t, t, 0)
			bin(`"**"`, eval.OpPow, t, integer, t, 0)
		case TypePhysical:
			real := a.stdType("real")
			bin(`"*"`, eval.OpMul, t, integer, t, 0)
			bin(`"*"`, eval.OpMul, t, real, t, 0)
			bin(`"*"`, eval.OpMul, integer, t, t, 0)
			bin(`"*"`, eval.OpMul, real, t, t, 0)
			bin(`"/"`, eval.OpDiv, t, integer, t, 0)
			bin(`"/"`, eval.OpDiv, t, real, t, 0)
			bin(`"/"`, eval.OpDiv, t, t, UniversalInteger, 0)
			bin(`"mod"`, eval.OpMod, t, t, t, token.Std2008)
			bin(`"rem"`, eval.OpRem, t, t, t, token.Std2008)
		}
		bin("minimum", eval.OpMinimum, t, t, t, token.Std2008)
		bin("maximum", eval.OpMaximum, t, t, t, token.Std2008)
		if str != nil {
			add(opSpec{name: "to_string", params: []*Type{t}, ret: str,
				first: token.Std2008, pnames: []string{"value"}})
		}
	case TypeArray:
		out = append(out, a.arrayOps(t, d)...)
	case TypeAccess:
		add(opSpec{name: "deallocate", params: []*Type{t},
			pnames: []string{"p"}, modes: []Mode{ModeInout},
			classes: []ObjClass{ClassVariable}})
	case TypeFile:
		out = append(out, a.fileOps(t)...)
	case TypeGeneric:
		// LRM 6.5.3: a generic type has only "=" and "/=", above.
	}
	return out
}

// arrayOps lists the implicit operations of an array base type.
func (a *analyzer) arrayOps(t *Type, d *Decl) []opSpec {
	var out []opSpec
	add := func(o opSpec) { out = append(out, o) }
	boolean := a.stdType("boolean")
	integer := a.stdType("integer")
	if len(t.Index) != 1 {
		return nil
	}
	e := t.Elem
	eb := e.Base()
	discrete := e.IsDiscrete()
	scalar := e.IsScalar()
	bin := func(name Key, op eval.Op, l, r, ret *Type,
		first token.Standard) {
		add(opSpec{name: name, op: op, params: []*Type{l, r}, ret: ret,
			first: first})
	}
	if discrete {
		for _, r := range relOps {
			bin(r.name, r.op, t, t, boolean, 0)
		}
	}
	elemBit := eb != nil && eb.Decl != nil &&
		(a.isStdType(eb.Decl, "bit") || a.isStdType(eb.Decl, "boolean"))
	elemULogic := eb != nil && eb.Decl != nil && eb.Decl.Unit != nil &&
		eb.Decl.Unit.Library == "ieee" &&
		eb.Decl.Unit.Name == "std_logic_1164" &&
		eb.Decl.Name == "std_ulogic"
	if elemBit {
		for _, l := range logicOps {
			bin(l.name, l.op, t, t, t, 0)
		}
		add(opSpec{name: `"not"`, op: eval.OpNot, params: []*Type{t},
			ret: t})
		for _, l := range logicOps {
			bin(l.name, l.op, t, e, t, token.Std2008)
			bin(l.name, l.op, e, t, t, token.Std2008)
		}
		for _, l := range logicOps {
			add(opSpec{name: l.name, op: l.op, params: []*Type{t},
				ret: e, first: token.Std2008})
		}
		for _, s := range shiftOps {
			bin(s.name, s.op, t, integer, t, 0)
		}
	}
	if elemBit || elemULogic {
		bin(`"?="`, eval.OpMatchEq, t, t, e, token.Std2008)
		bin(`"?/="`, eval.OpMatchNe, t, t, e, token.Std2008)
	}
	bin(`"&"`, eval.OpConcat, t, t, t, 0)
	bin(`"&"`, eval.OpConcat, t, e, t, 0)
	bin(`"&"`, eval.OpConcat, e, t, t, 0)
	bin(`"&"`, eval.OpConcat, e, e, t, 0)
	if discrete {
		bin("minimum", eval.OpMinimum, t, t, t, token.Std2008)
		bin("maximum", eval.OpMaximum, t, t, t, token.Std2008)
	}
	if scalar {
		add(opSpec{name: "minimum", op: eval.OpMinimum,
			params: []*Type{t}, ret: e, first: token.Std2008,
			pnames: []string{"l"}})
		add(opSpec{name: "maximum", op: eval.OpMaximum,
			params: []*Type{t}, ret: e, first: token.Std2008,
			pnames: []string{"l"}})
	}
	if str := a.stdType("string"); str != nil {
		// TO_STRING of a character array is VHDL-2008; VHDL-2019
		// extends it to every one-dimensional array of a scalar type
		// (std.standard lists INTEGER_VECTOR's).
		switch {
		case charEnum(eb):
			add(opSpec{name: "to_string", params: []*Type{t}, ret: str,
				first: token.Std2008, pnames: []string{"value"}})
		case scalar:
			add(opSpec{name: "to_string", params: []*Type{t}, ret: str,
				first: token.Std2019, pnames: []string{"value"}})
		}
	}
	return out
}

// charEnum reports whether t is a character type: an enumeration type
// with at least one character literal (LRM 5.2.2.1).
func charEnum(t *Type) bool {
	if t == nil || t.Class != TypeEnum {
		return false
	}
	for _, l := range t.Literals {
		if l.Name.IsCharacter() {
			return true
		}
	}
	return false
}

// fileOps lists FILE_OPEN, FILE_CLOSE, READ, WRITE, FLUSH, ENDFILE and
// the VHDL-2019 file operations of a file type (LRM 5.5.2).
func (a *analyzer) fileOps(t *Type) []opSpec {
	str := a.stdType("string")
	kind := a.stdType("file_open_kind")
	status := a.stdType("file_open_status")
	boolean := a.stdType("boolean")
	natural := a.stdType("natural")
	ct := t.Content
	f := ClassFile
	in, out, inout := ModeIn, ModeOut, ModeInout
	var ops []opSpec
	add := func(o opSpec) { ops = append(ops, o) }
	add(opSpec{name: "file_open", params: []*Type{t, str, kind},
		pnames:  []string{"f", "external_name", "open_kind"},
		classes: []ObjClass{f}})
	add(opSpec{name: "file_open", params: []*Type{status, t, str, kind},
		pnames:  []string{"status", "f", "external_name", "open_kind"},
		modes:   []Mode{out},
		classes: []ObjClass{ClassVariable, f}})
	add(opSpec{name: "file_close", params: []*Type{t},
		pnames: []string{"f"}, classes: []ObjClass{f}})
	add(opSpec{name: "read", params: []*Type{t, ct},
		pnames: []string{"f", "value"}, modes: []Mode{in, out},
		classes: []ObjClass{f, ClassVariable}})
	if ct != nil && ct.Base().Class == TypeArray &&
		ct.Constraints() == nil {
		add(opSpec{name: "read", params: []*Type{t, ct, natural},
			pnames: []string{"f", "value", "length"},
			modes:  []Mode{in, out, out},
			classes: []ObjClass{f, ClassVariable,
				ClassVariable}})
	}
	add(opSpec{name: "write", params: []*Type{t, ct},
		pnames: []string{"f", "value"}, classes: []ObjClass{f}})
	add(opSpec{name: "flush", params: []*Type{t}, pnames: []string{"f"},
		classes: []ObjClass{f}, first: token.Std2008})
	add(opSpec{name: "endfile", params: []*Type{t}, ret: boolean,
		pnames: []string{"f"}, classes: []ObjClass{f}})
	_ = inout
	return ops
}

// standardExtras declares the std.standard operations that are not
// derived from a type's class: RISING_EDGE and FALLING_EDGE of BOOLEAN
// and BIT, TO_STRING with a format, TO_OSTRING/TO_HSTRING and their
// aliases for BIT_VECTOR, and the universal operations.
func (a *analyzer) standardExtras(d *Decl) {
	str := a.stdType("string")
	sig := []Mode{ModeIn}
	sc := []ObjClass{ClassSignal}
	switch d.Name {
	case "boolean", "bit":
		for _, n := range []Key{"rising_edge", "falling_edge"} {
			a.declareOp(opSpec{name: n, params: []*Type{d.Type},
				ret: a.stdType("boolean"), first: token.Std2008,
				pnames: []string{"s"}, modes: sig, classes: sc},
				d, d.Type)
		}
	case "integer":
		a.universalOps(d)
	case "bit_vector":
		for _, n := range []Key{"to_ostring", "to_hstring"} {
			a.declareOp(opSpec{name: n, params: []*Type{d.Type}, ret: str,
				first: token.Std2008, pnames: []string{"value"}}, d,
				d.Type)
		}
	case "time":
		a.declareOp(opSpec{name: "to_string",
			params: []*Type{d.Type, d.Type}, ret: str,
			first: token.Std2008, pnames: []string{"value", "unit"}}, d,
			d.Type)
		real := a.stdType("real")
		natural := a.stdType("natural")
		if real != nil {
			a.declareOp(opSpec{name: "to_string",
				params: []*Type{real, natural}, ret: str,
				first:  token.Std2008,
				pnames: []string{"value", "digits"}}, d, real.Base())
			a.declareOp(opSpec{name: "to_string",
				params: []*Type{real, str}, ret: str,
				first:  token.Std2008,
				pnames: []string{"value", "format"}}, d, real.Base())
		}
	}
}

// universalOps declares the operations of universal_integer and
// universal_real, which std.standard lists before INTEGER.
func (a *analyzer) universalOps(d *Decl) {
	boolean := a.stdType("boolean")
	integer := d.Type
	for _, u := range []*Type{UniversalInteger, UniversalReal} {
		var ops []opSpec
		bin := func(name Key, op eval.Op, l, r, ret *Type) {
			ops = append(ops, opSpec{name: name, op: op,
				params: []*Type{l, r}, ret: ret})
		}
		bin(`"="`, eval.OpEq, u, u, boolean)
		bin(`"/="`, eval.OpNe, u, u, boolean)
		for _, r := range relOps {
			bin(r.name, r.op, u, u, boolean)
		}
		for _, x := range []struct {
			n  Key
			op eval.Op
		}{{`"+"`, eval.OpPlus}, {`"-"`, eval.OpNeg},
			{`"abs"`, eval.OpAbs}} {
			ops = append(ops, opSpec{name: x.n, op: x.op,
				params: []*Type{u}, ret: u})
		}
		bin(`"+"`, eval.OpAdd, u, u, u)
		bin(`"-"`, eval.OpSub, u, u, u)
		bin(`"*"`, eval.OpMul, u, u, u)
		bin(`"/"`, eval.OpDiv, u, u, u)
		if u == UniversalInteger {
			bin(`"mod"`, eval.OpMod, u, u, u)
			bin(`"rem"`, eval.OpRem, u, u, u)
		} else {
			bin(`"*"`, eval.OpMul, u, UniversalInteger, u)
			bin(`"*"`, eval.OpMul, UniversalInteger, u, u)
			bin(`"/"`, eval.OpDiv, u, UniversalInteger, u)
		}
		bin(`"**"`, eval.OpPow, u, integer, u)
		ops = append(ops,
			opSpec{name: "minimum", op: eval.OpMinimum,
				params: []*Type{u, u}, ret: u, first: token.Std2008},
			opSpec{name: "maximum", op: eval.OpMaximum,
				params: []*Type{u, u}, ret: u, first: token.Std2008})
		if str := a.stdType("string"); str != nil {
			ops = append(ops, opSpec{name: "to_string",
				params: []*Type{u}, ret: str, first: token.Std2008,
				pnames: []string{"value"}})
		}
		for _, o := range ops {
			a.declareOp(o, d, u)
		}
	}
}

// isStdType reports whether d is the std.standard type named name.
func (a *analyzer) isStdType(d *Decl, name Key) bool {
	return d != nil && d.Name == name && d.Unit != nil &&
		d.Unit.Library == "std" && d.Unit.Name == "standard"
}
