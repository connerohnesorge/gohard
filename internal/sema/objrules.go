package sema

// objrules.go holds the object class rules of declarations and
// interface lists (#34): which types an object of each class may have
// (LRM 6.4.2, 6.5.2), which interface objects may have a default
// expression, the object class of ports, and the function result
// subtype (LRM 4.2.1).

import (
	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// checksOff reports whether the rules of this file are off: while
// names are quiet, and in the bundled std/ieee text.
func (a *analyzer) checksOff() bool {
	return a.quiet || a.u.File.Bundled
}

// accessPart returns "access" when t is an access type, "subelement"
// when t is a composite type with a subelement of an access type, and
// "" otherwise (LRM 6.4.2.2, 6.4.2.3, 6.5.2).
func accessPart(t *Type) string {
	b := t.Base()
	if b == nil {
		return ""
	}
	if b.Class == TypeAccess {
		return "access"
	}
	if hasAccessElem(b, 0) {
		return "subelement"
	}
	return ""
}

// hasAccessElem reports whether the composite type t has a subelement
// of an access type.
func hasAccessElem(t *Type, depth int) bool {
	b := t.Base()
	if b == nil || depth > 32 {
		return false
	}
	switch b.Class {
	case TypeAccess:
		return depth > 0
	case TypeArray:
		return b.Elem != nil &&
			(b.Elem.Base().Class == TypeAccess ||
				hasAccessElem(b.Elem, depth+1))
	case TypeRecord:
		for _, f := range b.Fields {
			if f.Type != nil && (f.Type.Base().Class == TypeAccess ||
				hasAccessElem(f.Type, depth+1)) {
				return true
			}
		}
	}
	return false
}

// objectType reports a type that an object of the given kind cannot
// have: a constant, signal, generic constant or port is never of a
// file type, an access type or a composite type with an access
// subelement (LRM 6.4.2.2, 6.4.2.3, 6.5.2), and a variable is never of
// a file type (LRM 6.4.2.4). what names the object ("a signal").
func (a *analyzer) objectType(rg cst.Range, what string, t *Type,
	variable bool,
) {
	if a.checksOff() || t.Unknown() {
		return
	}
	if t.Base().Class == TypeFile {
		a.semaError(rg, what+" cannot be of the file type "+t.Name())
		return
	}
	if variable {
		return
	}
	switch t.Base().Class {
	case TypeProtected:
		// LRM 6.4.2.2, 6.4.2.3, 6.5.2: only variables are of a
		// protected type.
		a.semaError(rg, what+" cannot be of the protected type "+
			t.Name())
		return
	case TypeGeneric:
		// A VHDL-2019 generic type of the access or file class.
		if c := genericTypeClass(t); c != "" {
			a.semaError(rg, what+" cannot be of the "+c+" type "+
				t.Name())
		}
		return
	}
	if hasProtectedPart(t.Base(), 0) { // typedecl.go
		// LRM 6.4.2.2, 6.4.2.3, 6.5.2: only variables hold protected
		// objects, in VHDL-2019 also as subelements.
		a.semaError(rg, what+" cannot be of the type "+t.Name()+
			", which has a subelement of a protected type")
		return
	}
	switch accessPart(t) {
	case "access":
		a.semaError(rg, what+" cannot be of the access type "+t.Name())
	case "subelement":
		a.semaError(rg, what+" cannot be of the type "+t.Name()+
			", which has a subelement of an access type")
	}
}

// objectDeclRules checks the type of an object declaration of kind k.
func (a *analyzer) objectDeclRules(si cst.SubtypeIndication, k DeclKind,
	t *Type,
) {
	switch k {
	case DeclConstant:
		a.objectType(si.Span(), "a constant", t, false)
	case DeclSignal:
		a.objectType(si.Span(), "a signal", t, false)
	case DeclVariable, DeclSharedVariable:
		a.objectType(si.Span(), "a variable", t, true)
	}
}

// genericTypeClass returns "access" or "file" for a VHDL-2019 generic
// type of that class (type t is access ..., type t is file of ...),
// "" otherwise.
func genericTypeClass(t *Type) string {
	d := t.Base().Decl
	if d == nil || d.Node.IsZero() {
		return ""
	}
	for c := range d.Node.Nodes() {
		switch c.Kind() {
		case cst.KindAccessIncompleteTypeDef:
			return "access"
		case cst.KindFileIncompleteTypeDef:
			return "file"
		}
	}
	return ""
}

// fileDeclRules checks that a file declaration's subtype is a file
// type (LRM 6.4.2.5).
func (a *analyzer) fileDeclRules(si cst.SubtypeIndication, t *Type) {
	if a.checksOff() || t.Unknown() || t.Base().Class == TypeFile {
		return
	}
	a.semaError(si.Span(), "the subtype of a file declaration must be "+
		"a file type, not "+t.Name())
}

// resultRules checks a function's result subtype: never a file type
// (LRM 4.2.1).
func (a *analyzer) resultRules(ret cst.ReturnSpec, t *Type) {
	if !a.checksOff() && !t.Unknown() &&
		t.Base().Class == TypeProtected && a.std < token.Std2019 {
		// LRM 4.2.1: before VHDL-2019 a function does not return a
		// protected type.
		a.semaError(ret.TypeMark().Span(), "the result subtype of a "+
			"function cannot be the protected type "+t.Name())
		return
	}
	if a.checksOff() || t.Unknown() || t.Base().Class != TypeFile {
		return
	}
	a.semaError(ret.TypeMark().Span(), "the result subtype of a "+
		"function cannot be the file type "+t.Name())
}

// interfaceRules checks one interface object declaration of kind k
// (generic, port or parameter) with its class, mode, subtype and
// default (LRM 6.5.2, 6.5.6.3, 4.2.2).
func (a *analyzer) interfaceRules(io cst.InterfaceObjectDecl, k DeclKind,
	class ObjClass, mode Mode, t *Type, hasDefault bool,
) {
	if a.checksOff() {
		return
	}
	sp := io.Subtype().Span()
	switch k {
	case DeclPort:
		// LRM 6.5.6.3: ports are signals (VHDL-2019 adds variable
		// ports; before it the parser reports a variable port as
		// syntax/standard).
		if class == ClassConstant || class == ClassFile {
			a.semaError(io.Class().Span(), "a "+io.Class().Text()+
				" interface is not allowed in a port clause")
			return
		}
		if class == ClassSignal {
			a.objectType(sp, "a port", t, false)
		}
		if class == ClassVariable && a.std >= token.Std2019 {
			a.variablePort(io, mode, t, hasDefault)
			return
		}
		if hasDefault && mode == ModeLinkage {
			a.semaError(io.Node.Span(), "a port of mode linkage "+
				"cannot have a default expression")
		}
	case DeclGeneric:
		if class == ClassConstant {
			a.objectType(sp, "a generic", t, false)
		}
	case DeclParameter:
		if !t.Unknown() && t.Base().Class == TypeFile &&
			class != ClassFile && !io.Class().IsZero() {
			// LRM 6.5.2: an object of a file type is a file.
			a.semaError(sp, "a parameter of the file type "+t.Name()+
				" must be of class file")
		}
		if class == ClassSignal {
			a.objectType(sp, "a signal parameter", t, false)
		}
		if class == ClassConstant && !t.Unknown() &&
			(t.Base().Class == TypeProtected ||
				hasProtectedPart(t.Base(), 0)) {
			// LRM 6.5.2: a formal of a protected type (or holding
			// one) is a variable.
			a.objectType(sp, "a constant parameter", t, false)
		}
		switch {
		case !hasDefault:
		case class == ClassSignal:
			// LRM 4.2.2.3 (6.5.2 in VHDL-2008): a signal parameter has
			// no default expression.
			a.semaError(io.Node.Span(), "a signal parameter cannot "+
				"have a default expression")
		case !t.Unknown() && t.Base().Class == TypeProtected:
			// LRM 6.5.2: no default expression for an interface
			// object of a protected type.
			a.semaError(io.Node.Span(), "a parameter of the protected "+
				"type "+t.Name()+" cannot have a default expression")
		case class == ClassVariable && (mode == ModeOut ||
			mode == ModeInout):
			// LRM 6.5.2: only a variable parameter of mode in may
			// have a default expression.
			a.semaError(io.Node.Span(), "a variable parameter of mode "+
				"out or inout cannot have a default expression")
		}
	}
}

// pureParams checks the formal parameters of pure function d under
// VHDL-2019, which lets impure functions have parameters of any mode
// and class (LRM 4.2.2.1): a pure function's parameters have mode in
// and class constant, signal or file.
func (a *analyzer) pureParams(d *Decl) {
	if a.checksOff() || a.std < token.Std2019 {
		return
	}
	for _, p := range d.Params {
		switch {
		case p.Mode != ModeIn && p.Class != ClassFile:
			a.semaError(p.Span, "parameter "+p.Text+" of pure function "+
				d.Text+" must have mode in, not "+p.Mode.text())
		case p.Class == ClassVariable:
			a.semaError(p.Span, "parameter "+p.Text+" of pure function "+
				d.Text+" must be a constant, signal or file, not a "+
				"variable")
		}
	}
}

// protectedCopy reports a value given to a variable of type t that
// is or holds a protected object (LRM 5.6.1, 6.4.2.4; VHDL-2019
// composites of protected types): such a variable has no initial value
// and is never the target of a variable assignment, which would copy
// the object. how is "cannot be assigned" or "cannot have an initial
// value".
func (a *analyzer) protectedCopy(rg cst.Range, t *Type, how string) {
	if a.checksOff() || t.Unknown() {
		return
	}
	switch b := t.Base(); {
	case b.Class == TypeProtected:
		a.semaError(rg, "a variable of the protected type "+t.Name()+
			" "+how)
	case hasProtectedPart(b, 0): // typedecl.go
		a.semaError(rg, "a variable of the type "+t.Name()+", which "+
			"has a subelement of a protected type, "+how)
	}
}

// variableParam reports whether VHDL-2019 makes a formal parameter of
// type t with no explicit class a variable (LRM 6.5.2): t is a
// protected or access type, or a composite holding a protected object.
func variableParam(t *Type) bool {
	if t.Unknown() {
		return false
	}
	switch b := t.Base(); b.Class {
	case TypeProtected, TypeAccess:
		return true
	default:
		return hasProtectedPart(b, 0)
	}
}

// variablePort checks a VHDL-2019 variable port (LRM 6.5.2): it is of
// a protected type, has mode inout and no default expression.
func (a *analyzer) variablePort(io cst.InterfaceObjectDecl, mode Mode,
	t *Type, hasDefault bool,
) {
	if !t.Unknown() && t.Base().Class != TypeProtected &&
		t.Base().Class != TypeGeneric {
		a.semaError(io.Subtype().Span(), "a variable port must be of "+
			"a protected type, not "+t.Name())
	}
	if mode != ModeInout {
		rg := io.Names().Span()
		if m := io.Mode(); !m.IsZero() {
			rg = m.Span()
		}
		a.semaError(rg, "a variable port must have mode inout")
	}
	if hasDefault {
		a.semaError(io.Node.Span(), "a variable port cannot have a "+
			"default expression")
	}
}

// variableActual checks the actual of the VHDL-2019 variable port f in
// a port map (LRM 6.5.7.3): a shared variable or a variable port of
// the enclosing design entity, never open or an expression. e is the
// typed actual (nil for open), n its node.
func (a *analyzer) variableActual(f *Decl, e *Expr, n cst.Node) {
	if a.checksOff() || f == nil || f.Kind != DeclPort ||
		f.Class != ClassVariable || a.std < token.Std2019 {
		return
	}
	if e != nil && (e.Type.Unknown() || f.Type.Unknown() ||
		e.Type.Base() != f.Type.Base()) {
		return // a type error, reported where it is typed
	}
	e = Unalias(e)
	if e != nil && e.Kind == ExprName && e.Decl != nil &&
		sharedObject(e.Decl.Denotes()) {
		return
	}
	if e != nil && e.Kind != ExprName && sharedObject(actualObject(e)) {
		a.semaError(n.Span(), "an element of a shared variable cannot "+
			"be the actual of variable port "+f.Text)
		return
	}
	a.semaError(n.Span(), "the actual of variable port "+f.Text+
		" must be a shared variable or a variable port")
}

// sharedObject reports whether d is a shared variable or a variable
// port: what a variable port may be bound to.
func sharedObject(d *Decl) bool {
	return d != nil && (d.Kind == DeclSharedVariable ||
		d.Kind == DeclPort && d.Class == ClassVariable)
}

// Unalias returns the name the object alias e denotes, following
// aliases of whole objects (alias al is sv;) to the aliased name; e
// itself when it is not such an alias (LRM 6.6.2).
func Unalias(e *Expr) *Expr {
	for range 64 { // alias chains are short; never loop forever
		if e == nil || e.Kind != ExprName || e.Decl == nil {
			return e
		}
		d := e.Decl
		for range 64 { // an alias of an alias name: its Target
			if d.Kind != DeclAlias || d.Value != nil || d.Target == nil ||
				d.Target == d {
				break
			}
			d = d.Target
		}
		if d.Kind != DeclAlias || d.Value == nil {
			return e
		}
		e = d.Value
	}
	return e
}

// paramRead reports a read of a parameter of mode out that cannot be
// read: a signal parameter never (LRM 6.5.2), a variable parameter
// before VHDL-2008. It reports whether it found one.
func (a *analyzer) paramRead(e *Expr) bool {
	d := actualObject(e)
	if a.checksOff() || d == nil || d.Kind != DeclParameter ||
		d.Mode != ModeOut {
		return false
	}
	switch {
	case d.Class == ClassSignal:
		a.semaError(e.Node.Span(), "signal parameter "+d.Text+" has "+
			"mode out and cannot be read")
	case d.Class == ClassVariable && a.std < token.Std2008:
		a.semaError(e.Node.Span(), "parameter "+d.Text+" has mode out "+
			"and cannot be read before VHDL-2008")
	default:
		return false
	}
	return true
}

// derefRead checks the target e of an assignment that goes through an
// access value (x.all, or an implicit dereference x(i), x.f): the
// access value itself is read (LRM 8.3), so it is checked as a read.
func (a *analyzer) derefRead(e *Expr) {
	for x := e; x != nil; x = x.Prefix {
		switch x.Kind {
		case ExprDeref:
			a.checkReads(x.Prefix, 0)
			return
		case ExprIndex, ExprSlice, ExprSelect:
			if p := x.Prefix; p != nil && !p.Type.Unknown() &&
				p.Type.Base().Class == TypeAccess {
				a.checkReads(p, 0)
				return
			}
		default:
			return
		}
	}
}

// conditionReads checks the reads of a condition like those of any
// other expression (types.go checkReads).
func (a *analyzer) conditionReads(e *Expr) {
	if !a.mapActual {
		a.checkReads(e, 0)
	}
	a.exprRules(e) // callrules.go
}
