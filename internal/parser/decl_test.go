package parser

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/condanal"
	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// Tests of the decl and types builders. Declarative parts are parsed
// directly (parseRegion) so every region can be checked without the
// statement builders. Shapes elide expressions and names (rendered E),
// which the expr builder owns, so the cases pin the declaration
// structure only.

// parseRegion parses src as the declarative part of region r and
// returns the root (a DesignFile holding the DeclarativePart and EOF)
// and the problems. It checks the lossless round trip and determinism.
func parseRegion(t testing.TB, src string, r DeclRegion,
	std token.Standard,
) (cst.Node, []Problem) {
	t.Helper()
	run := func() (*cst.Green, []Problem) {
		opts := Options{Standard: std}
		p := newParser([]byte(src), opts, lexerOptions(opts))
		p.open(cst.KindDesignFile)
		p.declarativePart(r, token.Set{})
		p.finish()
		p.close()
		if p.dead {
			t.Fatalf("%q: the parse ran out of budget", src)
		}
		probs := p.probs
		slices.SortStableFunc(probs, func(a, b Problem) int {
			return a.Start - b.Start
		})
		return p.b.Finish(), probs
	}
	g, probs := run()
	if got := g.String(); got != src {
		t.Fatalf("%q: round trip gave %q", src, got)
	}
	for _, pr := range probs {
		if pr.Start < 0 || pr.End < pr.Start || pr.End > len(src) {
			t.Fatalf("%q: problem %v outside the input", src, pr)
		}
	}
	g2, probs2 := run()
	if !cst.Equal(g, g2) || !slices.EqualFunc(probs, probs2, sameProblem) {
		t.Fatalf("%q: parse is not deterministic", src)
	}
	return cst.NewRoot(g), probs
}

// stubProblem reports problems of builders that are still stubs.
func stubProblem(p Problem) bool {
	return p.Code == diag.SyntaxError && len(p.Args) > 0 &&
		strings.Contains(p.Args[0], "not parsed yet")
}

// realProblems drops stub problems.
func realProblems(ps []Problem) []Problem {
	var out []Problem
	for _, p := range ps {
		if !stubProblem(p) {
			out = append(out, p)
		}
	}
	return out
}

func problemText(src string, ps []Problem) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(string(p.Code) + "@" + strconv.Itoa(p.Start) + " " +
			strings.Join(p.Args, "|") + " near " +
			strconv.Quote(src[p.Start:min(len(src), p.Start+12)]) + "\n")
	}
	return b.String()
}

// elided are the node kinds a shape prints as E: expressions and names
// (owned by the expr builder) and error nodes (stubs skip into them),
// except the expression kinds the types builder makes itself.
var elided = func() map[cst.Kind]bool {
	m := map[cst.Kind]bool{cst.KindErrorNode: true}
	for _, k := range cst.GroupKinds("Expr") {
		m[k] = true
	}
	for _, k := range []cst.Kind{cst.KindSubtypeIndication,
		cst.KindRangeExpr, cst.KindOpenExpr} {
		delete(m, k)
	}
	return m
}()

// shape renders the node structure under n: Kind(children...), with
// elided kinds as E and statement lists without their content.
func shape(n cst.Node) string {
	var b strings.Builder
	var walk func(n cst.Node)
	walk = func(n cst.Node) {
		k := n.Kind()
		if elided[k] {
			b.WriteString("E")
			return
		}
		b.WriteString(k.String())
		if k == cst.KindSequenceOfStatements || k == cst.KindStatementPart {
			return
		}
		first := true
		for c := range n.Nodes() {
			if first {
				b.WriteString("(")
				first = false
			} else {
				b.WriteString(" ")
			}
			walk(c)
		}
		if !first {
			b.WriteString(")")
		}
	}
	walk(n)
	return b.String()
}

// itemsShape is the shape of the items of the declarative part under
// root, space separated.
func itemsShape(root cst.Node) string {
	var parts []string
	for part := range root.Nodes() {
		for item := range part.Nodes() {
			parts = append(parts, shape(item))
		}
	}
	return strings.Join(parts, " ")
}

