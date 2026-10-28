package parser

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/lexer"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// exResult is the outcome of running one grammar function on a snippet.
type exResult struct {
	root  cst.Node
	node  cst.Node // the first node the function built (zero if none)
	probs []Problem
	ok    bool
	rest  bool // tokens were left after the function returned
	dead  bool
}

// exRun parses src with fn as the only grammar function: the snippet
// is wrapped in a DesignFile, leftovers go into an ErrorNode (finish).
func exRun(src string, std token.Standard,
	fn func(*Parser) bool,
) exResult {
	opts := Options{Standard: std}
	p := newParser([]byte(src), opts, lexerOptions(opts))
	p.open(cst.KindDesignFile)
	ok := fn(p)
	rest := !p.atEOF()
	dead := p.dead
	p.finish()
	p.close()
	root := cst.NewRoot(p.b.Finish())
	var node cst.Node
	for n := range root.Nodes() {
		node = n
		break
	}
	return exResult{root: root, node: node, probs: p.probs, ok: ok,
		rest: rest, dead: dead}
}

var (
	fnExpr = func(p *Parser) bool { return p.parseExpression() }
	fnCond = func(p *Parser) bool {
		return p.parseConditionalExpression()
	}
	fnName    = func(p *Parser) bool { return p.parseName() }
	fnChoices = func(p *Parser) bool { return p.parseChoices() }
	fnAssoc   = func(p *Parser) bool {
		p.parseAssociationList()
		return true
	}
	fnTarget = func(p *Parser) bool { return p.parseNameOrAggregate() }
)

// sexpr prints a node compactly: (Kind child ...), a SimpleName or a
// LiteralExpr as its bare text, tokens as their text ("?" if missing).
func sexpr(n cst.Node) string {
	var b strings.Builder
	sexprTo(&b, n)
	return b.String()
}

func sexprTo(b *strings.Builder, n cst.Node) {
	switch n.Kind() {
	case cst.KindSimpleName, cst.KindLiteralExpr:
		if n.NumChildren() == 1 {
			if t := n.Element(0).Token; !t.IsZero() && !t.Missing() {
				b.WriteString(t.Text())
				return
			}
		}
	}
	b.WriteString("(")
	b.WriteString(n.Kind().String())
	for e := range n.Elements() {
		b.WriteString(" ")
		if e.IsToken() {
			if e.Token.Missing() {
				b.WriteString("?")
			} else {
				b.WriteString(e.Token.Text())
			}
			continue
		}
		sexprTo(b, e.Node)
	}
	b.WriteString(")")
}

func syntaxProblems(ps []Problem) []Problem {
	var out []Problem
	for _, p := range ps {
		if p.Code != diag.SyntaxStandard {
			out = append(out, p)
		}
	}
	return out
}

// checkSnippet checks the invariants every run must keep: lossless,
// problems inside the input, budget not exhausted.
func checkSnippet(t *testing.T, src string, r exResult) {
	t.Helper()
	if got := r.root.Text(); got != src {
		t.Fatalf("%q: round trip gave %q", src, got)
	}
	for _, p := range r.probs {
		if p.Start < 0 || p.End < p.Start || p.End > len(src) {
			t.Fatalf("%q: problem %+v outside the input", src, p)
		}
	}
	if r.dead {
		t.Fatalf("%q: budget exhausted", src)
	}
}

type exCase struct {
	src  string
	want string
}

func runCases(t *testing.T, fn func(*Parser) bool, cases []exCase) {
	t.Helper()
	for _, c := range cases {
		r := exRun(c.src, token.Std2019, fn)
		checkSnippet(t, c.src, r)
		if !r.ok || r.rest {
			t.Errorf("%q: ok=%v rest=%v\n%s", c.src, r.ok, r.rest,
				sexpr(r.root))
			continue
		}
		if ps := syntaxProblems(r.probs); len(ps) > 0 {
			t.Errorf("%q: unexpected problems %+v", c.src, ps)
		}
		if got := sexpr(r.node); got != c.want {
			t.Errorf("%q:\n got %s\nwant %s", c.src, got, c.want)
		}
	}
}

