package sema

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env"
)

// s3_generic_test.go holds the regression tests of the second round of
// v1.1 Stage 2 fixes to generic subprograms (LRM 4.4, 6.5.3, 6.5.7):
// the actual of a generic subprogram conforms to the formal's profile,
// a generic type actual is of its formal's class, a VHDL-2019 generic
// type of a class has its class's implicit operations, positional
// associations of a generic map come first, an instance does not make
// a body object unconstrained, and a run-time read of a package
// instance's generic is reported as unsupported.

// TestS3SubprogramActualProfile: the actual of a generic subprogram
// conforms to the formal's profile with the generic types replaced by
// their actuals (LRM 6.5.7.2); NVC 1.23 rejects the illegal lines
// ("no visible subprogram SLT matches signature [T, T return
// BOOLEAN]").
func TestS3SubprogramActualProfile(t *testing.T) {
	head := []string{
		"entity tb is end;",
		"architecture a of tb is",
		"  function mx generic (type t; function lt(a, b : t) " +
			"return boolean)",
		"    parameter (a, b : t) return t is",
		"  begin if lt(a, b) then return b; end if; return a; end;",
		"  function slt(a, b : string) return boolean is",
		"  begin return a'length < b'length; end;",
		"  function ilt(a, b : integer) return boolean is",
		"  begin return a < b; end;",
		"  function one(a : integer) return boolean is",
		"  begin return a < 0; end;",
	}
	src := func(ls ...string) string {
		return lines(slices.Concat(head, ls, []string{
			"begin", "end;"})...)
	}
	checkExact(t, []wantCase{
		{name: "legal", src: src(
			"  function m1 is new mx generic map (t => integer, lt => ilt);",
			"  function m2 is new mx generic map (t => string, lt => slt);",
			"  function m3 is new mx generic map (t => integer, lt => \"<\");",
			"  constant c : integer := mx generic map (t => integer, "+
				"lt => ilt) (3, 8);",
		)},
		{name: "illegal", src: src(
			"  function m1 is new mx generic map (t => integer, lt => slt);",
			"  function m2 is new mx generic map (t => integer, lt => one);",
			"  constant c : integer := mx generic map (t => integer, "+
				"lt => slt) (3, 8);",
		), want: []string{
			"12: no visible subprogram slt matches the profile of " +
				"generic subprogram lt (LRM 6.5.7.2)",
			"13: no visible subprogram one matches the profile of " +
				"generic subprogram lt (LRM 6.5.7.2)",
			"14: no visible subprogram slt matches the profile of " +
				"generic subprogram lt (LRM 6.5.7.2)",
		}},
	})
}

// s3Classes lists the VHDL-2019 generic type classes (LRM 6.5.3) with
// the actual types each accepts, an independent model of
// genericTypeClass.
var s3Classes = []struct {
	def  string
	fits []string
}{
	{"private", []string{"integer", "real", "time", "color", "string",
		"ia"}},
	{"<>", []string{"integer", "real", "time", "color"}},
	{"(<>)", []string{"integer", "color"}},
	{"range <>", []string{"integer"}},
	{"range <> . <>", []string{"real"}},
	{"units <>", []string{"time"}},
	{"access integer", []string{"ia"}},
	{"array (natural range <>) of character", []string{"string"}},
}

