package parser

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/condanal"
	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/token"
)

// checkInvariants parses src under opts and checks the #31 invariants:
// the tree's text is the input, every problem lies inside the input,
// the parse is deterministic, and diagnostics use known codes.
func checkInvariants(t testing.TB, name string, src []byte,
	opts Options,
) *Result {
	t.Helper()
	r := Parse(src, opts)
	if got := r.Root.String(); got != string(src) {
		t.Fatalf("%s: round trip failed (%d bytes in, %d out)", name,
			len(src), len(got))
	}
	if r.Tree().Kind() != cst.KindDesignFile {
		t.Fatalf("%s: root is %v", name, r.Tree().Kind())
	}
	for _, p := range r.Problems {
		if p.Start < 0 || p.End < p.Start || p.End > len(src) {
			t.Fatalf("%s: problem %v outside the input", name, p)
		}
		if _, ok := p.Code.Info(); !ok {
			t.Fatalf("%s: unknown code %s", name, p.Code)
		}
	}
	r2 := Parse(src, opts)
	if r2.Root.ID() != r.Root.ID() || !cst.Equal(r.Root, r2.Root) ||
		!slices.EqualFunc(r.Problems, r2.Problems, sameProblem) {
		t.Fatalf("%s: parse is not deterministic", name)
	}
	return r
}

func sameProblem(a, b Problem) bool {
	return a.Start == b.Start && a.End == b.End && a.Code == b.Code &&
		slices.Equal(a.Args, b.Args) && a.Inactive == b.Inactive
}

var allOpts = func() []Options {
	var out []Options
	for _, std := range token.Standards {
		for _, comments := range []bool{false, true} {
			out = append(out, Options{Standard: std, PSLComments: comments,
				PSLStrictKeywords: comments,
				Defines:           []condanal.Define{{Name: "A", Value: "1"}}})
		}
	}
	return out
}()

// pieces build the generated inputs: VHDL fragments, PSL, directives,
// broken constructs and invalid UTF-8 (edge values first).
var pieces = []string{
	"entity e is ", "end;", "end entity e;", "architecture a of e is ",
	"begin ", "package p is ", "package body p is ", "end package body;",
	"configuration c of e is ", "for a ", "for all : c use entity work.e;",
	"end for;", "context c is ", "library ieee; ", "use ieee.std.all; ",
	"context work.c; ", "vunit v (e) { ", "assert always a -> b; ", "}",
	"generic (n : natural := 1); ", "port (a : in bit); ", "signal s : bit;",
	"process begin wait; end process;", "s <= a;", "(", ")", ";", ",",
	"`if A = \"1\" then\n", "`elsif A = \"2\" then\n", "`else\n",
	"`end if\n", "`warning \"w\"\n", "-- psl assert always a;\n",
	"-- c\n", "/* b */", "\n", " ", "\xff", "é", "'", "\"", "\\", "x\"F\"",
	"property", "inherit", "view", "private", "context", "%", "{", "!",
	"package q is new p generic map (x => 1);",
}

func genInput(r *env.SplitMix64) []byte {
	var b []byte
	for range r.Uint64() % 24 {
		b = append(b, pieces[r.Uint64()%uint64(len(pieces))]...)
	}
	return b
}

func TestRoundTripGenerated(t *testing.T) {
	r := &env.SplitMix64{State: 7}
	var inputs [][]byte
	for _, p := range pieces {
		inputs = append(inputs, []byte(p))
	}
	for range 1500 {
		inputs = append(inputs, genInput(r))
	}
	for range 300 {
		b := make([]byte, r.Uint64()%64)
		for i := range b {
			b[i] = byte(r.Uint64())
		}
		inputs = append(inputs, b)
	}
	for i, src := range inputs {
		opts := allOpts[i%len(allOpts)]
		checkInvariants(t, strconv.Quote(string(src)), src, opts)
	}
}

func corpusFiles(t testing.TB) []string {
	files, _ := filepath.Glob("../../testdata/corpus/*/*.vhd")
	if len(files) == 0 {
		t.Fatal("no corpus files under testdata/corpus")
	}
	return files
}

func TestRoundTripCorpus(t *testing.T) {
	for _, f := range corpusFiles(t) {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, opts := range allOpts {
			checkInvariants(t, f, src, opts)
		}
	}
}

func parseClean(t *testing.T, src string, opts Options) *Result {
	t.Helper()
	r := checkInvariants(t, src, []byte(src), opts)
	for _, d := range r.Diagnostics("t.vhd") {
		t.Errorf("%q: unexpected %s:%d:%d %s %s", src, d.Path, d.Line,
			d.Col, d.Code, d.Message)
	}
	return r
}