func TestExprPrecedence(t *testing.T) {
	runCases(t, fnExpr, []exCase{
		{"a + b * c", "(BinaryExpr a + (BinaryExpr b * c))"},
		{"a * b + c", "(BinaryExpr (BinaryExpr a * b) + c)"},
		{"a - b - c", "(BinaryExpr (BinaryExpr a - b) - c)"},
		{"-a * b", "(UnaryExpr - (BinaryExpr a * b))"},
		{"-a + b", "(BinaryExpr (UnaryExpr - a) + b)"},
		{"-a ** 2", "(UnaryExpr - (BinaryExpr a ** 2))"},
		{"abs a ** 2", "(BinaryExpr (UnaryExpr abs a) ** 2)"},
		{"not a and b", "(BinaryExpr (UnaryExpr not a) and b)"},
		{"a and b and c", "(BinaryExpr (BinaryExpr a and b) and c)"},
		{"a = b or c /= d",
			"(BinaryExpr (BinaryExpr a = b) or (BinaryExpr c /= d))"},
		{"a sll 2 = b", "(BinaryExpr (BinaryExpr a sll 2) = b)"},
		{"a & b sll c + d",
			"(BinaryExpr (BinaryExpr a & b) sll (BinaryExpr c + d))"},
		{"a mod b rem c", "(BinaryExpr (BinaryExpr a mod b) rem c)"},
		{"a ?= b", "(BinaryExpr a ?= b)"},
		{"a ?/= b and c ?< d",
			"(BinaryExpr (BinaryExpr a ?/= b) and (BinaryExpr c ?< d))"},
		{"?? s", "(UnaryExpr ?? s)"},
		{"and v", "(UnaryExpr and v)"},
		{"xor v = '1'", "(BinaryExpr (UnaryExpr xor v) = '1')"},
		{"a <= b", "(BinaryExpr a <= b)"},
		{"(a or b) and c",
			"(BinaryExpr (ParenExpr ( (BinaryExpr a or b) )) and c)"},
		{"10 ns", "(PhysicalLiteral 10 ns)"},
		{"2.5 us + t", "(BinaryExpr (PhysicalLiteral 2.5 us) + t)"},
		{`x"FF" & "01" & 'c' & null`,
			`(BinaryExpr (BinaryExpr (BinaryExpr x"FF" & "01") & 'c') & null)`},
		{"16#FF#", "16#FF#"},
		{"unaffected", "(UnaffectedExpr unaffected)"},
	})
}

func TestExprNames(t *testing.T) {
	runCases(t, fnExpr, []exCase{
		{"a.b.c", "(SelectedName (SelectedName a . b) . c)"},
		{"p.all", "(SelectedName p . all)"},
		{"pkg.\"+\"", "(SelectedName pkg . \"+\")"},
		{"f(x)", "(CallOrIndexName f (AssociationList ( " +
			"(AssociationElement x) )))"},
		{"f(a => 1, b)", "(CallOrIndexName f (AssociationList ( " +
			"(AssociationElement a => 1) , (AssociationElement b) )))"},
		{"a(3 downto 0)", "(SliceName a ( (RangeExpr 3 downto 0) ))"},
		{"a(t range 0 to 3)", "(SliceName a ( (SubtypeIndication t " +
			"(RangeConstraint range (RangeExpr 0 to 3))) ))"},
		{"a(i)(j)", "(CallOrIndexName (CallOrIndexName a " +
			"(AssociationList ( (AssociationElement i) ))) " +
			"(AssociationList ( (AssociationElement j) )))"},
		{"a(i).f", "(SelectedName (CallOrIndexName a (AssociationList " +
			"( (AssociationElement i) ))) . f)"},
		{"s'event", "(AttributeName s ' event)"},
		{"a'range", "(AttributeName a ' range)"},
		{"a'reverse_range", "(AttributeName a ' reverse_range)"},
		{"x'subtype", "(AttributeName x ' subtype)"},
		{"r'record", "(AttributeName r ' record)"},
		{"t'image(x)", "(AttributeName t ' image ( x ))"},
		{"m'converse", "(AttributeName m ' converse)"},
		{"a'high(2)", "(AttributeName a ' high ( 2 ))"},
		{"f[integer return bit]'path_name",
			"(AttributeName f (Signature [ integer return bit ]) ' " +
				"path_name)"},
		{"f[]'instance_name",
			"(AttributeName f (Signature [ ]) ' instance_name)"},
		{"t'('0')", "(QualifiedExpr t ' (ParenExpr ( '0' )))"},
		{"t'(others => '0')", "(QualifiedExpr t ' (Aggregate ( " +
			"(ElementAssociation (Choices (OthersChoice others)) => " +
			"'0') )))"},
		{`"+"(a, b)`, `(CallOrIndexName (OperatorSymbol "+") ` +
			`(AssociationList ( (AssociationElement a) , ` +
			`(AssociationElement b) )))`},
		{"s'delayed(1 ns)'stable", "(AttributeName (AttributeName s ' " +
			"delayed ( (PhysicalLiteral 1 ns) )) ' stable)"},
		{"f generic map (n => 1) (x)", "(CallOrIndexName f " +
			"(CallGenericMap generic map (AssociationList ( " +
			"(AssociationElement n => 1) ))) (AssociationList ( " +
			"(AssociationElement x) )))"},
		{"p parameter map (x => 1)", "(CallOrIndexName p " +
			"(ParameterMapAspect parameter map (AssociationList ( " +
			"(AssociationElement x => 1) ))))"},
	})
}

