package sema

// d_static_test.go: the standard-aware evaluator (64-bit INTEGER under
// VHDL-2019 only), static conditional return, inferred constraints in
// the static interpreter and composite and object 'IMAGE (#18).

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/token"
)

// dAnalyze analyzes the package source pkg (package p) under std and
// fails on errors other than the v1.0 unsupported/construct ones (the
// v1.1 features are still flagged there; their values are evaluated
// regardless).
func dAnalyze(t *testing.T, std, pkg string) *Result {
	t.Helper()
	return dAnalyzeAllow(t, std, pkg, "")
}

// dAnalyzeAllow is dAnalyze that also accepts errors of code allow.
func dAnalyzeAllow(t *testing.T, std, pkg string, allow diag.Code,
) *Result {
	t.Helper()
	r := analyze(t, map[string]string{"p.vhd": pkg},
		func(c *config.Config) { c.Standard = std })
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error &&
			d.Code != diag.UnsupportedConstruct && d.Code != allow {
			t.Fatalf("%s: %d:%d: %s: %s", std, d.Line, d.Col, d.Code,
				d.Message)
		}
	}
	return r
}

// dConst evaluates the constant name of package p, checked against its
// subtype.
func dConst(t *testing.T, ev *Evaluator, r *Result, name string,
) (eval.Value, error) {
	t.Helper()
	l, _ := r.Library("work")
	u, ok := l.Unit("p")
	if !ok {
		t.Fatal("no package p")
	}
	ds := u.Region.Lookup(Key(name))
	if len(ds) == 0 {
		t.Fatalf("no constant %s", name)
	}
	return ev.Constant(ds[0])
}

// dWant evaluates the constants of want (name -> image) and compares
// their images (eval.ImageOf; a STRING compares as its text).
func dWant(t *testing.T, std string, r *Result, want map[string]string) {
	t.Helper()
	ev := NewEvaluator(r, 0)
	for _, name := range slices.Sorted(maps.Keys(want)) {
		v, err := dConst(t, ev, r, name)
		if err != nil {
			t.Errorf("%s %s: %v", std, name, err)
			continue
		}
		got := eval.ImageOf(v)
		if v.Kind() == eval.KindArray && v.T.BaseType() == eval.String {
			got = dText(v)
		}
		if got != want[name] {
			t.Errorf("%s %s = %s, want %s", std, name, got, want[name])
		}
	}
}

// dRng is a splitmix64 sequence: test values that are the same on
// every run (P1) without a random source.
type dRng uint64

func (r *dRng) int64() int64 {
	*r += 0x9e3779b97f4a7c15
	z := uint64(*r)
	z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
	z = (z ^ z>>27) * 0x94d049bb133111eb
	return int64(z ^ z>>31)
}

func (r *dRng) intn(n int) int { return int(uint64(r.int64()) % uint64(n)) }

// dText is the text of a STRING value.
func dText(v eval.Value) string {
	var b strings.Builder
	for _, e := range v.E {
		l := eval.Character.Literals[e.I]
		if len(l) == 3 && l[0] == '\'' {
			l = l[1:2]
		}
		b.WriteString(l)
	}
	return b.String()
}

const dIntPkg = `
package p is
  constant hi : integer := integer'high;
  constant lo : integer := integer'low;
  constant nh : natural := natural'high;
  constant pl : positive := positive'low;
  constant ph : positive := positive'high;
  type iv is array (natural range <>) of integer;
  function twice (x : integer) return integer;
end package;
package body p is
  function twice (x : integer) return integer is
    variable v : integer := x;
  begin
    v := v + x;
    return v;
  end function;
end package body;
`

