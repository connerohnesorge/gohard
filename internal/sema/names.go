package sema

// names.go resolves names (LRM 8): simple names, selected and expanded
// names, indexed names and calls, slices, attribute names and external
// names. It is the extension point for name semantics; overload
// resolution itself is overload.go's, expression typing types.go's.

import (
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/token"
)

// named is what a name denotes before it is used as a value.
type named struct {
	// decls are the declarations the name denotes: one, or a set of
	// overloadable ones whose choice is left to the caller (refs for
	// such a set are recorded by whoever resolves it).
	decls []*Decl
	// tok is the designator token of the name's last part.
	tok cst.Token
	// expr is set when the name denotes a value: an object, an
	// element, slice or index of one, a call's result, an attribute's
	// value. For a protected-type method it is the object.
	expr *Expr
	// typ is set when the name denotes a type or subtype.
	typ *Type
	// lib is set when the name denotes a library.
	lib *Library
	// rng is set for the range attributes ('RANGE, 'REVERSE_RANGE).
	rng *RangeCons
	// view is set when the name denotes a VHDL-2019 mode view (a view,
	// an alias of one, or V'CONVERSE; modeview.go).
	view *View
	// unknown means nothing more can be said and any error has been
	// reported: callers stay quiet.
	unknown bool
}

func unknownNamed() named { return named{unknown: true} }

// resolveName resolves the name n. kind is how the occurrence uses
// what it denotes (for the refs it records); want is the expected type
// of a call or value, nil if none.
func (a *analyzer) resolveName(n cst.Node, kind RefKind, want *Type,
) named {
	switch n.Kind() {
	case cst.KindSimpleName:
		return a.simpleName(cst.SimpleName{Node: n}.Ident(), kind)
	case cst.KindOperatorSymbol:
		return a.simpleName(cst.OperatorSymbol{Node: n}.Symbol(), kind)
	case cst.KindCharacterName:
		return a.simpleName(cst.CharacterName{Node: n}.Literal(), kind)
	case cst.KindSelectedName:
		return a.selectedName(cst.SelectedName{Node: n}, kind)
	case cst.KindCallOrIndexName:
		return a.callOrIndex(cst.CallOrIndexName{Node: n}, kind, want)
	case cst.KindSliceName:
		return a.sliceName(cst.SliceName{Node: n}, kind)
	case cst.KindAttributeName:
		return a.attributeName(cst.AttributeName{Node: n}, want)
	case cst.KindExternalName:
		return a.externalName(cst.ExternalName{Node: n})
	case cst.KindErrorNode, 0:
		return unknownNamed()
	}
	// Not a name by syntax (a parenthesized expression, a literal):
	// type it as a value.
	return named{expr: a.expr(n, want)}
}

// simpleName resolves an identifier, operator symbol or character
// literal by visibility.
// directlyDeclared reports whether a region around the current one
// declares k itself (not through a use clause): a declaration hides
// such a homograph from its own start (LRM 12.3), while use-visible
// ones stay visible in it.
func (a *analyzer) directlyDeclared(k Key) bool {
	for r := a.cur; r != nil; r = r.Parent {
		if len(r.Lookup(k)) > 0 {
			return true
		}
		for _, inc := range r.Includes {
			if len(inc.Lookup(k)) > 0 {
				return true
			}
		}
	}
	return false
}

func (a *analyzer) simpleName(tok cst.Token, kind RefKind) named {
	k := TokenKey(tok)
	if k == "" {
		return unknownNamed()
	}
	if a.declaring[k] && a.directlyDeclared(k) {
		// LRM 12.3: a declaration is not visible within itself, and it
		// already hides an outer homograph there.
		if !a.quiet {
			a.semaError(tok.Span(), tok.Text()+" is used in its own "+
				"declaration")
		}
		return unknownNamed()
	}
	r := a.lookup(k)
	if len(r.Decls) == 0 {
		if len(r.Conflict) > 0 {
			a.conflict(tok, r.Conflict)
		} else {
			a.undefined(tok)
		}
		return unknownNamed()
	}
	return a.denote(r.Decls, tok, kind)
}

