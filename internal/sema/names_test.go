package sema

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/cst"
)

// The name forms of LRM 8 in one design: selected names into records
// (nested, through arrays, function results and access values),
// expanded names (library.package.item, package.item, architecture,
// entity and block names as prefixes), protected-type methods,
// overloaded enumeration literals, object and subprogram aliases,
// operator symbols as names, attribute names (predefined and user
// attributes of signals and entities) and labels.
var nameFiles = map[string]string{
	"p.vhd": `library ieee;
use ieee.std_logic_1164.all;
package p is
  type rec is record
    a : integer;
    b : bit_vector(3 downto 0);
  end record;
  type rec2 is record
    r : rec;
  end record;
  type arr is array (0 to 3) of rec;
  type ptr is access rec;
  type color is (red, green, blue);
  type light is (red, amber, green);
  constant k : integer := 3;
  function f return rec;
  function g(x : integer) return rec;
  type prot is protected
    procedure set(v : integer);
    impure function get return integer;
  end protected;
  attribute myattr : integer;
  function "+"(l : rec; r : integer) return rec;
end package;
package body p is
  function f return rec is begin return (a => 1, b => "0000"); end;
  function g(x : integer) return rec is begin return (a => x, b => "0000"); end;
  type prot is protected body
    variable vv : integer := 0;
    procedure set(v : integer) is begin vv := v; end;
    impure function get return integer is begin return vv; end;
  end protected body;
  function "+"(l : rec; r : integer) return rec is
  begin return (a => l.a + r, b => l.b); end;
end package body;
`,
	"e.vhd": `library ieee;
use ieee.std_logic_1164.all;
use work.p.all;
use work.p;
entity e is
  generic (W : natural := 4);
  port (clk : in std_logic; o : out integer);
  attribute myattr of e : entity is 7;
end;
architecture rtl of e is
  signal s : rec;
  signal s2 : rec2;
  signal sa : arr;
  signal c : color := work.p.red;
  signal l : light := amber;
  signal bv : bit_vector(7 downto 0);
  alias hi : bit_vector(3 downto 0) is bv(7 downto 4);
  alias plus is "+" [rec, integer return rec];
  shared variable sv : prot;
  signal x : integer := work.p.k + p.k + g(2).a + f.a;
  signal y : boolean := c = red;
  signal z : integer := color'pos(blue) + bv'length + e'myattr + W;
  attribute myattr of s : signal is 5;
begin
  s.a <= s2.r.a + sa(1).a + s'myattr;
  s.b <= g(1).b(3 downto 0);
  hi <= "1010";
  o <= rtl.x + e.W;
  process (clk)
    variable pv : ptr;
    variable n : integer;
  begin
    if rising_edge(clk) and clk'event then
      pv := new rec;
      pv.a := 1;
      pv.all.a := 2;
      sv.set(3);
      n := sv.get;
      s <= plus(s, n);
      s <= "+"(s, 1);
      s <= work.p."+"(s, 1);
    end if;
    outer: for i in bv'range loop
      exit outer when i = 2;
      next outer;
    end loop outer;
  end process;
  blk: block
    signal bs : integer;
  begin
    bs <= blk.bs;
  end block blk;
end;
`,
}

