package ide

import (
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/resolve"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/server"
)

// Analysis is the analyzed workspace of one snapshot and the query
// layer every feature uses. It is immutable.
type Analysis struct {
	Snap *server.Snapshot
	// Result is the sema result of the snapshot's files; nil when the
	// snapshot holds no VHDL file.
	Result *sema.Result
	// Diagnostics are the published diagnostics, by URI.
	Diagnostics []server.FileDiagnostics

	key   string
	uriOf map[string]string // sema path -> URI
}

// at returns the analysis for another snapshot of the same content.
func (an *Analysis) at(snap *server.Snapshot) *Analysis {
	c := *an
	c.Snap = snap
	return &c
}

// Path returns the sema path (the project-relative slash path) of the
// document uri; ok is false for a document that is not analyzed.
func (an *Analysis) Path(uri string) (string, bool) {
	d, ok := an.Snap.Document(uri)
	if !ok || d.Rel == "" {
		return "", false
	}
	if _, ok := an.uriOf[d.Rel]; !ok {
		return "", false
	}
	return d.Rel, true
}

// URI returns the document URI of a sema path; ok is false for a
// bundled std/ieee file or a path outside the snapshot.
func (an *Analysis) URI(path string) (string, bool) {
	uri, ok := an.uriOf[path]
	return uri, ok
}

// Location converts a resolve location to a server location; ok is
// false for a bundled std/ieee location (not a snapshot document).
func (an *Analysis) Location(l resolve.Location) (server.Location, bool) {
	if l.Bundled {
		return server.Location{}, false
	}
	uri, ok := an.URI(l.Path)
	if !ok {
		return server.Location{}, false
	}
	return server.Location{URI: uri, Span: Span(l.Span)}, true
}

// Locations converts resolve locations, dropping bundled ones; the
// order is kept (resolve sorts by path and position).
func (an *Analysis) Locations(ls []resolve.Location) []server.Location {
	var out []server.Location
	for _, l := range ls {
		if sl, ok := an.Location(l); ok {
			out = append(out, sl)
		}
	}
	return out
}

// Span converts a cst range to a server span.
func Span(r cst.Range) server.Span {
	return server.Span{Start: r.Start, End: r.End}
}

// Target is what a position denotes.
type Target struct {
	// Path is the sema path of the queried document.
	Path string
	// Span is the name under the cursor: an occurrence or the defining
	// name of a declaration.
	Span server.Span
	// Ref is the occurrence when IsRef; otherwise the position is the
	// defining name of Decl.
	Ref   sema.Ref
	IsRef bool
	// Decl is the declaration denoted; nil for an occurrence whose
	// overload is not decided (Ref.Overload holds the candidates).
	Decl *sema.Decl
}

// query returns the sema path and byte offset of q (documents are
// analyzed as they are, so the offset is q.Offset).
func (an *Analysis) query(q server.Query) (string, int, bool) {
	if an.Result == nil {
		return "", 0, false
	}
	if p, ok := an.Path(q.URI); ok {
		return p, q.Offset, true
	}
	// A bundled file the editor opened (its cached copy, #33).
	if p, ok := server.BundlePath(q.URI); ok {
		if f, ok := an.Result.File(p); ok && f.Bundled {
			return p, q.Offset, true
		}
	}
	return "", 0, false
}

// At returns the occurrence or defining name at q (position ->
// occurrence -> declaration).
func (an *Analysis) At(q server.Query) (Target, bool) {
	path, off, ok := an.query(q)
	if !ok {
		return Target{}, false
	}
	if ref, ok := an.Result.RefAt(path, off); ok {
		return Target{Path: path, Span: Span(ref.Span), Ref: ref,
			IsRef: true, Decl: ref.Decl}, true
	}
	if d, ok := an.Result.DeclAt(path, off); ok {
		return Target{Path: path, Span: Span(d.Span), Decl: d}, true
	}
	return Target{}, false
}

// Definition returns where the name at q is declared, in any document
// of the snapshot; an undecided overload gives every fitting
// candidate (every candidate when none fits). Bundled declarations
// are left out; DefinitionAll keeps them.
func (an *Analysis) Definition(q server.Query) []server.Location {
	return an.Locations(an.DefinitionAll(q))
}

// DefinitionAll is Definition with bundled std/ieee locations.
func (an *Analysis) DefinitionAll(q server.Query) []resolve.Location {
	path, off, ok := an.query(q)
	if !ok {
		return nil
	}
	return resolve.Definition(an.Result, path, off)
}