func TestExprAggregates(t *testing.T) {
	runCases(t, fnExpr, []exCase{
		{"(a)", "(ParenExpr ( a ))"},
		{"(a, b)", "(Aggregate ( (ElementAssociation a) , " +
			"(ElementAssociation b) ))"},
		{"(others => '0')", "(Aggregate ( (ElementAssociation " +
			"(Choices (OthersChoice others)) => '0') ))"},
		{"(1 => x)", "(Aggregate ( (ElementAssociation (Choices 1) " +
			"=> x) ))"},
		{"(1 | 3 => a, 0 to 2 => b)", "(Aggregate ( (ElementAssociation " +
			"(Choices 1 | 3) => a) , (ElementAssociation (Choices " +
			"(RangeExpr 0 to 2)) => b) ))"},
		{"(f => 1, others => 0)", "(Aggregate ( (ElementAssociation " +
			"(Choices f) => 1) , (ElementAssociation (Choices " +
			"(OthersChoice others)) => 0) ))"},
		{"((a, b), (c, d))", "(Aggregate ( (ElementAssociation " +
			"(Aggregate ( (ElementAssociation a) , (ElementAssociation " +
			"b) ))) , (ElementAssociation (Aggregate ( " +
			"(ElementAssociation c) , (ElementAssociation d) ))) ))"},
		{"(a when c else b)", "(ParenExpr ( (ConditionalExpr " +
			"(WhenBranch a when c) else (WhenBranch b)) ))"},
		{"new t", "(Allocator new (SubtypeIndication t))"},
		{"new integer_vector(0 to 3)", "(Allocator new " +
			"(SubtypeIndication integer_vector (ArrayConstraint ( " +
			"(RangeExpr 0 to 3) ))))"},
		{"new t'(1, 2)", "(Allocator new (QualifiedExpr t ' " +
			"(Aggregate ( (ElementAssociation 1) , (ElementAssociation " +
			"2) ))))"},
		{"new work.p.t", "(Allocator new (SubtypeIndication " +
			"(SelectedName (SelectedName work . p) . t)))"},
	})
}

func TestExprExternalNames(t *testing.T) {
	runCases(t, fnExpr, []exCase{
		{"<<signal .tb.dut.s : bit>>", "(ExternalName << signal " +
			"(AbsolutePathname . (PathnameElement tb .) " +
			"(PathnameElement dut .) s) : (SubtypeIndication bit) >>)"},
		{"<<constant ^.^.u.c : integer>>", "(ExternalName << constant " +
			"(RelativePathname ^ . ^ . (PathnameElement u .) c) : " +
			"(SubtypeIndication integer) >>)"},
		{"<<variable @lib.pkg.v : t>>", "(ExternalName << variable " +
			"(PackagePathname @ lib . pkg . v) : (SubtypeIndication t) " +
			">>)"},
		{"<<signal g(3).x : std_logic_vector(7 downto 0)>>(1)",
			"(CallOrIndexName (ExternalName << signal " +
				"(RelativePathname (PathnameElement g ( 3 ) .) x) : " +
				"(SubtypeIndication std_logic_vector (ArrayConstraint ( " +
				"(RangeExpr 7 downto 0) ))) >>) (AssociationList ( " +
				"(AssociationElement 1) )))"},
	})
}

