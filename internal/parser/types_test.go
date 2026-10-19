package parser

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/token"
)

var typeShapeCases = []shapeCase{
	{src: "type t is (a, b, 'c'); type u;",
		want: "TypeDecl(EnumerationTypeDef) IncompleteTypeDecl"},
	{src: "type i is range 0 to 7; type r is range -1.0 to 1.0;",
		want: "TypeDecl(RangeTypeDef(RangeConstraint(RangeExpr(E E)))) " +
			"TypeDecl(RangeTypeDef(RangeConstraint(RangeExpr(E E))))"},
	{src: "type d is range 0 to 1000 units fs; ps = 1000 fs; ns = 1000 " +
		"ps; end units d;",
		want: "TypeDecl(PhysicalTypeDef(RangeConstraint(RangeExpr(E E)) " +
			"PrimaryUnitDecl SecondaryUnitDecl(E) SecondaryUnitDecl(E) " +
			"EndClause))"},
	{src: "type v is array (natural range <>, work.p.idx range <>) of " +
		"bit; type w is array (natural range <>) of std_logic_vector;",
		want: "TypeDecl(UnboundedArrayDef(IndexSubtypeDef(E) " +
			"IndexSubtypeDef(E) SubtypeIndication(E))) " +
			"TypeDecl(UnboundedArrayDef(IndexSubtypeDef(E) " +
			"SubtypeIndication(E)))"},
	{src: "type m is array (0 to 3, 1 downto 0) of std_logic_vector(7 " +
		"downto 0); type n is array (byte_range) of bit;",
		want: "TypeDecl(ConstrainedArrayDef(ArrayConstraint(RangeExpr(E " +
			"E) RangeExpr(E E)) SubtypeIndication(E ArrayConstraint(" +
			"RangeExpr(E E))))) TypeDecl(ConstrainedArrayDef(" +
			"ArrayConstraint(E) SubtypeIndication(E)))"},
	{src: "type rec is record a, b : bit; c : integer range 0 to 3; end " +
		"record rec; type e is record end record;",
		want: "TypeDecl(RecordTypeDef(ElementDecl(IdentifierList " +
			"SubtypeIndication(E)) ElementDecl(IdentifierList " +
			"SubtypeIndication(E RangeConstraint(RangeExpr(E E)))) " +
			"EndClause)) TypeDecl(RecordTypeDef(EndClause))"},
	{src: "type p is access integer; type f is file of string;",
		want: "TypeDecl(AccessTypeDef(SubtypeIndication(E))) " +
			"TypeDecl(FileTypeDef(E))"},
	{src: "type pt is protected procedure inc; impure function get " +
		"return integer; alias g is get [return integer]; end " +
		"protected pt;",
		want: "TypeDecl(ProtectedTypeDef(DeclarativePart(SubprogramDecl(" +
			"ProcedureSpec) SubprogramDecl(FunctionSpec(ReturnSpec(E))) " +
			"AliasDecl(E Signature(E))) EndClause))"},
	{src: "type pt is protected body variable n : integer := 0; " +
		"procedure inc is begin n := n + 1; end procedure; end " +
		"protected body pt;",
		want: "TypeDecl(ProtectedTypeBody(DeclarativePart(VariableDecl(" +
			"IdentifierList SubtypeIndication(E) E) SubprogramBody(" +
			"ProcedureSpec DeclarativePart SequenceOfStatements " +
			"EndClause)) EndClause))"},
	{src: "subtype byte is std_logic_vector(7 downto 0); subtype rl is " +
		"resolved std_ulogic;",
		want: "SubtypeDecl(SubtypeIndication(E ArrayConstraint(RangeExpr(" +
			"E E)))) SubtypeDecl(SubtypeIndication(ResolutionFunction(" +
			"E) E))"},
	{src: "subtype rv is (resolved) std_ulogic_vector; subtype rr is (a " +
		"resolved, b (f)) rec; subtype r2 is ((work.p.f)) arr2;",
		want: "SubtypeDecl(SubtypeIndication(ElementResolution(" +
			"ResolutionFunction(E)) E)) SubtypeDecl(SubtypeIndication(" +
			"RecordResolution(RecordElementResolution(" +
			"ResolutionFunction(E)) RecordElementResolution(" +
			"ElementResolution(ResolutionFunction(E)))) E)) " +
			"SubtypeDecl(SubtypeIndication(ElementResolution(" +
			"ElementResolution(ResolutionFunction(E))) E))"},
	{src: "subtype rc is rec(f(7 downto 0), g(open)); subtype a2 is " +
		"arr_t(open)(7 downto 0); subtype a3 is arr_r(0 to 3)(f(1 to " +
		"2));",
		want: "SubtypeDecl(SubtypeIndication(E RecordConstraint(" +
			"RecordElementConstraint(ArrayConstraint(RangeExpr(E E))) " +
			"RecordElementConstraint(ArrayConstraint(OpenExpr))))) " +
			"SubtypeDecl(SubtypeIndication(E ArrayConstraint(OpenExpr " +
			"ArrayConstraint(RangeExpr(E E))))) SubtypeDecl(" +
			"SubtypeIndication(E ArrayConstraint(RangeExpr(E E) " +
			"RecordConstraint(RecordElementConstraint(ArrayConstraint(" +
			"RangeExpr(E E)))))))"},
	{src: "subtype s2 is s'subtype; subtype idx is integer range " +
		"v'range; subtype b2 is t'base range 0 to 1;",
		want: "SubtypeDecl(SubtypeIndication(E)) SubtypeDecl(" +
			"SubtypeIndication(E RangeConstraint(E))) SubtypeDecl(" +
			"SubtypeIndication(E RangeConstraint(RangeExpr(E E))))"},
	{src: "type m2 is array (integer range 0 to 3) of bit;",
		want: "TypeDecl(ConstrainedArrayDef(ArrayConstraint(" +
			"SubtypeIndication(E RangeConstraint(RangeExpr(E E)))) " +
			"SubtypeIndication(E)))", needs: "expr"},
}

