package ide

import (
	"cmp"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/server"
	"github.com/connerohnesorge/gohard/internal/token"
)

func init() { provide(server.FeatureCodeActions) }

// CodeActions implements server.Analyzer (Analysis.QuickFixes).
func (a *Analyzer) CodeActions(q server.Query, r server.Span,
) server.Task[[]server.CodeAction] {
	return Query(a, q.Snap, func(an *Analysis) []server.CodeAction {
		return an.QuickFixes(q.URI, r)
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
//     the name, project libraries first, then std and ieee.
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
				if a, ok := an.mapLibrary(path, s); ok {
					as = append(as, a)
				}
			case diag.SemaUndefined:
				as = an.addUse(path, uri, doc.Text(), s, e)
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

// mapLibrary is the fix of the unmapped library named at off.
func (an *Analysis) mapLibrary(path string, off int,
) (server.CodeAction, bool) {
	fix, ok := an.Result.LibraryFixAt(path, off)
	if !ok {
		return server.CodeAction{}, false
	}
	cfg, present := an.Snap.ConfigDocument()
	if cfg == nil {
		return server.CodeAction{}, false
	}
	var text []byte
	if present {
		text = cfg.Text()
	}
	ed, ok := LibrariesEdit(text, fix.Entry)
	if !ok {
		return server.CodeAction{}, false
	}
	return server.CodeAction{
		Title: "Map library " + fix.Library + " to " + fix.Folder +
			" in gohard.yaml",
		Kind: "quickfix", Preferred: true,
		Edits: []server.FileEdit{{URI: cfg.URI,
			Edits: []server.Edit{ed}}},
	}, true
}

// LibrariesEdit returns the edit of the gohard.yaml text that adds
// entry ("lib: [folder]") under the top-level libraries key: a new
// line right after a block-style key (indented like its first entry),
// `{}` replaced by a block, or a new libraries block at the end when
// the key is missing. ok is false for a non-empty flow mapping, which
// the fix does not rewrite.
func LibrariesEdit(text []byte, entry string) (server.Edit, bool) {
	lines := strings.SplitAfter(string(text), "\n")
	off := 0
	for i, l := range lines {
		body := strings.TrimRight(l, "\r\n")
		rest, ok := strings.CutPrefix(body, "libraries")
		if !ok {
			off += len(l)
			continue
		}
		rest = strings.TrimLeft(rest, " \t")
		if !strings.HasPrefix(rest, ":") {
			off += len(l)
			continue
		}
		val := strings.TrimSpace(stripComment(rest[1:]))
		indent := "  "
		for _, n := range lines[i+1:] {
			t := strings.TrimLeft(n, " \t")
			if t == "" || t == "\n" || t == "\r\n" ||
				strings.HasPrefix(t, "#") {
				continue
			}
			if w := len(n) - len(t); w > 0 {
				indent = n[:w]
			}
			break
		}
		switch val {
		case "":
			if len(body) == len(l) { // the last line, no newline
				return server.Edit{Span: server.Span{Start: off + len(l),
					End: off + len(l)}, NewText: "\n" + indent + entry +
					"\n"}, true
			}
			at := off + len(l)
			return server.Edit{Span: server.Span{Start: at, End: at},
				NewText: indent + entry + "\n"}, true
		case "{}":
			colon := off + strings.Index(l, ":")
			return server.Edit{Span: server.Span{Start: colon + 1,
				End: off + len(body)},
				NewText: "\n  " + entry}, true
		}
		return server.Edit{}, false
	}
	nl := ""
	if len(text) > 0 && text[len(text)-1] != '\n' {
		nl = "\n"
	}
	return server.Edit{Span: server.Span{Start: len(text),
		End: len(text)}, NewText: nl + "libraries:\n  " + entry + "\n"}, true
}

func stripComment(s string) string {
	if i := strings.Index(s, "#"); i >= 0 {
		return s[:i]
	}
	return s
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
			Kind:  "quickfix", Preferred: len(cands) == 1,
			Edits: []server.FileEdit{{URI: uri,
				Edits: []server.Edit{ed}}},
		})
	}
	return out
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
// last clause, indented like it, or before the unit when it has none.
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
			end := last.Span().End
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
