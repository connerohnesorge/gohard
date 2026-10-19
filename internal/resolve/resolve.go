// Package resolve answers the language server's name queries from a
// sema.Result (#34, #25): definition, references, hover data, rename,
// completion of visible names, and document and workspace symbols.
// Every query is a pure function of the result and its arguments; all
// lists are sorted, so answers never depend on analysis scheduling.
//
// Positions are byte offsets in the file at a project-relative path;
// bundled std/ieee declarations are reported with Bundled set (the
// editor opens them read-only, #33).
package resolve

import (
	"cmp"
	"errors"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/lexer"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/token"
)

// Location is a span in a project or bundled file.
type Location struct {
	Path    string
	Span    cst.Range
	Bundled bool
}

func compareLoc(a, b Location) int {
	return cmp.Or(cmp.Compare(a.Path, b.Path),
		cmp.Compare(a.Span.Start, b.Span.Start),
		cmp.Compare(a.Span.End, b.Span.End))
}

// sortLocs sorts and dedupes locations.
func sortLocs(ls []Location) []Location {
	slices.SortFunc(ls, compareLoc)
	return slices.CompactFunc(ls, func(a, b Location) bool {
		return compareLoc(a, b) == 0
	})
}

// LocationOf returns where d is declared (its name); ok is false for
// declarations without a source position (libraries, universal types).
func LocationOf(d *sema.Decl) (Location, bool) {
	if d == nil || d.File == nil {
		return Location{}, false
	}
	return Location{Path: d.File.Path, Span: d.Span,
		Bundled: d.File.Bundled}, true
}

// target is what a position denotes: a reference or a declaration.
type target struct {
	ref  sema.Ref
	decl *sema.Decl
	ok   bool
}

func at(r *sema.Result, path string, off int) target {
	if ref, ok := r.RefAt(path, off); ok {
		return target{ref: ref, decl: ref.Decl, ok: true}
	}
	if d, ok := r.DeclAt(path, off); ok {
		return target{decl: d, ok: true}
	}
	return target{}
}

// Definition returns the declaration of the name at off. An overloaded
// name whose overload is not decided (ambiguous or failed) gives every
// candidate.
func Definition(r *sema.Result, path string, off int) []Location {
	t := at(r, path, off)
	if !t.ok {
		return nil
	}
	var out []Location
	if t.decl != nil {
		if l, ok := LocationOf(t.decl); ok {
			out = append(out, l)
		}
	} else if o := t.ref.Overload; o != nil {
		for _, c := range o.Candidates {
			if c.Fits || o.Chosen == nil {
				if l, ok := LocationOf(c.Decl); ok {
					out = append(out, l)
				}
			}
		}
	}
	return sortLocs(out)
}

// group returns the declarations that are one named entity for
// references and rename: d, the declaration it completes (Spec) and the
// declarations that complete it (a subprogram body, the full constant
// of a deferred one, a package body).
func group(r *sema.Result, d *sema.Decl) []*sema.Decl {
	root := d
	if d.Kind == sema.DeclArchitecture ||
		d.Kind == sema.DeclConfiguration {
		// A secondary unit's Spec is its entity, but the
		// architecture or configuration name is its own symbol.
		return []*sema.Decl{d}
	}
	for range 8 {
		if root.Spec == nil || root.Spec == root {
			break
		}
		root = root.Spec
	}
	out := []*sema.Decl{root}
	for _, u := range r.Units {
		for _, x := range u.Decls {
			if x != root && x.Spec == root &&
				x.Kind != sema.DeclArchitecture &&
				x.Kind != sema.DeclConfiguration {
				out = append(out, x)
			}
		}
	}
	return out
}

// References returns every occurrence of the entity named at off,
// across the project, sorted; with includeDecl the declarations
// themselves are listed too.
func References(r *sema.Result, path string, off int,
	includeDecl bool,
) []Location {
	t := at(r, path, off)
	if !t.ok || t.decl == nil {
		return nil
	}
	var out []Location
	for _, d := range group(r, t.decl) {
		if includeDecl {
			if l, ok := LocationOf(d); ok && !d.Implicit {
				out = append(out, l)
			}
		}
		for _, ref := range r.References(d) {
			out = append(out, Location{Path: ref.File.Path,
				Span: ref.Span, Bundled: ref.File.Bundled})
		}
	}
	return sortLocs(out)
}