// kindsPath returns the node kinds of the tree in preorder.
func kindsPath(r *Result) []string {
	var out []string
	for n := range r.Tree().Descendants() {
		out = append(out, n.Kind().String())
	}
	return out
}

// TestSkeleton parses every design-unit form the skeleton owns without
// any diagnostic.
func TestSkeleton(t *testing.T) {
	cases := []struct {
		src  string
		std  token.Standard
		want []string
	}{
		{"entity e is end;", 0, []string{"DesignFile", "DesignUnit",
			"ContextClause", "EntityDecl", "DeclarativePart",
			"EndClause"}},
		{"library ieee, work; use ieee.std_logic_1164.all, work.p.\"+\";\n" +
			"entity e is begin end entity e;", 0, []string{"DesignFile",
			"DesignUnit", "ContextClause", "LibraryClause", "SimpleName",
			"SimpleName", "UseClause", "SelectedName", "SelectedName",
			"SimpleName", "SelectedName", "SelectedName", "SimpleName",
			"EntityDecl", "DeclarativePart", "StatementPart",
			"EndClause"}},
		{"architecture rtl of work.e is begin end architecture rtl;", 0,
			[]string{"DesignFile", "DesignUnit", "ContextClause",
				"ArchitectureBody", "SelectedName", "SimpleName",
				"DeclarativePart", "StatementPart", "EndClause"}},
		{"package p is end package p; package body p is end package " +
			"body p;", 0, []string{"DesignFile", "DesignUnit",
			"ContextClause", "PackageDecl", "DeclarativePart", "EndClause",
			"DesignUnit", "ContextClause", "PackageBody",
			"DeclarativePart", "EndClause"}},
		{"package q is new work.p;", 0, []string{"DesignFile",
			"DesignUnit", "ContextClause", "PackageInstantiation",
			"SelectedName", "SimpleName"}},
		{"context c is library l; use l.p.all; context l.d; end context c;",
			0, []string{"DesignFile", "DesignUnit", "ContextClause",
				"ContextDecl", "ContextClause", "LibraryClause",
				"SimpleName", "UseClause", "SelectedName", "SelectedName",
				"SimpleName", "ContextReference", "SelectedName",
				"SimpleName", "EndClause"}},
		{"configuration c of e is use work.p.all; for rtl for u1, u2 : " +
			"comp use entity work.e(rtl); end for; for all : c2 use open; " +
			"end for; for g for inner end for; end for; end for; end;", 0,
			[]string{"DesignFile", "DesignUnit", "ContextClause",
				"ConfigurationDecl", "SimpleName", "DeclarativePart",
				"UseClause", "SelectedName", "SelectedName", "SimpleName",
				"BlockConfiguration", "SimpleName",
				"ComponentConfiguration", "ComponentSpecification",
				"InstantiationList", "SimpleName", "BindingIndication",
				"EntityAspect", "SelectedName", "SimpleName", "EndClause",
				"ComponentConfiguration", "ComponentSpecification",
				"InstantiationList", "SimpleName", "BindingIndication",
				"EntityAspect", "EndClause", "BlockConfiguration",
				"SimpleName", "BlockConfiguration", "SimpleName",
				"EndClause", "EndClause", "EndClause", "EndClause"}},
		{"vunit v (e(rtl)) { }  vmode m { } vprop p { }", 0,
			[]string{"DesignFile", "DesignUnit", "ContextClause", "PSLVunit",
				"PSLHierarchicalName", "SimpleName", "DesignUnit",
				"ContextClause", "PSLVmode", "DesignUnit", "ContextClause",
				"PSLVprop"}},
	}
	for _, c := range cases {
		std := c.std
		if std == 0 {
			std = token.Std2019
		}
		r := parseClean(t, c.src, Options{Standard: std})
		if got := kindsPath(r); !slices.Equal(got, c.want) {
			t.Errorf("%q:\n got %v\nwant %v", c.src, got, c.want)
		}
	}
}

func problemCodes(r *Result) []string {
	var out []string
	for _, d := range r.Diagnostics("t.vhd") {
		out = append(out, string(d.Code)+"@"+strconv.Itoa(d.Col))
	}
	return out
}

