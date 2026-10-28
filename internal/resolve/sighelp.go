package resolve

import (
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/token"
)

// Signature help (#56): the signatures of the subprogram call, generic
// map or port map around a position, with the active parameter.

// Signature is one signature of a signature help.
type Signature struct {
	// Decl is the subprogram of a call, or the entity, component or
	// block whose generic or port list a map associates.
	Decl *sema.Decl
	// Label is the signature as one line; Params are the byte ranges
	// of its parameters in Label, one per formal of Formals.
	Label   string
	Params  []cst.Range
	Formals []*sema.Decl
	// Active is the index of the active parameter, -1 for none.
	Active int
	// Doc is markdown: where the subprogram comes from, why a
	// candidate does not fit, and the doc comment.
	Doc string
}

// SignatureHelp is the signature help at off in the file at path: in
// the association list of a call, the overloads of the subprogram
// (the one the call resolves to first, then every other candidate in
// the order hover lists them); in a generic or port map, the generic
// or port list of the instantiated unit; else, on an operand of an
// operator, the operator's overloads. Every overload comes from the
// reference at the call's name, the one hover and definition answer
// from, so the three always agree. ok is false when off is in none.
func SignatureHelp(r *sema.Result, path string, off int,
) ([]Signature, bool) {
	f, ok := r.File(path)
	if !ok {
		return nil, false
	}
	call, list, callOK := innermostCall(f, off)
	m, mapOK := innermostMap(f, off)
	switch {
	case callOK && (!mapOK || within(list.Span(), m.list.Span())):
		return callSignatures(r, f, call, list, off)
	case mapOK:
		return []Signature{mapSignature(r, m, off)}, true
	}
	if op, ok := innermostOperator(f, off); ok {
		return callSignatures(r, f, op, cst.AssociationList{}, off)
	}
	return nil, false
}

// within reports whether a lies inside b.
func within(a, b cst.Range) bool {
	return b.Start <= a.Start && a.End <= b.End
}

// eachExpr calls fn on every typed expression of the file's units,
// nested ones included: the unit's expressions and the actuals of its
// generic and port maps.
func eachExpr(f *sema.File, fn func(e *sema.Expr)) {
	var walk func(e *sema.Expr, depth int)
	walk = func(e *sema.Expr, depth int) {
		if e == nil || depth > 256 || e.Node.IsZero() {
			return
		}
		fn(e)
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
		eachMap(u, func(m assocMap) {
			for _, as := range m.assocs {
				walk(as.Actual, 0)
				walk(as.Part, 0)
			}
		})
	}
}

// callList returns the association list of a call written with one
// (f(a, b), p(x), a VHDL-2019 parameter map).
func callList(e *sema.Expr) (cst.AssociationList, bool) {
	if e.Kind != sema.ExprCall {
		return cst.AssociationList{}, false
	}
	c, ok := cst.As[cst.CallOrIndexName](e.Node)
	if !ok {
		return cst.AssociationList{}, false
	}
	if l := c.Args(); !l.IsZero() {
		return l, true
	}
	if pm := c.ParameterMap(); !pm.IsZero() && !pm.List().IsZero() {
		return pm.List(), true
	}
	return cst.AssociationList{}, false
}

// inside reports whether off is between the parentheses of l (after
// the opening one, up to the closing one if it is there yet).
func inside(l cst.AssociationList, off int) bool {
	sp := l.Span()
	if off < sp.Start || off > sp.End {
		return false
	}
	open, close := cst.Token{}, cst.Token{}
	for t := range l.Tokens() {
		switch t.Kind() {
		case token.LParen:
			if open.IsZero() {
				open = t
			}
		case token.RParen:
			if !t.Missing() {
				close = t
			}
		}
	}
	if open.IsZero() || off < open.Span().End {
		return false
	}
	return close.IsZero() || off <= close.Span().Start
}

// innermostCall returns the call whose association list holds off,
// the innermost one when calls nest.
func innermostCall(f *sema.File, off int) (*sema.Expr,
	cst.AssociationList, bool,
) {
	var best *sema.Expr
	var bestList cst.AssociationList
	eachExpr(f, func(e *sema.Expr) {
		if e.Overload == nil && e.Decl == nil {
			return
		}
		l, ok := callList(e)
		if !ok || !inside(l, off) {
			return
		}
		if best == nil || within(l.Span(), bestList.Span()) {
			best, bestList = e, l
		}
	})
	return best, bestList, best != nil
}

