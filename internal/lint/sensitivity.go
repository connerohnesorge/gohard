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
// finding names what the quick fix adds to the list (ListName). The
// rule works on whole signals: an element or field in the list covers
// reads of every element or field of that signal, so listing r.x and
// reading r.w gives no finding.
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
				f := diag.New(diag.LintSensitivityList,
					ListName(u.File, n, r.Decl))
				f.Severity = sev
				out = append(out, finding{u, at(u.File, r.Span, f)})
			}
		}
	}
	return out
}

// ListName returns the name to add to the sensitivity list of process
// proc in file f for a read of a name declared by d: d's name as
// written, or for an alias declared in the process, which the list
// cannot see, the name of the object it stands for.
func ListName(f *sema.File, proc cst.Node, d *sema.Decl) string {
	if d.Kind == sema.DeclAlias && d.File == f {
		if p := proc.Span(); p.Start <= d.Span.Start &&
			d.Span.End <= p.End {
			if o := object(d); o != nil {
				return o.Text
			}
		}
	}
	return d.Text
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
