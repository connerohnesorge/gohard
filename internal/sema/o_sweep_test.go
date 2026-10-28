package sema

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/eval"
)

// o_sweep_test.go holds the static-evaluator side of the v1.1 "please
// report" sweep (#18 Done when): in a running process or during
// elaboration a not-static error is a GoHard gap, reported as
// unsupported/construct (please report); static evaluation keeps it;
// the elaboration-time forms get their own construct or design error;
// predefined attribute functions work as generic subprogram actuals;
// and the assertions of the pure functions a process calls are
// reported.

// oDyn is a Dynamic that owns nothing; with report set it is also a
// Reporter recording each report as "assertion severity: msg".
type oDyn struct {
	report  bool
	reports []string
}

func (*oDyn) Ref(*Decl) (eval.Ref, bool) { return nil, false }

func (*oDyn) Load(eval.Ref) (eval.Value, error) {
	return eval.Value{}, eval.NotStatic("ref")
}

func (*oDyn) Lower(*Evaluator, *Expr, *eval.Budget) (eval.Expr, bool,
	error,
) {
	return nil, false, nil
}

func (*oDyn) Call(eval.Func, []eval.Value, *eval.Budget) (eval.Value,
	bool, error,
) {
	return eval.Value{}, false, nil
}

// oReporter is an oDyn that reports.
type oReporter struct{ oDyn }

func (d *oReporter) Report(_ *SeqStmt, assertion bool, sev int64,
	msg string,
) error {
	d.reports = append(d.reports, fmt.Sprintf("%v %d: %s", assertion,
		sev, msg))
	return nil
}

// oNoNatives is a Natives hook that answers nothing.
func oNoNatives(*Expr, *Decl, []eval.Value, *eval.Type) (eval.Value,
	bool, error,
) {
	return eval.Value{}, false, nil
}

// oDecl returns the declaration name of a project unit of r.
func oDecl(t *testing.T, r *Result, name Key) *Decl {
	t.Helper()
	for _, u := range r.Units {
		if u.File == nil || u.File.Bundled {
			continue
		}
		for _, d := range u.Decls {
			if d.Name == name {
				return d
			}
		}
	}
	t.Fatalf("no declaration %s", name)
	return nil
}

// TestOSimErrProperties: simErr never changes what an error is (it
// unwraps to the original, with the same code), leaves static
// evaluation and every error other than a plain not-static one alone,
// and turns a plain not-static error of a running process or of
// elaboration into an UnsupportedError naming where it happened.
func TestOSimErrProperties(t *testing.T) {
	r := analyzeOpen(t, "2019", lines("package p is end package;"))
	static := NewEvaluator(r, 0)
	elab := static.WithNatives(oNoNatives)
	run := static.WithDynamic(&oDyn{})
	plain := []error{eval.NotStatic("call of f"),
		eval.NotStatic("the type t"),
		fmt.Errorf("context: %w", eval.NotStatic("x"))}
	kept := []error{nil, eval.ErrBudget,
		eval.RangeError(eval.Int(eval.UniversalInteger, 9),
			eval.UniversalInteger),
		NewUnsupported("some construct", nil),
		elabSignalRead("the signal s"), errors.New("other")}
	type evCase struct {
		ev    *Evaluator
		where string // "": static
	}
	for _, c := range []evCase{{static, ""}, {elab, " during elaboration"},
		{run, " in simulation"}} {
		for _, e := range append(slices.Clone(plain), kept...) {
			got := c.ev.simErr(e)
			if e == nil {
				if got != nil {
					t.Errorf("%q: nil became %v", c.where, got)
				}
				continue
			}
			if !errors.Is(got, e) || eval.CodeOf(got) != eval.CodeOf(e) {
				t.Errorf("%q: %v lost its identity: %v", c.where, e, got)
			}
			var ue *UnsupportedError
			changed := got != e
			if !slices.Contains(plain, e) || c.where == "" {
				if changed {
					t.Errorf("%q: %v changed to %v", c.where, e, got)
				}
				continue
			}
			if !errors.As(got, &ue) ||
				!strings.HasPrefix(ue.What, "evaluating ") ||
				!strings.HasSuffix(ue.What, c.where) {
				t.Errorf("%q: %v became %v", c.where, e, got)
			}
			if !strings.HasSuffix(got.Error(), "please report") {
				t.Errorf("%q: message %q", c.where, got.Error())
			}
			if c.ev.simErr(got) != got {
				t.Errorf("%q: simErr is not idempotent on %v", c.where,
					got)
			}
		}
	}
}