// exprStubbed reports whether the expression builder is still a stub;
// cases whose shape depends on it are skipped until it lands.
var exprStubbed = func() bool {
	r := Parse([]byte("package p is constant c : t := 1; end;"),
		Options{})
	return slices.ContainsFunc(r.Problems, stubProblem)
}()

// v2019Stubbed reports whether the 2019 builder is still a stub.
var v2019Stubbed = func() bool {
	opts := Options{Standard: token.Std2019}
	p := newParser([]byte("private variable v : t;"), opts,
		lexerOptions(opts))
	return !p.parsePrivateVariableDecl()
}()

type shapeCase struct {
	src    string
	region DeclRegion // 0: architecture
	want   string
	needs  string // "expr" or "v2019": skipped while that builder is a stub
}

func runShapeCases(t *testing.T, cases []shapeCase) {
	t.Helper()
	for _, c := range cases {
		if c.needs == "expr" && exprStubbed ||
			c.needs == "v2019" && v2019Stubbed {
			continue
		}
		r := c.region
		if r == 0 {
			r = RegionArchitecture
		}
		root, probs := parseRegion(t, c.src, r, token.Std2019)
		if ps := realProblems(probs); len(ps) > 0 {
			t.Errorf("%q: unexpected problems:\n%s", c.src,
				problemText(c.src, ps))
		}
		if got := itemsShape(root); got != c.want {
			t.Errorf("%q:\n got %s\nwant %s", c.src, got, c.want)
		}
		// The tree shape does not depend on the standard (gates only
		// add diagnostics).
		for _, std := range token.Standards {
			other, _ := parseRegion(t, c.src, r, std)
			if !cst.Equal(other.Green(), root.Green()) {
				t.Errorf("%q: the tree under %v differs from 2019", c.src,
					std)
			}
		}
	}
}

