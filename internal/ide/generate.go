package ide

import (
	"bytes"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/format"
	"github.com/connerohnesorge/gohard/internal/parser"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/server"
	"github.com/connerohnesorge/gohard/internal/token"
	"github.com/connerohnesorge/gohard/internal/workspace"
)

// The generated code actions (#57): the rewrites that instantiate an
// entity, declare its component and complete a port map, and the
// source action that creates a testbench. Every generated text is
// laid out by the formatter with the project's format.* keys:
// format.Range over the edited document for an edit, format.Format
// for a new file, so `gohard fmt --check` accepts it.

// Generate configures the generated code actions.
type Generate struct {
	// ToolVersion is the gohard version conditional analysis sees
	// when the edited text is parsed for formatting.
	ToolVersion string
	// Create reports whether the client applies CreateFile resource
	// operations; Create testbench is offered only then.
	Create bool
}

// gen is one request's generator: the analysis, the queried document
// and the parse and format options of the snapshot.
type gen struct {
	an    *Analysis
	path  string
	uri   string
	text  []byte
	f     *sema.File
	popts parser.Options
	fopts format.Options
}

// Refactors returns the generated code actions of the range r of the
// document uri, in this order:
//
//   - Instantiate entity (refactor.rewrite): on a concurrent
//     statement that is only a name (`counter;`, `work.counter`, also
//     with a label and no map) naming an entity, the entity
//     instantiation with every formal (genericMap, portMap);
//   - Declare component (refactor.rewrite): on a component
//     instantiation whose component is not declared, the declaration
//     of the entity of that name at the end of the architecture's
//     declarative part;
//   - Complete port map (refactor.rewrite): on an instance whose port
//     map leaves formals out, those formals;
//   - Create testbench (source): in an entity's design unit, the new
//     file <name>_tb.vhd next to the entity's file (testbench), when
//     g.Create.
func (an *Analysis) Refactors(uri string, r server.Span, g Generate,
) []server.CodeAction {
	path, ok := an.Path(uri)
	if !ok || an.Result == nil {
		return nil
	}
	f, ok := an.Result.File(path)
	doc, _ := an.Snap.Document(uri)
	if !ok || doc == nil || f.Parse == nil {
		return nil
	}
	x := &gen{an: an, path: path, uri: uri, text: doc.Text(), f: f,
		popts: parser.FromConfig(an.Snap.Config, g.ToolVersion),
		fopts: format.FromConfig(an.Snap.Config)}
	var out []server.CodeAction
	if stmt, u := x.statementAt(r); u != nil {
		out = append(out, x.instantiate(stmt, u)...)
		out = append(out, x.declareComponent(stmt, u)...)
		out = append(out, x.completePortMap(stmt, u)...)
	}
	if g.Create {
		out = append(out, x.createTestbench(r)...)
	}
	return out
}

// statementAt returns the concurrent procedure call or instance
// statement around r and the architecture holding it.
func (x *gen) statementAt(r server.Span) (cst.Node, *sema.Unit) {
	for n := x.f.Root().Covering(r.Start, r.End); !n.IsZero(); n = n.Parent() {
		switch n.Kind() {
		case cst.KindConcurrentProcedureCallStmt,
			cst.KindComponentInstantiationStmt:
			u := unitAt(x.f, n.Span().Start)
			if u == nil || u.Kind != sema.UnitArchitecture ||
				u.Region == nil || n.Inactive() {
				return cst.Node{}, nil
			}
			return n, u
		}
	}
	return cst.Node{}, nil
}

// unitAt returns the unit of f whose text holds off.
func unitAt(f *sema.File, off int) *sema.Unit {
	for _, u := range f.Units {
		if sp := u.Node.Span(); sp.Start <= off && off <= sp.End {
			return u
		}
	}
	return nil
}

