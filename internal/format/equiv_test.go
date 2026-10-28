package format

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// This file holds the #32 equivalence checks shared by FuzzFormat, the
// property tests and the rule tests: sameTree compares two trees as
// formatting must keep them, and onlyLayout checks that the edits of
// a result touch nothing but whitespace and keyword case.

// treeEvents flattens the tree under n into what formatting must keep,
// in order:
//
//   - "(Kind" and ")" for every node, so node kinds and nesting match;
//   - one entry per token: its kind, its flags and its text, keywords
//     lowered (keyword case may change, all other token text, which
//     includes every literal and extended identifier, is compared byte
//     for byte);
//   - one entry per comment, directive line and protected-data line,
//     with its text (a line comment without its trailing whitespace,
//     which is the line's trailing whitespace, the others without
//     final blank lines and line ending, so blank protected data is
//     layout); comments are emitted just before the next token, so a
//     comment must stay between the same two tokens but may move to
//     the other token's trivia;
//   - the exact text of every inactive conditional-analysis branch,
//     which formatting leaves byte for byte unchanged.
//
// Whitespace and line breaks are the only things left out.
func treeEvents(n cst.Node) []string {
	var out, pending []string
	trivia := func(ts []cst.Trivia) {
		for _, tr := range ts {
			switch tr.Kind {
			case token.Whitespace, token.Newline:
			case token.LineComment:
				// The rest of a `-- psl` line after the `;` that
				// ends the directive is LineComment text (#6) and may
				// be only the line's trailing whitespace, which
				// formatting removes: no comment is left.
				text := strings.TrimRight(tr.Text, " \t\v\f")
				if strings.TrimSpace(text) == "" {
					continue
				}
				pending = append(pending, "comment "+text)
			default:
				// Directive and protected-data text may own line
				// endings and, at the end of the file, blank lines;
				// those are the file's last line ending (#32).
				if text := trimFileEnd(tr.Text); text != "" {
					pending = append(pending, fmt.Sprintf("%v %q",
						tr.Kind, text))
				}
			}
		}
	}
	var walk func(n cst.Node)
	walk = func(n cst.Node) {
		out = append(out, "("+n.Kind().String())
		if n.Kind() == cst.KindInactiveBranch {
			out = append(out, fmt.Sprintf("verbatim %q", innerText(n)))
		}
		for e := range n.Elements() {
			if !e.IsToken() {
				walk(e.Node)
				continue
			}
			t := e.Token
			trivia(t.Leading())
			out = append(out, pending...)
			pending = pending[:0]
			text := t.Text()
			if t.Kind().IsKeyword() {
				text = strings.ToLower(text)
			}
			f := t.Flags()
			if t.Kind() == token.EOF {
				// EOF carries the PSL flag when a -- psl line ends
				// the file without a newline: the final newline
				// (layout) ends that line.
				f &^= cst.FlagPSLComment
			}
			out = append(out, fmt.Sprintf("%v %d %q", t.Kind(), f, text))
			trivia(t.Trailing())
		}
		out = append(out, ")")
	}
	walk(n)
	return append(out, pending...)
}

// innerText is the text of n from its first token to its last one,
// without the surrounding trivia (the layout around the node).
func innerText(n cst.Node) string {
	first, last := n.FirstToken(), n.LastToken()
	if first.IsZero() || last.IsZero() {
		return ""
	}
	full := n.FullSpan().Start
	return n.Text()[first.Span().Start-full : last.Span().End-full]
}

// sameTree reports the first difference between the trees under a
// and b as formatting must keep them (treeEvents), or nil when they
// are equivalent: same node kinds, same token text ignoring spacing
// with keywords compared case-insensitively, and the same comments in
// the same order.
func sameTree(a, b cst.Node) error {
	ea, eb := treeEvents(a), treeEvents(b)
	n := min(len(ea), len(eb))
	i := 0
	for i < n && ea[i] == eb[i] {
		i++
	}
	if i == len(ea) && i == len(eb) {
		return nil
	}
	path := openNodes(ea[:i])
	return fmt.Errorf("trees differ at event %d (in %s): %s vs %s", i,
		path, at(ea, i), at(eb, i))
}

