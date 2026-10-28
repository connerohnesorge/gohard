package sema

// overload.go is overload resolution (LRM 4.5, 12.5) for subprogram
// calls, operators and enumeration literals, shared by `gohard check`,
// the editor and the simulator (#34). In editor mode the result keeps
// every candidate with the reason it did not fit (Overload), which feeds
// the diagnostic, hover and completion alike.

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// actual is one actual of a call or operator.
type actual struct {
	e      *Expr
	formal Key       // the formal designator of a named association
	ftok   cst.Token // its token, for the reference
	// fpart is the formal part when it names a subelement or slice of
	// the formal (v.a => x, w(1) => y), else zero.
	fpart cst.Node
	node  cst.Node
	open  bool // the actual is open
}

// fit scores how well operand e fits a formal of type t: 0 means it
// does not fit; 3 is an exact base-type match, 2 a match decided by
// context or by an unknown type, 1 an implicit conversion of a
// universal operand.
func fit(e *Expr, t *Type) (int, string) {
	if t.Unknown() {
		return 2, ""
	}
	if t.Base().Class == TypeGeneric {
		// A generic type matches itself exactly; anything else only
		// weakly (its actual is known per instance), so a concrete
		// overload wins over one of a generic type.
		if e.pend == nil && !e.Type.Unknown() &&
			e.Type.Base() == t.Base() {
			return 3, ""
		}
		return 1, ""
	}
	if p := e.pend; p != nil {
		switch p.kind {
		case pendChar, pendName:
			sub := methodSubst(e.Prefix)
			for _, c := range p.cands {
				if valueFitsSub(c, t, sub) {
					return 2, ""
				}
			}
			return 0, p.tok.Text() + " is not a value of " + t.Name()
		case pendString:
			if stringFits(e.Lit, t) {
				return 2, ""
			}
			return 0, "a string literal is not a value of " + t.Name()
		case pendAggregate:
			c := t.Base().Class
			if c == TypeArray || c == TypeRecord {
				return 1, ""
			}
			return 0, "an aggregate is not a value of " + t.Name()
		case pendNull:
			if t.Base().Class == TypeAccess {
				return 2, ""
			}
			return 0, "null is not a value of " + t.Name()
		case pendAlloc:
			b := t.Base()
			if b.Class == TypeAccess && Compatible(p.made, b.Designated) {
				return 2, ""
			}
			return 0, "an allocator of " + orUnknown(p.made).Name() +
				" is not a value of " + t.Name()
		case pendCall:
			for _, c := range p.fitting {
				if rt := c.Denotes().Type; rt != nil && Compatible(rt, t) &&
					(t.Universal || !mixedUniversal(c.Denotes())) {
					return 2, ""
				}
			}
			if p.index != nil && Compatible(p.index.elem, t) {
				return 2, ""
			}
			return 0, p.tok.Text() + " does not return " + t.Name()
		}
	}
	if e.Type.Unknown() || e.Kind == ExprOpen {
		return 2, ""
	}
	h, w := e.Type.Base(), t.Base()
	switch {
	case h == w:
		return 3, ""
	case h.Universal && h.Class == w.Class && e.Kind == ExprCall &&
		mixedUniversal(e.Decl.Denotes()):
		// GHDL and NVC convert a universal operand implicitly only
		// when it is not the result of universal_real * or /
		// universal_integer (LRM 9.3.6).
		return 0, "a mixed universal_real and universal_integer " +
			"result is not converted to " + t.Name()
	case h.Universal && h.Class == w.Class:
		return 1, ""
	case h.Class == TypeGeneric:
		return 1, ""
	case h.Class == TypeNull && w.Class == TypeAccess:
		return 2, ""
	case Compatible(e.Type, t):
		return 2, ""
	}
	return 0, "is " + e.Type.Name() + ", not " + t.Name()
}

// stringFits reports whether a string or bit-string literal can be a
// value of t: a one-dimensional array of a character type holding every
// character of the literal (LRM 9.3.2).
func stringFits(lit cst.Token, t *Type) bool {
	b := t.Base()
	if b.Class != TypeArray || len(b.Index) != 1 {
		return false
	}
	elem := b.Elem.Base()
	if elem == nil || elem.Class == TypeUnknown ||
		elem.Class == TypeGeneric {
		return true
	}
	if elem.Class != TypeEnum {
		return false
	}
	has := func(c byte) bool {
		k := Key([]byte{'\'', c, '\''})
		for _, l := range elem.Literals {
			if l.Name == k {
				return true
			}
		}
		return false
	}
	text := lit.Text()
	if lit.Kind() == token.BitStringLiteral {
		// The expanded value holds 0 and 1 (and, for the 2008 forms,
		// the graphic characters written); requiring 0 and 1 keeps
		// BIT_VECTOR, STD_ULOGIC_VECTOR and UNSIGNED.
		return has('0') && has('1')
	}
	if len(text) < 2 {
		return true
	}
	body := strings.ReplaceAll(text[1:len(text)-1], `""`, `"`)
	for i := range len(body) {
		if !has(body[i]) {
			return false
		}
	}
	return true
}

