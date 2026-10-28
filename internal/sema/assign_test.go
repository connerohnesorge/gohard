package sema

import (
	"fmt"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/diag"
)

// assignForms are the assignment statements whose static widths and
// indices are checked with the unit (#34): concurrent, in a process,
// and a variable assignment. %s is the value.
var assignForms = map[string]string{
	"concurrent": "begin\n  x <= %s;\n",
	"process":    "begin\n  process begin\n    x <= %s;\n    wait;\n  end process;\n",
	"variable": "begin\n  process\n    variable x : bit_vector(T downto 0);\n" +
		"  begin\n    x := %s;\n    wait;\n  end process;\n",
}

func assignSrc(form string, tw int, value string) string {
	body := fmt.Sprintf(assignForms[form], value)
	return strings.ReplaceAll(fmt.Sprintf(`entity e is end;
architecture a of e is
  signal x : bit_vector(T downto 0);
  signal y : bit_vector(3 downto 0);
  signal w : bit_vector(0 to 5);
%send;
`, body), "T", fmt.Sprint(tw-1))
}

func staticErrors(r *Result) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range r.Diagnostics {
		if d.Code == diag.SemaStaticError {
			out = append(out, d)
		}
	}
	return out
}

// TestAssignWidths: for every form and every pair of static target and
// value widths, exactly one static error is reported when they differ
// and none when they agree; slices with explicit bounds count with
// their own length.
func TestAssignWidths(t *testing.T) {
	for form := range assignForms {
		for tw := 1; tw <= 5; tw++ {
			for vw := 1; vw <= 6; vw++ {
				val := fmt.Sprintf("w(0 to %d)", vw-1)
				r := analyze(t, map[string]string{
					"a.vhd": assignSrc(form, tw, val)}, nil)
				got := staticErrors(r)
				want := 0
				if tw != vw {
					want = 1
				}
				if len(got) != want {
					t.Errorf("%s tw=%d vw=%d: got %v", form, tw, vw,
						diagText(r))
				}
				if want == 1 && !strings.Contains(got[0].Message,
					fmt.Sprintf("expected %d elements, got %d", tw,
						vw)) {
					t.Errorf("%s: message %q", form, got[0].Message)
				}
			}
		}
	}
}

// TestAssignIndices: an index of a value is an error exactly when it
// leaves the prefix's range, in every form.
func TestAssignIndices(t *testing.T) {
	for form := range assignForms {
		for i := 0; i <= 6; i++ {
			val := fmt.Sprintf("(0 => y(%d))", i)
			r := analyze(t, map[string]string{
				"a.vhd": assignSrc(form, 1, val)}, nil)
			got := len(staticErrors(r))
			if want := map[bool]int{true: 1, false: 0}[i > 3]; got != want {
				t.Errorf("%s y(%d): got %s", form, i, diagText(r))
			}
		}
	}
}

// TestAssignNotTaken: values in code that a static condition may never
// run (conditional and selected waveforms, if/case/loop branches,
// generate bodies) and runtime-only failures are not reported with the
// unit; nor is the null waveform.
func TestAssignNotTaken(t *testing.T) {
	src := `entity e is end;
architecture a of e is
  constant K : natural := 0;
  signal x : bit_vector(1 downto 0);
  signal y : bit_vector(3 downto 0);
  signal b : bit;
begin
  x <= y when K = 1 else "00";
  with K select x <= y when 1, "00" when others;
  process begin
    if K = 1 then x <= y; b <= y(7); end if;
    case K is when 1 => x <= y; when others => null; end case;
    for i in 1 to K loop x <= y; end loop;
    wait;
  end process;
  g : if K = 1 generate x <= y; end generate;
end;
`
	r := analyze(t, map[string]string{"a.vhd": src}, nil)
	if got := staticErrors(r); len(got) != 0 {
		t.Errorf("got %s", diagText(r))
	}
}
