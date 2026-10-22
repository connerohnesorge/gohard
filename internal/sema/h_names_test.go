package sema

import (
	"strconv"
	"strings"
	"testing"
)

// hDiags runs hAnalyze and returns its diagnostics as diagText lines.
func hDiags(t *testing.T, std, src string) (*Result, string) {
	t.Helper()
	r := hAnalyze(t, std, map[string]string{"h.vhd": src})
	return r, strings.TrimSpace(diagText(r))
}

func hWant(t *testing.T, name, got string, want ...string) {
	t.Helper()
	if w := strings.Join(want, "\n"); got != w {
		t.Errorf("%s: got\n%s\nwant\n%s", name, got, w)
	}
}

const hReflectDecls = `entity top is end;
architecture a of top is
  type ptr is access integer;
  type rec is record a : integer; end record;
  subtype bv4 is bit_vector(0 to 3);
  type pt is protected procedure x; end protected;
  type pt is protected body procedure x is begin end; end protected body;
  type ft is file of integer;
  function f return integer is begin return 1; end;
  signal s : bit;
  shared variable sv : pt;
  file fl : ft;
begin
  process
    variable p : ptr := new integer'(5);
    variable r : rec;
    variable sm : std.reflection.subtype_mirror;
    variable vm : std.reflection.value_mirror;
    constant c : integer := 3;
  begin
`

// TestHReflectTypes: under VHDL-2019 T'REFLECT is a
// std.reflection.SUBTYPE_MIRROR and O'REFLECT a VALUE_MIRROR, without
// a use clause for std.reflection; an access prefix is not dereferenced
// (p'REFLECT is the access value's mirror, p.all'REFLECT the integer's),
// and mirrors' methods resolve.
func TestHReflectTypes(t *testing.T) {
	src := hReflectDecls + `    sm := integer'reflect;
    sm := ptr'reflect;
    sm := pt'reflect;
    sm := ft'reflect;
    sm := rec'reflect;
    sm := bv4'reflect;
    vm := p'reflect;
    vm := p.all'reflect;
    vm := s'reflect;
    vm := sv'reflect;
    vm := fl'reflect;
    vm := c'reflect;
    vm := r'reflect;
    vm := f'reflect;
    report std.reflection.value_class'image(vm.get_value_class);
    report integer'image(vm.to_integer.value);
    report std.reflection.index'image(
      rec'reflect.to_record.length);
    sm := vm.get_subtype_mirror;
    wait;
  end process;
end;
`
	r, got := hDiags(t, "2019", src)
	hWant(t, "2019", got)
	es := hAttrs(r, "reflect")
	want := []struct {
		mirror string
		prefix ExprKind
	}{
		{"subtype_mirror", ExprTypeMark}, {"subtype_mirror", ExprTypeMark},
		{"subtype_mirror", ExprTypeMark}, {"subtype_mirror", ExprTypeMark},
		{"subtype_mirror", ExprTypeMark}, {"subtype_mirror", ExprTypeMark},
		{"value_mirror", ExprName}, {"value_mirror", ExprDeref},
		{"value_mirror", ExprName}, {"value_mirror", ExprName},
		{"value_mirror", ExprName}, {"value_mirror", ExprName},
		{"value_mirror", ExprName}, {"value_mirror", ExprCall},
		{"subtype_mirror", ExprTypeMark},
	}
	if len(es) != len(want) {
		t.Fatalf("%d 'reflect expressions, want %d", len(es), len(want))
	}
	for i, e := range es {
		if n := string(e.Type.Decl.Name); n != want[i].mirror {
			// Decl.Name is the lower-case key.
			t.Errorf("#%d: type %s, want %s", i, n, want[i].mirror)
		}
		if e.Type.Decl.File == nil ||
			e.Type.Decl.File.Path != "std/reflection.vhdl" {
			t.Errorf("#%d: %s is not std.reflection's", i, e.Type.Name())
		}
		if e.Prefix == nil || e.Prefix.Kind != want[i].prefix {
			t.Errorf("#%d: prefix %v, want %v", i, e.Prefix, want[i].prefix)
		}
		if st, known := globallyStatic(e, 0); st || !known {
			t.Errorf("#%d: globally static %v %v", i, st, known)
		}
		if st, known := locallyStatic(e, 0, r.Standard); st || !known {
			t.Errorf("#%d: locally static %v %v", i, st, known)
		}
	}
	// p'reflect mirrors the access value itself.
	if pt := es[6].Prefix.Type.Base(); pt.Class != TypeAccess {
		t.Errorf("p'reflect prefix of class %v", pt.Class)
	}
}

