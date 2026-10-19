package cst

import (
	"strconv"
)

// Dump returns the stable text form of the tree under n, used by
// `gohard parse --dump` and the testdata/parse goldens (#31). One line
// per element, indented two spaces per level:
//
//	EntityDecl 0..42
//	  KwEntity 0..6 "entity"
//	    trail Whitespace " "
//	  Identifier 7..8 "e" missing
//
// A node line shows its kind and full span (with trivia). A token line
// shows its kind, the span of its text without trivia, the quoted text
// and its flags (missing, inactive, psl-comment); its trivia follow on
// lines starting "lead" or "trail". Text is quoted with strconv.Quote,
// so invalid UTF-8 and control bytes print the same everywhere. The form
// depends only on the tree, never on addresses or map order.
func Dump(n Node) string { return string(AppendDump(nil, n)) }

// AppendDump appends Dump(n) to dst.
func AppendDump(dst []byte, n Node) []byte {
	if n.IsZero() {
		return dst
	}
	return dumpNode(dst, n, 0)
}

func indent(dst []byte, depth int) []byte {
	for range depth {
		dst = append(dst, "  "...)
	}
	return dst
}

func appendRange(dst []byte, r Range) []byte {
	dst = strconv.AppendInt(dst, int64(r.Start), 10)
	dst = append(dst, ".."...)
	return strconv.AppendInt(dst, int64(r.End), 10)
}

func dumpNode(dst []byte, n Node, depth int) []byte {
	dst = indent(dst, depth)
	dst = append(dst, n.Kind().String()...)
	dst = append(dst, ' ')
	dst = appendRange(dst, n.FullSpan())
	dst = append(dst, '\n')
	for e := range n.Elements() {
		if e.IsToken() {
			dst = dumpToken(dst, e.Token, depth+1)
		} else {
			dst = dumpNode(dst, e.Node, depth+1)
		}
	}
	return dst
}

func dumpToken(dst []byte, t Token, depth int) []byte {
	for _, tr := range t.Leading() {
		dst = dumpTrivia(dst, "lead", tr, depth+1)
	}
	dst = indent(dst, depth)
	dst = append(dst, t.Kind().String()...)
	dst = append(dst, ' ')
	dst = appendRange(dst, t.Span())
	dst = append(dst, ' ')
	dst = strconv.AppendQuote(dst, t.Text())
	f := t.Flags()
	if f&FlagMissing != 0 {
		dst = append(dst, " missing"...)
	}
	if f&FlagInactive != 0 {
		dst = append(dst, " inactive"...)
	}
	if f&FlagPSLComment != 0 {
		dst = append(dst, " psl-comment"...)
	}
	dst = append(dst, '\n')
	for _, tr := range t.Trailing() {
		dst = dumpTrivia(dst, "trail", tr, depth+1)
	}
	return dst
}

func dumpTrivia(dst []byte, side string, tr Trivia, depth int) []byte {
	dst = indent(dst, depth)
	dst = append(dst, side...)
	dst = append(dst, ' ')
	dst = append(dst, tr.Kind.String()...)
	dst = append(dst, ' ')
	dst = strconv.AppendQuote(dst, tr.Text)
	return append(dst, '\n')
}