// openNodes names the nodes still open after events, outermost first.
func openNodes(events []string) string {
	var stack []string
	for _, e := range events {
		switch {
		case e == ")":
			stack = stack[:len(stack)-1]
		case strings.HasPrefix(e, "("):
			stack = append(stack, e[1:])
		}
	}
	return strings.Join(stack, " > ")
}

func isSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\v', '\f', '\r', '\n':
		return true
	}
	return false
}

// layoutBytes marks the bytes of src that formatting may delete:
// Whitespace and Newline trivia (as the lexer classifies them, so NBSP
// counts) and the ASCII blanks anywhere else, such as the trailing
// whitespace the lexer keeps in a line comment. Blanks inside tokens
// and comments are kept by sameTree, which compares their text.
func layoutBytes(src []byte, root cst.Node) []bool {
	m := make([]bool, len(src))
	for i, c := range src {
		m[i] = isSpace(c)
	}
	for t := range root.DescendantTokens() {
		off := t.FullSpan().Start
		mark := func(ts []cst.Trivia) {
			for _, tr := range ts {
				if tr.Kind == token.Whitespace ||
					tr.Kind == token.Newline {
					for i := off; i < off+len(tr.Text); i++ {
						m[i] = true
					}
				}
				off += len(tr.Text)
			}
		}
		mark(t.Leading())
		off += len(t.Text())
		mark(t.Trailing())
	}
	return m
}

// onlyLayout checks that every edit turning src (parsed as root) into
// its formatted text changes only whitespace and the case of keyword
// tokens: with layout bytes removed from the old text and ASCII
// blanks from the new, the two are equal, or equal ignoring case with
// every changed byte inside a keyword token of src. Literals and
// extended identifiers are thus never touched.
func onlyLayout(src []byte, root cst.Node, edits []Edit) error {
	var ws, kw []bool
	for _, e := range edits {
		if ws == nil {
			ws = layoutBytes(src, root)
		}
		old := string(src[e.Start:e.End])
		var a, b []byte
		for i := e.Start; i < e.End; i++ {
			if !ws[i] {
				a = append(a, src[i])
			}
		}
		for i := range len(e.NewText) {
			if !isSpace(e.NewText[i]) {
				b = append(b, e.NewText[i])
			}
		}
		if bytes.Equal(a, b) {
			continue
		}
		if !bytes.EqualFold(a, b) {
			return fmt.Errorf("edit %+v replaces %q: not only "+
				"whitespace and keyword case", e, old)
		}
		if kw == nil {
			kw = keywordBytes(len(src), root)
		}
		for i := e.Start; i < e.End; i++ {
			if !ws[i] && !kw[i] {
				return fmt.Errorf("edit %+v changes %q, byte %d is not "+
					"in a keyword", e, old, i)
			}
		}
	}
	return nil
}

// keywordBytes marks the bytes of src that belong to keyword tokens.
func keywordBytes(n int, root cst.Node) []bool {
	kw := make([]bool, n)
	for t := range root.DescendantTokens() {
		if t.Kind().IsKeyword() {
			sp := t.Span()
			for i := sp.Start; i < sp.End && i < n; i++ {
				kw[i] = true
			}
		}
	}
	return kw
}

// oneFinalNewline reports whether non-empty text ends in exactly one
// line ending.
func oneFinalNewline(text []byte) bool {
	if len(text) == 0 {
		return true
	}
	body, ok := bytes.CutSuffix(text, []byte("\n"))
	body = bytes.TrimSuffix(body, []byte("\r"))
	return ok && !bytes.HasSuffix(body, []byte("\n"))
}