func TestDeclarationShapes(t *testing.T) {
	ids := "IdentifierList"
	si := "SubtypeIndication(E)"
	runShapeCases(t, []shapeCase{
		{src: "constant c1, c2 : integer := 3; constant d : integer;",
			want: "ConstantDecl(" + ids + " " + si + " E) ConstantDecl(" +
				ids + " " + si + ")"},
		{src: "signal s : std_logic_vector(3 downto 0) register := x;",
			want: "SignalDecl(" + ids + " SubtypeIndication(E " +
				"ArrayConstraint(RangeExpr(E E))) E)"},
		{src: "signal b : resolved_bit bus;",
			want: "SignalDecl(" + ids + " " + si + ")"},
		{src: "signal b : resolved bit;",
			want: "SignalDecl(" + ids + " SubtypeIndication(" +
				"ResolutionFunction(E) E))"},
		{src: "shared variable sv : pt;",
			want: "VariableDecl(" + ids + " " + si + ")"},
		{src: "variable v : integer := 0;", region: RegionProcess,
			want: "VariableDecl(" + ids + " " + si + " E)"},
		{src: "file f1 : text open read_mode is \"in.txt\"; " +
			"file f2 : text is \"x\"; file f3 : text;",
			want: "FileDecl(" + ids + " " + si + " FileOpenInformation(" +
				"E E)) FileDecl(" + ids + " " + si +
				" FileOpenInformation(E)) FileDecl(" + ids + " " + si + ")"},
		{src: "alias a is s; alias b : bit is s;",
			want: "AliasDecl(E) AliasDecl(" + si + " E)"},
		{src: "alias a : bit_vector(0 to 3) is s(3 downto 0);",
			want: "AliasDecl(SubtypeIndication(E ArrayConstraint(" +
				"RangeExpr(E E))) E)", needs: "expr"},
		{src: "alias \"and\" is ieee.std_logic_1164.\"and\" [std_ulogic, " +
			"std_ulogic return std_ulogic]; alias lt is \"<\" " +
			"[integer, integer return boolean];",
			want: "AliasDecl(E Signature(E E E)) " +
				"AliasDecl(E Signature(E E E))"},
		{src: "attribute keep : boolean; attribute keep of s, t : signal " +
			"is true; attribute a of others : label is 1; attribute c " +
			"of f [integer return bit] : function is 2; attribute d of " +
			"'x', \"+\" : literal is 3;",
			want: "AttributeDecl(E) AttributeSpec(EntitySpecification(" +
				"EntityNameList(EntityDesignator EntityDesignator)) E) " +
				"AttributeSpec(EntitySpecification(EntityNameList) E) " +
				"AttributeSpec(EntitySpecification(EntityNameList(" +
				"EntityDesignator(Signature(E E)))) E) " +
				"AttributeSpec(EntitySpecification(EntityNameList(" +
				"EntityDesignator EntityDesignator)) E)"},
		{src: "component c is generic (n : natural := 4); port (a : in " +
			"bit; y : out bit_vector(n - 1 downto 0)); end component c;" +
			" component d end component;",
			want: "ComponentDecl(GenericClause(InterfaceList(" +
				"InterfaceObjectDecl(" + ids + " " + si + " E))) " +
				"PortClause(InterfaceList(InterfaceObjectDecl(" + ids +
				" SimpleModeIndication " + si + ") InterfaceObjectDecl(" +
				ids + " SimpleModeIndication SubtypeIndication(E " +
				"ArrayConstraint(RangeExpr(E E)))))) EndClause) " +
				"ComponentDecl(EndClause)"},
		{src: "for u1 : c use entity work.e(rtl); for all : c use " +
			"configuration work.cfg; end for; for others : c use open;",
			want: "ConfigurationSpec(ComponentSpecification(" +
				"InstantiationList E) BindingIndication(EntityAspect(E))) " +
				"ConfigurationSpec(ComponentSpecification(" +
				"InstantiationList E) BindingIndication(EntityAspect(E)) " +
				"EndClause) ConfigurationSpec(ComponentSpecification(" +
				"InstantiationList E) BindingIndication(EntityAspect))"},
		{src: "disconnect s, t : bit after 1 ns; disconnect others : bit " +
			"after 0 ns;",
			want: "DisconnectionSpec(E E E E) DisconnectionSpec(E E)"},
		{src: "group pair is (signal, signal <>); group g1 : pair (a, " +
			"'b');",
			want: "GroupTemplateDecl(EntityClassEntry EntityClassEntry) " +
				"GroupDecl(E E E)"},
		{src: "use work.p.all, work.q.x;", want: "UseClause(E E)"},
		{src: "function f (a : integer; b : bit := '0') return integer;",
			want: "SubprogramDecl(FunctionSpec(ParameterList(" +
				"InterfaceList(InterfaceObjectDecl(" + ids + " " + si +
				") InterfaceObjectDecl(" + ids + " " + si + " E))) " +
				"ReturnSpec(E)))"},
		{src: "pure function \"+\" (l, r : t) return t; impure function " +
			"g return r of integer;",
			want: "SubprogramDecl(FunctionSpec(ParameterList(" +
				"InterfaceList(InterfaceObjectDecl(" + ids + " " + si +
				"))) ReturnSpec(E))) SubprogramDecl(FunctionSpec(" +
				"ReturnSpec(E)))"},
		{src: "procedure p (signal s : out bit; variable v : inout " +
			"integer; constant c : in integer; file f : text); " +
			"procedure q parameter (x : integer); procedure r;",
			want: "SubprogramDecl(ProcedureSpec(ParameterList(" +
				"InterfaceList(InterfaceObjectDecl(" + ids +
				" SimpleModeIndication " + si + ") InterfaceObjectDecl(" +
				ids + " SimpleModeIndication " + si +
				") InterfaceObjectDecl(" + ids + " SimpleModeIndication " +
				si + ") InterfaceObjectDecl(" + ids + " " + si +
				"))))) SubprogramDecl(ProcedureSpec(ParameterList(" +
				"InterfaceList(InterfaceObjectDecl(" + ids + " " + si +
				"))))) SubprogramDecl(ProcedureSpec)"},
		{src: "function g generic (type t; n : natural) parameter (x : " +
			"t) return t;",
			want: "SubprogramDecl(FunctionSpec(SubprogramHeader(" +
				"GenericClause(InterfaceList(InterfaceTypeDecl " +
				"InterfaceObjectDecl(" + ids + " " + si + ")))) " +
				"ParameterList(InterfaceList(InterfaceObjectDecl(" + ids +
				" " + si + "))) ReturnSpec(E)))"},
		{src: "impure function now2 return time is variable t : time; " +
			"begin return t; end function now2; procedure p is begin " +
			"end;",
			region: RegionPackageBody,
			want: "SubprogramBody(FunctionSpec(ReturnSpec(E)) " +
				"DeclarativePart(VariableDecl(" + ids + " " + si +
				")) SequenceOfStatements EndClause) SubprogramBody(" +
				"ProcedureSpec DeclarativePart SequenceOfStatements " +
				"EndClause)"},
		{src: "procedure sw is new swap; function f2 is new f [integer " +
			"return integer];",
			want: "SubprogramInstantiation(E) SubprogramInstantiation(E " +
				"Signature(E E))"},
		{src: "function inc_int is new inc generic map (t => integer);",
			want: "SubprogramInstantiation(E GenericMapAspect(" +
				"AssociationList(AssociationElement(E E))))",
			needs: "expr"},
		{src: "type t is protected private variable c1 : t_counter; " +
			"alias inc1 is c1.increment []; end protected;",
			region: RegionPackage,
			want: "TypeDecl(ProtectedTypeDef(DeclarativePart(" +
				"PrivateVariableDecl(VariableDecl(" + ids + " " + si +
				")) AliasDecl(E Signature)) EndClause))", needs: "v2019"},
		{src: "view v of rec is a : in; b : out; end view v;",
			region: RegionPackage,
			want: "ModeViewDecl(" + si + " ModeViewElementDef(" + ids +
				" ElementModeIndication) ModeViewElementDef(" + ids +
				" ElementModeIndication) EndClause)", needs: "v2019"},
		{src: "type pt is protected generic (n : natural); procedure p; " +
			"end protected; type q is new pt generic map (n => 1);",
			region: RegionPackage,
			want: "TypeDecl(ProtectedTypeDef(ProtectedTypeHeader(" +
				"GenericClause(InterfaceList(InterfaceObjectDecl(" + ids +
				" " + si + ")))) DeclarativePart(SubprogramDecl(" +
				"ProcedureSpec)) EndClause)) TypeDecl(" +
				"ProtectedTypeInstantiationDef(E GenericMapAspect(" +
				"AssociationList(AssociationElement(E E)))))",
			needs: "v2019"},
		{src: "package q is constant k : integer := 1; end package q; " +
			"package body q is end package body; package q2 is new " +
			"work.gp;",
			want: "PackageDecl(DeclarativePart(ConstantDecl(" + ids + " " +
				si + " E)) EndClause) PackageBody(DeclarativePart " +
				"EndClause) PackageInstantiation(E)"},
	})
}

