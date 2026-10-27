package diag

import (
	"bytes"
	"encoding/xml"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/diag/junittest"
)

// junitDoc is the part of the JUnit form the tests read back.
type junitDoc struct {
	Name     string `xml:"name,attr"`
	Tests    int    `xml:"tests,attr"`
	Failures int    `xml:"failures,attr"`
	Errors   int    `xml:"errors,attr"`
	Suites   []struct {
		Tests    int `xml:"tests,attr"`
		Failures int `xml:"failures,attr"`
		Errors   int `xml:"errors,attr"`
		Props    []struct {
			Name  string `xml:"name,attr"`
			Value string `xml:"value,attr"`
		} `xml:"properties>property"`
		Cases []junitDocCase `xml:"testcase"`
	} `xml:"testsuite"`
}

type junitDocCase struct {
	Name    string          `xml:"name,attr"`
	Class   string          `xml:"classname,attr"`
	Time    string          `xml:"time,attr"`
	Error   []junitDocEntry `xml:"error"`
	Failure []junitDocEntry `xml:"failure"`
	Out     []string        `xml:"system-out"`
}

type junitDocEntry struct {
	Type    string `xml:"type,attr"`
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

func readJUnit(t *testing.T, out []byte) junitDoc {
	t.Helper()
	if err := junittest.Validate(out); err != nil {
		t.Fatalf("invalid against junit-10.xsd: %v\n%s", err, out)
	}
	var d junitDoc
	if err := xml.Unmarshal(out, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Suites) != 1 {
		t.Fatalf("%d test suites, want 1", len(d.Suites))
	}
	return d
}

// simReport is a check --sim run of two testbenches over three files:
// tb_fail fails an assertion and races, tb_ok passes with a note.
func simReport(strict bool) *Report {
	fail := New(SimAssertError, "assertion", "error", "boom").
		At("tb_fail.vhd", 9, 5)
	race := New(SimRace, "tb_fail.w", "tb_fail.r", "0", "1").
		At("tb_fail.vhd", 1, 8)
	note := New(SimAssertNote, "report", "after").At("tb_ok.vhd", 4, 3)
	lint := New(LintUnusedSignal, "signal", "s", "read").
		At("pkg.vhd", 6, 10)
	return &Report{
		Strict:      strict,
		Diagnostics: []Diagnostic{note, lint, fail, race},
		Checked:     []string{"tb_ok.vhd", "tb_fail.vhd", "pkg.vhd"},
		Testbenches: []Testbench{
			{Name: "tb_ok", Seed: 1, Status: "quiet", End: "20 ns",
				Repro:       ".gohard/traces/tb_ok-1.repro.json",
				Diagnostics: []Diagnostic{note}},
			{Name: "tb_fail", Seed: 1, Status: "halted", End: "9 ns",
				Repro:       ".gohard/traces/tb_fail-1.repro.json",
				Diagnostics: []Diagnostic{race, fail}},
		},
	}
}

func findCase(t *testing.T, d junitDoc, name string) junitDocCase {
	t.Helper()
	for _, c := range d.Suites[0].Cases {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no test case %q", name)
	return junitDocCase{}
}

// TestRenderJUnit pins the shape of the JUnit form (#61): a case per
// checked file then per testbench, failures typed by code, warnings
// in system-out unless --strict, the testbench facts as properties.
func TestRenderJUnit(t *testing.T) {
	d := readJUnit(t, RenderJUnit(simReport(false)))
	s := d.Suites[0]
	if d.Tests != 5 || d.Failures != 1 || d.Errors != 0 ||
		s.Tests != 5 || s.Failures != 1 || s.Errors != 0 {
		t.Errorf("counts %+v", d)
	}
	var order []string
	for _, c := range s.Cases {
		order = append(order, c.Class+" "+c.Name)
		if c.Time != "0" {
			t.Errorf("%s: time %q", c.Name, c.Time)
		}
	}
	want := []string{"gohard.check pkg.vhd", "gohard.check tb_fail.vhd",
		"gohard.check tb_ok.vhd", "gohard.sim tb_fail", "gohard.sim tb_ok"}
	if !slices.Equal(order, want) {
		t.Errorf("cases %v, want %v", order, want)
	}
	tf := findCase(t, d, "tb_fail")
	if len(tf.Failure) != 1 || tf.Failure[0].Type != "sim/assert-error" ||
		tf.Failure[0].Message != "assertion error: boom" ||
		strings.Contains(tf.Failure[0].Text, "sim/race") {
		t.Errorf("tb_fail failure %+v", tf.Failure)
	}
	if len(tf.Out) != 1 || !strings.HasPrefix(tf.Out[0], "seed: 1\n"+
		"status: halted\nend: 9 ns\n"+
		"repro: .gohard/traces/tb_fail-1.repro.json\n") ||
		!strings.Contains(tf.Out[0], "warning[sim/race]") {
		t.Errorf("tb_fail system-out %q", tf.Out)
	}
	if c := findCase(t, d, "tb_fail.vhd"); len(c.Failure)+len(c.Error)+
		len(c.Out) != 0 {
		t.Errorf("testbench diagnostics leaked into %+v", c)
	}
	if c := findCase(t, d, "pkg.vhd"); len(c.Failure) != 0 ||
		len(c.Out) != 1 || !strings.Contains(c.Out[0],
		"pkg.vhd:6:10: warning[lint/unused-signal]") {
		t.Errorf("pkg.vhd %+v", c)
	}
	var props []string
	for _, p := range s.Props {
		props = append(props, p.Name+"="+p.Value)
	}
	if !slices.Contains(props, "tb_ok.end=20 ns") ||
		!slices.Contains(props,
			"tb_fail.repro=.gohard/traces/tb_fail-1.repro.json") ||
		len(props) != 8 {
		t.Errorf("properties %v", props)
	}

	// Under --strict both warnings fail their cases.
	d = readJUnit(t, RenderJUnit(simReport(true)))
	if d.Failures != 2 {
		t.Errorf("strict: %d failures, want 2", d.Failures)
	}
	tf = findCase(t, d, "tb_fail")
	if len(tf.Failure) != 1 || !strings.Contains(tf.Failure[0].Text,
		"warning[sim/race]") || tf.Failure[0].Type != "sim/assert-error" {
		t.Errorf("strict tb_fail %+v", tf.Failure)
	}
	if c := findCase(t, d, "pkg.vhd"); len(c.Failure) != 1 ||
		c.Failure[0].Type != "lint/unused-signal" {
		t.Errorf("strict pkg.vhd %+v", c)
	}
}

// TestRenderJUnitTool: a tool or setup failure is an <error>, in a
// testbench case or in the case of its file, and a pathless one in the
// case named gohard.
func TestRenderJUnitTool(t *testing.T) {
	report := NewVariant(UnsupportedConstruct, PleaseReport, "x").
		At("tb.vhd", 3, 1)
	cfg := New(ConfigUnknownKey, "bogus").At("gohard.yaml", 2, 1)
	usage := New(CLIUsage, "bad")
	r := &Report{
		Diagnostics: []Diagnostic{report, cfg, usage},
		Testbenches: []Testbench{{Name: "tb",
			Diagnostics: []Diagnostic{report}}},
	}
	d := readJUnit(t, RenderJUnit(r))
	if d.Errors != 3 || d.Tests != 3 {
		t.Errorf("counts %+v", d)
	}
	for name, code := range map[string]Code{"gohard": CLIUsage,
		"gohard.yaml": ConfigUnknownKey, "tb": UnsupportedConstruct} {
		c := findCase(t, d, name)
		if len(c.Error) != 1 || c.Error[0].Type != string(code) {
			t.Errorf("%s: %+v", name, c)
		}
	}
}

// TestJUnitInputOrder: the bytes depend neither on the order of the
// diagnostics, the checked files and the testbenches, nor on repeated
// entries (P1).
func TestJUnitInputOrder(t *testing.T) {
	base := simReport(false)
	base.Diagnostics = append(base.Diagnostics, sample()...)
	want := RenderJUnit(base)
	readJUnit(t, want)
	for seed := range uint64(16) {
		r := *base
		r.Diagnostics = shuffled(append(r.Diagnostics, r.Diagnostics[:3]...),
			seed)
		r.Checked = slices.Clone(r.Checked)
		slices.Reverse(r.Checked)
		r.Checked = append(r.Checked, r.Checked[seed%3])
		r.Testbenches = slices.Clone(r.Testbenches)
		if seed%2 == 0 {
			slices.Reverse(r.Testbenches)
		}
		for i := range r.Testbenches {
			r.Testbenches[i].Diagnostics = shuffled(
				r.Testbenches[i].Diagnostics, seed)
		}
		if got := RenderJUnit(&r); !bytes.Equal(got, want) {
			t.Fatalf("seed %d changed the bytes:\n%s\nwant:\n%s", seed,
				got, want)
		}
	}
}

// TestJUnitEveryCodeValid: every table code, at every severity and
// with and without --strict, gives a document valid against the
// schema, and every diagnostic appears exactly once.
func TestJUnitEveryCodeValid(t *testing.T) {
	ds := sample()
	for _, strict := range []bool{false, true} {
		r := &Report{Diagnostics: ds, Strict: strict,
			Checked: []string{"a.vhd", "z.vhd"}}
		out := RenderJUnit(r)
		d := readJUnit(t, out)
		n := 0
		for _, c := range d.Suites[0].Cases {
			for _, e := range append(c.Error, c.Failure...) {
				n += strings.Count(e.Text, "\n") + 1
			}
			for _, o := range c.Out {
				n += strings.Count(o, "\n") + 1
			}
		}
		if want := len(Normalize(ds)); n != want {
			t.Errorf("strict %v: %d diagnostic lines, want %d", strict, n,
				want)
		}
	}
}

// TestJUnitEscaping: whatever bytes a path, message or testbench name
// holds, the document is well-formed and valid, and no text can
// forge an element or attribute.
func TestJUnitEscaping(t *testing.T) {
	g := uint64(11)
	pick := func(n int) int {
		g = g*6364136223846793005 + 1442695040888963407
		return int(g>>33) % n
	}
	alphabet := []string{"a", "/", "<", ">", "&", `"`, "'", "]]>", "\n",
		"\r", "\t", "\x00", "\x1b", "\x7f", "\u0085", "\u2028", "\ufffe",
		"\uffff", "\xff", "\xc3", "é", "😀", "<failure/>", "&amp;"}
	word := func() string {
		var b strings.Builder
		for range pick(10) {
			b.WriteString(alphabet[pick(len(alphabet))])
		}
		return b.String()
	}
	for range 500 {
		p, m := word(), word()
		d := Diagnostic{Path: p, Line: 1, Col: 1, Severity: Error,
			Code: SimAssertError, Message: m,
			Related: []Related{{Path: p, Line: 1, Col: 1, Message: m}}}
		w := Diagnostic{Path: p, Line: 2, Col: 1, Severity: Warning,
			Code: SimRace, Message: word()}
		r := &Report{Tool: word(), Diagnostics: []Diagnostic{d, w},
			Checked: []string{word(), p},
			Testbenches: []Testbench{{Name: word(), Status: word(),
				End: word(), Repro: word(), Diagnostics: []Diagnostic{d}}}}
		out := RenderJUnit(r)
		dd := readJUnit(t, out)
		if dd.Tests != len(dd.Suites[0].Cases) {
			t.Fatalf("tests %d, cases %d:\n%s", dd.Tests,
				len(dd.Suites[0].Cases), out)
		}
		tb := dd.Suites[0].Cases[len(dd.Suites[0].Cases)-1]
		// What a reader gets back is the text form of the message,
		// with characters XML cannot carry as U+FFFD.
		rs := []rune(escapeText(m))
		for i, r := range rs {
			if r == 0xfffe || r == 0xffff {
				rs[i] = 0xfffd
			}
		}
		if len(tb.Failure) != 1 || tb.Failure[0].Message != string(rs) {
			t.Fatalf("message %q came back as %+v", m, tb.Failure)
		}
	}
}

// TestJUnitValidatorRejects: the schema check is not vacuous; each of
// these documents breaks one rule of junit-10.xsd.
func TestJUnitValidatorRejects(t *testing.T) {
	ok := `<testsuites><testsuite name="s" tests="1" failures="0" ` +
		`errors="0" time="0"><testcase name="c" time="0">` +
		`<failure type="x">t</failure><system-out>o</system-out>` +
		`</testcase></testsuite></testsuites>`
	if err := junittest.Validate([]byte(ok)); err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}
	for _, bad := range []string{
		``,
		`<testsuites>`,
		`<report/>`,
		`<testsuites><testsuite tests="1" failures="0" errors="0"/>` +
			`</testsuites>`,
		`<testsuites><testsuite name="s" tests="1" failures="0" ` +
			`errors="0" time="1s"/></testsuites>`,
		`<testsuites><testcase name="c"/></testsuites>`,
		`<testsuites><testsuite name="s" tests="1" failures="0" ` +
			`errors="0"><testcase name="c"><properties/></testcase>` +
			`</testsuite></testsuites>`,
		`<testsuites><testsuite name="s" tests="1" failures="0" ` +
			`errors="0"><testcase name="c" file="a"/></testsuite>` +
			`</testsuites>`,
		`<testsuites><testsuite name="s" tests="1" failures="0" ` +
			`errors="0"><properties>x</properties></testsuite>` +
			`</testsuites>`,
		`<testsuites><testsuite name="s" tests="1" failures="0" ` +
			`errors="0"><properties><property name="a"/></properties>` +
			`</testsuite></testsuites>`,
		`<testsuites><testsuite name="s" tests="1" failures="0" ` +
			`errors="0"><system-out><b/></system-out></testsuite>` +
			`</testsuites>`,
		`<testsuites><testsuite name="s" tests="1" failures="0" ` +
			`errors="0"><testcase name="c"><rerunFailure/></testcase>` +
			`</testsuite></testsuites>`,
	} {
		if err := junittest.Validate([]byte(bad)); err == nil {
			t.Errorf("accepted invalid document %s", bad)
		}
	}
}
