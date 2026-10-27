// Package lint reports likely RTL mistakes in an analyzed design (#54):
// legal VHDL that is probably not what the author meant. It is a pure
// function of a sema.Result and the lint.* keys; `gohard check` and the
// language server both run it after sema and hier, so their
// diagnostics stay equal (#25).
package lint

import (
	"cmp"
	"slices"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// Check returns the lint findings of res under cfg. Each design unit's
// lint runs under a budget of eval.EditorSteps steps (P7); a unit that
// uses it up gets one sema/eval-limit warning in place of its
// findings.
func Check(res *sema.Result, cfg config.Lint) []diag.Diagnostic {
	return check(res, cfg, newMeter(eval.EditorSteps))
}

// check is Check counting the steps of each unit in m.
func check(res *sema.Result, cfg config.Lint, m *meter,
) []diag.Diagnostic {
	if res == nil {
		return nil
	}
	var fs []finding
	if sev, on := severity(cfg.UnusedSignal); on {
		fs = append(fs, unusedSignals(res, m, sev)...)
	}
	if sev, on := severity(cfg.ClockEdgeStyle); on {
		fs = append(fs, edgeStyle(res, m, sev)...)
	}
	if sev, on := severity(cfg.Latch); on {
		fs = append(fs, latches(res, m, sev)...)
	}
	if sev, on := severity(cfg.SensitivityList); on {
		fs = append(fs, sensitivity(res, m, sev)...)
	}
	var ds []diag.Diagnostic
	for _, f := range fs {
		if !m.over(f.u) {
			ds = append(ds, f.d)
		}
	}
	for _, u := range res.Units {
		if m.over(u) {
			ds = append(ds, limited(u))
		}
	}
	return ds
}

// limited is the warning of a unit over its lint budget, at the unit's
// name.
func limited(u *sema.Unit) diag.Diagnostic {
	what, rg := string(u.Name), u.Node.Span()
	if u.Decl != nil {
		what, rg = u.Decl.Kind.String()+" "+u.Decl.Text, u.Decl.Span
	}
	w := diag.New(diag.SemaEvalLimit, "the lint rules in "+what)
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

// trusted reports whether u's references are complete: a unit GoHard
// could not fully analyze, or whose file has errors, may miss the
// references that would silence a finding.
func trusted(u *sema.Unit, bad map[string]bool) bool {
	return !u.Opaque && !u.Unchecked && !bad[u.File.Path]
}

// errorFiles returns the paths of the files with errors.
func errorFiles(res *sema.Result) map[string]bool {
	bad := map[string]bool{}
	for _, d := range res.Diagnostics {
		if d.Severity == diag.Error {
			bad[d.Path] = true
		}
	}
	return bad
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
			return d.Denotes()
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