func TestInterfaceListShapes(t *testing.T) {
	ids := "IdentifierList"
	si := "SubtypeIndication(E)"
	obj := "InterfaceObjectDecl(" + ids + " SimpleModeIndication " + si +
		")"
	runShapeCases(t, []shapeCase{
		{src: "component c port (signal a, b : in std_logic := '0'; c : " +
			"out bit; d : inout bit; e : buffer bit; f : linkage bit; " +
			"g : bit bus); end component;",
			want: "ComponentDecl(PortClause(InterfaceList(" +
				"InterfaceObjectDecl(" + ids + " SimpleModeIndication " +
				si + " E) " + strings.Repeat(obj+" ", 4) +
				"InterfaceObjectDecl(" + ids + " " + si +
				"))) EndClause)"},
		{src: "component c port (a : in bit;); end component;",
			want: "ComponentDecl(PortClause(InterfaceList(" + obj +
				")) EndClause)"},
		{src: "component c generic (type t; impure function f return t " +
			"is <>; procedure pr is work.p.pr0; function \"=\" (a, b : " +
			"t) return boolean is \"=\"); end component;",
			want: "ComponentDecl(GenericClause(InterfaceList(" +
				"InterfaceTypeDecl InterfaceSubprogramDecl(FunctionSpec(" +
				"ReturnSpec(E)) InterfaceSubprogramDefault) " +
				"InterfaceSubprogramDecl(ProcedureSpec " +
				"InterfaceSubprogramDefault(E)) InterfaceSubprogramDecl(" +
				"FunctionSpec(ParameterList(InterfaceList(" +
				"InterfaceObjectDecl(" + ids + " " + si + "))) " +
				"ReturnSpec(E)) InterfaceSubprogramDefault(E)))) " +
				"EndClause)"},
		{src: "component c generic (type t is private; type u is " +
			"(<>)); port (p : view v; q : view (v) of t; r : type is " +
			"(<>)); end component;",
			want: "ComponentDecl(GenericClause(InterfaceList(" +
				"InterfaceTypeDecl(PrivateIncompleteTypeDef) " +
				"InterfaceTypeDecl(DiscreteIncompleteTypeDef))) " +
				"PortClause(InterfaceList(InterfaceObjectDecl(" + ids +
				" ModeViewIndication(E)) InterfaceObjectDecl(" + ids +
				" ArrayModeViewIndication(E " + si + ")) " +
				"InterfaceObjectDecl(" + ids + " AnonymousTypeIndication(" +
				"DiscreteIncompleteTypeDef)))) EndClause)",
			needs: "v2019"},
		{src: "component c generic (package ip is new work.p generic " +
			"map (<>)); end component;",
			want: "ComponentDecl(GenericClause(InterfaceList(" +
				"InterfacePackageDecl(E GenericMapAspect(AssociationList(" +
				"AssociationElement(E)))))) EndClause)",
			needs: "expr"},
	})
}

