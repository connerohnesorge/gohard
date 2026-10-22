package elab

// fx_verify_test.go holds the regression tests of the v1.1 Stage 3
// verification fixes (#18 Done when: nothing fails silently, and a
// legal construct either works or is unsupported/construct, please
// report): each test is a design that failed, silently or with a
// generic run-time error, before the fix.

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// fxReports returns the messages of the reports of r, in order.
func fxReports(r oRun) []string {
	var out []string
	for _, d := range r.diags {
		if m, ok := strings.CutPrefix(d,
			"sim/assert-note: report note: "); ok {
			out = append(out, m)
		}
	}
	return out
}

// fxErrors returns the diagnostics of r that are not reports or
// assertion warnings.
func fxErrors(r oRun) []string {
	var out []string
	for _, d := range r.diags {
		if !strings.HasPrefix(d, "sim/assert-note:") &&
			!strings.HasPrefix(d, "sim/assert-warning:") {
			out = append(out, d)
		}
	}
	return out
}

func fxWant(t *testing.T, r oRun, want ...string) {
	t.Helper()
	if es := fxErrors(r); len(es) > 0 {
		t.Fatalf("diagnostics: %q", es)
	}
	if got := fxReports(r); !slices.Equal(got, want) {
		t.Errorf("reports:\n got %q\nwant %q", got, want)
	}
}

// TestFxEnvEnumToString: the implicit TO_STRING of the enumeration
// types std.env declares is the implicit one, not std.env's (it gave
// "" with no error).
func TestFxEnvEnumToString(t *testing.T) {
	r := oSim(t, `use std.env.all;
entity tb is end entity;
architecture a of tb is
begin
  process
    variable d : dayofweek := thursday;
    variable o : dir_open_status := status_not_found;
    variable c : dir_create_status;
    variable f : file_delete_status := status_access_denied;
  begin
    report to_string(d) & " " & to_string(o) & " " & to_string(c) &
      " " & to_string(f);
    for x in dayofweek loop
      assert to_string(x) = dayofweek'image(x) severity failure;
    end loop;
    wait;
  end process;
end architecture;
`, "tb", "2019")
	fxWant(t, r, "thursday status_not_found status_ok "+
		"status_access_denied")
}

// TestFxReflectionIndex: the implicit operations of std.reflection's
// INDEX and INDEX_VECTOR are the implicit ones (TO_STRING was sent to
// std.reflection and reported as unsupported).
func TestFxReflectionIndex(t *testing.T) {
	r := oSim(t, `use std.reflection.all;
entity tb is end entity;
architecture a of tb is
begin
  process
    variable x : index := 3;
    variable iv : index_vector(1 to 2) := (5, 6);
  begin
    report to_string(x) & " " & to_string(iv(2)) & " " & to_string(iv);
    wait;
  end process;
end architecture;
`, "tb", "2019")
	fxWant(t, r, "3 6 (5,6)")
}

// TestFxToStringArrays: TO_STRING of an array of a character type is
// its characters, of any other scalar array its 'IMAGE (VHDL-2019, as
// NVC 1.23), for every element type and length.
func TestFxToStringArrays(t *testing.T) {
	for n := 0; n <= 3; n++ {
		var ints, bits []string
		for i := range n {
			ints = append(ints, fmt.Sprint(i*7-3))
			bits = append(bits, fmt.Sprint(i%2))
		}
		agg := func(xs []string, q string) string {
			if len(xs) == 0 {
				return ""
			}
			var es []string
			for i, x := range xs {
				es = append(es, fmt.Sprintf("%d => %s%s%s", i, q, x, q))
			}
			return "(" + strings.Join(es, ", ") + ")"
		}
		decl := func(name, typ string, xs []string, q string) string {
			s := fmt.Sprintf("    variable %s : %s(0 to %d)", name, typ,
				n-1)
			if a := agg(xs, q); a != "" {
				s += " := " + a
			}
			return s + ";\n"
		}
		src := "entity tb is end entity;\narchitecture a of tb is\n" +
			"begin\n  process\n" +
			decl("iv", "integer_vector", ints, "") +
			decl("bv", "bit_vector", bits, "'") +
			"  begin\n    report \"[\" & to_string(iv) & \"][\" & " +
			"to_string(bv) & \"]\";\n    wait;\n  end process;\n" +
			"end architecture;\n"
		want := "[(" + strings.Join(ints, ",") + ")][" +
			strings.Join(bits, "") + "]"
		if n == 0 {
			want = "[()][]"
		}
		fxWant(t, oSim(t, src, "tb", "2019"), want)
	}
}