// genericFiles: VHDL-2008/2019 names: a generic package and its
// instantiation, a package with a generic package, a generic function
// and its instantiation, a context declaration, a type alias, expanded
// names through a process label, external names.
var genericFiles = map[string]string{
	"g.vhd": `package gp is
  generic (type t; constant init : t; function op(l, r : t) return t);
  function twice(x : t) return t;
  constant start : t := init;
end package;
package body gp is
  function twice(x : t) return t is begin return op(x, x); end;
end package body;
package ip is new work.gp generic map (t => integer, init => 5,
  op => "+");
context ctx is
  library ieee;
  use ieee.std_logic_1164.all;
end context;
package user is
  generic (package q is new work.gp generic map (<>));
  constant u : q.t := q.start;
end package;
package ui is new work.user generic map (q => work.ip);
`,
	"h.vhd": `context work.ctx;
use work.ip.all;
entity h is end;
architecture rtl of h is
  function ident generic (type tt) parameter (x : tt) return tt is
  begin return x; end;
  function ident_int is new ident generic map (tt => integer);
  type color is (red, green, blue);
  alias hue is color;
  signal sl : std_logic;
  signal v : integer := twice(start) + work.ip.twice(1) + ident_int(3);
  signal w : integer := work.ui.u;
  signal c : hue := green;
  signal n : string(1 to 3) := c'simple_name;
  signal se : std.standard.integer := ieee.std_logic_1164.std_logic'pos('1');
  constant pk : integer := <<constant @work.ip.start : integer>>;
begin
  pr: process
    variable pv : integer;
  begin
    pr.pv := 1;
    report sl'path_name & pr'path_name & integer'image(v);
    wait;
  end process;
  sl <= <<signal .h.sl : std_logic>>;
  assert v'stable(1 ns) and v'last_value = 0 and c = hue'high;
end;
`,
}

type nameCase struct {
	path, word string
	nth        int
	dpath      string
	dword      string
	dnth       int
}

// checkNames checks that each occurrence denotes the declaration at the
// given occurrence.
func checkNames(t *testing.T, r *Result, files map[string]string,
	cases []nameCase,
) {
	t.Helper()
	for _, c := range cases {
		d := declOf(t, r, files, c.path, c.word, c.nth)
		if d == nil || d.File == nil || d.File.Path != c.dpath ||
			d.Span.Start != occurrence(t, files, c.dpath, c.dword,
				c.dnth) {
			t.Errorf("%s %s#%d -> %s, want %s:%s#%d", c.path, c.word,
				c.nth, where(d), c.dpath, c.dword, c.dnth)
		}
	}
}

// allResolve checks the properties the language server needs (#25):
// every identifier of a simple name and every suffix of a selected
// name in the project files resolves to a declaration (or an overload
// result), and every reference is listed among its declaration's
// references, so definition and references agree.
func allResolve(t *testing.T, r *Result) {
	t.Helper()
	for _, f := range r.Files {
		if f.Bundled {
			continue
		}
		for n := range f.Root().Descendants() {
			if n.Inactive() {
				continue
			}
			var tok = n.FirstToken()
			switch n.Kind() {
			case kindSimpleName:
			case kindSelectedName:
				tok = n.LastToken()
				if TokenKey(tok) == "all" {
					continue
				}
			default:
				continue
			}
			ref, ok := r.RefAt(f.Path, tok.Span().Start)
			if ok && (ref.Decl != nil || ref.Overload != nil) {
				continue
			}
			if _, ok := r.DeclAt(f.Path, tok.Span().Start); ok {
				continue
			}
			l, c := f.Lines.LineCol(tok.Span().Start)
			t.Errorf("%s:%d:%d: %s does not resolve", f.Path, l, c,
				tok.Text())
		}
		for _, u := range f.Units {
			for _, ref := range u.Refs {
				if ref.Decl == nil {
					continue
				}
				if !slices.ContainsFunc(r.References(ref.Decl),
					func(o Ref) bool {
						return o.File == ref.File && o.Span == ref.Span
					}) {
					l, c := f.Lines.LineCol(ref.Span.Start)
					t.Errorf("%s:%d:%d: not among the references of %s",
						f.Path, l, c, where(ref.Decl))
				}
			}
		}
	}
}

