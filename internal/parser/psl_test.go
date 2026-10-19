package parser

import (
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/lexer"
	"github.com/connerohnesorge/gohard/internal/token"
)

// pslCase is one PSL input: the declarations and statements of an
// architecture (or, with unit, a whole file), and the shapes of the
// items they parse into.
type pslCase struct {
	decls, stmts string
	unit         string
	want         []string
	// hook marks inputs that need the expression builder to honour
	// pslExprStop (an FL and/or whose right operand is PSL-only).
	hook bool
}

const pslArch = "entity e is end;\narchitecture a of e is\n"

func (c pslCase) source() string {
	if c.unit != "" {
		return c.unit
	}
	return pslArch + c.decls + "\nbegin\n" + c.stmts + "\nend;\n"
}

// pslItems returns the shapes of the items of the first architecture's
// declarative and statement parts, or of the first verification unit.
func pslItems(r *Result) []string {
	var out []string
	for n := range r.Tree().Descendants() {
		switch n.Kind() {
		case cst.KindArchitectureBody:
			for part := range n.Nodes() {
				if part.Kind() == cst.KindDeclarativePart ||
					part.Kind() == cst.KindStatementPart {
					for item := range part.Nodes() {
						out = append(out, pslShape(item))
					}
				}
			}
			return out
		case cst.KindPSLVunit, cst.KindPSLVmode, cst.KindPSLVprop:
			for item := range n.Nodes() {
				if item.Kind() != cst.KindPSLHierarchicalName {
					out = append(out, pslShape(item))
				}
			}
			return out
		}
	}
	return out
}

const (
	pB = "PSLBoolean(E)"
)

