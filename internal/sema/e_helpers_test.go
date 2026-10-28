package sema

// e_helpers_test.go holds the helpers of the v1.1 Stage 1 decl and
// statement rule tests (e_*_test.go). In v1.0 unsupported.go makes
// every unit using a #18 construct opaque and drops its semantic
// diagnostics; these tests check the rules that hold once the
// detectors are gone, so they analyze without the opacity step.

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/parser"
	"github.com/connerohnesorge/gohard/v2/internal/span"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// analyzeOpen is Analyze on one file t.vhd under standard std without
// the unsupported-construct step (unsupported.go opaqueUnits): the
// session's other phases run as in session.run.
func analyzeOpen(t testing.TB, std, src string) *Result {
	t.Helper()
	c := config.Defaults()
	c.Standard = std
	s, ok := token.ParseStandard(std)
	if !ok {
		t.Fatalf("bad standard %q", std)
	}
	popts := parser.FromConfig(c, "")
	text := []byte(src)
	f := &File{Path: "t.vhd", Text: text, Parse: parser.Parse(text, popts),
		Lines: span.NewIndex(text), Library: LibraryOf(c, "t.vhd")}
	ss := &session{
		std: s, cfg: c, jobs: env.Sequential{}, files: []*File{f},
		base: Bundle(), steps: int64(eval.EditorSteps), popts: popts,
	}
	for _, f := range ss.files {
		f.Units = scanFile(f)
		ss.units = append(ss.units, f.Units...)
		ss.diags = append(ss.diags, f.Parse.Diagnostics(f.Path)...)
		ss.diags = append(ss.diags, directiveRules(f, ss.std)...)
	}
	offset := len(ss.base.allUnits())
	for i, u := range ss.units {
		u.Index = offset + i
	}
	ss.buildLibraries()
	ss.applyStandIns()
	ss.linkDeps()
	ss.levelize()
	ss.initUnits(true)
	ss.analyzeUnits()
	ss.resolveLate()
	ss.missingBodies()
	ss.configurationRules()
	ss.calledBeforeBody()
	ss.libraryDiagnostics()
	ss.staticChecks()
	return ss.result()
}

// errorLines returns "line: message" for every error of r, sorted by
// line then message.
func errorLines(r *Result) []string {
	var out []string
	ds := slices.Clone(r.Diagnostics)
	slices.SortStableFunc(ds, func(a, b diag.Diagnostic) int {
		return cmp.Or(cmp.Compare(a.Line, b.Line),
			strings.Compare(a.Message, b.Message))
	})
	for _, d := range ds {
		if d.Severity.String() == "error" {
			out = append(out, fmt.Sprintf("%d: %s", d.Line, d.Message))
		}
	}
	return out
}

// errorMatch reports whether error got is the wanted one: equal, or
// for a want ending in "..." (a long candidate list) its prefix.
func errorMatch(got, want string) bool {
	if p, ok := strings.CutSuffix(want, "..."); ok {
		return strings.HasPrefix(got, p)
	}
	return got == want
}

// wantCase is a source and the exact errors it must produce: "N: text"
// for each error on line N (1-based), in errorLines order; a trailing
// "..." matches any rest of the message.
type wantCase struct {
	name, std string
	src       string
	want      []string
}

// checkExact analyzes each case without the opacity step and compares
// its errors with the wanted ones, exactly.
func checkExact(t *testing.T, cases []wantCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			std := cmp.Or(c.std, "2019")
			got := errorLines(analyzeOpen(t, std, c.src))
			if !slices.EqualFunc(got, c.want, errorMatch) {
				t.Errorf("errors:\n%s\nwant:\n%s",
					strings.Join(got, "\n"), strings.Join(c.want, "\n"))
			}
		})
	}
}

// lines joins source lines with newlines, so a case's line numbers
// are its index in the list plus one.
func lines(ls ...string) string { return strings.Join(ls, "\n") + "\n" }