// TestNameForms resolves each name form to its declaration, across
// files, and every name in the design resolves.
func TestNameForms(t *testing.T) {
	r := analyze(t, nameFiles, func(c *config.Config) {
		c.Standard = "2008"
	})
	noErrors(t, r)
	checkNames(t, r, nameFiles, []nameCase{
		{"e.vhd", "a", 0, "p.vhd", "a", 0}, // g(2).a: a call's result
		{"e.vhd", "a", 1, "p.vhd", "a", 0}, // f.a: parameterless call
		{"e.vhd", "a", 2, "p.vhd", "a", 0}, // s.a
		{"e.vhd", "a", 3, "p.vhd", "a", 0}, // s2.r.a: nested record
		{"e.vhd", "a", 4, "p.vhd", "a", 0}, // sa(1).a: array element
		{"e.vhd", "a", 5, "p.vhd", "a", 0}, // pv.a: through access
		{"e.vhd", "a", 6, "p.vhd", "a", 0}, // pv.all.a
		{"e.vhd", "r", 0, "p.vhd", "r", 0}, // s2.r
		{"e.vhd", "b", 1, "p.vhd", "b", 0}, // g(1).b(3 downto 0)
		{"e.vhd", "k", 0, "p.vhd", "k", 0}, // work.p.k
		{"e.vhd", "k", 1, "p.vhd", "k", 0}, // p.k (use work.p)
		{"e.vhd", "p", 3, "p.vhd", "p", 0}, // the package prefix p.k
		{"e.vhd", "set", 0, "p.vhd", "set", 0},
		{"e.vhd", "get", 0, "p.vhd", "get", 0},
		{"e.vhd", "red", 0, "p.vhd", "red", 0}, // work.p.red: color's
		{"e.vhd", "red", 1, "p.vhd", "red", 0}, // c = red: by context
		{"e.vhd", "amber", 0, "p.vhd", "amber", 0},
		{"e.vhd", "blue", 0, "p.vhd", "blue", 0},
		{"e.vhd", "x", 1, "e.vhd", "x", 0},           // rtl.x
		{"e.vhd", "rtl", 1, "e.vhd", "rtl", 0},       // rtl. prefix
		{"e.vhd", "W", 2, "e.vhd", "W", 0},           // e.W
		{"e.vhd", "bs", 2, "e.vhd", "bs", 0},         // blk.bs
		{"e.vhd", "blk", 1, "e.vhd", "blk", 0},       // blk. prefix
		{"e.vhd", "outer", 1, "e.vhd", "outer", 0},   // exit outer
		{"e.vhd", "outer", 2, "e.vhd", "outer", 0},   // next outer
		{"e.vhd", "myattr", 0, "p.vhd", "myattr", 0}, // attribute spec
		{"e.vhd", "myattr", 1, "p.vhd", "myattr", 0}, // e'myattr
		{"e.vhd", "myattr", 3, "p.vhd", "myattr", 0}, // s'myattr
		{"e.vhd", "hi", 1, "e.vhd", "hi", 0},         // object alias
		{"e.vhd", "plus", 1, "e.vhd", "plus", 0},     // subprogram alias
		{"e.vhd", "sv", 1, "e.vhd", "sv", 0},
	})
	allResolve(t, r)
	// The alias with a signature denotes the user "+".
	plus := declOf(t, r, nameFiles, "e.vhd", "plus", 0)
	if plus.Target == nil || plus.Target.Denotes().File == nil ||
		plus.Target.Denotes().File.Path != "p.vhd" {
		t.Errorf("plus denotes %s", where(plus.Target))
	}
}

