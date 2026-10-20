package sema

import (
	"strings"
	"testing"
)

// TestBindingSeesComponentPorts: the actuals of a binding indication
// in a configuration specification denote the bound component's local
// generics and ports (LRM 7.3.2.2, 12.1), and a name that is neither
// still is not declared.
func TestBindingSeesComponentPorts(t *testing.T) {
	src := `entity e is
  generic (w : integer := 1); port (a : in bit; b : out bit);
end;
architecture rtl of e is begin end;
entity top is end;
architecture t of top is
  component c is
    generic (g : integer := 1); port (x : in bit; y : out bit);
  end component;
  signal s, z : bit;
  for u : c use entity work.e(rtl)
    generic map (w => g) port map (a => x, b => y);
  for v : c use entity work.e(rtl) port map (a => nope, b => y);
begin
  u : c port map (s, z);
  v : c port map (s, z);
end;
`
	r := analyze(t, map[string]string{"t.vhd": src}, nil)
	got := diagText(r)
	if strings.Count(got, "\n") != 1 ||
		!strings.Contains(got, "nope is not declared") {
		t.Fatalf("want only nope undefined, got:\n%s", got)
	}
	// The port and generic of the component stay invisible outside
	// the binding indication.
	bad := strings.Replace(src, "u : c port map (s, z);",
		"u : c port map (x, z);", 1)
	r = analyze(t, map[string]string{"t.vhd": bad}, nil)
	if !strings.Contains(diagText(r), "x is not declared") {
		t.Fatalf("x visible outside binding:\n%s", diagText(r))
	}
}