// candidate is one overload under consideration.
type candidate struct {
	d       *Decl
	score   int
	near    int // see Candidate.Near
	reason  string
	formals []*Decl // the formal of each actual
	// generic counts actuals that fit a formal of a generic type
	// only weakly (the actual is not of that type itself).
	generic int
}

// match maps the actuals to the formals of d and scores the fit.
func match(d *Decl, acts []actual, want *Type, proc bool) candidate {
	return matchSub(d, acts, want, proc, nil)
}

// matchSub is match for a method whose generic types the object binds
// as sub gives (methodsubst.go).
func matchSub(d *Decl, acts []actual, want *Type, proc bool,
	sub typeSubst,
) candidate {
	c := candidate{d: d}
	x := d.Denotes()
	isProc := x.Kind == DeclProcedure ||
		(x.Kind == DeclGenericSubprogram && x.Type == nil)
	if proc != isProc {
		if proc {
			c.reason = "is not a procedure"
		} else {
			c.reason = "is a procedure"
		}
		return c
	}
	params := profileParams(x)
	if x.Kind == DeclEnumLiteral && len(acts) > 0 {
		c.reason = "is an enumeration literal"
		return c
	}
	used := make([]bool, len(params))
	c.formals = make([]*Decl, len(acts))
	score := 1
	for i, ac := range acts {
		var p *Decl
		if ac.formal != "" {
			for j, q := range params {
				if q.Name == ac.formal {
					p = q
					used[j] = true
				}
			}
			if p == nil {
				c.reason = "has no parameter " + string(ac.formal)
				c.near = 0
				return c
			}
		} else {
			if i >= len(params) {
				c.reason = "takes " + strconv.Itoa(len(params)) +
					" arguments, not " + strconv.Itoa(len(acts))
				c.near = 0
				return c
			}
			p = params[i]
			used[i] = true
		}
		c.formals[i] = p
		if ac.open {
			continue
		}
		s, why := fit(ac.e, partType(ac.fpart, sub.of(p.Type)))
		if s == 0 {
			c.reason = "argument " + strconv.Itoa(i+1) + " " + why
			return c
		}
		if s == 1 && partType(ac.fpart, sub.of(p.Type)).Base().Class ==
			TypeGeneric {
			c.generic++
		}
		score += s
		c.near++
	}
	c.near++
	for j, q := range params {
		if !used[j] && q.Value == nil && !q.Implicit {
			c.reason = "needs a value for " + q.Text
			c.near = 0
			return c
		}
		if !used[j] && q.Implicit && q.Value == nil {
			c.reason = "takes " + strconv.Itoa(len(params)) +
				" arguments, not " + strconv.Itoa(len(acts))
			c.near = 0
			return c
		}
	}
	if !proc && want == anyComposite {
		rt := sub.of(x.Type)
		if rt == nil || (rt.Base().Class != TypeArray &&
			rt.Base().Class != TypeRecord) {
			c.reason = "does not return a composite value"
			return c
		}
	}
	if !proc && !want.Unknown() {
		rt := sub.of(x.Type)
		if rt == nil {
			c.reason = "has no result"
			return c
		}
		if !Compatible(rt, want) {
			c.reason = "returns " + rt.Name() + ", not " + want.Name()
			return c
		}
		if mixedUniversal(x) && !want.Universal {
			c.reason = "mixes universal_real and universal_integer, " +
				"which only a universal context accepts"
			return c
		}
		if SameBase(rt, want) {
			score++
		}
	}
	c.score = score
	return c
}

// choose resolves an overloaded call or operator among cands and
// returns the editor-mode result and the chosen candidate (nil when
// none or several fit).
func (a *analyzer) choose(name string, cands []*Decl, acts []actual,
	want *Type, proc bool, sub typeSubst,
) (*Overload, *candidate) {
	if pr := a.s.probe; pr != nil && pr.Overload != nil && a.choosing == 0 {
		defer pr.Overload()()
	}
	a.choosing++
	defer func() { a.choosing-- }()
	o := &Overload{Name: name}
	var all []candidate
	best := 0
	for _, d := range cands {
		c := matchSub(d, acts, want, proc, sub)
		all = append(all, c)
		cd := Candidate{Decl: d, Fits: c.score > 0, Reason: c.reason}
		if !cd.Fits {
			cd.Near = c.near
		}
		o.Candidates = append(o.Candidates, cd)
		best = max(best, c.score)
	}
	o.Candidates = sortedCandidates(o)
	if best == 0 {
		return o, nil
	}
	var top []*candidate
	for i := range all {
		if all[i].score == best {
			top = append(top, &all[i])
		}
	}
	top = concreteFirst(top)
	if len(top) == 1 || sameDenotationC(top) {
		o.Chosen = top[0].d
		return o, top[0]
	}
	// Prefer an explicit declaration over implicit homographs and, of
	// the rest, the innermost (first visible) one.
	var explicit []*candidate
	for _, c := range top {
		if !c.d.Denotes().Implicit {
			explicit = append(explicit, c)
		}
	}
	if len(explicit) == 1 && !a.useHomographs(explicit[0], top) &&
		(hidesRest(explicit[0], top) ||
			!want.Unknown() || !resultsDiffer(top)) {
		o.Chosen = explicit[0].d
		return o, explicit[0]
	}
	return o, nil
}

