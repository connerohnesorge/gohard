package parser

import (
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/token"
)

const gateGenText = "if-generate elsif/else, case generate, alt labels"

var concCases = []stmtCase{
	{src: "process begin wait; end process;", want: []string{"ProcessStmt"}},
	{src: "p: process (clk, rst) is variable v : bit; begin v := '1'; " +
		"end process p;", want: []string{"ProcessStmt"},
		has: []cst.Kind{cst.KindProcessSensitivityList,
			cst.KindDeclarativePart, cst.KindSequenceOfStatements}},
	{src: "process (all) begin end process;", want: []string{"ProcessStmt"},
		gate: "process (all)", first: token.Std2008},
	{src: "postponed process begin end postponed process;",
		want: []string{"ProcessStmt"}},
	{src: "l: postponed process (a) begin end process l;",
		want: []string{"ProcessStmt"}},
	{src: "b: block begin end block;", want: []string{"BlockStmt"}},
	{src: "b: block (en = '1') is generic (n : integer); generic map " +
		"(n => 4); port (x : in bit); port map (x => y); signal s : " +
		"bit; begin s <= guarded x; end block b;",
		want: []string{"BlockStmt"},
		has: []cst.Kind{cst.KindBlockHeader, cst.KindGenericClause,
			cst.KindGenericMapAspect, cst.KindPortClause,
			cst.KindPortMapAspect}},
	{src: "proc(a, b);", want: []string{"ConcurrentProcedureCallStmt"}},
	{src: "postponed proc;", want: []string{"ConcurrentProcedureCallStmt"}},
	{src: "l: work.p.proc(x => 1);",
		want: []string{"ConcurrentProcedureCallStmt"}},
	{src: "display generic map (t)(y);",
		want: []string{"ConcurrentProcedureCallStmt"},
		gate: "generic map on call", first: token.Std2019},
	{src: `assert a = b report "x" severity warning;`,
		want: []string{"ConcurrentAssertionStmt"}},
	{src: "l: postponed assert a;",
		want: []string{"ConcurrentAssertionStmt"}},
	{src: "s <= a;", want: []string{"ConcurrentSimpleSignalAssign"}},
	{src: "s <= guarded transport a after 1 ns;",
		want: []string{"ConcurrentSimpleSignalAssign"}},
	{src: "(a, b) <= v;", want: []string{"ConcurrentSimpleSignalAssign"}},
	{src: "l: postponed s <= a when c else b when d else unaffected;",
		want: []string{"ConcurrentConditionalSignalAssign"},
		has:  []cst.Kind{cst.KindConditionalWaveforms}},
	{src: "s <= a when c;",
		want: []string{"ConcurrentConditionalSignalAssign"}},
	{src: "with sel select s <= guarded reject 1 ns inertial a when " +
		"\"0\" | \"1\", b when others;",
		want: []string{"ConcurrentSelectedSignalAssign"},
		has:  []cst.Kind{cst.KindSelectedWaveforms}},
	{src: "postponed with sel select? s <= a when \"-1\", b when others;",
		want: []string{"ConcurrentSelectedSignalAssign"},
		gate: "select? statement", first: token.Std2008},
	{src: "u1: comp port map (a => b, c => open);",
		want: []string{"ComponentInstantiationStmt"},
		has:  []cst.Kind{cst.KindInstantiatedUnit, cst.KindPortMapAspect}},
	{src: "u1: comp;", want: []string{"ComponentInstantiationStmt"}},
	{src: "u1: lib.comp generic map (4) port map (x);",
		want: []string{"ComponentInstantiationStmt"},
		has:  []cst.Kind{cst.KindGenericMapAspect}},
	{src: "u2: component comp port map (x);",
		want: []string{"ComponentInstantiationStmt"}},
	{src: "u3: entity work.e(rtl) generic map (n => 1) port map (x);",
		want: []string{"ComponentInstantiationStmt"}},
	{src: "u4: entity work.e;", want: []string{"ComponentInstantiationStmt"}},
	{src: "u5: configuration work.cfg port map (x);",
		want: []string{"ComponentInstantiationStmt"}},
	{src: "g: for i in 0 to 3 generate s(i) <= a(i); end generate;",
		want: []string{"ForGenerateStmt"},
		has: []cst.Kind{cst.KindParameterSpecification,
			cst.KindGenerateBody}},
	{src: "g: for i in v'range generate signal t : bit; begin t <= a; " +
		"end generate g;", want: []string{"ForGenerateStmt"}},
	{src: "g: for i in 0 to 1 generate begin end generate;",
		want: []string{"ForGenerateStmt"}},
	{src: "g: if c = 1 generate u: comp; end generate g;",
		want: []string{"IfGenerateStmt"}, has: []cst.Kind{
			cst.KindGenerateBranch, cst.KindComponentInstantiationStmt}},
	{src: "g: if a: c generate s <= a; end a; elsif b: d generate " +
		"elsif e generate else z: generate s <= b; end; end generate;",
		want: []string{"IfGenerateStmt"},
		gate: gateGenText, first: token.Std2008},
	{src: "g: case sel generate when a: 0 => s <= x; end a; when " +
		"1 | 2 => when others => end generate g;",
		want: []string{"CaseGenerateStmt"},
		has:  []cst.Kind{cst.KindCaseGenerateAlternative},
		gate: gateGenText, first: token.Std2008},
	{src: "process begin end process; u: comp; s <= a;",
		want: []string{"ProcessStmt", "ComponentInstantiationStmt",
			"ConcurrentSimpleSignalAssign"}},
}

func TestConcurrentStatements(t *testing.T) {
	checkStmtCases(t, concCases, concPre, conPost, cst.KindStatementPart)
}

