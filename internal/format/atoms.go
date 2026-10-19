package format

import (
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/parser"
	"github.com/connerohnesorge/gohard/internal/token"
)

// atom is a piece of the file the formatter never changes except for
// the case of a reserved word: a token, a comment or other non-blank
// trivia, or a verbatim chunk of whole lines.
type atom struct {
	start, end int // source bytes
	// kind is the token or trivia kind, 0 for a verbatim chunk.
	kind token.Kind
	// tok is the token for a token atom.
	tok cst.Token
	// text is the output text: the source text, with keyword case
	// applied to reserved words and line comments without trailing
	// whitespace.
	text string
	// psl marks the pieces of a `-- psl` comment line, whose spacing
	// is comment content and kept.
	psl bool
}

func (a *atom) isToken() bool { return !a.tok.IsZero() }
func (a *atom) isChunk() bool { return a.kind == 0 }
func (a *atom) isComment() bool {
	return a.kind == token.LineComment || a.kind == token.BlockComment
}

// span is a half-open byte range.
type span struct{ start, end int }

// collect cuts the file into atoms in source order and returns the
// ranges that must stay verbatim.
func collect(res *parser.Result, src []byte, opts Options) ([]atom,
	[]span) {
	var atoms []atom
	var verb []span
	root := res.Tree()
	add := func(a atom) {
		switch a.kind {
		case token.Directive, token.ProtectedData, token.Error,
			token.Unparsed:
			verb = append(verb, span{a.start, a.end})
		}
		atoms = append(atoms, a)
	}
	for t := range root.DescendantTokens() {
		off := t.FullSpan().Start
		psl := t.Flags()&cst.FlagPSLComment != 0
		trivia := func(ts []cst.Trivia) {
			for _, tr := range ts {
				end := off + len(tr.Text)
				// Blank protected data (only line ends and blanks) is
				// layout: blank lines like any other.
				blank := tr.Kind == token.ProtectedData &&
					strings.Trim(tr.Text, " \t\v\f\r\n") == ""
				if tr.Kind != token.Whitespace &&
					tr.Kind != token.Newline && !blank {
					text := tr.Text
					if tr.Kind == token.LineComment {
						// A line comment runs to the line end: its
						// trailing whitespace is the line's.
						text = strings.TrimRight(text, " \t\v\f")
					}
					add(atom{start: off, end: end, kind: tr.Kind,
						text: text,
						psl:  psl || tr.Kind == token.PSLCommentMarker})
				}
				off = end
			}
		}
		trivia(t.Leading())
		if t.Text() != "" {
			add(atom{start: off, end: off + len(t.Text()),
				kind: t.Kind(), tok: t, psl: psl,
				text: keywordText(t, psl, opts)})
		}
		off += len(t.Text())
		trivia(t.Trailing())
	}
	for _, r := range res.Inactive {
		verb = append(verb, span{r.Start, r.End})
	}
	for n := range root.Descendants() {
		switch n.Kind() {
		case cst.KindErrorNode, cst.KindInactiveBranch:
			s := n.Span()
			verb = append(verb, span{s.Start, s.End})
		}
	}
	return atoms, verb
}

// keywordText returns the output text of token t: reserved words in
// the configured case, everything else as written.
func keywordText(t cst.Token, psl bool, opts Options) string {
	text := t.Text()
	kw, ok := t.Kind().Keyword()
	if !ok || psl || t.Inactive() || opts.KeywordCase == Preserve ||
		kw.Class == token.ClassNever ||
		(kw.Class == token.ClassVHDL && kw.Gated(opts.Standard)) {
		return text
	}
	b := []byte(text)
	for i, c := range b {
		switch {
		case opts.KeywordCase == Lower && c >= 'A' && c <= 'Z':
			b[i] = c + 'a' - 'A'
		case opts.KeywordCase == Upper && c >= 'a' && c <= 'z':
			b[i] = c - ('a' - 'A')
		}
	}
	return string(b)
}

