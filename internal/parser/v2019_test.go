package parser

import (
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/condanal"
	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// ruleSnippets holds one valid file per rules.csv row (#7) that uses
// the row's construct.
var ruleSnippets = map[string]string{
	"prot_type": "package p is type t is protected end protected; " +
		"end package;",
	"shared_nonprot": "architecture a of e is shared variable v : integer; " +
		"begin end;",
	"block_comment": "/* x */ entity e is end;",
	"context_decl":  "context c is library ieee; end context;",
	"generic_type":  "entity e is generic (type t); end;",
	"generic_subp":  "entity e is generic (function f return bit); end;",
	"generic_pkg": "entity e is generic (package q is new work.p " +
		"generic map (<>)); end;",
	"generic_on_subp": "package p is function f generic (n : natural) " +
		"return bit; end package;",
	"pkg_inst": "package q is new work.p generic map (n => 1);",
	"subp_inst": "package p is function g is new f generic map (n => 1); " +
		"end package;",
	"nested_pkg": "architecture a of e is begin process package q is " +
		"end package; begin wait; end process; end;",
	"process_all": "architecture a of e is begin process (all) begin " +
		"wait; end process; end;",
	"cond_seq_sig": "architecture a of e is begin process begin " +
		"s <= a when c else b; wait; end process; end;",
	"sel_var": "architecture a of e is begin process begin with s select " +
		"v := '0' when '1', '1' when others; wait; end process; end;",
	"case_match": "architecture a of e is begin process begin case? x is " +
		"when others => null; end case?; wait; end process; end;",
	"select_match": "architecture a of e is begin with x select? s <= '0' " +
		"when others; end;",
	"match_ops":   "architecture a of e is begin s <= a ?= b; end;",
	"cond_conv":   "architecture a of e is begin s <= ?? a; end;",
	"unary_logic": "architecture a of e is begin s <= and v; end;",
	"force": "architecture a of e is begin process begin s <= force '1'; " +
		"wait; end process; end;",
	// `s <= release;` (the rules.csv snippet) is also a legal 1993
	// assignment of a signal named release; `release out` needs the
	// keyword under every standard.
	"release": "architecture a of e is begin process begin " +
		"s <= release out; wait; end process; end;",
	"ext_name": "architecture a of e is begin s <= << signal .tb.dut.x : " +
		"bit >>; end;",
	"record_constraint": "package p is subtype t is r(f(7 downto 0)); " +
		"end package;",
	"elem_resolution": "package p is subtype t is (resolved) " +
		"std_ulogic_vector; end package;",
	"bitstr_len": "package p is constant c : bit_vector := 8x\"FF\"; " +
		"end package;",
	"gen_elsif_else_case": "architecture a of e is begin g: case x " +
		"generate when others => end generate; end;",
	"inertial_actual": "architecture a of e is begin u: entity work.e " +
		"port map (p => inertial a); end;",
	"subtype_attr": "package p is subtype t is x'subtype; end package;",
	"pkg_body_attr_decl": "package body p is attribute a : integer; " +
		"end package body;",
	"condanal": "`if VHDL_VERSION >= \"2019\" then\n`end if\n" +
		"entity e is end;",
	"mode_view": "package p is view v of r is a : in; end view; " +
		"end package;",
	"private_var": "package p is type t is protected private variable " +
		"v : integer; end protected; end package;",
	"incomplete_generic": "entity e is generic (type t is private); end;",
	"cond_expr": "package p is constant c : integer := 1 when x else 2; " +
		"end package;",
	"cond_return": "package body p is function f return integer is " +
		"begin return 0 when c; return 1; end function; end package body;",
	"seq_block": "architecture a of e is begin process begin block begin " +
		"end block; wait; end process; end;",
	"empty_record": "package p is type r is record end record; " +
		"end package;",
	"trailing_semi": "entity e is port (a : bit;); end;",
	"end_component": "package p is component c is end; end package;",
	"record_attr":   "package p is subtype t is x'record; end package;",
	"anon_type":     "entity e is generic (g : type is (<>)); end;",
	"subp_generic_map_call": "package p is constant c : integer := " +
		"f generic map (n => 1) (x); end package;",
	"param_map": "package p is constant c : integer := f parameter map " +
		"(x => 1); end package;",
	"prot_port":      "entity e is port (p : inout pt); end;",
	"std_env_2008":   "use std.env.all; entity e is end;",
	"std_reflection": "use std.reflection.all; entity e is end;",
	"seq_selected_sig": "architecture a of e is begin process begin " +
		"with s select t <= a when '0', b when others; wait; " +
		"end process; end;",
	"cond_var": "architecture a of e is begin process begin " +
		"v := a when c else b; wait; end process; end;",
	"prot_generics": "package p is type t is protected generic " +
		"(n : natural); end protected; end package;",
	"prot_inst": "package p is type t2 is new t generic map (n => 1); " +
		"end package;",
	"var_generic_map": "package p is shared variable v : t " +
		"generic map (n => 1); end package;",
}

// ruleFired reports whether r reports syntax/standard for rule.
func ruleFired(r *Result, rule Rule) int {
	n := 0
	for _, p := range r.Problems {
		if p.Code != diag.SyntaxStandard {
			continue
		}
		if p.Args[0] == rule.Construct ||
			(strings.Contains(rule.Gate, "keyword") &&
				strings.HasPrefix(p.Args[0], "the reserved word ") &&
				p.Args[1] == rule.First.String()) {
			n++
		}
	}
	return n
}

// parserGated reports whether the parser (lexer, prepass or grammar)
// owns the rule's gate; sema rules never fire in the parser.
func parserGated(r Rule) bool {
	return !r.Reverse && (strings.Contains(r.Gate, "parser") ||
		strings.Contains(r.Gate, "lexer") ||
		strings.Contains(r.Gate, "prepass"))
}

// TestRulesEveryRowEveryStandard is the generated rules.csv test (#7,
// #31): for every row and every standard S, the row's snippet reports
// syntax/standard for the row exactly when the row's first standard is
// later than S (never for sema rows), the snippet is otherwise free of
// syntax errors, and the tree has the same shape under every standard.
func TestRulesEveryRowEveryStandard(t *testing.T) {
	for _, rule := range Rules() {
		src, ok := ruleSnippets[rule.ID]
		if !ok {
			t.Errorf("rule %s has no snippet", rule.ID)
			continue
		}
		var shape0 []string
		for _, std := range token.Standards {
			opts := Options{Standard: std,
				Defines: []condanal.Define{{Name: "X", Value: "1"}}}
			r := checkInvariants(t, rule.ID, []byte(src), opts)
			fired := ruleFired(r, rule) > 0
			want := parserGated(rule) && rule.First > std
			if fired != want {
				t.Errorf("%s under %v: fired=%v, want %v; problems %q",
					rule.ID, std, fired, want, problemCodes(r))
			}
			for _, p := range r.Problems {
				if p.Code == diag.SyntaxError {
					t.Errorf("%s under %v: syntax error %q at %d", rule.ID,
						std, p.Args, p.Start)
				}
			}
			shape := kindsPath(r)
			switch {
			case shape0 == nil:
				shape0 = shape
			case rule.Gate == "lexer" || rule.Gate == "prepass":
				// The lexer gates change the tokens (a /* before 2008
				// is not a comment), not the parser's tree.
			case !slices.Equal(shape, shape0):
				t.Errorf("%s: the tree under %v differs from 1993:\n%q\n%q",
					rule.ID, std, shape, shape0)
			}
		}
	}
	for id := range ruleSnippets {
		if _, ok := RuleByID(id); !ok {
			t.Errorf("snippet for unknown rule %s", id)
		}
	}
}

// TestRuleForKind: the gate engine's kind table names existing,
// parser-gated rules; it is a pure function of the kind.
func TestRuleForKind(t *testing.T) {
	n := 0
	for _, k := range cst.Kinds() {
		r, ok := RuleForKind(k)
		if !ok {
			if ruleIDForKind(k) != "" {
				t.Errorf("%v maps to unknown rule %s", k, ruleIDForKind(k))
			}
			continue
		}
		n++
		if !strings.Contains(r.Gate, "parser") || r.Reverse {
			t.Errorf("%v -> %s is not parser-gated (%s)", k, r.ID, r.Gate)
		}
		if r2, _ := RuleForKind(k); r2 != r {
			t.Errorf("%v: not deterministic", k)
		}
	}
	if n < 20 {
		t.Errorf("only %d kinds have a rule", n)
	}
}

// v2019Files are inputs with every construct the v2019 builder owns.
var v2019Files = []string{`package p is
  view v of r is
    a, b : in;
    c : out;
    d : view w;
    e : view (w2);
  end view v;
  type pt is protected
    generic (n : natural);
    private variable x : integer;
  end protected;
  type pt2 is new pt generic map (n => 1);
end package;
`, `entity e is
  generic (type t1 is private; type t2 is <>; type t3 is (<>);
    type t4 is range <>; type t5 is units <>; type t6 is range <>.<>;
    type t7 is array (natural range <>, type is (<>)) of type is private;
    type t8 is access t1; type t9 is file of t1;
    g : type is <>);
  port (p1 : view v of r; p2 : view (v) of ra; p3 : view v);
end;
`, `architecture a of e is begin
  process begin
    lbl: block is variable q : bit; begin null; end block lbl;
    block begin end block;
    wait;
  end process;
end;
`}

var v2019Shapes = [][]string{{
	"PackageDecl(DeclarativePart(ModeViewDecl(SubtypeIndication(E) " +
		"ModeViewElementDef(IdentifierList ElementModeIndication) " +
		"ModeViewElementDef(IdentifierList ElementModeIndication) " +
		"ModeViewElementDef(IdentifierList ElementModeIndication(E)) " +
		"ModeViewElementDef(IdentifierList ElementModeIndication(E)) " +
		"EndClause) TypeDecl(ProtectedTypeDef(ProtectedTypeHeader(" +
		"GenericClause(InterfaceList(InterfaceObjectDecl(IdentifierList " +
		"SubtypeIndication(E))))) DeclarativePart(" +
		"PrivateVariableDecl(VariableDecl(IdentifierList " +
		"SubtypeIndication(E)))) EndClause)) TypeDecl(" +
		"ProtectedTypeInstantiationDef(E GenericMapAspect(" +
		"AssociationList(AssociationElement(E E)))))) EndClause)",
}, {
	"EntityDecl(GenericClause(InterfaceList(InterfaceTypeDecl(" +
		"PrivateIncompleteTypeDef) InterfaceTypeDecl(" +
		"ScalarIncompleteTypeDef) InterfaceTypeDecl(" +
		"DiscreteIncompleteTypeDef) InterfaceTypeDecl(" +
		"IntegerIncompleteTypeDef) InterfaceTypeDecl(" +
		"PhysicalIncompleteTypeDef) InterfaceTypeDecl(" +
		"FloatingIncompleteTypeDef) InterfaceTypeDecl(" +
		"ArrayIncompleteTypeDef(IndexSubtypeDef(E) " +
		"AnonymousTypeIndication(DiscreteIncompleteTypeDef) " +
		"AnonymousTypeIndication(PrivateIncompleteTypeDef))) " +
		"InterfaceTypeDecl(AccessIncompleteTypeDef(SubtypeIndication(E))) " +
		"InterfaceTypeDecl(FileIncompleteTypeDef(E)) InterfaceObjectDecl(" +
		"IdentifierList AnonymousTypeIndication(" +
		"ScalarIncompleteTypeDef)))) PortClause(InterfaceList(" +
		"InterfaceObjectDecl(IdentifierList ModeViewIndication(E " +
		"SubtypeIndication(E))) InterfaceObjectDecl(IdentifierList " +
		"ArrayModeViewIndication(E SubtypeIndication(E))) " +
		"InterfaceObjectDecl(IdentifierList ModeViewIndication(E)))) " +
		"DeclarativePart EndClause)",
}, {
	"ArchitectureBody(E DeclarativePart StatementPart(ProcessStmt(" +
		"DeclarativePart SequenceOfStatements(SequentialBlockStmt(Label " +
		"DeclarativePart(VariableDecl(IdentifierList SubtypeIndication(E))) " +
		"SequenceOfStatements(NullStmt) EndClause) SequentialBlockStmt(" +
		"DeclarativePart SequenceOfStatements EndClause) WaitStmt) " +
		"EndClause)) EndClause)",
}}

// unitShapes returns the shapes of the library units of r.
func unitShapes(r *Result) []string {
	var out []string
	for du := range r.Tree().Nodes() {
		for n := range du.Nodes() {
			if n.Kind() != cst.KindContextClause {
				out = append(out, pslShape(n))
			}
		}
	}
	return out
}

// Test2019Shapes: every 2019 construct of the v2019 builder parses into
// the same tree under every standard; from 2019 on there is no
// diagnostic, before 2019 only syntax/standard.
func Test2019Shapes(t *testing.T) {
	for i, src := range v2019Files {
		for _, std := range token.Standards {
			r := checkInvariants(t, src, []byte(src), Options{Standard: std})
			if got := unitShapes(r); !slices.Equal(got, v2019Shapes[i]) {
				t.Errorf("file %d under %v:\n got %q\nwant %q", i, std, got,
					v2019Shapes[i])
			}
			for _, p := range r.Problems {
				if std >= token.Std2019 || p.Code != diag.SyntaxStandard {
					t.Errorf("file %d under %v: %v %q", i, std, p.Code,
						p.Args)
				}
			}
		}
	}
}

// TestGateKindInvariant: for the node-kind rules the v2019 builder
// owns, syntax/standard fires once per node exactly when the rule's
// first standard is later than the selected one (#7: every occurrence,
// a pure function of node kind and standard).
func TestGateKindInvariant(t *testing.T) {
	owned := []string{"mode_view", "private_var", "incomplete_generic",
		"anon_type", "seq_block"}
	inputs := slices.Clone(v2019Files)
	for _, id := range owned {
		inputs = append(inputs, ruleSnippets[id])
	}
	for _, src := range inputs {
		for _, std := range token.Standards {
			r := checkInvariants(t, src, []byte(src), Options{Standard: std})
			nodes := map[string]int{}
			for n := range r.Tree().Descendants() {
				if rule, ok := RuleForKind(n.Kind()); ok {
					nodes[rule.ID]++
				}
			}
			for _, id := range owned {
				rule, _ := RuleByID(id)
				want := 0
				if rule.First > std {
					want = nodes[id]
				}
				if got := ruleFired(r, rule); got != want {
					t.Errorf("%s under %v in %q: %d reports for %d nodes",
						id, std, src[:min(len(src), 40)], got, nodes[id])
				}
			}
		}
	}
}

// TestGateFirstStdNoReport: no rules.csv rule fires on its snippet from
// its first standard on (the gates never misfire), and every rule whose
// construct kind is in the kind table fires at most once per node.
func TestGateFirstStdNoReport(t *testing.T) {
	for _, rule := range Rules() {
		src := ruleSnippets[rule.ID]
		for _, std := range token.Standards {
			if std < rule.First {
				continue
			}
			r := Parse([]byte(src), Options{Standard: std})
			for _, p := range r.Problems {
				if p.Code == diag.SyntaxStandard {
					t.Errorf("%s under %v: %q", rule.ID, std, p.Args)
				}
			}
		}
	}
}

// Test2019Errors: broken 2019 constructs report at the right place and
// keep every byte.
func Test2019Errors(t *testing.T) {
	cases := []struct{ src, want string }{
		{"package p is view v of r is a : ; end view; end package;",
			"syntax/error@32"},
		{"package p is view v of r is a : in; end; end package;",
			"syntax/error@40"},
		{"entity e is port (p : view (v) r); end;", "syntax/error@31"},
		{"entity e is generic (type t is array (natural range <>) of); " +
			"end;", "syntax/error@59"},
		{"entity e is generic (g : type is foo); end;", "syntax/error@33"},
	}
	for _, c := range cases {
		r := checkInvariants(t, c.src, []byte(c.src),
			Options{Standard: token.Std2019})
		got := problemCodes(r)
		if len(got) == 0 || got[0] != c.want {
			t.Errorf("%q: %q, want %s first", c.src, got, c.want)
		}
	}
}

// TestGenericMapVariablesOnly: under 2019 a generic map after the
// subtype indication belongs to variable declarations (plain, shared,
// private, LRM 6.4.2.4) only; a signal or constant declaration with
// one is a syntax error.
func TestGenericMapVariablesOnly(t *testing.T) {
	const pre = "package p is "
	for _, decl := range []string{
		"signal s : t generic map (n => 1);",
		"constant c : t generic map (n => 1) := x;",
	} {
		src := pre + decl + " end package;"
		r := checkInvariants(t, src, []byte(src),
			Options{Standard: token.Std2019})
		got := problemCodes(r)
		if len(got) == 0 || !strings.HasPrefix(got[0], "syntax/error@") {
			t.Errorf("%q: %q, want a syntax error", decl, got)
		}
	}
	for _, decl := range []string{
		"shared variable v : t generic map (n => 1);",
	} {
		src := pre + decl + " end package;"
		r := checkInvariants(t, src, []byte(src),
			Options{Standard: token.Std2019})
		if got := problemCodes(r); len(got) != 0 {
			t.Errorf("%q: %q, want no problem", decl, got)
		}
	}
}
