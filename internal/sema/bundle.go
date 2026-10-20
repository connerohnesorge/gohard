package sema

import (
	"strconv"
	"strings"
	"sync"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/parser"
	"github.com/connerohnesorge/gohard/internal/span"
	"github.com/connerohnesorge/gohard/internal/stdlib"
	"github.com/connerohnesorge/gohard/internal/token"
)

// The bundled std and ieee libraries (#33) are analyzed once per
// process, as VHDL-2019 text, and shared read-only by every analysis:
// project units read their declarations and never modify them. Per
// standard views are applied at lookup (Decl.First, visibleIn), so the
// bundle itself does not depend on the selected standard.
//
// stdlib.Prebuilt is the hook for analyzing at build time instead
// (#33); until it lands the bundle is analyzed on first use.

var (
	bundleOnce sync.Once
	bundle     *Result
)

// Bundle returns the analyzed std/ieee bundle. Its Diagnostics are
// errors inside the bundled text, which project analyses report as
// stdlib/internal-error (exit 2).
func Bundle() *Result {
	bundleOnce.Do(func() { bundle = analyzeBundle() })
	return bundle
}

func analyzeBundle() *Result {
	var files []*File
	for _, bf := range stdlib.Files() {
		src, _ := stdlib.Source(bf.Path)
		files = append(files, &File{
			Path: bf.Path, Bundled: true, Library: IdentKey(bf.Library),
			Text: src, Lines: span.NewIndex(src),
			Parse: parser.Parse(src,
				parser.Options{Standard: token.Std2019}),
		})
	}
	tags := bundleTags()
	for _, c := range lrmContexts {
		src := []byte(c.text)
		files = append(files, &File{
			Path: c.path, Bundled: true, Library: "ieee",
			Text: src, Lines: span.NewIndex(src),
			Parse: parser.Parse(src,
				parser.Options{Standard: token.Std2019}),
		})
		tags[tagKey{c.path, strings.Index(c.text, c.name)}] =
			token.Std2008
	}
	s := &session{std: token.Std2019, jobs: env.Sequential{},
		files: files, steps: eval.EditorSteps, tags: tags}
	return s.run()
}

// lrmContexts are the two context declarations of library IEEE that
// IEEE 1076-2008 defines in its own text (16.1, IEEE_BIT_CONTEXT and
// IEEE_STD_CONTEXT) rather than in the IEEE-P1076 packages repository
// vendored under internal/stdlib, so the bundle declares them here,
// from the standard's wording, visible from VHDL-2008 on.
var lrmContexts = []struct{ path, name, text string }{
	{"ieee/ieee_bit_context.vhdl", "IEEE_BIT_CONTEXT",
		"context IEEE_BIT_CONTEXT is\n" +
			"  library IEEE;\n" +
			"  use IEEE.NUMERIC_BIT.all;\n" +
			"end context IEEE_BIT_CONTEXT;\n"},
	{"ieee/ieee_std_context.vhdl", "IEEE_STD_CONTEXT",
		"context IEEE_STD_CONTEXT is\n" +
			"  library IEEE;\n" +
			"  use IEEE.STD_LOGIC_1164.all;\n" +
			"  use IEEE.NUMERIC_STD.all;\n" +
			"end context IEEE_STD_CONTEXT;\n"},
}

// tagKey locates a bundled declaration by file and name offset.
type tagKey struct {
	path  string
	start int
}

// bundleTags maps every tagged bundled declaration to its first
// standard (stdlib.Decls, firststd.tsv).
func bundleTags() map[tagKey]token.Standard {
	m := map[tagKey]token.Standard{}
	for _, d := range stdlib.Decls() {
		m[tagKey{d.Path, d.Span.Start}] = d.First
	}
	return m
}

// firstStd returns the first-standard tag of a bundled declaration, 0
// when it has none (declarations of package bodies, parameters).
func (s *session) firstStd(d *Decl) token.Standard {
	if s.tags == nil || d.File == nil {
		return 0
	}
	return s.tags[tagKey{d.File.Path, d.Span.Start}]
}

// standardUnit returns std.standard.
func (s *session) standardUnit() *Unit {
	var l *Library
	if s.base != nil {
		l = s.base.libs["std"]
	} else {
		l = s.libs["std"]
	}
	u, _ := l.Unit("standard")
	return u
}

// standardRegion returns the region of std.standard, or nil.
func (s *session) standardRegion() *Region {
	if u := s.standardUnit(); u != nil {
		return u.Region
	}
	return nil
}

// stdType returns the std.standard type or subtype named name (nil
// while std.standard is analyzed and name is not declared yet).
func (a *analyzer) stdType(name Key) *Type {
	var ds []*Decl
	if a.inStandard {
		ds = a.lookup(name).Decls
	} else if r := a.s.standardRegion(); r != nil {
		ds = lookupIn(r, name, token.Std2019)
	}
	for _, d := range ds {
		if d.Kind.IsTypeMark() {
			return d.Type
		}
	}
	return nil
}

// stdLayout gives the std.standard types the evaluator's shared
// layouts (eval.Integer, eval.Time, ...).
func (a *analyzer) stdLayout(t *Type) {
	if !a.inStandard || t.Decl == nil {
		return
	}
	switch t.Decl.Name {
	case "boolean":
		t.Eval = eval.Boolean
	case "bit":
		t.Eval = eval.Bit
	case "character":
		t.Eval = eval.Character
	case "severity_level":
		t.Eval = eval.SeverityLevel
	case "integer":
		t.Eval = eval.Integer
	case "real":
		t.Eval = eval.Real
	case "time":
		t.Eval = eval.Time
	case "natural":
		t.Eval = eval.Natural
	case "positive":
		t.Eval = eval.Positive
	case "string":
		t.Eval = eval.String
	case "bit_vector":
		t.Eval = eval.BitVector
	}
}

// bundledInternal turns a diagnostic found inside the bundle into the
// tool failure stdlib/internal-error (#20, #33).
func bundledInternal(d diag.Diagnostic) diag.Diagnostic {
	lib, _, _ := strings.Cut(d.Path, "/")
	problem := d.Path + ":" + strconv.Itoa(d.Line) + ":" +
		strconv.Itoa(d.Col) + ": " + string(d.Code) + ": " + d.Message
	return stdlib.InternalError(lib, problem)
}