// invalidCase is a broken input: the first real problem's offset and a
// substring of its message, and the shape after recovery.
type invalidCase struct {
	src    string
	region DeclRegion
	at     int
	msg    string
	want   string // items shape; "" to skip
}

func TestInvalidDeclarations(t *testing.T) {
	cases := []invalidCase{
		{src: "signal s : ; constant c : integer;", at: 10,
			msg: "expected a subtype indication",
			want: "SignalDecl(IdentifierList) ConstantDecl(IdentifierList " +
				"SubtypeIndication(E))"},
		{src: "type t is ; signal s : bit;", at: 9,
			msg: "expected a type definition",
			want: "TypeDecl SignalDecl(IdentifierList " +
				"SubtypeIndication(E))"},
		{src: "constant c integer;", at: 10, msg: "expected ':'"},
		{src: "component c port (a : in bit b : out bit); end component;",
			at: 30, msg: "expected ';' or ')'"},
		{src: "type r is record a : bit end record;", at: 24,
			msg: "expected ';'"},
		{src: "function f (a : integer);", at: 24,
			msg: "expected reserved word return"},
		{src: "signal s : bit;", region: RegionProcess, at: 0,
			msg: "a signal declaration is not allowed in a process " +
				"declarative part"},
		{src: "variable v : integer;", at: 0,
			msg: "(declare it shared)"},
		{src: "variable v : integer;", region: RegionProtected, at: 0,
			msg: "a non-shared variable declaration is not allowed in " +
				"a protected type declaration"},
		{src: "function f return integer is begin end;", at: 0,
			region: RegionPackage,
			msg: "a subprogram body is not allowed in a package " +
				"declaration"},
		{src: "type e is (a, , b);", at: 13,
			msg: "expected an enumeration literal"},
		{src: "type a is array (natural range <>) bit;", at: 34,
			msg: "expected reserved word of"},
		{src: "type p is range 0 to 9 units fs; ps = 10 fs; 7; end units;",
			at: 45, msg: "expected a secondary unit declaration",
			want: "TypeDecl(PhysicalTypeDef(RangeConstraint(RangeExpr(E " +
				"E)) PrimaryUnitDecl SecondaryUnitDecl(E) E EndClause))"},
		{src: "component c port (a : in bit; 1 : out bit); end;", at: 30,
			msg: "expected an interface declaration"},
		{src: "attribute a of s : nonsense is 1;", at: 18,
			msg: "expected an entity class"},
		{src: "component c port (a : in bit;\nend component;", at: 29,
			msg: "expected ')'",
			want: "ComponentDecl(PortClause(InterfaceList(" +
				"InterfaceObjectDecl(IdentifierList SimpleModeIndication " +
				"SubtypeIndication(E)))) EndClause)"},
	}
	for _, c := range cases {
		r := c.region
		if r == 0 {
			r = RegionArchitecture
		}
		root, probs := parseRegion(t, c.src, r, token.Std2019)
		ps := realProblems(probs)
		if len(ps) == 0 {
			t.Errorf("%q: no problem", c.src)
			continue
		}
		if ps[0].Start != c.at || !strings.Contains(ps[0].Args[0], c.msg) {
			t.Errorf("%q: first problem\n%swant @%d %q", c.src,
				problemText(c.src, ps), c.at, c.msg)
		}
		if c.want != "" && !exprStubbed {
			if got := itemsShape(root); got != c.want {
				t.Errorf("%q:\n got %s\nwant %s", c.src, got, c.want)
			}
		}
	}
}