// TestOElaboration: during elaboration (natives installed, no process)
// an impure call is the construct cElabImpure, a dereference
// cElabDeref, an implicit TO_STRING goes to the natives, and a read of
// a signal or of its attribute is a design error, not a please-report
// one. Static evaluation keeps them all plain not-static.
func TestOElaboration(t *testing.T) {
	r := analyzeOpen(t, "2019", lines(
		"entity e is end entity;",
		"architecture a of e is",
		"  type pt is protected",
		"    impure function nxt return integer;",
		"  end protected;",
		"  type pt is protected body",
		"    variable c : integer := 0;",
		"    impure function nxt return integer is",
		"    begin c := c + 1; return c; end function;",
		"  end protected body;",
		"  shared variable cnt : pt;",
		"  impure function one return integer is",
		"  begin return 1; end function;",
		"  type ip is access integer;",
		"  impure function mk return ip is",
		"  begin return new integer'(3); end function;",
		"  signal clk : bit;",
		"  signal s1 : integer := one;",
		"  signal s2 : integer := cnt.nxt;",
		"  signal s3 : integer := mk.all;",
		"  signal s4 : string(1 to 2) := to_string(42);",
		"  signal s5 : bit := clk;",
		"  signal s6 : boolean := clk'event;",
		"begin",
		"end architecture;",
	))
	if errs := errorLines(r); len(errs) != 0 {
		t.Fatalf("errors %q", errs)
	}
	var implicit []string
	natives := func(_ *Expr, x *Decl, args []eval.Value, rt *eval.Type,
	) (eval.Value, bool, error) {
		if x.Implicit {
			implicit = append(implicit, string(x.Name))
			return eval.Str("42"), true, nil
		}
		return eval.Value{}, false, nil
	}
	static := NewEvaluator(r, 0)
	elab := static.WithNatives(natives)
	for _, c := range []struct {
		name Key
		what string // the construct, or the design error's message
	}{
		{"s1", cElabImpure}, {"s2", cElabImpure}, {"s3", cElabDeref},
		{"s5", "the signal clk cannot be read during elaboration"},
		{"s6", "the attribute 'EVENT of a signal cannot be read " +
			"during elaboration"},
	} {
		d := oDecl(t, r, c.name)
		_, err := elab.Value(d.Value)
		var ue *UnsupportedError
		isUE := errors.As(err, &ue)
		switch {
		case eval.CodeOf(err) != eval.ErrCodeNotStatic:
			t.Errorf("%s: %v, want not static", c.name, err)
		case strings.HasPrefix(c.what, "the "):
			if isUE || err.Error() != c.what {
				t.Errorf("%s: %v, want the design error %q", c.name,
					err, c.what)
			}
		case !isUE || ue.What != c.what:
			t.Errorf("%s: %v, want unsupported %q", c.name, err, c.what)
		}
		_, err = static.Value(d.Value)
		if errors.As(err, &ue) ||
			eval.CodeOf(err) != eval.ErrCodeNotStatic {
			t.Errorf("%s: static %v, want plain not static", c.name, err)
		}
	}
	v, err := elab.Value(oDecl(t, r, "s4").Value)
	if err != nil || v.Image() != `"42"` ||
		!slices.Equal(implicit, []string{"to_string"}) {
		t.Errorf("s4: %v %v via %q", v.Image(), err, implicit)
	}
}

// TestOUnboundGeneric: a running process reading an entity generic
// that elaboration left without a value gets cUnboundGeneric; static
// evaluation keeps the plain not-static error.
func TestOUnboundGeneric(t *testing.T) {
	r := analyzeOpen(t, "2019", lines(
		"entity e is",
		"  generic (g : integer);",
		"end entity;",
		"architecture a of e is",
		"  signal s : integer := g + 1;",
		"begin",
		"end architecture;",
	))
	ex := oDecl(t, r, "s").Value
	_, err := NewEvaluator(r, 0).WithDynamic(&oDyn{}).Value(ex)
	var ue *UnsupportedError
	if !errors.As(err, &ue) || ue.What != cUnboundGeneric {
		t.Errorf("run: %v, want unsupported %q", err, cUnboundGeneric)
	}
	_, err = NewEvaluator(r, 0).Value(ex)
	if errors.As(err, &ue) || eval.CodeOf(err) != eval.ErrCodeNotStatic {
		t.Errorf("static: %v, want plain not static", err)
	}
}