// innermostOperator returns the innermost operator expression (a
// binary or unary operation with an overload result) whose span holds
// off.
func innermostOperator(f *sema.File, off int) (*sema.Expr, bool) {
	var best *sema.Expr
	eachExpr(f, func(e *sema.Expr) {
		if e.Kind != sema.ExprCall || e.Overload == nil {
			return
		}
		k := e.Node.Kind()
		if k != cst.KindBinaryExpr && k != cst.KindUnaryExpr {
			return
		}
		sp := e.Node.Span()
		if off < sp.Start || off > sp.End {
			return
		}
		if best == nil || within(sp, best.Node.Span()) {
			best = e
		}
	})
	return best, best != nil
}

// callRef returns the reference sema recorded at the name of a call:
// the designator of the prefix (f, pkg.f, "+") or the operator symbol.
func callRef(f *sema.File, e *sema.Expr) (sema.Ref, bool) {
	var at cst.Range
	switch e.Node.Kind() {
	case cst.KindBinaryExpr:
		at = cst.BinaryExpr{Node: e.Node}.Op().Span()
	case cst.KindUnaryExpr:
		at = cst.UnaryExpr{Node: e.Node}.Op().Span()
	default:
		c, ok := cst.As[cst.CallOrIndexName](e.Node)
		if !ok {
			return sema.Ref{}, false
		}
		at = c.Prefix().Span()
	}
	var out sema.Ref
	found := false
	for _, u := range f.Units {
		for _, ref := range u.Refs {
			if ref.File == f && ref.Kind == sema.RefCall &&
				within(ref.Span, at) &&
				(!found || ref.Span.Start >= out.Span.Start) {
				out, found = ref, true
			}
		}
	}
	return out, found
}

// callSignatures returns the overloads of the call e.
func callSignatures(r *sema.Result, f *sema.File, e *sema.Expr,
	l cst.AssociationList, off int,
) ([]Signature, bool) {
	o := e.Overload
	chosen := e.Decl
	if ref, ok := callRef(f, e); ok {
		o, chosen = ref.Overload, ref.Decl
	}
	type cand struct {
		d      *sema.Decl
		reason string
	}
	var cands []cand
	if chosen != nil {
		cands = append(cands, cand{chosen, ""})
	}
	for _, c := range o.Sorted() {
		if c.Decl != chosen {
			cands = append(cands, cand{c.Decl, c.Reason})
		}
	}
	if len(cands) == 0 {
		return nil, false
	}
	out := make([]Signature, 0, len(cands))
	for _, c := range cands {
		s := subprogramSignature(c.d)
		if l.IsZero() {
			s.Active = operand(e, off)
		} else {
			s.Active = activeFormal(l, off, s.Formals)
		}
		if s.Active >= len(s.Params) {
			s.Active = -1 // the right operand of a unary candidate
		}
		var doc []string
		if x := c.d.Denotes(); x.Unit != nil {
			doc = append(doc, "from "+string(x.Unit.Library)+"."+
				string(x.Unit.Name))
		}
		if c.reason != "" {
			doc = append(doc, "does not fit: "+c.reason)
		}
		if d := DocOf(r, c.d); d != "" {
			doc = append(doc, d)
		}
		s.Doc = strings.Join(doc, "\n\n")
		out = append(out, s)
	}
	return out, true
}

// operand returns the operand of the operator expression e that off
// is in: 0 for a unary operator or the left operand, 1 for the right.
func operand(e *sema.Expr, off int) int {
	if b, ok := cst.As[cst.BinaryExpr](e.Node); ok &&
		off >= b.Op().Span().End {
		return 1
	}
	return 0
}

// activeFormal returns the index in formals of the parameter being
// written at off in the association list l: the formal named by a
// named association, the position of a positional one, or after named
// associations the first formal not yet associated. -1 when there is
// none.
func activeFormal(l cst.AssociationList, off int,
	formals []*sema.Decl,
) int {
	k := 0
	for t := range l.Tokens() {
		if t.Kind() == token.Comma && t.Span().End <= off {
			k++
		}
	}
	var items []cst.AssociationElement
	for it := range l.Items() {
		items = append(items, it)
	}
	index := func(fn cst.Expr) int {
		for t := range fn.DescendantTokens() {
			if t.Kind() != token.Identifier &&
				t.Kind() != token.ExtendedIdentifier {
				continue
			}
			key := sema.TokenKey(t)
			for i, d := range formals {
				if d.Name == key {
					return i
				}
			}
			return -1
		}
		return -1
	}
	if k < len(items) {
		if fn := items[k].Formal(); !fn.IsZero() {
			return index(fn)
		}
	}
	// Positional associations come first; after a named one, the
	// next formal is the first one no association names yet.
	named := map[int]bool{}
	positional := 0
	for j, it := range items {
		fn := it.Formal()
		switch {
		case fn.IsZero() && j == positional && j != k:
			positional++
		case !fn.IsZero() && j != k:
			if i := index(fn); i >= 0 {
				named[i] = true
			}
		}
	}
	if len(named) == 0 && k == positional {
		if k < len(formals) {
			return k
		}
		return -1
	}
	for i := positional; i < len(formals); i++ {
		if !named[i] {
			return i
		}
	}
	return -1
}

