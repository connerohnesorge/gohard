package parser

import (
	"testing"

	"github.com/connerohnesorge/gohard/internal/diag"

	"github.com/connerohnesorge/gohard/internal/token"
)

// pinnedFirst is the `first` column of #7's rule table, copied by hand
// so that a shifted standard in rules.csv cannot pass the tests that
// read their expectations from the same file. Rows marked "added by
// #31" are not in #7 yet.
var pinnedFirst = map[string]string{
	"prot_type":             "2002",
	"shared_nonprot":        "ERROR>=2002",
	"block_comment":         "2008",
	"context_decl":          "2008",
	"generic_type":          "2008",
	"generic_subp":          "2008",
	"generic_pkg":           "2008",
	"generic_on_subp":       "2008",
	"pkg_inst":              "2008",
	"subp_inst":             "2008",
	"nested_pkg":            "2008",
	"process_all":           "2008",
	"cond_seq_sig":          "2008",
	"sel_var":               "2008",
	"case_match":            "2008",
	"select_match":          "2008",
	"match_ops":             "2008",
	"cond_conv":             "2008",
	"unary_logic":           "2008",
	"force":                 "2008",
	"release":               "2008",
	"ext_name":              "2008",
	"record_constraint":     "2008",
	"elem_resolution":       "2008",
	"bitstr_len":            "2008",
	"gen_elsif_else_case":   "2008",
	"inertial_actual":       "2008",
	"subtype_attr":          "2008",
	"pkg_body_attr_decl":    "2008",
	"condanal":              "2019",
	"mode_view":             "2019",
	"private_var":           "2019",
	"incomplete_generic":    "2019",
	"cond_expr":             "2019",
	"cond_return":           "2019",
	"seq_block":             "2019",
	"empty_record":          "2019",
	"trailing_semi":         "2019",
	"end_component":         "2019",
	"record_attr":           "2019",
	"anon_type":             "2019",
	"subp_generic_map_call": "2019",
	"param_map":             "2019",
	"prot_port":             "2019",
	"std_env_2008":          "2008",
	"std_reflection":        "2019",
	// added by #31
	"seq_selected_sig": "2008",
	"cond_var":         "2008",
	"prot_generics":    "2019",
	"prot_inst":        "2019",
}

// TestRulesFirstPinned checks rules.csv's ids and first standards
// against the hand-copied table above.
func TestRulesFirstPinned(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Rules() {
		want, ok := pinnedFirst[r.ID]
		if !ok {
			t.Errorf("rules.csv row %q is not in the pinned table", r.ID)
			continue
		}
		seen[r.ID] = true
		got := r.First.String()
		if r.Reverse {
			got = "ERROR>=" + got
		}
		if got != want {
			t.Errorf("%s: first %s, #7 says %s", r.ID, got, want)
		}
	}
	for id := range pinnedFirst {
		if !seen[id] {
			t.Errorf("pinned rule %q missing from rules.csv", id)
		}
	}
	// Spot-check that the parser itself gates at the pinned standard,
	// not only that the table says so.
	for _, c := range []struct {
		src   string
		first token.Standard
	}{
		{"package p is constant c : integer := << constant .t.x : " +
			"integer >>; end;", token.Std2008},
		{"package p is constant c : boolean := a ?= b; end;",
			token.Std2008},
		{"package p is type r is record end record; end;",
			token.Std2019},
	} {
		for _, std := range token.Standards {
			r := Parse([]byte(c.src), Options{Standard: std})
			n := 0
			for _, pr := range r.Problems {
				if pr.Code == diag.SyntaxStandard {
					n++
				}
			}
			if want := std < c.first; (n > 0) != want {
				t.Errorf("%q under %v: %d syntax/standard", c.src,
					std, n)
			}
		}
	}
}
