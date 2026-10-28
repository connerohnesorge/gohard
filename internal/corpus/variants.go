package corpus

import (
	"bytes"
	"maps"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/lexer"
	"github.com/connerohnesorge/gohard/v2/internal/parser"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// The #8 variants behind the variant_stable invariant: each one is a
// source transformation of a design that must leave its outcome (exit,
// status, messages ignoring lines, stop time) unchanged.

// Transform rewrites the VHDL sources of one run. Keys are the
// design-relative paths; the result has the same keys.
type Transform struct {
	Name  string
	Apply func(src map[string][]byte, o parser.Options) map[string][]byte
}

// Transforms are the generated variants, in a fixed order.
var Transforms = []Transform{
	{"layout", perFile(layout)},
	{"case", perFile(upperCase)},
	{"rename", rename},
	{"reorder", perFile(reorder)},
}

func perFile(f func([]byte, parser.Options) []byte) func(
	map[string][]byte, parser.Options) map[string][]byte {
	return func(src map[string][]byte, o parser.Options) map[string][]byte {
		out := map[string][]byte{}
		for _, k := range slices.Sorted(maps.Keys(src)) {
			b := src[k]
			if isVHDL(k) {
				b = f(b, o)
			}
			out[k] = b
		}
		return out
	}
}

func lexOpts(o parser.Options) lexer.Options {
	return lexer.Options{Standard: o.Standard,
		PSLComments: o.PSLComments, StrictKeywords: o.PSLStrictKeywords}
}

// edit replaces token text: f returns the new text of each token.
func edit(src []byte, o parser.Options,
	f func(t lexer.Token, text []byte) []byte,
	trivia func(k token.Kind, text []byte) []byte) []byte {
	toks, lx := lexer.All(src, lexOpts(o), lexer.VHDL)
	var b bytes.Buffer
	emit := func(ts []lexer.Trivia) {
		for _, tv := range ts {
			b.Write(trivia(tv.Kind, src[tv.Start:tv.End]))
		}
	}
	for _, t := range toks {
		emit(lx.Leading(t))
		b.Write(f(t, src[t.Start:t.End]))
		emit(lx.Trailing(t))
	}
	return b.Bytes()
}

func same(_ lexer.Token, text []byte) []byte { return text }

func sameTrivia(_ token.Kind, text []byte) []byte { return text }

// layout adds a blank line at every line break and doubles every run
// of horizontal white space: only the layout changes (lines move).
func layout(src []byte, o parser.Options) []byte {
	return edit(src, o, same, func(k token.Kind, text []byte) []byte {
		switch k {
		case token.Whitespace:
			return append(bytes.Clone(text), text...)
		case token.Newline:
			return append(bytes.Clone(text), text...)
		}
		return text
	})
}

// plainWord reports whether t is a word whose case does not matter:
// a reserved word or basic identifier that spells no keyword in
// another standard or mode (PSL words stay as written), outside PSL
// comment lines.
func plainWord(t lexer.Token) bool {
	if t.Flags&lexer.PSLComment != 0 {
		return false
	}
	if t.Kind == token.Identifier {
		return t.Word == 0
	}
	return t.Kind.IsKeyword()
}

// upperCase writes every reserved word and basic identifier in upper
// case (VHDL is case-insensitive there).
func upperCase(src []byte, o parser.Options) []byte {
	return edit(src, o, func(t lexer.Token, text []byte) []byte {
		if plainWord(t) {
			return bytes.ToUpper(text)
		}
		return text
	}, sameTrivia)
}

// renameSuffix is appended to every renamed object.
const renameSuffix = "_rn"

// rename renames every signal, variable and constant the run's files
// declare (object declarations, not ports or generics, which a
// testbench or tool may name) by appending renameSuffix, consistently
// in every file of the run.
func rename(src map[string][]byte, o parser.Options) map[string][]byte {
	names := map[string]bool{}
	keys := slices.Sorted(maps.Keys(src))
	for _, k := range keys {
		if isVHDL(k) {
			declaredObjects(src[k], o, names)
		}
	}
	out := map[string][]byte{}
	for _, k := range keys {
		b := src[k]
		if isVHDL(k) {
			b = edit(b, o, func(t lexer.Token, text []byte) []byte {
				// Identifiers in `-- psl` lines are renamed too: they
				// name the same objects (#109).
				if t.Kind == token.Identifier && t.Word == 0 &&
					names[strings.ToLower(string(text))] {
					return append(bytes.Clone(text), renameSuffix...)
				}
				return text
			}, sameTrivia)
		}
		out[k] = b
	}
	return out
}

// declaredObjects adds the names of the object declarations of src:
// the identifiers after signal, variable, constant or shared variable
// at the start of a declaration, up to the colon. External names
// (<<signal path : type>>) declare nothing.
func declaredObjects(src []byte, o parser.Options, names map[string]bool) {
	toks, _ := lexer.All(src, lexOpts(o), lexer.VHDL)
	for i, t := range toks {
		switch t.Kind {
		case token.KwSignal, token.KwVariable, token.KwConstant:
		default:
			continue
		}
		if i > 0 {
			switch toks[i-1].Kind {
			case token.Semicolon, token.KwIs, token.KwShared,
				token.KwBegin, token.KwGenerate:
			default:
				continue
			}
		}
	list:
		for j := i + 1; j < len(toks); j++ {
			switch toks[j].Kind {
			case token.Identifier:
				if plainWord(toks[j]) {
					names[strings.ToLower(string(
						src[toks[j].Start:toks[j].End]))] = true
				}
			case token.Comma:
			default:
				break list
			}
		}
	}
}

// reorder reverses the concurrent statements of every architecture
// body whose statement part has no tool directive: concurrent
// statements are independent, so their order must not matter. A
// statement written in a `-- psl` comment moves with its `-- psl`
// marker (#109). A `-- psl` declaration in the statement part (default
// clock, property, sequence, endpoint) stays in place and only the
// statements between two of them are reversed: a `-- psl default
// clock` clocks the directives after it, so moving it would change
// which directives it clocks (#109).
func reorder(src []byte, o parser.Options) []byte {
	res := parser.Parse(src, o)
	if len(res.Problems) > 0 {
		return src
	}
	marker := pslMarkers(src, o)
	type span struct{ start, end int }
	var parts [][]span
	for n := range res.Tree().Descendants() {
		a, ok := cst.As[cst.ArchitectureBody](n)
		if !ok {
			continue
		}
		r := a.Stmts().Span()
		if bytes.IndexByte(src[r.Start:r.End], '`') >= 0 {
			continue
		}
		var run []span
		for it := range a.Stmts().Items() {
			if pslDecl(it.Node) {
				parts, run = append(parts, run), nil
				continue
			}
			r := it.Span()
			if m, ok := marker[r.Start]; ok {
				r.Start = m
			}
			run = append(run, span{r.Start, r.End})
		}
		parts = append(parts, run)
	}
	var b bytes.Buffer
	at := 0
	for _, items := range parts {
		if len(items) < 2 {
			continue
		}
		for k, it := range items {
			from := items[len(items)-1-k]
			b.Write(src[at:it.start])
			b.Write(src[from.start:from.end])
			at = it.end
		}
	}
	b.Write(src[at:])
	return b.Bytes()
}

// pslDecl reports whether n is a PSL default clock, property, sequence
// or endpoint declaration.
func pslDecl(n cst.Node) bool {
	switch n.Kind() {
	case cst.KindPSLDefaultClock, cst.KindPSLPropertyDecl,
		cst.KindPSLSequenceDecl, cst.KindPSLEndpointDecl:
		return true
	}
	return false
}

// pslMarkers maps the start of each token that opens a `-- psl`
// comment line to the start of that line's `-- psl` marker.
func pslMarkers(src []byte, o parser.Options) map[int]int {
	out := map[int]int{}
	toks, lx := lexer.All(src, lexOpts(o), lexer.VHDL)
	for _, t := range toks {
		if t.Flags&lexer.PSLComment == 0 {
			continue
		}
		for _, tv := range lx.Leading(t) {
			if tv.Kind == token.PSLCommentMarker {
				out[t.Start] = tv.Start
				break
			}
		}
	}
	return out
}
