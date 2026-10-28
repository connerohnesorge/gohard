package parser

import (
	_ "embed"
	"encoding/csv"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// rulesCSV is the #7 standard-gating table, the single source of the
// syntax/standard rules beyond keywords. Its columns are id, construct,
// first, lrm, snippet, diagnostic, gate, source and lrm_verified. LRM
// section numbers are documentation only.
//
//go:embed rules.csv
var rulesCSV string

// Rule is one row of rules.csv.
type Rule struct {
	ID        string
	Construct string
	// First is the first standard with the construct. Reverse rules
	// (first "ERROR>=2002": legal before, an error from then on) have
	// First set to that standard and Reverse true; they are sema gates.
	First       token.Standard
	Reverse     bool
	LRM         string
	Snippet     string
	Diagnostic  string
	Gate        string // lexer, parser, sema, prepass and combinations
	Source      string
	LRMVerified bool
}

var rules = loadRules(rulesCSV)

func loadRules(text string) []Rule {
	recs, err := csv.NewReader(strings.NewReader(text)).ReadAll()
	if err != nil || len(recs) < 1 {
		return nil
	}
	var out []Rule
	for _, r := range recs[1:] {
		if len(r) != 9 {
			return nil
		}
		first, rev := r[2], false
		if f, ok := strings.CutPrefix(first, "ERROR>="); ok {
			first, rev = f, true
		}
		std, ok := token.ParseStandard(first)
		if !ok {
			return nil
		}
		out = append(out, Rule{
			ID: r[0], Construct: r[1], First: std, Reverse: rev,
			LRM: r[3], Snippet: r[4], Diagnostic: r[5], Gate: r[6],
			Source: r[7], LRMVerified: r[8] == "true",
		})
	}
	return out
}

// Rules returns a copy of the rules.csv table in file order.
func Rules() []Rule { return append([]Rule(nil), rules...) }

// RuleByID returns the rule with the given id.
func RuleByID(id string) (Rule, bool) {
	for _, r := range rules {
		if r.ID == id {
			return r, true
		}
	}
	return Rule{}, false
}

// gateRule reports syntax/standard for rule id on [start, end) when the
// selected standard is older than the rule's first standard. Invariant
// (#7): it fires exactly when rule(N).first > S, on every occurrence,
// and never changes the tree. Reverse (sema) rules and unknown ids do
// nothing here. Builders call it once per occurrence of the construct,
// usually with the span of its first token (p.curSpan()).
func (p *Parser) gateRule(id string, start, end int) {
	r, ok := RuleByID(id)
	if !ok || r.Reverse {
		return
	}
	p.gate(r.Construct, r.First, start, end)
}