// TestGenericNames resolves VHDL-2008 names: generic packages and their
// instances, interface packages, generic subprograms and their
// instances, context declarations, type aliases, process-label
// expanded names, external names (typed by their subtype; a package
// pathname also resolves its names).
func TestGenericNames(t *testing.T) {
	for _, std := range []string{"2008", "2019"} {
		r := analyze(t, genericFiles, func(c *config.Config) {
			c.Standard = std
		})
		noErrors(t, r)
		checkNames(t, r, genericFiles, []nameCase{
			{"h.vhd", "twice", 0, "g.vhd", "twice", 0},
			{"h.vhd", "twice", 1, "g.vhd", "twice", 0},
			{"h.vhd", "start", 0, "g.vhd", "start", 0},
			{"h.vhd", "start", 1, "g.vhd", "start", 0}, // @work.ip.start
			{"h.vhd", "ip", 2, "g.vhd", "ip", 0},       // @work.ip
			{"h.vhd", "ctx", 0, "g.vhd", "ctx", 0},
			{"h.vhd", "ident", 1, "h.vhd", "ident", 0},
			{"h.vhd", "ident_int", 1, "h.vhd", "ident_int", 0},
			{"h.vhd", "u", 0, "g.vhd", "u", 0},
			{"h.vhd", "ui", 0, "g.vhd", "ui", 0},
			{"h.vhd", "pv", 1, "h.vhd", "pv", 0},
			{"h.vhd", "pr", 1, "h.vhd", "pr", 0},
			{"h.vhd", "hue", 2, "h.vhd", "hue", 0},
			{"h.vhd", "green", 0, "h.vhd", "green", 0},
			{"g.vhd", "start", 1, "g.vhd", "start", 0},
			{"g.vhd", "t", 2, "g.vhd", "t", 0},
			{"g.vhd", "gp", 3, "g.vhd", "gp", 0},
		})
		// The path of the hierarchical external name is not a name
		// (it is resolved at elaboration); every name resolves now.
		allResolve(t, r)
		pk := declOf(t, r, genericFiles, "h.vhd", "pk", 0)
		if pk.Value == nil || pk.Value.Decl == nil ||
			where(pk.Value.Decl) != "g.vhd:start" {
			t.Errorf("%s: the package pathname denotes %v", std,
				pk.Value)
		}
	}
}

// TestUseVisibility: LRM 12.4 potential visibility. Two use clauses
// making homographs visible: non-overloadable ones cancel (rule c),
// overloadable ones with one profile make a call ambiguous; one named
// entity reached twice is one declaration. An object alias is a
// declaration of its own (GHDL and NVC report the conflict in every
// standard); a type alias denotes the type itself from VHDL-2008 on
// but conflicts with it before; aliases of subprograms and literals
// never conflict. `use p.T` makes T's literals visible from 2008 on.
func TestUseVisibility(t *testing.T) {
	pkgs := `package p1 is
  function f(x : integer) return integer;
  function h(x : integer) return integer;
  type color is (red, green);
  constant k : integer := 1;
end;
package body p1 is
  function f(x : integer) return integer is begin return x; end;
  function h(x : integer) return integer is begin return x; end;
end;
package p2 is
  function f(x : integer) return integer;
  alias h is work.p1.h [integer return integer];
  alias k is work.p1.k;
  alias color is work.p1.color;
  alias red is work.p1.red [return work.p1.color];
end;
package body p2 is
  function f(x : integer) return integer is begin return x; end;
end;
`
	use := `use work.p1.all, work.p2.all;
entity e is end;
architecture a of e is
  signal s : integer := f(1) + h(1) + k;
  signal c : color := red;
begin end;
`
	typeUse := `use work.p1.color;
entity t is end;
architecture a of t is
  signal c : color := green;
begin end;
`
	cases := []struct {
		std, file, text string
		want            []string
	}{
		{"1993", "e.vhd", use, []string{
			"e.vhd:4:25: sema/ambiguous-overload",
			"e.vhd:4:39: sema/error: k is not visible",
			"e.vhd:5:14: sema/error: color is not visible"}},
		{"2008", "e.vhd", use, []string{
			"e.vhd:4:25: sema/ambiguous-overload",
			"e.vhd:4:39: sema/error: k is not visible"}},
		{"1993", "t.vhd", typeUse, []string{
			"t.vhd:4:23: sema/undefined: green is not declared"}},
		{"2008", "t.vhd", typeUse, nil},
	}
	for _, c := range cases {
		files := map[string]string{"p.vhd": pkgs, c.file: c.text}
		r := analyze(t, files, func(cf *config.Config) {
			cf.Standard = c.std
		})
		var got []string
		for _, d := range r.Diagnostics {
			got = append(got, d.Path+":"+itoa(d.Line)+":"+itoa(d.Col)+
				": "+string(d.Code)+": "+d.Message)
		}
		if len(got) != len(c.want) {
			t.Errorf("%s %s: got\n%s", c.std, c.file,
				strings.Join(got, "\n"))
			continue
		}
		for i := range got {
			if !strings.HasPrefix(got[i], c.want[i]) {
				t.Errorf("%s %s: got %s, want %s", c.std, c.file, got[i],
					c.want[i])
			}
		}
	}
}