// subprogramSignature is the label of a subprogram:
// `function NAME(formal : mode type; ...) return TYPE`.
func subprogramSignature(d *sema.Decl) Signature {
	x := d.Denotes()
	s := Signature{Decl: d, Formals: x.Params}
	var b strings.Builder
	switch x.Kind {
	case sema.DeclProcedure:
		b.WriteString("procedure ")
	default:
		b.WriteString("function ")
	}
	b.WriteString(x.Text)
	if len(x.Params) > 0 {
		b.WriteString("(")
		for i, p := range x.Params {
			if i > 0 {
				b.WriteString("; ")
			}
			start := b.Len()
			b.WriteString(formalText(p))
			s.Params = append(s.Params, cst.Range{Start: start,
				End: b.Len()})
		}
		b.WriteString(")")
	}
	if x.Kind != sema.DeclProcedure && x.Type != nil {
		b.WriteString(" return " + x.Type.Name())
	}
	s.Label = b.String()
	return s
}

// formalText is one formal of a signature: its declaration as written
// (`signal clk : in std_logic := '0'`), whitespace and comments
// folded to single spaces, or for a predefined operation
// `l : in INTEGER`.
func formalText(d *sema.Decl) string {
	if d.File != nil && !d.Implicit {
		switch d.Node.Kind() {
		case cst.KindInterfaceObjectDecl:
			io := cst.InterfaceObjectDecl{Node: d.Node}
			s := d.Text + " :"
			if c := io.Class(); !c.IsZero() {
				s = c.Text() + " " + s
			}
			if m := io.Mode(); !m.IsZero() && m.Span().End > m.Span().Start {
				s += " " + tokensText(m.Node)
			}
			if st := io.Subtype(); !st.IsZero() {
				s += " " + tokensText(st.Node)
			}
			if as := io.Node.Token(token.VarAssign, 0); !as.IsZero() {
				for c := range io.Node.Nodes() {
					if c.Span().Start >= as.Span().End {
						s += " := " + tokensText(c)
						break
					}
				}
			}
			return s
		case cst.KindInterfaceTypeDecl, cst.KindInterfaceSubprogramDecl,
			cst.KindInterfacePackageDecl:
			return tokensText(d.Node)
		}
	}
	t := "unknown type"
	if d.Type != nil {
		t = d.Type.Name()
	}
	if d.Name == "anonymous" {
		return t
	}
	mode := ""
	switch d.Mode {
	case sema.ModeIn:
		mode = "in "
	case sema.ModeOut:
		mode = "out "
	case sema.ModeInout:
		mode = "inout "
	case sema.ModeBuffer:
		mode = "buffer "
	case sema.ModeLinkage:
		mode = "linkage "
	}
	return d.Text + " : " + mode + t
}

// tokensText is the text of n's tokens, one space wherever the source
// has trivia between two of them.
func tokensText(n cst.Node) string {
	var b strings.Builder
	prev := -1
	for t := range n.DescendantTokens() {
		if t.Missing() {
			continue
		}
		sp := t.Span()
		if prev >= 0 && sp.Start > prev {
			b.WriteByte(' ')
		}
		b.WriteString(t.Text())
		prev = sp.End
	}
	return b.String()
}

// assocMap is one generic or port map of a unit: the associations,
// their list and the declaration whose formals they associate.
type assocMap struct {
	list   cst.AssociationList
	assocs []sema.Association
	// owner is the entity, component, block or generic unit; formals
	// are its generics or ports in order; port is set for a port map.
	owner   *sema.Decl
	formals []*sema.Decl
	port    bool
}

// eachMap calls fn on every generic and port map of the unit u:
// instances and blocks of its statement tree, and the generic maps of
// package and subprogram instances.
func eachMap(u *sema.Unit, fn func(assocMap)) {
	var stmts func(ss []*sema.Stmt, depth int)
	stmts = func(ss []*sema.Stmt, depth int) {
		if depth > 256 {
			return
		}
		for _, s := range ss {
			if s == nil {
				continue
			}
			stmtMaps(s, fn)
			stmts(s.Body, depth+1)
			for _, a := range s.Alts {
				stmts(a.Body, depth+1)
			}
		}
	}
	stmts(u.Body, 0)
	for _, d := range u.Decls {
		if len(d.Generics) == 0 || d.Implicit || d.Node.IsZero() {
			continue
		}
		l, ok := mapList(d.Node, false)
		if !ok {
			continue
		}
		fn(assocMap{list: l, assocs: d.Generics, owner: d,
			formals: formalsOf(d.Generics)})
	}
}

