package elab

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sim"
)

// reflSrc reflects a value and a subtype of every type class and calls
// every method of the twenty std.reflection protected types. The
// expected messages are NVC 1.23's where NVC gets them right.
const reflSrc = `use std.reflection.all;
use std.textio.all;
entity refl is end entity;
architecture a of refl is
  type color is (red, green, blue);
  subtype small is integer range -3 to 10;
  type dist is range 0 to 1000 units mm; cm = 10 mm; m = 100 cm; end units;
  type rec is record a : integer; b : bit; c : real; end record;
  type mat is array (1 to 2, color range red to green) of small;
  type iptr is access integer;
  type ifile is file of integer;
  type pt is protected
    procedure inc;
  end protected;
  type pt is protected body
    variable n : integer := 0;
    procedure inc is begin n := n + 1; end procedure;
  end protected body;
  shared variable cnt : pt;
  file f : ifile;
begin
  process
    variable i : small := 7;
    variable c : color := green;
    variable r : real := 2.5;
    variable d : dist := 25 cm;
    variable t : time := 3 ns;
    variable rc : rec := (7, '1', 1.5);
    variable bv : bit_vector(3 downto 0) := "1010";
    variable s : string(1 to 3) := "abc";
    variable mm : mat := ((1, 2), (3, 4));
    variable p : iptr;
    variable np : iptr;
    variable vm : value_mirror;
    variable ivm : integer_value_mirror;
    variable ism : integer_subtype_mirror;
    variable esm : enumeration_subtype_mirror;
    variable psm : physical_subtype_mirror;
    variable asm : array_subtype_mirror;
    variable rsm : record_subtype_mirror;
    variable fsm : floating_subtype_mirror;
  begin
    p := new integer;
    p.all := 42;
    vm := i'reflect;
    report "i class=" & value_class'image(vm.get_value_class);
    ivm := vm.to_integer;
    report "i value=" & integer'image(ivm.value) & " image=" & ivm.image;
    ism := ivm.get_subtype_mirror;
    report "i sub name=" & ism.simple_name & " left=" & ism.left.image &
      " right=" & ism.right.image & " low=" & ism.low.image & " high=" &
      ism.high.image & " len=" & index'image(ism.length) & " asc=" &
      boolean'image(ism.ascending);
    report "i sm name=" & vm.get_subtype_mirror.simple_name & " cls=" &
      type_class'image(vm.get_subtype_mirror.get_type_class);
    report "integer name=" & integer'reflect.simple_name & " natural=" &
      natural'reflect.simple_name;
    report "ident=" & boolean'image(ivm.to_value_mirror = vm);
    esm := c'reflect.to_enumeration.get_subtype_mirror;
    report "c pos=" & integer'image(c'reflect.to_enumeration.pos) &
      " image=" & c'reflect.to_enumeration.image & " name=" &
      esm.simple_name & " len=" & index'image(esm.length) & " lit1=" &
      esm.enumeration_literal(1).image & " litb=" &
      integer'image(esm.enumeration_literal("blue").pos) & " left=" &
      esm.left.image & " high=" & esm.high.image & " asc=" &
      boolean'image(esm.ascending);
    report "char=" &
      character'reflect.to_enumeration.enumeration_literal(65).image &
      " bit=" &
      bit'reflect.to_enumeration.enumeration_literal("'1'").image;
    fsm := r'reflect.to_floating.get_subtype_mirror;
    report "r value=" & real'image(r'reflect.to_floating.value) &
      " image=" & r'reflect.to_floating.image & " name=" &
      fsm.simple_name & " asc=" & boolean'image(fsm.ascending);
    psm := d'reflect.to_physical.get_subtype_mirror;
    report "d value=" & integer'image(d'reflect.to_physical.value) &
      " ui=" & index'image(d'reflect.to_physical.unit_index) &
      " image=" & d'reflect.to_physical.image & " name=" &
      psm.simple_name;
    report "d units=" & index'image(psm.units_length) & " u0=" &
      psm.unit_name(1) & " u2=" & psm.unit_name(3) & " ix=" &
      index'image(psm.unit_index("M")) & " s2=" &
      integer'image(psm.scale(3)) & " scm=" &
      integer'image(psm.scale("cm")) & " len=" &
      index'image(psm.length) & " asc=" & boolean'image(psm.ascending);
    report "t value=" & integer'image(t'reflect.to_physical.value) &
      " image=" & t'reflect.to_physical.image & " name=" &
      t'reflect.get_subtype_mirror.simple_name & " units=" &
      index'image(time'reflect.to_physical.units_length) & " u7=" &
      time'reflect.to_physical.unit_name(6);
    rsm := rc'reflect.to_record.get_subtype_mirror;
    report "rc len=" & index'image(rsm.length) & " e1=" &
      rsm.element_name(1) & " ib=" & index'image(rsm.element_index("C")) &
      " es=" & rsm.element_subtype(0).simple_name & " es2=" &
      rsm.element_subtype("b").simple_name & " name=" & rsm.simple_name;
    report "rc g0=" & rc'reflect.to_record.get(0).to_integer.image &
      " gb=" & rc'reflect.to_record.get("b").to_enumeration.image &
      " g2=" & rc'reflect.to_record.get(2).to_floating.image;
    asm := bv'reflect.to_array.get_subtype_mirror;
    report "bv dims=" & index'image(asm.dimensions) & " left=" &
      index'image(asm.left) & " right=" & index'image(asm.right) &
      " low=" & index'image(asm.low) & " high=" & index'image(asm.high) &
      " len=" & index'image(asm.length) & " asc=" &
      boolean'image(asm.ascending) & " name=" & asm.simple_name &
      " el=" & asm.element_subtype.simple_name & " ix=" &
      asm.index_subtype.simple_name;
    report "s g3=" & s'reflect.to_array.get(3).to_enumeration.image;
    report "s name=" & s'reflect.get_subtype_mirror.simple_name & " g1=" &
      s'reflect.to_array.get(1).to_enumeration.image;
    asm := mm'reflect.to_array.get_subtype_mirror;
    report "mm dims=" & index'image(asm.dimensions) & " l2=" &
      index'image(asm.left(2)) & " r2=" & index'image(asm.right(2)) &
      " len2=" & index'image(asm.length(2)) & " name=" & asm.simple_name &
      " ix2=" & asm.index_subtype(2).simple_name & " el=" &
      asm.element_subtype.simple_name;
    report "mm g=" & mm'reflect.to_array.get(2, 1).to_integer.image;
    report "mm gv=" & mm'reflect.to_array.get((2, 0)).to_integer.image;
    report "p null=" & boolean'image(p'reflect.to_access.is_null) &
      " np=" & boolean'image(np'reflect.to_access.is_null) & " name=" &
      p'reflect.get_subtype_mirror.simple_name & " des=" &
      p'reflect.to_access.get_subtype_mirror.designated_subtype.simple_name;
    report "f name=" & f'reflect.get_subtype_mirror.simple_name & " des=" &
      f'reflect.to_file.get_subtype_mirror.designated_subtype.simple_name;
    file_open(f, "data.bin", write_mode);
    report "f lname=" & f'reflect.to_file.get_file_logical_name &
      " kind=" &
      file_open_kind'image(f'reflect.to_file.get_file_open_kind);
    file_close(f);
    report "cnt class=" & value_class'image(cnt'reflect.get_value_class) &
      " name=" &
      cnt'reflect.to_protected.get_subtype_mirror.simple_name;
    report "pt name=" & pt'reflect.simple_name & " cls=" &
      type_class'image(pt'reflect.get_type_class);
    report "small via to_int=" & small'reflect.to_integer.simple_name &
      " rec=" & rec'reflect.to_record.simple_name;
    wait;
  end process;
end architecture;
`