var pslCases = []pslCase{
	// Declarations.
	{decls: "default clock is rising_edge(clk);",
		want: []string{"PSLDefaultClock(E)"}},
	{decls: "property p1 is always a -> b;",
		want: []string{"PSLPropertyDecl(PSLAlways(PSLImplication(" + pB +
			" " + pB + ")))"}},
	{decls: "property p2(boolean b; const n; mutable numeric m; " +
		"sequence s; property q; hdltype bit_vector(3 downto 0) v; " +
		"bit c, d; bitvector w; string t) is b until s;",
		want: []string{"PSLPropertyDecl(PSLFormalParameterList(" +
			"PSLFormalParameter PSLFormalParameter PSLFormalParameter " +
			"PSLFormalParameter PSLFormalParameter " +
			"PSLFormalParameter(SubtypeIndication(E ArrayConstraint(E))) " +
			"PSLFormalParameter PSLFormalParameter PSLFormalParameter) " +
			"PSLUntil(" + pB + " " + pB + "))"}},
	{decls: "sequence s1 is {a; [*]; b[->2]; c[=1 to 3]; d[+]; e[*1 to inf]};",
		want: []string{"PSLSequenceDecl(PSLBracedSere(PSLSereConcat(" +
			"PSLSereConcat(PSLSereConcat(PSLSereConcat(PSLSereConcat(" + pB +
			" PSLRepetition) PSLRepetition(" + pB + " PSLCount(E))) " +
			"PSLRepetition(" + pB + " PSLCount(E E))) PSLRepetition(" + pB +
			")) PSLRepetition(" + pB + " PSLCount(E)))))"}},
	// Directives and their report clauses.
	{stmts: "l1: assert always a report \"bad\" severity error;",
		want: []string{"PSLAssert(Label PSLAlways(" + pB +
			") PSLReport(E E))"}},
	{stmts: "assume never (a and b);",
		want: []string{"PSLAssume(PSLNever(" + pB + "))"}},
	{stmts: "assume_guarantee always a report \"g\";",
		want: []string{"PSLAssumeGuarantee(PSLAlways(" + pB +
			") PSLReport(E))"}},
	{stmts: "restrict {rst; not rst[*]};",
		want: []string{"PSLRestrict(PSLBracedSere(PSLSereConcat(" + pB +
			" PSLRepetition(" + pB + "))))"}},
	{stmts: "restrict_guarantee {a} report \"r\";",
		want: []string{"PSLRestrictGuarantee(PSLBracedSere(" + pB +
			") PSLReport(E))"}},
	{stmts: "c1: cover {a; b} @ rising_edge(clk) report \"hit\";",
		want: []string{"PSLCover(Label PSLClockedProp(PSLBracedSere(" +
			"PSLSereConcat(" + pB + " " + pB + ")) E) PSLReport(E))"}},
	{stmts: "fairness a; strong fairness a, b;",
		want: []string{"PSLFairness(E)", "PSLFairness(E E)"}},
	// Temporal operators.
	{stmts: "assert always {a} |=> {b; c} until! d;",
		want: []string{"PSLAssert(PSLAlways(PSLSuffixImplication(" +
			"PSLBracedSere(" + pB + ") PSLUntil(PSLBracedSere(" +
			"PSLSereConcat(" + pB + " " + pB + ")) " + pB + "))))"}},
	{stmts: "assert always a -> eventually! b;",
		want: []string{"PSLAssert(PSLAlways(PSLImplication(" + pB +
			" PSLEventually(" + pB + "))))"}},
	{stmts: "assert always (a <-> b) abort rst;",
		want: []string{"PSLAssert(PSLAlways(PSLAbort(PSLParenProp(" +
			"PSLImplication(" + pB + " " + pB + ")) E)))"}},
	{stmts: "assert always a async_abort r1 sync_abort r2;",
		want: []string{"PSLAssert(PSLAlways(PSLAbort(PSLAbort(" + pB +
			" E) E)))"}},
	{stmts: "assert always a -> next! b; assert next[3] (a);",
		want: []string{"PSLAssert(PSLAlways(PSLImplication(" + pB +
			" PSLNext(" + pB + "))))", "PSLAssert(PSLNext(PSLCount(E) " +
			pB + "))"}},
	{stmts: "assert next_a[1 to 3] a; assert next_e![2 to 4] b;",
		want: []string{"PSLAssert(PSLNextA(PSLCount(E E) " + pB + "))",
			"PSLAssert(PSLNextE(PSLCount(E E) " + pB + "))"}},
	{stmts: "assert next_event!(a)[3](b before_ c); " +
		"assert next_event_a(a)[1 to 2](b before! c); " +
		"assert next_event_e(a)[1 to 2](b before!_ c);",
		want: []string{"PSLAssert(PSLNextEvent(E PSLCount(E) " +
			"PSLParenProp(PSLBefore(" + pB + " " + pB + "))))",
			"PSLAssert(PSLNextEventA(E PSLCount(E E) PSLParenProp(" +
				"PSLBefore(" + pB + " " + pB + "))))",
			"PSLAssert(PSLNextEventE(E PSLCount(E E) PSLParenProp(" +
				"PSLBefore(" + pB + " " + pB + "))))"}},
	{stmts: "assert always a until_ b; assert always a until!_ b;",
		want: []string{"PSLAssert(PSLAlways(PSLUntil(" + pB + " " + pB +
			")))", "PSLAssert(PSLAlways(PSLUntil(" + pB + " " + pB +
			")))"}},
	{stmts: "assert forall i in {0 to 3, 5}: always x(i) -> next y(i);",
		want: []string{"PSLAssert(PSLForall(PSLValueSet(E E E) " +
			"PSLAlways(PSLImplication(" + pB + " PSLNext(" + pB +
			")))))"}},
	{stmts: "assert forall i(0 to 1) in boolean: always x(i);",
		want: []string{"PSLAssert(PSLForall(E E PSLValueSet PSLAlways(" +
			pB + ")))"}},
	{stmts: "assert AG (a -> AX b); assert EF a; assert A [a U b]; " +
		"assert E [a U b];",
		want: []string{"PSLAssert(PSLOBEUnary(PSLParenProp(" +
			"PSLImplication(" + pB + " PSLOBEUnary(" + pB + ")))))",
			"PSLAssert(PSLOBEUnary(" + pB + "))",
			"PSLAssert(PSLOBEUntil(" + pB + " " + pB + "))",
			"PSLAssert(PSLOBEUntil(" + pB + " " + pB + "))"}},
	{stmts: "assert [a W b]; assert G a -> X! b; assert F a;",
		want: []string{"PSLAssert(PSLUntil(" + pB + " " + pB + "))",
			"PSLAssert(PSLAlways(PSLImplication(" + pB + " PSLNext(" +
				pB + "))))", "PSLAssert(PSLEventually(" + pB + "))"}},
	{stmts: "assert always rose(a) -> stable(b) or fell(c);",
		want: []string{"PSLAssert(PSLAlways(PSLImplication(" +
			"PSLBuiltinCall(E) " + pB + ")))"}},
	{stmts: "assert ended({a; b}); cover {a; prev(b, 2)};",
		want: []string{"PSLAssert(PSLBuiltinCall(PSLBracedSere(" +
			"PSLSereConcat(" + pB + " " + pB + "))))",
			"PSLCover(PSLBracedSere(PSLSereConcat(" + pB +
				" PSLBuiltinCall(E E))))"}},
	{stmts: "assert always a union b -> c;",
		want: []string{"PSLAssert(PSLAlways(PSLImplication(PSLUnion(E E) " +
			pB + ")))"}},
	{stmts: "assert ! a; assert not (always a);",
		want: []string{"PSLAssert(PSLLogicalProp(" + pB + "))",
			"PSLAssert(PSLLogicalProp(PSLParenProp(PSLAlways(" + pB +
				"))))"}},
	{stmts: "assert always a and always b;", hook: true,
		want: []string{"PSLAssert(PSLAlways(PSLLogicalProp(" + pB +
			" PSLAlways(" + pB + "))))"}},
	// NVC regress psl12, GHDL gna/issue679, NVC psl21 and psl22.
	{stmts: "assert always (a -> next (not a or (b before_ a)));",
		hook: true,
		want: []string{"PSLAssert(PSLAlways(PSLParenProp(PSLImplication(" +
			pB + " PSLNext(PSLParenProp(PSLLogicalProp(" + pB +
			" PSLParenProp(PSLBefore(" + pB + " " + pB + ")))))))))"}},
	{stmts: "assert always lw -> cyc and (next not(wen));", hook: true,
		want: []string{"PSLAssert(PSLAlways(PSLImplication(" + pB +
			" PSLLogicalProp(" + pB + " PSLParenProp(PSLNext(" + pB +
			"))))))"}},
	{stmts: "cover {(n1 union n2) = 1};",
		want: []string{"PSLCover(PSLBracedSere(" + pB + "))"}},
	{stmts: "cover {nondet({1, 3 to 7, 9}) = 5};",
		want: []string{"PSLCover(PSLBracedSere(" + pB + "))"}},
	{stmts: "assert always (a -> b) @ falling_edge(clk);",
		want: []string{"PSLAssert(PSLClockedProp(PSLAlways(PSLParenProp(" +
			"PSLImplication(" + pB + " " + pB + "))) E))"}},
	// SEREs.
	{stmts: "cover {a && b} | {c & d} : {e};",
		want: []string{"PSLCover(PSLSereFusion(PSLSereOr(PSLBracedSere(" +
			"PSLSereAnd(" + pB + " " + pB + ")) PSLBracedSere(" + pB +
			")) PSLBracedSere(" + pB + ")))"}},
	{stmts: "cover {{a; b} & {c}; d within {e}; f @ clk};",
		want: []string{"PSLCover(PSLBracedSere(PSLSereConcat(PSLSereConcat(" +
			"PSLSereAnd(PSLBracedSere(PSLSereConcat(" + pB + " " + pB +
			")) PSLBracedSere(" + pB + ")) PSLSereWithin(" + pB +
			" PSLBracedSere(" + pB + "))) PSLClockedProp(" + pB +
			" E))))"}},
	{stmts: "assert {a} within {b} |-> c;",
		want: []string{"PSLAssert(PSLSuffixImplication(PSLSereWithin(" +
			"PSLBracedSere(" + pB + ") PSLBracedSere(" + pB + ")) " + pB +
			"))"}},
	// Parenthesized sequences, instances with PSL actuals, hdltype.
	{decls: "sequence sp is ({s_a; s_b}); " +
		"property ph(hdltype integer i; hdltype work.p.t j, k) is {a};",
		want: []string{"PSLSequenceDecl(PSLParenProp(PSLBracedSere(" +
			"PSLSereConcat(" + pB + " " + pB + "))))",
			"PSLPropertyDecl(PSLFormalParameterList(PSLFormalParameter(" +
				"SubtypeIndication(E)) PSLFormalParameter(SubtypeIndication(" +
				"E))) PSLBracedSere(" + pB + "))"}},
	{stmts: "cover seq_b(4, {a; b}); assert p(always a, b); " +
		"assert x before (x -> next y);",
		want: []string{"PSLCover(PSLSequenceInstance(E AssociationList(" +
			pB + " PSLBracedSere(PSLSereConcat(" + pB + " " + pB + ")))))",
			"PSLAssert(PSLPropertyInstance(E AssociationList(PSLAlways(" +
				pB + ") " + pB + ")))",
			"PSLAssert(PSLBefore(" + pB + " PSLParenProp(PSLImplication(" +
				pB + " PSLNext(" + pB + ")))))"}},
	// PSL-only words used as names inside PSL.
	{stmts: "assert always x -> a; assert always prev -> stable; " +
		"assert always x(3) -> e;",
		want: []string{"PSLAssert(PSLAlways(PSLImplication(" + pB + " " +
			pB + ")))", "PSLAssert(PSLAlways(PSLImplication(" + pB + " " +
			pB + ")))", "PSLAssert(PSLAlways(PSLImplication(" + pB + " " +
			pB + ")))"}},
	// The plain-Boolean assert rule (#6).
	{stmts: "assert x = 1; lbl: assert a report \"r\" severity note;",
		want: []string{"ConcurrentAssertionStmt(E)",
			"ConcurrentAssertionStmt(Label E E E)"}},
	// Verification units.
	{unit: "vunit v1 (e(rtl)) {\n  inherit v0, work.v2;\n" +
		"  default clock is rising_edge(clk);\n  signal s : bit;\n" +
		"  property p is always a -> b;\n  endpoint ep is {a; b};\n" +
		"  a1: assert p;\n  assert a;\n  s <= a and b;\n  cover {a};\n}\n",
		want: []string{"PSLInheritSpec(E E)", "PSLDefaultClock(E)",
			"SignalDecl(IdentifierList SubtypeIndication(E))",
			"PSLPropertyDecl(PSLAlways(PSLImplication(" + pB + " " + pB +
				")))",
			"PSLEndpointDecl(PSLBracedSere(PSLSereConcat(" + pB + " " + pB +
				")))", "PSLAssert(Label " + pB + ")", "PSLAssert(" + pB + ")",
			"ConcurrentSimpleSignalAssign(Target(E) Waveform(" +
				"WaveformElement(E)))", "PSLCover(PSLBracedSere(" + pB +
				"))"}},
	{unit: "vmode m1 { assume always a; }",
		want: []string{"PSLAssume(PSLAlways(" + pB + "))"}},
	{unit: "vprop p1 { assert always a -> b; }",
		want: []string{"PSLAssert(PSLAlways(PSLImplication(" + pB + " " +
			pB + ")))"}},
}

