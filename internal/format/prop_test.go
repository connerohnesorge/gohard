package format

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/parser"
	"github.com/connerohnesorge/gohard/internal/token"
)

var allFiles = flag.Bool("format.all", false,
	"check the format invariants on every .vhd file under testdata")

// checkInvariants checks the #32 invariants on one input that parses
// and returns the first violation (nil for an input that does not
// parse): the edits are sorted, disjoint and give the text; the output
// parses to the same tree (sameTree); the edits change only whitespace
// and keyword case (onlyLayout); formatting the output changes
// nothing; the output ends in exactly one line ending.
func checkInvariants(src []byte, popts parser.Options,
	opts Options) error {
	res := parser.Parse(src, popts)
	if !Parses(res) {
		return nil
	}
	r := Format(res, opts)
	if got := Apply(src, r.Edits); !bytes.Equal(got, r.Text) {
		return fmt.Errorf("edits do not give the text:\n%s\nwant\n%s",
			got, r.Text)
	}
	if !slices.IsSortedFunc(r.Edits, func(a, b Edit) int {
		return a.Start - b.Start
	}) {
		return fmt.Errorf("edits not sorted")
	}
	for i := 1; i < len(r.Edits); i++ {
		if r.Edits[i].Start < r.Edits[i-1].End {
			return fmt.Errorf("edits overlap: %v", r.Edits[i-1:i+1])
		}
	}
	res2 := parser.Parse(r.Text, popts)
	if !Parses(res2) {
		return fmt.Errorf("output does not parse:\n%s", r.Text)
	}
	if err := sameTree(res.Tree(), res2.Tree()); err != nil {
		return fmt.Errorf("%v\n%s", err, r.Text)
	}
	if err := onlyLayout(src, res.Tree(), r.Edits); err != nil {
		return err
	}
	r2 := Format(res2, opts)
	if !bytes.Equal(r2.Text, r.Text) {
		return fmt.Errorf("not idempotent:\n%s\nthen\n%s", r.Text,
			r2.Text)
	}
	if len(r2.Edits) != 0 {
		return fmt.Errorf("formatted text has edits: %v", r2.Edits)
	}
	if !oneFinalNewline(r.Text) {
		return fmt.Errorf("not exactly one final newline: %q", r.Text)
	}
	return nil
}

func at(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return "<end>"
}

// vhdlFiles lists the test inputs: the parse rule files and the oracle
// corpus, or every .vhd file under testdata with -format.all.
func vhdlFiles(t *testing.T) []string {
	roots := []string{"../../testdata/parse", "../../testdata/corpus"}
	if *allFiles {
		roots = []string{"../../testdata"}
	}
	var out []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(p string, e fs.DirEntry,
			err error) error {
			if err != nil {
				return err
			}
			if !e.IsDir() && (strings.HasSuffix(p, ".vhd") ||
				strings.HasSuffix(p, ".vhdl")) {
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(out)
	return out
}

func TestInvariantsOnFiles(t *testing.T) {
	files := vhdlFiles(t)
	if len(files) == 0 {
		t.Fatal("no test files")
	}
	configs := []Options{Default(),
		{Indent: 4, KeywordCase: Upper, MaxLineLength: 100,
			Standard: token.Std2019},
		{Indent: 3, KeywordCase: Preserve, MaxLineLength: 40,
			AlignPorts: true, Standard: token.Std1993}}
	bad := 0
	for _, p := range files {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, std := range []token.Standard{token.Std2008,
			token.Std2019} {
			for _, o := range configs {
				err := checkInvariants(src,
					parser.Options{Standard: std}, o)
				if err != nil {
					bad++
					if bad <= 10 {
						t.Errorf("%s (std %v, %+v): %v", p, std, o,
							trunc(err.Error()))
					}
				}
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d failures in %d files", bad, len(files))
	}
}

func trunc(s string) string {
	if len(s) > 3000 {
		return s[:3000] + "..."
	}
	return s
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// perturb rewrites the layout whitespace of src: every indentation
// becomes a random run of spaces and tabs, every line gets random
// trailing whitespace, and blank lines are doubled. Comment contents
// and tokens are untouched (only Whitespace and Newline trivia
// change).
func perturb(res *parser.Result, seed uint64) []byte {
	next := func() uint64 { // xorshift64
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return seed
	}
	ws := func() string {
		b := make([]byte, next()%5)
		for i := range b {
			b[i] = " \t"[next()%2]
		}
		return string(b)
	}
	var out []byte
	emit := func(ts []cst.Trivia) {
		for i, tr := range ts {
			switch tr.Kind {
			case token.Whitespace:
				if i > 0 && ts[i-1].Kind == token.Newline {
					out = append(out, ws()...)
					continue
				}
				out = append(out, tr.Text...)
			case token.Newline:
				out = append(out, ws()...)
				out = append(out, tr.Text...)
				if i > 0 && ts[i-1].Kind == token.Newline {
					out = append(out, tr.Text...)
				}
			default:
				out = append(out, tr.Text...)
			}
		}
	}
	for t := range res.Tree().DescendantTokens() {
		emit(t.Leading())
		out = append(out, t.Text()...)
		emit(t.Trailing())
	}
	return out
}

func TestLayoutIndependence(t *testing.T) {
	// Property: the output does not depend on the input's indentation,
	// trailing whitespace or the length of blank-line runs (outside
	// verbatim regions, which these inputs do not have).
	n := 0
	for _, p := range vhdlFiles(t) {
		src := mustRead(t, p)
		if bytes.ContainsAny(src, "`") {
			continue // directives and inactive branches stay verbatim
		}
		for _, std := range []token.Standard{token.Std2008,
			token.Std2019} {
			popts := parser.Options{Standard: std}
			res := parser.Parse(src, popts)
			if !Parses(res) {
				continue
			}
			want := Format(res, Default()).Text
			for seed := uint64(1); seed <= 3; seed++ {
				mixed := perturb(res, seed*0x9E3779B97F4A7C15)
				got := Format(parser.Parse(mixed, popts),
					Default()).Text
				if !bytes.Equal(got, want) {
					t.Fatalf("%s (std %v, seed %d):\n%s\nwant\n%s", p,
						std, seed, got, want)
				}
			}
			n++
		}
	}
	if n == 0 {
		t.Fatal("no file checked")
	}
}