// TestNameErrors: misused names are one error each, at the name.
func TestNameErrors(t *testing.T) {
	files := map[string]string{"x.vhd": `package p is
  type rec is record a : integer; end record;
  constant k : integer := 1;
end;
use work.p.all;
entity x is end;
architecture a of x is
  signal s : rec;
  signal n : integer;
  signal i : integer := s.z + work.p.nope + k.a + n(0);
  signal j : integer := s'nope;
begin end;
`}
	r := analyze(t, files, nil)
	want := []string{
		"x.vhd:10:27: sema/error: rec has no element z",
		"x.vhd:10:38: sema/error: nope is not declared in p",
		"x.vhd:10:47: sema/error: the prefix of a is INTEGER, not a record",
		"x.vhd:10:51: sema/error: n is INTEGER, which cannot be indexed",
		"x.vhd:11:27: sema/undefined: nope is not declared",
	}
	got := strings.Split(strings.TrimSpace(diagText(r)), "\n")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"),
			strings.Join(want, "\n"))
	}
}

// TestIndexOrCall: prefix(args) is a call, an index or slice of an
// array object, of an access value, or of the result of a
// parameterless function, a type conversion or an index constraint;
// the expected type decides between a call with the arguments and an
// index of a parameterless call.
func TestIndexOrCall(t *testing.T) {
	files := map[string]string{"i.vhd": `package q is
  type mat is array (0 to 1, 0 to 1) of integer;
  function vec return bit_vector;
  function vec(n : integer) return bit_vector;
  function m return mat;
  type ptr is access bit_vector;
end;
package body q is
  function vec return bit_vector is begin return "0101"; end;
  function vec(n : integer) return bit_vector is begin return "01"; end;
  function m return mat is begin return ((1, 2), (3, 4)); end;
end;
use work.q.all;
entity i is end;
architecture a of i is
  subtype nib is bit_vector(0 to 3);
  signal s : nib := vec;
  signal b : bit := vec(2)(0);
  signal b2 : bit := vec(0);
  signal sl : bit_vector(0 to 1) := vec(0 to 1);
  signal sl2 : bit_vector(0 to 1) := vec(3)(0 to 1);
  signal r : real := 1.5;
  signal n : integer := integer(r) + m(0, 1);
  signal bb : bit := s(nib'low);
  signal t : bit_vector(0 to 1) := s(nib'range)(0 to 1);
begin
  process
    variable p : ptr;
  begin
    p := new bit_vector'("1010");
    p(0) := '1';
    p.all(1) := '0';
    p(0 to 1) := "00";
    wait;
  end process;
end;
`}
	r := analyze(t, files, func(c *config.Config) { c.Standard = "2008" })
	noErrors(t, r)
	checkNames(t, r, files, []nameCase{
		{"i.vhd", "vec", 4, "i.vhd", "vec", 0}, // := vec
		{"i.vhd", "vec", 5, "i.vhd", "vec", 1}, // vec(2)(0): the call
		{"i.vhd", "vec", 6, "i.vhd", "vec", 0}, // vec(0): index of vec
		{"i.vhd", "vec", 7, "i.vhd", "vec", 0}, // vec(0 to 1): slice
		{"i.vhd", "vec", 8, "i.vhd", "vec", 1}, // vec(3)(0 to 1)
		{"i.vhd", "m", 2, "i.vhd", "m", 0},     // m(0, 1)
	})
	allResolve(t, r)
	for _, c := range []struct {
		word string
		want ExprKind
	}{{"b2", ExprIndex}, {"sl", ExprSlice}, {"b", ExprIndex},
		{"sl2", ExprSlice}, {"bb", ExprIndex}} {
		d := declOf(t, r, files, "i.vhd", c.word, 0)
		if d.Value == nil || d.Value.Kind != c.want {
			t.Errorf("%s := %v, want kind %v", c.word, d.Value, c.want)
		}
	}
}