// TestReflect checks every type class against NVC 1.23.
func TestReflect(t *testing.T) {
	r := gSim(t, []gSource{{"refl.vhd", reflSrc}}, "refl", EnvOptions{})
	r.want(t,
		"i class=class_integer",
		"i value=7 image=7",
		"i sub name=SMALL left=-3 right=10 low=-3 high=10 len=14 asc=true",
		"i sm name=SMALL cls=class_integer",
		"integer name=INTEGER natural=NATURAL",
		"ident=true",
		"c pos=1 image=green name=COLOR len=3 lit1=green litb=2 "+
			"left=red high=blue asc=true",
		"char='A' bit='1'",
		"r value=2.5 image=2.5 name=REAL asc=true",
		"d value=250 ui=1 image=250 MM name=DIST",
		"d units=3 u0=MM u2=M ix=3 s2=1000 scm=10 len=1001 asc=true",
		"t value=3000000 image=3000000 FS name=TIME units=8 u7=SEC",
		"rc len=3 e1=B ib=2 es=INTEGER es2=BIT name=REC",
		"rc g0=7 gb='1' g2=1.5",
		"bv dims=1 left=3 right=0 low=0 high=3 len=4 asc=false "+
			"name=BIT_VECTOR el=BIT ix=NATURAL",
		"s g3='c'",
		"s name=STRING g1='a'",
		"mm dims=2 l2=0 r2=1 len2=2 name=MAT ix2=COLOR el=SMALL",
		"mm g=4",
		"mm gv=3",
		"p null=false np=true name=IPTR des=INTEGER",
		"f name=IFILE des=INTEGER",
		"f lname=data.bin kind=write_mode",
		"cnt class=class_protected name=PT",
		"pt name=PT cls=class_protected",
		"small via to_int=SMALL rec=REC")
}

