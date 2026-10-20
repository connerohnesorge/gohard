package sema

import "testing"

// TestStaticNameRules: generate conditions and ranges are globally
// static (LRM 11.8), sensitivity list names are static signal names
// (LRM 10.2, 11.3), object alias names are static names and a
// nonobject alias has no subtype indication (LRM 6.6), aggregate
// target elements are locally static names (LRM 10.6.2.1) and formal
// designators are locally static (LRM 6.5.7.1). A constant outside a
// subprogram or process is globally static whatever its value (LRM
// 9.4.3), so the legal forms beside them stay clean.
func TestStaticNameRules(t *testing.T) {
	checkMarked(t, []markedCase{{name: "static", src: `
entity sub is
  port (y : out bit_vector(3 downto 0));
end;
architecture a of sub is begin end;
entity e is
  generic (w : integer := 3);
  port (x : in integer);
end;
architecture a of e is
  impure function lim return integer is begin return 2; end;
  constant k : integer := lim;
  signal s : bit_vector(0 to 3);
  signal y : bit_vector(3 downto 0);
  alias ak : bit_vector(0 to 1) is s(0 to k - 1);
  type t is (one);
  alias bad : t is t;                            --E
begin
  g1: if x > 4 generate end generate;            --E
  g2: for i in 1 to x generate end generate;     --E
  g3: for i in 1 to k generate end generate;
  g4: if w > 2 generate end generate;
  u1: entity work.sub port map (y(w downto 1) => y(2 downto 0), --E
    y(0) => y(0));
  u2: entity work.sub port map (y(3 downto 1) => y(2 downto 0),
    y(0) => y(0));
  process
    variable v : integer;
    variable a, b : bit;
  begin
    for i in 0 to 3 loop
      wait on s(i);                              --E
      (a, b) := bit_vector'("01");
    end loop;
    wait on s(1);
    wait on v;                                   --E
    wait;
  end process;
  process (now) begin end process;               --E
  process
    variable v : bit_vector(0 to 3);
    variable i : integer := 0;
    alias vi : bit is v(i);                      --E
  begin
    (v(i), v(1)) := bit_vector'("01");           --E
    wait;
  end process;
end;
`}})
}

// TestCaseExpressionStatic: before VHDL-2008 an array case expression
// has a locally static subtype, and a constant of a constrained array
// subtype that is not locally static is no locally static choice (LRM
// 8.8 and 7.4.1 of VHDL-1993).
func TestCaseExpressionStatic(t *testing.T) {
	src := `
entity e is
  generic (g : integer := 3);
end;
architecture a of e is
  signal s : bit_vector(g downto 0);
  signal l : bit_vector(1 downto 0);
  signal z : bit;
begin
  with s select z <=                %s
    '1' when "0000", '0' when others;
  with l select z <= '1' when "00", '0' when others;
  process
    variable v : bit_vector(3 downto 0);
    constant d : bit_vector(g downto 0) := (others => '0');
  begin
    case v is
      when d => null;               --E
      when others => null;
    end case;
    case v & v is                   %s
      when others => null;
    end case;
    wait;
  end process;
end;
`
	checkMarked(t, []markedCase{
		{name: "1993", std: "1993", src: sprintf2(src, "--E", "--E")},
		{name: "2008", src: sprintf2(src, "", "")},
	})
}

func sprintf2(src, a, b string) string {
	out := []byte{}
	n := 0
	for i := 0; i < len(src); i++ {
		if i+1 < len(src) && src[i] == '%' && src[i+1] == 's' {
			if n == 0 {
				out = append(out, a...)
			} else {
				out = append(out, b...)
			}
			n++
			i++
			continue
		}
		out = append(out, src[i])
	}
	return string(out)
}

// TestPurityRules: a function never calls a procedure with a wait
// statement, directly or through other procedures (LRM 10.2); a pure
// function declares no file and calls no procedure that references an
// object declared outside it or calls an impure function (LRM 4.1);
// impure functions may.
func TestPurityRules(t *testing.T) {
	checkMarked(t, []markedCase{{name: "purity", std: "1993", src: `
package p is
  shared variable sv : integer;
  impure function imp return integer;
  procedure w;
  procedure ww;
  procedure upd;
  procedure local(x : out integer);
  procedure via;
end;
package body p is
  impure function imp return integer is begin return sv; end;
  procedure w is begin wait for 1 ns; end;
  procedure ww is begin w; end;
  procedure upd is begin sv := 1; end;
  procedure local(x : out integer) is
    variable y : integer := 0;
  begin
    x := y;
  end;
  procedure via is begin upd; end;
  function f1 return integer is
    variable x : integer;
  begin
    ww;                            --E
    local(x);
    upd;                           --E
    via;                           --E
    return x;
  end;
  impure function f2 return integer is
    type ft is file of integer;
    file f : ft;
  begin
    upd;
    return 0;
  end;
  function f3 return integer is
    type ft is file of integer;
    file f : ft;                   --E
  begin
    return 0;
  end;
end;
`}})
}

// TestStatementPlaceRules: an entity's processes are passive (LRM
// 11.3) and a guarded assignment needs a visible GUARD (LRM 11.6).
func TestStatementPlaceRules(t *testing.T) {
	checkMarked(t, []markedCase{{name: "place", src: `
entity e is
  port (x : in integer; y : out integer);
begin
  process (x) begin
    assert x < 10;
  end process;
  process (x) begin
    y <= x + 1;                    --E
  end process;
end;
architecture a of e is
  signal p, q, b : bit;
begin
  p <= guarded q;                  --E
  b1: block (b = '1') is
  begin
    p <= guarded q;
  end block;
end;
`}})
}

// TestParameterReads: an out signal parameter is never read, an out
// variable parameter not before VHDL-2008, nor through an access value
// it holds; a linkage port is neither read nor assigned (LRM 6.5.2).
func TestParameterReads(t *testing.T) {
	src := `
entity e is
  port (l : linkage integer);
end;
architecture a of e is
  type rec is record y : integer; end record;
  type ptr is access rec;
  procedure p(signal s : out bit; variable v : out bit;
    variable r : out ptr) is
  begin
    s <= not s;                    --E
    v := not v;                    %s
    r.y := 1;                      %s
  end;
begin
  l <= 1;                          --E
  assert l = 1;                    --E
end;
`
	checkMarked(t, []markedCase{
		{name: "1993", std: "1993", src: sprintf2(src, "--E", "--E")},
		{name: "2008", src: sprintf2(src, "", "")},
	})
}
