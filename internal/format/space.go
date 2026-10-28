package format

import (
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// spaceLines sets the in-line gaps of every line.
func (d *doc) spaceLines() {
	for _, ln := range d.lines {
		for i := ln.first + 1; i < ln.last; i++ {
			d.gap[i] = d.space(&d.atoms[i-1], &d.atoms[i],
				string(d.origGap(i)))
		}
	}
}

// spacing is what the rules want between two tokens.
type spacing uint8

const (
	keep spacing = iota // one space if there was any whitespace
	one                 // exactly one space
	none                // no space, when the tokens cannot merge
)

// space returns the gap between atoms a and b on one line, given the
// original gap.
func (d *doc) space(a, b *atom, orig string) string {
	if a.psl || b.psl {
		return orig // `-- psl` comment content
	}
	if b.isComment() {
		// A trailing comment keeps its distance (its column often
		// lines up with its neighbours).
		if orig == "" || strings.Trim(orig, " ") == "" {
			return orig
		}
		return " "
	}
	if !a.isToken() || !b.isToken() {
		return collapse(orig)
	}
	switch rule(a.tok, b.tok) {
	case one:
		return " "
	case none:
		if orig == "" || canJoin(a.text, b.text) {
			return ""
		}
	}
	return collapse(orig)
}

func collapse(orig string) string {
	if orig == "" {
		return ""
	}
	return " "
}

// rule returns the spacing #32 asks for between adjacent tokens a and
// b.
func rule(a, b cst.Token) spacing {
	ak, bk := a.Kind(), b.Kind()
	switch bk {
	case token.Comma, token.Semicolon, token.RParen:
		return none
	}
	switch ak {
	case token.LParen:
		return none
	case token.Comma, token.Semicolon:
		return one
	}
	if spaced(ak) || spaced(bk) || binaryOp(a) || binaryOp(b) {
		return one
	}
	if unarySign(a) {
		return none
	}
	if (ak == token.Dot && a.Parent().Kind() == cst.KindSelectedName) ||
		(bk == token.Dot && b.Parent().Kind() == cst.KindSelectedName) {
		return none
	}
	return keep
}

// spaced reports the delimiters that always have one space around
// them: `:=`, `<=`, `=>` and `:`.
func spaced(k token.Kind) bool {
	switch k {
	case token.VarAssign, token.Le, token.Arrow, token.Colon:
		return true
	}
	return false
}

// binaryOp reports whether t is the operator of a binary expression.
func binaryOp(t cst.Token) bool {
	return t.Parent().Kind() == cst.KindBinaryExpr
}

// unarySign reports whether t is a sign or `??` in front of its
// operand.
func unarySign(t cst.Token) bool {
	switch t.Kind() {
	case token.Plus, token.Minus, token.Condition:
		return t.Parent().Kind() == cst.KindUnaryExpr && t.Index() == 0
	}
	return false
}

// canJoin reports whether texts a and b, written with no space between
// them, still lex as the same two tokens.
func canJoin(a, b string) bool {
	if a == "" || b == "" {
		return true
	}
	x, y := a[len(a)-1], b[0]
	if wordByte(x) && wordByte(y) {
		return false
	}
	if opByte(x) && opByte(y) {
		return false // `-` `-`, `/` `*`, `<` `=`, ...
	}
	if (x == '\'' || y == '\'') && x != '(' {
		return y != '\'' && x == '\'' && !wordByte(y) && !opByte(y)
	}
	return true
}

func wordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' || c == '_' || c == '\\' || c == '"' ||
		c == '#' || c >= 0x80
}

func opByte(c byte) bool {
	return strings.IndexByte("<>=:/*&|?-+.!@^~[]{}%", c) >= 0
}