func TestExprChoicesAndAssociations(t *testing.T) {
	runCases(t, fnChoices, []exCase{
		{"1 | 2 | 5 to 7", "(Choices 1 | 2 | (RangeExpr 5 to 7))"},
		{"others", "(Choices (OthersChoice others))"},
		{"t range 0 to 1", "(Choices (SubtypeIndication t " +
			"(RangeConstraint range (RangeExpr 0 to 1))))"},
		{`"00"`, `(Choices "00")`},
	})
	runCases(t, fnAssoc, []exCase{
		{"(a => open, b => inertial x and y, <>)", "(AssociationList ( " +
			"(AssociationElement a => (OpenExpr open)) , " +
			"(AssociationElement b => (InertialActual inertial " +
			"(BinaryExpr x and y))) , (AssociationElement (BoxExpr <>)) )" +
			")"},
		{"(t => resolved std_logic)", "(AssociationList ( " +
			"(AssociationElement t => (SubtypeIndication " +
			"(ResolutionFunction resolved) std_logic)) ))"},
		{"(to_int(f) => x)", "(AssociationList ( (AssociationElement " +
			"(CallOrIndexName to_int (AssociationList ( " +
			"(AssociationElement f) ))) => x) ))"},
		{"(default)", "(AssociationList ( (AssociationElement " +
			"(BoxExpr default)) ))"},
	})
	runCases(t, fnName, []exCase{
		{"'a'", "(CharacterName 'a')"},
		{`"and"`, `(OperatorSymbol "and")`},
		{"a(1 to 2)", "(SliceName a ( (RangeExpr 1 to 2) ))"},
		{"<<signal .a.b : bit>>", "(ExternalName << signal " +
			"(AbsolutePathname . (PathnameElement a .) b) : " +
			"(SubtypeIndication bit) >>)"},
	})
	runCases(t, fnTarget, []exCase{
		{"(a, b)", "(Aggregate ( (ElementAssociation a) , " +
			"(ElementAssociation b) ))"},
		{"s(1)", "(CallOrIndexName s (AssociationList ( " +
			"(AssociationElement 1) )))"},
	})
	runCases(t, fnCond, []exCase{
		{"a when c else b when d else e", "(ConditionalExpr (WhenBranch " +
			"a when c) else (WhenBranch b when d) else (WhenBranch e))"},
		{"a when c", "(ConditionalExpr (WhenBranch a when c))"},
		{"a + b", "(BinaryExpr a + b)"},
	})
}

// TestExprStatementBoundaries: the expression parser stops at the
// tokens statement builders own.
func TestExprStatementBoundaries(t *testing.T) {
	cases := []struct {
		src  string
		fn   func(*Parser) bool
		next token.Kind
	}{
		{"a when c else b", fnExpr, token.KwWhen},
		{"a after 1 ns", fnExpr, token.KwAfter},
		{"x to y", fnExpr, token.KwTo},
		{"s <= a", fnName, token.Le},
		{"v := a", fnName, token.VarAssign},
		{"u generic map (x)", fnName, token.KwGeneric},
		{"t'(x)", fnName, token.Tick},
		{"a => b", fnExpr, token.Arrow},
		{"a | b", fnExpr, token.Bar},
		{"a then", fnExpr, token.KwThen},
		{"f [bit]", fnName, token.LBracket},
	}
	for _, c := range cases {
		var next token.Kind
		r := exRun(c.src, token.Std2019, func(p *Parser) bool {
			ok := c.fn(p)
			next = p.nth(0)
			return ok
		})
		checkSnippet(t, c.src, r)
		if !r.ok || next != c.next || len(syntaxProblems(r.probs)) > 0 {
			t.Errorf("%q: ok=%v next=%v problems=%+v", c.src, r.ok, next,
				r.probs)
		}
	}
}

