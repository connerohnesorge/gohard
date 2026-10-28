package elab

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/diag"
)

// The name attributes 'SIMPLE_NAME, 'PATH_NAME and 'INSTANCE_NAME
// (LRM 16.2.5) in simulation. Every expected string below is NVC
// 1.23's output for the same design (nvc --std=2019 -a -e -r), except
// where a comment says otherwise.

// mNotes returns the messages of the report notes of a run, sorted:
// the strings, not the schedule, are under test.
func mNotes(r run) []string {
	var out []string
	for _, d := range r.diags {
		if d.Code == diag.SimAssertNote {
			out = append(out, strings.TrimPrefix(d.Message,
				"report note: "))
		}
	}
	slices.Sort(out)
	return out
}

func mWant(t *testing.T, r run, want ...string) {
	t.Helper()
	got := mNotes(r)
	slices.Sort(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("notes:\n%s\nwant:\n%s\nall: %v", strings.Join(got, "\n"),
			strings.Join(want, "\n"), r.notes())
	}
}

const mHierSrc = `
package pkg is
  constant c : integer := 1;
  function f(x : integer) return string;
end package;
package body pkg is
  function f(x : integer) return string is
    variable v : integer;
  begin
    return f'path_name & " " & v'instance_name & " " & c'path_name;
  end function;
end package body;

use work.pkg.all;
entity sub is
  generic (g : integer := 0);
  port (p : in bit);
end entity;
architecture a of sub is
  signal s : bit;
begin
  pr: process
    variable v : integer;
    procedure q(y : integer) is
      variable w : integer;
    begin
      report "q " & q'path_name & " " & w'instance_name & " " &
        y'path_name;
    end procedure;
  begin
    report "s " & s'path_name & " " & s'instance_name & " " &
      s'simple_name;
    report "pg " & p'path_name & " " & g'instance_name;
    report "v " & v'path_name & " " & v'instance_name;
    report "pr " & pr'path_name & " " & pr'instance_name & " " &
      pr'simple_name;
    report "sub " & sub'path_name & " " & a'instance_name;
    report "f " & f(1) & " " & work.pkg'path_name;
    q(1);
    wait;
  end process;
end architecture;

entity top is end entity;
architecture rtl of top is
  signal t : bit;
  component sub is
    generic (g : integer := 0);
    port (p : in bit);
  end component;
begin
  u1: entity work.sub generic map (1) port map (t);
  gen: for i in 0 to 1 generate
    signal gs : bit;
  begin
    b: block begin
      process begin
        report "gen " & gs'path_name & " " & i'instance_name & " " &
          b'path_name;
        wait;
      end process;
    end block;
  end generate;
  ig: if true generate
    u3: sub port map (t);
  end generate;
  ig2: if alt1: false generate
  elsif alt2: true generate
    signal as : bit;
  begin
    pp: process begin
      report "alt " & as'path_name & " " & as'instance_name & " " &
        pp'path_name;
      wait;
    end process;
  end generate;
  process
    variable u : integer;
  begin
    report "top " & u'path_name & " " & top'path_name & " " &
      rtl'instance_name & " " & t'instance_name;
    for j in 1 to 1 loop
      report "loop " & j'path_name;
    end loop;
    wait;
  end process;
end architecture;
`

// TestMNamesHierarchy: entities, architectures, instances (entity and
// component), generate iterations and alternatives, blocks, processes
// (labeled and not), loops, subprograms of processes and of packages,
// ports, generics and package objects.
func TestMNamesHierarchy(t *testing.T) {
	res, _ := simulate19(t, mHierSrc, "top")
	var want []string
	for _, u := range []struct{ p, i string }{
		{":top:u1", ":top(rtl):u1@sub(a)"},
		{":top:ig:u3", ":top(rtl):ig:u3@sub(a)"},
	} {
		want = append(want,
			"s "+u.p+":s "+u.i+":s s",
			"pg "+u.p+":p "+u.i+":g",
			"v "+u.p+":pr:v "+u.i+":pr:v",
			"pr "+u.p+":pr: "+u.i+":pr: pr",
			"sub "+u.p+": "+u.i+":",
			"f :work:pkg:f[integer return string]: "+
				":work:pkg:f[integer return string]:v :work:pkg:c "+
				":work:pkg:",
			"q "+u.p+":pr:q[integer]: "+u.i+":pr:q[integer]:w "+
				u.p+":pr:q[integer]:y")
	}
	for i := range 2 {
		g := ":top:gen(" + strconv.Itoa(i) + ")"
		want = append(want, "gen "+g+":gs :top(rtl):gen("+
			strconv.Itoa(i)+"):i "+g+":b:")
	}
	want = append(want,
		"alt :top:alt2:as :top(rtl):alt2:as :top:alt2:pp:",
		"top :top::u :top: :top(rtl): :top(rtl):t",
		"loop :top::j")
	mWant(t, res, want...)
}

