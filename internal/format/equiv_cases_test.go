package format

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/parser"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

func parseTree(t *testing.T, src string) cst.Node {
	t.Helper()
	res := parser.Parse([]byte(src), parser.Options{
		Standard: token.Std2008})
	if !Parses(res) {
		t.Fatalf("does not parse: %v\n%s", res.Problems, src)
	}
	return res.Tree()
}

const equivBase = "entity e is\n" +
	"  port (a : in bit; \\Ext Id\\ : out bit); -- c1\n" +
	"end entity;\n" +
	"architecture rtl of e is\n" +
	"  constant s : string := \"Ab c\"; /* b1 */\n" +
	"  constant v : bit_vector := X\"aF\";\n" +
	"begin\n" +
	"  -- c2\n" +
	"  y <= a and '1';\n" +
	"end;\n"

func TestSameTreeAccepts(t *testing.T) {
	// Layout and keyword case are all that may differ.
	base := parseTree(t, equivBase)
	for _, src := range []string{
		equivBase,
		strings.ReplaceAll(equivBase, "  ", "\t \t"),
		strings.ReplaceAll(equivBase, "\n", "\r\n\n"),
		strings.ReplaceAll(equivBase, " : ", ":"),
		strings.ReplaceAll(strings.ReplaceAll(equivBase, "entity",
			"ENTITY"), "begin", "BeGiN"),
		strings.ReplaceAll(equivBase, "-- c1\n", "-- c1 \t\n"),
	} {
		if err := sameTree(base, parseTree(t, src)); err != nil {
			t.Errorf("%v\n%s", err, src)
		}
	}
}

