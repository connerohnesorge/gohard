package elab

// fx2_verify_test.go holds the regression tests of the second v1.1
// Stage 3 verification round (#18): the methods of an object of a
// generic protected type take the object's generic type actuals in
// their profiles (they were checked against the uninstantiated
// profile: a false sema/no-overload, or please-report at run time),
// and a pure function that ends without a return statement is the
// design's run-time error, not a GoHard gap (it said please report).

import (
	"slices"
	"strings"
	"testing"
)

// fx2Box is a generic protected type whose methods take and return its
// generic type t; its users follow on line 15.
const fx2Box = `entity tb is end entity;
architecture a of tb is
  type box is protected
    generic (type t; d : t);
    procedure set(x : t);
    impure function get return t;
    impure function dflt return t;
  end protected;
  type box is protected body
    variable v : t := d;
    procedure set(x : t) is begin v := x; end procedure;
    impure function get return t is begin return v; end function;
    impure function dflt return t is begin return d; end function;
  end protected body;
`

// TestFx2ProtGenericProfiles: for every form that binds the generics
// (a variable's generic map, a protected type instantiation, an
// object reached through an access value) and array, scalar and string
// actuals, a method's result is of the actual type: indexed, compared
// with a literal, concatenated and given to an operator; and a literal
// or aggregate actual of a parameter of the generic type is of the
// actual type.
func TestFx2ProtGenericProfiles(t *testing.T) {
	type form struct {
		name, decls, vars, obj string
	}
	forms := func(actual string) []form {
		return []form{
			{"variable", "", "    variable b : box generic map (t => " +
				actual + ");\n", "b"},
			{"instance", "  type bi is new box generic map (t => " +
				actual + ");\n", "    variable b : bi;\n", "b"},
			{"shared", "  type bi is new box generic map (t => " +
				actual + ");\n  shared variable b : bi;\n", "", "b"},
			{"access", "  type bi is new box generic map (t => " +
				actual + ");\n  type bp is access bi;\n",
				"    variable p : bp := new bi;\n", "p"},
		}
	}
	cases := []struct {
		name, types, actual, dflt, body string
		want                            []string
	}{
		{name: "array", types: "  type iv is array (1 to 3) of " +
			"integer;\n", actual: "iv, d => (7, 8, 9)",
			body: "    report integer'image(%[1]s.get(1));\n" +
				"    %[1]s.set((1, 2, 3));\n" +
				"    report integer'image(%[1]s.get(3) + %[1]s.dflt(2));\n" +
				"    assert %[1]s.get = (1, 2, 3) severity failure;\n" +
				"    assert %[1]s.get(2) = 2 severity failure;\n",
			want: []string{"7", "11"}},
		{name: "integer", actual: "integer, d => 5",
			body: "    %[1]s.set(40);\n" +
				"    report integer'image(%[1]s.get + 2);\n" +
				"    assert %[1]s.get > %[1]s.dflt severity failure;\n" +
				"    report integer'image(-%[1]s.get);\n",
			want: []string{"42", "-40"}},
		{name: "bit", actual: "bit, d => '1'",
			body: "    if %[1]s.get = '1' then report \"one\"; end if;\n" +
				"    %[1]s.set('0');\n" +
				"    report bit'image(%[1]s.get and %[1]s.dflt);\n",
			want: []string{"one", "'0'"}},
		{name: "string", actual: "string(1 to 3), d => \"abc\"",
			body: "    report %[1]s.get & \"!\" & %[1]s.get(2);\n" +
				"    %[1]s.set(\"xyz\");\n" +
				"    report %[1]s.dflt & %[1]s.get;\n" +
				"    assert %[1]s.get = \"xyz\" severity failure;\n",
			want: []string{"abc!b", "abcxyz"}},
	}
	for _, c := range cases {
		for _, f := range forms(c.actual) {
			t.Run(c.name+"/"+f.name, func(t *testing.T) {
				obj := f.obj
				if f.name == "access" {
					obj = "p.all"
				}
				body := strings.ReplaceAll(c.body, "%[1]s", obj)
				src := fx2Box + c.types + f.decls +
					"begin\n  process\n" + f.vars + "  begin\n" + body +
					"    wait;\n  end process;\nend architecture;\n"
				fxWant(t, oSim(t, src, "tb", "2019"), c.want...)
			})
		}
	}
}

// TestFx2ProtGenericObjectsDiffer: two objects of one generic protected
// type with different generic type actuals each resolve their methods
// against their own actual, in one expression too.
func TestFx2ProtGenericObjectsDiffer(t *testing.T) {
	src := fx2Box + `  type iv is array (1 to 2) of integer;
begin
  process
    variable a : box generic map (t => iv, d => (3, 4));
    variable s : box generic map (t => string(1 to 2), d => "hi");
    variable n : box generic map (t => integer, d => 1);
  begin
    report integer'image(a.get(2) + n.get) & s.get & s.get(1);
    a.set((n.get, n.dflt + 9));
    report integer'image(a.get(1) * a.get(2));
    wait;
  end process;
end architecture;
`
	fxWant(t, oSim(t, src, "tb", "2019"), "5hih", "10")
}

// TestFx2NoReturn: a function that reaches the end of its body without
// a return statement (LRM 4.2) is the design's run-time error
// (sim/runtime-error, as NVC's "did not return a value"), pure or
// impure, called in a process or for a constant of the architecture,
// with a plain or a VHDL-2019 conditional return; never please-report.
func TestFx2NoReturn(t *testing.T) {
	bodies := map[string]string{
		"plain": "if x > 10 then return 1; end if;",
		"cond":  "return 1 when x > 10;",
	}
	for _, kind := range []string{"plain", "cond"} {
		for _, pure := range []string{"", "impure "} {
			for _, at := range []string{"process", "constant"} {
				name := kind + "/" + pure + at
				t.Run(name, func(t *testing.T) {
					decl, use := "", "f(1)"
					if at == "constant" {
						if pure != "" {
							t.Skip("an impure function in a constant " +
								"of the architecture runs at elaboration")
						}
						decl, use = "  constant c : integer := f(1);\n",
							"c"
					}
					src := "entity tb is end entity;\n" +
						"architecture a of tb is\n" +
						"  " + pure + "function f(x : integer) " +
						"return integer is\n  begin\n    " +
						bodies[kind] + "\n  end function;\n" + decl +
						"begin\n  process\n  begin\n" +
						"    report integer'image(f(11));\n" +
						"    report integer'image(" + use + ");\n" +
						"    wait;\n  end process;\nend architecture;\n"
					r := oSim(t, src, "tb", "2019")
					es := fxErrors(r)
					ok := len(es) == 1 && strings.HasPrefix(es[0],
						"sim/runtime-error: ") && strings.Contains(es[0],
						"function f ended without a return statement")
					if !ok {
						t.Fatalf("diagnostics %q", r.diags)
					}
					if at == "process" && !slices.Equal(fxReports(r),
						[]string{"1"}) {
						t.Errorf("reports %q", fxReports(r))
					}
				})
			}
		}
	}
}
