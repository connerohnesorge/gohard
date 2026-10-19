package parser

import (
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/token"
)

// Statement tests (seq.go, conc.go). A statement sample is wrapped into
// a design unit; the tests look at the items of the first statement
// list of the wrapper.

const (
	seqPre  = "entity e is end;\narchitecture a of e is begin\np: process\nbegin\n"
	seqPost = "\nend process;\nend;\n"
	concPre = "entity e is end;\narchitecture a of e is\nbegin\n"
	conPost = "\nend;\n"
)

// stmtList returns the first node of kind list in the tree.
func stmtList(r *Result, list cst.Kind) cst.Node {
	for n := range r.Tree().Descendants() {
		if n.Kind() == list {
			return n
		}
	}
	return cst.Node{}
}

// stmtItems returns the node kinds of the items of the first list.
func stmtItems(r *Result, list cst.Kind) []string {
	var out []string
	for c := range stmtList(r, list).Nodes() {
		out = append(out, c.Kind().String())
	}
	return out
}

// stmtErrors returns the problems other than syntax/standard (and the
// placeholders of builders that are not finished yet).
func stmtErrors(r *Result) []string {
	var out []string
	for _, p := range r.Problems {
		if p.Code == diag.SyntaxStandard ||
			(len(p.Args) > 0 && strings.Contains(p.Args[0], "not parsed yet")) {
			continue
		}
		out = append(out, string(p.Code)+" "+strings.Join(p.Args, " "))
	}
	return out
}

// stmtGates returns the constructs of the syntax/standard problems.
func stmtGates(r *Result) []string {
	var out []string
	for _, p := range r.Problems {
		if p.Code == diag.SyntaxStandard {
			out = append(out, p.Args[0])
		}
	}
	return out
}

// hasKind reports whether the tree has a node of kind k.
func hasKind(r *Result, k cst.Kind) bool {
	for n := range r.Tree().Descendants() {
		if n.Kind() == k {
			return true
		}
	}
	return false
}

type stmtCase struct {
	src  string
	want []string // item kinds
	has  []cst.Kind
	gate string // the construct gated before first (rules.csv text)
	// first is the first standard of the statement (0: 1993).
	first token.Standard
}