// denote turns visible declarations into what the name means and
// records the reference of a non-overloaded name.
func (a *analyzer) denote(ds []*Decl, tok cst.Token, kind RefKind,
) named {
	if len(ds) == 0 {
		return unknownNamed()
	}
	d := ds[0]
	if len(ds) > 1 || d.Overloadable() {
		return named{decls: ds, tok: tok}
	}
	x := d.Denotes()
	if x.Kind.IsTypeMark() || x.View != nil {
		kind = RefType
	}
	a.ref(tok, d, kind)
	switch {
	case x.View != nil && x.Mode != ModeView:
		// A view declaration or an alias of a view (a view port has a
		// View too, and is an object).
		return named{decls: ds, tok: tok, view: x.View}
	case x.Kind == DeclLibrary:
		l := a.s.libs[x.Name]
		if l == nil && a.s.base != nil {
			l = a.s.base.libs[x.Name]
		}
		return named{decls: ds, tok: tok, lib: l}
	case x.Kind.IsTypeMark():
		t := d.Type
		if t == nil {
			t = UnknownType
		}
		return named{decls: ds, tok: tok, typ: t}
	case x.Kind.IsObject() || x.Kind == DeclUnit ||
		(d.Kind == DeclAlias && d.Value != nil) ||
		(x.Kind == DeclAlias && x.Target == nil):
		return named{decls: ds, tok: tok, expr: &Expr{Kind: ExprName,
			Node: tok.Parent(), Decl: d, Type: orUnknown(d.Type)}}
	}
	return named{decls: ds, tok: tok}
}

func orUnknown(t *Type) *Type {
	if t == nil {
		return UnknownType
	}
	return t
}

// undefined reports sema/undefined for tok.
func (a *analyzer) undefined(tok cst.Token) {
	if a.quiet || tok.IsZero() || tok.Missing() {
		return
	}
	a.report(tok.Span(), diag.New(diag.SemaUndefined, tok.Text()))
}

// conflict reports a name made invisible by LRM 12.4 rule c.
func (a *analyzer) conflict(tok cst.Token, ds []*Decl) {
	if a.quiet {
		return
	}
	var from []string
	for _, d := range ds {
		w := d.Text
		if d.Parent != nil && d.Parent.Owner != nil {
			w = d.Parent.Owner.Text + "." + d.Text
		}
		from = append(from, w)
	}
	a.semaError(tok.Span(), tok.Text()+" is not visible: use clauses "+
		"make several declarations visible ("+strings.Join(from, ", ")+
		")")
}

// selectedName resolves prefix.suffix: an expanded name (library unit,
// package item, item of an enclosing construct), a record element, a
// dereference (.all) or a protected-type method.
func (a *analyzer) selectedName(sel cst.SelectedName, kind RefKind,
) named {
	suffix := sel.Suffix()
	p := a.resolveName(sel.Prefix().Node, RefSelect, nil)
	if p.unknown {
		return unknownNamed()
	}
	sk := TokenKey(suffix)
	if sk == "" {
		return unknownNamed()
	}
	switch {
	case p.lib != nil:
		if suffix.Kind() == token.KwAll {
			return unknownNamed() // only in use clauses
		}
		u := a.libUnit(p.lib, sk, suffix)
		if u == nil {
			return unknownNamed()
		}
		return a.denote([]*Decl{u.Decl}, suffix, kind)
	case p.expr != nil:
		return a.selectInValue(p.expr, suffix, sk, kind)
	case p.typ != nil:
		return unknownNamed()
	case len(p.decls) > 0 && p.decls[0].Overloadable():
		// f.x with f a parameterless function returning a record.
		v := a.value(p, sel.Prefix().Node, nil)
		return a.selectInValue(v, suffix, sk, kind)
	case len(p.decls) == 1:
		x := p.decls[0].Denotes()
		r := x.Region
		if r == nil && x.Kind == DeclPackageBody && x.Spec != nil {
			r = x.Spec.Region
		}
		if r == nil {
			return unknownNamed()
		}
		ds := lookupIn(r, sk, a.std)
		if len(ds) == 0 {
			if !a.quiet {
				a.semaError(suffix.Span(), suffix.Text()+
					" is not declared in "+x.Text)
			}
			return unknownNamed()
		}
		return a.denote(ds, suffix, kind)
	}
	return unknownNamed()
}

