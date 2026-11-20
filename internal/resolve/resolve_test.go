package resolve

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
)

var files = map[string]string{
	"pkg.vhd": `library ieee;
use ieee.numeric_std.all;
package defs is
  constant WIDTH : natural := 8;
  function inc(x : natural) return natural;
end package defs;
package body defs is
  function inc(x : natural) return natural is
  begin
    return x + 1;
  end function inc;
end package body defs;
`,
	"top.vhd": `library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
use work.defs.all;
entity top is
  port (a, b : in unsigned(WIDTH - 1 downto 0));
end entity top;
architecture rtl of top is
  signal sum : unsigned(WIDTH - 1 downto 0);
  signal n : natural := inc(WIDTH);
begin
  sum <= a + b;
  p: process (a) is
    variable v : natural;
  begin
    v := inc(v);
  end process;
end architecture;
`,
}

func result(t *testing.T) *sema.Result {
	t.Helper()
	var srcs []sema.Source
	for _, p := range []string{"pkg.vhd", "top.vhd"} {
		srcs = append(srcs, sema.Source{Path: p, Text: []byte(files[p])})
	}
	r := sema.Analyze(sema.Input{Files: srcs, Config: config.Defaults()})
	if len(r.Diagnostics) > 0 {
		t.Fatalf("diagnostics: %v", r.Diagnostics)
	}
	return r
}

// off returns the offset of the nth occurrence of s in path.
func off(t *testing.T, path, s string, nth int) int {
	t.Helper()
	text := files[path]
	from := 0
	for {
		i := strings.Index(text[from:], s)
		if i < 0 {
			t.Fatalf("%s: no %q #%d", path, s, nth)
		}
		if nth == 0 {
			return from + i
		}
		nth--
		from += i + 1
	}
}

func locs(ls []Location) string {
	var out []string
	for _, l := range ls {
		out = append(out, fmt.Sprintf("%s:%d", l.Path, l.Span.Start))
	}
	return strings.Join(out, " ")
}

func TestDefinition(t *testing.T) {
	r := result(t)
	got := locs(Definition(r, "top.vhd", off(t, "top.vhd", "WIDTH", 0)))
	if want := fmt.Sprintf("pkg.vhd:%d", off(t, "pkg.vhd", "WIDTH",
		0)); got != want {
		t.Errorf("WIDTH: %s, want %s", got, want)
	}
	// The operator lands in the bundled numeric_std.
	d := Definition(r, "top.vhd", off(t, "top.vhd", "+", 0))
	if len(d) != 1 || !d[0].Bundled ||
		d[0].Path != "ieee/numeric_std.vhdl" {
		t.Errorf("a + b: %+v", d)
	}
	// Definition on a declaration is the declaration itself.
	got = locs(Definition(r, "pkg.vhd", off(t, "pkg.vhd", "WIDTH", 0)))
	if want := fmt.Sprintf("pkg.vhd:%d", off(t, "pkg.vhd", "WIDTH",
		0)); got != want {
		t.Errorf("on the declaration: %s", got)
	}
}

func TestReferences(t *testing.T) {
	r := result(t)
	got := locs(References(r, "pkg.vhd", off(t, "pkg.vhd", "inc", 0),
		true))
	want := []string{
		fmt.Sprintf("pkg.vhd:%d", off(t, "pkg.vhd", "inc", 0)),
		fmt.Sprintf("pkg.vhd:%d", off(t, "pkg.vhd", "inc", 1)),
		fmt.Sprintf("pkg.vhd:%d", off(t, "pkg.vhd", "inc", 2)),
		fmt.Sprintf("top.vhd:%d", off(t, "top.vhd", "inc", 0)),
		fmt.Sprintf("top.vhd:%d", off(t, "top.vhd", "inc", 1)),
	}
	if got != strings.Join(want, " ") {
		t.Errorf("references of inc:\n%s\nwant\n%s", got,
			strings.Join(want, " "))
	}
	// The same set from any occurrence, without the declarations.
	from := locs(References(r, "top.vhd", off(t, "top.vhd", "inc", 1),
		false))
	if from != strings.Join(want[1:], " ") {
		t.Errorf("from a use: %s", from)
	}
}

func TestHover(t *testing.T) {
	r := result(t)
	h, ok := HoverAt(r, "top.vhd", off(t, "top.vhd", "+", 0))
	if !ok || !strings.Contains(h.Markdown, `function "+" `+
		`[UNRESOLVED_UNSIGNED, UNRESOLVED_UNSIGNED return `+
		`UNRESOLVED_UNSIGNED] from ieee.numeric_std`) {
		t.Errorf("hover +: %q", h.Markdown)
	}
	h, _ = HoverAt(r, "top.vhd", off(t, "top.vhd", "sum", 1))
	if !strings.Contains(h.Markdown,
		"signal sum : UNSIGNED(WIDTH - 1 downto 0)") {
		t.Errorf("hover sum: %q", h.Markdown)
	}
}

