package lint

import (
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
)

// edgeStyle reports lint/clock-edge-style: a clk'event edge test in a
// process of a design that also tests edges with rising_edge or
// falling_edge, so one style is used throughout. Only 'event in the
// condition of an if, elsif or wait is an edge test: 'event in a
// subprogram (a change detector, an edge function) or in an assertion
// is not a clock edge test of the design.
//
// Whether the design calls rising_edge or falling_edge is known only
// once every unit's tokens are scanned. When a unit runs out of budget
// before its scan ends and no call was found, each other unit with an
// edge test gets one sema/eval-limit warning for this rule in place
// of its findings.
func edgeStyle(res *sema.Result, m *meter, sev diag.Severity,
) []finding {
	type tick struct {
		u  *sema.Unit
		at cst.Range
	}
	var ticks []tick
	call, cut := false, false
	for _, u := range res.Units {
		if u.File.Bundled {
			continue
		}
		after := false
		for t := range u.Node.DescendantTokens() {
			if !m.spend(u, 1) {
				cut = true
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
	var out []finding
	if !call {
		if cut {
			for i, t := range ticks {
				if i == 0 || ticks[i-1].u != t.u {
					out = append(out, finding{t.u,
						limited(t.u, "lint/clock-edge-style in")})
				}
			}
		}
		return out
	}
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