// References returns every occurrence across the workspace of the
// entity named at q (a declaration with its body or full declaration
// counts as one entity), sorted by URI path and position; with
// includeDecl the declarations themselves are listed.
func (an *Analysis) References(q server.Query,
	includeDecl bool,
) []server.Location {
	path, off, ok := an.query(q)
	if !ok {
		return nil
	}
	return an.Locations(resolve.References(an.Result, path, off,
		includeDecl))
}

// ReferencesOf returns the references of the declaration d (see
// References); nil for a declaration without a project location.
func (an *Analysis) ReferencesOf(d *sema.Decl,
	includeDecl bool,
) []server.Location {
	l, ok := resolve.LocationOf(d)
	if !ok || l.Bundled || an.Result == nil {
		return nil
	}
	return an.Locations(resolve.References(an.Result, l.Path,
		l.Span.Start, includeDecl))
}

// Visible returns every declaration visible at q, in a deterministic
// order (for completion).
func (an *Analysis) Visible(q server.Query) []*sema.Decl {
	path, off, ok := an.query(q)
	if !ok {
		return nil
	}
	return an.Result.VisibleAt(path, off)
}

// Lookup returns the declarations named k visible at q.
func (an *Analysis) Lookup(q server.Query, k sema.Key) []*sema.Decl {
	path, off, ok := an.query(q)
	if !ok {
		return nil
	}
	return an.Result.LookupAt(path, off, k)
}

// DocumentSymbols returns the declarations of the document uri as a
// tree: design units, then their declarations and nested regions.
func (an *Analysis) DocumentSymbols(uri string) []server.Symbol {
	path, ok := an.Path(uri)
	if !ok || an.Result == nil {
		return nil
	}
	return an.symbols(resolve.DocumentSymbols(an.Result, path), true)
}

// WorkspaceSymbols returns the design units of the whole project and
// the declarations of its packages whose name contains query (case
// insensitive), flat, sorted by name, path and position.
func (an *Analysis) WorkspaceSymbols(query string) []server.Symbol {
	if an.Result == nil {
		return nil
	}
	return an.symbols(resolve.WorkspaceSymbols(an.Result, query), false)
}

func (an *Analysis) symbols(ss []resolve.Symbol,
	nested bool,
) []server.Symbol {
	var out []server.Symbol
	for _, s := range ss {
		l, ok := an.Location(s.Location)
		if !ok {
			continue
		}
		full := Span(s.Full)
		if full.End <= full.Start {
			full = l.Span
		}
		sym := server.Symbol{Name: s.Name, Kind: SymbolKind(s.Kind),
			Detail: s.Detail, URI: l.URI, Span: full, Selection: l.Span}
		if nested {
			sym.Children = an.symbols(s.Children, true)
		}
		out = append(out, sym)
	}
	return out
}

// LSP SymbolKind values.
const (
	symFile          = 1
	symModule        = 2
	symNamespace     = 3
	symPackage       = 4
	symClass         = 5
	symProperty      = 7
	symField         = 8
	symInterface     = 11
	symFunction      = 12
	symVariable      = 13
	symConstant      = 14
	symKey           = 20
	symEnumMember    = 22
	symStruct        = 23
	symEvent         = 24
	symTypeParameter = 26
)

// SymbolKind maps a declaration kind to the LSP SymbolKind.
func SymbolKind(k sema.DeclKind) int {
	switch k {
	case sema.DeclEntity, sema.DeclComponent:
		return symClass
	case sema.DeclArchitecture, sema.DeclConfiguration,
		sema.DeclVunit:
		return symModule
	case sema.DeclPackage, sema.DeclPackageBody, sema.DeclPackageInst,
		sema.DeclGenericPackage, sema.DeclLibrary:
		return symPackage
	case sema.DeclContext:
		return symNamespace
	case sema.DeclType, sema.DeclSubtype, sema.DeclGroupTemplate:
		return symStruct
	case sema.DeclGenericType:
		return symTypeParameter
	case sema.DeclConstant, sema.DeclGeneric:
		return symConstant
	case sema.DeclSignal, sema.DeclPort:
		return symEvent
	case sema.DeclFunction, sema.DeclProcedure,
		sema.DeclGenericSubprogram:
		return symFunction
	case sema.DeclEnumLiteral, sema.DeclUnit:
		return symEnumMember
	case sema.DeclElement:
		return symField
	case sema.DeclAttribute, sema.DeclAlias:
		return symProperty
	case sema.DeclModeView, sema.DeclPSL:
		return symInterface
	case sema.DeclLabel, sema.DeclGroup:
		return symKey
	case sema.DeclFile:
		return symFile
	}
	return symVariable
}