// TestConcurrentAssertPSL: under 2008 and later a concurrent assert
// whose property uses PSL operators goes to the PSL parser, a plain
// Boolean one stays a VHDL concurrent assertion, and so does every
// assert before 2008 or after postponed; the lexer mode is back to
// VHDL afterwards.
func TestConcurrentAssertPSL(t *testing.T) {
	cases := []struct {
		src string
		psl bool // under 2008+
	}{
		{"assert a = b;", false},
		{"l: assert f(a) and b report \"x\";", false},
		{"assert always a -> next b;", true},
		{"assert always (a -> b) @ rising_edge(clk);", true},
		{"l: assert never (a and b);", true},
		{"assert {a; b} |=> c;", true},
		{"assert a until b report \"x\";", true},
		{"assert (a) report \"until\";", false},
		{"postponed assert a;", false},
	}
	const next = "\nzz <= '1';"
	for _, c := range cases {
		for _, std := range token.Standards {
			if strings.Contains(c.src, "{") && std < token.Std2008 {
				continue // braces are not VHDL-1993
			}
			src := concPre + c.src + next + conPost
			r := checkInvariants(t, c.src, []byte(src),
				Options{Standard: std})
			items := stmtItems(r, cst.KindStatementPart)
			if len(items) != 2 ||
				items[1] != "ConcurrentSimpleSignalAssign" {
				t.Errorf("%q (%v): items %v", c.src, std, items)
				continue
			}
			wantPSL := c.psl && std >= token.Std2008
			gotPSL := items[0] != "ConcurrentAssertionStmt"
			if gotPSL != wantPSL {
				t.Errorf("%q (%v): %s, PSL want %v", c.src, std,
					items[0], wantPSL)
			}
			if gotPSL && !strings.HasPrefix(items[0], "PSL") &&
				items[0] != "ErrorNode" {
				t.Errorf("%q (%v): PSL item %s", c.src, std, items[0])
			}
		}
	}
}

// TestConcurrentPSLItems: PSL directives (labelled or not) and
// declarations in a statement part go to psl.go under 2008+; before
// 2008 the PSL-only words are identifiers.
func TestConcurrentPSLItems(t *testing.T) {
	cases := []string{
		"cover {a; b};",
		"l: cover {a};",
		"assume always a;",
		"l: restrict {a[*]};",
		"default clock is rising_edge(clk);",
		"property p is always a;",
		"sequence s is {a; b};",
		"fairness a;",
		"strong fairness a, b;",
	}
	for _, c := range cases {
		src := concPre + c + "\nzz <= '1';" + conPost
		r := checkInvariants(t, c, []byte(src),
			Options{Standard: token.Std2008})
		items := stmtItems(r, cst.KindStatementPart)
		if len(items) != 2 || items[1] != "ConcurrentSimpleSignalAssign" {
			t.Errorf("%q: items %v", c, items)
			continue
		}
		if !strings.HasPrefix(items[0], "PSL") && items[0] != "ErrorNode" {
			t.Errorf("%q: item %s", c, items[0])
		}
	}
	// PSL-only words used as names outside PSL stay VHDL.
	for _, c := range []string{"assume <= '1';", "l: assume port map (x);",
		"property;"} {
		src := concPre + c + conPost
		r := checkInvariants(t, c, []byte(src),
			Options{Standard: token.Std2008})
		items := stmtItems(r, cst.KindStatementPart)
		if len(items) != 1 || strings.HasPrefix(items[0], "PSL") ||
			items[0] == "ErrorNode" {
			t.Errorf("%q: items %v", c, items)
		}
	}
}

// TestConcurrentRecovery: a broken concurrent statement does not hide
// the next one.
func TestConcurrentRecovery(t *testing.T) {
	cases := []struct {
		src  string
		want []string
	}{
		{"s <= a b; t <= c;", []string{"ConcurrentSimpleSignalAssign",
			"ConcurrentSimpleSignalAssign"}},
		{"process begin s <= a; t <= b;", []string{"ProcessStmt"}},
		{"g: for i in 0 to 1 generate s <= a; end; t <= b;",
			[]string{"ForGenerateStmt", "ConcurrentSimpleSignalAssign"}},
		{"comp port map (x); t <= b;", []string{
			"ComponentInstantiationStmt", "ConcurrentSimpleSignalAssign"}},
		{"g: case s generate x <= y; end generate; t <= b;", []string{
			"CaseGenerateStmt", "ConcurrentSimpleSignalAssign"}},
	}
	for _, c := range cases {
		src := concPre + c.src + conPost
		r := checkInvariants(t, c.src, []byte(src),
			Options{Standard: token.Std2008})
		got := stmtItems(r, cst.KindStatementPart)
		if !slices.Equal(got, c.want) {
			t.Errorf("%q: items %v, want %v", c.src, got, c.want)
		}
		if len(stmtErrors(r)) == 0 {
			t.Errorf("%q: no error", c.src)
		}
	}
}

// TestStatementNesting: deeply nested statements end in the depth
// limit, not a stack overflow, and stay lossless.
func TestStatementNesting(t *testing.T) {
	n := 3 * maxDepth
	seq := seqPre + strings.Repeat("if a then ", n) +
		strings.Repeat("end if; ", n) + seqPost
	conc := concPre + strings.Repeat("g: if a generate ", n) +
		strings.Repeat("end generate; ", n) + conPost
	blk := concPre + strings.Repeat("b: block begin ", n) +
		strings.Repeat("end block; ", n) + conPost
	for _, src := range []string{seq, conc, blk} {
		checkInvariants(t, "nested", []byte(src),
			Options{Standard: token.Std2019})
	}
}