func TestSameTreeRejects(t *testing.T) {
	// Every change formatting must not make is reported.
	base := parseTree(t, equivBase)
	for _, c := range []struct{ old, new string }{
		{`"Ab c"`, `"ab c"`},                  // string literal case
		{`"Ab c"`, `"Ab  c"`},                 // string literal spacing
		{`X"aF"`, `X"AF"`},                    // bit-string literal
		{`X"aF"`, `x"aF"`},                    // bit-string base
		{`'1'`, `'0'`},                        // character literal
		{`\Ext Id\`, `\ext Id\`},              // extended identifier case
		{`\Ext Id\`, `\Ext  Id\`},             // extended identifier spacing
		{"y <=", "Y <="},                      // identifier case
		{"-- c1", "-- C1"},                    // line comment text
		{"-- c1", "--  c1"},                   // line comment spacing
		{"/* b1 */", "/*  b1 */"},             // block comment contents
		{"/* b1 */", ""},                      // comment dropped
		{"-- c2\n  y <= a", "y <= a -- c2\n"}, // comment moved
		{"a and '1'", "(a and '1')"},          // tokens added
		{"end;\n", "end architecture;\n"},     // optional syntax added
	} {
		if !strings.Contains(equivBase, c.old) {
			t.Fatalf("%q not in the base", c.old)
		}
		src := strings.Replace(equivBase, c.old, c.new, 1)
		if err := sameTree(base, parseTree(t, src)); err == nil {
			t.Errorf("%q -> %q not reported", c.old, c.new)
		}
	}
}

func TestSameTreeNodeKinds(t *testing.T) {
	// Same tokens and trivia under different node kinds or nesting
	// differ.
	build := func(outer, inner cst.Kind, nest bool) cst.Node {
		b := cst.NewBuilder()
		b.StartNode(outer)
		if nest {
			b.StartNode(inner)
		}
		b.Token(token.Identifier, "a", nil, nil, 0)
		if nest {
			b.FinishNode()
		}
		b.Token(token.Semicolon, ";", nil, nil, 0)
		b.FinishNode()
		return cst.NewRoot(b.Finish())
	}
	a := build(cst.KindDesignFile, cst.KindLabel, true)
	if err := sameTree(a, build(cst.KindDesignFile, cst.KindLabel,
		true)); err != nil {
		t.Fatal(err)
	}
	for _, b := range []cst.Node{
		build(cst.KindDesignFile, cst.KindUseClause, true),
		build(cst.KindDesignUnit, cst.KindLabel, true),
		build(cst.KindDesignFile, cst.KindLabel, false),
	} {
		err := sameTree(a, b)
		if err == nil || !strings.Contains(err.Error(), "DesignFile") &&
			!strings.Contains(err.Error(), "(") {
			t.Errorf("not reported: %v", err)
		}
	}
}

func TestOnlyLayout(t *testing.T) {
	src := []byte("Entity e IS end;\n")
	root := parseTree(t, string(src))
	ok := [][]Edit{
		nil,
		{{Start: 0, End: 6, NewText: "entity"}},
		{{Start: 6, End: 7, NewText: "  "}, {Start: 9, End: 11,
			NewText: "is"}},
		{{Start: 8, End: 12, NewText: " is "}}, // gap and keyword
		{{Start: 16, End: 17, NewText: "\r\n"}},
	}
	for _, es := range ok {
		if err := onlyLayout(src, root, es); err != nil {
			t.Errorf("%v: %v", es, err)
		}
	}
	bad := [][]Edit{
		{{Start: 7, End: 8, NewText: "E"}},       // identifier case
		{{Start: 7, End: 8, NewText: ""}},        // identifier dropped
		{{Start: 15, End: 15, NewText: ";"}},     // token added
		{{Start: 0, End: 6, NewText: "entity;"}}, // more than case
	}
	for _, es := range bad {
		if err := onlyLayout(src, root, es); err == nil {
			t.Errorf("%v not reported", es)
		}
	}
	// NBSP is whitespace to the lexer, so formatting may drop it.
	nb := []byte(" entity e is end;\n")
	if err := onlyLayout(nb, parseTree(t, string(nb)), []Edit{{Start: 0,
		End: 2}}); err != nil {
		t.Error(err)
	}
}

func TestOneFinalNewline(t *testing.T) {
	for s, want := range map[string]bool{"": true, "a\n": true,
		"a\r\n": true, "a": false, "a\n\n": false, "a\r\n\r\n": false,
		"\n": true} {
		if got := oneFinalNewline([]byte(s)); got != want {
			t.Errorf("%q: %v", s, got)
		}
	}
}

func TestTrimFileEnd(t *testing.T) {
	for in, want := range map[string]string{"": "", "0": "0",
		"0\n": "0", "0 \n  \n\r\n": "0 ", "a\nb \t\r\n": "a\nb \t",
		" \n": ""} {
		if got := trimFileEnd(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestProtectedDataAtFileEnd(t *testing.T) {
	// Protected data owns the file's last line ending and blank lines;
	// the output still ends in exactly one newline, idempotently, and
	// blank protected data is blank lines.
	for in, want := range map[string]string{
		"`protect key_block\n0":           "`protect key_block\n0\n",
		"`protect key_block\n0\n":         "`protect key_block\n0\n",
		"`protect key_block\n0 \n \n\n":   "`protect key_block\n0 \n",
		"`protect key_block\r\n0\r\n\r\n": "`protect key_block\r\n0\r\n",
		"`protect key_block\n \n":         "`protect key_block\n",
		"`protect key_block\n \n\t\n":     "`protect key_block\n",
		"`protect key_block\n\n":          "`protect key_block\n",
		"`protect data_block\n  \n\n`protect end_protected\n": "" +
			"`protect data_block\n\n`protect end_protected\n",
	} {
		popts := parser.Options{Standard: token.Std2008}
		if err := checkInvariants([]byte(in), popts,
			Default()); err != nil {
			t.Errorf("%q: %v", in, err)
		}
		r, err := Source([]byte(in), popts, Default())
		if err != nil || string(r.Text) != want {
			t.Errorf("%q: got %q, %v; want %q", in, r.Text, err, want)
		}
	}
}

// TestPSLTailWhitespace: after the `;` that ends a `-- psl` directive
// the rest of the line is LineComment text, which may be only trailing
// whitespace. Formatting removes it, and that is layout: the FuzzFormat
// crasher 2c906833ad6528cc. Real text after the `;` still counts.
func TestPSLTailWhitespace(t *testing.T) {
	po := parser.Options{Standard: token.Std1993, PSLComments: true}
	for _, src := range []string{
		" ArchiteCture B of A is\nbegin --psl end; ",
		"architecture B of A is\nbegin --psl end; \t\n",
		"architecture B of A is\nbegin --psl end;  -- x  \n",
	} {
		if err := checkInvariants([]byte(src), po, Default()); err != nil {
			t.Errorf("%q: %v", src, err)
		}
	}
	tree := func(s string) cst.Node {
		return parser.Parse([]byte(s), po).Tree()
	}
	a := tree("architecture B of A is\nbegin --psl end; x\n")
	b := tree("architecture B of A is\nbegin --psl end;\n")
	if sameTree(a, b) == nil {
		t.Errorf("text after a psl directive's `;` was ignored")
	}
}
