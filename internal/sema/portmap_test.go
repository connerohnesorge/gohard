package sema

import (
	"strings"
	"testing"
)

// TestConversionFormals: a formal written as a conversion function or
// a type conversion applied to the port, f(port) => actual, names that
// port (LRM 6.5.7.1); the reference goes to the port and the function
// resolves. A name that is no port inside the conversion is still an
// error.
func TestConversionFormals(t *testing.T) {
	src := `library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
entity sub is
  port (x : in boolean; y : out signed(3 downto 0));
end;
architecture a of sub is begin y <= "0000"; end;
library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
entity top is end;
architecture a of top is
  function to_bool(b : bit) return boolean is begin return b = '1'; end;
  function to_bit(b : boolean) return bit is
  begin if b then return '1'; end if; return '0'; end;
  signal s : bit;
  signal v : std_logic_vector(3 downto 0);
begin
  u: entity work.sub port map (
    x => to_bool(s),
    std_logic_vector(y) => v);
  w: entity work.sub port map (
    to_bit(x) => s,
    std_logic_vector(z) => v);
end;
`
	files := map[string]string{"t.vhd": src}
	r := analyze(t, files, nil)
	got := strings.TrimSpace(diagText(r))
	want := "t.vhd:24:22: sema/error: z is not a port of sub"
	if !strings.HasPrefix(got, want) || strings.Contains(got, "\n") {
		t.Fatalf("got\n%s\nwant %s", got, want)
	}
	off := strings.Index(src, "std_logic_vector(y)") +
		len("std_logic_vector(")
	ref, ok := r.RefAt("t.vhd", off)
	if !ok || ref.Decl == nil || ref.Decl.Name != "y" ||
		ref.Decl.Kind != DeclPort {
		t.Errorf("y in the formal: %+v, want the port y", ref)
	}
	off = strings.Index(src, "to_bit(x)")
	ref, ok = r.RefAt("t.vhd", off)
	if !ok || ref.Decl == nil || ref.Decl.Name != "to_bit" {
		t.Errorf("to_bit in the formal: %+v, want the function", ref)
	}
}

// TestPartialFormals: named associations that name a subelement or a
// slice of a parameter (v.a => x, w(1) => y, w(0 to 1) => z) type the
// actual against that part, not the whole parameter.
func TestPartialFormals(t *testing.T) {
	src := `entity e is end;
architecture behav of e is
  type rec is record a : bit; w : bit_vector(0 to 3); end record;
  procedure check (v : rec) is begin end;
  procedure asgn (w : bit_vector(0 to 3)) is begin end;
begin
  process
    variable b : bit;
    variable bv : bit_vector(0 to 1);
  begin
    check (v.a => b, v.w(0) => b, v.w(1 to 2) => bv, v.w(3) => '1');
    asgn (w(0) => '1', w(1) => b, w(2 to 3) => bv);
    check (v.a => bv, v.w => "0000");
    wait;
  end process;
end;
`
	r := analyze(t, map[string]string{"e.vhd": src}, nil)
	got := strings.TrimSpace(diagText(r))
	if !strings.HasPrefix(got, "e.vhd:13:5: sema/no-overload") ||
		strings.Contains(got, "\n") {
		t.Fatalf("got\n%s\nwant one no-overload at 13:5", got)
	}
}

// TestArchitectureNameHidden: an architecture named like a port of its
// entity does not hide the port inside the architecture.
func TestArchitectureNameHidden(t *testing.T) {
	src := `entity e is port (a : in bit; b : out bit); end;
architecture a of e is
begin
  b <= a;
  process (a) begin
    case a is when '0' => null; when others => null; end case;
  end process;
end architecture a;
`
	r := analyze(t, map[string]string{"e.vhd": src}, nil)
	if got := diagText(r); got != "" {
		t.Fatalf("diagnostics:\n%s", got)
	}
	off := strings.Index(src, "b <= a") + len("b <= ")
	ref, ok := r.RefAt("e.vhd", off)
	if !ok || ref.Decl == nil || ref.Decl.Kind != DeclPort {
		t.Errorf("a in b <= a: %+v, want the port", ref)
	}
}