// selectInValue selects suffix from the value v: a record element
// (through an access value too), .all, or a protected-type method.
func (a *analyzer) selectInValue(v *Expr, suffix cst.Token, sk Key,
	kind RefKind,
) named {
	t := v.Type
	if t.Unknown() {
		return named{expr: unknownExpr(suffix.Parent()), unknown: true}
	}
	b := t.Base()
	if suffix.Kind() == token.KwAll {
		if b.Class != TypeAccess {
			a.notA(suffix, v, "an access value")
			return unknownNamed()
		}
		return named{expr: &Expr{Kind: ExprDeref, Node: suffix.Parent(),
			Prefix: v, Type: orUnknown(allocated(b.Designated))}}
	}
	if b.Class == TypeAccess && b.Designated != nil {
		v = &Expr{Kind: ExprDeref, Node: v.Node, Prefix: v,
			Type: allocated(b.Designated)}
		b = b.Designated.Base()
	}
	switch b.Class {
	case TypeRecord:
		for _, f := range b.Fields {
			if f.Name == sk {
				a.ref(suffix, f, kind)
				return named{decls: []*Decl{f}, tok: suffix,
					expr: &Expr{Kind: ExprSelect, Node: suffix.Parent(),
						Prefix: v, Decl: f,
						Type: orUnknown(v.Type.FieldType(f))}}
			}
		}
		if !a.quiet {
			a.semaError(suffix.Span(), t.Name()+" has no element "+
				suffix.Text())
		}
		return unknownNamed()
	case TypeProtected:
		ds := lookupIn(b.Members, sk, a.std)
		if len(ds) == 0 {
			if !a.quiet {
				a.semaError(suffix.Span(), t.Name()+" has no method "+
					suffix.Text())
			}
			return unknownNamed()
		}
		return named{decls: ds, tok: suffix, expr: v}
	case TypeGeneric, TypeIncomplete, TypeUnknown:
		return named{expr: unknownExpr(suffix.Parent()), unknown: true}
	}
	a.notA(suffix, v, "a record")
	return unknownNamed()
}

func (a *analyzer) notA(tok cst.Token, v *Expr, what string) {
	if a.quiet {
		return
	}
	a.semaError(tok.Span(), "the prefix of "+tok.Text()+" is "+
		v.Type.Name()+", not "+what)
}

// libUnit returns the primary unit k of library l as the analyzed unit
// may see it, reporting library/missing-unit when it does not exist in
// the selected standard's view.
func (a *analyzer) libUnit(l *Library, k Key, tok cst.Token) *Unit {
	u, ok := l.Unit(k)
	if !ok {
		a.missingUnit(l, k, tok)
		return nil
	}
	if !a.s.readable(a.u, u) {
		return nil // an import cycle, reported by libs.go
	}
	if u.Decl == nil || !visibleIn(u.Decl, a.std) {
		if u.Decl != nil { // hidden by the standard's view (#33)
			a.missingUnit(l, k, tok)
		}
		return nil
	}
	return u
}

// callOrIndex resolves prefix(args): a call, an indexed name, a slice
// (prefix(range)), a type conversion or an index-constrained subtype.
func (a *analyzer) callOrIndex(c cst.CallOrIndexName, kind RefKind,
	want *Type,
) named {
	pn := c.Prefix().Node
	args := c.Args()
	if !c.GenericMap().IsZero() || !c.ParameterMap().IsZero() {
		// VHDL-2019 generic maps on calls (v1.1, #18): unsupported.go
		// reports the construct; only the names are resolved here.
		a.lenientName(pn)
		a.lenientArgs(c.GenericMap().List())
		a.lenientArgs(c.ParameterMap().List())
		a.lenientArgs(args)
		return unknownNamed()
	}
	p := a.resolveName(pn, kind, nil)
	switch {
	case p.unknown:
		a.lenientArgs(args)
		return unknownNamed()
	case p.typ != nil:
		if allRanges(args) {
			return named{typ: a.indexSubtype(p.typ, args)}
		}
		return named{expr: a.conversion(c.Node, p.typ, args)}
	case len(p.decls) > 0 && p.decls[0].Overloadable():
		if z := a.indexedResult(p.decls, args, want); z != nil {
			o := &Overload{Name: p.tok.Text(), Chosen: z}
			for _, d := range p.decls {
				o.Candidates = append(o.Candidates,
					Candidate{Decl: d, Fits: d == z})
			}
			a.refOverload(p.tok, o, RefCall)
			fc := &Expr{Kind: ExprCall, Node: pn, Decl: z,
				Type: z.Denotes().Type, Overload: o, Prefix: p.expr}
			return named{expr: a.index(c.Node, fc, args)}
		}
		return named{expr: a.call(c.Node, p, args, want, false)}
	case p.expr != nil:
		return named{expr: a.index(c.Node, p.expr, args)}
	}
	a.lenientArgs(args)
	return unknownNamed()
}