// hidesRest reports whether the explicit candidate x is a homograph of
// every other top candidate, so it hides those implicit declarations
// (LRM 12.3); an implicit operation with another result type is a
// different overload that only the context can rule out.
func hidesRest(x *candidate, top []*candidate) bool {
	for _, c := range top {
		if c != x && !homographs(x.d.Denotes(), c.d.Denotes()) {
			return false
		}
	}
	return true
}

// resultsDiffer reports whether the candidates return different base
// types, so a context still to come may choose among them (LRM 12.5).
func resultsDiffer(cs []*candidate) bool {
	for _, c := range cs[1:] {
		x, y := cs[0].d.Denotes().Type, c.d.Denotes().Type
		if (x == nil) != (y == nil) || x != nil && !SameBase(x, y) {
			return true
		}
	}
	return false
}

// sameDenotationC reports whether the candidates are one subprogram:
// the same declaration reached by different paths, or a declaration
// and its completion (homographs declared in one region). Homographs
// from different packages made visible by use clauses are different
// subprograms: a call that fits both is ambiguous (LRM 12.4, 12.5).
func sameDenotationC(cs []*candidate) bool {
	x := cs[0].d.Denotes()
	for _, c := range cs[1:] {
		y := c.d.Denotes()
		if y != x && !(y.Parent == x.Parent && homographs(x, y)) {
			return false
		}
	}
	return true
}

// certain reports whether every actual's type is known, so a failed or
// ambiguous resolution is the design's fault and not sema's.
func certain(acts []actual, want *Type) bool {
	for _, ac := range acts {
		if ac.open {
			continue
		}
		e := ac.e
		if p := e.pend; p != nil {
			switch {
			case p.kind == pendAggregate:
				// An aggregate's type comes only from the context
				// (LRM 9.3.3.1): several fitting overloads are a real
				// ambiguity.
				continue
			case p.kind == pendCall && !certain(p.acts, nil):
				// A call still open because an operand of its own is
				// unknown: its result type is unknown too.
				return false
			}
			continue
		}
		if e.Type.Unknown() || e.Type.Base().Class == TypeGeneric {
			return false
		}
	}
	return true
}

// brokenUses reports whether a use clause in effect here named
// nothing, so the visible overloads may be incomplete.
func (a *analyzer) brokenUses() bool {
	for r := a.cur; r != nil; r = r.Parent {
		for _, u := range r.Uses {
			if u.Broken {
				return true
			}
		}
	}
	return false
}

// operator resolves a predefined or user-defined operator.
func (a *analyzer) operator(n cst.Node, op cst.Token,
	operands []cst.Node, want *Type,
) *Expr {
	acts := make([]actual, len(operands))
	for i, x := range operands {
		acts[i] = actual{e: a.operand(x, nil), node: x}
		a.typeMarkValue(acts[i].e, x)
		a.rangeOperand(acts[i].e, x) // typerules.go
	}
	if len(operands) == 1 && a.std < token.Std2008 &&
		isLogical(op.Kind()) {
		// A unary reduction before VHDL-2008: the parser reported
		// syntax/standard; resolving it would only repeat that.
		saved := a.quiet
		a.quiet = true
		defer func() { a.quiet = saved }()
	}
	key := OperatorKey(op.Text())
	r := a.lookup(key)
	var cands []*Decl
	for _, d := range r.Decls {
		if x := d.Denotes(); x.Kind.IsSubprogram() ||
			x.Kind == DeclGenericSubprogram {
			cands = append(cands, d)
		}
	}
	return a.finishCall(n, op, op.Text(), cands, acts, want, false, nil,
		false)
}