// TestS3GenericTypeClass: for every class and actual type, the map is
// an error exactly when the model says the actual is of another class,
// in a VHDL-2008 style instance and in a call with a generic map.
func TestS3GenericTypeClass(t *testing.T) {
	actuals := []string{"integer", "real", "time", "color", "string",
		"ia"}
	need := map[string]string{
		"<>": "a scalar type", "(<>)": "a discrete type",
		"range <>": "an integer type", "range <> . <>": "a " +
			"floating-point type", "units <>": "a physical type",
		"access integer":                        "an access type",
		"array (natural range <>) of character": "an array type",
	}
	var ls, want []string
	ls = append(ls, "package p is",
		"  type color is (red, green);",
		"  type ia is access integer;")
	for i, c := range s3Classes {
		ls = append(ls, fmt.Sprintf("  procedure g%d generic (type t is "+
			"%s) parameter (x : integer);", i, c.def))
	}
	ls = append(ls, "end package;", "use work.p.all;",
		"entity tb is end;", "architecture a of tb is")
	for i, c := range s3Classes {
		for j, act := range actuals {
			ls = append(ls, fmt.Sprintf("  procedure i%d_%d is new g%d "+
				"generic map (t => %s);", i, j, i, act))
			if !slices.Contains(c.fits, act) {
				want = append(want, fmt.Sprintf("%d: cannot map type %s "+
					"to generic type t, which requires %s (LRM 6.5.3)",
					len(ls), s3TypeName(act), need[c.def]))
			}
		}
	}
	ls = append(ls, "begin", "  process begin")
	for i, c := range s3Classes {
		for _, act := range actuals {
			ls = append(ls, fmt.Sprintf("    g%d generic map (t => %s) "+
				"(1);", i, act))
			if !slices.Contains(c.fits, act) {
				want = append(want, fmt.Sprintf("%d: cannot map type %s "+
					"to generic type t, which requires %s (LRM 6.5.3)",
					len(ls), s3TypeName(act), need[c.def]))
			}
		}
	}
	ls = append(ls, "    wait;", "  end process;", "end;")
	got := errorLines(analyzeOpen(t, "2019", lines(ls...)))
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"),
			strings.Join(want, "\n"))
	}
}

// s3TypeName is how messages name the type act: std.standard's in
// upper case, the design's as declared.
func s3TypeName(act string) string {
	switch act {
	case "integer", "real", "time", "string":
		return strings.ToUpper(act)
	}
	return act
}

// TestS3ClassOverload: an overloaded uninstantiated name resolves to
// the instance whose generic type class fits the actual; the misfit
// one is dropped quietly.
func TestS3ClassOverload(t *testing.T) {
	src := lines(
		"package p is",
		"  function f generic (type t is range <>) (x : t; n : integer)",
		"    return t;",
		"  function f generic (type t is units <>) (n : integer; x : t)",
		"    return t;",
		"end package;",
		"package body p is",
		"  function f generic (type t is range <>) (x : t; n : integer)",
		"    return t is",
		"  begin return x + t(n); end;",
		"  function f generic (type t is units <>) (n : integer; x : t)",
		"    return t is",
		"  begin return x * n; end;",
		"end package body;",
		"use work.p.all;",
		"package q is",
		"  constant ci : integer := f generic map (t => integer)",
		"    (x => 4, n => 2);",
		"  constant ct : time := f generic map (t => time)",
		"    (x => 4 fs, n => 3);",
		"end package;",
	)
	r := analyzeOpen(t, "2019", src)
	if errs := errorLines(r); len(errs) != 0 {
		t.Fatalf("errors %q", errs)
	}
	s2Want(t, r, "q", map[string]string{"ci": "6", "ct": "12 fs"})
	r = analyzeOpen(t, "2019", lines(
		"package p is",
		"  function f generic (type t is range <>) (x : t) return t;",
		"end package;",
		"package body p is",
		"  function f generic (type t is range <>) (x : t) return t is",
		"  begin return x + 1; end;",
		"end package body;",
		"use work.p.all;",
		"package q is",
		"  constant ci : integer := f generic map (t => integer) (4);",
		"end package;",
	))
	s2Want(t, r, "q", map[string]string{"ci": "5"})
}

