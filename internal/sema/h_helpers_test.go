package sema

import (
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/parser"
	"github.com/connerohnesorge/gohard/v2/internal/span"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// hAnalyze analyzes files (path -> text) under standard std like
// Analyze, but without the v1.0 unsupported-construct detection
// (opaqueUnits): the v1.1 features' sema rules are tested before the
// integrator removes their detectors. Test code only.
func hAnalyze(t testing.TB, std string, files map[string]string,
) *Result {
	t.Helper()
	c := config.Defaults()
	c.Standard = std
	st, ok := token.ParseStandard(std)
	if !ok {
		t.Fatalf("standard %q", std)
	}
	popts := parser.FromConfig(c, "")
	var fs []*File
	for _, p := range slices.Sorted(mapsKeys(files)) {
		src := []byte(files[p])
		fs = append(fs, &File{Path: p, Text: src,
			Parse: parser.Parse(src, popts), Lines: span.NewIndex(src),
			Library: LibraryOf(c, p)})
	}
	s := &session{std: st, cfg: c, jobs: env.Sequential{}, files: fs,
		base: Bundle(), steps: int64(eval.EditorSteps), popts: popts}
	for _, f := range s.files {
		f.Units = scanFile(f)
		s.units = append(s.units, f.Units...)
		s.diags = append(s.diags, f.Parse.Diagnostics(f.Path)...)
		s.diags = append(s.diags, directiveRules(f, s.std)...)
	}
	offset := len(s.base.allUnits())
	for i, u := range s.units {
		u.Index = offset + i
	}
	s.buildLibraries()
	s.applyStandIns()
	s.linkDeps()
	s.levelize()
	s.initUnits(true)
	s.analyzeUnits()
	s.resolveLate()
	s.missingBodies()
	s.configurationRules()
	s.calledBeforeBody()
	s.libraryDiagnostics()
	s.staticChecks()
	return s.result()
}

// hAttrs returns the predefined attribute expressions attr in the
// statements of every process of r's units, in source order.
func hAttrs(r *Result, attr Key) []*Expr {
	var out []*Expr
	var stmts func([]*Stmt)
	stmts = func(ss []*Stmt) {
		for _, st := range ss {
			walkSeq(st.Seq, func(*SeqStmt) {}, func(e *Expr) {
				if e.Kind == ExprAttr && e.Attr == attr {
					out = append(out, e)
				}
			})
			stmts(st.Body)
		}
	}
	for _, u := range r.Units {
		stmts(u.Body)
	}
	return out
}