func TestRename(t *testing.T) {
	r := result(t)
	if _, ok := PrepareRename(r, "top.vhd", off(t, "top.vhd", "+",
		0)); ok {
		t.Error("a bundled operator is not renamable")
	}
	sp, ok := PrepareRename(r, "top.vhd", off(t, "top.vhd", "inc", 0))
	if !ok || sp.Len() != 3 {
		t.Fatalf("prepare inc: %v %v", sp, ok)
	}
	edits, err := Rename(r, "top.vhd", off(t, "top.vhd", "inc", 0),
		"step")
	if err != nil || len(edits) != 5 {
		t.Fatalf("rename: %v %v", edits, err)
	}
	// Applying the edits renames every occurrence, end name included.
	out := apply(files, edits)
	if strings.Contains(strings.ToLower(out["pkg.vhd"]+out["top.vhd"]),
		"inc") || !strings.Contains(out["pkg.vhd"], "end function step") {
		t.Errorf("renamed:\n%s%s", out["pkg.vhd"], out["top.vhd"])
	}
	if _, err := Rename(r, "top.vhd", off(t, "top.vhd", "inc", 0),
		"signal"); err != ErrBadName {
		t.Errorf("reserved word: %v", err)
	}
	if _, err := Rename(r, "top.vhd", off(t, "top.vhd", "inc", 0),
		"two words"); err != ErrBadName {
		t.Errorf("two words: %v", err)
	}
}

func apply(src map[string]string, edits []Edit) map[string]string {
	out := map[string]string{}
	for p, s := range src {
		out[p] = s
	}
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		s := out[e.Path]
		out[e.Path] = s[:e.Span.Start] + e.NewText + s[e.Span.End:]
	}
	return out
}

func TestComplete(t *testing.T) {
	r := result(t)
	at := off(t, "top.vhd", "v := inc", 0)
	var labels []string
	for _, it := range Complete(r, "top.vhd", at) {
		labels = append(labels, strings.ToLower(it.Label))
	}
	for _, want := range []string{"v", "sum", "n", "a", "b", "width",
		"inc", "to_unsigned", "std_logic", "integer", "top"} {
		if !slices.Contains(labels, want) {
			t.Errorf("completion lacks %s", want)
		}
	}
	// Before the variable's declaration it is not visible.
	before := off(t, "top.vhd", "p: process", 0)
	for _, it := range Complete(r, "top.vhd", before) {
		if strings.ToLower(it.Label) == "v" {
			t.Error("v is visible before its process")
		}
	}
	if !slices.IsSortedFunc(Complete(r, "top.vhd", at), func(a,
		b Item) int {
		return strings.Compare(strings.ToLower(a.Label),
			strings.ToLower(b.Label))
	}) {
		t.Error("completion is not sorted")
	}
}