// TestExprErrors: invalid expressions report an error at the right
// place, keep every byte and still build the tree.
func TestExprErrors(t *testing.T) {
	cases := []struct {
		src  string
		at   int // offset of the first problem
		want string
	}{
		{"a and b or c", 8, "one operator"},
		{"a nand b nand c", 9, "not associative"},
		{"a sll b srl c", 8, "shift operators"},
		{"a ** b ** c", 7, "** operators"},
		{"a * -b", 4, "sign cannot follow"},
		{"not not a", 4, "cannot follow not"},
		{"a +", 3, "expected an expression"},
		{"f(a b)", 3, "expected ',' or ')'"},
		{"f(a", 3, "expected ')'"},
		{"(a, b", 5, "expected ')'"},
		{"(others)", 7, "expected '=>'"},
		{"<<signal x bit>>", 10, "expected ':'"},
		{"<<s.x : bit>>", 2, "expected constant, signal or variable"},
		{"<<signal .a..b : bit>>", 12, "expected an identifier"},
		{"<<signal @lib.x : bit>>", 15, "expected '.'"},
		{"<<signal .a.b>>", 13, "expected ':'"},
		{"f()", 2, "expected an expression"},
		{"new", 3, "expected a subtype indication"},
	}
	for _, c := range cases {
		r := exRun(c.src, token.Std2019, fnExpr)
		checkSnippet(t, c.src, r)
		ps := syntaxProblems(r.probs)
		if len(ps) == 0 {
			t.Errorf("%q: no problem\n%s", c.src, sexpr(r.root))
			continue
		}
		if ps[0].Start != c.at || !strings.Contains(ps[0].Args[0], c.want) {
			t.Errorf("%q: first problem %d %q, want %d %q", c.src,
				ps[0].Start, ps[0].Args, c.at, c.want)
		}
	}
}