var seqCases = []stmtCase{
	{src: "wait;", want: []string{"WaitStmt"}},
	{src: "wait on a, b until c = '1' for 10 ns;", want: []string{"WaitStmt"},
		has: []cst.Kind{cst.KindSensitivityClause, cst.KindSensitivityList,
			cst.KindConditionClause, cst.KindTimeoutClause}},
	{src: "wait until rising_edge(clk);", want: []string{"WaitStmt"}},
	{src: "w: wait for 1 ns;", want: []string{"WaitStmt"},
		has: []cst.Kind{cst.KindLabel}},
	{src: `assert a = b report "bad" severity error;`,
		want: []string{"AssertStmt"}},
	{src: `report "hello";`, want: []string{"ReportStmt"}},
	{src: `report "x" & integer'image(i) severity note;`,
		want: []string{"ReportStmt"}},
	{src: "s <= '1';", want: []string{"SignalAssignStmt"},
		has: []cst.Kind{cst.KindTarget, cst.KindWaveform}},
	{src: "s(3 downto 0) <= transport x after 1 ns, y after 2 ns;",
		want: []string{"SignalAssignStmt"},
		has:  []cst.Kind{cst.KindDelayMechanism, cst.KindWaveformElement}},
	{src: "r.f <= reject 2 ns inertial x;",
		want: []string{"SignalAssignStmt"},
		has:  []cst.Kind{cst.KindDelayMechanism}},
	{src: "s <= inertial null after 3 ns;",
		want: []string{"SignalAssignStmt"}},
	{src: "(a, b) <= v;", want: []string{"SignalAssignStmt"},
		has: []cst.Kind{cst.KindAggregate}},
	{src: "s <= a when c else b when d else unaffected;",
		want:  []string{"ConditionalSignalAssignStmt"},
		has:   []cst.Kind{cst.KindConditionalWaveforms, cst.KindWhenBranch},
		gate:  "conditional signal assignment in process",
		first: token.Std2008},
	{src: "with sel select s <= a when \"00\", b when others;",
		want:  []string{"SelectedSignalAssignStmt"},
		has:   []cst.Kind{cst.KindSelectedWaveforms, cst.KindSelectBranch},
		gate:  "selected signal assignment in process",
		first: token.Std2008},
	{src: "with sel select? s <= transport a when \"0-\", b when others;",
		want:  []string{"SelectedSignalAssignStmt"},
		gate:  "select? statement",
		first: token.Std2008},
	{src: "s <= force in '1';", want: []string{"ForceAssignStmt"},
		first: token.Std2008},
	{src: "s <= force a when c else b;", want: []string{"ForceAssignStmt"},
		has: []cst.Kind{cst.KindConditionalExpressions}, first: token.Std2008},
	{src: "s <= release out;", want: []string{"ReleaseAssignStmt"},
		first: token.Std2008},
	{src: "v := a + 1;", want: []string{"VariableAssignStmt"}},
	{src: "v.f(i) := (others => '0');", want: []string{"VariableAssignStmt"}},
	{src: "v := a when c else b;",
		want:  []string{"ConditionalVariableAssignStmt"},
		has:   []cst.Kind{cst.KindConditionalExpressions},
		gate:  "conditional variable assignment",
		first: token.Std2008},
	{src: "with s select v := 1 when '0', 2 when others;",
		want:  []string{"SelectedVariableAssignStmt"},
		has:   []cst.Kind{cst.KindSelectedExpressions},
		gate:  "selected variable assignment",
		first: token.Std2008},
	{src: "proc;", want: []string{"ProcedureCallStmt"}},
	{src: "work.pkg.proc(a, b => c);", want: []string{"ProcedureCallStmt"}},
	{src: "display generic map (t'subtype)(y);",
		want: []string{"ProcedureCallStmt"}, has: []cst.Kind{
			cst.KindCallGenericMap}, gate: "generic map on call",
		first: token.Std2019},
	{src: "p parameter map (a => 1);", want: []string{"ProcedureCallStmt"},
		has:  []cst.Kind{cst.KindParameterMapAspect},
		gate: "parameter map aspect", first: token.Std2019},
	{src: "l: proc(x);", want: []string{"ProcedureCallStmt"},
		has: []cst.Kind{cst.KindLabel}},
	{src: "if a then null; elsif b then x := 1; elsif c then else " +
		"y := 2; end if;", want: []string{"IfStmt"},
		has: []cst.Kind{cst.KindElsifBranch, cst.KindElseBranch,
			cst.KindNullStmt}},
	{src: "l1: if a then end if l1;", want: []string{"IfStmt"}},
	{src: "case s is when \"00\" | \"01\" => null; when 2 to 5 => " +
		"null; when others => x := 1; end case;",
		want: []string{"CaseStmt"},
		has: []cst.Kind{cst.KindCaseAlternative, cst.KindChoices,
			cst.KindOthersChoice, cst.KindRangeExpr}},
	{src: "c: case? s is when \"1-\" => null; end case? c;",
		want: []string{"CaseStmt"}, gate: "case? statement",
		first: token.Std2008},
	{src: "loop exit; end loop;", want: []string{"LoopStmt"},
		has: []cst.Kind{cst.KindExitStmt}},
	{src: "l: while i < 10 loop i := i + 1; next l when i = 3; end loop l;",
		want: []string{"LoopStmt"},
		has:  []cst.Kind{cst.KindWhileScheme, cst.KindNextStmt}},
	{src: "for i in 0 to 7 loop exit when i = n; end loop;",
		want: []string{"LoopStmt"},
		has:  []cst.Kind{cst.KindForScheme, cst.KindParameterSpecification}},
	{src: "for i in v'range loop next; end loop;",
		want: []string{"LoopStmt"}},
	{src: "return;", want: []string{"ReturnStmt"}},
	{src: "return a + b;", want: []string{"ReturnStmt"}},
	{src: "return 0 when c else 1;",
		want:  []string{"ConditionalReturnStmt"},
		has:   []cst.Kind{cst.KindConditionalExpressions},
		gate:  "return x when c",
		first: token.Std2019},
	{src: "return when c;", want: []string{"ConditionalReturnStmt"},
		gate: "return x when c", first: token.Std2019},
	{src: "null;", want: []string{"NullStmt"}},
	{src: "b: block is variable v : integer; begin v := 1; end block b;",
		want: []string{"SequentialBlockStmt"},
		gate: "sequential block statement", first: token.Std2019},
	{src: "block begin end block;", want: []string{"SequentialBlockStmt"},
		gate: "sequential block statement", first: token.Std2019},
	{src: "wait; null; s <= a; v := b; p;",
		want: []string{"WaitStmt", "NullStmt", "SignalAssignStmt",
			"VariableAssignStmt", "ProcedureCallStmt"}},
}

