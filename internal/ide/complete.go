package ide

import (
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/resolve"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/server"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

func init() { provide(server.FeatureCompletion) }

// LSP CompletionItemKind values.
const (
	compFunction   = 3
	compField      = 5
	compVariable   = 6
	compClass      = 7
	compInterface  = 8
	compModule     = 9
	compProperty   = 10
	compUnit       = 11
	compKeyword    = 14
	compFile       = 17
	compFolder     = 19
	compEnumMember = 20
	compConstant   = 21
	compStruct     = 22
	compEvent      = 23
	compTypeParam  = 25
)

// CompletionKind maps a declaration kind to the LSP
// CompletionItemKind.
func CompletionKind(k sema.DeclKind) int {
	switch k {
	case sema.DeclEntity, sema.DeclComponent:
		return compClass
	case sema.DeclArchitecture, sema.DeclConfiguration,
		sema.DeclVunit, sema.DeclPackage, sema.DeclPackageBody,
		sema.DeclPackageInst, sema.DeclGenericPackage, sema.DeclContext:
		return compModule
	case sema.DeclLibrary:
		return compFolder
	case sema.DeclType, sema.DeclSubtype, sema.DeclGroupTemplate:
		return compStruct
	case sema.DeclGenericType:
		return compTypeParam
	case sema.DeclConstant, sema.DeclGeneric:
		return compConstant
	case sema.DeclSignal, sema.DeclPort:
		return compEvent
	case sema.DeclFunction, sema.DeclProcedure,
		sema.DeclGenericSubprogram:
		return compFunction
	case sema.DeclEnumLiteral:
		return compEnumMember
	case sema.DeclUnit:
		return compUnit
	case sema.DeclElement:
		return compField
	case sema.DeclAttribute, sema.DeclAlias:
		return compProperty
	case sema.DeclModeView, sema.DeclPSL:
		return compInterface
	case sema.DeclFile:
		return compFile
	}
	return compVariable
}

// Completion implements server.Analyzer (#25): after a '.', the
// elements of the prefix (record fields, the declarations of a
// package, the units of a library); otherwise the overload candidates
// of the call around the cursor (#34, the result hover shows), the
// names visible there, the libraries and the keywords of the selected
// standard. Items are filtered by the word before the cursor (case
// insensitive) and replace it.
func (a *Analyzer) Completion(q server.Query,
) server.Task[[]server.CompletionItem] {
	return Query(a, q.Snap, func(an *Analysis) []server.CompletionItem {
		return an.Complete(q)
	})
}

func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' ||
		c >= 'A' && c <= 'Z' || c >= 0x80
}

// wordBefore returns the start of the identifier ending at off.
func wordBefore(text []byte, off int) int {
	i := off
	for i > 0 && isWordByte(text[i-1]) {
		i--
	}
	return i
}

// Complete is the completion at q (see Analyzer.Completion).
func (an *Analysis) Complete(q server.Query) []server.CompletionItem {
	path, off, ok := an.query(q)
	if !ok {
		return nil
	}
	d, _ := an.Snap.Document(q.URI)
	text := d.Text()
	off = max(0, min(off, len(text)))
	start := wordBefore(text, off)
	prefix := strings.ToLower(string(text[start:off]))
	c := completer{prefix: prefix,
		span: server.Span{Start: start, End: off}, seen: map[string]bool{}}
	dot := start
	for dot > 0 && (text[dot-1] == ' ' || text[dot-1] == '\t') {
		dot--
	}
	if dot > 0 && text[dot-1] == '.' {
		c.selected(an, path, text, dot-1)
		return c.out
	}
	c.candidates(undecided(an.Result, path, off))
	for _, it := range resolve.Complete(an.Result, path, off) {
		k := sema.KeyOf(token.Identifier, it.Label)
		if k.IsOperator() || k.IsCharacter() {
			continue
		}
		c.add(it.Label, CompletionKind(it.Kind), it.Detail)
	}
	for _, l := range an.Result.Libraries {
		c.add(string(l.Name), compFolder, "library "+string(l.Name))
	}
	for _, kw := range keywords(an.Result.Standard) {
		c.add(kw, compKeyword, "")
	}
	return c.out
}

