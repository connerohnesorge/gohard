package parser

import (
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/token"
)

// TestExprOracleForms pins the expression forms the GHDL and NVC
// regression suites need (#31 oracle): chained relational operators
// (NVC regress/range4), an operator symbol as a selected-name prefix
// (VESTS tc945), indexing and slicing an attribute's value (tc1038,
// tc1091), a character literal as an attribute prefix (attr21,
// issue369), replacement characters (tc2853, based.vhd) and VHDL-2019
// conditional expressions as association actuals (issue1607).
func TestExprOracleForms(t *testing.T) {
	runCases(t, fnExpr, []exCase{
		{"a > b = false", "(BinaryExpr (BinaryExpr a > b) = false)"},
		{`"+".d`, `(SelectedName (OperatorSymbol "+") . d)`},
		{`"-".b.v`,
			`(SelectedName (SelectedName (OperatorSymbol "-") . b) . v)`},
		{"PT'AT2(2, 3)",
			"(CallOrIndexName (AttributeName PT ' AT2) (AssociationList " +
				"( (AssociationElement 2) , (AssociationElement 3) )))"},
		{"PT'AT1(2 to 4)",
			"(SliceName (AttributeName PT ' AT1) ( (RangeExpr 2 to 4) ))"},
		{"t'image(x)", "(AttributeName t ' image ( x ))"},
		{"'1' [return my_enum]'test",
			"(AttributeName (CharacterName '1') (Signature [ return " +
				"my_enum ]) ' test)"},
		{"P.'1''a'RIGHT",
			"(AttributeName (AttributeName (SelectedName P . '1') ' a) ' " +
				"RIGHT)"},
		{"'1''a", "(AttributeName (CharacterName '1') ' a)"},
		{"(0 ! 1 => '1')",
			"(Aggregate ( (ElementAssociation (Choices 0 ! 1) => '1') ))"},
		{"2:1_0: + 16:F.FF:E+2", "(BinaryExpr 2:1_0: + 16:F.FF:E+2)"},
		{"%ab%%c% & X%FF%", "(BinaryExpr %ab%%c% & X%FF%)"},
		{"f(a => 2 when b = 7 else 1, v when c else '1')",
			"(CallOrIndexName f (AssociationList ( (AssociationElement a " +
				"=> (ConditionalExpr (WhenBranch 2 when (BinaryExpr b = 7)) " +
				"else (WhenBranch 1))) , (AssociationElement " +
				"(ConditionalExpr (WhenBranch v when c) else (WhenBranch " +
				"'1'))) )))"},
	})
}

// TestAssociationElementAccessors: the typed wrapper of [formal =>]
// actual. A positional element has no formal and its only Expr is the
// actual; a named one has both (the LSP and sema read these).
func TestAssociationElementAccessors(t *testing.T) {
	r := exRun("f(x, a => y, b(1) => open)", token.Std2019, fnExpr)
	var got []string
	for n := range r.root.Descendants() {
		e, ok := cst.As[cst.AssociationElement](n)
		if !ok {
			continue
		}
		f := "-"
		if !e.Formal().IsZero() {
			f = e.Formal().Text()
		}
		got = append(got, f+"|"+e.Actual().Text())
	}
	want := []string{"-|x", "a |y", "b(1) |open", "-|1"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestTrailingCommentIsNotDocComment: a same-line comment after a
// declaration stays with it (trailing trivia); the next declaration's
// doc comment is only the block right above it (#31).
func TestTrailingCommentIsNotDocComment(t *testing.T) {
	src := "package p is\n  signal a : bit; -- about a\n" +
		"  signal b : bit;\n  -- doc c\n  signal c : bit;\nend;\n"
	r := Parse([]byte(src), Options{})
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
	want := []string{"", "", "-- doc c"}
	if !slices.Equal(docs, want) {
		t.Errorf("doc comments %q, want %q", docs, want)
	}
}