// regionItems is one sample item per declaration class (PSL
// declarations, owned by the psl builder, are left out).
var regionItems = []struct {
	c   declClass
	src string
}{
	{dcType, "type t is (a, b);"},
	{dcConstant, "constant c : integer := 1;"},
	{dcSignal, "signal s : bit;"},
	{dcSharedVariable, "shared variable v : pt;"},
	{dcVariable, "variable v : integer;"},
	{dcFile, "file f : text;"},
	{dcAlias, "alias a is b;"},
	{dcAttributeDecl, "attribute a : integer;"},
	{dcAttributeSpec, "attribute a of s : signal is 1;"},
	{dcComponent, "component c end component;"},
	{dcConfigSpec, "for all : c use entity work.e;"},
	{dcDisconnect, "disconnect s : t after 1 ns;"},
	{dcUse, "use work.p.all;"},
	{dcGroup, "group g : t (a, b);"},
	{dcSubprogramDecl, "function f return integer;"},
	{dcSubprogramBody, "procedure q is begin end procedure;"},
	{dcSubprogramInst, "function g is new f;"},
	{dcPackageDecl, "package q is end package;"},
	{dcPackageBody, "package body q is end package body;"},
	{dcPackageInst, "package q is new work.p;"},
	{dcModeView, "view v of r is a : in; end view;"},
	{dcPrivate, "private variable v : pt;"},
}

// TestRegionRules: every item parses in every region into the same
// node, and is reported exactly when the region does not allow it.
func TestRegionRules(t *testing.T) {
	for r := RegionEntity; r <= RegionPSLUnit; r++ {
		for _, it := range regionItems {
			root, probs := parseRegion(t, it.src, r, token.Std2019)
			var other []Problem
			reported := false
			for _, p := range realProblems(probs) {
				if strings.Contains(p.Args[0], "is not allowed in") {
					reported = true
					if !strings.HasPrefix(p.Args[0],
						declClassNames[it.c]) || p.Start != 0 {
						t.Errorf("%v %q: %v", r, it.src, p)
					}
				} else {
					other = append(other, p)
				}
			}
			if reported == r.allows(it.c) {
				t.Errorf("%s: %q reported=%v", regionNames[r], it.src,
					reported)
			}
			if len(other) > 0 {
				t.Errorf("%s: %q:\n%s", regionNames[r], it.src,
					problemText(it.src, other))
			}
			ref, _ := parseRegion(t, it.src, RegionPSLUnit, token.Std2019)
			if !cst.Equal(root.Green(), ref.Green()) {
				t.Errorf("%s: %q: the tree depends on the region",
					regionNames[r], it.src)
			}
		}
	}
	if !RegionPackageBody.allows(dcSharedVariable) ||
		RegionPackageBody.allows(dcSignal) ||
		RegionProtected.allows(dcType) || !RegionProcess.allows(dcVariable) {
		t.Error("region table")
	}
}

