package elab

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/eval"
)

// TestInferredSignal: a signal of an unconstrained subtype (VHDL-2019
// inferred constraints) takes its index ranges from its initial value,
// nested element ranges included, so the model's subtype is fully
// constrained, the trace declares the inferred ranges and an assignment
// of the wrong length is a length error.
func TestInferredSignal(t *testing.T) {
	src := `
entity tb is end entity tb;
architecture sim of tb is
  type words is array (natural range <>) of bit_vector;
  signal s : bit_vector := "1010";
  constant k : bit_vector(7 downto 0) := x"f0";
  signal d : bit_vector := k;
  signal w : words := ("001", "010");
begin
  process
  begin
    s <= "0110";
    wait for 1 ns;
    report to_string(s) & " " & integer'image(d'left) & " " &
      integer'image(w(1)'length);
    s <= "011";
    wait;
  end process;
end architecture sim;
`
	r, d := simulate19(t, src, "tb")
	want := map[string]string{
		"tb.s": "0 to 3", "tb.d": "7 downto 0", "tb.w": "0 to 1"}
	for _, s := range d.Model.Signals {
		w, ok := want[s.Path]
		if !ok {
			continue
		}
		delete(want, s.Path)
		if !eval.FullyConstrained(s.Type) {
			t.Errorf("%s: subtype not fully constrained", s.Path)
			continue
		}
		b := s.Type.Bounds[0]
		got := b.String()
		if got != w {
			t.Errorf("%s: bounds %s, want %s", s.Path, got, w)
		}
	}
	if len(want) != 0 {
		t.Errorf("signals not elaborated: %v", want)
	}
	got := r.notes()
	if len(got) != 2 ||
		got[0] != "hint sim/assert-note report note: 0110 7 3" ||
		!strings.Contains(got[1], "length") {
		t.Errorf("diagnostics %q", got)
	}
	for _, l := range []string{"signal tb.d bit_vector signal 7 downto 0",
		"signal tb.s bit_vector signal 0 to 3"} {
		if !strings.Contains(string(r.text), l+"\n") {
			t.Errorf("trace has no %q:\n%s", l, r.text)
		}
	}
}

// TestInferredVariable: variables and constants take the open
// constraints of their subtype from the initial value, the element
// constraint of an array of unconstrained arrays and the field
// constraint of a record too; a later assignment must match them.
func TestInferredVariable(t *testing.T) {
	src := `
entity tb is end entity tb;
architecture sim of tb is
  type words is array (natural range <>) of bit_vector;
  type rec is record
    tag : string;
    n   : integer;
  end record rec;
begin
  process
    variable inf : bit_vector := "10110";
    variable wa  : words := (x"0f", x"f0", x"00");
    variable r   : rec := (tag => "abcd", n => 1);
  begin
    report integer'image(inf'length) & " " & integer'image(wa'length) &
      " " & integer'image(wa(2)'length) & " " &
      integer'image(r.tag'length);
    wa(1) := x"aa";
    r.tag := "wxyz";
    report to_hstring(wa(1)) & " " & r.tag;
    inf := "111";
    wait;
  end process;
end architecture sim;
`
	r, _ := simulate19(t, src, "tb")
	got := r.notes()
	if len(got) != 3 ||
		got[0] != "hint sim/assert-note report note: 5 3 8 4" ||
		got[1] != "hint sim/assert-note report note: AA wxyz" ||
		!strings.Contains(got[2], "length") {
		t.Errorf("diagnostics %q", got)
	}
}