// indexedResult decides f(args) between a call of f with args and an
// index or slice of the result of a parameterless f (LRM 9.3.4: the
// prefix of an indexed name may be a function call without actuals).
// It returns the parameterless function to call when only that reading
// fits: no overload taking the arguments returns the expected type,
// and exactly one parameterless overload returns an array (or an
// access to one) whose element or slice has the expected type and
// whose dimensions match the arguments. Otherwise it returns nil and
// the overload resolution of a call decides (overload.go), which also
// tries the index reading when no call fits.
func (a *analyzer) indexedResult(ds []*Decl, args cst.AssociationList,
	want *Type,
) *Decl {
	n, ranges := 0, allRanges(args)
	for item := range args.Items() {
		if !item.Formal().IsZero() {
			return nil // named association: a call
		}
		n++
	}
	if n == 0 {
		return nil
	}
	var pick *Decl
	fits := 0
	for _, d := range ds {
		x := d.Denotes()
		if x.Kind != DeclFunction && x.Kind != DeclGenericSubprogram {
			continue
		}
		if !ranges && takes(x, n) && (want.Unknown() ||
			valueFits(d, want)) {
			return nil // the call reading fits
		}
		if required(x) > 0 || x.Type == nil {
			continue
		}
		at := x.Type
		if b := at.Base(); b.Class == TypeAccess && b.Designated != nil {
			at = b.Designated
		}
		if at.Base().Class != TypeArray {
			continue
		}
		var got *Type
		switch {
		case ranges && n == 1 && at.Dims() == 1:
			got = at
		case !ranges && at.Dims() == n:
			got = at.ElemType()
		default:
			continue
		}
		if want.Unknown() || Compatible(orUnknown(got), want) {
			pick = d
			fits++
		}
	}
	if fits != 1 {
		return nil
	}
	return pick
}

// takes reports whether subprogram x accepts n positional actuals.
func takes(x *Decl, n int) bool {
	return len(x.Params) >= n && required(x) <= n
}

// required counts the parameters of x without a default value.
func required(x *Decl) int {
	r := 0
	for _, p := range x.Params {
		if p.Value == nil {
			r++
		}
	}
	return r
}

// lenientArgs resolves the names in arguments whose callee is unknown.
func (a *analyzer) lenientArgs(args cst.AssociationList) {
	for item := range args.Items() {
		if v := item.Actual(); !v.IsZero() {
			a.expr(v.Node, nil)
		}
	}
}

// allRanges reports whether every argument is a discrete range, so
// prefix(args) is an index constraint (or a slice).
func allRanges(args cst.AssociationList) bool {
	n := 0
	for item := range args.Items() {
		n++
		if !item.Formal().IsZero() || !isRangeSyntax(item.Actual().Node) {
			return false
		}
	}
	return n > 0
}

// isRangeSyntax reports whether n is syntactically a discrete range:
// a range expression, a subtype indication with a constraint, or a
// range attribute.
func isRangeSyntax(n cst.Node) bool {
	switch n.Kind() {
	case cst.KindRangeExpr, cst.KindSubtypeIndication:
		return true
	case cst.KindAttributeName:
		d := cst.AttributeName{Node: n}.Designator()
		k := TokenKey(d)
		return k == "range" || k == "reverse_range"
	}
	return false
}

// indexSubtype builds T(r1, r2, ...) as an index-constrained subtype.
func (a *analyzer) indexSubtype(t *Type, args cst.AssociationList) *Type {
	st := NewSubtype(t)
	if t.Unknown() {
		st = &Type{Class: TypeUnknown}
	}
	idx := t.Base().Index
	i := 0
	for item := range args.Items() {
		var w *Type
		if i < len(idx) {
			w = idx[i]
		}
		st.IndexCons = append(st.IndexCons,
			a.discreteRange(item.Actual().Node, w))
		i++
	}
	return st
}

