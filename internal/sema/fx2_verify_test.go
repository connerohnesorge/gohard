package sema

// fx2_verify_test.go: regression tests of the second v1.1 Stage 3
// verification round in the analyzer (#18): a formal of a generic
// protected type associated twice is an error in every form of its
// generic map (it was accepted, the first actual used), the methods of
// an object take the object's generic type actuals in their profiles
// (methodsubst.go; they were checked against the uninstantiated ones),
// and a pure function without a return is a design error the static
// evaluator words as the interpreter does.

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/eval"
)

// TestFx2ProtGenericDuplicate: LRM 6.5.7.1, a generic of a protected
// type is associated at most once: in an instantiation's map, a
// variable's and a header's, as in an entity instance's.
func TestFx2ProtGenericDuplicate(t *testing.T) {
	src := lines(
		"entity e is end entity;",
		"architecture a of e is",
		"  type p is protected",
		"    generic (n : integer; m : integer := 0);",
		"  end protected;",
		"  type p is protected body end protected body;",
		"  type pi is new p generic map (n => 1, n => 2);",
		"  type pj is new p generic map (1, n => 2);",
		"  type h is protected",
		"    generic (k : integer);",
		"    generic map (k => 1, k => 2);",
		"  end protected;",
		"  type h is protected body end protected body;",
		"  shared variable s : p generic map (n => 1, m => 2, m => 3);",
		"  type ok is new p generic map (n => 1, m => 2);",
		"begin",
		"  process",
		"    variable v : p generic map (n => 1, n => 2);",
		"  begin wait; end process;",
		"end architecture;",
	)
	checkExact(t, []wantCase{{name: "dup", src: src, want: []string{
		"7: generic n already has an actual",
		"8: generic n already has an actual",
		"11: generic k already has an actual",
		"14: generic m already has an actual",
		"18: generic n already has an actual",
	}}})
}

// fx2Types are scalar and composite types with a value of each, for
// the profile property.
var fx2Types = []struct{ name, value string }{
	{"integer", "1"}, {"boolean", "true"}, {"character", "'c'"},
	{"string(1 to 2)", `"ab"`}, {"bit_vector(0 to 1)", `"01"`},
	{"real", "1.5"}, {"iv", "(1, 2)"},
}

// TestFx2MethodProfileProp is a property: for an object whose generic
// type t has the actual type A (by a variable's generic map or a
// protected type instantiation), a method's result and parameter of
// type t are of A: `x := o.get` and `o.set(v)` are legal exactly when x
// and the value v are of A, and `o.get(1)` indexes the result exactly
// when A is an array type.
func TestFx2MethodProfileProp(t *testing.T) {
	for seed := range uint64(60) {
		r := &env.SplitMix64{State: seed}
		act := fx2Types[env.Intn(r, len(fx2Types))]
		use := fx2Types[env.Intn(r, len(fx2Types))]
		inst := env.Intn(r, 2) == 0
		decl, typ := "", "box generic map (t => "+act.name+")"
		if inst {
			decl = "  type bi is new box generic map (t => " +
				act.name + ");"
			typ = "bi"
		}
		src := lines(
			"entity e is end entity;",
			"architecture a of e is",
			"  type iv is array (1 to 2) of integer;",
			"  type box is protected",
			"    generic (type t);",
			"    procedure set(x : t);",
			"    impure function get return t;",
			"  end protected;",
			"  type box is protected body",
			"    variable v : t;",
			"    procedure set(x : t) is begin v := x; end procedure;",
			"    impure function get return t is",
			"    begin return v; end function;",
			"  end protected body;",
			decl,
			"begin",
			"  process",
			"    variable o : "+typ+";",
			"    variable x : "+use.name+";",
			"  begin",
			"    x := o.get;",
			"    o.set("+use.value+");",
			"    wait;",
			"  end process;",
			"end architecture;",
		)
		name := fmt.Sprintf("%d %s/%s inst=%v", seed, act.name, use.name,
			inst)
		es := errorLines(analyzeOpen(t, "2019", src))
		var lines []int
		for _, e := range es {
			var n int
			fmt.Sscanf(e, "%d:", &n)
			lines = append(lines, n)
		}
		same := act.name == use.name
		switch {
		case same && len(es) > 0:
			t.Errorf("%s: errors %q", name, es)
		case !same && !containsLine(lines, 21):
			t.Errorf("%s: x := o.get accepted; errors %q", name, es)
		case !same && !compatibleLiteral(act.name, use.value) &&
			!containsLine(lines, 22):
			t.Errorf("%s: o.set(%s) accepted; errors %q", name,
				use.value, es)
		}
		for _, e := range es {
			if strings.Contains(e, "takes 0 arguments") ||
				strings.Contains(e, " is t,") ||
				strings.Contains(e, "returns t,") {
				t.Errorf("%s: uninstantiated profile: %q", name, e)
			}
		}
	}
}

func containsLine(ls []int, n int) bool {
	for _, l := range ls {
		if l == n {
			return true
		}
	}
	return false
}

// compatibleLiteral reports whether the literal or aggregate v of
// another type fx2Types lists is also a value of the type named a.
func compatibleLiteral(a, v string) bool {
	switch {
	case strings.HasPrefix(v, `"`):
		return strings.HasPrefix(a, "string") ||
			strings.HasPrefix(a, "bit_vector") && v == `"01"`
	case strings.HasPrefix(v, "("):
		return a == "iv"
	}
	return false
}

// TestFx2MethodIndexed: o.get(i) indexes the result of the
// parameterless method get when the object's generic type actual is an
// array type, for each binding form; the call keeps the method's
// declaration and is of the actual type.
func TestFx2MethodIndexed(t *testing.T) {
	for _, form := range []struct{ decl, typ string }{
		{"", "box generic map (t => iv)"},
		{"  type bi is new box generic map (t => iv);", "bi"},
	} {
		r := analyzeOpen(t, "2019", lines(
			"entity e is end entity;",
			"architecture a of e is",
			"  type iv is array (1 to 3) of integer;",
			"  type box is protected",
			"    generic (type t);",
			"    impure function get return t;",
			"  end protected;",
			"  type box is protected body",
			"    variable v : t;",
			"    impure function get return t is",
			"    begin return v; end function;",
			"  end protected body;",
			form.decl,
			"begin",
			"  process",
			"    variable o : "+form.typ+";",
			"    variable n : integer;",
			"  begin",
			"    n := o.get(2) + o.get(1);",
			"    wait;",
			"  end process;",
			"end architecture;",
		))
		if es := errorLines(r); len(es) > 0 {
			t.Errorf("%q: %q", form.typ, es)
		}
	}
}

// TestFx2NoReturnError: the static evaluator's error for a function
// without a return is not a GoHard gap: simErr keeps it, so the
// simulator reports the design's error.
func TestFx2NoReturnError(t *testing.T) {
	err := noReturn(&Decl{Text: "f"})
	if got := err.Error(); got !=
		"function f ended without a return statement" {
		t.Errorf("message %q", got)
	}
	if eval.CodeOf(err) != eval.ErrCodeNotStatic {
		t.Errorf("code %v: analysis must leave it to the simulator",
			eval.CodeOf(err))
	}
	r := analyzeOpen(t, "2019", lines("package p is end package;"))
	static := NewEvaluator(r, 0)
	for _, ev := range []*Evaluator{static,
		static.WithNatives(oNoNatives), static.WithDynamic(&oDyn{})} {
		var ue *UnsupportedError
		if got := ev.simErr(err); got != err || errors.As(got, &ue) {
			t.Errorf("simErr changed %v to %v", err, got)
		}
	}
}