// TestDStdIntegers: INTEGER, NATURAL and POSITIVE are 64 bits wide
// under VHDL-2019 and 32 bits wide before, in constants, in subtypes
// and in interpreted function bodies; everything else derived from
// INTEGER follows, while STRING and BIT_VECTOR keep their identities.
func TestDStdIntegers(t *testing.T) {
	decl := "  function twice (x : integer) return integer;\n"
	cut := strings.Index(dIntPkg, decl) + len(decl)
	big := dIntPkg[:cut] +
		`  constant b40 : integer := 2 ** 40;
  subtype wide is integer range 0 to 2 ** 40;
  constant w : wide := 2 ** 40;
  constant v : iv := (2 ** 40, -1);
  constant t40 : integer := twice(2 ** 39);
` + dIntPkg[cut:]
	for _, c := range []struct {
		std  string
		want map[string]string
	}{
		{"1993", map[string]string{"hi": "2147483647",
			"lo": "-2147483648", "nh": "2147483647", "pl": "1",
			"ph": "2147483647"}},
		{"2008", map[string]string{"hi": "2147483647",
			"lo": "-2147483648", "nh": "2147483647", "pl": "1",
			"ph": "2147483647"}},
		{"2019", map[string]string{"hi": "9223372036854775807",
			"lo": "-9223372036854775808", "nh": "9223372036854775807",
			"pl": "1", "ph": "9223372036854775807",
			"b40": "1099511627776", "w": "1099511627776",
			"v": "(1099511627776,-1)", "t40": "1099511627776"}},
	} {
		src := dIntPkg
		if c.std == "2019" {
			src = big
		}
		r := dAnalyze(t, c.std, src)
		dWant(t, c.std, r, c.want)
		ev := NewEvaluator(r, 0)
		std, _ := token.ParseStandard(c.std)
		if ev.Standard() != std {
			t.Errorf("%s: Standard() = %v", c.std, ev.Standard())
		}
		it := r.Bundle.libs["std"]
		u, _ := it.Unit("standard")
		layout := func(name Key) *eval.Type {
			t.Helper()
			lt, err := ev.Layout(u.Region.Lookup(name)[0].Type)
			if err != nil {
				t.Fatal(err)
			}
			return lt
		}
		i, n, p := eval.StdIntegers(std)
		if layout("integer") != i || layout("natural") != n ||
			layout("positive") != p {
			t.Errorf("%s: INTEGER layouts are not eval.StdIntegers", c.std)
		}
		if layout("string") != eval.String ||
			layout("bit_vector") != eval.BitVector {
			t.Errorf("%s: STRING or BIT_VECTOR changed identity", c.std)
		}
	}
	// Before 2019 the same values are out of INTEGER's range.
	r := dAnalyze(t, "2008", dIntPkg)
	ev := NewEvaluator(r, 0)
	l, _ := r.Library("work")
	u, _ := l.Unit("p")
	tw := u.Region.Lookup("twice")[0]
	_, err := ev.Call(tw, []eval.Value{eval.Int(eval.Integer,
		math.MaxInt32)}, eval.NewBudget(0))
	if eval.CodeOf(err) != eval.ErrCodeRange {
		t.Errorf("2008 twice(integer'high): %v, want a range error", err)
	}
	ev19 := NewEvaluator(dAnalyze(t, "2019", dIntPkg), 0)
	l, _ = ev19.libs("work")
	u, _ = l.Unit("p")
	tw = u.Region.Lookup("twice")[0]
	_, err = ev19.Call(tw, []eval.Value{eval.Int(eval.Integer64,
		math.MaxInt64)}, eval.NewBudget(0))
	if eval.CodeOf(err) != eval.ErrCodeOverflow {
		t.Errorf("2019 twice(integer'high): %v, want an overflow", err)
	}
}

// TestDStdIntegersProperty: an INTEGER constant of any 64-bit value is
// that value under VHDL-2019; under VHDL-2008 it is that value exactly
// when it fits 32 bits and a range error otherwise.
func TestDStdIntegersProperty(t *testing.T) {
	rng := new(dRng(18))
	vals := []int64{0, 1, -1, math.MaxInt32, math.MinInt32,
		math.MaxInt32 + 1, math.MinInt32 - 1, math.MaxInt64,
		math.MinInt64 + 1}
	for range 300 {
		vals = append(vals, rng.int64()>>rng.intn(64))
		if v := vals[len(vals)-1]; rng.intn(2) == 0 && v != math.MinInt64 {
			vals[len(vals)-1] = -v
		}
	}
	var b strings.Builder
	b.WriteString("package p is\n")
	for i, v := range vals {
		if v == math.MinInt64 {
			v++
			vals[i] = v
		}
		lit := strconv.FormatInt(v, 10)
		if v < 0 {
			lit = "-" + strconv.FormatInt(-v, 10)
		}
		fmt.Fprintf(&b, "  constant c%d : integer := %s;\n", i, lit)
	}
	b.WriteString("end package;\n")
	for _, std := range []string{"2008", "2019"} {
		// Before 2019 the values past 32 bits are static errors.
		r := dAnalyzeAllow(t, std, b.String(), diag.SemaStaticError)
		ev := NewEvaluator(r, 0)
		for i, v := range vals {
			got, err := dConst(t, ev, r, fmt.Sprintf("c%d", i))
			fits := v >= math.MinInt32 && v <= math.MaxInt32
			switch {
			case std == "2019" || fits:
				if err != nil || got.I != v {
					t.Errorf("%s c%d = %v (%v), want %d", std, i, got.I,
						err, v)
				}
			case eval.CodeOf(err) != eval.ErrCodeRange:
				t.Errorf("%s c%d = %d: %v, want a range error", std, i,
					v, err)
			}
		}
	}
}