// TestPSLShapes parses every PSL form into its tree under 2008 and 2019
// without a diagnostic, and the cases together build every PSL node
// kind.
func TestPSLShapes(t *testing.T) {
	seen := map[cst.Kind]bool{}
	for _, c := range pslCases {
		for _, std := range []token.Standard{token.Std2008,
			token.Std2019} {
			src := c.source()
			r := checkInvariants(t, src, []byte(src),
				Options{Standard: std})
			if ds := r.Diagnostics("t.vhd"); len(ds) > 0 {
				for _, d := range ds {
					t.Errorf("%v %q: %d:%d %s %s", std, src, d.Line, d.Col,
						d.Code, d.Message)
				}
			}
			if got := pslItems(r); !slices.Equal(got, c.want) {
				t.Errorf("%v %q:\n got %q\nwant %q", std, src, got, c.want)
			}
			for n := range r.Tree().Descendants() {
				seen[n.Kind()] = true
			}
		}
	}
	for _, k := range cst.Kinds() {
		name := k.String()
		if !strings.HasPrefix(name, "PSL") {
			continue
		}
		if !seen[k] {
			t.Errorf("no case builds %s", name)
		}
	}
}

// TestPSLAssertRule: a concurrent assert is a PSL directive unless its
// property is a plain Boolean (#6); before 2008 it is always VHDL, and in
// a verification unit or a `-- psl` comment it is always PSL.
func TestPSLAssertRule(t *testing.T) {
	cases := []struct {
		src  string
		opts Options
		want cst.Kind
	}{
		{pslArch + "begin assert a = b; end;",
			Options{Standard: token.Std2008}, cst.KindConcurrentAssertionStmt},
		{pslArch + "begin assert always a; end;",
			Options{Standard: token.Std2008}, cst.KindPSLAssert},
		{pslArch + "begin assert a = b; end;",
			Options{Standard: token.Std1993}, cst.KindConcurrentAssertionStmt},
		{pslArch + "begin assert f(a) report \"x\"; end;",
			Options{Standard: token.Std2019}, cst.KindConcurrentAssertionStmt},
		{"vunit v { assert a; }",
			Options{Standard: token.Std2008}, cst.KindPSLAssert},
		{pslArch + "begin\n-- psl assert a;\nend;",
			Options{Standard: token.Std1993, PSLComments: true},
			cst.KindPSLAssert},
		{pslArch + "begin\n-- psl assert always a;\nend;",
			Options{Standard: token.Std2008, PSLComments: true},
			cst.KindPSLAssert},
	}
	for _, c := range cases {
		r := checkInvariants(t, c.src, []byte(c.src), c.opts)
		for _, d := range r.Diagnostics("t.vhd") {
			t.Errorf("%q: %s %s", c.src, d.Code, d.Message)
		}
		got := cst.Kind(0)
		for n := range r.Tree().Descendants() {
			if n.Kind() == cst.KindPSLAssert ||
				n.Kind() == cst.KindConcurrentAssertionStmt {
				got = n.Kind()
			}
		}
		if got != c.want {
			t.Errorf("%q %v: got %v, want %v", c.src, c.opts.Standard, got,
				c.want)
		}
	}
}

