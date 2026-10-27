package elab

import (
	"fmt"
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// The native bodies the interpreter runs itself (#59): rising_edge and
// falling_edge, whose parameter is a signal, and the resolution of
// std_logic nets. Every transition between two STD_ULOGIC values, on an
// unresolved signal and on a net of two drivers, prints the edge
// functions and the resolved value; the output and the trace are the
// same with native and with interpreted bodies, at several seeds.
func TestNativeEdgesAndResolution(t *testing.T) {
	src := prelude + `
entity tb is end entity;
architecture a of tb is
  constant vals : std_ulogic_vector(0 to 8) := "UX01ZWLH-";
  signal s : std_ulogic := 'U';
  signal d1, d2 : std_ulogic := 'Z';
  signal r : std_logic;
begin
  r <= d1;
  r <= d2;
  drive : process
  begin
    for i in vals'range loop
      for j in vals'range loop
        s <= vals(i);
        d1 <= vals(i);
        d2 <= vals(j);
        wait for 1 ns;
        s <= vals(j);
        d1 <= vals(j);
        wait for 1 ns;
      end loop;
    end loop;
    wait;
  end process;
  watch : process (s, r)
    variable l : line;
  begin
    write(l, std_ulogic'image(s) & boolean'image(rising_edge(s)) &
      boolean'image(falling_edge(s)) & std_logic'image(r) &
      boolean'image(rising_edge(r)) & boolean'image(falling_edge(r)));
    writeline(output, l);
  end process;
end architecture;
`
	for _, seed := range []uint64{0, 1, 2, 3} {
		native := simulate(t, src, "tb", runOpts{seed: seed})
		interp := simulate(t, src, "tb", runOpts{seed: seed,
			interpreted: true})
		if fmt.Sprint(native.diags) != fmt.Sprint(interp.diags) {
			t.Errorf("seed %d: diagnostics %v, interpreted %v", seed,
				native.diags, interp.diags)
		}
		if len(native.out) < 81 {
			t.Fatalf("seed %d: %d lines", seed, len(native.out))
		}
		if !slices.Equal(native.out, interp.out) {
			t.Errorf("seed %d: output differs:\n%v\n%v", seed,
				native.out, interp.out)
		}
		if string(native.text) != string(interp.text) {
			t.Errorf("seed %d: trace differs", seed)
		}
	}
}

// TestNativeResolver: the native resolution of ieee.std_logic_1164's
// resolved gives the interpreted resolver's value (as the kernel sees
// it, subtype name included) on every driver list of up to three
// drivers and on generated longer ones (P4).
func TestNativeResolver(t *testing.T) {
	cfg := config.Defaults()
	cfg.Standard = "2008"
	r := sema.Analyze(sema.Input{Config: cfg, Jobs: env.Sequential{},
		Files: []sema.Source{{Path: "t.vhd", Text: []byte(prelude +
			"entity e is end entity;\n")}}})
	lib, _ := r.Library("ieee")
	u, ok := lib.Unit("std_logic_1164")
	if !ok {
		t.Fatal("no ieee.std_logic_1164")
	}
	var fn *sema.Decl
	for _, d := range u.Region.Decls {
		if d.Name == "resolved" && d.Kind == sema.DeclFunction {
			fn = d
		}
	}
	if fn == nil {
		t.Fatal("no resolved")
	}
	resolver := func(native bool) *FuncResolver {
		d := &Design{r: r, Budget: eval.NewBudget(0), native: native}
		res, err := d.resolver(fn)
		if err != nil {
			t.Fatal(err)
		}
		return res.(*FuncResolver)
	}
	nat, interp := resolver(true), resolver(false)
	if !nat.native || interp.native {
		t.Fatalf("native %v, interpreted %v", nat.native, interp.native)
	}
	elem := nat.arr.Elem
	check := func(pos []int64) {
		ds := make([]eval.Value, len(pos))
		for i, p := range pos {
			ds[i] = eval.Enum(elem, p)
		}
		got, err := nat.Resolve(eval.NewBudget(0), ds)
		if err != nil {
			t.Fatal(err)
		}
		want, err := interp.Resolve(eval.NewBudget(0), ds)
		if err != nil {
			t.Fatal(err)
		}
		if got.I != want.I || got.T.Name != want.T.Name ||
			got.T.Range != want.T.Range {
			t.Errorf("%v: %d %s, interpreted %d %s", pos, got.I,
				got.T.Name, want.I, want.T.Name)
		}
	}
	var lists [][]int64
	for a := range int64(9) {
		lists = append(lists, []int64{a})
		for b := range int64(9) {
			lists = append(lists, []int64{a, b})
			for c := range int64(9) {
				lists = append(lists, []int64{a, b, c})
			}
		}
	}
	g := env.SplitMix64{State: 59}
	for range 500 {
		pos := make([]int64, 4+env.Intn(&g, 12))
		for i := range pos {
			pos[i] = int64(env.Intn(&g, 9))
		}
		lists = append(lists, pos)
	}
	for _, l := range lists {
		check(l)
	}
	if t.Failed() {
		t.Logf("%d driver lists", len(lists))
	}
}

// TestNativeBodiesBudget: a loop of TO_UNSIGNED calls that build a
// million elements each from scalar arguments ends in
// sim/budget-exceeded with native bodies as it does with interpreted
// ones: a native call is charged for the elements it builds (P7). The
// budget, 16M evaluation steps, covers 16 of the 100 calls.
func TestNativeBodiesBudget(t *testing.T) {
	src := prelude + `
entity tb is end entity;
architecture a of tb is
  function len_of(x : unsigned) return natural is
  begin
    return x'length;
  end function;
begin
  p : process
    variable n : natural := 0;
  begin
    for i in 1 to 100 loop
      n := n + len_of(to_unsigned(0, 1000000));
    end loop;
    report integer'image(n);
    wait;
  end process;
end architecture;
`
	for _, interpreted := range []bool{false, true} {
		r := simulate(t, src, "tb", runOpts{steps: 1_000_000,
			interpreted: interpreted})
		if len(r.diags) != 1 ||
			r.diags[0].Code != diag.SimBudgetExceeded {
			t.Errorf("interpreted %v: diagnostics %v", interpreted,
				r.diags)
		}
	}
}