// call resolves prefix(args) where the prefix denotes overloadable
// declarations (nm.decls); with no args it is a parameterless call or
// an enumeration literal. proc selects procedures (procedure call
// statements).
func (a *analyzer) call(n cst.Node, nm named, args cst.AssociationList,
	want *Type, proc bool,
) *Expr {
	if !proc {
		if e := a.indexedCall(n, nm, args); e != nil {
			return e
		}
	}
	var acts []actual
	for item := range args.Items() {
		ac := actual{node: item.Actual().Node}
		if f := item.Formal(); !f.IsZero() {
			ac.ftok = leftmostToken(f.Node)
			ac.formal = TokenKey(ac.ftok)
			if f.Kind() != cst.KindSimpleName {
				ac.fpart = f.Node
				a.lenientFormal(f.Node)
			}
		}
		if ac.node.Kind() == cst.KindOpenExpr {
			ac.open = true
			ac.e = &Expr{Kind: ExprOpen, Node: ac.node,
				Type: UnknownType}
		} else {
			ac.e = a.operand(ac.node, nil)
			a.typeMarkValue(ac.e, ac.node)
		}
		acts = append(acts, ac)
	}
	a.positionalAfterNamed(acts) // typecheck.go
	sub := methodSubst(nm.expr)
	if len(acts) == 0 && !proc {
		// A value: an enumeration literal or a parameterless function
		// whose type the context may decide.
		var fits []*Decl
		for _, d := range nm.decls {
			x := d.Denotes()
			if x.Kind == DeclEnumLiteral || match(d, nil, nil,
				false).score > 0 {
				if want.Unknown() || valueFitsSub(d, want, sub) {
					fits = append(fits, d)
				}
			}
		}
		if want.Unknown() && len(fits) > 1 && !sameDenotation(fits) {
			kind := ExprName
			if fits[0].Denotes().Kind != DeclEnumLiteral {
				kind = ExprCall
			}
			e := &Expr{Kind: kind, Node: n, Type: UnknownType,
				Prefix: nm.expr}
			e.pend = &pending{kind: pendName, cands: nm.decls,
				tok: nm.tok}
			return e
		}
	}
	if x := indexAlternative(nm.decls, acts, sub); x != nil && !proc &&
		want.Unknown() {
		// f(args) may also index the result of a parameterless f
		// (LRM 8.4, 9.3.4): the context decides between the readings.
		o, _ := a.choose(nm.tok.Text(), nm.decls, acts, nil, false, sub)
		var fit []*Decl
		for _, cd := range o.Candidates {
			if cd.Fits {
				fit = append(fit, cd.Decl)
			}
		}
		if len(fit) == 0 {
			return a.indexReading(n, nm.tok, nm.decls, x, acts, nm.expr)
		}
		if !fitsResult(fit, x.elem) {
			e := &Expr{Kind: ExprCall, Node: n, Type: UnknownType,
				Prefix: nm.expr}
			e.pend = &pending{kind: pendCall, cands: nm.decls,
				tok: nm.tok, acts: acts, name: nm.tok.Text(),
				object: nm.expr, fitting: fit, index: x}
			return e
		}
	}
	a.unknownFormals(nm.decls, acts) // callrules.go
	return a.finishCall(n, nm.tok, nm.tok.Text(), nm.decls, acts, want,
		proc, nm.expr, false)
}

// indexAlt is the index reading of f(args): the parameterless function
// f returning an array (or an access to one) indexed by the actuals.
type indexAlt struct {
	fn   *Decl
	arr  *Type // the array indexed
	elem *Type
}

// indexAlternative returns the index reading of f(acts) when exactly
// one parameterless function of ds returns an array whose dimensions
// the positional actuals index, each fitting its index type; nil
// otherwise.
func indexAlternative(ds []*Decl, acts []actual, sub typeSubst,
) *indexAlt {
	if len(acts) == 0 {
		return nil
	}
	for _, ac := range acts {
		if ac.formal != "" || ac.open || ac.e.Kind == ExprRange ||
			ac.e.Kind == ExprTypeMark {
			return nil
		}
	}
	var alt *indexAlt
	for _, d := range ds {
		x := d.Denotes()
		if x.Kind != DeclFunction || required(x) > 0 || x.Type == nil {
			continue
		}
		at := sub.of(x.Type)
		if b := at.Base(); b.Class == TypeAccess && b.Designated != nil {
			at = b.Designated
		}
		b := at.Base()
		if b.Class != TypeArray || len(b.Index) != len(acts) {
			continue
		}
		ok := true
		for i, ac := range acts {
			if s, _ := fit(ac.e, b.Index[i]); s == 0 {
				ok = false
			}
		}
		if !ok {
			continue
		}
		if alt != nil {
			return nil
		}
		alt = &indexAlt{fn: d, arr: at, elem: orUnknown(at.ElemType())}
	}
	return alt
}

// indexReading builds f(acts) as the index of the result of the
// parameterless function x.fn.
func (a *analyzer) indexReading(n cst.Node, tok cst.Token,
	cands []*Decl, x *indexAlt, acts []actual, object *Expr,
) *Expr {
	o := &Overload{Name: tok.Text(), Chosen: x.fn}
	for _, d := range cands {
		cd := Candidate{Decl: d, Fits: d == x.fn}
		if !cd.Fits {
			cd.Reason = "is not the parameterless function indexed"
		}
		o.Candidates = append(o.Candidates, cd)
	}
	o.Candidates = sortedCandidates(o)
	a.refOverload(tok, editorOverload(o), RefCall) // pkginst.go
	var v *Expr = &Expr{Kind: ExprCall, Node: n, Decl: x.fn,
		Type: methodSubst(object).result(x.fn), Overload: o,
		Prefix: object}
	if v.Type.Base().Class == TypeAccess {
		v = &Expr{Kind: ExprDeref, Node: n, Prefix: v,
			Type: allocated(x.arr)}
	}
	e := &Expr{Kind: ExprIndex, Node: n, Prefix: v, Type: x.elem}
	b := x.arr.Base()
	for i, ac := range acts {
		a.settle(ac.e, b.Index[i])
		a.expect(ac.e, b.Index[i], ac.node)
		e.Args = append(e.Args, ac.e)
	}
	return e
}

