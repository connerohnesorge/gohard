package vhdlgen_test

import (
	"bytes"
	"strconv"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/gen/vhdlgen"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// Analyze analyzes d under its standard.
func analyze(d vhdlgen.Design) *sema.Result {
	cfg := config.Defaults()
	cfg.Standard = d.Standard
	var src []sema.Source
	for _, f := range d.Files {
		src = append(src, sema.Source{Path: f.Path, Text: f.Text})
	}
	return sema.Analyze(sema.Input{Config: cfg, Files: src})
}

// genSeeds is the number of seeds the properties enumerate.
func genSeeds() uint64 {
	if testing.Short() {
		return 50
	}
	return 400
}

// TestDeterministic: the same (seed, data, options) give the same
// bytes (P1); another seed gives another design.
func TestDeterministic(t *testing.T) {
	for seed := range genSeeds() {
		for _, o := range []vhdlgen.Options{{}, {Racy: true}} {
			data := []byte(strconv.FormatUint(seed*7, 10))
			a := vhdlgen.Generate(seed, data, o)
			b := vhdlgen.Generate(seed, data, o)
			if a.Text() != b.Text() || a.RaceFree != b.RaceFree ||
				a.Standard != b.Standard {
				t.Fatalf("seed %d: two designs", seed)
			}
		}
	}
	if vhdlgen.Generate(1, nil, vhdlgen.Options{}).Text() ==
		vhdlgen.Generate(2, nil, vhdlgen.Options{}).Text() {
		t.Error("seeds 1 and 2 give the same design")
	}
}

// TestSmallest: seed 0 with no data is the smallest design, and it is
// the same whatever the size bounds.
func TestSmallest(t *testing.T) {
	a := vhdlgen.Generate(0, nil, vhdlgen.Options{})
	b := vhdlgen.Generate(0, nil, vhdlgen.Options{Units: 9, Signals: 9,
		Statements: 9})
	if a.Text() != b.Text() {
		t.Fatalf("seed 0 depends on the bounds:\n%s\n%s", a.Text(),
			b.Text())
	}
	if len(a.Files) != 1 || a.Standard != "2008" || !a.RaceFree {
		t.Fatalf("seed 0: %d files, standard %s, race-free %t",
			len(a.Files), a.Standard, a.RaceFree)
	}
}

// TestLegal: every generated design analyzes without an error under
// its standard, with the data bytes deciding first and the seed after.
func TestLegal(t *testing.T) {
	std := map[string]int{}
	for seed := range genSeeds() {
		data := []byte(strconv.FormatUint(seed*31, 16))
		o := vhdlgen.Options{Racy: seed%3 == 0}
		if seed%5 == 4 {
			o.Standard = vhdlgen.Standards[seed%4]
		}
		d := vhdlgen.Generate(seed, data, o)
		if o.Standard != "" && d.Standard != o.Standard {
			t.Fatalf("seed %d: standard %s, want %s", seed, d.Standard,
				o.Standard)
		}
		std[d.Standard]++
		r := analyze(d)
		for _, dg := range r.Diagnostics {
			if dg.Severity == diag.Error {
				t.Fatalf("seed %d (%s): %s:%d:%d: %s %s\n%s", seed,
					d.Standard, dg.Path, dg.Line, dg.Col, dg.Code,
					dg.Message, numbered(d))
			}
		}
	}
	for _, s := range vhdlgen.Standards {
		if std[s] == 0 {
			t.Errorf("no design under %s", s)
		}
	}
}

// numbered returns the design's files with line numbers.
func numbered(d vhdlgen.Design) string {
	var b bytes.Buffer
	for _, f := range d.Files {
		b.WriteString("-- file " + f.Path + "\n")
		for i, l := range bytes.SplitAfter(f.Text, []byte("\n")) {
			if len(l) > 0 {
				b.WriteString(strconv.Itoa(i+1) + "\t")
				b.Write(l)
			}
		}
	}
	return b.String()
}