func TestSymbols(t *testing.T) {
	r := result(t)
	ds := DocumentSymbols(r, "top.vhd")
	if len(ds) != 2 || ds[0].Name != "top" || ds[1].Name != "rtl" {
		t.Fatalf("document symbols: %+v", ds)
	}
	var names []string
	for _, c := range ds[1].Children {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "p,sum,n" &&
		strings.Join(names, ",") != "sum,n,p" {
		t.Errorf("architecture children: %v", names)
	}
	ws := WorkspaceSymbols(r, "wid")
	if len(ws) != 1 || ws[0].Name != "WIDTH" {
		t.Errorf("workspace symbols: %+v", ws)
	}
	all := WorkspaceSymbols(r, "")
	if len(all) < 5 {
		t.Errorf("workspace symbols: %d", len(all))
	}
}

// constrainedSrc declares objects whose subtype indications carry an
// index constraint, a scalar range, a record constraint or an element
// constraint. Names are unique so a hover can be aimed at one of them.
const constrainedSrc = `library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity e is
  generic (
    gwidth : natural := 8;
    grange : integer range 0 to 7 := 4
  );
  port (
    pa, pb : in  unsigned(gwidth - 1 downto 0);
    pflag  : in  std_logic;
    pcnt   : out integer range 0 to 15
  );
end entity e;

architecture rtl of e is
  type rec is record
    edata : unsigned;
    en    : integer;
  end record;
  type rec2 is record
    erange : integer range 0 to 9;
    evec   : unsigned(7 downto 0);
  end record;
  type mat is array (natural range <>) of unsigned;
  type mem is array (natural range <>, natural range <>) of bit;
  type Word is array (natural range <>) of bit;
  subtype byte is unsigned(7 downto 0);

  -- Sum of the two operands.
  signal added : unsigned(7 downto 0);
  signal wide : unsigned(gwidth - 1 downto 0);
  signal plain : std_logic;
  signal named : byte;
  signal recsig : rec(edata(7 downto 0), en range 0 to 3);
  signal matsig : mat(0 to 1)(7 downto 0);
  signal memsig : mem(0 to 3, 7 downto 0);
  signal wordsig : Word(7 downto 0);
  signal ch : character range ' ' to '~';
  signal reslv : resolved std_ulogic;
  signal elemr : (resolved) unsigned(7 downto 0);
  signal attrng : unsigned(added'range);
  signal bvnat : bit_vector(natural range 0 to 3);
  signal lo, hi : unsigned(3 downto 0);
  signal multiline : unsigned(
    7 downto 0);
  signal neg : integer range -1 to 1;
  constant mask : unsigned(7 downto 0) := (others => '1');
  constant clim : integer range 0 to 15 := 4;
  alias hi4 : unsigned(3 downto 0) is added(7 downto 4);
  alias whole is added;

  function f(px : unsigned(7 downto 0)) return std_ulogic is
  begin
    return px(px'low);
  end function;

  function g(mx : mat(open)(7 downto 0)) return bit is
  begin
    return '0';
  end function;

  procedure setv(vx : integer range 0 to 9) is
  begin
    null;
  end procedure;
begin
  added <= pa;
  wide <= pb;
  plain <= pflag;
  named <= added;
  recsig.edata <= added;
  recsig.en <= grange;
  matsig(0) <= added;
  memsig(0, 0) <= '0';
  wordsig <= (others => '0');
  ch <= 'a';
  reslv <= '0';
  elemr <= added;
  attrng <= whole;
  bvnat <= (others => '0');
  lo <= mask(3 downto 0);
  multiline <= added;
  neg <= 0;
  pcnt <= clim;
  p : process (lo, added) is
    variable vv : unsigned(3 downto 0);
  begin
    vv := hi4;
    hi <= vv;
    if f(added) = '1' then
      setv(grange);
    end if;
  end process;
end architecture;
`

// TestHoverConstrainedSubtype is the markdown textDocument/hover gives
// a client for a constrained declaration (#117): the type mark as
// declared, plus the constraint written on that declaration.
func TestHoverConstrainedSubtype(t *testing.T) {
	r := analyzeSrc(t, constrainedSrc, "")
	code := func(line string) string {
		return "```vhdl\n" + line + "\n```"
	}
	added := code("signal added : UNSIGNED(7 downto 0)") +
		"\n\nSum of the two operands."
	cases := []struct {
		word string
		nth  int
		want string
	}{
		{"added", 0, added},
		{"added", 1, added}, // the use in added'range
		{"wide", 0, code("signal wide : UNSIGNED(gwidth - 1 downto 0)")},
		{"plain", 0, code("signal plain : STD_LOGIC")},
		{"named", 0, code("signal named : byte")},
		{"recsig", 0, code("signal recsig : " +
			"rec(edata(7 downto 0), en range 0 to 3)")},
		{"matsig", 0, code("signal matsig : mat(0 to 1)(7 downto 0)")},
		{"memsig", 0, code("signal memsig : mem(0 to 3, 7 downto 0)")},
		{"wordsig", 0, code("signal wordsig : Word(7 downto 0)")},
		{"ch", 0, code("signal ch : CHARACTER range ' ' to '~'")},
		{"reslv", 0, code("signal reslv : resolved STD_ULOGIC")},
		{"elemr", 0, code("signal elemr : " +
			"(resolved) UNSIGNED(7 downto 0)")},
		{"attrng", 0, code("signal attrng : UNSIGNED(added'range)")},
		{"bvnat", 0, code("signal bvnat : " +
			"BIT_VECTOR(natural range 0 to 3)")},
		{"lo", 0, code("signal lo : UNSIGNED(3 downto 0)")},
		{"hi", 0, code("signal hi : UNSIGNED(3 downto 0)")},
		{"multiline", 0, code("signal multiline : UNSIGNED(7 downto 0)")},
		{"neg", 0, code("signal neg : INTEGER range -1 to 1")},
		{"mask", 0, code("constant mask : UNSIGNED(7 downto 0)")},
		{"clim", 0, code("constant clim : INTEGER range 0 to 15")},
		{"gwidth", 0, code("generic gwidth : NATURAL")},
		{"grange", 0, code("generic grange : INTEGER range 0 to 7")},
		{"pa", 0, code("port pa : UNSIGNED(gwidth - 1 downto 0)")},
		{"pb", 0, code("port pb : UNSIGNED(gwidth - 1 downto 0)")},
		{"pflag", 0, code("port pflag : STD_LOGIC")},
		{"pcnt", 0, code("port pcnt : INTEGER range 0 to 15")},
		{"vv", 0, code("variable vv : UNSIGNED(3 downto 0)")},
		{"px", 0, code("parameter px : UNSIGNED(7 downto 0)")},
		{"mx", 0, code("parameter mx : mat(open)(7 downto 0)")},
		{"vx", 0, code("parameter vx : INTEGER range 0 to 9")},
		{"erange", 0, code("element erange : INTEGER range 0 to 9")},
		{"evec", 0, code("element evec : UNSIGNED(7 downto 0)")},
		{"edata", 0, code("element edata : UNSIGNED")},
		{"en", 0, code("element en : INTEGER")},
		{"hi4", 0, code("alias hi4 : UNSIGNED(3 downto 0)")},
		{"whole", 0, code("alias whole : UNSIGNED")},
	}
	for _, c := range cases {
		got := hoverAtWord(t, r, constrainedSrc, c.word, c.nth)
		if got != c.want {
			t.Errorf("%s #%d:\n got %q\nwant %q", c.word, c.nth, got,
				c.want)
		}
	}
	// A subprogram's profile stays the type marks (#118 is separate):
	// the parameter's own hover is what carries the constraint.
	f := hoverAtWord(t, r, constrainedSrc, "f", 0)
	if !strings.Contains(f, "function f [UNSIGNED return STD_ULOGIC]") ||
		strings.Contains(f, "7 downto 0") {
		t.Errorf("function profile should not repeat the constraint: %q",
			f)
	}
	setv := hoverAtWord(t, r, constrainedSrc, "setv", 0)
	if strings.Contains(setv, "range") {
		t.Errorf("procedure profile should not repeat the range: %q",
			setv)
	}
}

// viewSrc is a VHDL-2019 mode view whose port subtype is constrained.
const viewSrc = `library ieee;
use ieee.numeric_std.all;
package pkg is
  type rec is record
    data : unsigned;
    n    : integer;
  end record;
  type recs is array (natural range <>) of rec;
  view rv of rec is
    data : out;
    n    : in;
  end view;
end package;

use work.pkg.all;
entity e is
  port (
    p : view rv of rec(data(7 downto 0), n range 0 to 3);
    q : view (rv) of recs(0 to 1)(data(3 downto 0), n range 0 to 1)
  );
end entity e;
`

func TestHoverModeViewConstraint(t *testing.T) {
	r := analyzeSrc(t, viewSrc, "2019")
	code := func(line string) string {
		return "```vhdl\n" + line + "\n```"
	}
	cases := []struct {
		word, want string
	}{
		{"p", code("port p : rec(data(7 downto 0), n range 0 to 3)")},
		{"q", code("port q : recs(0 to 1)(data(3 downto 0), " +
			"n range 0 to 1)")},
	}
	for _, c := range cases {
		got := hoverAtWord(t, r, viewSrc, c.word, 0)
		if got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.word, got, c.want)
		}
	}
}