// TestS3ClassOps: a generic type of a class has the implicit
// operations of the class (LRM 6.5.3). A seeded property: for random
// operands the instance computes what the actual type's operators
// compute.
func TestS3ClassOps(t *testing.T) {
	pkg := lines(
		"package p is",
		"  function ar generic (type t is range <>) (x, y : t) return t;",
		"  function rl generic (type t is range <>) (x, y : t) "+
			"return boolean;",
		"  function ph generic (type t is units <>) (x : t) return t;",
		"  function fl generic (type t is range <> . <>) (x : t) "+
			"return t;",
		"  function di generic (type t is (<>)) (x, y : t) return t;",
		"end package;",
		"package body p is",
		"  function ar generic (type t is range <>) (x, y : t) "+
			"return t is",
		"  begin return (x + y) * (x - y) - abs(y) / 3 + x mod 7; end;",
		"  function rl generic (type t is range <>) (x, y : t) "+
			"return boolean is",
		"  begin return x < y or x >= y + 10; end;",
		"  function ph generic (type t is units <>) (x : t) "+
			"return t is",
		"  begin return x + x - x / 4; end;",
		"  function fl generic (type t is range <> . <>) (x : t) "+
			"return t is",
		"  begin return x * x + x / 2.0; end;",
		"  function di generic (type t is (<>)) (x, y : t) return t is",
		"  begin return maximum(x, y); end;",
		"end package body;",
	)
	rng := &env.SplitMix64{State: 18<<32 | 3}
	mod := func(a, b int64) int64 { return ((a % b) + b) % b }
	abs := func(a int64) int64 { return max(a, -a) }
	want := map[string]string{}
	var ls []string
	for i := range 12 {
		x := int64(env.Intn(rng, 2001)) - 1000
		y := int64(env.Intn(rng, 2001)) - 1000
		n := strconv.Itoa(i)
		ls = append(ls,
			fmt.Sprintf("  constant a%s : integer := ar generic map "+
				"(t => integer) (%d, %d);", n, x, y),
			fmt.Sprintf("  constant r%s : boolean := rl generic map "+
				"(t => integer) (%d, %d);", n, x, y),
			fmt.Sprintf("  constant d%s : integer := di generic map "+
				"(t => integer) (%d, %d);", n, x, y),
			fmt.Sprintf("  constant h%s : time := ph generic map "+
				"(t => time) (%d fs);", n, abs(x)*4))
		want["a"+n] = strconv.FormatInt((x+y)*(x-y)-abs(y)/3+mod(x, 7),
			10)
		want["r"+n] = strconv.FormatBool(x < y || x >= y+10)
		want["d"+n] = strconv.FormatInt(max(x, y), 10)
		want["h"+n] = strconv.FormatInt(abs(x)*4*2-abs(x), 10) + " fs"
	}
	ls = append(ls, "  constant f0 : real := fl generic map (t => real) "+
		"(1.5);")
	want["f0"] = "3.0"
	src := pkg + lines(slices.Concat([]string{"use work.p.all;",
		"package q is"}, ls, []string{"end package;"})...)
	r := analyzeOpen(t, "2019", src)
	if errs := errorLines(r); len(errs) != 0 {
		t.Fatalf("errors %q", errs)
	}
	s2Want(t, r, "q", want)
}

// TestS3AccessClass: an access-class generic type designates its
// designated generic type: .all and DEALLOCATE apply (LRM 6.5.3; NVC
// 1.23 runs the same design).
func TestS3AccessClass(t *testing.T) {
	checkExact(t, []wantCase{{name: "access", src: lines(
		"entity tb is end;",
		"architecture a of tb is",
		"  procedure take generic (type d is private; type t is access d)",
		"    (variable p : inout t; variable r : out d) is",
		"  begin r := p.all; deallocate(p); end procedure;",
		"  type ia is access integer;",
		"begin",
		"  process",
		"    variable p : ia := new integer'(9);",
		"    variable r : integer;",
		"  begin",
		"    take generic map (d => integer, t => ia) (p, r);",
		"    wait;",
		"  end process;",
		"end;",
	)}})
}