// TestHReflectErrors: the exact diagnostics of illegal 'REFLECT uses.
func TestHReflectErrors(t *testing.T) {
	src := hReflectDecls + `    sm := string'reflect;
    vm := top'reflect;
    sm := c'reflect;
    vm := integer'reflect;
    r.a := c'reflect;
    vm := c'reflect(1);
    wait;
  end process;
end;
`
	_, got := hDiags(t, "2019", src)
	hWant(t, "2019", got,
		"h.vhd:21:11: sema/error: the prefix of 'reflect must be a "+
			"fully constrained subtype, not string",
		"h.vhd:22:11: sema/error: the prefix of 'reflect must be a "+
			"type or an object, not top",
		"h.vhd:23:11: sema/type-mismatch: expected SUBTYPE_MIRROR, "+
			"found VALUE_MIRROR",
		"h.vhd:24:11: sema/type-mismatch: expected VALUE_MIRROR, "+
			"found SUBTYPE_MIRROR",
		"h.vhd:25:12: sema/type-mismatch: expected INTEGER, found "+
			"VALUE_MIRROR",
		"h.vhd:26:21: sema/error: 'reflect has no parameter")
}

// TestHReflectBefore2019: before VHDL-2019 'REFLECT is not predefined
// (a user attribute of that name) and std.reflection does not exist;
// std.env's 2019 additions are hidden too. Under 2019 all are visible.
func TestHReflectBefore2019(t *testing.T) {
	src := `use std.reflection.all;
use std.env.all;
entity top is end;
architecture a of top is
begin
  process
    variable i : integer;
  begin
    report integer'image(i'reflect);
    report vhdl_version & getenv("HOME");
    report real'image(epoch);
    wait;
  end process;
end;
`
	_, got := hDiags(t, "2008", src)
	hWant(t, "2008", got,
		"h.vhd:1:9: library/missing-unit: library std has no unit "+
			"reflection (scanned the bundled std library for VHDL-2008)",
		"h.vhd:9:28: sema/undefined: reflect is not declared",
		"h.vhd:10:12: sema/undefined: vhdl_version is not declared",
		"h.vhd:10:27: sema/undefined: getenv is not declared",
		"h.vhd:11:23: sema/undefined: epoch is not declared")
	ok := strings.Replace(src, "integer'image(i'reflect)",
		"value_class'image(i'reflect.get_value_class)", 1)
	_, got = hDiags(t, "2019", ok)
	hWant(t, "2019", got)
}

// TestHReflectPurity: O'REFLECT reads O, so a procedure that reflects a
// signal declared outside it makes a pure caller impure, like reading
// the signal (LRM 4.1).
func TestHReflectPurity(t *testing.T) {
	src := `entity top is end;
architecture a of top is
  signal s : bit;
  procedure p is
    variable vm : std.reflection.value_mirror;
  begin
    vm := s'reflect;
  end;
  function f return integer is begin p; return 1; end;
begin
end;
`
	_, got := hDiags(t, "2019", src)
	hWant(t, "2019", got,
		"h.vhd:9:38: sema/error: pure function f cannot call procedure "+
			"p, which references signal s")
}

const hImageDecls = `entity top is end;
architecture a of top is
  type rec is record a : integer; b : bit; end record;
  type arr is array (natural range <>) of rec;
  type ar2 is array (0 to 1, 0 to 1) of bit;
  type rs is record c : character; s : string(1 to 2); end record;
  type ptr is access integer;
  type rp is record p : ptr; end record;
  type ap is array (0 to 1) of ptr;
  type pt is protected procedure x; end protected;
  type pt is protected body procedure x is begin end; end protected body;
  type apt is array (0 to 1) of pt;
  type ft is file of integer;
begin
  process
    variable r : rec := (1, '1');
    variable q : rp;
    variable p : ptr := new integer'(5);
    variable v2 : ar2;
    variable x : apt;
  begin
`

// TestHCompositeImage: VHDL-2019 'IMAGE takes a composite type or
// object whose subelements have images (NVC: representable types);
// access, file and protected subelements are reported, and an access
// object is dereferenced (p'IMAGE is the integer's image).
func TestHCompositeImage(t *testing.T) {
	ok := hImageDecls + `    report rec'image(r) & r'image;
    report arr'image((0 => r));
    report ar2'image(v2) & v2'image & rs'image(('a', "xy"));
    report string'image("ab") & bit_vector'image("10") & p'image;
    report p.all'image;
    wait;
  end process;
end;
`
	r, got := hDiags(t, "2019", ok)
	hWant(t, "2019 legal", got)
	for i, e := range hAttrs(r, "image") {
		if e.Type.Unknown() || e.Type.Name() != "STRING" {
			t.Errorf("#%d: 'image of type %s", i, e.Type.Name())
		}
	}
	bad := hImageDecls + `    report rp'image(q);
    report q'image;
    report ap'image((null, null));
    report apt'image;
    report x'image;
    report ft'image;
    wait;
  end process;
end;
`
	_, got = hDiags(t, "2019", bad)
	w := "must be a scalar type or a composite type of such types, not "
	hWant(t, "2019 illegal", got,
		"h.vhd:22:12: sema/error: the prefix of 'image "+w+"rp",
		"h.vhd:23:12: sema/error: the prefix of 'image must be an "+
			"object of a scalar type or a composite type of such "+
			"types, not q",
		"h.vhd:24:12: sema/error: the prefix of 'image "+w+"ap",
		"h.vhd:25:12: sema/error: the prefix of 'image "+w+"apt",
		"h.vhd:26:12: sema/error: the prefix of 'image must be an "+
			"object of a scalar type or a composite type of such "+
			"types, not x",
		"h.vhd:27:12: sema/error: the prefix of 'image "+w+"ft")
}

