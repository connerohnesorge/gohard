package config

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/connerohnesorge/gohard/v2/internal/diag"
)

var update = flag.Bool("update", false,
	"rewrite the internal/config golden files")

// gcase is one input of a golden file.
type gcase struct {
	title string
	src   string
}

// render is the golden form of parsing one input: the exit code
// `gohard config check` would give, the text diagnostics, and the value
// of every key the input set.
func render(src string) string {
	c, ds := Parse(FileName, []byte(src))
	var b strings.Builder
	r := &diag.Report{Diagnostics: ds, Files: 1}
	fmt.Fprintf(&b, "exit %d\n", r.Exit())
	b.Write(diag.RenderText(r))
	for _, k := range c.SetKeys() {
		fmt.Fprintf(&b, "set %s = %s\n", k, show(c.Value(k)))
	}
	return b.String()
}

// show formats a key value for goldens.
func show(v any) string {
	switch v := v.(type) {
	case string:
		return strconv.Quote(v)
	case Time:
		return v.String()
	case time.Time:
		return v.Format(time.RFC3339Nano)
	case []string:
		q := make([]string, len(v))
		for i, s := range v {
			q[i] = strconv.Quote(s)
		}
		return "[" + strings.Join(q, ", ") + "]"
	case []Library:
		var parts []string
		for _, l := range v {
			var fs []string
			for _, f := range l.Folders {
				fs = append(fs, fmt.Sprintf("%q@%d:%d", f.Path, f.Line,
					f.Col))
			}
			parts = append(parts, fmt.Sprintf("%s@%d:%d: [%s]", l.Name,
				l.Line, l.Col, strings.Join(fs, ", ")))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []Define:
		var parts []string
		for _, d := range v {
			parts = append(parts, fmt.Sprintf("%s@%d:%d: %q", d.Name,
				d.Line, d.Col, d.Value))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []Model:
		var parts []string
		for _, m := range v {
			parts = append(parts, fmt.Sprintf("%s@%d:%d: %q", m.Unit,
				m.Line, m.Col, m.Path))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []Var:
		var parts []string
		for _, e := range v {
			parts = append(parts, fmt.Sprintf("%s: %q", e.Name, e.Value))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprint(v)
}

// renderCases is the golden form of a list of inputs.
func renderCases(cases []gcase) []byte {
	var b bytes.Buffer
	for i, c := range cases {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "== %s\n", c.title)
		for l := range strings.Lines(c.src) {
			b.WriteString("| " + strings.TrimSuffix(l, "\n") + "\n")
		}
		b.WriteString(render(c.src))
	}
	return b.Bytes()
}

func checkGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/config -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is out of date (run go test ./internal/config "+
			"-update and review the diff); got:\n%s", path, got)
	}
}

// TestGoldenFiles runs every testdata/golden/<case>.yaml and compares
// the result with <case>.golden.
func TestGoldenFiles(t *testing.T) {
	ins, err := filepath.Glob("testdata/golden/*.yaml")
	if err != nil || len(ins) == 0 {
		t.Fatalf("no golden inputs: %v", err)
	}
	for _, in := range ins {
		src, err := os.ReadFile(in)
		if err != nil {
			t.Fatal(err)
		}
		golden := strings.TrimSuffix(in, ".yaml") + ".golden"
		checkGolden(t, golden, []byte(render(string(src))))
	}
}

// nest writes key: value as YAML with the dotted key expanded into
// nested mappings.
func nest(key, value string) string {
	parts := strings.Split(key, ".")
	var b strings.Builder
	for i, p := range parts {
		b.WriteString(strings.Repeat("  ", i) + p + ":")
		if i < len(parts)-1 {
			b.WriteByte('\n')
		}
	}
	if strings.Contains(value, "\n") {
		ind := strings.Repeat("  ", len(parts))
		b.WriteString("\n")
		for l := range strings.Lines(value) {
			b.WriteString(ind + l)
		}
		return b.String()
	}
	return b.String() + " " + value + "\n"
}

func keysOf(t Type) []Key {
	var out []Key
	for _, k := range keys {
		if k.Type == t {
			out = append(out, k)
		}
	}
	return out
}

// TestGoldenBools covers every YAML 1.1 boolean spelling in every bool
// key (only true and false are accepted), and the quoted forms.
func TestGoldenBools(t *testing.T) {
	var cases []gcase
	for _, k := range keysOf(TypeBool) {
		for _, s := range yaml11Bools {
			cases = append(cases, gcase{k.Name + ": " + s,
				nest(k.Name, s)})
		}
		cases = append(cases,
			gcase{k.Name + ": quoted", nest(k.Name, `"true"`)},
			gcase{k.Name + ": number", nest(k.Name, "1")})
	}
	checkGolden(t, "testdata/bools.golden", renderCases(cases))
}

// stringSlots are a place for a free-form string value inside every key
// that holds strings, written so that the value goes where %s is.
func stringSlots() []gcase {
	return []gcase{
		{"defines", "defines:\n  FOO: %s\n"},
		{"simulation.env.vars", "simulation:\n  env:\n    vars:\n" +
			"      HOME: %s\n"},
		{"libraries", "libraries:\n  lib:\n    - %s\n"},
		{"simulation.models", "simulation:\n  models:\n" +
			"    lib.unit: %s\n"},
		{"simulation.dump_scope", "simulation:\n  dump_scope:\n" +
			"    - %s\n"},
		{"simulation.testbenches", "simulation:\n  testbenches:\n" +
			"    - %s\n"},
		{"allow_opaque", "allow_opaque:\n  - %s\n"},
	}
}

// TestGoldenStrings covers an unquoted YAML 1.1 boolean spelling in
// every key that holds strings, and the accepted quoted form.
func TestGoldenStrings(t *testing.T) {
	var cases []gcase
	for _, slot := range stringSlots() {
		for _, s := range []string{"yes", "off", "N", "TRUE"} {
			cases = append(cases, gcase{slot.title + ": " + s,
				fmt.Sprintf(slot.src, s)})
		}
		cases = append(cases, gcase{slot.title + ": quoted",
			fmt.Sprintf(slot.src, `"yes"`)})
	}
	checkGolden(t, "testdata/strings.golden", renderCases(cases))
}

// wrongValues returns values of the wrong type or out of range for k.
func wrongValues(k Key) []string {
	shapes := []string{"{a: 1}", "[1, 2]", "~", "*x", "!!int 5"}
	switch k.Type {
	case TypeBool:
		return append(shapes, "maybe")
	case TypeInt:
		return append(shapes, `"5"`, "1.5", "0x10", "012", "abc",
			strconv.FormatInt(k.Min-1, 10), "-1",
			"9223372036854775808")
	case TypeUint64:
		return append(shapes, `"5"`, "1e3", "-1",
			"18446744073709551616")
	case TypeEnum:
		return append(shapes, "nope", `"NOPE"`, "2008", "yes")
	case TypeTime:
		return append(shapes, "1ms", "1", "ms", "1 s", "1.5.2 ns",
			"1__0 ns", "_1 ns", "1 ms ", "1e ns", "10000 hr",
			"1e30 fs")
	case TypeTimestamp:
		return append(shapes, "2000-01-01", "2000-01-01T00:00:00",
			"2000-01-01T00:00:00+01:00", "2000-13-01T00:00:00Z",
			"yesterday")
	case TypeList:
		return append(shapes[:3], "a", "[[a]]", "[{a: 1}]", "[~]",
			`[""]`, "[1]")
	case TypeMap:
		return append(shapes[:3], "a", "{a: [1]}", "{a: {b: 1}}",
			"{a: ~}", `{"": x}`, "{9a: x}", "{[a]: x}")
	}
	return shapes
}

// TestGoldenTypes covers every type and range violation of every key.
func TestGoldenTypes(t *testing.T) {
	var cases []gcase
	for _, k := range keys {
		for _, v := range wrongValues(k) {
			src := nest(k.Name, v)
			if v == "*x" {
				src = "x: &x 1\n" + src
			}
			cases = append(cases, gcase{k.Name + ": " + v, src})
		}
	}
	checkGolden(t, "testdata/types.golden", renderCases(cases))
}

// TestGoldenDefaults sets every key to its default: exactly one
// config/redundant-default warning each (exit 0, 1 with --strict).
func TestGoldenDefaults(t *testing.T) {
	var cases []gcase
	for _, k := range keys {
		cases = append(cases, gcase{k.Name, nest(k.Name, k.Default)})
	}
	checkGolden(t, "testdata/defaults.golden", renderCases(cases))
}

// TestGoldenKeys covers unknown and duplicate keys at every nesting
// level: the top level, every group, and inside every map-valued key.
func TestGoldenKeys(t *testing.T) {
	var cases []gcase
	for _, g := range append([]string{""}, Groups()...) {
		title := g
		if g == "" {
			title = "top level"
		}
		cases = append(cases, gcase{"unknown key at " + title,
			nest(join(g, "bogus"), "1")})
	}
	cases = append(cases, gcase{"unknown group", "formats:\n  x: 1\n"})
	for _, k := range keys {
		v := "{}"
		switch k.Type {
		case TypeBool:
			v = "true"
		case TypeInt, TypeUint64:
			v = "50"
		case TypeEnum:
			v = strconv.Quote(k.Enum[len(k.Enum)-1])
		case TypeTime:
			v = `"2 ms"`
		case TypeTimestamp:
			v = `"2001-01-01T00:00:00Z"`
		case TypeList:
			v = "[]"
		}
		one := nest(k.Name, v)
		lines := strings.Split(strings.TrimSuffix(one, "\n"), "\n")
		dup := one + lines[len(lines)-1] + "\n"
		cases = append(cases, gcase{"duplicate " + k.Name, dup})
	}
	for _, m := range []gcase{
		{"libraries", "libraries:\n  lib: [a]\n  LIB: [b]\n"},
		{"defines", "defines:\n  FOO: \"1\"\n  foo: \"2\"\n"},
		{"simulation.models", "simulation:\n  models:\n" +
			"    l.u: a.vhd\n    L.U: b.vhd\n"},
		{"simulation.env.vars (case-sensitive)", "simulation:\n" +
			"  env:\n    vars:\n      A: x\n      a: y\n      A: z\n"},
	} {
		cases = append(cases, gcase{"duplicate entry in " + m.title,
			m.src})
	}
	checkGolden(t, "testdata/keys.golden", renderCases(cases))
}

// TestGoldenBuiltinDefines covers every built-in conditional-analysis
// identifier under defines:, in two spellings (exit 2).
func TestGoldenBuiltinDefines(t *testing.T) {
	var cases []gcase
	for _, b := range Builtins {
		for _, s := range []string{b, strings.ToLower(b)} {
			cases = append(cases, gcase{s,
				"defines:\n  " + s + ": \"x\"\n  USER: \"1\"\n"})
		}
	}
	checkGolden(t, "testdata/builtins.golden", renderCases(cases))
}

// TestGoldenCoversEveryCode checks that the golden files, together with
// the CLI fixtures (which can reach the file system: config/unreadable,
// library/folder-missing), hold every config/ and library/folder- code
// of the diag table (#28).
func TestGoldenCoversEveryCode(t *testing.T) {
	if *update {
		t.Skip("goldens are being rewritten")
	}
	files, _ := filepath.Glob("testdata/*.golden")
	more, _ := filepath.Glob("testdata/golden/*.golden")
	cliOut, _ := filepath.Glob("../../testdata/cli/*/want.std*")
	if len(cliOut) == 0 {
		t.Fatal("no CLI fixtures found")
	}
	var all []byte
	for _, f := range slices.Concat(files, more, cliOut) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, b...)
	}
	for _, info := range diag.Table() {
		if info.Code.Area() != "config" &&
			info.Code != diag.LibraryFolderMissing {
			continue
		}
		if !bytes.Contains(all, []byte("["+string(info.Code)+"]")) {
			t.Errorf("no golden fixture produces %s", info.Code)
		}
	}
}