// checkStmtCases parses each case under every standard and checks the
// items, the nodes, that nothing but placeholders and gates is
// reported, that the gate fires exactly below the first standard, and
// that the tree does not depend on the standard.
func checkStmtCases(t *testing.T, cases []stmtCase, pre, post string,
	list cst.Kind,
) {
	for _, c := range cases {
		src := pre + c.src + post
		var dumps []string
		for _, std := range token.Standards {
			r := checkInvariants(t, c.src, []byte(src),
				Options{Standard: std})
			dumps = append(dumps, cst.Dump(r.Tree()))
			if got := stmtItems(r, list); !slices.Equal(got, c.want) {
				t.Errorf("%q (%v): items %v, want %v", c.src, std, got,
					c.want)
			}
			for _, k := range c.has {
				if !hasKind(r, k) {
					t.Errorf("%q (%v): no %v node", c.src, std, k)
				}
			}
			if errs := stmtErrors(r); len(errs) > 0 {
				t.Errorf("%q (%v): problems %v", c.src, std, errs)
			}
			gates := stmtGates(r)
			if std >= c.first && len(gates) > 0 {
				t.Errorf("%q (%v): unexpected gates %v", c.src, std, gates)
			}
			if std < c.first && len(gates) == 0 {
				t.Errorf("%q (%v): no gate", c.src, std)
			}
			if c.gate != "" && std < c.first &&
				!slices.Contains(gates, c.gate) {
				t.Errorf("%q (%v): gates %v, want %q", c.src, std, gates,
					c.gate)
			}
		}
		for _, d := range dumps[1:] {
			if d != dumps[0] {
				t.Errorf("%q: the tree depends on the standard", c.src)
				break
			}
		}
	}
}

func TestSequentialStatements(t *testing.T) {
	checkStmtCases(t, seqCases, seqPre, seqPost,
		cst.KindSequenceOfStatements)
}

// TestStatementSpans: every item node of a one-statement sample covers
// exactly the sample's text, and k copies of a sample give k items
// whose green nodes are shared (content-derived identity).
func TestStatementSpans(t *testing.T) {
	check := func(cases []stmtCase, pre, post string, list cst.Kind) {
		for _, c := range cases {
			if len(c.want) != 1 {
				continue
			}
			for _, k := range []int{1, 3} {
				body := strings.Repeat(c.src+"\n", k)
				src := pre + body + post
				r := Parse([]byte(src), Options{Standard: token.Std2019})
				var items []cst.Node
				for n := range stmtList(r, list).Nodes() {
					items = append(items, n)
				}
				if len(items) != k {
					t.Errorf("%q x%d: %d items", c.src, k, len(items))
					continue
				}
				for _, n := range items {
					sp := n.Span()
					if got := src[sp.Start:sp.End]; got != c.src {
						t.Errorf("%q: item text %q", c.src, got)
					}
				}
			}
		}
	}
	check(seqCases, seqPre, seqPost, cst.KindSequenceOfStatements)
	check(concCases, concPre, conPost, cst.KindStatementPart)
}

// TestStatementPrefixes: every prefix of every sample (cut at each
// byte) parses losslessly and deterministically with problems inside
// the input, and the statement after a broken one is still found.
func TestStatementPrefixes(t *testing.T) {
	check := func(cases []stmtCase, pre, post string, list cst.Kind,
		next string, nextKind string,
	) {
		for _, c := range cases {
			for i := range len(c.src) {
				cut := c.src[:i]
				src := pre + cut + post
				checkInvariants(t, cut, []byte(src),
					Options{Standard: token.Std2019})
				// A broken statement then a valid one: the valid one
				// is recovered whenever the cut ends a token at `;`.
				if !strings.HasSuffix(cut, ";") || compound[c.want[0]] {
					continue
				}
				src = pre + cut + "\n" + next + post
				r := checkInvariants(t, cut, []byte(src),
					Options{Standard: token.Std2019})
				items := stmtItems(r, list)
				if len(items) == 0 || items[len(items)-1] != nextKind {
					t.Errorf("%q then %q: items %v", cut, next, items)
				}
			}
		}
	}
	check(seqCases, seqPre, seqPost, cst.KindSequenceOfStatements,
		"null;", "NullStmt")
	check(concCases, concPre, conPost, cst.KindStatementPart,
		"zz <= '1';", "ConcurrentSimpleSignalAssign")
}

// compound are the statements with nested statement lists: a cut
// inside one leaves it open, so the next statement joins its body.
var compound = map[string]bool{"IfStmt": true, "CaseStmt": true,
	"LoopStmt": true, "SequentialBlockStmt": true, "ProcessStmt": true,
	"BlockStmt": true, "ForGenerateStmt": true, "IfGenerateStmt": true,
	"CaseGenerateStmt": true}

