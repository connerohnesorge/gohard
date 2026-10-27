package lint

import (
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// edgeStyle reports lint/clock-edge-style: a clk'event edge test in a
// process of a design that also tests edges with rising_edge or
// falling_edge, so one style is used throughout. Only 'event in the
// condition of an if, elsif or wait is an edge test: 'event in a
// subprogram (a change detector, an edge function) or in an assertion
// is not a clock edge test of the design.
func edgeStyle(res *sema.Result, m *meter, sev diag.Severity,
) []finding {
	type tick struct {
		u  *sema.Unit
		at cst.Range
	}
	var ticks []tick
	call := false
	for _, u := range res.Units {
		if u.File.Bundled {
			continue
		}
		after := false
		for t := range u.Node.DescendantTokens() {
			if !m.spend(u, 1) {
				break
			}
			s := strings.ToLower(t.Text())
			switch {
			case s == "rising_edge" || s == "falling_edge":
				call = true
			case after && s == "event" && edgeTest(t):
				ticks = append(ticks, tick{u, t.Span()})
			}
			after = s == "'"
		}
	}
	if !call {
		return nil
	}
	var out []finding
	for _, t := range ticks {
		f := diag.New(diag.LintClockEdgeStyle)
		f.Severity = sev
		out = append(out, finding{t.u, at(t.u.File, t.at, f)})
	}
	return out
}

// edgeTest reports whether t is in the condition of an if, elsif or
// wait statement of a process, not in a subprogram.
func edgeTest(t cst.Token) bool {
	cond := false
	for n := range t.Parent().Ancestors() {
		switch n.Kind() {
		case cst.KindProcessStmt:
			return cond
		case cst.KindSubprogramBody:
			return false
		case cst.KindIfStmt, cst.KindElsifBranch, cst.KindWaitStmt:
			cond = true
		case cst.KindAssertStmt, cst.KindReportStmt, cst.KindCaseStmt,
			cst.KindLoopStmt, cst.KindExitStmt, cst.KindNextStmt,
			cst.KindReturnStmt, cst.KindConditionalReturnStmt,
			cst.KindSignalAssignStmt, cst.KindVariableAssignStmt,
			cst.KindConditionalSignalAssignStmt,
			cst.KindConditionalVariableAssignStmt,
			cst.KindSelectedSignalAssignStmt,
			cst.KindSelectedVariableAssignStmt,
			cst.KindForceAssignStmt, cst.KindProcedureCallStmt:
			if !cond {
				return false // in a statement other than the test
			}
		}
	}
	return false
}