// chunkify widens the verbatim ranges to whole lines and to whole
// atoms, merges them, and replaces the atoms they cover with one chunk
// atom each. A range of blank lines becomes a chunk too, so blank lines
// inside an inactive branch are kept.
func chunkify(src []byte, atoms []atom, verb []span) []atom {
	var vs []span
	for _, v := range verb {
		if v.end > v.start {
			if a := lineAlign(src, v); a.end > a.start {
				vs = append(vs, a)
			}
		}
	}
	if len(vs) == 0 {
		return atoms
	}
	for {
		vs = mergeSpans(vs)
		changed := false
		for i := range vs {
			j, _ := slices.BinarySearchFunc(atoms, vs[i].start,
				func(a atom, off int) int { return cmpInt(a.end, off+1) })
			for ; j < len(atoms) && atoms[j].start < vs[i].end; j++ {
				a := atoms[j]
				if a.start < vs[i].start || a.end > vs[i].end {
					vs[i] = cover(src, span{min(a.start,
						vs[i].start), max(a.end, vs[i].end)})
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	out := make([]atom, 0, len(atoms)+len(vs))
	k := 0
	chunk := func() {
		text := string(src[vs[k].start:vs[k].end])
		if vs[k].end == len(src) {
			// A chunk can own the file's last line ending (protected
			// data runs to the end of the file); the file's final
			// newline is the renderer's.
			text = trimFileEnd(text)
		}
		if text != "" {
			// A blank chunk at the end of the file is trailing
			// blank lines: the final gap.
			out = append(out, atom{start: vs[k].start, end: vs[k].end,
				text: text})
		}
		k++
	}
	for _, a := range atoms {
		for k < len(vs) && vs[k].start <= a.start {
			chunk()
		}
		if k > 0 && a.start < vs[k-1].end {
			continue // inside the chunk just written
		}
		out = append(out, a)
	}
	for k < len(vs) {
		chunk()
	}
	return out
}

// trimFileEnd drops the blank lines and the line ending that end s,
// keeping the last non-blank line whole (trailing spaces included); a
// blank s gives "".
func trimFileEnd(s string) string {
	e := len(strings.TrimRight(s, " \t\v\f\r\n"))
	for e > 0 && e < len(s) && s[e] != '\n' && s[e] != '\r' {
		e++
	}
	return s[:e]
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// mergeSpans sorts spans and merges the overlapping ones.
func mergeSpans(vs []span) []span {
	slices.SortFunc(vs, func(a, b span) int {
		if a.start != b.start {
			return a.start - b.start
		}
		return a.end - b.end
	})
	out := vs[:0]
	for _, v := range vs {
		if n := len(out); n > 0 && v.start < out[n-1].end {
			out[n-1].end = max(out[n-1].end, v.end)
			continue
		}
		out = append(out, v)
	}
	return out
}

// cover line-aligns v without shrinking it: an atom that ends with a
// line end keeps it, and the chunk then runs to the end of the next
// line.
func cover(src []byte, v span) span {
	a := lineAlign(src, v)
	if a.end < v.end {
		a.end = v.end
		for a.end < len(src) && src[a.end] != '\n' && src[a.end] != '\r' {
			a.end++
		}
	}
	return a
}

// lineAlign widens v to the start of its first line and the end of its
// last line (before the line's newline). A range that ends with a
// newline ends on the line that newline ends.
func lineAlign(src []byte, v span) span {
	s := v.start
	for s > 0 && src[s-1] != '\n' && src[s-1] != '\r' {
		s--
	}
	e := v.end
	switch {
	case e > s && src[e-1] == '\n':
		e--
		if e > s && src[e-1] == '\r' {
			e--
		}
	case e > s && src[e-1] == '\r':
		e--
	default:
		for e < len(src) && src[e] != '\n' && src[e] != '\r' {
			e++
		}
	}
	return span{s, max(e, s)}
}