// index types prefix(args) for an array value: an indexed name, or a
// slice when the one argument is a discrete range.
func (a *analyzer) index(n cst.Node, v *Expr, args cst.AssociationList,
) *Expr {
	t := v.Type
	b := t.Base()
	if b != nil && b.Class == TypeAccess && b.Designated != nil {
		v = &Expr{Kind: ExprDeref, Node: v.Node, Prefix: v,
			Type: allocated(b.Designated)}
		t, b = v.Type, b.Designated.Base()
	}
	if t.Unknown() || b.Class == TypeGeneric {
		a.lenientArgs(args)
		return unknownExpr(n)
	}
	if b.Class != TypeArray {
		a.lenientArgs(args)
		if !a.quiet {
			a.semaError(n.Span(), a.text(v.Node)+" is "+t.Name()+
				", which cannot be indexed")
		}
		return unknownExpr(n)
	}
	if allRanges(args) {
		var rc *RangeCons
		count := 0
		for item := range args.Items() {
			if count == 0 {
				rc = a.discreteRange(item.Actual().Node, firstIndex(b))
			} else if !a.quiet {
				// LRM 8.5: a slice names one discrete range, of a
				// one-dimensional array.
				a.semaError(item.Actual().Span(), "a slice has one "+
					"discrete range")
			}
			count++
		}
		if count == 1 && b.Dims() > 1 && !a.quiet {
			a.semaError(args.Span(), a.text(v.Node)+" has "+
				strconv.Itoa(b.Dims())+" dimensions and cannot be sliced")
		}
		a.sliceDirection(rc, t)
		st := NewSubtype(t)
		st.IndexCons = []*RangeCons{rc}
		return &Expr{Kind: ExprSlice, Node: n, Prefix: v, Range: rc,
			Type: st}
	}
	e := &Expr{Kind: ExprIndex, Node: n, Prefix: v,
		Type: orUnknown(t.ElemType())}
	i := 0
	for item := range args.Items() {
		var w *Type
		if i < len(b.Index) {
			w = b.Index[i]
		}
		an := item.Actual().Node
		x := a.operand(an, w)
		a.settle(x, w)
		if x.Kind != ExprTypeMark {
			a.expect(x, w, an)
		}
		if x.Kind == ExprTypeMark && b.Dims() == 1 {
			// X(S) with S a discrete subtype name is a slice (LRM
			// 8.5): the syntax does not tell it from an index.
			rc := &RangeCons{Node: x.Node, Attr: x, Type: x.Type}
			st := NewSubtype(t)
			st.IndexCons = []*RangeCons{rc}
			return &Expr{Kind: ExprSlice, Node: n, Prefix: v, Range: rc,
				Type: st}
		}
		if x.Kind == ExprTypeMark && !a.quiet {
			x.illTyped = true
			a.semaError(an.Span(), a.text(an)+" is a type, not a value")
		}
		e.Args = append(e.Args, x)
		i++
	}
	if i != b.Dims() && !a.quiet {
		// LRM 8.4: one index per dimension.
		a.semaError(args.Span(), a.text(v.Node)+" has "+
			strconv.Itoa(b.Dims())+" dimensions, not "+strconv.Itoa(i)+
			" indices")
	}
	return e
}

// sliceDirection reports a slice whose range runs the other way than
// its prefix's index range (LRM 8.5), when both are written ranges; a
// null slice of literal bounds is legal.
func (a *analyzer) sliceDirection(rc *RangeCons, t *Type) {
	if a.quiet || rc == nil || rc.Attr != nil || rc.Left == nil ||
		t.isDynamic() {
		return
	}
	cons := t.Constraints()
	if cons == nil || cons[0] == nil || cons[0].Attr != nil ||
		cons[0].Left == nil || cons[0].Dir == rc.Dir {
		return
	}
	if l, ok := literalInt(rc.Left); ok {
		if r, ok := literalInt(rc.Right); ok &&
			(rc.Dir == eval.To && l > r || rc.Dir == eval.Downto && l < r) {
			return
		}
	}
	a.semaError(rc.Node.Span(), "the slice runs "+dirWord(rc.Dir)+
		" but the index range of "+t.Name()+" runs "+
		dirWord(cons[0].Dir))
}

func dirWord(d eval.Dir) string {
	if d == eval.Downto {
		return "downto"
	}
	return "to"
}

// literalInt returns the value of an integer literal expression.
func literalInt(e *Expr) (int64, bool) {
	if e != nil && e.Kind == ExprCall && len(e.Args) == 1 &&
		e.Decl != nil && e.Decl.Implicit && e.Decl.Name == `"-"` {
		v, ok := literalInt(e.Args[0])
		return -v, ok
	}
	if e == nil || e.Kind != ExprLiteral ||
		e.Lit.Kind() != token.IntegerLiteral {
		return 0, false
	}
	return ParseInteger(e.Lit.Text())
}

func firstIndex(b *Type) *Type {
	if len(b.Index) > 0 {
		return b.Index[0]
	}
	return nil
}