// TestExprGates: each gated construct reports syntax/standard exactly
// under the standards before the rule's first one, and the tree is the
// same under every standard (#7 invariant).
func TestExprGates(t *testing.T) {
	cases := []struct{ src, rule string }{
		{"a ?= b", "match_ops"},
		{"a ?>= b", "match_ops"},
		{"?? s", "cond_conv"},
		{"nor v", "unary_logic"},
		{"<<signal .a.b : bit>>", "ext_name"},
		{"x'subtype", "subtype_attr"},
		{"r'record", "record_attr"},
		{"f(p => inertial a)", "inertial_actual"},
		{"(a when c else b)", "cond_expr"},
		{"f generic map (n => 1) (x)", "subp_generic_map_call"},
		{"p parameter map (x => 1)", "param_map"},
	}
	for _, c := range cases {
		rule, ok := RuleByID(c.rule)
		if !ok {
			t.Fatalf("no rule %s", c.rule)
		}
		var id cst.ID
		for i, std := range token.Standards {
			r := exRun(c.src, std, fnExpr)
			checkSnippet(t, c.src, r)
			if i == 0 {
				id = r.root.ID()
			} else if r.root.ID() != id {
				t.Errorf("%q: tree differs under %v", c.src, std)
			}
			n := 0
			for _, p := range r.probs {
				if p.Code == diag.SyntaxStandard &&
					p.Args[0] == rule.Construct {
					n++
				} else {
					t.Errorf("%q under %v: unexpected %+v", c.src, std, p)
				}
			}
			if want := btoi(std < rule.First); n != want {
				t.Errorf("%q under %v: %d gate reports, want %d", c.src,
					std, n, want)
			}
		}
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ----------------------------------------------- precedence property

// exAST is a random expression: an operator with operands, or a leaf.
type exAST struct {
	op    string
	level int // binding strength, see exLevels
	kids  []*exAST
}

// Levels (higher binds tighter): 0 ??, 1 logical, 2 relational, 3
// shift, 4 adding and sign, 5 multiplying, 6 **, 7 unary, 8 primary.
var exBinOps = [][]string{
	1: {"and", "or", "xor", "xnor", "nand", "nor"},
	2: {"=", "/=", "<", "<=", ">", ">=", "?=", "?/=", "?<", "?<=", "?>",
		"?>="},
	3: {"sll", "srl", "sla", "sra", "rol", "ror"},
	4: {"+", "-", "&"},
	5: {"*", "/", "mod", "rem"},
	6: {"**"},
}

var exUnOps = []string{"abs", "not", "and", "or", "nand", "nor", "xor",
	"xnor"}

func genAST(r *env.SplitMix64, depth int) *exAST {
	if depth == 0 || r.Uint64()%4 == 0 {
		leaves := []string{"a", "b", "c", "d", "1", "2.0", "'1'", `"01"`}
		return &exAST{op: leaves[r.Uint64()%uint64(len(leaves))], level: 8}
	}
	switch k := r.Uint64() % 10; {
	case k < 6:
		lv := 1 + int(r.Uint64()%6)
		ops := exBinOps[lv]
		return &exAST{op: ops[r.Uint64()%uint64(len(ops))], level: lv,
			kids: []*exAST{genAST(r, depth-1), genAST(r, depth-1)}}
	case k < 8:
		return &exAST{op: exUnOps[r.Uint64()%uint64(len(exUnOps))],
			level: 7, kids: []*exAST{genAST(r, depth-1)}}
	case k < 9:
		op := "-"
		if r.Uint64()%2 == 0 {
			op = "+"
		}
		return &exAST{op: op, level: 4, kids: []*exAST{genAST(r, depth-1)}}
	default:
		return &exAST{op: "??", level: 0,
			kids: []*exAST{genAST(r, depth-1)}}
	}
}

// render prints e with the fewest parentheses the VHDL grammar needs.
func (e *exAST) render() string {
	sub := func(k *exAST, min int) string {
		if k.level < min {
			return "(" + k.render() + ")"
		}
		return k.render()
	}
	switch {
	case len(e.kids) == 0:
		return e.op
	case len(e.kids) == 1:
		min := 8
		switch e.level {
		case 4:
			min = 5
		case 7:
			min = 8
		}
		return e.op + " " + sub(e.kids[0], min)
	}
	l, r := e.kids[0], e.kids[1]
	var ls, rs string
	switch e.level {
	case 1:
		assoc := e.op != "nand" && e.op != "nor"
		if assoc && l.level == 1 && l.op == e.op && len(l.kids) == 2 {
			ls = l.render()
		} else {
			ls = sub(l, 2)
		}
		rs = sub(r, 2)
	case 2, 3:
		ls, rs = sub(l, e.level+1), sub(r, e.level+1)
	case 4, 5:
		ls, rs = sub(l, e.level), sub(r, e.level+1)
	case 6:
		ls, rs = sub(l, 7), sub(r, 7)
	}
	return ls + " " + e.op + " " + rs
}

func (e *exAST) canon() string {
	if len(e.kids) == 0 {
		return e.op
	}
	s := "(" + e.op
	for _, k := range e.kids {
		s += " " + k.canon()
	}
	return s + ")"
}

// canonCST is canon for the parsed tree, parentheses dropped.
func canonCST(n cst.Node) string {
	var kids []cst.Node
	var op string
	for e := range n.Elements() {
		if e.IsToken() {
			if op == "" && n.Kind() != cst.KindParenExpr {
				op = e.Token.Text()
			}
			continue
		}
		kids = append(kids, e.Node)
	}
	switch n.Kind() {
	case cst.KindParenExpr:
		return canonCST(kids[0])
	case cst.KindSimpleName, cst.KindLiteralExpr:
		return n.Element(0).Token.Text()
	}
	s := "(" + strings.ToLower(op)
	for _, k := range kids {
		s += " " + canonCST(k)
	}
	return s + ")"
}

// TestExprPrecedenceProperty: for random expression trees, the text
// printed with minimal parentheses parses back to the same tree with no
// problem, under 2008 and 2019.
func TestExprPrecedenceProperty(t *testing.T) {
	r := &env.SplitMix64{State: 31}
	for i := range 3000 {
		e := genAST(r, 1+i%6)
		src := e.render()
		std := token.Std2008 + token.Standard(i%2)*11
		res := exRun(src, std, fnExpr)
		checkSnippet(t, src, res)
		if !res.ok || res.rest || len(res.probs) > 0 {
			t.Fatalf("%q: ok=%v rest=%v problems=%+v", src, res.ok,
				res.rest, res.probs)
		}
		if got, want := canonCST(res.node), e.canon(); got != want {
			t.Fatalf("%q:\n got %s\nwant %s", src, got, want)
		}
	}
}

// ---------------------------------------------- robustness properties

var exSoup = []string{"a", "b", "1", "2.5", "ns", "(", ")", ",", "=>",
	"+", "-", "*", "**", "and", "nand", "=", "?=", "??", "'", "range",
	"to", "downto", "others", ".", "<<", ">>", "signal", ":", "new",
	"abs", "not", "|", "[", "]", "return", "when", "else", "open",
	"inertial", `"s"`, "'c'", `x"F"`, "@", "^", "<>", "all", "generic",
	"map", "parameter", "subtype", "record", ";", "null", "\xff", " ",
	"\n", "-- c\n"}

// TestExprSoup: random token sequences never break the invariants with
// any entry point under any standard, and the parse is deterministic.
func TestExprSoup(t *testing.T) {
	fns := []func(*Parser) bool{fnExpr, fnCond, fnName, fnChoices,
		fnAssoc, fnTarget}
	r := &env.SplitMix64{State: 5}
	for i := range 6000 {
		var b strings.Builder
		for range r.Uint64() % 20 {
			b.WriteString(exSoup[r.Uint64()%uint64(len(exSoup))])
			if r.Uint64()%2 == 0 {
				b.WriteString(" ")
			}
		}
		src := b.String()
		std := token.Standards[i%len(token.Standards)]
		fn := fns[i%len(fns)]
		r1 := exRun(src, std, fn)
		checkSnippet(t, src, r1)
		r2 := exRun(src, std, fn)
		if r1.root.ID() != r2.root.ID() || len(r1.probs) != len(r2.probs) {
			t.Fatalf("%q: not deterministic", src)
		}
	}
}

// TestExprDeepNesting: deeply nested parentheses, unary operators and
// calls give one nesting diagnostic and keep every byte.
func TestExprDeepNesting(t *testing.T) {
	for _, src := range []string{
		strings.Repeat("(", 5000) + "a" + strings.Repeat(")", 5000),
		strings.Repeat("f(", 5000) + "a" + strings.Repeat(")", 5000),
		strings.Repeat("abs ", 5000) + "a",
		strings.Repeat("- ", 5000) + "a",
		strings.Repeat("a + (", 5000) + "a" + strings.Repeat(")", 5000),
	} {
		r := exRun(src, token.Std2008, fnExpr)
		if r.root.Text() != src || r.dead {
			t.Fatalf("%.20q: round trip failed", src)
		}
		deep, other := 0, 0
		for _, p := range r.probs {
			if strings.Contains(p.Args[0], "nested too deeply") {
				deep++
			} else if !strings.Contains(p.Args[0], "cannot follow") {
				other++
			}
		}
		if deep != 1 || other != 0 {
			t.Errorf("%.20q: %d nesting and %d other diagnostics, want 1 "+
				"and 0", src, deep, other)
		}
	}
}

var nl = []byte("\n")

// exOracleGlob selects the extra sources of TestExprCorpusInitializers.
var exOracleGlob = "../../testdata/libs/osvvm/*.vhd"

// TestExprCorpusInitializers is an oracle on real code: every `:=`
// value in the corpus designs parses as one expression (conditional
// expressions included) with no syntax error and ends at `;` or `)`.
func TestExprCorpusInitializers(t *testing.T) {
	files := corpusFiles(t)
	extra, _ := filepath.Glob(exOracleGlob)
	n := 0
	for _, f := range append(files, extra...) {
		if strings.HasSuffix(f, "_template.vhd") {
			continue // ${...} placeholders, not VHDL
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		toks, _ := lexer.All(src, lexer.Options{Standard: token.Std2019},
			lexer.VHDL)
		for i, tk := range toks {
			if tk.Kind != token.VarAssign || i+1 >= len(toks) {
				continue
			}
			at := toks[i+1].Start
			// The window grows only if the value runs past it; no
			// finish, so the rest of the window is not built.
			var ok bool
			var next token.Kind
			var end int
			var probs []Problem
			for _, w := range []int{1 << 12, 1 << 20} {
				win := src[at:min(len(src), at+w)]
				opts := Options{Standard: token.Std2019}
				p := newParser(win, opts, lexerOptions(opts))
				p.open(cst.KindDesignFile)
				ok = p.parseConditionalExpression()
				next, end, probs = p.nth(0), p.lastEnd, p.probs
				if next != token.EOF || len(win) == len(src)-at {
					break
				}
			}
			n++
			where := func() string {
				return f + ":" + strconv.Itoa(1+bytes.Count(src[:at], nl))
			}
			if !ok || (next != token.Semicolon && next != token.RParen) {
				t.Errorf("%s: stopped at %v: %.60q", where(), next,
					string(src[at:]))
				continue
			}
			for _, p := range probs {
				if p.Start <= end {
					t.Errorf("%s: %s %q", where(), p.Code, p.Args)
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("no initializers found")
	}
	t.Logf("%d initializers", n)
}

// BenchmarkExpr measures a long expression-heavy line.
func BenchmarkExpr(b *testing.B) {
	src := strings.Repeat("a(i) + b.c * (others => '0') ** 2 and "+
		"f(x => 1, y => t'image(v)) = <<signal .t.s : bit>> and ", 200) +
		"z"
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		exRun(src, token.Std2019, fnExpr)
	}
}
