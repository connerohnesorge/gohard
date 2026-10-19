package sema

import (
	"strings"
	"testing"
)

// TestAssertionParts: the report and severity of assertion and report
// statements are typed by their keyword, not by position, so a
// severity without a report is a SEVERITY_LEVEL and not the report
// STRING (sequential, concurrent and report statements), and a wrong
// type in either part is one mismatch at that part.
func TestAssertionParts(t *testing.T) {
	src := `entity e is end;
architecture a of e is
  signal s : bit;
begin
  assert s = '1' severity failure;
  l: postponed assert s = '1' severity note;
  assert s = '1' report "r" severity warning;
  assert s = '1' report "r";
  process begin
    assert s = '0' severity error;
    assert false report "x" severity failure;
    report "y" severity note;
    report "z";
    wait;
  end process;
end;
`
	r := analyze(t, map[string]string{"e.vhd": src}, nil)
	if got := diagText(r); got != "" {
		t.Fatalf("diagnostics:\n%s", got)
	}
	bad := `entity e is end;
architecture a of e is
begin
  assert true severity 5;
  process begin
    report 1 severity note;
    assert true report "y" severity 3;
    wait;
  end process;
end;
`
	r = analyze(t, map[string]string{"e.vhd": bad}, nil)
	got := strings.Split(strings.TrimSpace(diagText(r)), "\n")
	want := []string{"e.vhd:4:24:", "e.vhd:6:12:", "e.vhd:7:37:"}
	if len(got) != len(want) {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("got %s, want %s...", got[i], want[i])
		}
	}
}