// TestS3PositionalAfterNamed: in every generic map aspect positional
// associations come first (LRM 6.5.7.1), reported once per map.
func TestS3PositionalAfterNamed(t *testing.T) {
	checkExact(t, []wantCase{
		{name: "legal", src: lines(
			"entity ent is generic (k, j : integer := 0); end;",
			"architecture a of ent is begin end;",
			"entity tb is end;",
			"architecture a of tb is",
			"  function two generic (k, j : integer) (x : integer) "+
				"return integer is",
			"  begin return x + k + j; end;",
			"  function i1 is new two generic map (1, j => 3);",
			"  constant c : integer := two generic map (1, 2) (x => 1);",
			"begin",
			"  u : entity work.ent generic map (1, j => 2);",
			"end;",
		)},
		{name: "illegal", src: lines(
			"entity ent is generic (k, j : integer := 0); end;",
			"architecture a of ent is begin end;",
			"entity tb is end;",
			"architecture a of tb is",
			"  function two generic (k, j : integer) (x : integer) "+
				"return integer is",
			"  begin return x + k + j; end;",
			"  function i1 is new two generic map (k => 1, 3);",
			"  constant c : integer := two generic map (k => 1, 3, 4) "+
				"(x => 1);",
			"begin",
			"  u : entity work.ent generic map (k => 1, 2);",
			"end;",
		), want: []string{
			"7: a positional actual cannot follow a named one",
			"8: a positional actual cannot follow a named one",
			"10: a positional actual cannot follow a named one",
		}},
	})
}

// TestS3UnconstrainedInstanceObject: an instance whose generic type
// actual is unconstrained cannot make a body variable without an
// initial value unconstrained (NVC 1.23: "declaration of variable TMP
// cannot have unconstrained type STRING").
func TestS3UnconstrainedInstanceObject(t *testing.T) {
	body := []string{
		"entity tb is end;",
		"architecture a of tb is",
		"  procedure swp generic (type t)",
		"    parameter (variable a, b : inout t) is",
		"    variable tmp : t;",
		"  begin tmp := a; a := b; b := tmp; end procedure;",
		"  function cp generic (type t) parameter (a : t) return t is",
		"    variable v : t := a;",
		"  begin return v; end function;",
	}
	src := func(ls ...string) string {
		return lines(slices.Concat(body, ls)...)
	}
	checkExact(t, []wantCase{
		{name: "legal", src: src(
			"  procedure iswp is new swp generic map (t => integer);",
			"  procedure s3 is new swp generic map (t => string(1 to 3));",
			"  function scp is new cp generic map (t => string);",
			"begin",
			"end;",
		)},
		{name: "illegal", src: src(
			"  procedure sswp is new swp generic map (t => string);",
			"begin",
			"  process",
			"    variable s1, s2 : string(1 to 3) := \"abc\";",
			"  begin",
			"    swp generic map (t => string) (s1, s2);",
			"    wait;",
			"  end process;",
			"end;",
		), want: []string{
			"10: in this instance of swp, variable tmp is of the " +
				"unconstrained type STRING (LRM 6.4.2.3)",
			"15: in this instance of swp, variable tmp is of the " +
				"unconstrained type STRING (LRM 6.4.2.3)",
		}},
	})
}

// TestS3PackageInstanceGeneric: reading the generic of a package
// instance is reported as an unsupported construct (#18: please
// report), never as a design that is not static, also through a
// constant of the package and a call of one of its subprograms.
func TestS3PackageInstanceGeneric(t *testing.T) {
	r := analyzeOpen(t, "2019", lines(
		"package gp is",
		"  generic (k : integer);",
		"  constant c : integer := k * 2;",
		"  function f (x : integer) return integer;",
		"end package;",
		"package body gp is",
		"  function f (x : integer) return integer is",
		"  begin return x + k; end;",
		"end package body;",
		"package gp3 is new work.gp generic map (k => 3);",
		"use work.gp3.all;",
		"package q is",
		"  constant ck : integer := work.gp3.k;",
		"  constant cc : integer := c;",
		"  constant cf : integer := f(1);",
		"end package;",
	))
	if errs := errorLines(r); len(errs) != 0 {
		t.Fatalf("errors %q", errs)
	}
	ev := NewEvaluator(r, 0)
	l, _ := r.Library("work")
	u, _ := l.Unit("q")
	for _, name := range []Key{"ck", "cc", "cf"} {
		_, err := ev.Constant(u.Region.Lookup(name)[0])
		var ue *UnsupportedError
		if !errors.As(err, &ue) || ue.What != cPackageInstanceGeneric {
			t.Errorf("%s: %v, want unsupported %q", name, err,
				cPackageInstanceGeneric)
		}
	}
}
