package sema

import (
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/token"
)

// markedCase is one source whose error lines are marked: every line
// holding "--E" must get at least one error and no other line any.
type markedCase struct {
	name, std string
	lenient   bool
	src       string
}

// checkMarked analyzes each case alone and compares the lines with an
// error against the marked lines: the rule fires where it should and
// the legal forms next to it stay clean.
func checkMarked(t *testing.T, cases []markedCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := analyze(t, map[string]string{"t.vhd": c.src},
				func(cf *config.Config) {
					if c.std != "" {
						cf.Standard = c.std
					}
					cf.Lenient = c.lenient
				})
			var want, got []int
			for i, l := range strings.Split(c.src, "\n") {
				if strings.Contains(l, "--E") {
					want = append(want, i+1)
				}
			}
			for _, d := range r.Diagnostics {
				if d.Severity.String() != "error" {
					continue
				}
				if !slices.Contains(got, d.Line) {
					got = append(got, d.Line)
				}
			}
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Errorf("error lines %v, want %v\n%s", got, want,
					diagText(r))
			}
		})
	}
}

// TestObjectTypeRules: constants, signals, generics and ports are not
// of a file or access type or a type with an access subelement, a
// variable is not of a file type, a file declaration is of a file type
// and a function never returns one (LRM 4.2.1, 6.4.2, 6.5.2).
func TestObjectTypeRules(t *testing.T) {
	checkMarked(t, []markedCase{{name: "objects", src: `
package p is
  type acc is access integer;
  type ft is file of integer;
  type rec is record a : acc; end record;
  type arr is array (0 to 1) of acc;
  type ok_rec is record a : integer; end record;
  constant c1 : acc;                --E
  constant c2 : rec;                --E
  constant c3 : ok_rec;
  signal s1 : arr;                  --E
  signal s2 : ft;                   --E
  signal s3 : integer;
  file f1 : integer;                --E
  file f2 : ft;
  function g return ft;             --E
  function h return integer;
  procedure pr(variable x : ft);    --E
  procedure pq(file x : ft; y : in acc);
end;
entity e is
  generic (g1 : acc;                --E
    g2 : integer := 0);
  port (p1 : in arr;                --E
    p2 : in integer);
end;
`}})
}

// TestInterfaceRules: the object class of ports, and which interface
// objects may have a default expression (LRM 4.2.2, 6.5.2, 6.5.6.3).
func TestInterfaceRules(t *testing.T) {
	checkMarked(t, []markedCase{{name: "interfaces", src: `
entity e is
  port (constant c : in integer;               --E
    l : linkage integer := 0;                  --E
    i : in integer := 0);
end;
package p is
  procedure p1(signal s : in integer := 1);    --E
  procedure p2(variable v : out integer := 1); --E
  procedure p3(variable v : inout integer := 1); --E
  procedure p4(variable v : in integer := 1; c : integer := 2);
end;
`}})
}

// TestSharedVariableLenient: from VHDL-2002 on a shared variable is of
// a protected type (LRM 6.4.2.4); lenient mode keeps the VHDL-93 rule
// as GHDL's -frelaxed and NVC's --relaxed do.
func TestSharedVariableLenient(t *testing.T) {
	src := `
package p is
  shared variable v : integer;  %s
end;
`
	checkMarked(t, []markedCase{
		{name: "2008", src: strings.Replace(src, "%s", "--E", 1)},
		{name: "1993", std: "1993", src: strings.Replace(src, "%s", "",
			1)},
		{name: "lenient", lenient: true,
			src: strings.Replace(src, "%s", "", 1)},
	})
}

// TestForceInParameter: a force or release of mode in, given or by
// default, on a signal parameter or port of mode in is legal; mode out
// is not, nor is a plain assignment (LRM 10.5.2.1).
func TestForceInParameter(t *testing.T) {
	checkMarked(t, []markedCase{{name: "force", src: `
entity e is
  port (i : in bit);
end;
architecture a of e is
  procedure f(signal s : in bit) is
  begin
    s <= force '0';
    s <= force in '0';
    s <= release;
    s <= force out '0';     --E
    s <= '0';               --E
  end;
begin
  process begin
    i <= force '1';
    i <= release in;
    i <= release out;       --E
    wait;
  end process;
end;
`}})
}

// TestPortMaps: an unassociated port of mode in has a default, an
// unassociated port of another mode is constrained, a formal is
// associated once, and the modes of a formal port and of its actual
// port follow the table of LRM 6.5.6.3 (relaxed in VHDL-2008).
func TestPortMaps(t *testing.T) {
	sub := `
entity sub is
  generic (g : integer := 0);
  port (i : in bit; d : in bit := '0'; o : out bit_vector);
end;
architecture a of sub is begin end;
`
	checkMarked(t, []markedCase{{name: "unassociated", src: sub + `
entity top is end;
architecture a of top is
  signal s : bit;
  signal v : bit_vector(0 to 1);
begin
  u1: entity work.sub port map (i => s, o => v);
  u2: entity work.sub port map (o => v);        --E
  u3: entity work.sub port map (i => s);        --E
  u4: entity work.sub port map (i => open, o => v);  --E
  u5: entity work.sub generic map (g => 1, g => 2)   --E
    port map (s, s, v);
  u6: entity work.sub port map (i => s, i => s, o => v);  --E
end;
`}})
	modes := `
entity leaf is
  port (fi : in bit; fo : out bit; fx : inout bit; fb : buffer bit;
    fl : linkage bit);
end;
architecture a of leaf is begin end;
entity top is
  port (ai : in bit; ao : out bit; ax : inout bit; ab : buffer bit;
    al : linkage bit);
end;
architecture a of top is
begin
  u1: entity work.leaf port map (ai, ao, ax, ab, al);
  u2: entity work.leaf port map (fi => ax, fo => ax, fx => ax,
    fb => ab, fl => ai);
  u3: entity work.leaf port map (fi => al, fo => ao, fx => ax, --E
    fb => ab, fl => al);
  u4: entity work.leaf port map (fi => ai, fo => ai, fx => ax, --E
    fb => ab, fl => al);
  u5: entity work.leaf port map (fi => ai, fo => ao, fx => ao, --E
    fb => ab, fl => al);
  u6: entity work.leaf port map (fi => ao, fo => ao, fx => ax, --G
    fb => ao, fl => al); --G
end;
`
	checkMarked(t, []markedCase{
		{name: "modes2008", src: modes},
		{name: "modes1993", std: "1993",
			src: strings.ReplaceAll(modes, "--G", "--E")},
	})
}

func TestPortModeTable(t *testing.T) {
	// Every mode accepts itself (except a linkage actual for another
	// formal) and a linkage formal accepts every mode; later
	// standards only add pairs.
	all := []Mode{ModeIn, ModeOut, ModeInout, ModeBuffer, ModeLinkage}
	for _, f := range all {
		for _, m := range all {
			old, now := portModeOK(f, m, token.Std1993),
				portModeOK(f, m, token.Std2008)
			if mid := portModeOK(f, m, token.Std2002); old && !mid ||
				mid && !now {
				t.Errorf("%s<=%s not monotonic", f.text(), m.text())
			}
			if old && !now {
				t.Errorf("%s<=%s legal in 1993 only", f.text(), m.text())
			}
			if f == m && !old {
				t.Errorf("%s<=%s illegal", f.text(), m.text())
			}
			if f == ModeLinkage && !old {
				t.Errorf("linkage<=%s illegal", m.text())
			}
			if m == ModeLinkage && f != ModeLinkage && now {
				t.Errorf("%s<=linkage legal", f.text())
			}
		}
	}
}