// TestPSLModeRestored: the lexer is back in VHDL mode right after the
// `;` of a directive or declaration, so PSL words there are identifiers
// and PSL-only operators are not scanned.
func TestPSLModeRestored(t *testing.T) {
	for _, item := range []string{"cover {a};", "assert always a;",
		"assert a;", "property p is always a;", "default clock is c;",
		"fairness a;"} {
		src := pslArch + "begin " + item + " never <= next_a; end;"
		r := parseClean(t, src, Options{Standard: token.Std2008})
		var n int
		for tk := range r.Tree().DescendantTokens() {
			switch tk.Text() {
			case "never", "next_a":
				if tk.Kind() != token.Identifier {
					t.Errorf("%q: %q is %v", item, tk.Text(), tk.Kind())
				}
				n++
			}
		}
		if n != 2 {
			t.Errorf("%q: %d identifiers", item, n)
		}
	}
}

// TestPSLComments: `-- psl` lines parse as PSL under every standard
// when psl.comments is set (every token of the item flagged), and are
// plain comments otherwise.
func TestPSLComments(t *testing.T) {
	src := pslArch + "begin\n-- psl default clock is rising_edge(clk);\n" +
		"-- psl assert always a ->\n--   next b;\n" +
		"-- psl cover {a; b};\ns <= a;\nend;\n"
	for _, std := range token.Standards {
		r := parseClean(t, src, Options{Standard: std, PSLComments: true})
		got := pslItems(r)
		want := []string{"PSLDefaultClock(E)",
			"PSLAssert(PSLAlways(PSLImplication(" + pB + " PSLNext(" + pB +
				"))))", "PSLCover(PSLBracedSere(PSLSereConcat(" + pB + " " +
				pB + ")))", "ConcurrentSimpleSignalAssign(Target(E) " +
				"Waveform(WaveformElement(E)))"}
		if !slices.Equal(got, want) {
			t.Errorf("%v: got %q", std, got)
		}
		for n := range r.Tree().Descendants() {
			if !strings.HasPrefix(n.Kind().String(), "PSL") {
				continue
			}
			for tk := range n.DescendantTokens() {
				if tk.Flags()&cst.FlagPSLComment == 0 {
					t.Errorf("%v: token %q in %v not flagged", std,
						tk.Text(), n.Kind())
				}
			}
		}
		r = parseClean(t, src, Options{Standard: std})
		if got := pslItems(r); len(got) != 1 {
			t.Errorf("%v without psl.comments: %q", std, got)
		}
	}
	// A comment directive missing its `;` ends at the comment's end.
	bad := pslArch + "begin\n-- psl cover {a}\ns <= a;\nend;\n"
	r := checkInvariants(t, bad, []byte(bad), Options{
		Standard: token.Std2008, PSLComments: true})
	if got := problemCodes(r); !slices.Equal(got,
		[]string{"syntax/error@17"}) {
		t.Errorf("missing ; in comment: %q", got)
	}
}