// instantiate is Instantiate entity on stmt: a procedure call with no
// arguments (`counter;`, `work.counter;`) or a component instantiation
// of a name with no map (`u1 : counter;`) that names an entity.
func (x *gen) instantiate(stmt cst.Node, u *sema.Unit,
) []server.CodeAction {
	var name cst.Node
	var label cst.Label
	switch stmt.Kind() {
	case cst.KindConcurrentProcedureCallStmt:
		c := cst.ConcurrentProcedureCallStmt{Node: stmt}
		if !c.Postponed().IsZero() {
			return nil
		}
		name, label = c.Call().Node, c.Label()
	default:
		c := cst.ComponentInstantiationStmt{Node: stmt}
		iu := c.Unit()
		if iu.IsZero() || !c.GenericMap().IsZero() ||
			!c.PortMap().IsZero() || iu.Node.FirstToken().Kind() !=
			token.Identifier && iu.Node.FirstToken().Kind() !=
			token.ExtendedIdentifier {
			return nil
		}
		name, label = iu.Name().Node, c.Label()
	}
	if ref, ok := x.an.Result.RefAt(x.path, lastToken(name).Span().Start); ok &&
		ref.Decl != nil && ref.Decl.Kind != sema.DeclEntity {
		return nil // a procedure call or a declared component
	}
	end, ok := x.nameEnd(stmt, name)
	if !ok {
		return nil
	}
	at := stmt.Span().Start
	var out []server.CodeAction
	for _, e := range x.entitiesNamed(name, u) {
		lib := x.libraryName(e, u)
		l := ""
		if !label.IsZero() {
			l = label.Name().Text()
		}
		if l == "" {
			l = x.freshLabel(u, at, "u_"+basic(e.Decl.Text, "dut"))
		}
		gs, ps := formals(e.Region)
		text := instanceText(l, lib, e.Decl.Text,
			x.associations(gs, at), x.associations(ps, at))
		edits := []server.Edit{x.replace(at, end, text)}
		if lk := sema.IdentKey(lib); lib != "work" && lk != "std" &&
			!names(x.an.Result, u, lk) {
			edits = append([]server.Edit{insertContext(u, x.text,
				"library "+lib+";", false, "")}, edits...)
		}
		out = append(out, server.CodeAction{
			Title: "Instantiate entity " + lib + "." + e.Decl.Text,
			Kind:  server.ActionRewrite,
			Edits: []server.FileEdit{{URI: x.uri, Edits: edits}},
		})
	}
	return out
}

// nameEnd returns where the text Instantiate entity replaces ends: the
// `;` after the name, else the name. Without the `;` the parser folds
// what follows (the next statement) into stmt as an error; that text
// is kept, and the action is not offered when it starts on the name's
// line, where the statement is more than a name.
func (x *gen) nameEnd(stmt, name cst.Node) (int, bool) {
	last := lastToken(name)
	end := last.Span().End
	next := last.Next()
	for next.Missing() {
		next = next.Next()
	}
	switch {
	case next.IsZero() || next.Span().Start >= stmt.Span().End:
		return end, true
	case next.Kind() == token.Semicolon && next.Parent().Equal(stmt):
		return next.Span().End, true
	}
	return end, bytes.IndexByte(x.text[end:next.Span().Start], '\n') >= 0
}

// declareComponent is Declare component on a component instantiation
// (`u0 : counter port map (...)`, `u0 : component counter ...`) whose
// name declares no component, for each entity of that name.
func (x *gen) declareComponent(stmt cst.Node, u *sema.Unit,
) []server.CodeAction {
	if stmt.Kind() != cst.KindComponentInstantiationStmt {
		return nil
	}
	iu := cst.ComponentInstantiationStmt{Node: stmt}.Unit()
	if iu.IsZero() || !iu.Node.Token(token.KwEntity, 0).IsZero() ||
		!iu.Node.Token(token.KwConfiguration, 0).IsZero() {
		return nil
	}
	name := iu.Name().Node
	if name.Kind() != cst.KindSimpleName {
		return nil
	}
	k := sema.TokenKey(lastToken(name))
	for _, d := range x.an.Result.LookupAt(x.path, stmt.Span().Start, k) {
		if d.Kind == sema.DeclComponent {
			return nil
		}
	}
	at, ok := declarationsEnd(u)
	if !ok {
		return nil
	}
	var out []server.CodeAction
	for _, e := range x.entitiesNamed(name, u) {
		if e.Library != u.Library {
			// The default binding looks for the component's entity in
			// the unit's library only: the component would be unbound.
			continue
		}
		ctx, ok := x.componentContext(e, u, at.off)
		if !ok {
			continue
		}
		ins := "\n\n" + componentText(e)
		if at.empty {
			ins = "\n" + componentText(e)
		}
		var edits []server.Edit
		if len(ctx) > 0 {
			edits = append(edits, insertContext(u, x.text,
				strings.Join(ctx, "\n"), false, ""))
		}
		out = append(out, server.CodeAction{
			Title: "Declare component " + e.Decl.Text,
			Kind:  server.ActionRewrite,
			Edits: []server.FileEdit{{URI: x.uri, Edits: append(edits,
				x.replace(at.off, at.off, ins))}},
		})
	}
	return out
}