// indexedCall resolves f(args) as an index or slice of the result of
// the parameterless function f (LRM 8.4, 8.5) when no overload of f
// takes that many arguments and exactly one parameterless function
// returns an array; nil otherwise.
func (a *analyzer) indexedCall(n cst.Node, nm named,
	args cst.AssociationList,
) *Expr {
	count := 0
	for item := range args.Items() {
		if !item.Formal().IsZero() {
			return nil // named associations are a call
		}
		count++
	}
	if count == 0 {
		return nil
	}
	var zero *Decl
	sub := methodSubst(nm.expr)
	o := &Overload{Name: nm.tok.Text()}
	for _, d := range nm.decls {
		x := d.Denotes()
		params := profileParams(x)
		if len(params) >= count && x.Kind != DeclEnumLiteral {
			return nil
		}
		cd := Candidate{Decl: d, Reason: "takes " +
			strconv.Itoa(len(params)) + " arguments, not " +
			strconv.Itoa(count)}
		if x.Kind == DeclFunction && match(d, nil, nil, false).score > 0 &&
			x.Type != nil && sub.of(x.Type).Base().Class == TypeArray {
			if zero != nil {
				return nil
			}
			zero = d
			cd = Candidate{Decl: d, Fits: true}
		}
		o.Candidates = append(o.Candidates, cd)
	}
	if zero == nil {
		return nil
	}
	o.Chosen = zero
	o.Candidates = sortedCandidates(o)
	a.refOverload(nm.tok, editorOverload(o), RefCall) // pkginst.go
	c := &Expr{Kind: ExprCall, Node: n, Decl: zero,
		Type: sub.result(zero), Overload: o, Prefix: nm.expr}
	return a.index(n, c, args)
}

// leftmostToken returns the first name token of a formal designator:
// x in x, x(3), x.y and x(1 to 2). A conversion function around the
// formal (f(x) => a) is read as an indexed formal; the overload of the
// conversion is not resolved yet.
func leftmostToken(n cst.Node) cst.Token {
	for range 64 {
		switch n.Kind() {
		case cst.KindSimpleName:
			return cst.SimpleName{Node: n}.Ident()
		case cst.KindOperatorSymbol:
			return cst.OperatorSymbol{Node: n}.Symbol()
		case cst.KindCallOrIndexName:
			n = cst.CallOrIndexName{Node: n}.Prefix().Node
		case cst.KindSelectedName:
			n = cst.SelectedName{Node: n}.Prefix().Node
		case cst.KindSliceName:
			n = cst.SliceName{Node: n}.Prefix().Node
		default:
			return cst.Token{}
		}
	}
	return cst.Token{}
}

// lenientFormal resolves the names inside a non-simple formal part
// (indices, conversion functions) without reporting.
func (a *analyzer) lenientFormal(n cst.Node) {
	switch n.Kind() {
	case cst.KindCallOrIndexName:
		c := cst.CallOrIndexName{Node: n}
		for item := range c.Args().Items() {
			if v := item.Actual(); !v.IsZero() {
				saved := a.quiet
				a.quiet = true
				a.expr(v.Node, nil)
				a.quiet = saved
			}
		}
	case cst.KindSliceName:
		saved := a.quiet
		a.quiet = true
		a.discreteRange(cst.SliceName{Node: n}.Range().Node, nil)
		a.quiet = saved
	}
}

// finishCall chooses an overload, reports failures, settles the
// actuals and records the references. When the context has not given a
// result type (final false) and the fitting overloads differ only in
// what they return, the call stays pending for the context to settle.
func (a *analyzer) finishCall(n cst.Node, tok cst.Token, name string,
	cands []*Decl, acts []actual, want *Type, proc bool, object *Expr,
	final bool,
) *Expr {
	sub := methodSubst(object)
	o, c := a.choose(name, cands, acts, want, proc, sub)
	if c == nil && !final && !proc && want.Unknown() &&
		multipleResults(o) {
		e := &Expr{Kind: ExprCall, Node: n, Type: UnknownType,
			Prefix: object}
		var fit []*Decl
		for _, cd := range o.Candidates {
			if cd.Fits {
				fit = append(fit, cd.Decl)
			}
		}
		e.pend = &pending{kind: pendCall, cands: cands, tok: tok,
			acts: acts, name: name, object: object, fitting: fit}
		return e
	}
	e := &Expr{Kind: ExprCall, Node: n, Type: UnknownType, Overload: o,
		Prefix: object}
	switch {
	case c != nil:
		e.Decl = c.d
		if !proc {
			e.Type = orUnknown(sub.result(c.d))
		}
		e.Formals = c.formals
		a.uninstantiatedCall(tok, c.d) // pkginst.go
		for i, ac := range acts {
			if f := c.formals[i]; f != nil {
				a.settle(ac.e, partType(ac.fpart, sub.of(f.Type)))
				if ac.formal != "" {
					a.ref(ac.ftok, instanceFormal(c.d.Denotes(), f),
						RefFormal)
				}
				if !ac.open && ac.fpart.IsZero() {
					a.actualClass(f, ac)
				}
			}
		}
	default:
		sure := certain(acts, want) && !a.brokenUses()
		// The actuals of a call that did not resolve are settled
		// quietly: an ambiguity there is a consequence of the failed
		// call, which is reported below.
		saved := a.quiet
		a.quiet = true
		for _, ac := range acts {
			a.settle(ac.e, nil)
		}
		a.quiet = saved
		fits := 0
		for _, cd := range o.Candidates {
			if cd.Fits {
				fits++
			}
		}
		switch {
		case len(cands) == 0:
		case fits == 0 && sure && !a.quiet:
			e.illTyped = true
			a.report(tok.Span(), diag.New(diag.SemaNoOverload, name,
				reasons(o)).WithRelated(candidateNotes(o, false)...))
		case fits > 1 && sure && !a.quiet:
			e.illTyped = true
			a.report(tok.Span(), diag.New(diag.SemaAmbiguousOverload,
				name, fitting(o)).WithRelated(candidateNotes(o, true)...))
		}
		if t := commonFit(o); t != nil && !proc {
			e.Type = t
		}
	}
	for _, ac := range acts {
		e.Args = append(e.Args, ac.e)
	}
	a.refOverload(tok, editorOverload(o), RefCall) // pkginst.go
	return e
}

