package sema

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/token"
)

// typingCase is a declarative part and a process body checked inside
// an architecture that sees std_logic_1164, numeric_std and textio.
// want is "" for a clean case, else the diagnostic code expected on
// the line marked --! (and on no other line).
type typingCase struct {
	name  string
	decls string
	stmts string
	want  string
	text  string // a substring of the message, if set
	std   token.Standard
}

const typingTypes = `
  type int_ptr is access integer;
  type rec is record a : integer; b : std_logic; end record;
  type arr is array (natural range <>) of std_logic_vector(3 downto 0);
  type mem is array (0 to 3, 0 to 1) of bit;
  type my_int is range 0 to 100;
  type color is (red, green, blue);
  type light is (red, amber, green);
  signal s1 : std_logic_vector(7 downto 0);
  signal s2 : unsigned(7 downto 0);
  signal s3 : signed(7 downto 0);
  signal b1 : boolean;
  signal sl : std_logic;
  signal m1 : my_int;
  signal c1 : color;
`

func typingSource(c typingCase) string {
	return `library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
use std.textio.all;
entity t is end;
architecture rtl of t is` + typingTypes + c.decls + `
begin
  process
    variable p1 : int_ptr;
    variable v : integer;
    variable r : real;
    variable lv : line;
  begin
` + c.stmts + `
    wait;
  end process;
end;
`
}