// Hover is the hover data of a name.
type Hover struct {
	// Markdown is the text shown.
	Markdown string
	// Span is the name hovered.
	Span cst.Range
	// Decl is what the name denotes (nil for an undecided overload);
	// Overload is the overload result of a call or operator.
	Decl     *sema.Decl
	Overload *sema.Overload
}

// HoverAt returns the hover data at off. For a call or operator it
// shows the chosen overload and the package it comes from, or, when the
// overload is not decided, every candidate with the reason it does not
// fit (the same result the diagnostic uses).
func HoverAt(r *sema.Result, path string, off int) (Hover, bool) {
	t := at(r, path, off)
	if !t.ok {
		return Hover{}, false
	}
	h := Hover{Decl: t.decl, Overload: t.ref.Overload, Span: t.ref.Span}
	if t.ref.File == nil && t.decl != nil {
		h.Span = t.decl.Span
	}
	var b strings.Builder
	if t.decl != nil {
		b.WriteString("```vhdl\n" + Describe(t.decl) + "\n```")
	}
	if o := h.Overload; o != nil && o.Chosen == nil {
		if t.decl == nil {
			b.WriteString("`" + o.Name + "`: no overload chosen")
		}
		for _, c := range o.Candidates {
			b.WriteString("\n- " + Describe(c.Decl))
			if !c.Fits {
				b.WriteString(": " + c.Reason)
			} else {
				b.WriteString(": fits")
			}
		}
	}
	h.Markdown = b.String()
	return h, h.Markdown != ""
}

// Describe is a one-line description of d: its kind, name, type or
// profile, and for subprograms and literals the unit it comes from
// ("function "+" [UNSIGNED, UNSIGNED return UNSIGNED] from
// ieee.numeric_std").
func Describe(d *sema.Decl) string {
	x := d.Denotes()
	s := d.Kind.String() + " " + d.Text
	switch {
	case x.Kind.IsSubprogram() || x.Kind == sema.DeclEnumLiteral:
		s += " " + sema.Signature(x)
	case x.Kind.IsObject() || x.Kind == sema.DeclElement ||
		x.Kind == sema.DeclAlias || x.Kind == sema.DeclAttribute:
		if x.Type != nil {
			s += " : " + x.Type.Name()
		}
	}
	if u := x.Unit; u != nil && (x.Kind.IsSubprogram() ||
		x.Kind == sema.DeclEnumLiteral || x.Implicit ||
		u.File.Bundled) {
		s += " from " + string(u.Library) + "." + string(u.Name)
	}
	return s
}

// Edit replaces Span in the file at Path with NewText.
type Edit struct {
	Path    string
	Span    cst.Range
	NewText string
}

// Errors from rename.
var (
	ErrNotRenamable = errors.New("nothing to rename here")
	ErrBadName      = errors.New("not a valid VHDL identifier")
)

// PrepareRename returns the span of the renamable name at off: a
// project declaration (not bundled, not implicit, not a library).
func PrepareRename(r *sema.Result, path string, off int,
) (cst.Range, bool) {
	t := at(r, path, off)
	if !t.ok || !renamable(t.decl) {
		return cst.Range{}, false
	}
	if t.ref.File != nil {
		return t.ref.Span, true
	}
	return t.decl.Span, true
}

func renamable(d *sema.Decl) bool {
	if d == nil || d.File == nil || d.File.Bundled || d.Implicit ||
		d.Kind == sema.DeclLibrary {
		return false
	}
	k := sema.KeyOf(token.Identifier, d.Text)
	return !k.IsOperator() && !k.IsCharacter()
}

// Rename returns the edits renaming the entity at off to newName in
// every project file, sorted by path and position.
func Rename(r *sema.Result, path string, off int, newName string,
) ([]Edit, error) {
	t := at(r, path, off)
	if !t.ok || !renamable(t.decl) {
		return nil, ErrNotRenamable
	}
	if !validIdentifier(newName, r.Standard) {
		return nil, ErrBadName
	}
	var edits []Edit
	for _, l := range References(r, path, off, true) {
		if !l.Bundled {
			edits = append(edits, Edit{Path: l.Path, Span: l.Span,
				NewText: newName})
		}
	}
	return edits, nil
}

