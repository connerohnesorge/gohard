package sema

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/eval"
)

// TestYVariablePortAlias: an alias of a shared variable (or of an
// alias of one, or of a variable port) denotes the object itself, so
// it is a legal variable port actual, as in NVC 1.23 (LRM 6.5.7.3,
// 6.6.2). An element of a shared variable, written directly or
// through an alias, is not, with a message saying so.
func TestYVariablePortAlias(t *testing.T) {
	dut := lines(
		"use work.pk.all;",
		"entity dut is port (variable c : inout pt); end entity;",
		"architecture a of dut is begin end architecture;",
	)
	checkExact(t, []wantCase{
		{name: "aliases", src: ePkg + dut + lines(
			"use work.pk.all;",
			"entity mid is port (variable c : inout pt); end entity;",
			"architecture a of mid is",
			"  alias pc is c;",
			"begin",
			"  u : entity work.dut port map (c => pc);",
			"end architecture;",
			"use work.pk.all;",
			"entity tb is end entity;",
			"architecture a of tb is",
			"  shared variable sv : pt;",
			"  alias al is sv;",
			"  alias al2 is al;",
			"begin",
			"  u1 : entity work.dut port map (c => al);",
			"  u2 : entity work.dut port map (c => al2);",
			"  u3 : entity work.mid port map (c => al2);",
			"end architecture;",
		)},
		{name: "elements", src: ePkg + dut + lines(
			"use work.pk.all;",
			"entity tb is end entity;",
			"architecture a of tb is",
			"  shared variable arr : pt_arr;",
			"  alias e is arr(1);",
			"begin",
			"  u1 : entity work.dut port map (c => arr(0));",
			"  u2 : entity work.dut port map (c => e);",
			"end architecture;",
		), want: []string{
			"25: an element of a shared variable cannot be the actual " +
				"of variable port c",
			"26: an element of a shared variable cannot be the actual " +
				"of variable port c",
		}},
	})
}

// TestYWithNatives: a call of a function without a body is not static
// for analysis; an evaluator WithNatives hands it, with its argument
// values and result subtype, to the hook, and every evaluator derived
// from it does too. A hook answering ok false leaves it not static.
func TestYWithNatives(t *testing.T) {
	r := analyzeOpen(t, "2019", lines(
		"use std.env.all;",
		"package p is",
		"  constant name : string := TOOL_NAME;",
		"  constant var  : string := GETENV(\"K\" & \"EY\");",
		"  constant len  : natural := name'length + var'length;",
		"end package;",
	))
	if errs := errorLines(r); len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	l, _ := r.Library("work")
	u, _ := l.Unit("p")
	decl := func(n Key) *Decl { return u.Region.Lookup(n)[0] }
	if _, err := NewEvaluator(r, 0).Value(decl("len").Value); eval.CodeOf(
		err) != eval.ErrCodeNotStatic {
		t.Fatalf("without natives: %v, want not static", err)
	}
	var calls []string
	hook := func(e *Expr, x *Decl, args []eval.Value, rt *eval.Type,
	) (eval.Value, bool, error) {
		s := string(x.Name)
		for _, a := range args {
			s += "(" + a.Image() + ")"
		}
		calls = append(calls, s)
		if e == nil || e.Decl == nil || rt == nil {
			t.Errorf("call %s: no expression or result subtype", s)
		}
		switch x.Name {
		case "tool_name":
			return eval.Str("gohard"), true, nil
		case "getenv":
			return eval.Str(strings.ToLower(yString(args[0]))),
				true, nil
		}
		return eval.Value{}, false, nil
	}
	derived := []func(*Evaluator) *Evaluator{
		func(ev *Evaluator) *Evaluator { return ev },
		(*Evaluator).Fresh,
		func(ev *Evaluator) *Evaluator {
			return ev.Fresh().Bind(decl("len"), eval.Value{})
		},
		(*Evaluator).WithMemo,
	}
	for i, derive := range derived {
		// A fresh base each time: derived evaluators share the
		// constant cache of their base.
		ev := derive(NewEvaluator(r, 0).WithNatives(hook))
		calls = nil
		v, err := ev.Value(decl("len").Value)
		if err != nil || v.I != 9 {
			t.Errorf("evaluator %d: len = %v, %v; want 9", i, v.Image(),
				err)
		}
		if got := strings.Join(calls, " "); got !=
			`tool_name getenv("KEY")` {
			t.Errorf("evaluator %d: calls %s", i, got)
		}
	}
	refuse := NewEvaluator(r, 0).WithNatives(func(*Expr, *Decl,
		[]eval.Value, *eval.Type) (eval.Value, bool, error) {
		return eval.Value{}, false, nil
	})
	_, err := refuse.Value(decl("name").Value)
	if eval.CodeOf(err) != eval.ErrCodeNotStatic ||
		!strings.Contains(err.Error(), "TOOL_NAME") {
		t.Errorf("refused: %v, want call of TOOL_NAME not static", err)
	}
}

// yString is the text of a STRING value.
func yString(v eval.Value) string {
	b := make([]byte, len(v.E))
	for i, e := range v.E {
		b[i] = byte(e.I)
	}
	return string(b)
}
