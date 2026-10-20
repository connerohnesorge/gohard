package sema

// names.go resolves names (LRM 8): simple names, selected and expanded
// names, indexed names and calls, slices, attribute names and external
// names. It is the extension point for name semantics; overload
// resolution itself is overload.go's, expression typing types.go's.

import (
	"slices"
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
	if a.declaring[k] && a.directlyDeclared(k) && !a.s.cfg.Lenient {
		// LRM 12.3: a declaration is not visible within itself, and it
		// already hides an outer homograph there. Lenient analysis
		// (GHDL -frelaxed, NVC --relaxed) lets the outer homograph be
		// seen there instead.
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
			// L.all only ends a use clause (LRM 12.4), which
			// useName resolves without coming here.
			if !a.quiet {
				a.semaError(sel.Span(), a.text(sel.Node)+" denotes "+
					"no named entity: library "+p.tok.Text()+
					" cannot be selected by all here")
			}
			return unknownNamed()
		}
		u := a.libUnit(p.lib, sk, suffix)
		if u == nil {
			return unknownNamed()
		}
		return a.denote([]*Decl{u.Decl}, suffix, kind)
	case p.expr != nil && len(p.decls) > 0 &&
		p.decls[0].Overloadable() && a.enclosing(p.decls) == nil:
		// o.f.x with f a parameterless method of the protected
		// object o returning a record: f is called (LRM 8.3, 4.5.2).
		v := a.value(p, sel.Prefix().Node, nil)
		return a.selectInValue(v, suffix, sk, kind)
	case p.expr != nil:
		return a.selectInValue(p.expr, suffix, sk, kind)
	case p.typ != nil:
		// LRM 8.3: the prefix of an expanded name denotes a library,
		// a design unit or an enclosing construct, and that of a
		// selected element an object or function call; a type mark
		// is neither.
		if !a.quiet && !p.typ.Unknown() {
			a.semaError(sel.Span(), "type "+a.text(sel.Prefix().Node)+
				" cannot be the prefix of a selected name")
		}
		return unknownNamed()
	case a.enclosing(p.decls) != nil:
		// f.x with f an enclosing subprogram is an expanded name
		// (LRM 8.3).
		x := a.enclosing(p.decls)
		ds := lookupIn(x.Region, sk, a.std)
		if len(ds) == 0 {
			if !a.quiet {
				a.semaError(suffix.Span(), suffix.Text()+
					" is not declared in "+x.Text)
			}
			return unknownNamed()
		}
		return a.denote(ds, suffix, kind)
	case len(p.decls) > 0 && p.decls[0].Overloadable():
		// f.x with f a parameterless function returning a record.
		v := a.value(p, sel.Prefix().Node, nil)
		return a.selectInValue(v, suffix, sk, kind)
	case len(p.decls) == 1:
		x := p.decls[0].Denotes()
		if a.uninstantiatedPrefix(x, sel) { // pkginst.go
			return unknownNamed()
		}
		r := x.Region
		if x.Kind == DeclLabel {
			// LRM 8.3: an expanded name whose prefix denotes a
			// statement is only allowed within that statement.
			in := a.within(x)
			if in == nil {
				if !a.quiet {
					a.semaError(sel.Span(), "the expanded name "+
						a.text(sel.Node)+" is only allowed within "+
						x.Text)
				}
				return unknownNamed()
			}
			r = in
		}
		if r == nil && x.Kind == DeclPackageBody && x.Spec != nil {
			r = x.Spec.Region
		}
		if r == nil {
			a.attrDeclPrefix(p, sel.Prefix().Node, "selected")
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
	a.attrDeclPrefix(p, sel.Prefix().Node, "selected")
	return unknownNamed()
}

// signatureMatched reports the signature sig of an alias that matches
// none of the subprograms and literals r names (LRM 4.5.3, 6.6.3).
func (a *analyzer) signatureMatched(sig cst.Signature, r named,
	pick []*Decl,
) {
	if a.quiet || len(pick) > 0 || r.unknown || len(r.decls) == 0 ||
		!r.decls[0].Overloadable() {
		return
	}
	a.semaError(sig.Span(), "no visible subprogram or enumeration "+
		"literal "+r.tok.Text()+" matches the signature "+a.text(sig.Node))
}

// subprogramDefault resolves the default `is name` of the interface
// subprogram g: name denotes a visible subprogram that conforms to g's
// profile (LRM 6.5.4, 6.5.6.2). A profile with a generic or unknown
// type is not matched.
func (a *analyzer) subprogramDefault(n cst.Node, g *Decl) {
	r := a.resolveName(n, RefRead, nil)
	if a.quiet || r.unknown {
		return
	}
	if len(r.decls) == 0 || !r.decls[0].Overloadable() {
		a.semaError(n.Span(), a.text(n)+" is not a subprogram")
		return
	}
	var want []*Type
	for _, p := range profileParams(g) {
		want = append(want, p.Type)
	}
	rt := resultType(g)
	for _, t := range append(slices.Clip(want), rt) {
		if t != nil && (t.Unknown() || t.Base().Class == TypeGeneric) {
			return
		}
	}
	for _, d := range r.decls {
		x := d.Denotes()
		if x.Kind == DeclEnumLiteral {
			continue
		}
		ps := profileParams(x)
		xr := resultType(x)
		if len(ps) != len(want) || (xr == nil) != (rt == nil) ||
			(rt != nil && !SameBase(xr, rt) && !xr.Unknown()) {
			continue
		}
		ok := true
		for i, p := range ps {
			if !SameBase(p.Type, want[i]) && !p.Type.Unknown() {
				ok = false
			}
		}
		if ok {
			a.ref(r.tok, d, RefRead)
			return
		}
	}
	a.semaError(n.Span(), "no visible subprogram "+a.text(n)+
		" matches the profile of "+g.Text)
}

// subprogramActual reports the actual v (resolved to r) of a generic
// subprogram that names no subprogram (LRM 6.5.7.2).
func (a *analyzer) subprogramActual(v cst.Node, r named) {
	if a.quiet || r.unknown || !(cst.Name{}).Accepts(v.Kind()) ||
		len(r.decls) > 0 && r.decls[0].Overloadable() {
		return
	}
	a.semaError(v.Span(), a.text(v)+" is not a subprogram: a generic "+
		"subprogram is associated with a subprogram")
}

// entityAsComponent reports whether a component instantiation may
// name an entity: the instantiated unit of LRM 11.7.1 names a
// component unless the reserved word entity precedes it, and only
// lenient analysis accepts an entity there.
func (a *analyzer) entityAsComponent() bool { return a.s.cfg.Lenient }

// stringChars reports a character of the string literal e, of the
// array type t, whose character literal of t's element type is not
// visible where e is written (LRM 9.3.2).
func (a *analyzer) stringChars(e *Expr, t *Type) {
	lit := e.Lit
	if a.quiet || e.illTyped || a.u.File.Bundled ||
		lit.Kind() != token.StringLiteral || len(lit.Text()) < 2 {
		return
	}
	elem := t.Base().Elem
	if elem == nil || elem.Base().Class != TypeEnum {
		return
	}
	body := strings.ReplaceAll(lit.Text()[1:len(lit.Text())-1], `""`,
		`"`)
	var seen [256]bool
	for i := range len(body) {
		c := body[i]
		if seen[c] {
			continue
		}
		seen[c] = true
		k := Key([]byte{'\'', c, '\''})
		ok := false
		for _, d := range a.lookup(k).Decls {
			if x := d.Denotes(); x.Kind == DeclEnumLiteral &&
				SameBase(x.Type, elem) {
				ok = true
				break
			}
		}
		if !ok {
			a.semaError(lit.Span(), "character "+string(k)+" of type "+
				elem.Name()+" is not visible here")
			return
		}
	}
}

// within returns the region around the current one that the statement
// labeled x opens, or nil when the current region is outside it.
func (a *analyzer) within(x *Decl) *Region {
	for r := a.cur; r != nil; r = r.Parent {
		if r.Owner == x || (x.Region != nil && r == x.Region) {
			return r
		}
	}
	return nil
}

// attrDeclPrefix reports a prefix naming an attribute declaration: an
// attribute name is only the designator of an attribute name or
// specification (LRM 6.7, 8.6), not a value to index or select.
func (a *analyzer) attrDeclPrefix(p named, n cst.Node, how string) {
	if a.quiet || len(p.decls) != 1 ||
		p.decls[0].Denotes().Kind != DeclAttribute {
		return
	}
	a.semaError(n.Span(), "attribute "+a.text(n)+" cannot be "+how)
}

// enclosing returns the overloadable declaration of ds whose region
// encloses the current one: the prefix of an expanded name naming an
// item of an enclosing subprogram (LRM 8.3), or nil.
func (a *analyzer) enclosing(ds []*Decl) *Decl {
	if len(ds) == 0 || !ds[0].Overloadable() {
		return nil
	}
	for r := a.cur; r != nil; r = r.Parent {
		for _, d := range ds {
			if d.Region != nil && d.Region == r {
				return d
			}
		}
	}
	return nil
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
	u, ok := l.UnitFrom(k, a.u)
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
	case p.lib != nil:
		// LRM 8.1: a library name is only the prefix of an expanded
		// name; it denotes no value to index or slice.
		a.lenientArgs(args)
		if !a.quiet {
			a.semaError(c.Span(), "library "+p.tok.Text()+
				" cannot be indexed or sliced")
		}
		return unknownNamed()
	case p.typ != nil:
		if allRanges(args) {
			return named{typ: a.indexSubtype(p.typ, args)}
		}
		return named{expr: a.conversion(c.Node, p.typ, args)}
	case len(p.decls) > 0 && p.decls[0].Overloadable():
		if a.ambiguousSlice(p.decls, args, c.Node) { // indexrules.go
			a.lenientArgs(args)
			return unknownNamed()
		}
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
		a.conversionPrefix(pn, p.expr) // indexrules.go
		return named{expr: a.index(c.Node, p.expr, args)}
	}
	a.lenientArgs(args)
	a.attrDeclPrefix(p, pn, "indexed")
	a.notIndexable(p, c.Node, "indexed") // indexrules.go
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
	a.settleIndexed(v)
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
	if cons == nil || cons[0] == nil {
		return
	}
	idx := cons[0]
	if idx.Attr != nil && idx.Attr.Kind == ExprTypeMark {
		// An index constraint by a subtype (aray1(idx)) runs as the
		// subtype's range does.
		if sr := idx.Type.ScalarRange(); sr != nil && sr.Attr == nil {
			idx = sr
		}
	}
	if idx.Attr != nil || idx.Left == nil || idx.Dir == rc.Dir {
		return
	}
	cons = []*RangeCons{idx}
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

// nthIndex is the index subtype that an array attribute with the
// dimension arguments args denotes (LRM 16.2.2, 16.2.3): that of the
// literal dimension N, else that of the first dimension.
func nthIndex(b *Type, args []*Expr) *Type {
	if len(args) == 1 {
		if n, ok := literalInt(args[0]); ok && n >= 1 &&
			n <= int64(len(b.Index)) {
			return b.Index[n-1]
		}
	}
	return firstIndex(b)
}

func (a *analyzer) sliceName(s cst.SliceName, kind RefKind) named {
	p := a.resolveName(s.Prefix().Node, kind, nil)
	var v *Expr
	switch {
	case p.lib != nil:
		a.discreteRange(sliceRange(s), nil)
		if !a.quiet {
			a.semaError(s.Span(), "library "+p.tok.Text()+
				" cannot be indexed or sliced")
		}
		return unknownNamed()
	case p.expr != nil:
		a.conversionPrefix(s.Prefix().Node, p.expr) // indexrules.go
		v = p.expr
	case len(p.decls) > 0 && p.decls[0].Overloadable():
		if a.ambiguousSliced(p.decls, s.Node) { // indexrules.go
			a.discreteRange(sliceRange(s), nil)
			return unknownNamed()
		}
		v = a.value(p, s.Prefix().Node, nil)
	case p.typ != nil:
		// T(range) as a subtype is a CallOrIndexName; a slice of a
		// type is meaningless.
		a.discreteRange(sliceRange(s), firstIndex(p.typ.Base()))
		return named{typ: p.typ}
	default:
		a.discreteRange(sliceRange(s), nil)
		a.notIndexable(p, s.Node, "sliced") // indexrules.go
		return unknownNamed()
	}
	a.settleIndexed(v)
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

// attrSince is the standard that first predefines each attribute of
// predefinedAttrs not in VHDL-1993.
var attrSince = map[Key]token.Standard{
	"subtype": token.Std2008, "element": token.Std2008,
	"designated_subtype": token.Std2019, "index": token.Std2019,
	"converse": token.Std2019, "reflect": token.Std2019,
	"record": token.Std2019, "signal": token.Std2019,
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
	class, predef := predefinedAttrs[dk]
	if s, ok := attrSince[dk]; ok && a.std < s {
		// Not yet predefined in this standard: a user-defined
		// attribute of that name (LRM 16.2 of the standard).
		predef = false
	}
	if dk == "converse" && predef {
		return a.converse(an, p)
	}
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
	if a.attrPrefix(dtok, class, p, pv, pt, pn) {
		if !argNode.IsZero() {
			a.expr(argNode, nil)
		}
		if class == attrRange {
			return named{rng: &RangeCons{Node: an.Node,
				Type: UnknownType}, unknown: true}
		}
		return unknownNamed()
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
		if st, known := locallyStatic(e.Args[0], 0, a.std); known &&
			!st && !e.Args[0].illTyped {
			a.semaError(argNode.Span(), "the dimension of '"+
				dtok.Text()+" must be locally static")
			return
		}
		if n, ok := choiceValue(e.Args[0]); ok &&
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
			rc.Type = orUnknown(nthIndex(pt.Base(), e.Args))
		} else if pt != nil {
			rc.Type = pt
		}
		return named{rng: rc, expr: e}
	case attrType:
		if dk == "subtype" || dk == "element" {
			// T'SUBTYPE(X) and T'ELEMENT(X) are type conversions:
			// the attribute denotes a subtype, usable as a type mark
			// (LRM 16.2, 9.3.6).
			t := a.typeAttr(dk, pt, pv, nil)
			if argNode.IsZero() {
				return named{typ: t}
			}
			return named{expr: a.conversion1(an.Node, t, argNode)}
		}
		arg(UniversalInteger)
		return named{typ: a.typeAttr(dk, pt, pv, e.Args)}
	case attrName:
		e.Type = orUnknown(a.stdType("string"))
		return named{expr: e}
	case attrSignal:
		a.signalPrefix(dtok, pv)
		a.implicitOfParam(dtok, string(dk), pv) // targetrules.go
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
			a.valParam(e.Args, argNode)
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
		e.Type = orUnknown(nthIndex(pt.Base(), e.Args))
	case pt != nil:
		e.Type = pt
	}
	return named{expr: e}
}

// attrPrefix reports a predefined attribute whose prefix is of the
// wrong kind (LRM 16.2) and returns whether it did:
//   - 'BASE takes a type or subtype, not an object;
//   - 'IMAGE, 'VALUE, 'POS, ... take a scalar type (16.2.2);
//   - the array attributes 'LEFT, 'LENGTH, 'RANGE, ... of an object
//     take an array object, and of a type a scalar type ('LENGTH and
//     'RANGE: an array type) or a constrained array type (16.2.3);
//   - a named entity that is neither a type nor an object (an entity,
//     an architecture, a label) has none of them.
//
// Unknown, generic and incomplete types are not judged.
func (a *analyzer) attrPrefix(dtok cst.Token, class attrClass, p named,
	pv *Expr, pt *Type, pn cst.Node,
) bool {
	if a.quiet || p.unknown || p.rng != nil {
		return false
	}
	dk := TokenKey(dtok)
	bad := func(want string) bool {
		a.semaError(pn.Span(), "the prefix of '"+dtok.Text()+
			" must be "+want+", not "+a.text(pn))
		return true
	}
	array := class == attrRange || (class == attrValue && dk != "reflect")
	if class == attrName && len(p.decls) == 0 {
		// LRM 16.2.5: the prefix denotes a named entity, which a
		// subelement, slice, call or attribute result is not.
		return bad("a named entity")
	}
	if class == attrSignal && pv == nil &&
		(pt != nil || len(p.decls) == 1) {
		// LRM 16.2.4: the prefix is a static signal name.
		return bad("a signal")
	}
	if pv == nil && pt == nil {
		if !array || len(p.decls) != 1 || p.decls[0].Overloadable() {
			return false
		}
		switch p.decls[0].Denotes().Kind {
		case DeclEntity, DeclArchitecture, DeclPackage, DeclLabel,
			DeclComponent, DeclConfiguration, DeclAttribute:
			return bad("an array object or a type")
		}
		return false
	}
	if pt.Unknown() {
		return false
	}
	switch c := pt.Base().Class; {
	case c == TypeIncomplete && pv == nil:
		// LRM 5.4.2: before its full declaration an incomplete type
		// may only be the designated type of an access type.
		return bad("a fully declared type")
	case c == TypeGeneric || c == TypeIncomplete || c == TypeUnknown:
		return false
	case dk == "base" && pv != nil:
		return bad("a type or subtype")
	case class == attrFunction && pv == nil && !pt.IsScalar():
		return bad("a scalar type")
	case !array:
		return false
	case pv != nil:
		if c != TypeArray && !(class == attrValue && pt.IsScalar()) {
			// A scalar object is reported by the caller.
			return bad("an array object")
		}
	case c == TypeArray:
		if pt.Constraints() == nil && !pt.isDynamic() {
			return bad("a constrained array type")
		}
	case c == TypeRecord || c == TypeAccess || c == TypeFile ||
		c == TypeProtected:
		return bad("an array or scalar type")
	case (dk == "length" || class == attrRange) && a.std < token.Std2019:
		return bad("an array type")
	}
	return false
}

// valParam reports a T'VAL parameter that is not of an integer type
// (LRM 16.2.2).
func (a *analyzer) valParam(args []*Expr, n cst.Node) {
	if a.quiet || len(args) != 1 || args[0].Type.Unknown() ||
		args[0].Type.Base().Class == TypeInteger {
		return
	}
	a.semaError(n.Span(), "the parameter of 'VAL must be of an integer "+
		"type, not "+args[0].Type.Name())
}

// typeAttr is the subtype a type attribute ('BASE, 'ELEMENT, 'SUBTYPE,
// 'INDEX, 'DESIGNATED_SUBTYPE) of the prefix type pt denotes; pv is
// the prefix object, if any, and args the dimension argument.
func (a *analyzer) typeAttr(dk Key, pt *Type, pv *Expr, args []*Expr,
) *Type {
	switch {
	case pt == nil || pt.Unknown():
		return UnknownType
	case dk == "base":
		return pt.Base()
	case dk == "element" && (pv != nil || pt.isDynamic()) &&
		notFullyConstrained(pt.ElemType(), 0):
		// The element subtype of an object (or of an object's
		// subtype) takes its constraint from the object.
		return runTime(orUnknown(pt.ElemType()))
	case dk == "element":
		return orUnknown(pt.ElemType())
	case dk == "designated_subtype":
		return orUnknown(pt.Base().Designated)
	case dk == "index":
		return orUnknown(nthIndex(pt.Base(), args))
	case pv != nil && notFullyConstrained(pt, 0):
		// obj'SUBTYPE of an unconstrained interface object: its
		// constraint is the actual's, known at run time.
		return runTime(pt)
	}
	return pt
}

// conversion1 types T(x), a type conversion of the single operand x.
func (a *analyzer) conversion1(n cst.Node, t *Type, x cst.Node) *Expr {
	v := a.operand(x, nil)
	if v.pend != nil {
		if s, _ := fit(v, t); s > 0 {
			a.settle(v, t)
		} else {
			a.settle(v, nil)
		}
	}
	e := &Expr{Kind: ExprConversion, Node: n, Type: t, Args: []*Expr{v}}
	if !a.quiet && !v.illTyped && !closelyRelated(v.Type, t, a.std) {
		v.illTyped, e.illTyped = true, true
		a.semaError(v.Node.Span(), "cannot convert "+v.Type.Name()+
			" to "+t.Name()+": the types are not closely related")
	}
	return e
}

// aliasSelfScope marks the designator of an object or type alias as
// being declared while its target is resolved (LRM 12.3: the alias
// hides an outer homograph from its start and is not visible in
// itself). An alias with a signature is overloadable and hides only a
// homograph of its own profile, so it is left alone.
func (a *analyzer) aliasSelfScope(ad cst.AliasDecl) func() {
	if !ad.Signature().IsZero() ||
		ad.Designator().Kind() != token.Identifier &&
			ad.Designator().Kind() != token.ExtendedIdentifier {
		return func() {}
	}
	return a.selfScopeTokens(ad.Designator())
}

// attrDeclType reports an attribute declaration whose type is an
// access, file or protected type or a composite type with an access
// subelement (LRM 6.7).
func (a *analyzer) attrDeclType(n cst.Node, d *Decl) {
	t := d.Type
	if a.quiet || a.u.File.Bundled || t.Unknown() {
		return
	}
	what := ""
	switch {
	case t.Base().Class == TypeFile:
		what = "a file type"
	case t.Base().Class == TypeProtected:
		what = "a protected type"
	case accessPart(t) == "access":
		what = "an access type"
	case accessPart(t) == "subelement":
		what = "a type with a subelement of an access type"
	default:
		return
	}
	a.semaError(n.Span(), "the type of attribute "+d.Text+
		" cannot be "+what+": "+t.Name())
}

// attrOnce records that the attribute specification of attr names d
// through ed, reporting a second one (LRM 7.2: a named entity gets a
// value of an attribute from one specification).
func (a *analyzer) attrOnce(ed cst.EntityDesignator, d, attr *Decl) {
	et := ed.Tag()
	if d != nil && attr != nil && ed.Signature().IsZero() &&
		(!d.Overloadable() || len(a.lookup(TokenKey(et)).Decls) == 1) {
		// LRM 7.2: a named entity is given a value of an attribute by
		// at most one specification.
		k := [2]*Decl{attr, d}
		if a.attrSpecs[k] {
			a.semaError(et.Span(), d.Text+" already has a value of "+
				"attribute "+attr.Text)
			return
		}
		if a.attrSpecs == nil {
			a.attrSpecs = map[[2]*Decl]bool{}
		}
		a.attrSpecs[k] = true
		if d.Unit == a.u {
			d.attrs = append(d.attrs, attr)
		}
	}
}

// attrOthers records attr as given by an attribute specification for
// others or all (k), which may name any entity of the unit.
func (a *analyzer) attrOthers(k Key, attr *Decl) {
	if attr != nil && (k == "others" || k == "all") {
		a.u.othersAttrs = append(a.u.othersAttrs, attr)
	}
}

// unspecified reports P'A where P names an object of another, already
// analyzed design unit that no attribute specification gives a value
// of the user-defined attribute A (LRM 7.2, 8.6: the attribute name
// denotes the value the specification gives). Objects of the unit
// being analyzed are not judged: their specification may follow.
func (a *analyzer) unspecified(p named, n cst.Node, attr *Decl) bool {
	if a.quiet || len(p.decls) != 1 {
		return false
	}
	d := p.decls[0]
	if !d.Kind.IsObject() || d.Unit == nil || d.Unit == a.u ||
		d.Unit.File == nil || d.Unit.File.Bundled ||
		slices.Contains(d.attrs, attr) ||
		slices.Contains(d.Unit.othersAttrs, attr) ||
		a.attrSpecs[[2]*Decl{attr, d}] {
		return false
	}
	a.semaError(n.Span(), d.Text+" has no value of attribute "+
		attr.Text+": no attribute specification gives it one")
	return true
}

// attrClasses are the entity classes of an attribute specification
// whose named entities attrEntity checks: the object, type and
// subtype classes (LRM 7.2).
var attrClasses = []struct {
	tok  token.Kind
	name string
}{
	{token.KwSignal, "signal"}, {token.KwVariable, "variable"},
	{token.KwConstant, "constant"}, {token.KwType, "type"},
	{token.KwSubtype, "subtype"},
}

// attrEntity checks the entity designator ed of an attribute
// specification of attr naming d (nil if nothing visible has its name):
// d gets a value of attr only once (attrOnce), and d agrees with the
// entity class of es (LRM 7.2): an object, type or subtype named must
// be declared, and be of that class. Labels, which may be
// declared after the specification, and the other classes are not
// checked; neither is an alias, whose class is its object's.
func (a *analyzer) attrEntity(es cst.EntitySpecification,
	ed cst.EntityDesignator, d, attr *Decl,
) {
	et := ed.Tag()
	if a.quiet {
		return
	}
	if et.Kind() == token.StringLiteral ||
		et.Kind() == token.CharacterLiteral {
		return
	}
	class := ""
	for _, c := range attrClasses {
		if !es.Node.Token(c.tok, 0).IsZero() {
			class = c.name
		}
	}
	if class == "" {
		a.attrOnce(ed, d, attr)
		return
	}
	if d == nil {
		a.undefined(et)
		return
	}
	got := ""
	switch d.Kind {
	case DeclSignal, DeclPort:
		got = "signal"
	case DeclVariable, DeclSharedVariable:
		got = "variable"
	case DeclConstant, DeclGeneric:
		got = "constant"
	case DeclType:
		got = "type"
	case DeclSubtype:
		got = "subtype"
	case DeclParameter:
		switch d.Class {
		case ClassSignal:
			got = "signal"
		case ClassVariable:
			got = "variable"
		case ClassConstant:
			got = "constant"
		}
	}
	if got != "" && got != class {
		a.semaError(et.Span(), d.Kind.String()+" "+d.Text+
			" is not of class '"+class+"'")
		return
	}
	a.attrOnce(ed, d, attr)
}

// physicalUnit reports the unit name of a physical literal that
// denotes something other than a unit of a physical type (LRM 5.2.4,
// 9.3.2): e.Decl is unset then.
func (a *analyzer) physicalUnit(n cst.Node, nm named, e *Expr) {
	if a.quiet || e.Decl != nil || nm.unknown {
		return
	}
	a.semaError(n.Span(), a.text(n)+" is not a physical unit")
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
	dk := TokenKey(dtok)
	if d != nil && (dk == "driving" || dk == "driving_value") &&
		(d.Kind == DeclPort || d.Kind == DeclParameter &&
			d.Class == ClassSignal) && d.Mode == ModeIn {
		// LRM 16.2.4: a port prefix of 'DRIVING or 'DRIVING_VALUE has
		// mode out, inout or buffer.
		a.semaError(pv.Node.Span(), "the prefix of '"+dtok.Text()+
			" must not be "+d.Text+", of mode in")
		return
	}
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
	argNode := attrArg(an)
	if a.userPrefix(p, an.Prefix().Node) {
		if !argNode.IsZero() {
			a.expr(argNode, nil)
		}
		return unknownNamed()
	}
	r := a.lookup(TokenKey(dtok))
	if len(p.decls) == 1 && len(r.Decls) == 0 {
		// lib.e'A of a design unit whose attribute A is not visible
		// here: the attribute specification in the unit gives e the
		// attribute (LRM 7.2), and P'A denotes its value (LRM 8.6).
		if x := p.decls[0].Denotes(); x.Unit != nil &&
			x.Unit.Decl == x && x.Unit != a.u {
			for _, ad := range x.Unit.unitAttrs {
				if ad.Name == TokenKey(dtok) {
					r.Decls = []*Decl{ad}
					break
				}
			}
		}
	}
	for _, d := range r.Decls {
		if d.Denotes().Kind == DeclAttribute {
			a.ref(dtok, d, RefRead)
			a.unspecified(p, an.Node, d.Denotes())
			v := &Expr{Kind: ExprAttr, Node: an.Node,
				Attr: TokenKey(dtok), Prefix: p.expr, Decl: d,
				Type: orUnknown(d.Denotes().Type)}
			if argNode.IsZero() {
				return named{expr: v}
			}
			return named{expr: a.attrIndex(an.Node, v, argNode)}
		}
	}
	if !argNode.IsZero() {
		a.expr(argNode, nil)
	}
	if !p.unknown {
		a.undefined(dtok)
	}
	return unknownNamed()
}

// userPrefix reports the prefix of a user-defined attribute name that
// does not denote a named entity (LRM 8.6, 7.2), and returns whether
// it did: a subelement, slice or function call of an object, or an
// attribute (no entity class takes attributes of an attribute).
func (a *analyzer) userPrefix(p named, n cst.Node) bool {
	if a.quiet || p.unknown {
		return false
	}
	if p.expr != nil {
		switch p.expr.Kind {
		case ExprIndex, ExprSlice, ExprSelect, ExprCall:
			if len(p.decls) == 0 || p.expr.Kind != ExprSelect ||
				p.decls[0].Denotes().Kind == DeclElement {
				a.semaError(n.Span(), "the prefix of a user-defined "+
					"attribute must be a named entity, not "+
					"the subelement, slice or call "+a.text(n))
				return true
			}
		}
		return false
	}
	if len(p.decls) == 1 && p.decls[0].Denotes().Kind == DeclAttribute {
		a.semaError(n.Span(), "the prefix of a user-defined attribute "+
			"must be a named entity with attributes, not attribute "+
			a.text(n))
		return true
	}
	return false
}

// attrIndex types P'A(X) for a user-defined attribute A: the name
// P'A denotes the attribute's value, and (X) indexes it (LRM 8.4,
// 8.6) when that value is a one-dimensional array.
func (a *analyzer) attrIndex(n cst.Node, v *Expr, x cst.Node) *Expr {
	b := v.Type.Base()
	if v.Type.Unknown() || b == nil || b.Class != TypeArray ||
		b.Dims() != 1 {
		a.attrNotArray(n, v) // indexrules.go
		a.expr(x, nil)
		return &Expr{Kind: ExprAttr, Node: n, Attr: v.Attr,
			Prefix: v.Prefix, Decl: v.Decl, Type: UnknownType}
	}
	i := a.expr(x, b.Index[0])
	return &Expr{Kind: ExprIndex, Node: n, Prefix: v,
		Args: []*Expr{i}, Type: orUnknown(v.Type.ElemType())}
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
	u, ok := l.UnitFrom(TokenKey(toks[1]), a.u)
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

// isArrayish reports whether t is an array type or an access to one.
func isArrayish(t *Type) bool {
	if t.Unknown() {
		return false
	}
	b := t.Base()
	if b.Class == TypeAccess && b.Designated != nil {
		b = b.Designated.Base()
	}
	return b.Class == TypeArray
}

// arrayResult reports whether one of ds returns an array (or an access
// to one).
func arrayResult(ds []*Decl) bool {
	for _, d := range ds {
		if isArrayish(d.Denotes().Type) {
			return true
		}
	}
	return false
}

// settleIndexed settles a pending f(x) that is the prefix of an index
// or slice name, f(x)(y): it is the reading that yields an array (LRM
// 8.4, 8.5).
func (a *analyzer) settleIndexed(v *Expr) {
	p := v.pend
	if p == nil || p.kind != pendCall || p.index == nil {
		return
	}
	if !arrayResult(p.fitting) && isArrayish(p.index.elem) {
		*v = *a.indexReading(v.Node, p.tok, p.cands, p.index, p.acts,
			p.object)
		return
	}
	*v = *a.finishCall(v.Node, p.tok, p.name, p.cands, p.acts, nil,
		false, p.object, true)
}
