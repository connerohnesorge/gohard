package sema

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
)

// viewPkg is a package of record types and views: a flat view, a view
// with a subview, one with an array subview, and a converse alias.
const viewPkg = `package pkg is
  type rec is record a : bit; b : bit; c : bit; end record;
  type subr is record x : bit; y : bit; end record;
  type outer is record s : subr; d : bit; end record;
  type subarr is array (0 to 1) of subr;
  type outer2 is record sa : subarr; e : bit; end record;
  view v of rec is a : out; b : in; c : inout; end view;
  view sv of subr is x : out; y : in; end view;
  view ov of outer is s : view sv; d : out; end view;
  view ov2 of outer2 is sa : view (sv); e : buffer; end view;
  alias cv is v'converse;
end package;
`

func analyze2019(t *testing.T, files map[string]string) *Result {
	t.Helper()
	return analyze(t, files, func(c *config.Config) {
		c.Standard = "2019"
	})
}

// TestModeViewModel: views record the mode of every element in record
// order; 'CONVERSE swaps in and out, makes buffer in and keeps inout,
// recursively, and the converse of the converse is the original.
func TestModeViewModel(t *testing.T) {
	files := map[string]string{"pkg.vhd": viewPkg,
		"e.vhd": `use work.pkg.all;
entity e is port (p : view ov2; q : view cv; r : view v'converse of rec);
end;
`}
	r := analyze2019(t, files)
	noErrors(t, r)
	modes := func(v *View) string {
		var b []string
		v.leaves("", 0, func(p string, m Mode) {
			b = append(b, p+":"+modeText(m))
		})
		return strings.Join(b, " ")
	}
	cases := []struct {
		word string
		nth  int
		want string
		name string
	}{
		{"v", 0, "a:out b:in c:inout", "v"},
		{"cv", 0, "a:in b:out c:inout", "v'converse"},
		{"ov2", 0, "sa(i).x:out sa(i).y:in e:buffer", "ov2"},
	}
	for _, c := range cases {
		d := declOf(t, r, files, "pkg.vhd", c.word, c.nth)
		if d.View == nil {
			t.Fatalf("%s has no view", c.word)
		}
		if got := modes(d.View); got != c.want {
			t.Errorf("%s: %s, want %s", c.word, got, c.want)
		}
		if d.View.Name() != c.name {
			t.Errorf("%s: name %s", c.word, d.View.Name())
		}
		// Buffer becomes in, whose converse is out: the converse is an
		// involution only on views without buffer.
		back := d.View.ConverseView().ConverseView()
		if !strings.Contains(c.want, "buffer") &&
			modes(back) != modes(d.View) ||
			back.Converse != d.View.Converse {
			t.Errorf("%s: converse of converse differs", c.word)
		}
	}
	for _, c := range []struct {
		port, want string
	}{
		{"p", "sa(i).x:out sa(i).y:in e:buffer"},
		{"q", "a:in b:out c:inout"},
		{"r", "a:in b:out c:inout"},
	} {
		d := declOf(t, r, files, "e.vhd", c.port, 0)
		if d.Mode != ModeView || d.View == nil || d.Type.Unknown() {
			t.Fatalf("port %s: mode %v view %v type %v", c.port, d.Mode,
				d.View, d.Type)
		}
		if got := modes(d.View); got != c.want {
			t.Errorf("port %s: %s, want %s", c.port, got, c.want)
		}
	}
	// The element names of a view refer to the record elements, and the
	// view names in ports and aliases to the views.
	if got := where(declOf(t, r, files, "pkg.vhd", "a", 1)); got !=
		"pkg.vhd:a" {
		t.Errorf("view element a -> %s", got)
	}
	if d := declOf(t, r, files, "e.vhd", "cv", 0); d.Kind != DeclAlias {
		t.Errorf("cv -> %s", d.Kind)
	}
	if d := declOf(t, r, files, "pkg.vhd", "sv", 1); d.Kind !=
		DeclModeView {
		t.Errorf("view sv -> %s", d.Kind)
	}
}