// TestPSLNotBefore2008: outside `-- psl` comments PSL does not exist
// before 2008: the PSL-only words are names there.
func TestPSLNotBefore2008(t *testing.T) {
	src := pslArch + "signal sequence, property : bit;\nbegin\n" +
		"assume <= sequence; property <= assume;\nend;\n"
	for _, std := range []token.Standard{token.Std1993, token.Std2002} {
		r := parseClean(t, src, Options{Standard: std})
		for n := range r.Tree().Descendants() {
			if strings.HasPrefix(n.Kind().String(), "PSL") {
				t.Errorf("%v: %v", std, n.Kind())
			}
		}
	}
}

// TestPSLErrors: broken PSL reports at the right place, keeps every
// byte and resynchronizes at the `;`.
func TestPSLErrors(t *testing.T) {
	const pre = "architecture a of e is begin "
	cases := []struct {
		stmts string
		at    int // offset in stmts of the first problem
		n     int // number of problems
	}{
		{"assert always (a -> );", 19, 1},
		{"assert always a -> ;", 18, 1},
		{"cover {a; ;", 9, 4},
		{"assert next_a a;", 13, 1},
		{"assert A [a b];", 11, 1},
		{"assert always a b c; s <= a;", 15, 1},
		{"cover {a}", 9, 1},
		{"assert always a", 15, 1},
	}
	for _, c := range cases {
		src := pre + c.stmts + "\nend;"
		r := checkInvariants(t, src, []byte(src),
			Options{Standard: token.Std2008})
		if len(r.Problems) != c.n || r.Problems[0].Start-len(pre) != c.at {
			t.Errorf("%q: %q, want %d problems from %d", c.stmts,
				problemCodes(r), c.n, c.at)
		}
	}
	src := "architecture a of e is property p is always a\nbegin end;"
	r := checkInvariants(t, src, []byte(src),
		Options{Standard: token.Std2008})
	if got := problemCodes(r); !slices.Equal(got,
		[]string{"syntax/error@46"}) {
		t.Errorf("declaration without ;: %q", got)
	}
}