// TestStatementsGenerated: random sequences of statement samples,
// fragments and keywords, possibly cut, keep the parser invariants.
func TestStatementsGenerated(t *testing.T) {
	var frags []string
	for _, c := range seqCases {
		frags = append(frags, c.src)
	}
	for _, c := range concCases {
		frags = append(frags, c.src)
	}
	frags = append(frags, "end", ";", "(", ")", "when", "else", "<=",
		":=", "begin", "generate", "loop", "is", "then", "=>", "`if A = "+
			"\"1\" then\n", "`else\n", "`end if\n", "-- psl ", "\n", "\xff",
		"process", "if", "case", "with", "select", "?", "assert",
		"postponed", "l:", "{", "}", "@", "->")
	rng := &env.SplitMix64{State: 31}
	for i := range 400 {
		var b strings.Builder
		n := 1 + int(rng.Uint64()%12)
		for range n {
			b.WriteString(frags[rng.Uint64()%uint64(len(frags))])
			b.WriteByte(' ')
		}
		body := b.String()
		if rng.Uint64()%3 == 0 {
			body = body[:rng.Uint64()%uint64(len(body)+1)]
		}
		for _, w := range [][2]string{{seqPre, seqPost},
			{concPre, conPost}, {"", ""}} {
			for _, opts := range allOpts {
				checkInvariants(t, "generated "+itoa(i),
					[]byte(w[0]+body+w[1]), opts)
			}
		}
	}
}

// TestSequentialRecovery: broken statements report an error at the
// expected place and the parse goes on with the next statement.
func TestSequentialRecovery(t *testing.T) {
	cases := []struct {
		src  string
		want []string
		at   string // the text the first error is at (or right after)
	}{
		{"x := a b := 2; null;", []string{"VariableAssignStmt",
			"NullStmt"}, "b"},
		{"if a then null; null;", []string{"IfStmt"}, ""},
		{"wait until;", []string{"WaitStmt"}, ";"},
		{"s <= ; null;", []string{"SignalAssignStmt", "NullStmt"}, ";"},
		{"case s is null; when others => null; end case;",
			[]string{"CaseStmt"}, "null"},
		{"x y; null;", []string{"ProcedureCallStmt", "NullStmt"}, "y"},
	}
	for _, c := range cases {
		src := seqPre + c.src + seqPost
		r := checkInvariants(t, c.src, []byte(src),
			Options{Standard: token.Std2019})
		got := stmtItems(r, cst.KindSequenceOfStatements)
		if !slices.Equal(got, c.want) {
			t.Errorf("%q: items %v, want %v", c.src, got, c.want)
		}
		errs := stmtErrors(r)
		if len(errs) == 0 {
			t.Errorf("%q: no error", c.src)
			continue
		}
		if c.at == "" {
			continue
		}
		var first Problem
		for _, p := range r.Problems {
			if p.Code == diag.SyntaxError {
				first = p
				break
			}
		}
		base := len(seqPre)
		at := strings.Index(c.src, c.at)
		// errorExpected reports right after the previous token.
		prev := strings.TrimRight(c.src[:at], " ")
		if first.Start != base+at && first.Start != base+len(prev) {
			t.Errorf("%q: first error at %d, want %d or %d (%v)", c.src,
				first.Start-base, at, len(prev), first.Args)
		}
	}
}

// TestForceAsIdentifier: before 2008 `force` and `release` lex as
// identifiers; a signal of that name is an ordinary target or value,
// while the keyword use gets syntax/standard and the 2008 node.
func TestForceAsIdentifier(t *testing.T) {
	cases := []struct {
		src  string
		want string
		gate bool
	}{
		{"s <= force;", "SignalAssignStmt", false},
		{"s <= release;", "SignalAssignStmt", false},
		{"s <= force and b;", "SignalAssignStmt", false},
		{"s <= force x;", "ForceAssignStmt", true},
		{"s <= release in;", "ReleaseAssignStmt", true},
		{"s <= force '1';", "ForceAssignStmt", true},
		{"s <= force'image(x);", "SignalAssignStmt", false},
	}
	for _, c := range cases {
		r := checkInvariants(t, c.src, []byte(seqPre+c.src+seqPost),
			Options{Standard: token.Std1993})
		got := stmtItems(r, cst.KindSequenceOfStatements)
		if !slices.Equal(got, []string{c.want}) {
			t.Errorf("%q: %v, want %s", c.src, got, c.want)
		}
		if g := len(stmtGates(r)) > 0; g != c.gate {
			t.Errorf("%q: gates %v", c.src, stmtGates(r))
		}
		if errs := stmtErrors(r); len(errs) > 0 {
			t.Errorf("%q: %v", c.src, errs)
		}
	}
}
