package ide

import (
	"cmp"
	"slices"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/lexer"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/server"
	"github.com/connerohnesorge/gohard/v2/internal/span"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

func init() { provide(server.FeatureSemanticTokens) }

// TokenTypes is the semantic tokens legend (#55): the LSP standard
// types, then signal and port, which LSP has no type for.
var TokenTypes = []string{"namespace", "type", "class", "function",
	"parameter", "variable", "property", "enumMember", "keyword",
	"string", "number", "comment", "signal", "port"}

// Token type indices into TokenTypes.
const (
	tokNamespace = iota
	tokType
	tokClass
	tokFunction
	tokParameter
	tokVariable
	tokProperty
	tokEnumMember
	tokKeyword
	tokString
	tokNumber
	tokComment
	tokSignal
	tokPort
)

// TokenModifiers are the semantic token modifiers: the LSP standard
// ones, then inactive (a branch conditional analysis left out).
var TokenModifiers = []string{"declaration", "readonly", "static",
	"defaultLibrary", "inactive"}

// Token modifier bits (bit i is TokenModifiers[i]).
const (
	modDeclaration uint32 = 1 << iota
	modReadonly
	modStatic
	modDefaultLibrary
	modInactive
)

// SemanticTokens implements server.Analyzer
// (Analysis.SemanticTokens).
func (a *Analyzer) SemanticTokens(snap *server.Snapshot, uri string,
) server.Task[[]server.SemToken] {
	return Query(a, snap, func(an *Analysis) []server.SemToken {
		return an.SemanticTokens(uri)
	})
}

// semTok is a token with the precedence that decides overlaps.
type semTok struct {
	server.SemToken
	rank int
}

// Ranks, best first: a declaration's defining name, then a resolved
// name, then the lexical classes. A keyword the analysis records as a
// name (an operator such as `and`) stays a keyword.
const (
	rankKeyword = iota
	rankDecl
	rankRef
	rankLexical
)

// SemanticTokens returns the semantic tokens of the document uri
// (a project file, or a bundled file the editor opened), sorted and
// not overlapping:
//
//   - every name the analysis resolved, typed by what it denotes
//     (tokenType), with modifiers declaration at the defining name,
//     readonly, static, and defaultLibrary for std and ieee names;
//     labels, attributes, groups, mode views and PSL declarations, and
//     operator symbols used as operators, get no token;
//   - reserved words (keyword), string, bit-string and character
//     literals (string), numbers (number) and comments (comment);
//   - each line of an inactive conditional-analysis branch, from its
//     first to its last non-blank byte, as one comment token with the
//     inactive modifier, so editors can dim it; nothing else is
//     reported inside the branch.
func (an *Analysis) SemanticTokens(uri string) []server.SemToken {
	path, _, ok := an.query(server.Query{URI: uri})
	if !ok {
		return nil
	}
	f, ok := an.Result.File(path)
	if !ok || f.Parse == nil {
		return nil
	}
	var ts []semTok
	add := func(r cst.Range, typ int, mods uint32, rank int) {
		if r.End > r.Start {
			ts = append(ts, semTok{server.SemToken{
				Span: Span(r), Type: typ, Mods: mods}, rank})
		}
	}
	ts = lexicalTokens(f, ts)
	for _, u := range f.Units {
		for _, d := range u.Decls {
			if d.File != f || d.Implicit || d.Span.Len() == 0 {
				continue
			}
			if typ, mods, ok := tokenType(d); ok {
				add(d.Span, typ, mods|modDeclaration, rankDecl)
			}
		}
		for _, r := range u.Refs {
			if r.File != f || !nameText(f.Text, r.Span) {
				continue
			}
			d := r.Decl
			if d == nil && r.Overload != nil &&
				len(r.Overload.Candidates) > 0 {
				d = r.Overload.Candidates[0].Decl
			}
			if typ, mods, ok := tokenType(d); ok {
				add(r.Span, typ, mods, rankRef)
			}
		}
	}
	slices.SortFunc(ts, func(a, b semTok) int {
		return cmp.Or(cmp.Compare(a.Span.Start, b.Span.Start),
			cmp.Compare(a.rank, b.rank),
			cmp.Compare(b.Span.End, a.Span.End))
	})
	return withInactive(ts, f.Parse.Inactive, f.Text, f.Lines)
}

// lexicalTokens appends the keyword, literal and comment tokens of the
// file's tree; tokens of inactive branches are left to withInactive.
func lexicalTokens(f *sema.File, ts []semTok) []semTok {
	add := func(start, end, typ, rank int) {
		if end > start {
			ts = append(ts, semTok{server.SemToken{
				Span: server.Span{Start: start, End: end}, Type: typ},
				rank})
		}
	}
	trivia := func(off int, tv []cst.Trivia) {
		for _, x := range tv {
			if x.Kind == token.LineComment ||
				x.Kind == token.BlockComment {
				add(off, off+len(x.Text), tokComment, rankLexical)
			}
			off += len(x.Text)
		}
	}
	for t := range f.Root().DescendantTokens() {
		trivia(t.FullSpan().Start, t.Leading())
		trivia(t.Span().End, t.Trailing())
		if t.Missing() || t.Inactive() {
			continue
		}
		s := t.Span()
		switch k := t.Kind(); {
		case k.IsKeyword():
			add(s.Start, s.End, tokKeyword, rankKeyword)
		case k == token.StringLiteral || k == token.BitStringLiteral ||
			k == token.CharacterLiteral:
			add(s.Start, s.End, tokString, rankLexical)
		case k == token.IntegerLiteral || k == token.RealLiteral:
			add(s.Start, s.End, tokNumber, rankLexical)
		}
	}
	return ts
}

// nameText reports whether the occurrence at r is written as a name:
// an identifier, an extended identifier, a character literal or an
// operator symbol in quotes ("+" called as a function), not an
// operator used as one.
func nameText(text []byte, r cst.Range) bool {
	if r.Start < 0 || r.Start >= len(text) {
		return false
	}
	switch c := text[r.Start]; {
	case c == '\\' || c == '\'' || c == '"' || c >= 0x80:
		return true
	default:
		return c|0x20 >= 'a' && c|0x20 <= 'z'
	}
}

// tokenType returns the token type and modifiers of a name denoting d;
// ok is false for names that get no token.
func tokenType(d *sema.Decl) (typ int, mods uint32, ok bool) {
	for range 8 { // an alias denotes what it names
		if d == nil || d.Kind != sema.DeclAlias || d.Target == nil {
			break
		}
		d = d.Target
	}
	if d == nil {
		return 0, 0, false
	}
	if d.Kind == sema.DeclLibrary && (d.Name == "std" || d.Name == "ieee") ||
		d.Kind != sema.DeclLibrary && (d.Implicit ||
			d.File != nil && d.File.Bundled) {
		mods |= modDefaultLibrary
	}
	switch d.Kind {
	case sema.DeclLibrary:
		return tokNamespace, mods, true
	case sema.DeclType, sema.DeclSubtype, sema.DeclGenericType:
		return tokType, mods, true
	case sema.DeclEntity, sema.DeclArchitecture, sema.DeclPackage,
		sema.DeclPackageBody, sema.DeclPackageInst,
		sema.DeclConfiguration, sema.DeclContext, sema.DeclVunit,
		sema.DeclComponent, sema.DeclGenericPackage:
		return tokClass, mods, true
	case sema.DeclFunction, sema.DeclProcedure,
		sema.DeclGenericSubprogram:
		return tokFunction, mods, true
	case sema.DeclParameter:
		if d.Class == sema.ClassConstant {
			mods |= modReadonly
		}
		return tokParameter, mods, true
	case sema.DeclConstant:
		return tokVariable, mods | modReadonly, true
	case sema.DeclGeneric:
		return tokVariable, mods | modReadonly | modStatic, true
	case sema.DeclLoopParam, sema.DeclGenerateParam:
		return tokVariable, mods | modReadonly, true
	case sema.DeclVariable, sema.DeclFile, sema.DeclAlias:
		return tokVariable, mods, true
	case sema.DeclSharedVariable:
		return tokVariable, mods | modStatic, true
	case sema.DeclSignal:
		return tokSignal, mods, true
	case sema.DeclPort:
		if d.Mode == sema.ModeIn {
			mods |= modReadonly
		}
		return tokPort, mods, true
	case sema.DeclElement:
		return tokProperty, mods, true
	case sema.DeclEnumLiteral, sema.DeclUnit:
		return tokEnumMember, mods, true
	}
	return 0, 0, false
}

// withInactive drops the tokens that overlap an earlier one or an
// inactive region, and adds the inactive tokens: one per line of each
// region, from its first to its last non-blank byte. ts is sorted by
// start, then rank.
func withInactive(ts []semTok, regions []lexer.Region, text []byte,
	x *span.Index,
) []server.SemToken {
	var in []server.SemToken
	for _, r := range regions {
		a, b := max(r.Start, 0), min(r.End, len(text))
		for a < b {
			l := x.Line(a)
			s, e := a, min(b, x.LineEnd(l))
			for s < e && blank(text[s]) {
				s++
			}
			for e > s && blank(text[e-1]) {
				e--
			}
			if e > s {
				in = append(in, server.SemToken{
					Span: server.Span{Start: s, End: e},
					Type: tokComment, Mods: modInactive})
			}
			if l+1 >= x.LineCount() {
				break
			}
			a = x.LineStart(l + 1)
		}
	}
	out := make([]server.SemToken, 0, len(ts)+len(in))
	end, j := 0, 0
	for _, t := range ts {
		for j < len(in) && in[j].Span.Start < t.Span.End {
			if in[j].Span.Start >= end {
				out = append(out, in[j])
				end = in[j].Span.End
			}
			j++
		}
		if t.Span.Start < end || inRegion(regions, t.Span) {
			continue
		}
		out = append(out, t.SemToken)
		end = t.Span.End
	}
	for ; j < len(in); j++ {
		if in[j].Span.Start >= end {
			out = append(out, in[j])
			end = in[j].Span.End
		}
	}
	return out
}

func blank(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' ||
		c == '\v'
}

// inRegion reports whether s overlaps one of the sorted, disjoint
// regions.
func inRegion(regions []lexer.Region, s server.Span) bool {
	i, _ := slices.BinarySearchFunc(regions, s.Start,
		func(r lexer.Region, off int) int { return cmp.Compare(r.End, off+1) })
	return i < len(regions) && regions[i].Start < s.End
}