// componentContext returns the library and use clauses unit u needs
// so that each name the generic and port declarations of entity e use,
// and each character of their string literals (LRM 9.3.2), denotes at
// off, where the component declaration goes, what it denotes in e. A
// name that only e's context clause makes visible needs `use
// LIB.PKG.all;` (`use LIB.PKG;` for a package named alone) or `library
// LIB;`. ok is false when a clause cannot do it: another declaration
// of the name is visible at off.
func (x *gen) componentContext(e, u *sema.Unit, off int,
) (clauses []string, ok bool) {
	var needs []need
	gs, ps := formals(e.Region)
	var spans []cst.Range
	for _, d := range slices.Concat(gs, ps) {
		spans = append(spans, d.Node.Span())
		needs = stringChars(needs, d.Value)
	}
	for _, ref := range e.Refs {
		if !slices.ContainsFunc(spans, func(s cst.Range) bool {
			return s.Start <= ref.Span.Start && ref.Span.End <= s.End
		}) {
			continue
		}
		d := ref.Decl
		if d == nil && ref.Overload != nil {
			d = ref.Overload.Chosen
		}
		tok := e.File.Root().TokenAt(ref.Span.Start)
		if d == nil || tok.Prev().Kind() == token.Dot {
			continue // a selected name's suffix
		}
		k := d.Name
		if d.Kind == sema.DeclLibrary {
			k = sema.TokenKey(tok) // work denotes the unit's library
		}
		needs = append(needs, need{d, k})
	}
	add := func(c string) {
		if !slices.Contains(clauses, c) {
			clauses = append(clauses, c)
		}
	}
	for _, n := range needs {
		d, k := n.d, n.k
		if d.Unit == e {
			continue // a formal: the component declares it too
		}
		vis := x.an.Result.LookupAt(x.path, off, k)
		switch {
		case slices.Contains(vis, d):
			continue
		case len(vis) > 0 && !d.Overloadable():
			return nil, false
		case d.Kind == sema.DeclLibrary:
			add("library " + string(d.Name) + ";")
			continue
		}
		p := d.Unit
		if p == nil || p.Region == nil || p.Decl == nil ||
			p.Decl.Kind == sema.DeclGenericPackage ||
			p.Kind != sema.UnitPackage && p.Kind != sema.UnitPackageInst {
			return nil, false
		}
		l, ok := x.an.Result.Library(p.Library)
		if !ok {
			return nil, false
		}
		lib := string(p.Library)
		if p.Library == u.Library && !l.Bundled {
			lib = "work"
		}
		use := "use " + lib + "." + p.Decl.Text
		switch {
		case d == p.Decl:
			use += ";"
		case d.Parent == p.Region:
			use += ".all;"
		default:
			return nil, false // in a nested region of the package
		}
		if l.Bundled {
			use = strings.ToLower(use)
		}
		if lib != "work" && lib != "std" &&
			!names(x.an.Result, u, p.Library) {
			add("library " + lib + ";")
		}
		add(use)
	}
	return clauses, true
}

// need is a declaration a copied text names, with the name as written.
type need struct {
	d *sema.Decl
	k sema.Key
}

