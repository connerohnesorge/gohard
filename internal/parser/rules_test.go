package parser

import (
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/condanal"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// TestRulesTable checks the rules.csv data: the 46 rows of #7 plus the
// 4 parser gates #31 added (seq_selected_sig, cond_var, prot_generics,
// prot_inst), the one #18 added (var_generic_map), unique ids, a known
// gate kind, and the lexer and prepass gates use the table's construct
// text.
func TestRulesTable(t *testing.T) {
	rs := Rules()
	if len(rs) != 51 {
		t.Fatalf("%d rules, want 51", len(rs))
	}
	seen := map[string]bool{}
	for _, r := range rs {
		if r.ID == "" || seen[r.ID] || r.Construct == "" ||
			r.Gate == "" || r.Snippet == "" {
			t.Errorf("rule %+v", r)
		}
		seen[r.ID] = true
	}
	if r, _ := RuleByID("shared_nonprot"); !r.Reverse ||
		r.First != token.Std2002 {
		t.Errorf("shared_nonprot %+v", r)
	}
	gated := map[string]string{
		"block_comment": "/* x */",
		"bitstr_len":    `constant c : bit_vector := 8x"FF";`,
		"condanal":      "`if A = \"1\" then\n`end if\n",
	}
	for id, src := range gated {
		rule, _ := RuleByID(id)
		r := Parse([]byte(src), Options{Standard: token.Std1993,
			Defines: []condanal.Define{{Name: "A", Value: "1"}}})
		var got []string
		for _, p := range r.Problems {
			if p.Code == diag.SyntaxStandard {
				got = append(got, p.Args[0])
			}
		}
		if len(got) == 0 || !slices.Contains(got, rule.Construct) {
			t.Errorf("%s: gate texts %q, want %q", id, got,
				rule.Construct)
		}
	}
}

// TestGateRule: gateRule fires exactly when the rule's first standard is
// later than the selected one.
func TestGateRule(t *testing.T) {
	for _, rule := range Rules() {
		for _, std := range token.Standards {
			p := newParser(nil, Options{Standard: std},
				lexerOptions(Options{Standard: std}))
			p.gateRule(rule.ID, 0, 0)
			fired := len(p.probs) > 0
			want := !rule.Reverse && rule.First > std
			if fired != want {
				t.Errorf("%s under %v: fired=%v", rule.ID, std, fired)
			}
		}
	}
}
