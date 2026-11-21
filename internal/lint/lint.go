// Package lint reports likely RTL mistakes in an analyzed design (#54):
// legal VHDL that is probably not what the author meant. It is a pure
// function of a sema.Result and the lint.* keys; `gohard check` and the
// language server both run it after sema and hier, so their
// diagnostics stay equal (#25).
package lint

import (
	"cmp"
	"slices"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// Check returns the lint findings of res under cfg. Each design unit's
// lint runs under a budget of eval.EditorSteps steps (P7); a unit that
// uses it up gets one sema/eval-limit warning in place of its
// findings. lint/clock-edge-style depends on every unit: when a unit
// over budget leaves it undecided, the other units with an edge test
// get a sema/eval-limit warning for that rule (edgeStyle).
func Check(res *sema.Result, cfg config.Lint) []diag.Diagnostic {
	return check(res, cfg, newMeter(eval.EditorSteps))
}

// check is Check counting the steps of each unit in m.
func check(res *sema.Result, cfg config.Lint, m *meter,
) []diag.Diagnostic {
	if res == nil {
		return nil
	}
	h := newHoles(res)
	var fs []finding
	if sev, on := severity(cfg.UnusedSignal); on {
		fs = append(fs, unusedSignals(res, m, sev, h)...)
	}
	if sev, on := severity(cfg.ClockEdgeStyle); on {
		fs = append(fs, edgeStyle(res, m, sev, h)...)
	}
	if sev, on := severity(cfg.Latch); on {
		fs = append(fs, latches(res, m, sev, h)...)
	}
	if sev, on := severity(cfg.SensitivityList); on {
		fs = append(fs, sensitivity(res, m, sev, h)...)
	}
	var ds []diag.Diagnostic
	for _, f := range fs {
		if !m.over(f.u) {
			ds = append(ds, f.d)
		}
	}
	for _, u := range res.Units {
		if m.over(u) {
			ds = append(ds, limited(u, "the lint rules in"))
		}
	}
	return ds
}

// limited is the warning, at the unit's name, that what (the lint rules
// in, or one rule in) unit u could not be computed within the budget.
func limited(u *sema.Unit, what string) diag.Diagnostic {
	name, rg := string(u.Name), u.Node.Span()
	if u.Decl != nil {
		name, rg = u.Decl.Kind.String()+" "+u.Decl.Text, u.Decl.Span
	}
	w := diag.New(diag.SemaEvalLimit, what+" "+name)
	return at(u.File, rg, w)
}

// finding is one lint diagnostic and the unit whose lint found it.
type finding struct {
	u *sema.Unit
	d diag.Diagnostic
}

// meter counts the steps of each unit's lint: one per syntax node,
// token, reference, declaration or sequential statement a rule visits,
// so the count depends only on the design. A rule stops working on a
// unit once the unit is over the limit.
type meter struct {
	limit int64
	used  map[*sema.Unit]int64
}

func newMeter(limit int64) *meter {
	return &meter{limit: limit, used: map[*sema.Unit]int64{}}
}

// spend charges n steps to u and reports whether u is still within
// the limit.
func (m *meter) spend(u *sema.Unit, n int) bool {
	m.used[u] += int64(n)
	return m.used[u] <= m.limit
}

// over reports whether u has used more than the limit.
func (m *meter) over(u *sema.Unit) bool { return m.used[u] > m.limit }

// severity maps a rule's lint.* value to a severity; on is false for
// "off".
func severity(v string) (sev diag.Severity, on bool) {
	switch v {
	case "hint":
		return diag.Hint, true
	case "warning":
		return diag.Warning, true
	}
	return 0, false
}

// analyzable reports whether u's references are complete. An opaque
// unit, or a unit that depends on one, was not fully analyzed, so a
// missing reference could be anywhere in it and no lint rule runs on
// it. A semantic error leaves every other name resolved. A syntax
// error swallows names only inside its error nodes (holes); each
// rule then skips only a finding those names could silence.
func analyzable(u *sema.Unit) bool {
	return u != nil && !u.Opaque && !u.Unchecked
}

// nameAt is an identifier token inside a syntax-error node.
type nameAt struct {
	key sema.Key
	at  cst.Range
}

// gap is one error node the parser swallowed.
type gap struct {
	span  cst.Range
	names []nameAt
}

// holes are the syntax errors of a result, by file. A file with only
// semantic errors has none.
type holes struct {
	byFile map[*sema.File][]gap
}

// newHoles collects the error nodes of files that have a syntax/error.
// Other files are not walked: a semantic error leaves the tree intact.
func newHoles(res *sema.Result) holes {
	h := holes{}
	if res == nil {
		return h
	}
	broken := map[string]bool{}
	for _, d := range res.Diagnostics {
		if d.Code == diag.SyntaxError {
			broken[d.Path] = true
		}
	}
	if len(broken) == 0 {
		return h
	}
	h.byFile = map[*sema.File][]gap{}
	for _, f := range res.Files {
		if f == nil || f.Parse == nil || !broken[f.Path] {
			continue
		}
		var gs []gap
		for n := range f.Root().Descendants() {
			if n.Kind() != cst.KindErrorNode || n.Inactive() {
				continue
			}
			sp := n.Span()
			if len(gs) > 0 {
				p := gs[len(gs)-1].span
				if p.Start <= sp.Start && sp.End <= p.End {
					continue // a node inside the one just recorded
				}
			}
			g := gap{span: sp}
			for t := range n.DescendantTokens() {
				knd := t.Kind()
				if knd != token.Identifier &&
					knd != token.ExtendedIdentifier {
					continue
				}
				if k := sema.TokenKey(t); k != "" {
					g.names = append(g.names, nameAt{k, t.Span()})
				}
			}
			gs = append(gs, g)
		}
		if len(gs) > 0 {
			h.byFile[f] = gs
		}
	}
	return h
}

// overlaps reports whether an error node in f overlaps rg.
func (h holes) overlaps(f *sema.File, rg cst.Range) bool {
	for _, g := range h.byFile[f] {
		if g.span.Start < rg.End && rg.Start < g.span.End {
			return true
		}
	}
	return false
}

// mentions reports whether an error node overlapping rg contains one
// of keys inside rg. A reference of that name may have been swallowed
// there, and only there.
func (h holes) mentions(f *sema.File, rg cst.Range, keys []sema.Key,
) bool {
	if f == nil || len(keys) == 0 {
		return false
	}
	want := map[sema.Key]bool{}
	for _, k := range keys {
		if k != "" {
			want[k] = true
		}
	}
	if len(want) == 0 {
		return false
	}
	for _, g := range h.byFile[f] {
		if g.span.End <= rg.Start || g.span.Start >= rg.End {
			continue
		}
		for _, n := range g.names {
			if n.at.End <= rg.Start || n.at.Start >= rg.End {
				continue
			}
			if want[n.key] {
				return true
			}
		}
	}
	return false
}

// useKeys returns d's name and the names of aliases of d declared in
// units. A swallowed use may name the alias rather than d.
func useKeys(d *sema.Decl, units ...*sema.Unit) []sema.Key {
	if d == nil || d.Name == "" {
		return nil
	}
	ks := []sema.Key{d.Name}
	seen := map[sema.Key]bool{d.Name: true}
	for _, u := range units {
		if u == nil {
			continue
		}
		for _, a := range u.Decls {
			if a.Kind != sema.DeclAlias || object(a) != d ||
				a.Name == "" || seen[a.Name] {
				continue
			}
			seen[a.Name] = true
			ks = append(ks, a.Name)
		}
	}
	return ks
}

// hidesUse reports whether a syntax error in one of units swallowed a
// token of d's name or of an alias of d. The use that would silence
// an unused-signal finding may be that token.
func (h holes) hidesUse(d *sema.Decl, units ...*sema.Unit) bool {
	if len(h.byFile) == 0 {
		return false
	}
	keys := useKeys(d, units...)
	for _, u := range units {
		if u == nil || u.Node.IsZero() {
			continue
		}
		if h.mentions(u.File, u.Node.Span(), keys) {
			return true
		}
	}
	return false
}

func at(f *sema.File, rg cst.Range, d diag.Diagnostic) diag.Diagnostic {
	l, c := f.Lines.LineCol(rg.Start)
	el, ec := f.Lines.LineCol(rg.End)
	return d.At(f.Path, l, c).To(el, ec)
}

// refsIn returns u's references inside rg. u.Refs is sorted by
// position, so this costs the references it returns, not all of u's.
func refsIn(m *meter, u *sema.Unit, rg cst.Range) []sema.Ref {
	i, _ := slices.BinarySearchFunc(u.Refs, rg.Start,
		func(r sema.Ref, s int) int { return cmp.Compare(r.Span.Start, s) })
	var out []sema.Ref
	j := i
	for ; j < len(u.Refs) && u.Refs[j].Span.Start < rg.End; j++ {
		if r := u.Refs[j]; r.Span.End <= rg.End {
			out = append(out, r)
		}
	}
	m.spend(u, j-i+1)
	return out
}

// object returns the declaration d stands for: for an alias of an
// object (or of part of one), the object, transitively; else what d
// denotes. A write or read through an alias is one of its object.
func object(d *sema.Decl) *sema.Decl {
	for range 64 { // alias chains are short; never loop forever
		if d == nil || d.Kind != sema.DeclAlias {
			return d
		}
		if d.Value == nil {
			// An alias of an alias, or of a non-object: sema sets
			// Target, not Value. The target may be an object alias.
			if d.Target == nil || d.Target == d {
				return d
			}
			d = d.Target
			continue
		}
		x := named(d.Value)
		if x == nil {
			return d
		}
		d = x
	}
	return d
}

// named returns the declaration a name expression is part of: the
// prefix of its selections, indexes and slices.
func named(e *sema.Expr) *sema.Decl {
	for e != nil {
		switch e.Kind {
		case sema.ExprSelect, sema.ExprIndex, sema.ExprSlice:
			e = e.Prefix
		case sema.ExprName:
			return e.Decl
		default:
			return nil
		}
	}
	return nil
}

func isSignal(d *sema.Decl) bool {
	return d != nil && (d.Kind == sema.DeclSignal ||
		d.Kind == sema.DeclPort && d.Class == sema.ClassSignal)
}