func (a *analyzer) sliceName(s cst.SliceName, kind RefKind) named {
	p := a.resolveName(s.Prefix().Node, kind, nil)
	var v *Expr
	switch {
	case p.expr != nil:
		v = p.expr
	case len(p.decls) > 0 && p.decls[0].Overloadable():
		v = a.value(p, s.Prefix().Node, nil)
	case p.typ != nil:
		// T(range) as a subtype is a CallOrIndexName; a slice of a
		// type is meaningless.
		a.discreteRange(sliceRange(s), firstIndex(p.typ.Base()))
		return named{typ: p.typ}
	default:
		a.discreteRange(sliceRange(s), nil)
		return unknownNamed()
	}
	t := v.Type
	b := t.Base()
	if b != nil && b.Class == TypeAccess && b.Designated != nil {
		v = &Expr{Kind: ExprDeref, Node: v.Node, Prefix: v,
			Type: allocated(b.Designated)}
		t, b = v.Type, b.Designated.Base()
	}
	var w *Type
	if b != nil && b.Class == TypeArray {
		w = firstIndex(b)
	}
	rc := a.discreteRange(sliceRange(s), w)
	if t.Unknown() {
		return named{expr: unknownExpr(s.Node), unknown: true}
	}
	if b.Class != TypeArray && b.Class != TypeGeneric {
		if !a.quiet {
			a.semaError(s.Node.Span(), a.text(v.Node)+" is "+t.Name()+
				", which cannot be sliced")
		}
		return named{expr: unknownExpr(s.Node), unknown: true}
	}
	a.sliceDirection(rc, t)
	st := NewSubtype(t)
	st.IndexCons = []*RangeCons{rc}
	return named{expr: &Expr{Kind: ExprSlice, Node: s.Node, Prefix: v,
		Range: rc, Type: st}}
}

// attrClass is the kind of result a predefined attribute has.
type attrClass uint8

const (
	attrValue    attrClass = iota + 1 // a value of a computed type
	attrType                          // a type ('BASE, 'SUBTYPE, ...)
	attrRange                         // a range ('RANGE)
	attrSignal                        // a signal or signal value
	attrName                          // a string ('PATH_NAME, ...)
	attrFunction                      // a function of an argument
)

// predefinedAttrs are the predefined attributes of LRM 16.2 (VHDL-2019
// included; the VHDL-2019 'REFLECT is v1.1, #18).
var predefinedAttrs = map[Key]attrClass{
	"left": attrValue, "right": attrValue, "high": attrValue,
	"low": attrValue, "length": attrValue, "ascending": attrValue,
	"image": attrFunction, "value": attrFunction, "pos": attrFunction,
	"val": attrFunction, "succ": attrFunction, "pred": attrFunction,
	"leftof": attrFunction, "rightof": attrFunction,
	"base": attrType, "subtype": attrType, "element": attrType,
	"designated_subtype": attrType, "index": attrType,
	"range": attrRange, "reverse_range": attrRange,
	"delayed": attrSignal, "stable": attrSignal, "quiet": attrSignal,
	"transaction": attrSignal, "event": attrSignal,
	"active": attrSignal, "last_event": attrSignal,
	"last_active": attrSignal, "last_value": attrSignal,
	"driving": attrSignal, "driving_value": attrSignal,
	"simple_name": attrName, "path_name": attrName,
	"instance_name": attrName, "converse": attrType,
	"reflect": attrValue, "record": attrType, "signal": attrSignal,
}

