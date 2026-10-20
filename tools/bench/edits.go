package main

import (
	"bytes"
	"regexp"

	"github.com/connerohnesorge/gohard/internal/env"
)

// Edit is one keystroke: the text that replaces the range from
// (Line, Char) to (EndLine, EndChar), zero-based, in UTF-16 code units
// (the files the benchmark edits are ASCII on the edited lines).
type Edit struct {
	Line, Char, EndLine, EndChar int
	Text                         string
}

// Pos is a zero-based position in a document.
type Pos struct{ Line, Char int }

// typed is what a keystroke inserts at the start of a code line: a
// stray identifier, so every insertion changes the file's diagnostics
// and every following deletion changes them back.
const typed = "x "

// codeLines returns the zero-based numbers of the lines of text that
// hold code: not blank, not a comment, ASCII only. A file with block
// comments has none, so no insertion lands inside a comment.
func codeLines(text []byte) []int {
	if bytes.Contains(text, []byte("/*")) {
		return nil
	}
	var out []int
	for i, l := range bytes.Split(text, []byte("\n")) {
		t := bytes.TrimSpace(l)
		if len(t) == 0 || bytes.HasPrefix(t, []byte("--")) ||
			!ascii(l) {
			continue
		}
		out = append(out, i)
	}
	return out
}

func ascii(b []byte) bool {
	for _, c := range b {
		if c >= 0x80 {
			return false
		}
	}
	return true
}

// Keystrokes returns n edits of text as a pure function of (text,
// seed): pairs that insert typed at the start of a code line and then
// delete it again, so after every even number of edits the text is the
// original. It returns nil when text has no code line.
func Keystrokes(text []byte, seed uint64, n int) []Edit {
	lines := codeLines(text)
	if len(lines) == 0 {
		return nil
	}
	r := env.SplitMix64{State: seed}
	out := make([]Edit, 0, n)
	for len(out) < n {
		l := lines[r.Uint64()%uint64(len(lines))]
		out = append(out, Edit{Line: l, EndLine: l, Text: typed})
		if len(out) < n {
			out = append(out, Edit{Line: l, EndLine: l,
				EndChar: len(typed)})
		}
	}
	return out
}

// Apply returns text with e applied (positions are byte columns,
// which equal UTF-16 columns on the ASCII lines Keystrokes edits).
func Apply(text []byte, e Edit) []byte {
	off := func(line, char int) int {
		o := 0
		for range line {
			i := bytes.IndexByte(text[o:], '\n')
			if i < 0 {
				return len(text)
			}
			o += i + 1
		}
		return min(o+char, len(text))
	}
	a, b := off(e.Line, e.Char), off(e.EndLine, e.EndChar)
	out := append([]byte{}, text[:a]...)
	out = append(out, e.Text...)
	return append(out, text[b:]...)
}

var word = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_]*`)

// keywords are skipped when picking names to query.
var keywords = map[string]bool{
	"begin": true, "end": true, "is": true, "if": true, "then": true,
	"else": true, "elsif": true, "when": true, "others": true,
	"signal": true, "constant": true, "variable": true, "port": true,
	"generic": true, "in": true, "out": true, "inout": true,
	"process": true, "case": true, "for": true, "loop": true,
	"generate": true, "and": true, "or": true, "not": true,
	"to": true, "downto": true, "of": true, "map": true, "use": true,
	"library": true, "all": true, "architecture": true,
	"entity": true, "package": true, "body": true, "function": true,
	"procedure": true, "return": true, "type": true, "subtype": true,
	"range": true, "wait": true, "until": true, "null": true,
	"record": true, "array": true, "component": true, "xor": true,
}

// Names returns n query positions in text (the middle of a name that
// is not a keyword, on a code line) as a pure function of (text,
// seed); nil when text has none.
func Names(text []byte, seed uint64, n int) []Pos {
	src := bytes.Split(text, []byte("\n"))
	var all []Pos
	for _, l := range codeLines(text) {
		for _, m := range word.FindAllIndex(src[l], -1) {
			w := bytes.ToLower(src[l][m[0]:m[1]])
			if keywords[string(w)] {
				continue
			}
			all = append(all, Pos{l, (m[0] + m[1]) / 2})
		}
	}
	if len(all) == 0 {
		return nil
	}
	r := env.SplitMix64{State: seed}
	out := make([]Pos, n)
	for i := range out {
		out[i] = all[r.Uint64()%uint64(len(all))]
	}
	return out
}