// multipleResults reports whether the fitting candidates return
// different types, so the context's type can choose among them.
func multipleResults(o *Overload) bool {
	var first *Type
	n := 0
	for _, c := range o.Candidates {
		if !c.Fits {
			continue
		}
		rt := c.Decl.Denotes().Type
		if rt == nil {
			return false
		}
		n++
		if first == nil {
			first = rt
		} else if !SameBase(first, rt) {
			return true
		}
	}
	return false
}

// commonFit returns the result type shared by every fitting candidate.
func commonFit(o *Overload) *Type {
	var ds []*Decl
	for _, c := range o.Candidates {
		if c.Fits {
			ds = append(ds, c.Decl)
		}
	}
	if len(ds) == 0 {
		return nil
	}
	return commonType(ds)
}

// Signature returns the profile of a subprogram or enumeration literal
// in the bundled-key form "[T1, T2 return R]".
func Signature(d *Decl) string {
	x := d.Denotes()
	var marks []string
	for _, p := range profileParams(x) {
		marks = append(marks, orUnknown(p.Type).Name())
	}
	s := "[" + strings.Join(marks, ", ")
	if rt := resultType(x); rt != nil {
		if len(marks) > 0 {
			s += " "
		}
		s += "return " + rt.Name()
	}
	return s + "]"
}

// describe names a candidate for messages: name [profile] from P,
// where P is the package (or other unit or region) that declares it,
// implicitly or not.
func describe(d *Decl) string {
	s := d.Text + " " + Signature(d)
	if o := Origin(d); o != "" {
		s += " from " + o
	}
	return s
}

// Origin names the declarative region an overload comes from, as hover
// shows it: the package, subprogram or design unit declaring it (for
// an implicitly declared operation, the one declaring its type), "" if
// unknown.
func Origin(d *Decl) string {
	x := d.Denotes()
	if x == nil {
		return ""
	}
	if p := x.Parent; p != nil && p.Owner != nil {
		return p.Owner.Text
	}
	if u := x.Unit; u != nil && u.Decl != nil {
		return u.Decl.Text
	}
	return ""
}

// reasons lists why each candidate does not fit, in canonical order.
func reasons(o *Overload) string {
	var out []string
	for _, c := range sortedCandidates(o) {
		out = append(out, describe(c.Decl)+" "+c.Reason)
	}
	return strings.Join(limit(out), "; ")
}

// fitting lists the candidates that fit.
func fitting(o *Overload) string {
	var out []string
	for _, c := range sortedCandidates(o) {
		if c.Fits {
			out = append(out, describe(c.Decl))
		}
	}
	return strings.Join(limit(out), "; ")
}

// sortedCandidates orders candidates by relevance: the fitting ones,
// then the nearest misses; ties by file, position and signature, a
// deterministic order independent of visibility paths.
func sortedCandidates(o *Overload) []Candidate {
	cs := slices.Clone(o.Candidates)
	sigs := map[*Decl]string{}
	sig := func(d *Decl) string {
		s, ok := sigs[d]
		if !ok {
			s = Signature(d)
			sigs[d] = s
		}
		return s
	}
	slices.SortStableFunc(cs, func(a, b Candidate) int {
		x, y := a.Decl.Denotes(), b.Decl.Denotes()
		if c := cmp.Or(compareBool(b.Fits, a.Fits),
			cmp.Compare(b.Near, a.Near),
			cmp.Compare(x.Path(), y.Path()),
			cmp.Compare(x.Span.Start, y.Span.Start)); c != 0 {
			return c
		}
		// The signature is built only for a tie (implicit operators
		// share their type's position): it is the costly key, built
		// once per declaration.
		return cmp.Compare(sig(x), sig(y))
	})
	return cs
}