// gateCase counts the occurrences of one rules.csv rule in src.
type gateCase struct {
	rule   string
	src    string
	region DeclRegion
	n      int
}

var gateCases = []gateCase{
	{"generic_type", "component c generic (type t; type u); end " +
		"component;", 0, 2},
	{"generic_subp", "component c generic (function f return integer; " +
		"procedure p); end component;", 0, 2},
	{"generic_pkg", "component c generic (package p is new work.q " +
		"generic map (<>)); end component;", 0, 1},
	{"generic_on_subp", "procedure p generic (n : natural); function f " +
		"generic (n : natural) return integer;", 0, 2},
	{"subp_inst", "procedure q is new p; function g is new f;", 0, 2},
	{"nested_pkg", "package q is end package; package body q is end " +
		"package body;", 0, 2},
	{"pkg_inst", "package q is new work.p;", 0, 1},
	{"record_constraint", "subtype a is r(f(open)); subtype b is " +
		"t(open)(0 to 1);", 0, 4},
	{"elem_resolution", "subtype a is (resolved) slv; subtype b is (x " +
		"resolved) r;", 0, 2},
	{"pkg_body_attr_decl", "attribute a : integer; attribute b : " +
		"string;", RegionPackageBody, 2},
	{"pkg_body_attr_decl", "attribute a : integer;", 0, 0},
	{"trailing_semi", "component c port (a : bit;); end component; " +
		"procedure p (x : integer;);", 0, 2},
	{"end_component", "component c end; component d end d; component " +
		"e end component;", 0, 2},
	{"empty_record", "type r is record end record; type s is record " +
		"a : bit; end record;", 0, 1},
	{"subtype_attr", "subtype s is x'subtype; signal t : y'subtype;", 0,
		2},
	{"record_attr", "subtype s is x'record; signal t : y'element'record;",
		0, 2},
}

// TestGates: every parser-gated rule of the decl and types builders
// fires on every occurrence exactly when its first standard is later
// than the selected one, and never changes the tree (#7).
func TestGates(t *testing.T) {
	for _, c := range gateCases {
		rule, ok := RuleByID(c.rule)
		if !ok {
			t.Fatalf("unknown rule %s", c.rule)
		}
		r := c.region
		if r == 0 {
			r = RegionArchitecture
		}
		ref, _ := parseRegion(t, c.src, r, token.Std2019)
		for _, std := range token.Standards {
			root, probs := parseRegion(t, c.src, r, std)
			n := 0
			for _, p := range probs {
				if p.Code == diag.SyntaxStandard &&
					p.Args[0] == rule.Construct {
					n++
					if p.Args[1] != rule.First.String() ||
						p.Args[2] != std.String() {
						t.Errorf("%s: args %v", c.rule, p.Args)
					}
				}
			}
			want := 0
			if rule.First > std {
				want = c.n
			}
			if n != want {
				t.Errorf("%s under %v: %d diagnostics, want %d", c.rule,
					std, n, want)
			}
			if !cst.Equal(root.Green(), ref.Green()) {
				t.Errorf("%s under %v: the tree differs", c.rule, std)
			}
		}
	}
}

// TestPrefixes: every byte prefix of every sample (valid and invalid)
// parses in every region it was written for: lossless, deterministic,
// within budget, problems inside the input.
func TestPrefixes(t *testing.T) {
	var srcs []string
	for _, it := range regionItems {
		srcs = append(srcs, it.src)
	}
	for _, c := range gateCases {
		srcs = append(srcs, c.src)
	}
	for _, c := range typeShapeCases {
		srcs = append(srcs, c.src)
	}
	for _, src := range srcs {
		for i := range len(src) + 1 {
			parseRegion(t, src[:i], RegionArchitecture, token.Std2019)
		}
	}
}