// TestModeViewMisuse: each misuse of a view, a view port or 'CONVERSE
// is one error at the offending name (the cases NVC 1.23 reports, plus
// subview elements, which it misses).
func TestModeViewMisuse(t *testing.T) {
	cases := []struct {
		name, text string
		want       []string
	}{
		{"not-record", `package bad is
  view v1 of bit is a : out; end view;
end package;`, []string{
			"2:14: sema/error: a mode view must be of a record type, " +
				"not BIT"}},
		{"missing", `package bad is
  type rec is record a : bit; b : bit; c : bit; end record;
  view v2 of rec is a : out; end view;
end package;`, []string{
			"3:8: sema/error: view v2 gives no mode for elements b, c " +
				"of rec"}},
		{"twice", `package bad is
  type rec is record a : bit; b : bit; end record;
  view v3 of rec is a : out; b : in; a : in; end view;
end package;`, []string{
			"3:38: sema/error: element a is given a mode twice in view v3"}},
		{"no-element", `package bad is
  type rec is record a : bit; b : bit; end record;
  view v4 of rec is a : out; b : in; z : in; end view;
end package;`, []string{"3:38: sema/error: rec has no element z"}},
		{"linkage", `package bad is
  type rec is record a : bit; b : bit; end record;
  view v6 of rec is a : out; b : linkage; end view;
end package;`, []string{
			"3:34: sema/error: an element of a mode view cannot have " +
				"mode linkage"}},
		{"subview-type", `use work.pkg.all;
package bad is
  type r2 is record s : rec; t : bit; end record;
  view w of r2 is s : view sv; t : in; end view;
end package;`, []string{
			"4:19: sema/error: element s is rec, but view sv is of subr"}},
		{"not-a-view", `use work.pkg.all;
package bad is
  alias xx is rec'converse;
end package;`, []string{
			"3:15: sema/error: the prefix of 'converse must be a mode " +
				"view, not rec"}},
		{"port-not-view", `use work.pkg.all;
entity bad is port (p : view rec); end;`, []string{
			"2:30: sema/error: rec is not a mode view"}},
		{"port-type", `use work.pkg.all;
entity bad is port (p : view v of subr); end;`, []string{
			"2:35: sema/error: subr is not compatible with view v, " +
				"which is of rec"}},
		{"port-array", `use work.pkg.all;
entity bad is port (p : view (sv) of rec); end;`, []string{
			"2:38: sema/error: rec is not an array type, so it cannot " +
				"have view (sv)"}},
		{"assign-in", `use work.pkg.all;
entity bad is port (p : view v; p3 : view cv; p4 : view ov;
  p5 : view ov2);
end;
architecture a of bad is begin
  p.a <= '1';
  p.c <= '1';
  p.b <= '1';
  p3.a <= '1';
  p <= ('1', '1', '1');
  p4.s.y <= '1';
  p5.sa(1).y <= '1';
  p5.sa(1).x <= '1';
  p5.e <= '1';
  p4.d <= p.b;
end;`, []string{
			"8:3: sema/error: cannot assign to p.b: it has mode in in " +
				"view v of port p",
			"9:3: sema/error: cannot assign to p3.a: it has mode in in " +
				"view v'converse of port p3",
			"10:3: sema/error: cannot assign to p: element p.b has mode " +
				"in in view v of port p",
			"11:3: sema/error: cannot assign to p4.s.y: it has mode in " +
				"in view ov of port p4",
			"12:3: sema/error: cannot assign to p5.sa.y: it has mode in " +
				"in view ov2 of port p5"}},
		{"proc-param", `use work.pkg.all;
entity bad is end;
architecture a of bad is
  procedure drive(signal q : view v) is
  begin
    q.a <= '1';
    q.b <= '0';
  end procedure;
begin end;`, []string{
			"7:5: sema/error: cannot assign to q.b: it has mode in in " +
				"view v of parameter q"}},
		{"constant-view", `use work.pkg.all;
entity bad is end;
architecture a of bad is
  procedure pr(constant q : view v) is begin end;
begin end;`, []string{
			"4:16: sema/error: an interface object of mode view must " +
				"be a signal"}},
		{"port-map", `use work.pkg.all;
entity sub is port (p : view v; o : out bit); end;
architecture a of sub is begin end;
use work.pkg.all;
entity bad is port (q : in rec; r : view cv; s : view v); end;
architecture a of bad is
  signal t : rec;
begin
  u1: entity work.sub port map (p => q, o => open);
  u2: entity work.sub port map (p => r, o => open);
  u3: entity work.sub port map (p => s, o => s.b);
  u4: entity work.sub port map (p => t, o => r.a);
  u5: entity work.sub port map (t, s.a);
end;`, []string{
			"9:38: sema/error: view v of port p gives element a mode " +
				"out, but it has mode in in the actual q, a port of mode in",
			"10:38: sema/error: view v of port p gives element a mode " +
				"out, but it has mode in in view v'converse of the " +
				"actual r",
			"11:46: sema/error: port o has mode out, but its actual s.b " +
				"has mode in in view v",
			"12:46: sema/error: port o has mode out, but its actual r.a " +
				"has mode in in view v'converse"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{"pkg.vhd": viewPkg,
				"bad.vhd": c.text}
			r := analyze2019(t, files)
			var got []string
			for _, d := range r.Diagnostics {
				if d.Path != "bad.vhd" || d.Severity != 1 {
					t.Errorf("unexpected: %s:%d:%d: %s", d.Path, d.Line,
						d.Col, d.Message)
					continue
				}
				got = append(got, itoa(d.Line)+":"+itoa(d.Col)+": "+
					string(d.Code)+": "+d.Message)
			}
			if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
				t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"),
					strings.Join(c.want, "\n"))
			}
		})
	}
}