func analyzeSrc(t *testing.T, src, std string) *sema.Result {
	t.Helper()
	cfg := config.Defaults()
	if std != "" {
		cfg.Standard = std
	}
	r := sema.Analyze(sema.Input{Files: []sema.Source{{
		Path: "c.vhd", Text: []byte(src)}}, Config: cfg})
	if len(r.Diagnostics) > 0 {
		t.Fatalf("diagnostics: %v", r.Diagnostics)
	}
	return r
}

func hoverAtWord(t *testing.T, r *sema.Result, src, word string,
	nth int,
) string {
	t.Helper()
	h, ok := HoverAt(r, "c.vhd", nthWord(t, src, word, nth))
	if !ok {
		t.Fatalf("%s #%d: no hover", word, nth)
	}
	return h.Markdown
}

func nthWord(t *testing.T, text, word string, nth int) int {
	t.Helper()
	low := strings.ToLower(text)
	w := strings.ToLower(word)
	wordByte := func(c byte) bool {
		return c == '_' || c >= 'a' && c <= 'z' ||
			c >= '0' && c <= '9'
	}
	from := 0
	for {
		i := strings.Index(low[from:], w)
		if i < 0 {
			t.Fatalf("no occurrence %d of %q", nth, word)
		}
		i += from
		end := i + len(w)
		if (i == 0 || !wordByte(low[i-1])) &&
			(end == len(low) || !wordByte(low[end])) {
			if nth == 0 {
				return i
			}
			nth--
		}
		from = i + 1
	}
}