// TestDeclInactiveBranches: inactive conditional-analysis branches in
// declarative parts, interface lists, records and physical units become
// structured InactiveBranch subtrees.
func TestDeclInactiveBranches(t *testing.T) {
	src := "package p is\n" +
		"`if A = \"1\" then\n  signal s : bit;\n" +
		"`else\n  signal s : integer;\n`end if\n" +
		"  component c port (\n" +
		"`if A = \"1\" then\n    a : in bit;\n" +
		"`else\n    a : in integer;\n`end if\n" +
		"    b : out bit);\n  end component;\n" +
		"  type r is record\n" +
		"`if A = \"2\" then\n    x : bit;\n`end if\n" +
		"    y : bit;\n  end record;\n" +
		"  type d is range 0 to 9 units fs;\n" +
		"`if A = \"2\" then\n    ps = 1000 fs;\n`end if\n" +
		"  end units;\n" +
		"end package;\n"
	r := checkInvariants(t, "inactive", []byte(src), Options{
		Standard: token.Std2019,
		Defines:  []condanal.Define{{Name: "A", Value: "1"}},
	})
	for _, p := range r.Problems {
		if !stubProblem(p) {
			t.Errorf("unexpected %v", p)
		}
	}
	parents := map[string]string{}
	for n := range r.Tree().Descendants() {
		if n.Kind() != cst.KindInactiveBranch {
			continue
		}
		var kids []string
		for c := range n.Nodes() {
			kids = append(kids, c.Kind().String())
		}
		parents[n.Parent().Kind().String()] += strings.Join(kids, ",") +
			";"
	}
	want := map[string]string{
		"DeclarativePart": "SignalDecl;",
		"InterfaceList":   "InterfaceObjectDecl;",
		"RecordTypeDef":   "ElementDecl;",
		"PhysicalTypeDef": "SecondaryUnitDecl;",
	}
	for k, v := range want {
		if parents[k] != v {
			t.Errorf("inactive branches under %s: %q, want %q", k,
				parents[k], v)
		}
	}
}

// TestDeclAccessors: the typed wrappers find the fields the builders
// produce.
func TestDeclAccessors(t *testing.T) {
	src := "signal a, b : bit register := '0'; " +
		"pure function \"+\" (l, r : t) return t; " +
		"component c port (signal x : inout bit); end component;"
	root, _ := parseRegion(t, src, RegionArchitecture, token.Std2019)
	part := root.Child[cst.DeclarativePart](0)
	var items []cst.Decl
	for it := range part.Items() {
		items = append(items, it)
	}
	if len(items) != 3 {
		t.Fatalf("%d items", len(items))
	}
	sig, ok := cst.As[cst.SignalDecl](items[0].Node)
	if !ok || sig.SignalKind().Kind() != token.KwRegister {
		t.Errorf("signal kind %v", sig.SignalKind().Kind())
	}
	var names []string
	for n := range sig.Names().Names() {
		names = append(names, n.Text())
	}
	if !slices.Equal(names, []string{"a", "b"}) {
		t.Errorf("names %v", names)
	}
	sub, _ := cst.As[cst.SubprogramDecl](items[1].Node)
	fn, ok := cst.As[cst.FunctionSpec](sub.Spec().Node)
	if !ok || fn.Designator().Text() != `"+"` ||
		fn.ReturnType().IsZero() || fn.Params().IsZero() {
		t.Errorf("function spec %q", fn.Designator().Text())
	}
	comp, _ := cst.As[cst.ComponentDecl](items[2].Node)
	var objs []cst.InterfaceObjectDecl
	for d := range comp.Ports().List().Items() {
		o, _ := cst.As[cst.InterfaceObjectDecl](d.Node)
		objs = append(objs, o)
	}
	if len(objs) != 1 || objs[0].Class().Kind() != token.KwSignal {
		t.Fatalf("ports %v", objs)
	}
	mode, _ := cst.As[cst.SimpleModeIndication](objs[0].Mode().Node)
	if mode.Mode().Kind() != token.KwInout {
		t.Errorf("mode %v", mode.Mode().Kind())
	}
}