// TestDDerivedKeepStandard: every evaluator derived from one keeps its
// standard (Fresh, Bind, BindType, WithMemo, WithDynamic, frames).
func TestDDerivedKeepStandard(t *testing.T) {
	for _, std := range []token.Standard{token.Std2008, token.Std2019} {
		ev := newEvaluator(nil, 0)
		ev.std = std
		for name, d := range map[string]*Evaluator{
			"Fresh": ev.Fresh(), "Bind": ev.Bind(&Decl{}, eval.Value{}),
			"BindType": ev.BindType(&Decl{}, nil), "WithMemo": ev.WithMemo(),
			"WithDynamic": ev.WithDynamic(nil), "enter": ev.enter(),
			"chain": ev.Fresh().WithMemo().Bind(&Decl{},
				eval.Value{}).enter(),
		} {
			if d.Standard() != std {
				t.Errorf("%s of a %v evaluator is %v", name, std,
					d.Standard())
			}
		}
	}
}

const dCondPkg = `
package p is
  function clip (x : integer) return integer;
  function sign (x : integer) return integer;
  function cap (x, lim : integer) return integer;
  function steps (x : integer) return integer;
end package;
package body p is
  function clip (x : integer) return integer is
  begin
    return 0 when x < 0;
    return x;
  end function;

  function sign (x : integer) return integer is
  begin
    return -1 when x < 0 else 1 when x > 0 else 0;
  end function;

  function cap (x, lim : integer) return integer is
  begin
    return lim when x > lim else unaffected;
    return x;
  end function;

  procedure halve (x : inout integer; n : out integer) is
  begin
    n := 0;
    for i in 1 to 64 loop
      return when x mod 2 /= 0;
      x := x / 2;
      n := i;
    end loop;
  end procedure;

  -- The number of times x halves evenly, times 1000, plus what is
  -- left.
  function steps (x : integer) return integer is
    variable v : integer := x;
    variable n : integer;
  begin
    halve(v, n);
    return n * 1000 + v;
  end function;
end package body;
`

// TestDCondReturn: the static interpreter runs the VHDL-2019
// conditional return: a condition, a conditional value, an unaffected
// branch (no return) and the plain procedure form, against the Go
// meaning of each function over a range of arguments.
func TestDCondReturn(t *testing.T) {
	r := dAnalyze(t, "2019", dCondPkg)
	ev := NewEvaluator(r, 0)
	l, _ := r.Library("work")
	u, _ := l.Unit("p")
	fn := func(name string) *Decl { return u.Region.Lookup(Key(name))[0] }
	call := func(name string, args ...int64) int64 {
		t.Helper()
		var vs []eval.Value
		for _, a := range args {
			vs = append(vs, eval.Int(eval.Integer64, a))
		}
		v, err := ev.Call(fn(name), vs, eval.NewBudget(0))
		if err != nil {
			t.Fatalf("%s%v: %v", name, args, err)
		}
		return v.I
	}
	for x := int64(-40); x <= 40; x++ {
		if got, want := call("clip", x), max(x, 0); got != want {
			t.Errorf("clip(%d) = %d, want %d", x, got, want)
		}
		want := int64(0)
		if x < 0 {
			want = -1
		} else if x > 0 {
			want = 1
		}
		if got := call("sign", x); got != want {
			t.Errorf("sign(%d) = %d, want %d", x, got, want)
		}
		if got, want := call("cap", x, 9), min(x, 9); got != want {
			t.Errorf("cap(%d, 9) = %d, want %d", x, got, want)
		}
		if x <= 0 {
			continue
		}
		n, v := int64(0), x
		for v%2 == 0 {
			v, n = v/2, n+1
		}
		if got := call("steps", x); got != n*1000+v {
			t.Errorf("steps(%d) = %d, want %d", x, got, n*1000+v)
		}
	}
	// 2**40 halves 40 times: the loop body returns past 32 bits too.
	if got := call("steps", 1<<40); got != 40*1000+1 {
		t.Errorf("steps(2**40) = %d", got)
	}
}

