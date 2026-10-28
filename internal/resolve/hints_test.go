package resolve

import (
	"fmt"
	"strings"
	"testing"
)

const hintSrc = `entity e is
  generic (W : natural := 8; D : natural := W * 2; G : natural := 2 ** 4);
  port (a : in bit; b : out bit);
end entity;
architecture rtl of e is
  function f(x : integer; y : integer) return integer is
  begin
    return x + y;
  end function;
  constant C : integer := f(1, y => 2);
  constant K : integer := 3;
  constant N : integer := -3;
  constant S : bit_vector(3 downto 0) := "10" & "01";
  constant BIG : bit_vector(4095 downto 0) := (others => '0');
  constant LONG : string := "abcdefghijklmnopqrstuvwxyz" & "abcdefghijklmnopqrstuvwxyz";
  alias al is a;
  alias wide : bit is b;
  signal v : bit_vector(7 downto 0);
  alias hi is v(7 downto 4);
  signal x : integer;
begin
  u0 : entity work.e generic map (4) port map (a, open);
  process
    variable acc : integer;
  begin
    for i in 0 to 3 loop
      acc := f(i, 2);
      acc := f(x, 2);
    end loop;
    wait;
  end process;
end architecture;
`

// hintText renders hints as "line:col label" (one-based, bytes).
func hintText(src string, hs []Hint) string {
	var b strings.Builder
	for _, h := range hs {
		line := strings.Count(src[:h.Off], "\n") + 1
		col := h.Off - strings.LastIndex(src[:h.Off], "\n")
		fmt.Fprintf(&b, "%d:%d %s\n", line, col, h.Label)
	}
	return b.String()
}

func TestHints(t *testing.T) {
	r := analyzeOne(t, hintSrc)
	if len(r.Diagnostics) > 0 {
		t.Fatalf("diagnostics: %v", r.Diagnostics)
	}
	all := HintKinds{ParameterNames: true, Types: true, Values: true}
	for _, c := range []struct {
		name string
		k    HintKinds
		want string
	}{
		{"parameter names", HintKinds{ParameterNames: true}, `10:29 x =>
22:35 W =>
22:51 b =>
27:16 x =>
27:19 y =>
28:19 y =>
`},
		{"types", HintKinds{Types: true}, `16:11 : BIT
19:11 : BIT_VECTOR
26:10 : INTEGER
`},
		{"values", HintKinds{Values: true}, `2:73 = 16
10:39 = 3
13:53 = "1001"
15:88 = "abcdefghijklmnopqrstuvwxyzabcdefghijklm...
`},
		{"off", HintKinds{}, ""},
	} {
		got := hintText(hintSrc, Hints(r, "a.vhd", 0, len(hintSrc), c.k))
		if got != c.want {
			t.Errorf("%s:\n%s\nwant\n%s", c.name, got, c.want)
		}
	}
	// A hint never depends on the range asked for: the hints of two
	// halves are the hints of the whole.
	whole := Hints(r, "a.vhd", 0, len(hintSrc), all)
	mid := strings.Index(hintSrc, "begin\n  u0")
	half := append(Hints(r, "a.vhd", 0, mid-1, all),
		Hints(r, "a.vhd", mid, len(hintSrc), all)...)
	if a, b := hintText(hintSrc, whole), hintText(hintSrc, half); a != b {
		t.Errorf("whole:\n%s\nhalves:\n%s", a, b)
	}
}