// TestOAttrActualStatic: a predefined attribute function of a type as
// a generic subprogram's actual (the analyzer's own rule against it is
// dropped here, see oAttrActual) folds statically: 'IMAGE, 'VALUE,
// 'POS, 'VAL, 'SUCC and 'PRED, with a type, a subtype or an enclosing
// generic type as the prefix, in a 2008 instance or a 2019 call.
func TestOAttrActualStatic(t *testing.T) {
	r := analyzeOpen(t, "2019", lines(
		"package p is",
		"  function show generic (type e;",
		"    function img (x : e) return string) (v : e) return string;",
		"  function conv generic (type e; type r;",
		"    function f (x : e) return r) (v : e) return r;",
		"  function wrap generic (type w) (v : w) return string;",
		"  subtype small is integer range 0 to 9;",
		"  type color is (red, green, blue);",
		"  function si is new show generic map (e => integer,",
		"    img => integer'image);",
		"  function sm is new show generic map (e => small,",
		"    img => small'image);",
		"  function pos is new conv generic map (e => character,",
		"    r => integer, f => character'pos);",
		"  function val is new conv generic map (e => integer,",
		"    r => color, f => color'val);",
		"  function vl is new conv generic map (e => string,",
		"    r => integer, f => integer'value);",
		"  function nx is new conv generic map (e => color,",
		"    r => color, f => color'succ);",
		"  function pv is new conv generic map (e => small,",
		"    r => small, f => small'pred);",
		"  function wc is new wrap generic map (w => color);",
		"  constant c1 : string := si(42) & sm(3) & wc(blue);",
		"  constant c2 : integer := pos('A') + vl(\" 12 \");",
		"  constant c3 : color := nx(val(0));",
		"  constant c4 : small := pv(9);",
		"  constant c5 : string :=",
		"    show generic map (e => bit, img => bit'image) ('1');",
		"end package;",
		"package body p is",
		"  function show generic (type e;",
		"    function img (x : e) return string) (v : e) return string is",
		"  begin return \"<\" & img(v) & \">\"; end function;",
		"  function conv generic (type e; type r;",
		"    function f (x : e) return r) (v : e) return r is",
		"  begin return f(v); end function;",
		"  function wrap generic (type w) (v : w) return string is",
		"    function sw is new show generic map (e => w, img => w'image);",
		"  begin return sw(v); end function;",
		"end package body;",
	))
	for _, e := range errorLines(r) {
		if !strings.Contains(e, "is not a subprogram: a generic") {
			t.Fatalf("error %s", e)
		}
	}
	ev := NewEvaluator(r, 0)
	for name, want := range map[Key]string{
		"c1": `"<42><3><blue>"`, "c2": "77", "c3": "green", "c4": "8",
		"c5": `"<'1'>"`,
	} {
		v, err := ev.Constant(oDecl(t, r, name))
		if err != nil || v.Image() != want {
			t.Errorf("%s = %s (%v), want %s", name, v.Image(), err, want)
		}
	}
}

// TestOReporter: the assertions and reports of a pure function the
// static evaluator runs for a process go to its Reporter, in order,
// with their severity and message, once per call even when the call is
// repeated (a reporting call is not memoized); a passing assertion
// reports nothing and the value is the same with or without a
// Reporter.
func TestOReporter(t *testing.T) {
	r := analyzeOpen(t, "2019", lines(
		"package p is",
		"  procedure note (x : integer);",
		"  function f (x : integer) return integer;",
		"  constant k1 : integer := f(1);",
		"  constant k5 : integer := f(5) + f(5);",
		"end package;",
		"package body p is",
		"  procedure note (x : integer) is",
		"  begin",
		"    report \"note \" & integer'image(x) severity warning;",
		"  end procedure;",
		"  function f (x : integer) return integer is",
		"  begin",
		"    assert x < 2 report \"big \" & integer'image(x);",
		"    assert x < 3;",
		"    if x > 4 then note(x); end if;",
		"    return x + 1;",
		"  end function;",
		"end package body;",
	))
	if errs := errorLines(r); len(errs) != 0 {
		t.Fatalf("errors %q", errs)
	}
	big := []string{"true 2: big 5", "true 2: Assertion violation.",
		"false 1: note 5"}
	for name, c := range map[Key]struct {
		v    string
		want []string
	}{
		"k1": {"2", nil},
		"k5": {"12", slices.Concat(big, big)},
	} {
		d := oDecl(t, r, name)
		rep := &oReporter{}
		v, err := NewEvaluator(r, 0).WithDynamic(rep).Value(d.Value)
		if err != nil || v.Image() != c.v ||
			!slices.Equal(rep.reports, c.want) {
			t.Errorf("%s = %s (%v) reports %q, want %s %q", name,
				v.Image(), err, rep.reports, c.v, c.want)
		}
		sv, err := NewEvaluator(r, 0).Value(d.Value)
		if err != nil || sv.Image() != c.v {
			t.Errorf("%s static = %s (%v)", name, sv.Image(), err)
		}
	}
}