// pslPieces build the generated PSL inputs.
var pslPieces = []string{
	"assert ", "cover ", "assume ", "restrict ", "fairness ", "strong ",
	"always ", "never ", "eventually! ", "next ", "next! ", "next_a ",
	"next_e! ", "next_event ", "next_event_a! ", "until ", "until!_ ",
	"before ", "before_ ", "within ", "abort ", "sync_abort ", "union ",
	"forall ", "AG ", "EX ", "A ", "E ", "U ", "W ", "X ", "F ", "G ",
	"{", "}", "[", "]", "(", ")", "[*", "[+]", "[=", "[->", "->", "<->",
	"|->", "|=>", "&&", "||", "&", "|", ";", ":", "@", "!", ",",
	"to ", "inf ", "a ", "b ", "rose(", "prev(", "not ", "and ", "or ",
	"report \"r\" ", "severity ", "1 ", "'1' ", "x(3) ", "\n-- psl ",
	"\n", "property ", "sequence ", "endpoint ", "is ", "default clock ",
	"inherit ", "boolean ", "const ", "hdltype ", "`if A = \"1\" then\n",
	"`else\n", "`end if\n", "\xff",
}

// TestPSLGenerated: arbitrary PSL token soup inside an architecture and
// a verification unit round-trips, terminates, is deterministic and
// reports only real positions, under every standard and setting.
func TestPSLGenerated(t *testing.T) {
	r := &env.SplitMix64{State: 31}
	for i := range 3000 {
		var b strings.Builder
		for n := r.Uint64() % 24; n > 0; n-- {
			b.WriteString(pslPieces[r.Uint64()%uint64(len(pslPieces))])
		}
		body := b.String()
		src := pslArch + "begin\n" + body + "\nend;\n"
		if i%2 == 1 {
			src = "vunit v {\n" + body + "\n}\n"
		}
		checkInvariants(t, src, []byte(src), allOpts[i%len(allOpts)])
	}
}

