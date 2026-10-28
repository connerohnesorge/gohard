package ide

import (
	"cmp"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/libs"
	"github.com/connerohnesorge/gohard/v2/internal/lint"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/server"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

func init() { provide(server.FeatureCodeActions) }

// CodeActions implements server.Analyzer: the quick fixes
// (Analysis.QuickFixes), then the generated rewrites and source
// actions (Analysis.Refactors, #57).
func (a *Analyzer) CodeActions(q server.Query, r server.Span, create bool,
) server.Task[[]server.CodeAction] {
	return Query(a, q.Snap, func(an *Analysis) []server.CodeAction {
		return slices.Concat(an.QuickFixes(q.URI, r),
			an.Refactors(q.URI, r, Generate{
				ToolVersion: a.opts.ToolVersion, Create: create}))
	})
}

// QuickFixes returns the quick fixes of the diagnostics of document
// uri that touch r (#25, #34), in diagnostic order:
//
//   - library/unmapped: map the library in gohard.yaml, writing the
//     libraries line sema proposes (sema.Result.LibraryFixAt, the
//     search of `gohard libs detect`);
//   - sema/undefined: add `use LIB.PKG.all;` (and `library LIB;` when
//     the unit does not name LIB yet) for every package that declares
//     the name, project libraries first, then std and ieee;
//   - lint/sensitivity-list: add the signal to the process's
//     sensitivity list (#54).
func (an *Analysis) QuickFixes(uri string, r server.Span,
) []server.CodeAction {
	path, ok := an.Path(uri)
	if !ok || an.Result == nil {
		return nil
	}
	doc, _ := an.Snap.Document(uri)
	x := doc.Index()
	var out []server.CodeAction
	seen := map[string]bool{}
	for _, fd := range an.Diagnostics {
		if fd.URI != uri {
			continue
		}
		for _, d := range fd.Diagnostics {
			s, _ := x.OffsetOf(d.Line, d.Col)
			e := s
			if d.EndLine > 0 {
				e, _ = x.OffsetOf(d.EndLine, d.EndCol)
			}
			if s > r.End || r.Start > e {
				continue
			}
			var as []server.CodeAction
			switch d.Code {
			case diag.LibraryUnmapped:
				as = an.mapLibrary(path, s)
			case diag.SemaUndefined:
				as = an.addUse(path, uri, doc.Text(), s, e)
			case diag.LintSensitivityList:
				as = an.addSensitivity(path, uri, doc.Text(), s, e)
			}
			for _, a := range as {
				if !seen[a.Title] {
					seen[a.Title] = true
					out = append(out, a)
				}
			}
		}
	}
	return out
}

// mapLibrary is the fix of the unmapped library named at off: the
// project folder sema proposes, else every install of the library
// that the search of `gohard libs detect` finds (#1), one action each.
func (an *Analysis) mapLibrary(path string, off int) []server.CodeAction {
	cfg, present := an.Snap.ConfigDocument()
	if cfg == nil {
		return nil
	}
	var text []byte
	if present {
		text = cfg.Text()
	}
	action := func(title, entry string, preferred bool,
	) []server.CodeAction {
		ed, ok := LibrariesEdit(text, entry)
		if !ok {
			return nil
		}
		return []server.CodeAction{{Title: title, Kind: server.ActionQuickFix,
			Preferred: preferred, Edits: []server.FileEdit{{
				URI: cfg.URI, Edits: []server.Edit{ed}}}}}
	}
	if fix, ok := an.Result.LibraryFixAt(path, off); ok {
		return action("Map library "+fix.Library+" to "+fix.Folder+
			" in gohard.yaml", fix.Entry, true)
	}
	name, ok := an.Result.UnmappedLibraryAt(path, off)
	if !ok || an.libs.FS == nil {
		return nil
	}
	cands := libs.Find(an.libs, an.Snap.Root, name)
	var out []server.CodeAction
	for _, c := range cands {
		out = append(out, action("Map library "+name+" to "+
			c.Install.Label()+" in gohard.yaml", name+": ["+
			libs.Quote(c.Library.Folder)+"]", len(cands) == 1)...)
	}
	return out
}

// LibrariesEdit returns the edit of the gohard.yaml text that adds
// entry ("lib: [folder]") under the top-level libraries key, the edit
// `gohard libs detect --write` makes (libs.LibrariesEdit). ok is false
// for a non-empty flow mapping, which the fix does not rewrite.
func LibrariesEdit(text []byte, entry string) (server.Edit, bool) {
	e, ok := libs.LibrariesEdit(text, entry)
	return server.Edit{Span: server.Span{Start: e.Start, End: e.End},
		NewText: e.NewText}, ok
}

// usePackage is a package that declares a name.
type usePackage struct {
	lib     sema.Key
	pkg     *sema.Unit
	bundled bool
}

// addUse is the fix of an undefined simple name at [s, e).
func (an *Analysis) addUse(path, uri string, text []byte, s, e int,
) []server.CodeAction {
	if e <= s || e > len(text) {
		return nil
	}
	name := string(text[s:e])
	k := sema.KeyOf(token.Identifier, name)
	if s > 0 && text[s-1] == '.' || k.IsOperator() || k.IsCharacter() {
		return nil // a selected name: its prefix is the problem
	}
	f, ok := an.Result.File(path)
	if !ok {
		return nil
	}
	var u *sema.Unit
	for _, x := range f.Units {
		if sp := x.Node.Span(); sp.Start <= s && e <= sp.End {
			u = x
		}
	}
	if u == nil {
		return nil
	}
	var cands []usePackage
	for _, l := range an.Result.Libraries {
		for _, p := range l.Units() {
			if p.Kind != sema.UnitPackage || p.Duplicate ||
				p.Region == nil || p == u {
				continue
			}
			if p.Decl != nil && p.Decl.Kind == sema.DeclGenericPackage {
				continue // must be instantiated first
			}
			if declares(p.Region, k, an.Result.Standard) {
				cands = append(cands, usePackage{lib: l.Name, pkg: p,
					bundled: l.Bundled})
			}
		}
	}
	slices.SortStableFunc(cands, func(a, b usePackage) int {
		return cmp.Or(boolCmp(a.bundled, b.bundled),
			cmp.Compare(a.lib, b.lib), cmp.Compare(a.pkg.Name, b.pkg.Name))
	})
	var out []server.CodeAction
	for _, c := range cands {
		lib := string(c.lib)
		if c.lib == f.Library && !c.bundled {
			lib = "work"
		}
		clause := "use " + lib + "." + string(c.pkg.Decl.Text) + ".all;"
		if c.bundled {
			clause = strings.ToLower(clause)
		}
		needLib := lib != "work" && lib != "std" &&
			!names(an.Result, u, c.lib)
		ed := insertContext(u, text, clause, needLib, lib)
		out = append(out, server.CodeAction{
			Title: "Add " + strings.TrimSuffix(clause, ";"),
			Kind:  server.ActionQuickFix, Preferred: len(cands) == 1,
			Edits: []server.FileEdit{{URI: uri,
				Edits: []server.Edit{ed}}},
		})
	}
	return out
}

// addSensitivity is the fix of a signal read at [s, e) that the
// enclosing process's sensitivity list does not name: append the name
// the finding names (lint.ListName), the signal's for an alias
// declared in the process.
func (an *Analysis) addSensitivity(path, uri string, text []byte, s, e int,
) []server.CodeAction {
	f, ok := an.Result.File(path)
	if !ok || e <= s || e > len(text) {
		return nil
	}
	for n := range f.Root().Covering(s, e).Ancestors() {
		if n.Kind() != cst.KindProcessStmt {
			continue
		}
		var last cst.Token
		for t := range (cst.ProcessStmt{Node: n}).Sensitivity().Tokens() {
			last = t
		}
		if last.Kind() != token.RParen {
			return nil
		}
		name := string(text[s:e])
		if ref, ok := an.Result.RefAt(path, s); ok && ref.Decl != nil {
			name = lint.ListName(f, n, ref.Decl)
		}
		at := last.Span().Start
		return []server.CodeAction{{
			Title: "Add " + name + " to the sensitivity list",
			Kind:  server.ActionQuickFix, Preferred: true,
			Edits: []server.FileEdit{{URI: uri, Edits: []server.Edit{{
				Span: server.Span{Start: at, End: at}, NewText: ", " + name,
			}}}},
		}}
	}
	return nil
}

func boolCmp(a, b bool) int {
	switch {
	case a == b:
		return 0
	case !a:
		return -1
	}
	return 1
}

// declares reports whether the package region reg declares a name k
// visible under std.
func declares(reg *sema.Region, k sema.Key, std token.Standard) bool {
	for _, d := range reg.Lookup(k) {
		if !d.Implicit && (d.First == 0 || d.First <= std) {
			return true
		}
	}
	return false
}

// names reports whether the context clause of u, or of its primary
// unit when u is an architecture or package body, names library k.
func names(r *sema.Result, u *sema.Unit, k sema.Key) bool {
	if u.Context != nil {
		if _, ok := u.Context.Libs[k]; ok {
			return true
		}
	}
	if u.Kind.Primary() {
		return false
	}
	l, ok := r.Library(u.Library)
	if !ok {
		return false
	}
	p, ok := l.Unit(u.Of)
	return ok && p != u && names(r, p, k)
}

// insertContext returns the edit adding clause (after `library lib;`
// when needLib) to the context clause of u: on a new line after its
// last clause (and the comments on its line), indented like it, or
// before the unit when it has none.
func insertContext(u *sema.Unit, text []byte, clause string,
	needLib bool, lib string,
) server.Edit {
	add := clause
	if needLib {
		add = "library " + lib + ";\n" + clause
	}
	var ctx cst.Node
	for c := range u.Node.Nodes() {
		if c.Kind() == cst.KindContextClause {
			ctx = c
		}
	}
	if !ctx.IsZero() {
		if last := ctx.LastToken(); !last.IsZero() && !last.Missing() &&
			last.Span().End > last.Span().Start {
			end := afterComments(last)
			first := ctx.FirstToken().Span().Start
			indent := leading(text[lineStart(text, first):])
			var b strings.Builder
			for _, l := range strings.Split(add, "\n") {
				b.WriteString("\n" + indent + l)
			}
			return server.Edit{Span: server.Span{Start: end, End: end},
				NewText: b.String()}
		}
	}
	at := u.Node.FirstToken().Span().Start
	ls := lineStart(text, at)
	indent := leading(text[ls:])
	var b strings.Builder
	for _, l := range strings.Split(add, "\n") {
		b.WriteString(indent + l + "\n")
	}
	b.WriteString("\n")
	return server.Edit{Span: server.Span{Start: ls, End: ls},
		NewText: b.String()}
}

func lineStart(text []byte, off int) int {
	for off > 0 && text[off-1] != '\n' {
		off--
	}
	return off
}

func leading(b []byte) string {
	n := 0
	for n < len(b) && (b[n] == ' ' || b[n] == '\t') {
		n++
	}
	return string(b[:n])
}