// stringChars appends to ns, for each string literal in the expression
// e of an array type of an enumeration type, the enumeration literal of
// each of its characters.
func stringChars(ns []need, e *sema.Expr) []need {
	if e == nil {
		return ns
	}
	if e.Kind == sema.ExprLiteral && e.Lit.Kind() == token.StringLiteral &&
		e.Type != nil && e.Type.Base().Elem != nil {
		lits := e.Type.Base().Elem.Base().Literals
		body := e.Lit.Text()
		if len(body) >= 2 {
			body = body[1 : len(body)-1]
		}
		for i := range len(body) {
			k := sema.Key([]byte{'\'', body[i], '\''})
			for _, d := range lits {
				if d.Name == k && !slices.Contains(ns, need{d, k}) {
					ns = append(ns, need{d, k})
				}
			}
		}
	}
	ns = stringChars(ns, e.Prefix)
	for _, a := range e.Args {
		ns = stringChars(ns, a)
	}
	for _, a := range e.Assocs {
		for _, c := range a.Choices {
			ns = stringChars(ns, c)
		}
		ns = stringChars(ns, a.Value)
	}
	return ns
}

// completePortMap is Complete port map on an instance whose port map
// leaves formals out: each one is added after the last association
// (see associations), and the statement is laid out again.
func (x *gen) completePortMap(stmt cst.Node, u *sema.Unit,
) []server.CodeAction {
	if stmt.Kind() != cst.KindComponentInstantiationStmt {
		return nil
	}
	pm := cst.ComponentInstantiationStmt{Node: stmt}.PortMap()
	if pm.IsZero() {
		return nil
	}
	list := pm.List()
	lp, rp := list.Node.Token(token.LParen, 0), list.Node.Token(token.RParen, 0)
	if list.IsZero() || lp.IsZero() || lp.Missing() || rp.IsZero() ||
		rp.Missing() {
		return nil
	}
	s := instanceStmt(u.Body, stmt)
	if s == nil || s.Kind != sema.StmtInstance {
		return nil
	}
	owner := s.Component
	if owner == nil {
		owner = s.Entity
	}
	if owner == nil || owner.Region == nil {
		return nil
	}
	_, ports := formals(owner.Region)
	done := map[*sema.Decl]bool{}
	for _, a := range s.Ports {
		if a.Formal == nil {
			return nil // a map sema could not match: leave it alone
		}
		done[a.Formal] = true
	}
	var missing []*sema.Decl
	for _, p := range ports {
		if !done[p] {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	// The new associations follow the last one: on its line when the
	// map is on one line, else each on a line of its own after the
	// rest of the last one's line (a comment there stays with it).
	sp := stmt.Span()
	at := lp.Span().End
	sep := ""
	for it := range list.Items() {
		at, sep = it.Node.Span().End, ", "
	}
	as := x.associations(missing, sp.Start)
	full := string(x.text[sp.Start:at])
	rest := x.text[at:rp.Span().Start]
	if eol := bytes.IndexByte(rest, '\n'); eol >= 0 && sep != "" {
		indent := leading(x.text[lineStart(x.text, at):])
		full += "," + string(rest[:eol])
		for i, a := range as {
			full += "\n" + indent + a
			if i < len(as)-1 {
				full += ","
			}
		}
		full += string(x.text[at+eol : sp.End])
	} else {
		full += sep + strings.Join(as, ", ") + string(x.text[at:sp.End])
	}
	return []server.CodeAction{{
		Title: "Complete port map",
		Kind:  server.ActionRewrite,
		Edits: []server.FileEdit{{URI: x.uri, Edits: []server.Edit{
			x.replace(sp.Start, sp.End, full)}}},
	}}
}

// instanceStmt returns the statement of the tree ss whose node is n.
func instanceStmt(ss []*sema.Stmt, n cst.Node) *sema.Stmt {
	for _, s := range ss {
		if s == nil {
			continue
		}
		if s.Node.Span() == n.Span() && s.Node.Kind() == n.Kind() {
			return s
		}
		if sp := s.Node.Span(); sp.Start > n.Span().Start ||
			sp.End < n.Span().End {
			continue
		}
		if r := instanceStmt(s.Body, n); r != nil {
			return r
		}
		for _, a := range s.Alts {
			if r := instanceStmt(a.Body, n); r != nil {
				return r
			}
		}
	}
	return nil
}

// insertion is where a declaration goes: after the last declaration
// of a declarative part, or after `is` when it is empty.
type insertion struct {
	off   int
	empty bool
}

// declarationsEnd returns the end of the declarative part of the
// architecture u, after the comments on the line of its last token.
func declarationsEnd(u *sema.Unit) (insertion, bool) {
	ab := cst.ArchitectureBody{Node: u.Lib}
	if ab.IsZero() || u.Lib.Kind() != cst.KindArchitectureBody {
		return insertion{}, false
	}
	if dp := ab.Decls(); !dp.IsZero() {
		if t := dp.Node.LastToken(); !t.IsZero() && !t.Missing() &&
			t.Span().Len() > 0 {
			return insertion{off: afterComments(t)}, true
		}
	}
	is := u.Lib.Token(token.KwIs, 0)
	if is.IsZero() || is.Missing() {
		return insertion{}, false
	}
	return insertion{off: afterComments(is), empty: true}, true
}

// afterComments returns the end of t, or of the last comment after t
// on its line: text inserted there leaves the comment on t's line.
func afterComments(t cst.Token) int {
	off, end := t.Span().End, t.Span().End
	for _, tr := range t.Trailing() {
		off += len(tr.Text)
		if tr.Kind != token.Whitespace {
			end = off
		}
	}
	return end
}

// entitiesNamed returns the entities the name n written in u denotes:
// for L.N the entity N of library L (work is u's library); for a
// simple name N the entity N of u's library, else the entity N of
// each other project library, by library name. The entity of u itself
// and units that are not analyzed are left out.
func (x *gen) entitiesNamed(n cst.Node, u *sema.Unit) []*sema.Unit {
	var libs []sema.Key
	var name cst.Token
	switch n.Kind() {
	case cst.KindSimpleName:
		name = cst.SimpleName{Node: n}.Ident()
		libs = append(libs, u.Library)
	case cst.KindSelectedName:
		sn := cst.SelectedName{Node: n}
		p := sn.Prefix().Node
		if p.Kind() != cst.KindSimpleName {
			return nil
		}
		lib := sema.TokenKey(cst.SimpleName{Node: p}.Ident())
		if lib == sema.Work {
			lib = u.Library
		}
		name, libs = sn.Suffix(), append(libs, lib)
	default:
		return nil
	}
	if name.IsZero() || name.Missing() {
		return nil
	}
	k := sema.TokenKey(name)
	get := func(lk sema.Key) *sema.Unit {
		l, ok := x.an.Result.Library(lk)
		if !ok {
			return nil
		}
		e, ok := l.UnitFrom(k, u)
		if !ok || e.File == nil || e.File.Bundled ||
			e.Kind != sema.UnitEntity || e.Duplicate ||
			e.Opaque || e.Region == nil || e.Decl == nil ||
			e.Library == u.Library && e.Name == u.Of {
			return nil
		}
		return e
	}
	if e := get(libs[0]); e != nil || n.Kind() == cst.KindSelectedName {
		if e == nil {
			return nil
		}
		return []*sema.Unit{e}
	}
	var out []*sema.Unit
	for _, l := range x.an.Result.Libraries {
		if l.Name == u.Library {
			continue
		}
		if e := get(l.Name); e != nil {
			out = append(out, e)
		}
	}
	return out // Libraries are sorted by name
}

// libraryName is how unit u names the library of entity e: work for
// its own library.
func (x *gen) libraryName(e, u *sema.Unit) string {
	if e.Library == u.Library {
		return "work"
	}
	return string(e.Library)
}

// freshLabel returns base, or base_2, base_3, ... when the name is
// declared in u or visible at off.
func (x *gen) freshLabel(u *sema.Unit, off int, base string) string {
	taken := func(s string) bool {
		k := sema.IdentKey(s)
		for _, d := range u.Decls {
			if d.Name == k {
				return true
			}
		}
		return len(x.an.Result.LookupAt(x.path, off, k)) > 0
	}
	l := base
	for i := 2; taken(l); i++ {
		l = base + "_" + strconv.Itoa(i)
	}
	return l
}

// replace returns the edit replacing [s, e) of the document with ins,
// laid out as `gohard fmt` lays it out in the edited document: the
// document with the replacement is formatted inside the replaced range
// only. A replacement that starts a line takes the line's indentation
// in, so the formatter indents it.
func (x *gen) replace(s, e int, ins string) server.Edit {
	if ls := lineStart(x.text, s); strings.TrimSpace(
		string(x.text[ls:s])) == "" {
		ins, s = string(x.text[ls:s])+ins, ls
	}
	doc := slices.Concat(x.text[:s], []byte(ins), x.text[e:])
	res := parser.Parse(doc, x.popts)
	out := format.Apply(doc, format.Range(res, x.fopts, s, s+len(ins)))
	return server.Edit{Span: server.Span{Start: s, End: e},
		NewText: string(out[s : len(out)-(len(x.text)-e)])}
}

// formals returns the generics and the ports of an entity or
// component region, in order.
func formals(r *sema.Region) (generics, ports []*sema.Decl) {
	for _, d := range r.Decls {
		switch d.Kind {
		case sema.DeclGeneric, sema.DeclGenericType,
			sema.DeclGenericSubprogram, sema.DeclGenericPackage:
			generics = append(generics, d)
		case sema.DeclPort:
			ports = append(ports, d)
		}
	}
	return generics, ports
}

// associations returns `formal => actual` for each formal, as an
// instance at off writes them: the formal's own name when an object
// of that name is visible there; else open when the formal has a
// default or is a port of a mode other than in; else the formal's name,
// which the user then declares.
func (x *gen) associations(ds []*sema.Decl, off int) []string {
	var out []string
	for _, d := range ds {
		actual := d.Text
		if !x.objectVisible(off, d.Name) && (hasDefault(d) ||
			d.Kind == sema.DeclPort && d.Mode != sema.ModeIn) {
			actual = "open"
		}
		out = append(out, d.Text+" => "+actual)
	}
	return out
}

// objectVisible reports whether an object named k is visible at off.
func (x *gen) objectVisible(off int, k sema.Key) bool {
	for _, d := range x.an.Result.LookupAt(x.path, off, k) {
		switch d.Kind {
		case sema.DeclConstant, sema.DeclSignal, sema.DeclVariable,
			sema.DeclSharedVariable, sema.DeclAlias, sema.DeclGeneric,
			sema.DeclPort, sema.DeclLoopParam, sema.DeclGenerateParam:
			return true
		}
	}
	return false
}

// hasDefault reports whether the interface declaration of d gives a
// default: `:= expr` of an object, `is name` or `is <>` of a
// subprogram.
func hasDefault(d *sema.Decl) bool {
	switch d.Node.Kind() {
	case cst.KindInterfaceObjectDecl:
		return !d.Node.Token(token.VarAssign, 0).IsZero()
	case cst.KindInterfaceSubprogramDecl:
		return !cst.InterfaceSubprogramDecl{Node: d.Node}.Default().IsZero()
	}
	return false
}

// instanceText is an entity instantiation with the given generic and
// port associations, laid out for the formatter.
func instanceText(label, lib, name string, generics, ports []string,
) string {
	var b strings.Builder
	b.WriteString(label + " : entity " + lib + "." + name)
	assocList(&b, "generic map", generics)
	assocList(&b, "port map", ports)
	b.WriteString(";")
	return b.String()
}

func assocList(b *strings.Builder, head string, as []string) {
	if len(as) == 0 {
		return
	}
	b.WriteString("\n  " + head + " (\n")
	for i, a := range as {
		b.WriteString("    " + a)
		if i < len(as)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("  )")
}

// componentText is the component declaration of entity e: its
// generic and port declarations as written (comments dropped).
func componentText(e *sema.Unit) string {
	var b strings.Builder
	b.WriteString("component " + e.Decl.Text + " is")
	gs, ps := formals(e.Region)
	for _, part := range []struct {
		head string
		ds   []*sema.Decl
	}{{"generic", gs}, {"port", ps}} {
		var items []string
		var last cst.Node
		for _, d := range part.ds {
			if d.Node.IsZero() || d.Node.Equal(last) {
				continue
			}
			last = d.Node
			items = append(items, flat(d.Node))
		}
		if len(items) == 0 {
			continue
		}
		b.WriteString("\n  " + part.head + " (\n")
		for i, it := range items {
			b.WriteString("    " + it)
			if i < len(items)-1 {
				b.WriteString(";")
			}
			b.WriteString("\n")
		}
		b.WriteString("  );")
	}
	b.WriteString("\nend component " + e.Decl.Text + ";")
	return b.String()
}

// flat is the text of n's tokens, one space wherever the source has
// trivia between two of them (so comments are dropped).
func flat(n cst.Node) string {
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

// lastToken returns the last token of n that is not missing.
func lastToken(n cst.Node) cst.Token {
	var last cst.Token
	for t := range n.DescendantTokens() {
		if !t.Missing() {
			last = t
		}
	}
	return last
}

// basic returns s when it is a basic identifier, else alt.
func basic(s, alt string) string {
	if strings.HasPrefix(s, `\`) {
		return alt
	}
	return s
}

// createTestbench is Create testbench for the entity whose design
// unit (context clause included) holds r.
func (x *gen) createTestbench(r server.Span) []server.CodeAction {
	e := unitAt(x.f, r.Start)
	if e == nil || e.Kind != sema.UnitEntity || e.Duplicate || e.Opaque ||
		e.Unchecked || e.Region == nil || e.Decl == nil ||
		e.File == nil || strings.HasPrefix(e.Decl.Text, `\`) {
		return nil
	}
	tb, ok := testbench(e, x.an.Result.Standard)
	if !ok {
		return nil
	}
	name := e.Decl.Text + "_tb"
	rel := path.Join(path.Dir(e.File.Path), name+".vhd")
	euri, ok := x.an.URI(e.File.Path)
	if !ok {
		return nil
	}
	edoc, ok := x.an.Snap.Document(euri)
	if !ok || edoc.Path == "" {
		return nil
	}
	uri := server.PathToURI(filepath.Join(filepath.Dir(edoc.Path),
		name+".vhd"))
	if _, ok := x.an.Snap.Document(uri); ok {
		return nil
	}
	if _, ok := x.an.Result.File(rel); ok {
		return nil
	}
	if l, ok := x.an.Result.Library(e.Library); ok {
		if _, ok := l.Unit(sema.IdentKey(name)); ok {
			return nil
		}
	}
	cfg := Config(x.an.Snap)
	if !analyzed(cfg, rel) || sema.LibraryOf(cfg, rel) != e.Library {
		return nil
	}
	text := []byte(tb)
	res := parser.Parse(text, x.popts)
	text = format.Format(res, x.fopts).Text
	return []server.CodeAction{{
		Title: "Create testbench " + name + ".vhd",
		Kind:  server.ActionSource,
		Edits: []server.FileEdit{{URI: uri, Create: true,
			Edits: []server.Edit{{NewText: string(text)}}}},
	}}
}

// analyzed reports whether a file at the project-relative slash path
// p would be analyzed, as `gohard check` discovers files: below the
// root, outside folders starting with '.', which the walk skips; above
// it, in a folder outside the root that gohard.yaml lists under
// libraries (cfg as Config gives it, root-relative), outside such
// folders below it.
func analyzed(cfg config.Config, p string) bool {
	hidden := func(rel string) bool {
		for _, part := range strings.Split(path.Dir(rel), "/") {
			if strings.HasPrefix(part, ".") && part != "." {
				return true
			}
		}
		return false
	}
	if !strings.HasPrefix(p, "../") {
		return !hidden(p)
	}
	for _, l := range cfg.Libraries {
		for _, f := range l.Folders {
			dir := path.Clean(f.Path)
			if !strings.HasPrefix(dir, "../") || workspace.IsVHDL(dir) ||
				!strings.HasPrefix(p, dir+"/") {
				continue
			}
			if !hidden(strings.TrimPrefix(p, dir+"/")) {
				return true
			}
		}
	}
	return false
}