// TestDCondReturnNoValue: a function whose conditional return takes
// its unaffected branch and then ends has no value: not static, never
// a zero value.
func TestDCondReturnNoValue(t *testing.T) {
	r := dAnalyze(t, "2019", `
package p is
  function f (x : integer) return integer;
end package;
package body p is
  function f (x : integer) return integer is
  begin
    return 1 when x > 0 else unaffected;
  end function;
end package body;
`)
	ev := NewEvaluator(r, 0)
	l, _ := r.Library("work")
	u, _ := l.Unit("p")
	f := u.Region.Lookup("f")[0]
	if v, err := ev.Call(f, []eval.Value{eval.Int(eval.Integer64, 3)},
		eval.NewBudget(0)); err != nil || v.I != 1 {
		t.Errorf("f(3) = %v, %v", v, err)
	}
	_, err := ev.Call(f, []eval.Value{eval.Int(eval.Integer64, -3)},
		eval.NewBudget(0))
	if eval.CodeOf(err) != eval.ErrCodeNotStatic {
		t.Errorf("f(-3): %v, want not static", err)
	}
}

const dInferPkg = `
package p is
  type rec is record
    f : bit_vector;
    n : integer;
  end record;
  type arr is array (natural range <>) of bit_vector;
  function lens return integer;
  function bounds return integer;
  function nested return integer;
  function inrec return integer;
  function bad_outer return integer;
  function bad_elem return integer;
  function bad_field return integer;
  function bad_whole return integer;
  function dyn (x : bit_vector) return integer;
  constant c_lens : integer := lens;
  constant c_bounds : integer := bounds;
  constant c_nested : integer := nested;
  constant c_inrec : integer := inrec;
end package;
package body p is
  function lens return integer is
    variable v : bit_vector := "10110";
    constant k : string := "abc";
  begin
    return v'length * 10 + k'length;
  end function;

  function bounds return integer is
    variable v : bit_vector := "10110";
  begin
    return v'left * 10 + v'right;
  end function;

  function nested return integer is
    variable a : arr := ("01", "10", "11");
  begin
    return a'length * 10 + a(1)'length;
  end function;

  function inrec return integer is
    variable x : rec := (f => "1011", n => 2);
  begin
    x.f := "0000";
    return x.f'length;
  end function;

  function bad_outer return integer is
    variable v : bit_vector := "10110";
  begin
    v := "101";
    return 0;
  end function;

  function bad_elem return integer is
    variable a : arr := ("01", "10", "11");
  begin
    a(1) := "111";
    return 0;
  end function;

  function bad_field return integer is
    variable x : rec := (f => "1011", n => 2);
  begin
    x.f := "1";
    return 0;
  end function;

  function bad_whole return integer is
    variable x : rec := (f => "1011", n => 2);
  begin
    x := (f => "1", n => 3);
    return 0;
  end function;

  -- The value's subtype is known at run time only.
  function dyn (x : bit_vector) return integer is
    variable v : bit_vector := x & "1";
  begin
    v := not v;
    return v'length * 10 + v'right;
  end function;
end package body;
`