// TestReflectLRM checks what NVC 1.23 gets wrong or does not do:
// elements of descending and offset arrays by index value, the
// designated value of an access mirror, the bounds of physical and
// floating subtypes, a type class mismatch.
func TestReflectLRM(t *testing.T) {
	src := `use std.reflection.all;
entity tb is end entity;
architecture a of tb is
  type dist is range 0 to 1000 units mm; cm = 10 mm; end units;
  subtype short is dist range 5 mm to 50 mm;
  type iptr is access integer;
  type arr is array (integer range <>) of integer;
  type node;
  type link is access node;
  type node is record v : integer; nxt : link; end record;
begin
  process
    variable bv : bit_vector(3 downto 0) := "1000";
    variable a : arr(10 to 12) := (1, 2, 3);
    variable p : iptr;
    variable vm : value_mirror;
    variable l : link;
    variable d : short := 7 mm;
    variable u : real := 0.25;
  begin
    report "bv " & bv'reflect.to_array.get(3).to_enumeration.image &
      bv'reflect.to_array.get(0).to_enumeration.image;
    report "a " & a'reflect.to_array.get(11).to_integer.image & " " &
      index'image(a'reflect.to_array.get_subtype_mirror.left);
    p := new integer'(42);
    vm := p'reflect;
    p.all := 43;
    report "p " & vm.to_access.get.to_integer.image & " " &
      p'reflect.to_access.get.to_integer.image;
    l := new node'(1, new node'(2, null));
    report "list " & l'reflect.to_access.get.to_record.get("NXT").
      to_access.get.to_record.get(0).to_integer.image;
    report "short " & short'reflect.to_physical.left.image & " " &
      short'reflect.to_physical.high.image & " " &
      index'image(short'reflect.to_physical.length) & " " &
      d'reflect.to_physical.get_subtype_mirror.right.image;
    report "real " & boolean'image(
      real'reflect.to_floating.low.value = real'low) & " " &
      boolean'image(real'reflect.to_floating.right.value = real'high) &
      " " &
      boolean'image(u'reflect.to_floating.get_subtype_mirror.ascending);
    report "same " & boolean'image(
      bv'reflect.get_subtype_mirror = bv'reflect.get_subtype_mirror) &
      " " & boolean'image(vm.get_subtype_mirror = vm.get_subtype_mirror);
    report "wrong " & integer'image(vm.to_integer.value);
    wait;
  end process;
end architecture;
`
	r := gSim(t, []gSource{{"tb.vhd", src}}, "tb", EnvOptions{})
	if len(r.errs) != 1 || !strings.Contains(r.errs[0],
		"the value mirror is of class CLASS_ACCESS, not CLASS_INTEGER") {
		t.Errorf("errors %q", r.errs)
	}
	r.errs = nil
	r.want(t, "bv '1''0'", "a 2 10", "p 42 43", "list 2",
		"short 5 MM 50 MM 46 50 MM", "real true true true",
		"same false true")
}