func TestTypeShapes(t *testing.T) { runShapeCases(t, typeShapeCases) }

// TestTypeMarkAttributes: 'subtype, 'base and 'element type marks are
// AttributeName nodes holding the prefix name.
func TestTypeMarkAttributes(t *testing.T) {
	src := "subtype a is s'subtype; subtype b is work.p.t'base; " +
		"subtype c is v'element; signal d : a(i)'subtype; " +
		"signal e : b.c(1)(2)'element; signal f : v'element(0 to 1); " +
		"signal g : m'element'element;"
	root, probs := parseRegion(t, src, RegionArchitecture, token.Std2019)
	if ps := realProblems(probs); len(ps) > 0 {
		t.Fatal(problemText(src, ps))
	}
	var got []string
	for n := range root.Descendants() {
		if n.Kind() == cst.KindAttributeName {
			got = append(got, n.Text())
		}
	}
	want := []string{"s'subtype", "work.p.t'base", "v'element",
		"a(i)'subtype", "b.c(1)(2)'element", "v'element", "m'element'element", "m'element"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("attribute type marks %q, want %q", got, want)
	}
}

// TestRecordConstraintLookahead: a parenthesized constraint is a record
// constraint exactly when it starts `( element ( ... )` followed by `,`,
// `)` or `(`.
func TestRecordConstraintLookahead(t *testing.T) {
	cases := map[string]bool{
		"(f(1 to 2))":                         true,
		"(f(open), g(0 to 1))":                true,
		"(f(1)(2))":                           true,
		"(f(3) downto 0)":                     false,
		"(a to b)":                            false,
		"(t'range)":                           false,
		"(g(x)'range)":                        false,
		"(f(" + strings.Repeat("(", 80) + "x": false,
	}
	for src, want := range cases {
		opts := Options{Standard: token.Std2019}
		p := newParser([]byte(src), opts, lexerOptions(opts))
		if got := p.atRecordConstraint(); got != want {
			t.Errorf("%q: %v, want %v", src, got, want)
		}
	}
}
