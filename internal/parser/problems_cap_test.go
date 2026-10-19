package parser

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/diag"
)

// TestProblemCap: error-dense input records at most MaxProblems
// problems plus one "too many errors" that counts the rest, so a large
// junk file cannot keep one problem per byte alive (P7). (Measured
// live heap of a 2 MB NUL file: 218 MB before the cap, 18 MB after.)
func TestProblemCap(t *testing.T) {
	const n = 2_000_000
	for _, c := range []byte{0, '#', 0xFF} {
		src := bytes.Repeat([]byte{c}, n)
		r := Parse(src, Options{})
		if len(r.Problems) != MaxProblems+1 {
			t.Fatalf("%q: %d problems", c, len(r.Problems))
		}
		var tooMany []Problem
		for _, pr := range r.Problems {
			if pr.Start < 0 || pr.End > n || pr.Start > pr.End {
				t.Fatalf("%q: problem out of range %v", c, pr)
			}
			if pr.Code == diag.SyntaxError && len(pr.Args) == 1 &&
				strings.HasPrefix(pr.Args[0], "too many errors") {
				tooMany = append(tooMany, pr)
			}
		}
		// One lexical problem per byte plus the missing end of file.
		want := "too many errors; " + strconv.Itoa(n+1-MaxProblems) +
			" more not shown"
		if len(tooMany) != 1 || tooMany[0].Args[0] != want {
			t.Errorf("%q: too-many problems %v, want %q", c, tooMany,
				want)
		}
		if r.Tree().Text() != string(src) {
			t.Errorf("%q: round trip lost bytes", c)
		}
	}
}

// TestProblemCapBelow: under the cap nothing is dropped and no
// "too many errors" is reported.
func TestProblemCapBelow(t *testing.T) {
	src := bytes.Repeat([]byte{0}, MaxProblems-2)
	r := Parse(src, Options{})
	if len(r.Problems) != MaxProblems-1 {
		t.Fatalf("%d problems", len(r.Problems))
	}
	for _, pr := range r.Problems {
		if len(pr.Args) == 1 &&
			strings.HasPrefix(pr.Args[0], "too many errors") {
			t.Fatalf("unexpected %v", pr)
		}
	}
}