// TestFxProtectedConstrainedActual: an anonymous constrained subtype
// as a generic type actual (of a protected type, a type instance, a
// subprogram instance) keeps its constraint (it failed as "the default
// value of unconstrained BIT_VECTOR is not static").
func TestFxProtectedConstrainedActual(t *testing.T) {
	for _, c := range []struct{ actual, want string }{
		{"bit_vector(0 to 2)", "3"},
		{"bit_vector(7 downto 3)", "5"},
		{"string(1 to 4)", "4"},
		{"integer_vector(1 to 1)", "1"},
	} {
		r := oSim(t, `entity tb is end entity;
architecture a of tb is
  type box is protected
    generic (type t);
    impure function size return natural;
  end protected;
  type box is protected body
    variable v : t;
    impure function size return natural is
    begin
      return v'length;
    end function;
  end protected body;
  type inst is new box generic map (t => `+c.actual+`);
  procedure p generic (type t) parameter (x : integer) is
    variable w : t;
  begin
    report integer'image(w'length);
  end procedure;
  procedure q is new p generic map (t => `+c.actual+`);
begin
  process
    variable b : box generic map (t => `+c.actual+`);
    variable i : inst;
  begin
    report integer'image(b.size) & integer'image(i.size);
    q(0);
    wait;
  end process;
end architecture;
`, "tb", "2019")
		fxWant(t, r, c.want+c.want, c.want)
	}
}

// TestFxSelectedRangeChoice: range choices of selected signal
// assignments, concurrent and sequential, select as a case statement
// does, for every selector value.
func TestFxSelectedRangeChoice(t *testing.T) {
	for sel := -1; sel <= 10; sel++ {
		in := func(lo, hi int) bool { return sel >= lo && sel <= hi }
		bit := func(b bool) string {
			if b {
				return "'1'"
			}
			return "'0'"
		}
		r := oSim(t, fmt.Sprintf(`entity tb is end entity;
architecture a of tb is
  signal sel : integer := %d;
  signal x, y : bit;
begin
  with sel select x <= '1' when 0 to 3 | 8, '0' when others;
  process
  begin
    with sel select y <= '1' when 9 downto 5, '0' when others;
    wait for 1 ns;
    report bit'image(x) & bit'image(y);
    wait;
  end process;
end architecture;
`, sel), "tb", "2008")
		fxWant(t, r, bit(in(0, 3) || sel == 8)+bit(in(5, 9)))
	}
}

// TestFxSubprogramInstanceNames: the names of a declaration of a
// subprogram instance use the instance's designator (LRM 4.4, as NVC
// 1.23), for a procedure the interpreter runs and a pure function the
// static evaluator runs, each instance its own.
func TestFxSubprogramInstanceNames(t *testing.T) {
	r := oSim(t, `entity tb is end entity;
architecture a of tb is
  procedure pp generic (n : integer) parameter (x : integer) is
    variable w : bit;
  begin
    report w'path_name;
  end procedure;
  procedure q1 is new pp generic map (n => 1);
  procedure q2 is new pp generic map (n => 2);
  function ff generic (n : integer) return string is
    variable w : bit;
  begin
    return w'instance_name;
  end function;
  function f1 is new ff generic map (n => 1);
  function f2 is new ff generic map (n => 2);
begin
  process
  begin
    q1(1);
    q2(2);
    report f1 & " " & f2;
    wait;
  end process;
end architecture;
`, "tb", "2019")
	fxWant(t, r, ":tb:q1[integer]:w", ":tb:q2[integer]:w",
		":tb(a):f1[return string]:w :tb(a):f2[return string]:w")
}

// TestFxElabAsserts: the assertion and report statements of the
// functions elaboration runs are reported and counted once per
// evaluation of a declaration (they were dropped), for any number of
// constants and signals; a process reading the constants does not
// run them again.
func TestFxElabAsserts(t *testing.T) {
	for n := 1; n <= 4; n++ {
		var decls, reads []string
		for i := range n {
			decls = append(decls,
				fmt.Sprintf("  constant c%d : integer := f(%d);", i, i),
				fmt.Sprintf("  signal s%d : integer := f(%d);", i,
					100+i))
			reads = append(reads, fmt.Sprintf("c%d + s%d", i, i))
		}
		src := `use std.env.all;
entity tb is end entity;
architecture a of tb is
  function f (x : integer) return integer is
  begin
    report "f" & integer'image(x);
    assert false report "w" & integer'image(x) severity warning;
    return x;
  end function;
` + strings.Join(decls, "\n") + `
begin
  process
    variable sum : integer;
  begin
    sum := ` + strings.Join(reads, " + ") + `;
    report "count " & integer'image(GetVhdlAssertCount);
    wait;
  end process;
end architecture;
`
		r := oSim(t, src, "tb", "2019")
		got := fxReports(r)
		count := fmt.Sprintf("count %d", 2*n)
		if len(got) == 0 || got[len(got)-1] != count {
			t.Fatalf("n=%d: reports %q, want the last %q", n, got, count)
		}
		for i := range n {
			for _, x := range []int{i, 100 + i} {
				m := fmt.Sprintf("f%d", x)
				if k := strings.Count(strings.Join(got, "|")+"|",
					m+"|"); k != 1 {
					t.Errorf("n=%d: %s reported %d times: %q", n, m, k,
						got)
				}
			}
		}
	}
}

