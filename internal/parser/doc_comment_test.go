package parser

import (
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/token"
)

// TestBlockDocComment: a block comment spanning lines after a
// declaration's `;` is the next declaration's doc comment, not
// trailing trivia of the `;` (#10, #31); a one-line block comment on
// the same line stays with the declaration before it.
func TestBlockDocComment(t *testing.T) {
	src := "package p is\n  signal a : bit; /* x\n  y */\n" +
		"  signal b : bit; /* about b */\n  signal c : bit;\nend;\n"
	r := Parse([]byte(src), Options{Standard: token.Std2008})
	if len(r.Problems) != 0 {
		t.Fatalf("problems: %v", r.Problems)
	}
	var docs []string
	for n := range r.Tree().Descendants() {
		if n.Kind() != cst.KindSignalDecl {
			continue
		}
		var d []string
		for _, tr := range cst.DocComment(n.FirstToken()) {
			d = append(d, tr.Text)
		}
		docs = append(docs, strings.Join(d, ""))
	}
	want := []string{"", "/* x\n  y */", ""}
	if !slices.Equal(docs, want) {
		t.Errorf("doc comments %q, want %q", docs, want)
	}
}
