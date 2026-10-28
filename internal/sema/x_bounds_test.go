package sema

// x_bounds_test.go: the static case-choice and range checks use the
// INTEGER of the analysis standard, 64 bits wide under VHDL-2019
// (LRM 5.2.3.2), so a 2019 case over INTEGER must cover the values past
// 32 bits and integer'high folds to 2**63-1.

import (
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/eval"
)

func caseSrc(sub, choices string) string {
	return `entity e is end entity;
architecture a of e is
begin
  process
    variable i : ` + sub + `;
  begin
    case i is
` + choices + `
    end case;
    wait;
  end process;
end architecture;
`
}

func TestXCaseCoverage64(t *testing.T) {
	cases := []struct {
		std, sub, choices string
		want              []string
	}{
		{"2019", "integer", `      when integer'low to -1 => null;
      when 0 to 2147483647 => null;`,
			[]string{"7: the choices do not cover " +
				"2147483648 to 9223372036854775807"}},
		{"2008", "integer", `      when integer'low to -1 => null;
      when 0 to 2147483647 => null;`, nil},
		{"2019", "natural", `      when 0 to 2147483647 => null;`,
			[]string{"7: the choices do not cover " +
				"2147483648 to 9223372036854775807"}},
		{"2008", "natural", `      when 0 to 2147483647 => null;`, nil},
		{"2019", "integer", `      when 2147483647 => null;
      when integer'high => null;
      when others => null;`, nil},
		{"2008", "integer", `      when 2147483647 => null;
      when integer'high => null;
      when others => null;`,
			[]string{"9: the choice integer'high repeats a value " +
				"already covered"}},
		{"2019", "positive", `      when 1 to positive'high - 1 => null;
      when 9223372036854775807 => null;`, nil},
		{"2019", "integer", `      when integer'low to 0 => null;
      when 1 to 9223372036854775807 => null;`, nil},
	}
	for _, c := range cases {
		r := analyzeOpen(t, c.std, caseSrc(c.sub, c.choices))
		if got := errorLines(r); !slices.Equal(got, c.want) {
			t.Errorf("%s %s %q:\n got %q\nwant %q", c.std, c.sub,
				c.choices, got, c.want)
		}
	}
}

// TestXCaseSplitProperty: splitting INTEGER at any value v into
// integer'low to v-1 and v to integer'high covers it exactly under
// 2019; leaving out integer'high reports exactly the missing value.
func TestXCaseSplitProperty(t *testing.T) {
	x := uint64(0x9e3779b97f4a7c15)
	for n := range 60 {
		x += 0x9e3779b97f4a7c15
		z := x
		z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
		z = (z ^ z>>27) * 0x94d049bb133111eb
		v := int64(z ^ z>>31)
		if v == math.MinInt64 {
			v++
		}
		if n%2 == 0 {
			v >>= 20
		}
		ch := fmt.Sprintf("      when integer'low to %d => null;\n"+
			"      when %d to integer'high => null;", v-1, v)
		if v == math.MinInt64+1 {
			ch = fmt.Sprintf("      when integer'low => null;\n"+
				"      when %d to integer'high => null;", v)
		}
		r := analyzeOpen(t, "2019", caseSrc("integer", ch))
		if got := errorLines(r); len(got) != 0 {
			t.Fatalf("v=%d: %q", v, got)
		}
		if v == math.MaxInt64 {
			continue
		}
		ch = fmt.Sprintf("      when integer'low to %d => null;\n"+
			"      when %d to integer'high - 1 => null;", v-1, v)
		want := []string{"7: the choices do not cover " +
			"9223372036854775807"}
		r = analyzeOpen(t, "2019", caseSrc("integer", ch))
		if got := errorLines(r); !slices.Equal(got, want) {
			t.Fatalf("v=%d: got %q want %q", v, got, want)
		}
	}
}

// TestXRangeOverflow64: a loop bound past 32 bits is legal under 2019
// and outside INTEGER before.
func TestXRangeOverflow64(t *testing.T) {
	src := `entity e is end entity;
architecture a of e is
begin
  process
  begin
    for i in 2147483647 + 1 to 2147483647 + 2 loop
    end loop;
    wait;
  end process;
end architecture;
`
	if got := errorLines(analyzeOpen(t, "2019", src)); len(got) != 0 {
		t.Errorf("2019: %q", got)
	}
	got := errorLines(analyzeOpen(t, "2008", src))
	if len(got) == 0 {
		t.Errorf("2008: no error for a bound past 32 bits")
	}
}

// TestXLayouts: a floating type or subtype keeps its declared range,
// and an open index constraint leaves the index range to a later
// constraint while its element constraint applies.
func TestXLayouts(t *testing.T) {
	src := `package p is
  type fdown is range 2.5 downto -1.5;
  subtype unit_real is real range -1.25 to 0.75;
  subtype narrow is fdown range 1.0 downto 0.5;
  type arr is array (natural range <>) of bit_vector;
  subtype parr is arr(open)(5 downto 3);
  subtype parr2 is parr(1 to 2);
end package;
`
	for _, std := range []string{"2008", "2019"} {
		r := analyzeOpen(t, std, src)
		if got := errorLines(r); len(got) != 0 {
			t.Fatalf("%s: %q", std, got)
		}
		ev := NewEvaluator(r, 0)
		lay := func(name string) *eval.Type {
			for _, u := range r.Units {
				for _, d := range u.Decls {
					if string(d.Name) == name {
						lt, err := ev.LayoutIn(d.Type, eval.NewBudget(1e6))
						if err != nil {
							t.Fatalf("%s %s: %v", std, name, err)
						}
						return lt
					}
				}
			}
			t.Fatalf("no %s", name)
			return nil
		}
		reals := map[string]eval.RealRange{
			"fdown":     {Left: 2.5, Right: -1.5, Dir: eval.Downto},
			"unit_real": {Left: -1.25, Right: 0.75, Dir: eval.To},
			"narrow":    {Left: 1.0, Right: 0.5, Dir: eval.Downto},
		}
		for n, want := range reals {
			if got := lay(n).Real; got != want {
				t.Errorf("%s %s: %+v, want %+v", std, n, got, want)
			}
		}
		if p := lay("parr"); p.Bounds != nil || p.Elem == nil ||
			len(p.Elem.Bounds) != 1 || p.Elem.Bounds[0].Left != 5 {
			t.Errorf("%s parr: %+v", std, p)
		}
		if p := lay("parr2"); len(p.Bounds) != 1 ||
			p.Bounds[0].Left != 1 || p.Bounds[0].Right != 2 ||
			p.Elem == nil || p.Elem.Bounds[0].Right != 3 {
			t.Errorf("%s parr2: %+v", std, p)
		}
	}
}
