package sema

import (
	"strings"
	"testing"
)

// TestConstantErrorOnce: a constant whose value violates its subtype
// is one static error, at the constant; objects and constants whose
// bounds or values read it are not reported again (they are not
// static), and an unrelated error after them still is.
func TestConstantErrorOnce(t *testing.T) {
	src := `entity n is end;
architecture a of n is
  constant K : natural := -1;
  signal s : bit_vector(K downto 0);
  constant J : integer := K + 1;
  constant L : natural := J - 5;
  constant M : positive := 0;
begin end;
`
	r := analyze(t, map[string]string{"n.vhd": src}, nil)
	got := strings.Split(strings.TrimSpace(diagText(r)), "\n")
	want := []string{"n.vhd:3:27: sema/static-error",
		"n.vhd:7:28: sema/static-error"}
	if len(got) != len(want) {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("got %s, want %s", got[i], want[i])
		}
	}
}
