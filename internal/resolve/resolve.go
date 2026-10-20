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
	"fmt"
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
		for _, c := range Targets(o) {
			if l, ok := LocationOf(c.Decl); ok {
				out = append(out, l)
			}
		}
	}
	return sortLocs(out)
}

// Targets returns the candidates go-to-definition lists for an
// undecided call: the ones that fit (an ambiguous call), or every
// candidate when none fits.
func Targets(o *sema.Overload) []sema.Candidate {
	var fit []sema.Candidate
	for _, c := range o.Candidates {
		if c.Fits {
			fit = append(fit, c)
		}
	}
	if len(fit) == 0 {
		return o.Candidates
	}
	return fit
}

// group returns the declarations that are one named entity for
// references and rename: d, the declaration it completes (Spec) and the
// declarations that complete it (a subprogram body, the full constant
// of a deferred one, a package body).
func group(r *sema.Result, d *sema.Decl) []*sema.Decl {
	if (d.Kind == sema.DeclParameter || d.Kind == sema.DeclGeneric) &&
		d.Parent != nil && d.Parent.Owner != nil &&
		d.Parent.Owner.Kind.IsSubprogram() {
		// A parameter or generic of a subprogram declaration and the
		// one of the same name in its body are one name: the body
		// must conform to the declaration (LRM 4.10).
		var out []*sema.Decl
		for _, s := range group(r, d.Parent.Owner) {
			for _, x := range s.Params {
				if x.Kind == d.Kind && x.Name == d.Name {
					out = append(out, x)
				}
			}
			if s.Region == nil || d.Kind != sema.DeclGeneric {
				continue
			}
			for _, x := range s.Region.Decls {
				if x.Kind == d.Kind && x.Name == d.Name {
					out = append(out, x)
				}
			}
		}
		if !slices.Contains(out, d) {
			out = append(out, d)
		}
		return out
	}
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

// HoverAt returns the hover data at off: the declaration's one-line
// description, its doc comment (the CST rule, #10) and, for a call or
// operator, the chosen overload and the package it comes from. When
// the overload is not decided it shows the diagnostic the overload
// result gives and every candidate with the reason it does not fit
// (the same result the diagnostic uses, #34).
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
		if doc := DocOf(r, t.decl); doc != "" {
			b.WriteString("\n\n" + doc)
		}
	}
	if o := h.Overload; o != nil && o.Chosen == nil {
		if t.decl == nil {
			if d, ok := o.Explain(); ok {
				b.WriteString(d.Message + "\n")
			} else {
				b.WriteString("`" + o.Name + "`: no overload chosen\n")
			}
		}
		for _, c := range o.Sorted() {
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

// DocOf returns the doc comment of d as plain text (comment markers
// removed, one line per comment line): the comment block right above
// the declaration (cst.DocComment). A subprogram body or full constant
// without one uses its declaration's; a design unit's is the block
// above its first keyword, after the context clause.
func DocOf(r *sema.Result, d *sema.Decl) string {
	for _, x := range []*sema.Decl{d, d.Spec} {
		if x == nil || x.Node.IsZero() || x.File == nil {
			continue
		}
		if doc := docText(cst.DocComment(x.Node.FirstToken())); doc != "" {
			return doc
		}
		if u := x.Unit; u != nil && u.Decl == x {
			if doc := docText(cst.DocComment(lineStart(x))); doc != "" {
				return doc
			}
		}
	}
	return ""
}

// lineStart is the first token of d's node on the line of its name.
func lineStart(d *sema.Decl) cst.Token {
	var first cst.Token
	prevEnd := -1
	text := d.File.Text
	for tk := range d.Node.DescendantTokens() {
		sp := tk.Span()
		if sp.Start >= d.Span.Start {
			break
		}
		if prevEnd < 0 || prevEnd <= sp.Start && sp.Start <= len(text) &&
			strings.Contains(string(text[prevEnd:sp.Start]), "\n") {
			first = tk
		}
		prevEnd = sp.End
	}
	return first
}

func docText(trs []cst.Trivia) string {
	var lines []string
	for _, tr := range trs {
		s := tr.Text
		if tr.Kind == token.BlockComment {
			s = strings.TrimSuffix(strings.TrimPrefix(s, "/*"), "*/")
			for l := range strings.SplitSeq(s, "\n") {
				l = strings.TrimSpace(l)
				l = strings.TrimSpace(strings.TrimPrefix(l, "*"))
				lines = append(lines, l)
			}
			continue
		}
		s = strings.TrimPrefix(s, "--")
		s = strings.TrimPrefix(s, " ")
		lines = append(lines, strings.TrimRight(s, " \t\r"))
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
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
	ErrBundled      = errors.New("it is declared in the bundled " +
		"std/ieee libraries, which are read-only")
	ErrImplicit = errors.New("it is predefined")
	ErrOpaque   = errors.New("it is declared in an opaque unit, " +
		"whose references are not known")
	ErrStandIn = errors.New("it stands in for an opaque unit " +
		"of the same name")
	ErrExtended = errors.New("it is an extended identifier, whose " +
		"spelling is never changed")
	// ErrConflict wraps the reason a new name collides with a
	// declaration already visible where the entity is declared or
	// used.
	ErrConflict = errors.New("the new name is already in use")
)

// PrepareRename returns the span of the renamable name at off: a
// project declaration (not bundled, not implicit, not a library, not
// in an opaque unit, not an extended identifier).
func PrepareRename(r *sema.Result, path string, off int,
) (cst.Range, bool) {
	t := at(r, path, off)
	if !t.ok || renamable(r, t.decl) != nil {
		return cst.Range{}, false
	}
	if t.ref.File != nil {
		return t.ref.Span, true
	}
	return t.decl.Span, true
}

// renamable says why d cannot be renamed, or nil.
func renamable(r *sema.Result, d *sema.Decl) error {
	switch {
	case d == nil || d.Kind == sema.DeclLibrary:
		return ErrNotRenamable
	case d.File != nil && d.File.Bundled:
		return ErrBundled
	case d.Implicit:
		return ErrImplicit
	case d.File == nil:
		return ErrNotRenamable
	case d.Unit != nil && d.Unit.Opaque:
		return ErrOpaque
	case strings.HasPrefix(d.Text, "\\"):
		return ErrExtended
	case standsIn(r, d):
		return ErrStandIn
	}
	k := sema.KeyOf(token.Identifier, d.Text)
	if k.IsOperator() || k.IsCharacter() {
		return ErrNotRenamable
	}
	return nil
}

// standsIn reports a design unit named like an opaque unit of its
// library (a simulation.models stand-in, #35): the name binds the
// two.
func standsIn(r *sema.Result, d *sema.Decl) bool {
	if d.Unit == nil || d.Unit.Decl != d {
		return false
	}
	for _, u := range r.Units {
		if u.Opaque && u != d.Unit && u.Name == d.Unit.Name &&
			u.Library == d.Unit.Library {
			return true
		}
	}
	return false
}

// Rename returns the edits renaming the entity at off to newName in
// every project file, sorted by path and position. Each use site keeps
// its letter case (#25): a site spelled like the declaration gets
// newName as given, an all upper-case or all lower-case site gets
// newName in that case. An extended newName is used as given. The
// error says why nothing is renamed (ErrNotRenamable, ErrBundled,
// ErrImplicit, ErrOpaque, ErrStandIn, ErrExtended, ErrBadName,
// ErrConflict).
func Rename(r *sema.Result, path string, off int, newName string,
) ([]Edit, error) {
	t := at(r, path, off)
	if !t.ok {
		return nil, ErrNotRenamable
	}
	if err := renamable(r, t.decl); err != nil {
		return nil, err
	}
	if !validIdentifier(newName, r.Standard) {
		return nil, ErrBadName
	}
	locs := References(r, path, off, true)
	if err := conflict(r, group(r, t.decl), locs, newName); err != nil {
		return nil, err
	}
	var edits []Edit
	for _, l := range locs {
		if l.Bundled {
			continue
		}
		f, ok := r.File(l.Path)
		if !ok || l.Span.End > len(f.Text) {
			continue
		}
		site := string(f.Text[l.Span.Start:l.Span.End])
		edits = append(edits, Edit{Path: l.Path, Span: l.Span,
			NewText: keepCase(site, t.decl.Text, newName)})
	}
	return edits, nil
}

// conflict refuses a rename whose new name collides with another
// declaration: one in the region of a renamed declaration or a region
// enclosing it (for an architecture this includes its entity's ports
// and generics), a design unit of the same library, or one visible at
// a use site, which the renamed use would be captured by. Only
// homographs collide: overloads of distinct profiles may share a name.
func conflict(r *sema.Result, grp []*sema.Decl, locs []Location,
	newName string,
) error {
	k := sema.KeyOf(token.Identifier, newName)
	if strings.HasPrefix(newName, "\\") {
		k = sema.KeyOf(token.ExtendedIdentifier, newName)
	}
	clash := func(x *sema.Decl) bool {
		if x == nil || x.Kind == sema.DeclLibrary ||
			slices.Contains(grp, x) {
			return false
		}
		for _, d := range grp {
			if sema.Homographs(d, x) {
				return true
			}
		}
		return false
	}
	refuse := func(x *sema.Decl) error {
		where := ""
		if l, ok := LocationOf(x); ok && !l.Bundled {
			where = fmt.Sprintf(" (%s:%d)", l.Path, line(r, l))
		} else if x.File != nil && x.File.Bundled {
			where = " (bundled)"
		}
		return fmt.Errorf("%w: %s %s%s", ErrConflict,
			Describe(x), "is visible", where)
	}
	for _, d := range grp {
		if d.Unit != nil && d.Unit.Decl == d {
			for _, u := range r.Units {
				if u.Decl != nil && u.Decl.Name == k &&
					u.Library == d.Unit.Library && clash(u.Decl) &&
					d.Kind != sema.DeclArchitecture &&
					d.Kind != sema.DeclConfiguration {
					return refuse(u.Decl)
				}
			}
		}
		for reg := d.Parent; reg != nil; reg = reg.Parent {
			for _, x := range reg.Lookup(k) {
				if clash(x) {
					return refuse(x)
				}
			}
		}
		if d.Parent == nil || d.Parent.Owner == nil {
			continue
		}
		// The secondary units of the region's primary unit extend
		// it (an architecture its entity, a package body its
		// package; LRM 12.1).
		for _, u := range r.Units {
			if u.Decl == nil || u.Decl.Spec != d.Parent.Owner ||
				u.Region == nil {
				continue
			}
			for _, x := range u.Region.Lookup(k) {
				if clash(x) {
					return refuse(x)
				}
			}
		}
	}
	for _, l := range locs {
		if l.Bundled {
			continue
		}
		for _, x := range r.LookupAt(l.Path, l.Span.Start, k) {
			if clash(x) {
				return refuse(x)
			}
		}
	}
	return nil
}

// line is the 1-based line of l.
func line(r *sema.Result, l Location) int {
	f, ok := r.File(l.Path)
	if !ok {
		return 0
	}
	n := 1
	for _, c := range f.Text[:min(l.Span.Start, len(f.Text))] {
		if c == '\n' {
			n++
		}
	}
	return n
}

// keepCase spells newName at a use site written site, for a
// declaration spelled decl.
func keepCase(site, decl, newName string) string {
	if strings.HasPrefix(newName, "\\") || site == decl ||
		strings.HasPrefix(site, "\\") {
		return newName
	}
	up, low := strings.ToUpper(site), strings.ToLower(site)
	switch {
	case up == low:
		return newName
	case site == up:
		return strings.ToUpper(newName)
	case site == low:
		return strings.ToLower(newName)
	}
	return newName
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