func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// candidateNotes are related locations for the candidates of o
// declared in project files (the fitting ones when fitting is set),
// each with its reason: the editor shows them next to the diagnostic.
// Bundled std/ieee candidates are named in the message only.
func candidateNotes(o *Overload, fitting bool) []diag.Related {
	var out []diag.Related
	for _, c := range o.Candidates {
		x := c.Decl.Denotes()
		if x.File == nil || x.File.Bundled || fitting && !c.Fits {
			continue
		}
		if len(out) == 8 {
			break
		}
		l, col := x.File.Lines.LineCol(x.Span.Start)
		el, ec := x.File.Lines.LineCol(x.Span.End)
		msg := "candidate " + describe(c.Decl)
		if x.Implicit {
			// An implicit operation is located at its type.
			msg += ", predefined for the type declared here"
		}
		if c.Reason != "" {
			msg += ": " + c.Reason
		}
		out = append(out, diag.Related{Path: x.File.Path, Line: l,
			Col: col, EndLine: el, EndCol: ec, Message: msg})
	}
	return out
}

func limit(s []string) []string {
	const most = 8
	if len(s) <= most {
		return s
	}
	return append(s[:most:most], "and "+strconv.Itoa(len(s)-most)+
		" more")
}

// matchSignature returns the declarations of ds whose profile matches
// the signature sig ([T1, T2 return R], LRM 4.5.3).
func (a *analyzer) matchSignature(ds []*Decl, sig cst.Signature,
) []*Decl {
	var params []*Type
	var ret *Type
	afterReturn := false
	for e := range sig.Elements() {
		if e.IsToken() {
			if e.Token.Kind() == token.KwReturn {
				afterReturn = true
			}
			continue
		}
		if !(cst.Name{}).Accepts(e.Node.Kind()) {
			continue
		}
		t := a.typeMark(e.Node)
		if afterReturn {
			ret = t
		} else {
			params = append(params, t)
		}
	}
	var out []*Decl
	for _, d := range ds {
		x := d.Denotes()
		ps := profileParams(x)
		if len(ps) != len(params) {
			continue
		}
		ok := true
		for i := range ps {
			if !SameBase(ps[i].Type, params[i]) &&
				!params[i].Unknown() && !ps[i].Type.Unknown() {
				ok = false
			}
		}
		rt := resultType(x)
		if (ret == nil) != (rt == nil) ||
			(ret != nil && !ret.Unknown() && !rt.Unknown() &&
				!SameBase(ret, rt)) {
			ok = false
		}
		if ok {
			out = append(out, d)
		}
	}
	return out
}

// isLogical reports whether k is a logical operator keyword, which is
// binary only before VHDL-2008.
func isLogical(k token.Kind) bool {
	switch k {
	case token.KwAnd, token.KwOr, token.KwNand, token.KwNor,
		token.KwXor, token.KwXnor:
		return true
	}
	return false
}

// OverloadAt returns the editor-mode overload result of the innermost
// call or operator expression of the file at path whose source span
// contains off (inside its name or its argument list), with that span:
// completion inside a call's arguments shows the same candidates and
// reasons as the diagnostic and hover (#34). ok is false when off is
// in no call.
func (r *Result) OverloadAt(path string, off int) (*Overload, cst.Range,
	bool,
) {
	f, ok := r.File(path)
	if !ok {
		return nil, cst.Range{}, false
	}
	var best *Expr
	var walk func(e *Expr, depth int)
	walk = func(e *Expr, depth int) {
		if e == nil || depth > 256 || e.Node.IsZero() {
			return
		}
		sp := e.Node.Span()
		if off < sp.Start || off > sp.End {
			return
		}
		if e.Overload != nil && (best == nil ||
			sp.End-sp.Start < best.Node.Span().End-best.Node.Span().Start) {
			best = e
		}
		walk(e.Prefix, depth+1)
		for _, x := range e.Args {
			walk(x, depth+1)
		}
		for _, as := range e.Assocs {
			for _, c := range as.Choices {
				walk(c, depth+1)
			}
			walk(as.Value, depth+1)
		}
		if rc := e.Range; rc != nil {
			walk(rc.Left, depth+1)
			walk(rc.Right, depth+1)
			walk(rc.Attr, depth+1)
		}
	}
	for _, u := range f.Units {
		for _, e := range u.Exprs {
			walk(e, 0)
		}
	}
	if best == nil {
		return nil, cst.Range{}, false
	}
	return best.Overload, cst.Range(best.Node.Span()), true
}

// partType returns the type of the part of a formal of type t that the
// formal part fn names: an element (v.a), an indexed element (w(1)), a
// slice (w(1 to 2)) or the whole formal (fn zero). It is UnknownType
// when the part cannot be typed here (a conversion function), so the
// actual is not held against it.
func partType(fn cst.Node, t *Type) *Type {
	if fn.IsZero() || fn.Kind() == cst.KindSimpleName {
		return t
	}
	switch fn.Kind() {
	case cst.KindSelectedName:
		sn := cst.SelectedName{Node: fn}
		pt := partType(sn.Prefix().Node, t)
		if pt.Unknown() || pt.Base().Class != TypeRecord {
			return UnknownType
		}
		k := TokenKey(sn.Suffix())
		for _, f := range pt.Base().Fields {
			if f.Name == k {
				return orUnknown(f.Type)
			}
		}
	case cst.KindCallOrIndexName:
		c := cst.CallOrIndexName{Node: fn}
		pt := partType(c.Prefix().Node, t)
		if pt.Unknown() || pt.Base().Class != TypeArray {
			return UnknownType
		}
		if allRanges(c.Args()) {
			return runTime(pt) // the slice's own length
		}
		n := 0
		for range c.Args().Items() {
			n++
		}
		if n == pt.Dims() {
			return orUnknown(pt.ElemType())
		}
	case cst.KindSliceName:
		pt := partType(cst.SliceName{Node: fn}.Prefix().Node, t)
		if !pt.Unknown() && pt.Base().Class == TypeArray {
			return runTime(pt)
		}
	}
	return UnknownType
}