// attributeName resolves prefix'designator[(arg)].
func (a *analyzer) attributeName(an cst.AttributeName, want *Type,
) named {
	dtok := an.Designator()
	dk := TokenKey(dtok)
	pn := an.Prefix().Node
	p := a.resolveName(pn, RefRead, nil)
	if sig := an.Signature(); !sig.IsZero() && len(p.decls) > 0 {
		pick := a.matchSignature(p.decls, sig)
		if len(pick) > 0 {
			a.ref(p.tok, pick[0], RefRead)
			p.decls = pick[:1]
		}
	}
	if dk == "converse" {
		return a.converse(an, p)
	}
	class, predef := predefinedAttrs[dk]
	if !predef {
		return a.userAttribute(an, p, dtok)
	}
	argNode := attrArg(an)
	pt := p.typ
	var pv *Expr
	switch {
	case p.expr != nil:
		pv = p.expr
		pt = pv.Type
	case len(p.decls) > 0 && p.decls[0].Overloadable() &&
		class != attrName:
		pv = a.value(p, pn, nil)
		pt = pv.Type
	}
	if pv != nil && !pt.Unknown() && pt.Base().Class == TypeAccess &&
		pt.Base().Designated != nil {
		// prefix'attr of an access value is the attribute of the
		// designated object (LRM 8.1, implicit dereference).
		pt = pt.Base().Designated
		pv = &Expr{Kind: ExprDeref, Node: pv.Node, Prefix: pv, Type: pt}
	}
	e := &Expr{Kind: ExprAttr, Node: an.Node, Attr: dk, Prefix: pv,
		Type: UnknownType}
	if pt != nil && pv == nil {
		e.Prefix = &Expr{Kind: ExprTypeMark, Node: pn, Type: pt}
	}
	arg := func(w *Type) {
		if argNode.IsZero() {
			return
		}
		e.Args = append(e.Args, a.expr(argNode, w))
	}
	isArray := pt != nil && !pt.Unknown() && pt.Base().Class == TypeArray
	dimension := func() {
		// LRM 16.2.3: the dimension is a static integer from 1 to
		// the array's dimensionality.
		if len(e.Args) != 1 || !isArray || a.quiet {
			return
		}
		if n, ok := literalInt(e.Args[0]); ok &&
			(n < 1 || n > int64(pt.Dims())) {
			a.semaError(argNode.Span(), "dimension "+
				strconv.FormatInt(n, 10)+" of '"+dtok.Text()+" is not "+
				"one of the "+strconv.Itoa(pt.Dims())+" dimensions of "+
				pt.Name())
		}
	}
	switch class {
	case attrRange:
		arg(UniversalInteger)
		dimension()
		rc := &RangeCons{Node: an.Node, Attr: e, Type: UnknownType}
		if isArray {
			rc.Type = orUnknown(firstIndex(pt.Base()))
		} else if pt != nil {
			rc.Type = pt
		}
		return named{rng: rc, expr: e}
	case attrType:
		arg(UniversalInteger)
		switch {
		case pt == nil || pt.Unknown():
			return named{typ: UnknownType}
		case dk == "base":
			return named{typ: pt.Base()}
		case dk == "element" && pv != nil &&
			notFullyConstrained(pt.ElemType(), 0):
			return named{typ: runTime(orUnknown(pt.ElemType()))}
		case dk == "element":
			return named{typ: orUnknown(pt.ElemType())}
		case dk == "designated_subtype":
			return named{typ: orUnknown(pt.Base().Designated)}
		case dk == "index":
			return named{typ: orUnknown(firstIndex(pt.Base()))}
		case pv != nil && notFullyConstrained(pt, 0):
			// obj'SUBTYPE of an unconstrained interface object: its
			// constraint is the actual's, known at run time.
			return named{typ: runTime(pt)}
		}
		return named{typ: pt}
	case attrName:
		e.Type = orUnknown(a.stdType("string"))
		return named{expr: e}
	case attrSignal:
		a.signalPrefix(dtok, pv)
		switch dk {
		case "delayed", "stable", "quiet":
			arg(a.stdType("time"))
			// LRM 16.2.4: the time parameter is a static expression.
			if len(e.Args) == 1 && !a.quiet {
				if st, known := globallyStatic(e.Args[0], 0); known &&
					!st {
					a.semaError(argNode.Span(), "the parameter of '"+
						dtok.Text()+" must be static")
				}
			}
		default:
			arg(nil)
		}
		switch dk {
		case "stable", "quiet", "event", "active", "driving":
			e.Type = orUnknown(a.stdType("boolean"))
		case "transaction":
			e.Type = orUnknown(a.stdType("bit"))
		case "last_event", "last_active":
			e.Type = orUnknown(a.stdType("time"))
		default:
			e.Type = orUnknown(pt)
		}
		return named{expr: e}
	case attrFunction:
		// 'IMAGE, 'VALUE, 'POS, ... are attributes of a type or
		// subtype (LRM 16.2.2); VHDL-2019 adds object'IMAGE without
		// an argument.
		objectImage := a.std >= token.Std2019 && dk == "image" &&
			argNode.IsZero() && pv != nil && pv.Kind != ExprCall
		if p.typ == nil && !p.unknown && !objectImage {
			if !a.quiet {
				a.semaError(pn.Span(), "the prefix of '"+dtok.Text()+
					" must be a type, not "+a.text(pn))
			}
			arg(nil)
			e.Type = UnknownType
			if dk == "image" {
				e.Type = orUnknown(a.stdType("string"))
			}
			return named{expr: e}
		}
		switch dk {
		case "image":
			if pv != nil && argNode.IsZero() {
				// VHDL-2019 object'IMAGE.
			} else {
				arg(pt)
			}
			e.Type = orUnknown(a.stdType("string"))
		case "value":
			arg(a.stdType("string"))
			e.Type = orUnknown(pt)
		case "pos":
			arg(pt)
			e.Type = UniversalInteger
		case "val":
			arg(nil)
			e.Type = orUnknown(pt).Base()
		default:
			arg(pt)
			e.Type = orUnknown(pt).Base()
		}
		return named{expr: e}
	}
	// attrValue: 'LEFT, 'RIGHT, 'HIGH, 'LOW, 'LENGTH, 'ASCENDING.
	defer dimension()
	if pv != nil && pt != nil && !pt.Unknown() && pt.IsScalar() &&
		dk != "reflect" && !a.quiet {
		// LRM 16.2.2/16.2.3: of a scalar only the type has these
		// attributes; an object prefix must be an array.
		a.semaError(pn.Span(), "the prefix of '"+dtok.Text()+" must "+
			"be an array object or a type, not "+a.text(pn)+" of "+
			"scalar type "+pt.Name())
		arg(UniversalInteger)
		return named{expr: e}
	}
	arg(UniversalInteger)
	switch {
	case dk == "length":
		e.Type = UniversalInteger
	case dk == "ascending":
		e.Type = orUnknown(a.stdType("boolean"))
	case isArray:
		e.Type = orUnknown(firstIndex(pt.Base()))
	case pt != nil:
		e.Type = pt
	}
	return named{expr: e}
}