// TestReflectLazy checks that mirrors are made on use only and are
// charged to the step budget (P7): reflecting a 100000-element array
// and getting one element three times costs a few steps more than
// reading that element, not one per element.
func TestReflectLazy(t *testing.T) {
	src := `use std.reflection.all;
entity tb is end entity;
architecture a of tb is
  type big is array (0 to 99999) of integer;
begin
  process
    variable v : big := (others => 5);
    variable vm : array_value_mirror;
    variable n : integer := 0;
  begin
    vm := v'reflect.to_array;
    for k in 1 to 3 loop
      n := n + vm.get(77).to_integer.value;
    end loop;
    report integer'image(n);
    wait;
  end process;
end architecture;
`
	plain := strings.NewReplacer("vm := v'reflect.to_array;", "",
		"vm.get(77).to_integer.value", "v(77)").Replace(src)
	used := func(src string) int64 {
		b := eval.NewBudget(0)
		d := gDesign(t, []gSource{{"tb.vhd", src}}, "tb", EnvOptions{}, b)
		k, err := sim.New(d.Model, sim.Options{Budget: b})
		if err != nil {
			t.Fatal(err)
		}
		k.RunAll(64, nil)
		if ds := k.Diagnostics(); len(ds) != 1 ||
			ds[0].Message != "report note: 15" {
			t.Fatalf("%v", ds)
		}
		return b.Used()
	}
	m, p := used(src), used(plain)
	if m <= p || m-p > 100 {
		t.Errorf("mirrors cost %d steps (%d with, %d without)", m-p, m, p)
	}
}

// TestReflectClone forks a run that makes mirrors and reads the
// clock, the call path and the assert counters as it goes: both
// copies end alike, whatever the cut (Kernel.Clone, P1).
func TestReflectClone(t *testing.T) {
	src := `use std.reflection.all;
use std.env.all;
entity tb is end entity;
architecture a of tb is
  type rec is record a : integer; b : bit_vector(1 to 2); end record;
begin
  process
    variable acc : integer := 0;
    variable cp : call_path_vector_ptr;
    variable r : rec := (0, "01");
    variable vm : value_mirror;
    variable rm : record_value_mirror;
  begin
    for k in 1 to 20 loop
      r.a := k;
      vm := r'reflect;
      rm := vm.to_record;
      acc := acc + rm.get(0).to_integer.value +
        integer(rm.get("b").to_array.get_subtype_mirror.length);
      assert k mod 7 /= 0 report "seven" severity warning;
      wait for 1 ns;
    end loop;
    cp := get_call_path;
    report integer'image(acc) & " " & to_string(gmtime, 6) & " " &
      integer'image(GetVhdlAssertCount) & " " & to_string(cp);
    wait;
  end process;
end architecture;
`
	want := "report note: 250 2000-01-01T00:00:00.000000 2 tb.vhd:23:_P0"
	for cut := 1; cut < 120; cut += 17 {
		d := gDesign(t, []gSource{{"tb.vhd", src}}, "tb", EnvOptions{},
			nil)
		k, err := sim.New(d.Model, sim.Options{})
		if err != nil {
			t.Fatal(err)
		}
		k.Run(cut)
		c := k.Clone()
		k.RunAll(64, nil)
		c.RunAll(64, nil)
		for _, x := range []*sim.Kernel{k, c} {
			ds := x.Diagnostics()
			if len(ds) != 3 || ds[2].Message != want {
				t.Errorf("cut %d: %v", cut, ds)
			}
		}
	}
}