// TestPSLDeep: deeply nested PSL neither overflows the stack nor runs
// out of budget on linear input.
func TestPSLDeep(t *testing.T) {
	for _, body := range []string{
		strings.Repeat("always ", 20000) + "a",
		strings.Repeat("next ", 20000) + "a",
		strings.Repeat("(", 5000) + "a -> b" + strings.Repeat(")", 5000),
		strings.Repeat("{", 5000) + "a" + strings.Repeat("}", 5000),
		strings.Repeat("a -> ", 20000) + "a",
		strings.Repeat("a until ", 20000) + "a",
		"{" + strings.Repeat("a; ", 50000) + "a}",
		strings.Repeat("! ", 20000) + "a",
	} {
		src := pslArch + "begin assert " + body + "; end;"
		r := checkInvariants(t, src[:min(len(src), 60)], []byte(src),
			Options{Standard: token.Std2008})
		for _, p := range r.Problems {
			if p.Code == diag.SyntaxError &&
				strings.Contains(p.Args[0], "budget") {
				t.Errorf("%q...: budget exhausted", body[:20])
			}
		}
	}
}

// TestPSLExprStop: the hook is false outside PSL and true exactly
// before an FL and/or whose right operand is PSL-only.
func TestPSLExprStop(t *testing.T) {
	cases := []struct {
		text string
		mode lexer.Mode
		want bool
	}{
		{"and always b", lexer.PSL, true},
		{"or next! b", lexer.PSL, true},
		{"and {b}", lexer.PSL, true},
		{"and (b -> c)", lexer.PSL, true},
		{"and not always b", lexer.PSL, true},
		{"and b", lexer.PSL, false},
		{"and (b or c)", lexer.PSL, false},
		{"and x", lexer.PSL, false},
		{"and always b", lexer.VHDL, false},
		{"+ always", lexer.PSL, false},
	}
	for _, c := range cases {
		opts := Options{Standard: token.Std2008}
		p := newParser([]byte(c.text), opts, lexerOptions(opts))
		p.setMode(c.mode)
		if got := p.pslExprStop(); got != c.want {
			t.Errorf("%q in %v: %v", c.text, c.mode, got)
		}
	}
}
