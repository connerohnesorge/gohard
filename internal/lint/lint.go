// Package lint reports likely RTL mistakes in an analyzed design (#54):
// legal VHDL that is probably not what the author meant. It is a pure
// function of a sema.Result and the lint.* keys; `gohard check` and the
// language server both run it after sema and hier, so their
// diagnostics stay equal (#25).
package lint

import (
	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// Check returns the lint findings of res under cfg.
func Check(res *sema.Result, cfg config.Lint) []diag.Diagnostic {
	if res == nil {
		return nil
	}
	var ds []diag.Diagnostic
	if sev, on := severity(cfg.UnusedSignal); on {
		ds = append(ds, unusedSignals(res, sev)...)
	}
	if sev, on := severity(cfg.Latch); on {
		ds = append(ds, latches(res, sev)...)
	}
	if sev, on := severity(cfg.SensitivityList); on {
		ds = append(ds, sensitivity(res, sev)...)
	}
	return ds
}

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
