package corpus

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env"
)

const twoEntities = "entity inner is\nend entity;\n" +
	"architecture a of inner is begin end architecture;\n" +
	"  ENTITY Outer_TB is\nend entity;\n"

func TestParseConformanceCase(t *testing.T) {
	c, err := ParseConformanceCase("f", "c", []byte(twoEntities))
	if err != nil {
		t.Fatal(err)
	}
	// Defaults: 2019, the last entity in lower case.
	if c.Std != "2019" || c.Top != "outer_tb" || c.Pending != "" {
		t.Errorf("defaults %+v", c)
	}
	src := "-- std: 2008\n-- Note: prose, not a header\n" +
		"--pending:  conditional return ... when \n" +
		"-- nvc-differs: NVC rounds\n-- top: inner\n" + twoEntities +
		"-- std: 1993\n" // after the header: ignored
	c, err = ParseConformanceCase("f", "c", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := ConformanceCase{Feature: "f", Name: "c", Std: "2008",
		Top: "inner", Pending: "conditional return ... when",
		NVCDiffers: "NVC rounds"}
	if c != want {
		t.Errorf("got %+v\nwant %+v", c, want)
	}
	for _, bad := range []string{
		"-- std: 2008\n-- std: 2019\n" + twoEntities,
		"-- pending:\n" + twoEntities,
		"-- std: 2017\n" + twoEntities,
		"-- std: 2008\npackage p is end package;\n",
	} {
		if _, err := ParseConformanceCase("f", "c",
			[]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestConformanceCommands(t *testing.T) {
	c := ConformanceCase{Feature: "f", Name: "c", Std: "2008",
		Top: "tb"}
	if got := strings.Join(c.CheckArgs(), " "); got !=
		"check --sim --seed 0 --tb tb" {
		t.Errorf("check args %q", got)
	}
	if got := strings.Join(c.NVCArgs(), " "); got !=
		"--std=2008 -a c.vhd -e tb -r" {
		t.Errorf("nvc args %q", got)
	}
	// 2008 is GoHard's default: a gohard.yaml repeating it warns.
	if c.Config() != nil {
		t.Errorf("2008 config %q", c.Config())
	}
	c.Std = "2019"
	if string(c.Config()) != "standard: \"2019\"\n" {
		t.Errorf("2019 config %q", c.Config())
	}
	if c.Path() != "testdata/conformance/f/c.vhd" ||
		c.WantFile() != "c.want" || c.NVCFile() != "c.nvc.txt" {
		t.Errorf("paths %s %s %s", c.Path(), c.WantFile(), c.NVCFile())
	}
}

// randText returns random line-structured text from an alphabet that
// includes newlines, '#', '=' and a stream marker's characters, but no
// line that is exactly a section marker.
func randText(r *env.SplitMix64) []byte {
	const alpha = "ab =#\\\nxe:"
	n := int(r.Uint64() % 40)
	var b []byte
	for range n {
		b = append(b, alpha[r.Uint64()%uint64(len(alpha))])
	}
	return b
}

// TestWantRoundTrip: ParseWant inverts RenderWant for every exit code
// and stream content, with or without a final newline.
func TestWantRoundTrip(t *testing.T) {
	c := ConformanceCase{Name: "c", Std: "2019", Top: "tb"}
	r := &env.SplitMix64{State: 18}
	for i := range 2000 {
		exit := int(r.Uint64() % 4)
		out, errs := randText(r), randText(r)
		b := c.RenderWant(exit, out, errs)
		w, err := ParseWant(b)
		if err != nil {
			t.Fatalf("%d: %v\n%s", i, err, b)
		}
		if w.Exit != exit || !bytes.Equal(w.Stdout, out) ||
			!bytes.Equal(w.Stderr, errs) {
			t.Fatalf("%d: got %+v from\n%s", i, w, b)
		}
	}
	for _, bad := range []string{"", "exit 0\n", "exit x\n== stdout\n" +
		"== stderr\n", "exit 0\n== stdout\nno newline== stderr\n"} {
		if _, err := ParseWant([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestNVCRoundTrip(t *testing.T) {
	c := ConformanceCase{Feature: "f", Name: "c", Std: "2019", Top: "tb"}
	src := []byte("entity tb is end entity;\n")
	for _, out := range []string{"", "** Note: 0ms+0: hi\n", "no nl"} {
		b := c.RenderNVC("1.23.0", src, 1, out)
		r, err := ParseNVC(b)
		if err != nil {
			t.Fatalf("%q: %v", out, err)
		}
		wantOut := out
		if out == "no nl" {
			wantOut += "\n"
		}
		if r.Version != "1.23.0" || r.Exit != 1 || r.Output != wantOut ||
			r.SourceHash != SourceHash(src) ||
			r.Command != strings.Join(c.NVCArgs(), " ") {
			t.Errorf("%q: %+v", out, r)
		}
		// Every header line is required, in order.
		lines := strings.SplitAfter(string(b), "\n")
		for i := range 6 {
			cut := strings.Join(slices.Delete(slices.Clone(lines), i,
				i+1), "")
			if _, err := ParseNVC([]byte(cut)); err == nil {
				t.Errorf("accepted without header line %d", i+1)
			}
		}
	}
}

const nvcOut = `** Error: 1ns+0: boom
   Process :t_tb:_p0 at t.vhd:5
** Error: 1ns+0: Assertion violation.
   > t.vhd:9
   |
 9 |     assert false;
   |            ^^^^^
** Warning: 1ns+0: w
   Process :t_tb:_p0 at t.vhd:5
** Note: 1ns+0: multi
line
   Process :t_tb:_p0 at t.vhd:5
** Note: 1ns+0: FINISH called
   Procedure FINISH [] at lib/std.19/env-body.vhd:48
   Process :t_tb:_p0 at t.vhd:12
`

const gohardOut = `t.vhd:8:5: error[sim/assert-error]: assertion error: boom
t.vhd:9:5: error[sim/assert-error]: assertion error: Assertion violation.
t.vhd:10:5: warning[sim/assert-warning]: report warning: w
t.vhd:11:5: hint[sim/assert-note]: report note: multi\nline
summary: 2 errors, 1 warnings, 1 hints, 1 files
`

// TestMessages: the same run as NVC and GoHard print it (recorded
// with nvc 1.23.0) gives the same messages; NVC's FINISH note and
// location lines are not messages.
func TestMessages(t *testing.T) {
	n, g := NVCMessages(nvcOut), GoHardMessages([]byte(gohardOut))
	if len(n) != 4 || !slices.Equal(n, g) {
		t.Errorf("NVC %q\nGoHard %q", n, g)
	}
}

func TestCheckNVC(t *testing.T) {
	c := ConformanceCase{Feature: "f", Name: "t", Std: "2019",
		Top: "t_tb"}
	src := []byte("entity t_tb is end entity;\n")
	rec := c.RenderNVC("1.23.0", src, 1, nvcOut)
	want := &Want{Exit: 1, Stdout: []byte(gohardOut)}
	if p := c.CheckNVC(src, rec, want); len(p) > 0 {
		t.Errorf("matching recording: %q", p)
	}
	// Stale: another source, another command.
	if p := c.CheckNVC(append(src, ' '), rec, want); len(p) != 1 {
		t.Errorf("stale source: %q", p)
	}
	d := c
	d.Top = "other"
	if p := d.CheckNVC(src, rec, want); len(p) != 1 {
		t.Errorf("stale command: %q", p)
	}
	// A different message or pass/fail is a problem unless the case
	// says NVC differs or is pending.
	other := &Want{Exit: 0, Stdout: []byte(strings.Replace(gohardOut,
		"boom", "bang", 1))}
	if p := c.CheckNVC(src, rec, other); len(p) != 2 {
		t.Errorf("different run: %q", p)
	}
	for _, e := range []ConformanceCase{
		{Feature: "f", Name: "t", Std: "2019", Top: "t_tb",
			NVCDiffers: "x"},
		{Feature: "f", Name: "t", Std: "2019", Top: "t_tb", Pending: "x"},
	} {
		if p := e.CheckNVC(src, rec, other); len(p) > 0 {
			t.Errorf("%+v: %q", e, p)
		}
	}
	if p := c.CheckNVC(src, []byte("junk"), want); len(p) != 1 {
		t.Errorf("junk recording: %q", p)
	}
}
