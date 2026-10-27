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
// counted. A read through an alias is a read of its signal; the
// finding names the alias as written, which the quick fix adds.
func sensitivity(res *sema.Result, m *meter, sev diag.Severity,
) []finding {
	bad := errorFiles(res)
	var out []finding
	for _, u := range res.Units {
		if !trusted(u, bad) {
			continue
		}
		root := u.File.Root()
		for n := range u.Node.Descendants() {
			if !m.spend(u, 1) {
				break
			}
			if n.Kind() != cst.KindProcessStmt {
				continue
			}
			p := cst.ProcessStmt{Node: n}
			sl := p.Sensitivity()
			if sl.IsZero() || !sl.All().IsZero() || clocked(m, u, p) {
				continue
			}
			listed := map[*sema.Decl]bool{}
			for _, r := range refsIn(m, u, sl.Span()) {
				listed[object(r.Decl)] = true
			}
			for _, r := range refsIn(m, u, p.Stmts().Span()) {
				d := object(r.Decl)
				if !isSignal(d) || listed[d] ||
					r.Kind != sema.RefRead && r.Kind != sema.RefSelect ||
					r.Kind == sema.RefSelect && targeted(root, r.Span) ||
					attributed(u.File.Text, r.Span) ||
					associated(root, r.Span) {
					continue
				}
				listed[d] = true // one finding per signal, at its first read
				f := diag.New(diag.LintSensitivityList, r.Decl.Text)
				f.Severity = sev
				out = append(out, finding{u, at(u.File, r.Span, f)})
			}
		}
	}
	return out
}

// clocked reports whether process p tests a clock edge: it calls
// rising_edge or falling_edge, or reads 'event or 'stable.
func clocked(m *meter, u *sema.Unit, p cst.ProcessStmt) bool {
	tick := false
	for t := range p.DescendantTokens() {
		if !m.spend(u, 1) {
			return true // u is over its budget: its findings are dropped
		}
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
