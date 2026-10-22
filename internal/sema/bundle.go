package sema

import (
	"bytes"
	"compress/flate"
	"io"
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

// The bundled std and ieee libraries (#33) are analyzed as VHDL-2019
// text when GoHard is built (go generate: tools/prebuild stores the
// result with the codec of codec.go in internal/stdlib/prebuilt.bin,
// which the binary embeds), and shared read-only by every analysis:
// project units read their declarations and never modify them. Per
// standard views are applied at lookup (Decl.First, visibleIn), so the
// bundle itself does not depend on the selected standard.
//
// Startup decodes the stored bundle and analyzes nothing. Only a build
// whose prebuilt.bin is stale (another codec schema or other sources,
// which the generated-file sync check fails in CI) analyzes the text
// on first use instead; BundleSource reports which happened.

var (
	bundleOnce sync.Once
	bundle     *Result
	bundleFrom string
)

// Bundle returns the analyzed std/ieee bundle. Its Diagnostics are
// errors inside the bundled text, which project analyses report as
// stdlib/internal-error (exit 2).
func Bundle() *Result {
	bundleOnce.Do(func() {
		if r, objs, err := unpackBundleObjs(stdlib.Prebuilt()); err == nil {
			bundle, bundleFrom, bundleObjs = r, "prebuilt", objs
			return
		}
		bundle, bundleFrom = analyzeBundle(), "analyzed"
	})
	return bundle
}

// BundleSource reports where Bundle's result came from: "prebuilt"
// (decoded from the build-time analysis) or "analyzed" (the embedded
// result was stale, so the text was analyzed at first use).
func BundleSource() string {
	Bundle()
	return bundleFrom
}

// PrebuildBundle analyzes the bundled text and returns its stored form,
// the content of internal/stdlib/prebuilt.bin (tools/prebuild): the
// encoded bundle, DEFLATE-compressed.
func PrebuildBundle() []byte { return packBundle(analyzeBundle()) }

func packBundle(r *Result) []byte {
	var b bytes.Buffer
	w, _ := flate.NewWriter(&b, flate.BestCompression)
	w.Write(encodeBundle(r))
	w.Close()
	return b.Bytes()
}

func unpackBundle(b []byte) (*Result, error) {
	r, _, err := unpackBundleObjs(b)
	return r, err
}

func unpackBundleObjs(b []byte) (*Result, []any, error) {
	raw, err := io.ReadAll(flate.NewReader(bytes.NewReader(b)))
	if err != nil {
		return nil, nil, errCodec
	}
	return decodeBundleObjs(raw)
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
func (s *session) standardUnit() *Unit { return s.stdUnit("standard") }

// stdUnit returns the bundled primary unit std.name, or nil (while the
// bundle is analyzed and the unit is not analyzed yet).
func (s *session) stdUnit(name Key) *Unit {
	var l *Library
	if s.base != nil {
		l = s.base.libs["std"]
	} else {
		l = s.libs["std"]
	}
	if l == nil {
		return nil
	}
	u, _ := l.Unit(name)
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

// reflectionType returns the std.reflection type named name (VHDL-2019
// LRM 16.3: SUBTYPE_MIRROR, VALUE_MIRROR, ...), the result types of
// 'REFLECT, whether or not the unit names std.reflection; nil when it
// cannot be found (the bundle analyzing itself). Its full declaration
// is returned, never the incomplete one.
func (a *analyzer) reflectionType(name Key) *Type {
	u := a.s.stdUnit("reflection")
	if u == nil || u.Region == nil || u == a.u {
		return nil
	}
	for _, d := range lookupIn(u.Region, name, token.Std2019) {
		if d.Kind.IsTypeMark() && d.Type != nil &&
			d.Type.Base().Class != TypeIncomplete {
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
