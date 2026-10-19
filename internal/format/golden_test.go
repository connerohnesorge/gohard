package format

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/parser"
)

var updateGolden = flag.Bool("update", false,
	"rewrite the testdata/format goldens (<variant>.want.vhd)")

// goldenRoot holds the #32 golden cases; see its README.
const goldenRoot = "../../testdata/format"

// goldenVariant is one configuration of a golden case: the gohard.yaml
// text (nil for the defaults) and the file of its expected output.
type goldenVariant struct {
	name string
	yaml []byte
	want string
}

// goldenVariants lists the variants of the case in dir: one per
// <variant>.yaml, or the single variant "default" with no gohard.yaml.
func goldenVariants(t *testing.T, dir string) []goldenVariant {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var vs []goldenVariant
	for _, e := range ents {
		name, ok := strings.CutSuffix(e.Name(), ".yaml")
		if !ok || e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		vs = append(vs, goldenVariant{name, b,
			filepath.Join(dir, name+".want.vhd")})
	}
	if len(vs) == 0 {
		vs = []goldenVariant{{"default", nil,
			filepath.Join(dir, "default.want.vhd")}}
	}
	return vs
}

// goldenOptions parses a variant's gohard.yaml the way the CLI and the
// server do (config.Parse, then parser.FromConfig and FromConfig).
func goldenOptions(t *testing.T, yaml []byte) (parser.Options, Options) {
	t.Helper()
	c := config.Defaults()
	if yaml != nil {
		var ds []diag.Diagnostic
		c, ds = config.Parse(config.FileName, yaml)
		// A redundant default is a warning: the variant spells the
		// default out on purpose.
		for _, d := range ds {
			if d.Severity == diag.Error {
				t.Fatalf("gohard.yaml: %v", d)
			}
		}
	}
	return parser.FromConfig(c, "0.0.0-dev"), FromConfig(c)
}

// TestGolden formats testdata/format/<case>/in.vhd with each variant's
// configuration and compares the result with <variant>.want.vhd byte
// for byte (-update rewrites the goldens). It also checks, for every
// variant, that the input satisfies the FuzzFormat invariants
// (checkInvariants), that the golden is a fixed point (formatting it
// gives no edits), and that formatting through Source and through
// Range over the whole file agree with Format.
func TestGolden(t *testing.T) {
	ents, err := os.ReadDir(goldenRoot)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		n++
		dir := filepath.Join(goldenRoot, e.Name())
		t.Run(e.Name(), func(t *testing.T) { runGoldenCase(t, dir) })
	}
	if n == 0 {
		t.Fatal("no golden cases under " + goldenRoot)
	}
}

func runGoldenCase(t *testing.T, dir string) {
	src, err := os.ReadFile(filepath.Join(dir, "in.vhd"))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range goldenVariants(t, dir) {
		t.Run(v.name, func(t *testing.T) {
			popts, opts := goldenOptions(t, v.yaml)
			res := parser.Parse(src, popts)
			if !Parses(res) {
				t.Fatalf("in.vhd does not parse: %v",
					res.Diagnostics("in.vhd"))
			}
			// Goldens are valid VHDL for their standard, so they
			// also hold for the corpus oracle (tools/oracle).
			for _, d := range res.Diagnostics("in.vhd") {
				if d.Severity == diag.Error {
					t.Errorf("in.vhd: %v", d)
				}
			}
			got := Format(res, opts)
			if *updateGolden {
				if err := os.WriteFile(v.want, got.Text,
					0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(v.want)
			if err != nil {
				t.Fatalf("%v (run with -update)", err)
			}
			if !bytes.Equal(got.Text, want) {
				t.Errorf("formatted in.vhd differs from %s "+
					"(run with -update):\n%s\nwant:\n%s",
					filepath.Base(v.want), got.Text, want)
			}
			if strings.HasPrefix(filepath.Base(dir), "inactive") &&
				len(res.Inactive) == 0 {
				t.Error("an inactive-* case has no inactive branch")
			}
			if err := inactiveKept(src, res, got.Text); err != nil {
				t.Error(err)
			}
			if err := checkInvariants(src, popts, opts); err != nil {
				t.Errorf("invariants on in.vhd: %v", err)
			}
			again := Format(parser.Parse(want, popts), opts)
			if again.Changed() {
				t.Errorf("%s is not a fixed point: edits %v",
					filepath.Base(v.want), again.Edits)
			}
			viaSource, err := Source(src, popts, opts)
			if err != nil || !bytes.Equal(viaSource.Text, got.Text) {
				t.Errorf("Source = %q, %v; want the Format text",
					viaSource.Text, err)
			}
			// An insertion at the very end (the final newline)
			// belongs to the text after the range, so Range leaves it
			// out.
			whole := Range(res, opts, 0, len(src))
			inRange := slices.DeleteFunc(slices.Clone(got.Edits),
				func(e Edit) bool {
					return e.Start == len(src) && e.End == len(src)
				})
			if len(src) == 0 {
				inRange = got.Edits
			}
			if !slices.Equal(whole, inRange) {
				t.Errorf("Range over the whole file = %v, want %v",
					whole, inRange)
			}
		})
	}
}

// inactiveKept checks that the text of every inactive
// conditional-analysis branch of src appears byte for byte, in order,
// in the formatted output out (#32: never changed), with the line
// ending of its last line: an LF branch in a CRLF file keeps its LFs.
func inactiveKept(src []byte, res *parser.Result, out []byte) error {
	rest := out
	for _, r := range res.Inactive {
		end := r.End
		if end > r.Start && src[end-1] != '\n' {
			if k := bytes.IndexByte(src[end:], '\n'); k >= 0 &&
				len(bytes.TrimRight(src[end:end+k], "\r")) == 0 {
				end += k + 1
			}
		}
		chunk := src[r.Start:end]
		i := bytes.Index(rest, chunk)
		if i < 0 {
			return fmt.Errorf("inactive branch %d-%d changed:\n%q",
				r.Start, r.End, chunk)
		}
		rest = rest[i+len(chunk):]
	}
	return nil
}

// TestGoldenCoverage checks that the golden variants set every value
// #32 asks for of every format.* key, so a golden for each value
// exists (#32 Done when).
func TestGoldenCoverage(t *testing.T) {
	want := []string{
		"format.align_ports=false", "format.align_ports=true",
		"format.indent=1", "format.indent=2", "format.indent=4",
		"format.keyword_case=lower", "format.keyword_case=preserve",
		"format.keyword_case=upper",
		"format.max_line_length=120", "format.max_line_length=40",
		"format.max_line_length=80",
	}
	seen := map[string]bool{}
	ents, err := os.ReadDir(goldenRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		for _, v := range goldenVariants(t,
			filepath.Join(goldenRoot, e.Name())) {
			if v.yaml == nil {
				continue
			}
			c, _ := config.Parse(config.FileName, v.yaml)
			for _, k := range c.SetKeys() {
				if strings.HasPrefix(k, "format.") {
					seen[fmt.Sprintf("%s=%v", k, c.Value(k))] = true
				}
			}
		}
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("no golden variant sets %s", w)
		}
	}
}