const mProtSrc = `
entity t3 is end entity;
architecture y of t3 is
  type inner is protected
    procedure hi;
  end protected;
  type inner is protected body
    variable z : integer;
    procedure hi is
    begin
      report "hi " & hi'path_name & " " & z'path_name & " " &
        z'instance_name;
    end procedure;
  end protected body;
  type outer is protected
    procedure go;
    impure function name return string;
  end protected;
  type outer is protected body
    variable in1 : inner;
    procedure go is begin in1.hi; end procedure;
    impure function name return string is
    begin
      return name'path_name & " " & in1'instance_name;
    end function;
  end protected body;
  type pt is protected
    procedure m(a : integer; b : bit);
  end protected;
  type pt is protected body
    variable iv : integer;
    procedure m(a : integer; b : bit) is
      variable lv : integer;
    begin
      report "m " & m'path_name & " " & m'instance_name & " " &
        lv'path_name & " " & iv'path_name & " " & a'instance_name;
    end procedure;
  end protected body;
  shared variable so : outer;
  shared variable sv : pt;
  type acc is access inner;
begin
  process
    variable p : acc;
    variable pv : pt;
  begin
    so.go;
    report "name " & so.name;
    sv.m(1, '0');
    pv.m(1, '0');
    p := new inner;
    p.all.hi;
    wait;
  end process;
end architecture;
`

// TestMNamesProtected: inside a protected type (VHDL-2019) the names
// of methods, their objects and the type's variables start with the
// protected object's path: a shared variable, a private variable of
// another object, a process variable, or an allocated object (the
// empty name in the allocating process, _p0 being the unlabeled one).
func TestMNamesProtected(t *testing.T) {
	res, _ := simulate19(t, mProtSrc, "t3")
	mWant(t, res,
		"hi :t3:so:in1:hi[]: :t3:so:in1:z :t3(y):so:in1:z",
		"name :t3:so:name[return string]: :t3(y):so:in1",
		"m :t3:sv:m[integer,bit]: :t3(y):sv:m[integer,bit]: "+
			":t3:sv:m[integer,bit]:lv :t3:sv:iv "+
			":t3(y):sv:m[integer,bit]:a",
		"m :t3::pv:m[integer,bit]: :t3(y)::pv:m[integer,bit]: "+
			":t3::pv:m[integer,bit]:lv :t3::pv:iv "+
			":t3(y)::pv:m[integer,bit]:a",
		"hi :t3:_p0::hi[]: :t3:_p0::z :t3(y):_p0::z")
}

// TestMNamesComposite: an element of an array or record of protected
// objects names its index or field (NVC has no composites of
// protected types; this is GoHard's form).
func TestMNamesComposite(t *testing.T) {
	src := `
entity tb is end entity;
architecture a of tb is
  type pt is protected
    impure function me return string;
  end protected;
  type pt is protected body
    impure function me return string is
    begin
      return me'path_name;
    end function;
  end protected body;
  type arr is array (1 to 3) of pt;
  type rec is record x : integer; y : pt; end record;
  shared variable sa : arr;
  shared variable sr : rec;
begin
  process begin
    report sa(2).me & " " & sr.y.me;
    wait;
  end process;
end architecture;
`
	res, _ := simulate19(t, src, "tb")
	mWant(t, res, ":tb:sa(2):me[return string]: "+
		":tb:sr.y:me[return string]:")
}

// TestMNamesUniqueObjects: whatever the number of protected objects
// and the maps' order, each object's names are its own and the run is
// the same every time.
func TestMNamesUniqueObjects(t *testing.T) {
	var b strings.Builder
	b.WriteString(`
entity tb is end entity;
architecture a of tb is
  type pt is protected
    impure function me return string;
  end protected;
  type pt is protected body
    variable z : integer;
    impure function me return string is
    begin
      return z'instance_name;
    end function;
  end protected body;
`)
	const n = 12
	for i := range n {
		b.WriteString("  shared variable s" + strconv.Itoa(i) +
			" : pt;\n")
	}
	b.WriteString("begin\n")
	for i := range n {
		s := strconv.Itoa(i)
		b.WriteString("  l" + s + ": process\n" +
			"    variable v" + s + " : pt;\n  begin\n" +
			"    report s" + s + ".me & \" \" & v" + s + ".me;\n" +
			"    wait;\n  end process;\n")
	}
	b.WriteString("end architecture;\n")
	var want []string
	for i := range n {
		s := strconv.Itoa(i)
		want = append(want, ":tb(a):s"+s+":z :tb(a):l"+s+":v"+s+":z")
	}
	var first []string
	for range 5 {
		res, _ := simulate19(t, b.String(), "tb")
		mWant(t, res, want...)
		if first == nil {
			first = res.notes()
		} else if !slices.Equal(first, res.notes()) {
			t.Fatalf("runs differ:\n%v\n%v", first, res.notes())
		}
	}
}

