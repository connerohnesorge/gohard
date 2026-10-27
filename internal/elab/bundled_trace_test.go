package elab

import (
	"strings"
	"testing"
)

// An assertion in a bundled ieee body (numeric_std's metavalue
// warning, run from source: the native "<" declines a metavalue) is a
// trace event naming the bundled file and line, so the trace is valid
// (#59: NEORV32 lost its trace on such warnings); the diagnostic has no
// position, as before.
func TestBundledAssertTraceEvent(t *testing.T) {
	src := prelude + `
entity tb is end entity;
architecture a of tb is
  signal u : unsigned(1 downto 0) := "0X";
begin
  p : process
  begin
    if u < 1 then
      report "less";
    end if;
    wait;
  end process;
end architecture;
`
	for _, interpreted := range []bool{false, true} {
		r := simulate(t, src, "tb", runOpts{interpreted: interpreted})
		if r.text == nil {
			t.Fatalf("interpreted %v: no trace", interpreted)
		}
		want := `! assert warning ieee/numeric_std-body.vhdl:`
		if !strings.Contains(string(r.text), want) {
			t.Errorf("interpreted %v: no %q in\n%s", interpreted, want,
				r.text)
		}
		for _, d := range r.diags {
			if strings.Contains(d.Message, "metavalue") && d.Path != "" {
				t.Errorf("diagnostic at %s: %s", d.Path, d.Message)
			}
		}
	}
}
