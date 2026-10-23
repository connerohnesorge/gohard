package lint

import (
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// sensitivity reports lint/sensitivity-list: a signal read in a process
// with an explicit sensitivity list that the list does not name. A
// clocked process (one that tests an edge) reads its data on the
// clock, so only combinational processes are checked. Reads in a
// procedure call's arguments and attribute prefixes (s'length) are not
// counted.
func sensitivity(res *sema.Result, sev diag.Severity) []diag.Diagnostic {
	bad := errorFiles(res)
	var ds []diag.Diagnostic
	for _, u := range res.Units {
		if !trusted(u, bad) {
			continue
		}
		root := u.File.Root()
		for n := range u.Node.Descendants() {
			if n.Kind() != cst.KindProcessStmt {
				continue
			}
			p := cst.ProcessStmt{Node: n}
			sl := p.Sensitivity()
			if sl.IsZero() || !sl.All().IsZero() || clocked(p) {
				continue
			}
			listed := map[*sema.Decl]bool{}
			for _, r := range refsIn(u, sl.Span()) {
				listed[r.Decl] = true
			}
			for _, r := range refsIn(u, p.Stmts().Span()) {
				d := r.Decl
				if d == nil || listed[d] || !isSignal(d) ||
					r.Kind != sema.RefRead && r.Kind != sema.RefSelect ||
					r.Kind == sema.RefSelect && targeted(root, r.Span) ||
					attributed(u.File.Text, r.Span) ||
					associated(root, r.Span) {
					continue
				}
				listed[d] = true // one finding per signal, at its first read
				f := diag.New(diag.LintSensitivityList, d.Text)
				f.Severity = sev
				ds = append(ds, at(u.File, r.Span, f))
			}
		}
	}
	return ds
}

func isSignal(d *sema.Decl) bool {
	return d.Kind == sema.DeclSignal ||
		d.Kind == sema.DeclPort && d.Class == sema.ClassSignal
}

// refsIn returns u's references inside rg.
func refsIn(u *sema.Unit, rg cst.Range) []sema.Ref {
	var out []sema.Ref
	for _, r := range u.Refs {
		if rg.Start <= r.Span.Start && r.Span.End <= rg.End {
			out = append(out, r)
		}
	}
	return out
}

// clocked reports whether process p tests a clock edge: it calls
// rising_edge or falling_edge, or reads 'event or 'stable.
func clocked(p cst.ProcessStmt) bool {
	tick := false
	for t := range p.DescendantTokens() {
		s := strings.ToLower(t.Text())
		switch {
		case s == "rising_edge" || s == "falling_edge":
			return true
		case tick && (s == "event" || s == "stable"):
			return true
		}
		tick = s == "'"
	}
	return false
}

// attributed reports whether the name at rg is an attribute prefix.
func attributed(text []byte, rg cst.Range) bool {
	return rg.End < len(text) && text[rg.End] == '\''
}
