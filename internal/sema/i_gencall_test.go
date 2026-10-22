package sema

import (
	"strings"
	"testing"
)

// i_gencall_test.go tests VHDL-2019 generic maps on subprogram calls
// (LRM 9.3.4, #18): the implicit instance a call with a generic map
// makes (pkginst.go instance), overload resolution over the instances,
// the parameter map form, and the errors NVC 1.23 reports for the
// same sources.

// gcPkg declares the generic subprograms of the tests, at lines 1-17.
var gcPkg = lines(
	"package p is",
	"  function scale generic (k : integer) (x : integer) return integer;",
	"  function dflt generic (k : integer := 2) (x : integer)"+
		" return integer;",
	"  procedure add generic (k : integer) (x : inout integer);",
	"  function ident generic (type t) (x : t) return t;",
	"  function abs2 (x : integer) return integer;",
	"end package;",
	"package body p is",
	"  function scale generic (k : integer) (x : integer) return integer is",
	"  begin return k * x; end function;",
	"  function dflt generic (k : integer := 2) (x : integer)"+
		" return integer is",
	"  begin return k * x; end function;",
	"  procedure add generic (k : integer) (x : inout integer) is",
	"  begin x := x + k; end procedure;",
	"  function ident generic (type t) (x : t) return t is",
	"  begin return x; end function;",
	"  function abs2 (x : integer) return integer is begin return x; end;",
	"end package body;",
)

// gcUse wraps the statements body (starting at line 23) in a process
// that sees package p.
func gcUse(body ...string) string {
	return gcPkg + lines(
		"use work.p.all;",
		"entity tb is end;",
		"architecture a of tb is",
		"begin",
		"  process",
		"    variable v : integer := 1;",
		"    variable b : boolean;",
		"  begin",
	) + lines(body...) + lines("    wait;", "  end process;", "end;")
}

// gcCalls returns the subprogram calls in the process statements of
// r's units, in source order.
func gcCalls(r *Result) []*Expr {
	var out []*Expr
	var stmts func([]*Stmt)
	stmts = func(ss []*Stmt) {
		for _, st := range ss {
			walkSeq(st.Seq, func(*SeqStmt) {}, func(e *Expr) {
				if e.Kind == ExprCall && e.Decl != nil &&
					e.Decl.Kind.IsSubprogram() {
					out = append(out, e)
				}
			})
			stmts(st.Body)
		}
	}
	for _, u := range r.Units {
		stmts(u.Body)
	}
	return out
}

// gcRefAt returns the ref at the first occurrence of needle in src,
// plus skip bytes.
func gcRefAt(t *testing.T, r *Result, src, needle string, skip int,
) Ref {
	t.Helper()
	i := strings.Index(src, needle)
	if i < 0 {
		t.Fatalf("no %q", needle)
	}
	ref, ok := r.RefAt("t.vhd", i+skip)
	if !ok {
		t.Fatalf("no ref at %q+%d", needle, skip)
	}
	return ref
}

func TestIGenericCallErrors(t *testing.T) {
	checkExact(t, []wantCase{
		{name: "legal", src: gcUse(
			"    v := scale generic map (k => 3) (x => 4);",
			"    v := scale generic map (k => 3) parameter map (x => 5);",
			"    v := scale generic map (3) (6);",
			"    v := dflt generic map (k => 5) (7);",
			"    v := dflt generic map (k => open) (7);",
			"    v := ident generic map (t => integer) (8);",
			"    b := ident generic map (t => boolean) (true);",
			"    add generic map (k => 10) (v);",
			"    add generic map (k => 10) parameter map (x => v);",
			"    v := abs2 parameter map (x => 1);",
			"    v := work.p.scale generic map (k => 2) (v);",
		)},
		{name: "no generic map", src: gcUse(
			"    v := dflt(7);",
			"    v := scale(7);",
			"    add(v);",
		), want: []string{
			"27: cannot call uninstantiated function dflt without a " +
				"generic map (LRM 9.3.4)",
			"28: cannot call uninstantiated function scale without a " +
				"generic map (LRM 9.3.4)",
			"29: cannot call uninstantiated procedure add without a " +
				"generic map (LRM 9.3.4)",
		}},
		{name: "no generic map 2008", std: "2008", src: gcUse(
			"    v := dflt(7);",
			"    add(v);",
		), want: []string{
			"27: cannot call uninstantiated function dflt; instantiate " +
				"it first (LRM 4.4)",
			"28: cannot call uninstantiated procedure add; instantiate " +
				"it first (LRM 4.4)",
		}},
		{name: "not uninstantiated", src: gcUse(
			"    v := abs2 generic map (k => 3) (5);",
			"    v := v generic map (k => 1) (2);",
		), want: []string{
			"27: function abs2 is not an uninstantiated subprogram " +
				"(LRM 9.3.4)",
			"28: v is not an uninstantiated subprogram (LRM 9.3.4)",
		}},
		{name: "parameter map on an object", src: gcUse(
			"    v := v parameter map (x => 1);",
		), want: []string{
			"27: v is not a subprogram, so it takes no parameter map " +
				"(LRM 9.3.4)",
		}},
		{name: "generic map actuals", src: gcUse(
			"    v := scale generic map (j => 3) (5);",
			"    v := scale generic map (k => true) (2);",
			"    v := scale generic map (x => 1) (2);",
			"    v := scale generic map (1, 2) (2);",
		), want: []string{
			"27: j is not a generic of scale",
			"28: no true matches: TRUE [return BOOLEAN] from STANDARD " +
				"returns BOOLEAN, not INTEGER",
			"29: x is not a generic of scale",
			"30: found at least 2 positional actuals but scale has only " +
				"1 generic",
		}},
		{name: "parameters", src: gcUse(
			"    v := scale generic map (k => 3);",
			"    v := ident generic map (t => integer) (true);",
			"    b := scale generic map (k => 3) (1);",
		), want: []string{
			"27: no scale matches: scale [INTEGER return INTEGER] from p " +
				"needs a value for x",
			"28: no ident matches: ident [INTEGER return INTEGER] from p " +
				"argument 1 is BOOLEAN, not INTEGER",
			"29: no scale matches: scale [INTEGER return INTEGER] from p " +
				"returns INTEGER, not BOOLEAN",
		}},
		{name: "parameter map and actuals", src: gcUse(
			"    v := scale generic map (k => 3) parameter map (x => 5)"+
				" (6);",
			"    add generic map (k => 1) parameter map (x => v) (v);",
		), want: []string{
			"27: scale generic map (k => 3) parameter map (x => 5) (6) " +
				"is INTEGER, which cannot be indexed",
			"28: a procedure call cannot have both a parameter map " +
				"aspect and another actual parameter part (LRM 9.3.4)",
		}},
	})
}