// mixedUniversal reports whether d is one of the predefined operations
// of universal_real with a universal_integer operand (LRM 9.2.7).
func mixedUniversal(d *Decl) bool {
	if d == nil || !d.Implicit || len(d.Params) != 2 {
		return false
	}
	l, r := d.Params[0].Type, d.Params[1].Type
	return l != r && (l == UniversalReal || r == UniversalReal) &&
		(l == UniversalInteger || r == UniversalInteger)
}

// actualClass reports an actual of the wrong object class for its
// formal (LRM 4.2.2.2, 4.2.2.3): a variable formal takes a variable
// name, a signal formal a signal name, and an out or inout formal a
// name it can write.
func (a *analyzer) actualClass(f *Decl, ac actual) {
	if a.quiet || ac.e == nil || ac.e.illTyped || ac.e.Type.Unknown() {
		return
	}
	switch f.Class {
	case ClassVariable, ClassSignal:
	default:
		return
	}
	want := "variable"
	if f.Class == ClassSignal {
		want = "signal"
	}
	d := actualObject(ac.e)
	if d == nil {
		switch ac.e.Kind {
		case ExprLiteral, ExprPhysical, ExprCall, ExprAggregate,
			ExprAllocator:
		default:
			// An external name, an allocated object, an implicit
			// signal ('DELAYED): an object GoHard does not track.
			return
		}
		a.semaError(ac.node.Span(), "the actual for "+want+" parameter "+
			f.Text+" must be a "+want+" name")
		return
	}
	switch d.Kind {
	case DeclEnumLiteral, DeclUnit, DeclFunction:
		a.semaError(ac.node.Span(), "the actual for "+want+" parameter "+
			f.Text+" must be a "+want+" name")
		return
	}
	if have := objectClass(d); have != "" && have != want {
		a.semaError(ac.node.Span(), "the actual for "+want+" parameter "+
			f.Text+" must be a "+want+", not "+d.Kind.String()+" "+d.Text)
		return
	}
	a.actualMode(f, d, ac, want)
}

// actualMode reports an actual whose mode does not allow the formal's
// (LRM 6.5.2, 6.5.7.1): an out or inout formal writes its actual, so
// the actual cannot be an interface object of mode in or an element of
// mode in of a mode view; an in or inout formal reads it, so before
// VHDL-2008 the actual cannot be a port of mode out.
func (a *analyzer) actualMode(f, d *Decl, ac actual, class string) {
	sp := ac.node.Span()
	mode := f.Mode
	if mode == ModeNone {
		mode = ModeIn
	}
	what := "the actual for " + modeText(mode) + " " + class +
		" parameter " + f.Text
	if writes(f.Mode) {
		if d.Mode == ModeView {
			vp, ok := viewRoot(ac.e)
			switch {
			case !ok:
			case vp.v == nil && vp.mode == ModeIn:
				a.semaError(sp, what+" cannot be "+vp.path+
					": it has mode in in view "+a.viewOf(vp)+" of "+
					a.portWord(vp.port)+" "+vp.port.Text)
			case vp.v != nil:
				if in := firstIn(vp.v, vp.path+"."); in != "" {
					a.semaError(sp, what+" cannot be "+vp.path+
						": element "+in+" has mode in in view "+
						a.viewOf(vp)+" of "+a.portWord(vp.port)+" "+
						vp.port.Text)
				}
			}
			return
		}
		if d.Mode == ModeIn && (d.Kind == DeclPort ||
			d.Kind == DeclParameter) {
			a.semaError(sp, what+" cannot be "+d.Kind.String()+" "+
				d.Text+", which has mode in")
			return
		}
		if class == "signal" {
			a.outsideProcessSignal(ac.e, ac.node) // targetrules.go
		}
		return
	}
	if (mode == ModeIn || mode == ModeInout) && d.Kind == DeclPort &&
		d.Mode == ModeOut && a.std < token.Std2008 {
		a.semaError(sp, what+" cannot be port "+d.Text+", which has "+
			"mode out and cannot be read before VHDL-2008")
	}
}

// concreteFirst drops the candidates that fit only because a formal is
// of a generic type when another candidate fits without that. A
// generic type is a distinct type (LRM 6.5.3), so inside its
// uninstantiated unit no other value is of it, and a concrete overload
// is the only legal interpretation.
func concreteFirst(top []*candidate) []*candidate {
	var kept []*candidate
	for _, c := range top {
		if c.generic == 0 {
			kept = append(kept, c)
		}
	}
	if len(kept) == 0 || len(kept) == len(top) {
		return top
	}
	return kept
}