// TestCorpusNames: in every v1.0 oracle corpus design (each analyzes
// without errors, TestCorpus) every simple name and selected-name
// suffix resolves, and definition and references agree.
func TestCorpusNames(t *testing.T) {
	designs, run := loadCorpus(t)
	n := 0
	for _, d := range designs {
		if d.Milestone != "v1.0" {
			continue
		}
		n++
		t.Run(d.Name, func(t *testing.T) {
			allResolve(t, run(filepath.Join("../../testdata/corpus",
				d.Name), d))
		})
	}
	if n < 10 {
		t.Errorf("only %d v1.0 corpus designs", n)
	}
}

const kindSelectedName = cst.KindSelectedName

// TestSignalAttributes: signal attributes need a signal prefix (a
// signal, a port, a signal parameter, an element or alias of one).
func TestSignalAttributes(t *testing.T) {
	files := map[string]string{"s.vhd": `entity s is
  port (p : in bit_vector(1 downto 0));
end;
architecture a of s is
  signal sg : bit;
  alias al : bit is p(0);
  constant c : bit := '0';
  function f(signal x : bit; y : bit) return boolean is
  begin
    return x'event and y'event;
  end;
begin
  process
    variable v : bit;
  begin
    wait until sg'event or p(1)'event or al'stable or p'last_value = "00";
    wait until v'event or c'active;
  end process;
end;
`}
	r := analyze(t, files, nil)
	want := "s.vhd:10:24: sema/error: the prefix of 'event must be a " +
		"signal, not parameter y\n" +
		"s.vhd:17:16: sema/error: the prefix of 'event must be a " +
		"signal, not variable v\n" +
		"s.vhd:17:27: sema/error: the prefix of 'active must be a " +
		"signal, not constant c\n"
	if got := diagText(r); got != want {
		t.Errorf("got\n%swant\n%s", got, want)
	}
}

// TestUseAllOverloads: `use p.all` makes every overload of a name
// visible, not just the first one found, so each call below picks its
// own declaration, and the implicit operators of STANDARD (several
// "+" per type) all stay visible.
func TestUseAllOverloads(t *testing.T) {
	files := map[string]string{"u.vhd": `package p is
  function f(x : integer) return integer;
  function f(x : real) return integer;
  function f(x : bit) return integer;
end;
package body p is
  function f(x : integer) return integer is begin return 1; end;
  function f(x : real) return integer is begin return 2; end;
  function f(x : bit) return integer is begin return 3; end;
end;
use work.p.all;
entity u is end;
architecture a of u is
  constant c : integer := f(1) + f(1.0) + f('1') - (1 + 2) * (-3);
  constant s : string := "a" & "b" & 'c';
begin end;
`}
	r := analyze(t, files, nil)
	if got := diagText(r); got != "" {
		t.Fatalf("diagnostics:\n%s", got)
	}
	src := files["u.vhd"]
	line := strings.Index(src, "f(1) +")
	seen := map[*Decl]bool{}
	for _, at := range []string{"f(1) +", "f(1.0)", "f('1')"} {
		off := line + strings.Index(src[line:], at)
		ref, ok := r.RefAt("u.vhd", off)
		if !ok || ref.Decl == nil {
			t.Fatalf("%s: no reference", at)
		}
		seen[ref.Decl] = true
	}
	if len(seen) != 3 {
		t.Errorf("the three calls resolve to %d declarations, want 3",
			len(seen))
	}
}