// TestPSLOnlyWordsAsIdentifiers: each PSL-only 2008 word used as an
// identifier outside PSL gives the portability hint by default and the
// error with psl.strict_keywords; before 2008 neither (#31, #11 entry 3).
func TestPSLOnlyWordsAsIdentifiers(t *testing.T) {
	n := 0
	for _, kw := range token.Keywords() {
		if kw.Class != token.ClassPSL2008 {
			continue
		}
		n++
		src := "entity " + kw.Word + " is end;"
		for _, std := range token.Standards {
			for _, strict := range []bool{false, true} {
				r := checkInvariants(t, src, []byte(src),
					Options{Standard: std, PSLStrictKeywords: strict})
				var want []string
				switch {
				case std >= token.Std2008 && strict:
					want = []string{"syntax/psl-keyword@8"}
				case std >= token.Std2008:
					want = []string{"syntax/psl-identifier@8"}
				}
				if got := problemCodes(r); !slices.Equal(got, want) {
					t.Errorf("%q %v strict=%v: %v, want %v", src, std,
						strict, got, want)
				}
				ds := r.Diagnostics("t.vhd")
				if len(ds) > 0 && !strings.Contains(ds[0].Message, kw.Word) {
					t.Errorf("message %q", ds[0].Message)
				}
				e, _ := cst.As[cst.EntityDecl](r.Tree().Element(0).Node.
					Element(1).Node)
				if e.Name().Kind() != token.Identifier {
					t.Errorf("%q: name kind %v", src, e.Name().Kind())
				}
			}
		}
	}
	if n != 11 {
		t.Errorf("%d PSL-only 2008 words, want 11", n)
	}
}

// TestEverywhereReserved2008: context, cover, default, force,
// parameter and release are keywords from 2008 on in VHDL code, and
// identifiers before.
func TestEverywhereReserved2008(t *testing.T) {
	for _, w := range []string{"context", "cover", "default", "force",
		"parameter", "release"} {
		src := "entity " + w + " is end;"
		for _, std := range token.Standards {
			r := checkInvariants(t, src, []byte(src), Options{Standard: std})
			got := problemCodes(r)
			if std < token.Std2008 && len(got) != 0 {
				t.Errorf("%s under %v: %v", w, std, got)
			}
			if std >= token.Std2008 && (len(got) == 0 ||
				!strings.HasPrefix(got[0], "syntax/error")) {
				t.Errorf("%s under %v: %v, want syntax/error", w, std, got)
			}
		}
	}
}

// TestKeywordGate: a later standard's keyword where the grammar needs it
// is treated as the keyword with syntax/standard; the tree does not
// depend on the standard (#7).
func TestKeywordGate(t *testing.T) {
	src := "context c is end context c;"
	var dumps []string
	for _, std := range token.Standards {
		r := checkInvariants(t, src, []byte(src), Options{Standard: std})
		got := problemCodes(r)
		var want []string
		if std < token.Std2008 {
			want = []string{"syntax/standard@1", "syntax/standard@18"}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%v: %v, want %v", std, got, want)
		}
		dumps = append(dumps, cst.Dump(r.Tree()))
	}
	for _, d := range dumps[1:] {
		if d != dumps[0] {
			t.Errorf("tree depends on the standard:\n%s\nvs\n%s", d,
				dumps[0])
		}
	}
	r := Parse([]byte(src), Options{Standard: token.Std1993})
	ds := r.Diagnostics("t.vhd")
	if want := "the reserved word context requires VHDL-2008 or later " +
		"(standard is 1993)"; ds[0].Message != want {
		t.Errorf("message %q", ds[0].Message)
	}
}

func TestInactiveBranches(t *testing.T) {
	src := "library ieee;\n`if A = \"1\" then\nuse ieee.a.all;\n`else\n" +
		"use ieee.b.all;\nlibrary\n`end if\nentity e is end;\n" +
		"`if A = \"2\" then\nentity f is end;\n`end if\n"
	r := checkInvariants(t, src, []byte(src), Options{
		Standard: token.Std2019, Defines: []condanal.Define{{Name: "A", Value: "1"}}})
	got := problemCodes(r)
	// The broken `library` in the inactive branch gives faded hints.
	if want := []string{"syntax/inactive@8", "syntax/inactive@8"}; !slices.Equal(
		got,
		want,
	) {
		t.Errorf("problems %v, want %v", got, want)
	}
	var inactive []string
	for n := range r.Tree().Descendants() {
		if n.Kind() == cst.KindInactiveBranch {
			var kinds []string
			for c := range n.Nodes() {
				kinds = append(kinds, c.Kind().String())
			}
			inactive = append(inactive, strings.Join(kinds, ","))
			for tk := range n.DescendantTokens() {
				if !tk.Inactive() {
					t.Errorf("token %q not flagged", tk.Text())
				}
			}
		}
	}
	want := []string{"UseClause,LibraryClause", "DesignUnit"}
	if !slices.Equal(inactive, want) {
		t.Errorf("inactive branches %v, want %v", inactive, want)
	}
}