// TestDInferred: an object of an unconstrained subtype declared in a
// subprogram takes its constraint from its initial value (VHDL-2019
// inferred constraints), also for array elements and record fields.
func TestDInferred(t *testing.T) {
	r := dAnalyze(t, "2019", dInferPkg)
	dWant(t, "2019", r, map[string]string{"c_lens": "53",
		"c_bounds": "4", "c_nested": "32", "c_inrec": "4"})
	// The inferred subtype is the object's: a value of another length
	// does not fit it, at any level.
	ev := NewEvaluator(r, 0)
	l, _ := r.Library("work")
	u, _ := l.Unit("p")
	for _, name := range []Key{"bad_outer", "bad_elem", "bad_field",
		"bad_whole"} {
		_, err := ev.Call(u.Region.Lookup(name)[0], nil, eval.NewBudget(0))
		if eval.CodeOf(err) != eval.ErrCodeLength {
			t.Errorf("%s: %v, want a length error", name, err)
		}
	}
	bv := eval.Value{T: eval.BitVector, B: []eval.Range{{Left: 0,
		Right: 2}}, E: []eval.Value{eval.Int(eval.Bit, 1),
		eval.Int(eval.Bit, 0), eval.Int(eval.Bit, 1)}}
	v, err := ev.Call(u.Region.Lookup("dyn")[0], []eval.Value{bv},
		eval.NewBudget(0))
	if err != nil || v.I != 43 {
		t.Errorf("dyn: %v, %v, want 43", v, err)
	}
}

const dImagePkg = `
package p is
  type rec_t is record
    n : integer;
    b : bit;
  end record;
  type int_vec is array (natural range <>) of integer;
  type color is (red, green, blue);
  type nest_t is record
    r : rec_t;
    v : bit_vector(1 to 2);
    c : color;
  end record;
  function objects return string;
  constant c_rec : string := rec_t'image((7, '1'));
  constant c_bv : string := bit_vector'image("1010");
  constant c_iv : string := int_vec'image((1, 2, -99, 0));
  constant c_nest : string := nest_t'image(((-3, '0'), "01", blue));
  constant c_str : string := string'image("ab");
  constant c_obj : string := objects;
end package;
package body p is
  function objects return string is
    variable r : rec_t := (7, '1');
    variable bv : bit_vector(3 downto 0) := "1010";
    variable i : integer := 42;
    variable c : color := green;
    variable x : real := 0.5;
  begin
    return r'image & " " & bv'image & " " & i'image & " " &
      c'image & " " & x'image;
  end function;
end package body;
`

// TestDImage: 'IMAGE of composite types and of objects (VHDL-2019),
// in NVC's format; O'IMAGE of a scalar object is its value's image.
func TestDImage(t *testing.T) {
	r := dAnalyze(t, "2019", dImagePkg)
	dWant(t, "2019", r, map[string]string{
		"c_rec": `(7,'1')`, "c_bv": `"1010"`, "c_iv": "(1,2,-99,0)",
		"c_nest": `((-3,'0'),"01",blue)`, "c_str": "(a,b)",
		"c_obj": `(7,'1') "1010" 42 green ` +
			eval.Value{T: eval.Real, F: 0.5}.Image(),
	})
}

// TestDObjectImageProperty: O'IMAGE of an INTEGER object is
// INTEGER'IMAGE of its value for any 64-bit value (VHDL-2019).
func TestDObjectImageProperty(t *testing.T) {
	r := dAnalyze(t, "2019", `
package p is
  function f (x : integer) return boolean;
  function g (x : integer) return string;
end package;
package body p is
  function f (x : integer) return boolean is
    variable v : integer := x;
  begin
    return v'image = integer'image(x) and x'image = v'image;
  end function;
  function g (x : integer) return string is
  begin
    return x'image;
  end function;
end package body;
`)
	ev := NewEvaluator(r, 0)
	l, _ := r.Library("work")
	u, _ := l.Unit("p")
	f, g := u.Region.Lookup("f")[0], u.Region.Lookup("g")[0]
	rng := new(dRng(1))
	for range 500 {
		x := rng.int64() >> rng.intn(64)
		if rng.intn(2) == 0 {
			x = -x
		}
		arg := []eval.Value{eval.Int(eval.Integer64, x)}
		v, err := ev.Call(f, arg, eval.NewBudget(0))
		if err != nil || v.I != 1 {
			t.Fatalf("f(%d) = %v, %v", x, v, err)
		}
		s, err := ev.Call(g, arg, eval.NewBudget(0))
		if err != nil || dText(s) != strconv.FormatInt(x, 10) {
			t.Fatalf("g(%d) = %v, %v", x, s, err)
		}
	}
}