// keywords returns the reserved words of std outside PSL code, sorted.
func keywords(std token.Standard) []string {
	var out []string
	for _, k := range token.Keywords() {
		if k.Class == token.ClassVHDL && k.Reserved(std, false, false) {
			out = append(out, k.Word)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

type completer struct {
	prefix string
	span   server.Span
	seen   map[string]bool // label + detail
	out    []server.CompletionItem
}

func (c *completer) add(label string, kind int, detail string) {
	if !strings.HasPrefix(strings.ToLower(label), c.prefix) {
		return
	}
	k := strings.ToLower(label) + "\x00" + detail
	if c.seen[k] {
		return
	}
	c.seen[k] = true
	c.out = append(c.out, server.CompletionItem{Label: label, Kind: kind,
		Detail: detail,
		Edit:   &server.Edit{Span: c.span, NewText: label}})
}

// CandidateDetail is the line hover shows for an overload candidate
// (#34): its description and why it does not fit, or "fits".
func CandidateDetail(cd sema.Candidate) string {
	if cd.Fits {
		return resolve.Describe(cd.Decl) + ": fits"
	}
	return resolve.Describe(cd.Decl) + ": " + cd.Reason
}

// undecided returns the innermost undecided overload of a call or
// operator whose expression holds off, or nil.
func undecided(r *sema.Result, path string, off int) *sema.Overload {
	at := off
	for range 16 {
		o, sp, ok := r.OverloadAt(path, at)
		if !ok || o == nil || sp.Start > off || off > sp.End {
			return nil
		}
		if o.Chosen == nil {
			return o
		}
		if sp.Start == 0 {
			return nil
		}
		at = sp.Start - 1 // the enclosing expression
	}
	return nil
}

// candidates adds the candidates of an undecided overload, in the
// resolver's order (fitting ones first), with the reason each does
// not fit; their plain items are then left out.
func (c *completer) candidates(o *sema.Overload) {
	if o == nil || o.Chosen != nil {
		return
	}
	for _, cd := range o.Candidates {
		c.add(cd.Decl.Text, CompletionKind(cd.Decl.Kind),
			CandidateDetail(cd))
		k := strings.ToLower(cd.Decl.Text) + "\x00" +
			resolve.Describe(cd.Decl)
		c.seen[k] = true
	}
}

// selected completes the suffix of a selected name whose '.' is at
// dot: the prefix name before it is looked up as an occurrence.
func (c *completer) selected(an *Analysis, path string, text []byte,
	dot int,
) {
	end := dot
	for end > 0 && (text[end-1] == ' ' || text[end-1] == '\t') {
		end--
	}
	if end == 0 || !isWordByte(text[end-1]) {
		return
	}
	var d *sema.Decl
	if ref, ok := an.Result.RefAt(path, end); ok && ref.Span.End == end {
		d = ref.Decl
	} else if x, ok := an.Result.DeclAt(path, end); ok &&
		x.Span.End == end {
		d = x
	}
	if d == nil {
		return
	}
	for _, x := range members(an.Result, d.Denotes()) {
		c.add(x.Text, CompletionKind(x.Kind), resolve.Describe(x))
	}
	switch d.Denotes().Kind {
	case sema.DeclPackage, sema.DeclPackageInst, sema.DeclLibrary:
		c.add("all", compKeyword, "")
	}
}

// members returns what may follow d and a '.': the primary units of a
// library, the declarations of a package, the elements of a record
// object or type, the declarations of a protected object.
func members(r *sema.Result, d *sema.Decl) []*sema.Decl {
	var out []*sema.Decl
	switch {
	case d.Kind == sema.DeclLibrary:
		l, ok := r.Library(d.Name)
		if !ok {
			return nil
		}
		for _, u := range l.Units() {
			if u.Kind.Primary() && u.Decl != nil && !u.Duplicate {
				out = append(out, u.Decl)
			}
		}
		slices.SortStableFunc(out, func(a, b *sema.Decl) int {
			return strings.Compare(string(a.Name), string(b.Name))
		})
	case d.Kind == sema.DeclPackage || d.Kind == sema.DeclPackageInst:
		reg := d.Region
		if reg == nil && d.Unit != nil {
			reg = d.Unit.Region
		}
		if reg == nil {
			return nil
		}
		for _, x := range reg.Decls {
			if !x.Implicit && x.Kind != sema.DeclParameter &&
				(x.First == 0 || x.First <= r.Standard) {
				out = append(out, x)
			}
		}
	case d.Type != nil:
		b := d.Type.Base()
		if len(b.Fields) > 0 {
			return slices.Clone(b.Fields)
		}
		if b.Decl != nil && b.Decl.Region != nil &&
			b.Decl.Region.Kind == sema.RegionProtected {
			for _, x := range b.Decl.Region.Decls {
				if !x.Implicit && x.Kind != sema.DeclParameter {
					out = append(out, x)
				}
			}
		}
	}
	return out
}
