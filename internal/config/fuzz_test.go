package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/span"
)

// FuzzConfig checks the #28 invariants on any bytes: Parse never
// panics, is deterministic, reports only codes from the diag table, and
// every position is a real line and column of the input. The saved
// inputs in the repository's testdata/fuzz/FuzzConfig/ (#30) and the
// golden inputs seed the corpus, so plain go test replays them.
func FuzzConfig(f *testing.F) {
	ins, _ := filepath.Glob("testdata/golden/*.yaml")
	for _, in := range ins {
		if b, err := os.ReadFile(in); err == nil {
			f.Add(b)
		}
	}
	saved, _ := filepath.Glob("../../testdata/fuzz/FuzzConfig/*")
	for _, name := range saved {
		b, err := readCorpusFile(name)
		if err != nil {
			f.Fatalf("%s: %v", name, err)
		}
		f.Add(b)
	}
	for _, s := range []string{
		"", "\xff", "a: [", "\ufeffstandard: x", "\xff\xfes\x00",
		"{é: yes}", "\r\r\n\t", "- - -", "? a\n", "!!binary x",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		c1, d1 := Parse(FileName, src)
		c2, d2 := Parse(FileName, src)
		if !slices.EqualFunc(d1, d2, func(a, b diag.Diagnostic) bool {
			return diag.Compare(a, b) == 0
		}) || !equalConfig(c1, c2) {
			t.Fatalf("Parse is not deterministic")
		}
		if !slices.IsSortedFunc(d1, diag.Compare) {
			t.Fatalf("diagnostics are not sorted")
		}
		idx := span.NewIndex(src)
		for _, d := range d1 {
			if _, ok := d.Code.Info(); !ok ||
				d.Code == diag.InternalUnknownCode {
				t.Fatalf("unknown code %s", d.Code)
			}
			if d.Path != FileName {
				t.Fatalf("path %q", d.Path)
			}
			if !realPosition(idx, d.Line, d.Col) {
				t.Fatalf("%d:%d is not a position of the input "+
					"(%d lines): %s", d.Line, d.Col, idx.LineCount(),
					d.Message)
			}
		}
		for _, k := range c1.SetKeys() {
			p := c1.Position(k)
			if !realPosition(idx, p.Line, p.Col) {
				t.Fatalf("%s at %d:%d is not real", k, p.Line, p.Col)
			}
		}
	})
}

// readCorpusFile decodes a Go fuzz corpus file with one []byte
// argument ("go test fuzz v1" then []byte("...")).
func readCorpusFile(name string) ([]byte, error) {
	raw, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	head, body, _ := strings.Cut(string(raw), "\n")
	body = strings.TrimSpace(body)
	lit, ok := strings.CutPrefix(body, "[]byte(")
	lit, ok2 := strings.CutSuffix(lit, ")")
	if head != "go test fuzz v1" || !ok || !ok2 {
		return nil, fmt.Errorf("not a one-argument []byte corpus file")
	}
	s, err := strconv.Unquote(lit)
	return []byte(s), err
}

// realPosition reports whether one-based line:col (UTF-8 bytes) names a
// rune start or the end of a line in the input.
func realPosition(idx *span.Index, line, col int) bool {
	if line < 1 || line > idx.LineCount() || col < 1 {
		return false
	}
	off, ok := idx.OffsetOf(line, col)
	if !ok {
		return false
	}
	l, c := idx.LineCol(off)
	return l == line && c == col
}

// TestLongLine parses a 100,000-entry mapping on one line with unknown,
// duplicate and invalid entries. Position conversion is linear, so this
// takes well under a second; a quadratic conversion would take minutes
// and hit the test timeout.
func TestLongLine(t *testing.T) {
	const N = 50000
	var b strings.Builder
	b.WriteString("{defines: {")
	for i := range N {
		fmt.Fprintf(&b, "A%d: yes, B%d: \"x\", ", i%(N/2), i)
	}
	b.WriteString("}, ")
	for i := range N {
		fmt.Fprintf(&b, "u%d: 1, ", i%100)
	}
	b.WriteString("}\n")
	_, ds := Parse(FileName, []byte(b.String()))
	if len(ds) != 2*N {
		t.Fatalf("%d diagnostics", len(ds))
	}
	// The input is ASCII on one line: every column up to the line end
	// is real (realPosition itself is linear per call).
	for _, d := range ds {
		if d.Line != 1 || d.Col < 1 || d.Col > b.Len() {
			t.Fatalf("%d:%d not real", d.Line, d.Col)
		}
	}
}
