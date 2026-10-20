package ide

import (
	"errors"
	"strings"

	"github.com/connerohnesorge/gohard/internal/resolve"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/server"
)

// Navigation (#25): hover, go-to-definition, references and rename,
// across files, libraries and into the bundled std/ieee sources.

func init() {
	provide(server.FeatureHover | server.FeatureDefinition |
		server.FeatureReferences | server.FeatureRename)
}

// BundleScheme is the URI scheme of the bundled std/ieee sources
// (#33): gohard-bundle:///ieee/numeric_std.vhdl. They are not
// snapshot documents; the editor opens them read-only with the text
// from BundledSource.
const BundleScheme = "gohard-bundle"

// BundledURI returns the URI of the bundled file at bundle path p.
func BundledURI(p string) string { return BundleScheme + ":///" + p }

// BundledSource returns the text of the bundled file at uri.
func BundledSource(uri string) ([]byte, bool) {
	p, ok := strings.CutPrefix(uri, BundleScheme+":///")
	if !ok {
		return nil, false
	}
	f, ok := sema.Bundle().File(p)
	if !ok || !f.Bundled {
		return nil, false
	}
	return f.Text, true
}

// LocationAll converts a resolve location, a bundled one included
// (with its URI from BundledURI and the line index of its text).
func (an *Analysis) LocationAll(l resolve.Location,
) (server.Location, bool) {
	if !l.Bundled {
		return an.Location(l)
	}
	f, ok := an.Result.File(l.Path)
	if !ok || !f.Bundled {
		return server.Location{}, false
	}
	return server.Location{URI: BundledURI(l.Path), Span: Span(l.Span),
		Lines: f.Lines}, true
}

// LocationsAll converts locations with LocationAll, in order.
func (an *Analysis) LocationsAll(ls []resolve.Location,
) []server.Location {
	var out []server.Location
	for _, l := range ls {
		if sl, ok := an.LocationAll(l); ok {
			out = append(out, sl)
		}
	}
	return out
}

// HoverAt is the hover of q: what the name denotes, its doc comment,
// and for a call or operator the overload result (resolve.HoverAt).
func (an *Analysis) HoverAt(q server.Query) *server.Hover {
	path, off, ok := an.query(q)
	if !ok {
		return nil
	}
	h, ok := resolve.HoverAt(an.Result, path, off)
	if !ok {
		return nil
	}
	sp := Span(h.Span)
	return &server.Hover{Markdown: h.Markdown, Span: &sp}
}

// Hover implements server.Analyzer.
func (a *Analyzer) Hover(q server.Query) server.Task[*server.Hover] {
	return Query(a, q.Snap, func(an *Analysis) *server.Hover {
		return an.HoverAt(q)
	})
}

// Definition implements server.Analyzer: the declaration in any
// project file or bundled std/ieee source.
func (a *Analyzer) Definition(q server.Query,
) server.Task[[]server.Location] {
	return Query(a, q.Snap, func(an *Analysis) []server.Location {
		return an.LocationsAll(an.DefinitionAll(q))
	})
}

// ReferencesAll returns References with the bundled declaration of a
// std/ieee name (includeDecl) kept.
func (an *Analysis) ReferencesAll(q server.Query,
	includeDecl bool,
) []server.Location {
	path, off, ok := an.query(q)
	if !ok {
		return nil
	}
	return an.LocationsAll(resolve.References(an.Result, path, off,
		includeDecl))
}

// References implements server.Analyzer.
func (a *Analyzer) References(q server.Query, includeDecl bool,
) server.Task[[]server.Location] {
	return Query(a, q.Snap, func(an *Analysis) []server.Location {
		return an.ReferencesAll(q, includeDecl)
	})
}

// PrepareRenameAt is the span of the renamable name at q, or nil.
func (an *Analysis) PrepareRenameAt(q server.Query) *server.Span {
	path, off, ok := an.query(q)
	if !ok {
		return nil
	}
	r, ok := resolve.PrepareRename(an.Result, path, off)
	if !ok {
		return nil
	}
	sp := Span(r)
	return &sp
}

// PrepareRename implements server.Analyzer.
func (a *Analyzer) PrepareRename(q server.Query) server.Task[*server.Span] {
	return Query(a, q.Snap, func(an *Analysis) *server.Span {
		return an.PrepareRenameAt(q)
	})
}

// RenameAt is the workspace-wide rename of the name at q to newName
// (resolve.Rename): edits grouped per document, documents by URI, or
// why the rename is refused.
func (an *Analysis) RenameAt(q server.Query, newName string,
) server.Renamed {
	path, off, ok := an.query(q)
	if !ok {
		return server.Renamed{Refused: "cannot rename " +
			strings.TrimSpace(newName) + ": the document is not analyzed"}
	}
	edits, err := resolve.Rename(an.Result, path, off, newName)
	if err != nil {
		return server.Renamed{Refused: refusal(an, q, newName, err)}
	}
	var out []server.FileEdit
	for _, e := range edits {
		uri, ok := an.URI(e.Path)
		if !ok {
			continue
		}
		if len(out) == 0 || out[len(out)-1].URI != uri {
			out = append(out, server.FileEdit{URI: uri})
		}
		fe := &out[len(out)-1]
		fe.Edits = append(fe.Edits, server.Edit{Span: Span(e.Span),
			NewText: e.NewText})
	}
	return server.Renamed{Edits: out}
}

func refusal(an *Analysis, q server.Query, newName string,
	err error,
) string {
	if errors.Is(err, resolve.ErrBadName) {
		return "cannot rename to " + newName + ": " + err.Error()
	}
	if errors.Is(err, resolve.ErrNotRenamable) {
		return "cannot rename: " + err.Error()
	}
	name := "this name"
	if t, ok := an.At(q); ok && t.Decl != nil {
		name = t.Decl.Text
	}
	return "cannot rename " + name + ": " + err.Error()
}

// Rename implements server.Analyzer.
func (a *Analyzer) Rename(q server.Query, newName string,
) server.Task[server.Renamed] {
	return Query(a, q.Snap, func(an *Analysis) server.Renamed {
		return an.RenameAt(q, newName)
	})
}