// stmtMaps reports the generic and port maps of an instance or block.
func stmtMaps(s *sema.Stmt, fn func(assocMap)) {
	var owner *sema.Decl
	var region *sema.Region
	switch s.Kind {
	case sema.StmtInstance:
		owner = s.Component
		if owner == nil {
			owner = s.Entity
		}
		if owner != nil {
			region = owner.Region
		}
	case sema.StmtBlock:
		owner, region = s.Label, s.Region
	default:
		return
	}
	for _, port := range []bool{false, true} {
		as := s.Generics
		if port {
			as = s.Ports
		}
		l, ok := mapList(s.Node, port)
		if !ok {
			continue
		}
		var formals []*sema.Decl
		if region != nil {
			for _, d := range region.Decls {
				if port && d.Kind == sema.DeclPort ||
					!port && isGeneric(d.Kind) {
					formals = append(formals, d)
				}
			}
		}
		fn(assocMap{list: l, assocs: as, owner: owner, formals: formals,
			port: port})
	}
}

func isGeneric(k sema.DeclKind) bool {
	switch k {
	case sema.DeclGeneric, sema.DeclGenericType,
		sema.DeclGenericSubprogram, sema.DeclGenericPackage:
		return true
	}
	return false
}

// formalsOf returns the formals of a generic map that sema resolved,
// in their declaration order (for a package or subprogram instance,
// whose generic list is not at hand).
func formalsOf(as []sema.Association) []*sema.Decl {
	var out []*sema.Decl
	for _, a := range as {
		if a.Formal != nil {
			out = append(out, a.Formal)
		}
	}
	return out
}

// mapList returns the generic map (port false) or port map list that
// is a direct part of the node n (an instance, block header, package
// or subprogram instance).
func mapList(n cst.Node, port bool) (cst.AssociationList, bool) {
	var search func(n cst.Node, depth int) (cst.AssociationList, bool)
	search = func(n cst.Node, depth int) (cst.AssociationList, bool) {
		for c := range n.Nodes() {
			switch c.Kind() {
			case cst.KindGenericMapAspect:
				if !port {
					l := cst.GenericMapAspect{Node: c}.List()
					return l, !l.IsZero()
				}
			case cst.KindPortMapAspect:
				if port {
					l := cst.PortMapAspect{Node: c}.List()
					return l, !l.IsZero()
				}
			case cst.KindBlockHeader:
				if depth == 0 {
					if l, ok := search(c, 1); ok {
						return l, true
					}
				}
			}
		}
		return cst.AssociationList{}, false
	}
	return search(n, 0)
}

// innermostMap returns the map whose association list holds off.
func innermostMap(f *sema.File, off int) (assocMap, bool) {
	var best assocMap
	found := false
	for _, u := range f.Units {
		eachMap(u, func(m assocMap) {
			if inside(m.list, off) && (!found ||
				within(m.list.Span(), best.list.Span())) {
				best, found = m, true
			}
		})
	}
	return best, found
}

// mapSignature is the generic or port list of a map's unit:
// `entity NAME port (formal : mode type; ...)`.
func mapSignature(r *sema.Result, m assocMap, off int) Signature {
	s := Signature{Decl: m.owner, Formals: m.formals}
	var b strings.Builder
	if m.owner != nil {
		b.WriteString(m.owner.Kind.String() + " " + m.owner.Text + " ")
	}
	if m.port {
		b.WriteString("port (")
	} else {
		b.WriteString("generic (")
	}
	for i, d := range m.formals {
		if i > 0 {
			b.WriteString("; ")
		}
		start := b.Len()
		b.WriteString(formalText(d))
		s.Params = append(s.Params, cst.Range{Start: start, End: b.Len()})
	}
	b.WriteString(")")
	s.Label = b.String()
	s.Active = activeFormal(m.list, off, m.formals)
	if m.owner != nil {
		var doc []string
		if u := m.owner.Unit; u != nil && u.Decl == m.owner {
			doc = append(doc, "from "+string(u.Library)+"."+
				string(u.Name))
		}
		if d := DocOf(r, m.owner); d != "" {
			doc = append(doc, d)
		}
		s.Doc = strings.Join(doc, "\n\n")
	}
	return s
}