// TestFxNotStaticPleaseReport: a plain not-static error stopping a
// running process is a GoHard gap (unsupported/construct, please
// report); an evaluator gap (eval.ErrCodeUnsupported) too; any other
// error is the design's.
func TestFxNotStaticPleaseReport(t *testing.T) {
	ns := fmt.Errorf("x: %w", eval.NotStatic("the value of y"))
	if what, ok := notStatic(ns); !ok || what != "the value of y" {
		t.Errorf("notStatic(%v) = %q, %v", ns, what, ok)
	}
	for _, err := range []error{nil, errors.New("plain"),
		&eval.Error{Code: eval.ErrCodeNotStatic,
			Msg: "the signal clk cannot be read during elaboration"},
		&eval.Error{Code: eval.ErrCodeRange, Msg: "z is not static"},
	} {
		if _, ok := notStatic(err); ok {
			t.Errorf("notStatic(%v): a design error", err)
		}
	}
	un := fmt.Errorf("p: %w", eval.Unsupported("'foo of an array"))
	if what, ok := sema.UnsupportedWhat(un); !ok ||
		what != "'foo of an array" {
		t.Errorf("UnsupportedWhat(%v) = %q, %v", un, what, ok)
	}
}

// TestFxUnboundTopGeneric: a testbench generic without a default is
// the design's error (sim/runtime-error), not a please-report gap.
func TestFxUnboundTopGeneric(t *testing.T) {
	r := oSim(t, `entity sub is
  generic (g : integer);
end entity;
architecture a of sub is
  signal t : integer := g * 100;
begin
end architecture;
`, "sub", "2019")
	want := "sim/runtime-error: the generic g of the testbench sub " +
		"has no default in sub at 0 fs"
	if !slices.Equal(r.diags, []string{want}) {
		t.Errorf("diagnostics %q, want %q", r.diags, want)
	}
}

// TestFxArrayExtremum: MINIMUM and MAXIMUM of an array of a scalar
// type in a process (they failed as "minimum is not defined for array
// operands").
func TestFxArrayExtremum(t *testing.T) {
	r := oSim(t, `entity tb is end entity;
architecture a of tb is
begin
  process
    variable iv : integer_vector(1 to 4) := (5, -2, 9, 0);
    variable nv : integer_vector(1 to 0);
  begin
    report integer'image(minimum(iv)) & " " &
      integer'image(maximum(iv)) & " " &
      character'image(maximum(string'("vhdl")));
    assert minimum(nv) = integer'high and maximum(nv) = integer'low
      severity failure;
    wait;
  end process;
end architecture;
`, "tb", "2008")
	fxWant(t, r, "-2 9 'v'")
}

// TestFxInferredOthersTarget: an aggregate with others assigned to an
// object whose constraint is inferred from its initial value takes
// the object's constraint (it was "an aggregate with others without
// a constrained context"), for variables and signals of every length.
func TestFxInferredOthersTarget(t *testing.T) {
	for n := 1; n <= 4; n++ {
		init := strings.Repeat("1", n)
		r := oSim(t, fmt.Sprintf(`entity tb is end entity;
architecture a of tb is
  signal sv : bit_vector := "%s";
begin
  process
    variable v : bit_vector := "%s";
    variable iv : integer_vector := (1 to %d => 7);
  begin
    v := (others => '0');
    iv := (others => 3);
    sv <= (0 => '1', others => '0');
    wait for 1 ns;
    report to_string(v) & " " & to_string(sv) & " " &
      integer'image(iv'length) & integer'image(iv(iv'right));
    wait;
  end process;
end architecture;
`, init, init, n), "tb", "2019")
		want := strings.Repeat("0", n) + " 1" + strings.Repeat("0", n-1) +
			" " + fmt.Sprint(n) + "3"
		fxWant(t, r, want)
	}
}