// validIdentifier reports whether s is one basic or extended
// identifier that is not a reserved word of std.
func validIdentifier(s string, std token.Standard) bool {
	toks, l := lexer.All([]byte(s), lexer.Options{Standard: std},
		lexer.VHDL)
	n := 0
	ok := false
	for _, t := range toks {
		if len(l.Leading(t)) > 0 || len(l.Trailing(t)) > 0 ||
			len(l.Problems(t)) > 0 {
			return false
		}
		if t.Kind == token.EOF {
			continue
		}
		n++
		ok = t.Kind == token.Identifier ||
			t.Kind == token.ExtendedIdentifier
	}
	return n == 1 && ok
}

// Item is one completion proposal.
type Item struct {
	Label  string
	Kind   sema.DeclKind
	Detail string
	Decl   *sema.Decl
}

// Complete returns the names visible at off (each declaration once),
// sorted by label then detail.
func Complete(r *sema.Result, path string, off int) []Item {
	var out []Item
	for _, d := range r.VisibleAt(path, off) {
		if d.Kind == sema.DeclParameter && d.Implicit {
			continue
		}
		out = append(out, Item{Label: d.Text, Kind: d.Kind,
			Detail: Describe(d), Decl: d})
	}
	slices.SortStableFunc(out, func(a, b Item) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Label),
			strings.ToLower(b.Label)), cmp.Compare(a.Detail, b.Detail))
	})
	return slices.CompactFunc(out, func(a, b Item) bool {
		return a.Label == b.Label && a.Detail == b.Detail
	})
}

// Symbol is a document or workspace symbol.
type Symbol struct {
	Name     string
	Kind     sema.DeclKind
	Detail   string
	Location Location
	// Full is the whole declaration's span.
	Full     cst.Range
	Children []Symbol
}

// DocumentSymbols returns the declarations of the file at path as a
// tree: units, then their declarations and nested regions.
func DocumentSymbols(r *sema.Result, path string) []Symbol {
	f, ok := r.File(path)
	if !ok {
		return nil
	}
	var out []Symbol
	for _, u := range f.Units {
		if u.Decl == nil {
			continue
		}
		s := symbol(u.Decl)
		if u.Region != nil {
			s.Children = regionSymbols(u.Region, f)
		}
		out = append(out, s)
	}
	return out
}

func symbol(d *sema.Decl) Symbol {
	l, _ := LocationOf(d)
	return Symbol{Name: d.Text, Kind: d.Kind, Detail: Describe(d),
		Location: l, Full: d.Full}
}

func regionSymbols(reg *sema.Region, f *sema.File) []Symbol {
	var out []Symbol
	for _, d := range reg.Decls {
		if d.Implicit || d.File != f || d.Kind == sema.DeclParameter {
			continue
		}
		s := symbol(d)
		if d.Region != nil && d.Region != reg && d.Region.File == f &&
			d.Region.Owner == d {
			s.Children = regionSymbols(d.Region, f)
		}
		out = append(out, s)
	}
	slices.SortStableFunc(out, func(a, b Symbol) int {
		return cmp.Compare(a.Location.Span.Start, b.Location.Span.Start)
	})
	return out
}

// WorkspaceSymbols returns the project's design units and the
// declarations of its packages whose name contains query (case
// insensitive), sorted by name, path and position.
func WorkspaceSymbols(r *sema.Result, query string) []Symbol {
	q := strings.ToLower(query)
	match := func(d *sema.Decl) bool {
		return strings.Contains(strings.ToLower(d.Text), q)
	}
	var out []Symbol
	for _, u := range r.Units {
		if u.Decl == nil {
			continue
		}
		if match(u.Decl) {
			out = append(out, symbol(u.Decl))
		}
		if u.Kind == sema.UnitPackage && u.Region != nil {
			for _, d := range u.Region.Decls {
				if !d.Implicit && d.File == u.File && match(d) {
					out = append(out, symbol(d))
				}
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Symbol) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name),
			strings.ToLower(b.Name)), compareLoc(a.Location, b.Location))
	})
	return out
}