var typingCases = []typingCase{
	// Universal numbers and implicit conversion (LRM 9.3.6).
	{name: "universal", decls: `
  constant k3 : time := 2 * 10 ns;
  constant k4 : time := 10 ns * 2.5;
  constant k5 : time := 10 ns / 2;
  constant k6 : integer := 10 ns / 1 ns;
  constant k7 : integer := 2 ** 3;
  constant k8 : real := 2.0 ** 3;
  constant k9 : integer := abs (-3) + 7 mod 3 + 7 rem 3;
  constant k10 : my_int := 3;
  constant k11 : my_int := k10 + 1;`},
	{name: "integer from real", decls: `
  constant k : integer := 1.5; --!`, want: "sema/type-mismatch",
		text: "expected INTEGER, found universal_real"},
	{name: "distinct integer types", decls: `
  constant k : integer := m1; --!`, want: "sema/type-mismatch",
		text: "found my_int"},
	{name: "integer for a vector", decls: `
  constant k : std_logic_vector(1 downto 0) := 3; --!`,
		want: "sema/type-mismatch"},
	{name: "integer for time", decls: `
  constant k : time := 3; --!`, want: "sema/type-mismatch"},
	{name: "character for integer", decls: `
  constant k : integer := 'a'; --!`, want: "sema/type-mismatch"},
	{name: "qualified type", decls: `
  constant k : integer := my_int'(3); --!`, want: "sema/type-mismatch"},
	// LRM 9.3.6: the innermost complete context does not determine a
	// unique numeric type for the universal_integer operand, so it is
	// not converted; GHDL and NVC both reject these.
	{name: "mixed universal real", decls: `
  constant k : real := 1.0 * 2; --!`, want: "sema/no-overload",
		text: "mixes universal_real and universal_integer"},
	{name: "mixed universal integer", decls: `
  constant k : real := 2 * 1.5; --!`, want: "sema/no-overload"},
	{name: "real plus integer", decls: `
  constant k : real := 1.0 + 1; --!`, want: "sema/no-overload"},
	// Enumeration literals are overloaded (LRM 5.2.2.1, 12.5).
	{name: "enumeration by context", decls: `
  constant k1 : color := red;
  constant k2 : light := red;
  constant k3 : boolean := color'(red) = red;
  constant k4 : boolean := c1 = green;`},
	{name: "literal of the other type", decls: `
  constant k : color := amber; --!`, want: "sema/no-overload",
		text: "returns light, not color"},
	{name: "ambiguous literals", decls: `
  constant k : boolean := red = red; --!`,
		want: "sema/ambiguous-overload",
		text: `"=" [color, color return BOOLEAN] from rtl; "=" [light, light`},
	{name: "ambiguous strings", decls: `
  constant k : boolean := "01" = "10"; --!`,
		want: "sema/ambiguous-overload", text: "[STRING, STRING"},
	// Literals and aggregates (LRM 9.3.2, 9.3.3).
	{name: "literals", decls: `
  constant k1 : std_logic_vector(7 downto 0) := x"FF";
  constant k2 : std_logic_vector(11 downto 0) := 12x"FF";
  constant k3 : string := "abc" & "def";
  constant k4 : std_logic_vector(8 downto 0) := '0' & k1;
  constant k5 : bit_vector(3 downto 0) := b"1010";`},
	{name: "aggregates", decls: `
  constant k1 : rec := (a => 1, b => '0');
  constant k2 : rec := (1, '1');
  constant k3 : arr(0 to 1) := (others => (others => '0'));
  constant k4 : mem := ((others => '0'), ("01"), "10", "11");
  constant k5 : std_logic_vector(7 downto 0) := (7 => '1', others => '0');
  constant k6 : std_logic_vector(7 downto 0) :=
    (1 downto 0 => "11", others => '0');
  constant k7 : std_logic_vector(7 downto 0) := (x"F", x"0");
  constant k8 : std_logic_vector(7 downto 0) :=
    (k5(3 downto 0), k5(7 downto 4));`},
	{name: "slice association before 2008", std: token.Std1993,
		decls: `
  constant k1 : std_logic_vector(7 downto 0) := (others => '0');
  constant k2 : std_logic_vector(7 downto 0) :=
    (k1(3 downto 0), k1(7 downto 4)); --!`, want: "sema/type-mismatch"},
	{name: "slice with an index choice", decls: `
  constant k1 : std_logic_vector(7 downto 0) := (others => '0');
  constant k2 : std_logic_vector(7 downto 0) :=
    (0 => k1(3 downto 0), others => '0'); --!`,
		want: "sema/type-mismatch"},
	{name: "record element type", decls: `
  constant k : rec := (a => '1', b => '0'); --!`,
		want: "sema/type-mismatch"},
	// Type conversions (LRM 9.3.6).
	{name: "conversions", decls: `
  constant k1 : integer := integer(3.7);
  constant k2 : real := real(3);
  constant k3 : std_logic_vector(7 downto 0) := std_logic_vector(s2);
  constant k4 : unsigned(7 downto 0) := unsigned(s1);
  constant k5 : integer := integer(m1);
  constant k6 : my_int := my_int(k5);
  constant k7 : signed(7 downto 0) := signed(unsigned'(x"0F"));`},
	// The operand is a complete context (LRM 9.3.6), as GHDL and NVC
	// read it: a string literal's type cannot come from it.
	{name: "conversion of a string literal", decls: `
  constant k8 : unsigned(3 downto 0) := unsigned("0101"); --!`,
		want: "sema/error", text: "operand of a type conversion"},
	{name: "conversion to an enumeration", decls: `
  constant k : color := color(3); --!`, want: "sema/error",
		text: "not closely related"},
	{name: "conversion of a record", decls: `
  constant r1 : rec := (1, '0');
  constant k : integer := integer(r1); --!`, want: "sema/error",
		text: "cannot convert rec to INTEGER"},
	{name: "conversion of vector to integer", decls: `
  constant k : integer := integer(s1); --!`, want: "sema/error"},
	// Allocators (LRM 9.3.7).
	{name: "allocators", stmts: `
    p1 := new integer'(5);
    p1 := new integer;
    p1 := null;
    v := p1.all + 1;`},
	{name: "allocator of the wrong type", stmts: `
    p1 := new real'(5.0); --!`, want: "sema/type-mismatch"},
	// Operators, predefined and from numeric_std (LRM 9.2).
	{name: "operators", stmts: `
    if s1 = x"00" then end if;
    if s2 = 3 then end if;
    if s2 < 3 then end if;
    s2 <= s2 + 1;
    s2 <= 1 + s2;
    s3 <= -s3;
    s3 <= abs s3;
    s1 <= std_logic_vector(s2 + 1);
    s1 <= std_logic_vector(to_unsigned(5, 8));
    s2 <= to_unsigned(5, s2'length);
    s2 <= resize(s2, 8);
    s1 <= s1 sll 2;
    s2 <= s2 sll 2;
    s1 <= not s1;
    s1 <= s1 and "00001111";
    b1 <= s1 = "00000000";
    b1 <= (s1 = x"00") and (s2 /= 0);
    v := to_integer(s2) * 2;
    v := character'pos('a');
    v := integer'(3) + v;
    b1 <= minimum(1, 2) = 1;
    b1 <= maximum(s2, s2) = s2;
    r := 2.0 * 3.0;`},
	{name: "2008 operators", decls: `
  constant k1 : std_logic := and s1;
  constant k2 : std_logic := xor s1;
  constant k3 : std_logic := s2 ?< s2;
  constant k4 : boolean := (s1 ?= s1) = '1';
  constant k5 : boolean := ?? '1';`, stmts: `
    if sl then end if;
    if sl and '1' then end if;
    if s2 ?= 3 then end if;
    sl <= '1' when b1 else '0';`},
	{name: "matching operator not for slv", decls: `
  constant k : std_logic := s1 ?< s1; --!`, want: "sema/no-overload"},
	{name: "?? returns BOOLEAN", decls: `
  constant k : bit := ?? '1'; --!`, want: "sema/no-overload",
		text: "returns BOOLEAN, not BIT"},
	{name: "condition before 2008", std: token.Std1993, stmts: `
    if sl then end if; --!`, want: "sema/type-mismatch",
		text: "expected BOOLEAN"},
	{name: "reduction before 2008", std: token.Std1993, decls: `
  constant k : std_logic := and s1; --!`, want: "syntax/standard"},
	{name: "report strings", stmts: `
    report "a" & "b";
    report "x" & integer'image(3) & lf;
    write(lv, string'("x"));
    write(lv, 3);`},
	{name: "assignment type", stmts: `
    v := 1.5; --!`, want: "sema/type-mismatch"},
	{name: "signal assignment type", stmts: `
    s2 <= s1; --!`, want: "sema/type-mismatch",
		text: "found STD_LOGIC_VECTOR"},
}

// TestTyping checks expression typing and overload resolution on
// small cases: a clean case has no diagnostic, an error case exactly
// one, on the line marked --!, with the expected code.
func TestTyping(t *testing.T) {
	for _, c := range typingCases {
		t.Run(c.name, func(t *testing.T) {
			src := typingSource(c)
			std := "2008"
			if c.std == token.Std1993 {
				std = "1993"
			}
			r := analyze(t, map[string]string{"t.vhd": src},
				func(cf *config.Config) { cf.Standard = std })
			got := diagText(r)
			if c.want == "" {
				if got != "" {
					t.Fatalf("unexpected:\n%s", got)
				}
				return
			}
			line := 0
			for i, l := range strings.Split(src, "\n") {
				if strings.Contains(l, "--!") {
					line = i + 1
				}
			}
			found := false
			for _, d := range r.Diagnostics {
				if d.Line != line {
					t.Fatalf("diagnostic off line %d:\n%s", line, got)
				}
				found = found || string(d.Code) == c.want &&
					strings.Contains(d.Message, c.text)
			}
			if !found {
				t.Fatalf("want %s on line %d containing %q, got:\n%s",
					c.want, line, c.text, got)
			}
		})
	}
}