// TestModeViews: the mode_views corpus package parses its directives
// with the built-in identifier, the user identifier from its gohard.yaml
// and the inactive branches marked in the dump; the `error in the
// inactive branch stays silent.
func TestModeViews(t *testing.T) {
	cfgSrc, err := os.ReadFile("../../testdata/corpus/mode_views/gohard.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, ds := config.Parse(config.FileName, cfgSrc)
	if len(ds) > 0 {
		t.Fatalf("config: %v", ds)
	}
	opts := FromConfig(c, "0.0.0-dev")
	if len(opts.Defines) != 1 || opts.Defines[0].Name != "MV_PROFILE" {
		t.Fatalf("options %+v", opts)
	}
	opts.Standard = token.Std2019 // the design's standard (corpus.yaml)
	src, err := os.ReadFile(
		"../../testdata/corpus/mode_views/mode_views_pkg.vhd")
	if err != nil {
		t.Fatal(err)
	}
	r := checkInvariants(t, "mode_views_pkg.vhd", src, opts)
	if len(r.Inactive) != 3 {
		t.Errorf("inactive regions %v, want 3", r.Inactive)
	}
	for _, p := range r.Problems {
		switch p.Code {
		case diag.SyntaxUndefinedCondition, diag.SyntaxDirectiveError,
			diag.SyntaxStandard:
			t.Errorf("unexpected %s %v", p.Code, p.Args)
		}
	}
	dump := cst.Dump(r.Tree())
	// Two of the three inactive branches hold tokens; the third holds
	// only the `error directive, which is trivia.
	if n := strings.Count(dump, "InactiveBranch"); n != 2 {
		t.Errorf("%d InactiveBranch nodes in the dump, want 2", n)
	}
	if !strings.Contains(dump, `"\"old\"" inactive`) {
		t.Errorf("the inactive constant is not marked")
	}
	// Under 2008 the directives are gated and the built-in branch flips.
	opts.Standard = token.Std2008
	r = checkInvariants(t, "mode_views_pkg.vhd", src, opts)
	gated := 0
	for _, p := range r.Problems {
		if p.Code == diag.SyntaxStandard &&
			p.Args[0] == condanal.Construct {
			gated++
		}
	}
	if gated != 8 {
		t.Errorf("%d gated directives, want 8", gated)
	}
}

// TestBudget: running out of budget ends the parse with a diagnostic
// and the tree still holds every byte.
func TestBudget(t *testing.T) {
	src := []byte(strings.Repeat("library a; use a.b.all; ", 50) +
		"entity e is end;")
	r := checkInvariants(t, "budget", src, Options{Budget: 40})
	found := false
	for _, p := range r.Problems {
		if p.Code == diag.SyntaxError &&
			strings.Contains(p.Args[0], "step budget") {
			found = true
		}
	}
	if !found {
		t.Errorf("no budget diagnostic: %v", r.Problems)
	}
}

// TestDeepNesting: nesting deeper than maxDepth gives one diagnostic,
// keeps every byte and does not exhaust the stack.
func TestDeepNesting(t *testing.T) {
	src := "configuration c of e is " +
		strings.Repeat("for x ", 5000) + strings.Repeat("end for; ", 5000) +
		"end;"
	r := checkInvariants(t, "deep", []byte(src), Options{})
	n := 0
	for _, p := range r.Problems {
		if strings.Contains(p.Args[0], "nested too deeply") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d nesting diagnostics, want 1", n)
	}
}

// TestDefaultBudgetLinear: the default budget is never hit on the
// corpus, and steps grow linearly with input size.
func TestDefaultBudgetLinear(t *testing.T) {
	for _, f := range corpusFiles(t) {
		src, _ := os.ReadFile(f)
		lo := lexerOptions(Options{Standard: token.Std2019})
		p := newParser(src, Options{Standard: token.Std2019}, lo)
		p.parseDesignFile()
		if p.dead {
			t.Errorf("%s: budget exhausted", f)
		}
		if p.steps > stepsPerByte*len(src)/4+baseSteps {
			t.Errorf("%s: %d steps for %d bytes", f, p.steps, len(src))
		}
	}
}

// TestEditReplay applies random edits (the generated editTexts and
// deletions) to corpus files and checks the invariants after each step.
func TestEditReplay(t *testing.T) {
	r := &env.SplitMix64{State: 99}
	files := corpusFiles(t)
	for _, f := range files {
		src, _ := os.ReadFile(f)
		for range 40 {
			at := int(r.Uint64() % uint64(len(src)+1))
			if r.Uint64()%3 == 0 && len(src) > 0 {
				end := min(len(src), at+int(r.Uint64()%8))
				src = append(src[:at:at], src[end:]...)
			} else {
				ins := editTexts[r.Uint64()%uint64(len(editTexts))]
				src = slices.Concat(src[:at], []byte(ins), src[at:])
			}
			checkInvariants(t, f, src, Options{Standard: token.Std2019})
		}
	}
}
