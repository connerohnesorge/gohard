package lint

import (
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// edgeStyle reports lint/clock-edge-style: a clk'event edge test in a
// process of a design that also tests edges with rising_edge or
// falling_edge, so one style is used throughout. 'event in a
// subprogram (a change detector, an edge function) is not a clock edge
// test of the design.
func edgeStyle(res *sema.Result, sev diag.Severity) []diag.Diagnostic {
	type tick struct {
		f  *sema.File
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
			s := strings.ToLower(t.Text())
			switch {
			case s == "rising_edge" || s == "falling_edge":
				call = true
			case after && s == "event" && inProcess(t):
				ticks = append(ticks, tick{u.File, t.Span()})
			}
			after = s == "'"
		}
	}
	if !call {
		return nil
	}
	var ds []diag.Diagnostic
	for _, t := range ticks {
		f := diag.New(diag.LintClockEdgeStyle)
		f.Severity = sev
		ds = append(ds, at(t.f, t.at, f))
	}
	return ds
}

// inProcess reports whether t is in a process statement and not in a
// subprogram.
func inProcess(t cst.Token) bool {
	for n := range t.Parent().Ancestors() {
		switch n.Kind() {
		case cst.KindProcessStmt:
			return true
		case cst.KindSubprogramBody:
			return false
		}
	}
	return false
}