// TestHCompositeImage2008: before VHDL-2019 'IMAGE still takes a
// scalar type only, with the v1.0 diagnostic.
func TestHCompositeImage2008(t *testing.T) {
	src := `entity top is end;
architecture a of top is
  type rec is record a : integer; end record;
begin
  process
    variable r : rec;
  begin
    report rec'image(r);
    report string'image("ab");
    wait;
  end process;
end;
`
	_, got := hDiags(t, "2008", src)
	hWant(t, "2008", got,
		"h.vhd:8:12: sema/error: the prefix of 'image must be a scalar "+
			"type, not rec",
		"h.vhd:9:12: sema/error: the prefix of 'image must be a scalar "+
			"type, not string")
}

// TestHImageStatic: object'IMAGE is as static as its object; the
// type form as its argument.
func TestHImageStatic(t *testing.T) {
	src := `entity top is end;
architecture a of top is
  constant k : integer := 4;
  signal s : bit;
begin
  process
    variable v : integer;
  begin
    report k'image & v'image & s'image & integer'image(k) &
      integer'image(v);
    wait;
  end process;
end;
`
	r, got := hDiags(t, "2019", src)
	hWant(t, "2019", got)
	es := hAttrs(r, "image")
	want := []bool{true, false, false, true, false}
	if len(es) != len(want) {
		t.Fatalf("%d 'image expressions", len(es))
	}
	for i, e := range es {
		if st, known := globallyStatic(e, 0); st != want[i] || !known {
			t.Errorf("#%d globally static: %v %v, want %v", i, st,
				known, want[i])
		}
	}
	if st, _ := locallyStatic(es[1], 0, r.Standard); st {
		t.Errorf("v'image locally static")
	}
}

// TestHPrivateVariable: a VHDL-2019 private variable is directly
// visible inside its protected type's body but is not a method: no
// selected name reaches it, from outside the type or inside its body
// (NVC alike).
func TestHPrivateVariable(t *testing.T) {
	src := `entity top is end;
architecture a of top is
  type pt is protected
    private variable n : integer := 0;
    procedure inc;
    impure function peek(o : inout pt) return integer;
  end protected;
  type pt is protected body
    procedure inc is begin n := n + 1; end;
    impure function peek(o : inout pt) return integer is
    begin return o.n; end;
  end protected body;
  shared variable c : pt;
begin
  process begin
    c.inc;
    report integer'image(c.n);
    c.n := 2;
    wait;
  end process;
end;
`
	_, got := hDiags(t, "2019", src)
	w := "pt has no method n; n is a private variable, visible only " +
		"inside pt"
	hWant(t, "2019", got,
		"h.vhd:11:20: sema/error: "+w,
		"h.vhd:17:28: sema/error: "+w,
		"h.vhd:18:7: sema/error: "+w)
}

// TestHHasImageProperty: hasImage holds for a scalar, and for a
// composite exactly when it holds for all its element subtypes;
// access, file and protected types never have one. Random type trees.
func TestHHasImageProperty(t *testing.T) {
	rng := &hRand{s: 18}
	leaves := []TypeClass{TypeEnum, TypeInteger, TypeFloating,
		TypePhysical, TypeAccess, TypeFile, TypeProtected}
	var gen func(depth int) (*Type, bool)
	gen = func(depth int) (*Type, bool) {
		if depth > 3 || rng.intn(3) == 0 {
			c := leaves[rng.intn(len(leaves))]
			return &Type{Class: c},
				c != TypeAccess && c != TypeFile && c != TypeProtected
		}
		if rng.intn(2) == 0 {
			el, ok := gen(depth + 1)
			return &Type{Class: TypeArray, Elem: el}, ok
		}
		rec := &Type{Class: TypeRecord}
		all := true
		for i := range 1 + rng.intn(3) {
			ft, ok := gen(depth + 1)
			all = all && ok
			rec.Fields = append(rec.Fields, &Decl{Kind: DeclElement,
				Name: Key("f" + strconv.Itoa(i)), Pos: i, Type: ft})
		}
		return rec, all
	}
	for i := range 2000 {
		ty, want := gen(0)
		if got := hasImage(ty, 0); got != want {
			t.Fatalf("#%d: hasImage %v, want %v", i, got, want)
		}
	}
}

// hRand is a splitmix64 generator: reproducible without math/rand,
// which detlint keeps out of the core packages.
type hRand struct{ s uint64 }

func (r *hRand) intn(n int) int {
	r.s += 0x9e3779b97f4a7c15
	z := r.s
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return int((z ^ (z >> 31)) % uint64(n))
}