// signalPrefix reports a signal attribute ('EVENT, 'STABLE, ...) whose
// prefix is an object of another class (LRM 16.2.4); a prefix whose
// object is unknown (an external name, a dereference, an attribute
// result) is not checked.
func (a *analyzer) signalPrefix(dtok cst.Token, pv *Expr) {
	if a.quiet || pv == nil {
		return
	}
	d := actualObject(pv)
	if d == nil || !d.Kind.IsObject() || d.Class == ClassSignal ||
		d.Kind == DeclSignal || d.Kind == DeclPort {
		return
	}
	a.semaError(pv.Node.Span(), "the prefix of '"+dtok.Text()+
		" must be a signal, not "+d.Kind.String()+" "+d.Text)
}

// userAttribute resolves a user-defined attribute of a named entity.
func (a *analyzer) userAttribute(an cst.AttributeName, p named,
	dtok cst.Token,
) named {
	if arg := attrArg(an); !arg.IsZero() {
		a.expr(arg, nil)
	}
	r := a.lookup(TokenKey(dtok))
	for _, d := range r.Decls {
		if d.Denotes().Kind == DeclAttribute {
			a.ref(dtok, d, RefRead)
			return named{expr: &Expr{Kind: ExprAttr, Node: an.Node,
				Attr: TokenKey(dtok), Prefix: p.expr, Decl: d,
				Type: orUnknown(d.Denotes().Type)}}
		}
	}
	if !p.unknown {
		a.undefined(dtok)
	}
	return unknownNamed()
}

// externalName types <<class path : subtype>> (LRM 8.7) by its subtype
// indication: the object is only known after elaboration. The generate
// indexes of the path are typed, and the names of a package pathname
// (@lib.pkg.object) are recorded as references where the package is
// already analyzed; nothing in the path is reported, since an external
// name may legally name what this unit cannot see yet.
func (a *analyzer) externalName(en cst.ExternalName) named {
	t := a.subtypeIndication(en.Subtype())
	path := en.Path().Node
	for c := range path.Descendants() {
		if pe, ok := cst.As[cst.PathnameElement](c); ok {
			if g := pe.GenerateIndex(); !g.IsZero() {
				a.expr(g.Node, nil)
			}
		}
	}
	e := &Expr{Kind: ExprName, Node: en.Node, Type: t}
	if path.Kind() == cst.KindPackagePathname {
		if d := a.packagePathname(path); d != nil && !t.Unknown() &&
			d.Denotes().Kind.IsObject() {
			e.Decl = d
		}
	}
	return named{expr: e}
}

// packagePathname resolves @lib.pkg{.pkg}.object quietly and returns
// the object's declaration, or nil.
func (a *analyzer) packagePathname(path cst.Node) *Decl {
	var toks []cst.Token
	for tok := range path.TokensIn(nameTokens) {
		toks = append(toks, tok)
	}
	if len(toks) < 3 {
		return nil
	}
	lk := TokenKey(toks[0])
	if lk == Work && a.u != nil {
		lk = a.u.Library
	}
	l := a.s.libs[lk]
	if l == nil && a.s.base != nil {
		l = a.s.base.libs[lk]
	}
	if l == nil {
		return nil
	}
	if l.Decl != nil {
		a.ref(toks[0], l.Decl, RefSelect)
	}
	u, ok := l.Unit(TokenKey(toks[1]))
	if !ok || u.Decl == nil || !a.s.readable(a.u, u) ||
		!visibleIn(u.Decl, a.std) {
		return nil
	}
	d := u.Decl
	a.ref(toks[1], d, RefSelect)
	for i, tok := range toks[2:] {
		r := d.Denotes().Region
		if r == nil || !isPackageKind(d.Denotes().Kind) {
			return nil
		}
		ds := lookupIn(r, TokenKey(tok), a.std)
		if len(ds) != 1 {
			return nil
		}
		d = ds[0]
		kind := RefSelect
		if i == len(toks)-3 {
			kind = RefRead
		}
		a.ref(tok, d, kind)
	}
	return d
}