// TestMNamesGeneratePositions: every iteration of a for generate over
// integers, a descending range and an enumeration names its parameter
// by position, and each name is its instance's (hier.Instance.Names).
func TestMNamesGeneratePositions(t *testing.T) {
	src := `
entity tb is end entity;
architecture a of tb is
  type color is (red, green, blue);
begin
  g: for i in -2 to 2 generate
    signal s : bit;
  begin
    process begin report s'path_name; wait; end process;
  end generate;
  d: for k in 7 downto 6 generate
    process begin report k'instance_name; wait; end process;
  end generate;
  e: for c in color generate
    process begin report c'path_name; wait; end process;
  end generate;
end architecture;
`
	res, d := simulate19(t, src, "tb")
	want := []string{":tb(a):d(7):k", ":tb(a):d(6):k",
		":tb:e(0):c", ":tb:e(1):c", ":tb:e(2):c"}
	for i := -2; i <= 2; i++ {
		want = append(want, ":tb:g("+strconv.Itoa(i)+"):s")
	}
	mWant(t, res, want...)
	// The names come from the hierarchy: each generate instance's
	// Names plus the object.
	got := map[string]bool{}
	for _, s := range mNotes(res) {
		got[s] = true
	}
	for _, c := range d.Top.Children {
		n := c.Names()
		for _, s := range []string{n.Path + ":s", n.Inst + ":k",
			n.Path + ":c"} {
			if got[s] {
				delete(got, s)
			}
		}
	}
	if len(got) > 0 {
		t.Errorf("names not of an instance: %v", got)
	}
}

// TestMNamesConstantsAndAliases: a design-region constant initialized
// from a name attribute is computed by the interpreter (never "not
// static"), per instance, a signal's initial value and a generic
// actual by elaboration; an alias has its own names (LRM 6.6.1);
// extended identifiers are lowered, as NVC.
func TestMNamesConstantsAndAliases(t *testing.T) {
	src := `
package p5 is
  signal ps : bit;
  constant pc : string := ps'path_name;
end package;
use work.p5.all;
entity c5 is
  generic (nm : string := "x");
end entity;
architecture a of c5 is
  signal ss : bit;
  alias al is ss;
  signal \Ext Sig\ : bit;
  constant k : string := ss'instance_name;
  signal init : string(1 to 8) := ss'path_name;
begin
  process begin
    report k & " " & nm & " " & pc & " " & init & " " & al'path_name & " " &
      al'simple_name & " " & \Ext Sig\'path_name;
    wait;
  end process;
end architecture;
entity t5 is end entity;
architecture x of t5 is
begin
  u: entity work.c5 generic map (nm => u'path_name);
end architecture;
`
	res, _ := simulate19(t, src, "t5")
	mWant(t, res, ":t5(x):u@c5(a):ss :t5:u :work:p5:ps :t5:u:ss "+
		":t5:u:al al "+
		`:t5:u:\ext sig\`)
}

// TestMNamesGenericPackage: a declaration of a generic package, whose
// running instance the interpreter does not know, is
// unsupported/construct (please report), not a wrong string or a
// runtime error.
func TestMNamesGenericPackage(t *testing.T) {
	src := `
package gp is
  generic (n : integer);
  procedure show;
end package;
package body gp is
  procedure show is
    variable v : integer;
  begin
    report v'path_name & " " & gp'path_name;
  end procedure;
end package body;
package gp3 is new work.gp generic map (3);
entity tb is end entity;
architecture a of tb is
begin
  process begin
    work.gp3.show;
    wait;
  end process;
end architecture;
`
	res := simWorld(t, src, "tb", "2019", 0)
	var got []string
	for _, d := range res.diags {
		if d.Code == diag.SimRuntimeError {
			t.Errorf("runtime error: %s", d.Message)
		}
		if d.Code == diag.UnsupportedConstruct {
			got = append(got, d.Message)
		}
	}
	want := "'PATH_NAME and 'INSTANCE_NAME of a declaration of a " +
		"generic package is not supported yet, please report"
	if len(got) != 1 || got[0] != want {
		t.Errorf("unsupported %q, want [%q]", got, want)
	}
}
