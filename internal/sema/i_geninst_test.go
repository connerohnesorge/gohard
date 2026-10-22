package sema

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env"
)

// i_geninst_test.go tests the implicit instance Decl of a VHDL-2019
// call with a generic map (pkginst.go instance), its editor view
// (#13), overloads of uninstantiated subprograms, recursion, the
// spec/body link of generic-type profiles (sameGenericProfile) and
// method aliases (Decl.MethodObj).

// TestIGenericCallInstance checks the shape of the implicit instance:
// target, generic associations and profile.
func TestIGenericCallInstance(t *testing.T) {
	src := gcUse(
		"    v := scale generic map (k => 3) (x => 4);",
		"    b := ident generic map (t => boolean) (true);",
		"    add generic map (10) parameter map (x => v);",
		"    v := dflt generic map (k => open) (5);",
	)
	r := analyzeOpen(t, "2019", src)
	if errs := errorLines(r); len(errs) > 0 {
		t.Fatalf("errors: %q", errs)
	}
	calls := gcCalls(r)
	if len(calls) != 4 {
		t.Fatalf("got %d calls, want 4", len(calls))
	}
	for i, name := range []Key{"scale", "ident", "add", "dflt"} {
		e := calls[i]
		d := e.Decl
		if !implicitInstance(d) || d.Name != name {
			t.Fatalf("call %d: decl %v is not an instance of %s", i,
				d.Name, name)
		}
		g := d.Target
		if g == nil || !uninstantiated(g) || !isSubprogramDecl(g.Node) {
			t.Fatalf("%s: target %v is not the declaration", name, g)
		}
		if g.Kind != d.Kind || d.Region != nil || d.Parent != g.Parent ||
			!d.Node.Equal(e.Node) || d.Unit != r.Units[3] {
			t.Errorf("%s: kind/region/parent/node/unit wrong", name)
		}
		if d.Generics == nil || len(d.Generics) != 1 {
			t.Errorf("%s: generics %v", name, d.Generics)
		}
		gens := genericDecls(g)
		for j, as := range d.Generics {
			if !slices.Contains(gens, as.Formal) {
				t.Errorf("%s: association %d formal %v is not a generic "+
					"of the target", name, j, as.Formal)
			}
		}
		if len(d.Params) != len(g.Params) {
			t.Fatalf("%s: %d params, target has %d", name, len(d.Params),
				len(g.Params))
		}
		for j, p := range d.Params {
			if p.Pos != j || p.Name != g.Params[j].Name {
				t.Errorf("%s: param %d is %s at %d", name, j, p.Name,
					p.Pos)
			}
		}
	}
	sc := calls[0].Decl
	if sc.Generics[0].Formal.Name != "k" ||
		srcText(src, sc.Generics[0].Actual) != "3" ||
		!slices.Equal(sc.Params, sc.Target.Params) {
		t.Errorf("scale: generics %+v, params copied", sc.Generics)
	}
	// ident: t => boolean replaces t in the profile, not in the target.
	id := calls[1].Decl
	boolean := id.Generics[0].Actual.Type
	if id.Type != boolean || id.Params[0].Type != boolean ||
		calls[1].Type != boolean {
		t.Errorf("ident profile: result %s, param %s, want BOOLEAN",
			id.Type.Name(), id.Params[0].Type.Name())
	}
	if id.Target.Params[0].Type == boolean || id.Target.Type == boolean {
		t.Errorf("the target's own profile was changed")
	}
	ad := calls[2].Decl
	if ad.Generics[0].Formal.Name != "k" || len(calls[2].Args) != 1 ||
		calls[2].Type.Name() == "INTEGER" {
		t.Errorf("add: generics %+v, args %d", ad.Generics,
			len(calls[2].Args))
	}
	// k => open: the association is kept, with no actual (default).
	df := calls[3].Decl
	if df.Generics[0].Formal.Name != "k" || df.Generics[0].Actual != nil {
		t.Errorf("dflt: open association %+v", df.Generics[0])
	}
}

// srcText returns the source text of e.
func srcText(src string, e *Expr) string {
	if e == nil {
		return ""
	}
	sp := e.Node.Span()
	return src[sp.Start:sp.End]
}

// TestIGenericCallRefs: the editor (#13) sees a call with a generic
// map as a call of the generic subprogram: the prefix, the generic
// formals and the named parameters refer to declared names.
func TestIGenericCallRefs(t *testing.T) {
	src := gcUse(
		"    v := scale generic map (k => 3) (x => 4);",
		"    b := ident generic map (t => boolean) (x => true);",
		"    add generic map (k => 1) parameter map (x => v);",
	)
	r := analyzeOpen(t, "2019", src)
	if errs := errorLines(r); len(errs) > 0 {
		t.Fatalf("errors: %q", errs)
	}
	calls := gcCalls(r)
	for i, name := range []string{"scale", "ident", "add"} {
		inst := calls[i].Decl
		g := inst.Target
		head := name + " generic map ("
		ref := gcRefAt(t, r, src, head, 0)
		if ref.Decl != g || ref.Kind != RefCall {
			t.Errorf("%s: prefix ref %v (%v), want the target", name,
				ref.Decl, ref.Kind)
		}
		if ref.Overload == nil || ref.Overload.Chosen != g {
			t.Fatalf("%s: the ref's overload does not choose the "+
				"target", name)
		}
		for _, cd := range ref.Overload.Candidates {
			if implicitInstance(cd.Decl) {
				t.Errorf("%s: an instance among the ref's candidates",
					name)
			}
		}
		if calls[i].Overload == nil || calls[i].Overload.Chosen != inst {
			t.Errorf("%s: the call's overload does not choose the "+
				"instance", name)
		}
		fr := gcRefAt(t, r, src, head, len(head))
		if fr.Decl == nil || fr.Decl.Parent != g.Region ||
			fr.Kind != RefFormal || !isGenericKind(fr.Decl.Kind) {
			t.Errorf("%s: generic formal ref %v", name, fr.Decl)
		}
		at := strings.Index(src, head)
		px := strings.Index(src[at:], "x =>")
		pr, ok := r.RefAt("t.vhd", at+px)
		if !ok || pr.Decl != g.Params[0] || pr.Kind != RefFormal {
			t.Errorf("%s: parameter ref %v, want the target's x", name,
				pr.Decl)
		}
	}
	refs := r.References(calls[0].Decl.Target)
	calls0 := 0
	for _, x := range refs {
		if x.Kind == RefCall {
			calls0++
		}
	}
	if calls0 != 1 {
		t.Errorf("scale: %d call references, want 1 (%v)", calls0, refs)
	}
}

// TestIGenericCallOverload: overload resolution chooses among the
// instances of overloaded uninstantiated subprograms by the parameter
// actuals and the result, and drops those whose generic map does not
// fit. NVC 1.23 rejects an overloaded uninstantiated name outright;
// LRM 9.3.4 (an implicit instance per call) with LRM 4.5 (overload
// resolution) allows it.
func TestIGenericCallOverload(t *testing.T) {
	src := lines(
		"package q is",
		"  function two generic (k : integer) (x : integer)"+
			" return integer;",
		"  function two generic (k : integer) (x : boolean)"+
			" return integer;",
		"  function one generic (k : integer) (x : integer)"+
			" return integer;",
		"  function one generic (j : integer) (x : integer)"+
			" return boolean;",
		"end;",
		"package body q is",
		"  function two generic (k : integer) (x : integer)"+
			" return integer is begin return k + x; end;",
		"  function two generic (k : integer) (x : boolean)"+
			" return integer is begin return k; end;",
		"  function one generic (k : integer) (x : integer)"+
			" return integer is begin return k; end;",
		"  function one generic (j : integer) (x : integer)"+
			" return boolean is begin return j > x; end;",
		"end;",
		"use work.q.all;",
		"entity tb is end;",
		"architecture a of tb is",
		"begin",
		"  process",
		"    variable v : integer;",
		"    variable b : boolean;",
		"  begin",
		"    v := two generic map (k => 1) (5);",
		"    v := two generic map (k => 7) (true);",
		"    v := one generic map (k => 7) (1);",
		"    b := one generic map (j => 7) (1);",
		"    v := one generic map (z => 7) (1);",
		"    wait;",
		"  end process;",
		"end;",
	)
	r := analyzeOpen(t, "2019", src)
	want := []string{"25: z is not a generic of one"}
	if got := errorLines(r); !slices.Equal(got, want) {
		t.Fatalf("errors %q, want %q", got, want)
	}
	calls := gcCalls(r)
	if len(calls) < 4 {
		t.Fatalf("%d resolved calls, want 4", len(calls))
	}
	for i, gen := range []Key{"k", "k", "k", "j"} {
		d := calls[i].Decl
		if !implicitInstance(d) || d.Generics[0].Formal == nil ||
			d.Generics[0].Formal.Name != gen {
			t.Errorf("call %d: generic %v", i, d.Generics)
		}
	}
	if calls[0].Decl.Target.Params[0].Type.Name() != "INTEGER" ||
		calls[1].Decl.Target.Params[0].Type.Name() != "BOOLEAN" {
		t.Errorf("two: wrong overloads")
	}
	if calls[2].Decl.Target == calls[3].Decl.Target {
		t.Errorf("one: the result did not decide")
	}
	// The chosen instance's map is analyzed again, with its refs.
	ref := gcRefAt(t, r, src, "(j => 7)", 1)
	if ref.Decl != calls[3].Decl.Generics[0].Formal {
		t.Errorf("j: ref %v", ref.Decl)
	}
	for i := range r.Units[2].Refs {
		x := r.Units[2].Refs[i]
		if implicitInstance(x.Decl) {
			t.Errorf("a ref records an implicit instance at %v", x.Span)
		}
	}
}

// TestIGenericRecursion: inside an uninstantiated subprogram its
// designator denotes the instance being executed, so a recursive call
// needs no generic map (NVC 1.23 accepts it and prints 120); outside,
// the same call is an error.
func TestIGenericRecursion(t *testing.T) {
	checkExact(t, []wantCase{{name: "recursive", src: lines(
		"package p is",
		"  function fact generic (k : integer) (x : integer)"+
			" return integer;",
		"end package;",
		"package body p is",
		"  function fact generic (k : integer) (x : integer)"+
			" return integer is",
		"    function inner (y : integer) return integer is",
		"    begin return fact(y); end;",
		"  begin",
		"    if x <= 1 then return k; end if;",
		"    return x * inner(x - 1);",
		"  end function;",
		"  function other (x : integer) return integer is",
		"  begin return fact(x); end;",
		"end package body;",
	), want: []string{
		"13: cannot call uninstantiated function fact without a " +
			"generic map (LRM 9.3.4)",
	}}})
}

// TestIGenericBodyLink: the body of a subprogram whose profile names
// its own generic types completes its declaration (findSpec,
// sameGenericProfile): it is linked, its conformance checked, and a
// second body is a duplicate. Profiles that differ in more than the
// identity of the generic types stay apart.
func TestIGenericBodyLink(t *testing.T) {
	src := lines(
		"package p is",
		"  function id generic (type t) (x : t) return t;",
		"  function sw generic (type a; type b) (x : a; y : b) return b;",
		"end package;",
		"package body p is",
		"  function id generic (type t) (x : t) return t is",
		"  begin return x; end function;",
		"  function sw generic (type a; type b) (x : a; y : b)"+
			" return b is",
		"  begin return y; end function;",
		"end package body;",
	)
	r := analyzeOpen(t, "2019", src)
	if errs := errorLines(r); len(errs) > 0 {
		t.Fatalf("errors: %q", errs)
	}
	linked := 0
	for _, u := range r.Units {
		for _, d := range u.Decls {
			if d.Kind == DeclFunction && d.Spec != nil {
				linked++
				if d.Spec.Name != d.Name || !uninstantiated(d.Spec) ||
					!isSubprogramDecl(d.Spec.Node) {
					t.Errorf("%s linked to %s", d.Name, d.Spec.Name)
				}
			}
		}
	}
	if linked != 2 {
		t.Errorf("%d bodies linked, want 2", linked)
	}
	checkExact(t, []wantCase{
		{name: "swapped", src: lines(
			"package p is",
			"  function sw generic (type a; type b) (x : a; y : b)"+
				" return b;",
			"end package;",
			"package body p is",
			"  function sw generic (type a; type b) (x : b; y : a)"+
				" return b is",
			"  begin return x; end function;",
			"end package body;",
		)},
		{name: "nonconforming", src: lines(
			"package p is",
			"  function id generic (type t) (x : t) return t;",
			"end package;",
			"package body p is",
			"  function id generic (type u) (x : u) return u is",
			"  begin return x; end function;",
			"end package body;",
		), want: []string{
			"5: the body of id does not conform to its declaration at " +
				"line 2",
		}},
		{name: "duplicate", src: lines(
			"package p is",
			"  function id generic (type t) (x : t) return t;",
			"end package;",
			"package body p is",
			"  function id generic (type t) (x : t) return t is",
			"  begin return x; end function;",
			"  function id generic (type t) (x : t) return t is",
			"  begin return x; end function;",
			"end package body;",
		), want: []string{
			"7: the body of id is already defined (line 5)",
		}},
	})
}

// TestIGenericProfileProperty: sameGenericProfile is an equivalence on
// generated generic profiles that agrees with the profile read by
// position: two subprograms have the same generic profile exactly
// when their parameter lists name the same generic positions or the
// same concrete types.
func TestIGenericProfileProperty(t *testing.T) {
	rng := &env.SplitMix64{State: 18<<32 | 27}
	kinds := []string{"a", "b", "integer", "boolean"}
	gen := func() []int {
		n := 1 + env.Intn(rng, 3)
		ps := make([]int, n+1) // the last one is the result
		for i := range ps {
			ps[i] = env.Intn(rng, len(kinds))
		}
		return ps
	}
	text := func(name string, gens [2]string, ps []int) string {
		var params []string
		for i, k := range ps[:len(ps)-1] {
			typ := kinds[k]
			if k < 2 {
				typ = gens[k]
			}
			params = append(params, fmt.Sprintf("x%d : %s", i, typ))
		}
		res := kinds[ps[len(ps)-1]]
		if ps[len(ps)-1] < 2 {
			res = gens[ps[len(ps)-1]]
		}
		return "  function " + name + " generic (type " + gens[0] +
			"; type " + gens[1] + ") (" + strings.Join(params, "; ") +
			") return " + res + ";"
	}
	for i := range 40 {
		pa, pb := gen(), gen()
		if i%3 == 0 {
			pb = slices.Clone(pa)
		}
		src := lines("package p is",
			text("f", [2]string{"a", "b"}, pa),
			text("g", [2]string{"c", "d"}, pb),
			"end package;")
		r := analyzeOpen(t, "2019", src)
		var f, g *Decl
		for _, d := range r.Units[0].Decls {
			switch d.Name {
			case "f":
				f = d
			case "g":
				g = d
			}
		}
		want := slices.Equal(pa, pb)
		if got := sameGenericProfile(f, g); got != want {
			t.Errorf("%v vs %v: got %v, want %v", pa, pb, got, want)
		}
		if !sameGenericProfile(f, f) || !sameGenericProfile(g, g) {
			t.Errorf("%v: not reflexive", pa)
		}
		if sameGenericProfile(f, g) != sameGenericProfile(g, f) {
			t.Errorf("%v vs %v: not symmetric", pa, pb)
		}
	}
}

// TestIGenericCallForms: every way of writing the same call (named or
// positional generics and parameters, with or without `parameter
// map`) resolves to an instance of the same subprogram with the same
// generic actual, and analysis is deterministic.
func TestIGenericCallForms(t *testing.T) {
	rng := &env.SplitMix64{State: 9<<32 | 3}
	for range 30 {
		k := env.Intn(rng, 1000)
		var body []string
		for range 4 {
			gm := fmt.Sprint(k)
			if env.Intn(rng, 2) == 0 {
				gm = "k => " + gm
			}
			pm := "v"
			if env.Intn(rng, 2) == 0 {
				pm = "x => v"
			}
			call := "scale generic map (" + gm + ") "
			if env.Intn(rng, 2) == 0 {
				call += "parameter map "
			}
			call += "(" + pm + ")"
			if env.Intn(rng, 2) == 0 {
				body = append(body, "    v := "+call+";")
			} else {
				body = append(body, "    add generic map ("+gm+") "+
					call[len("scale generic map ("+gm+") "):]+";")
			}
		}
		src := gcUse(body...)
		r := analyzeOpen(t, "2019", src)
		if errs := errorLines(r); len(errs) > 0 {
			t.Fatalf("%s: errors %q", src, errs)
		}
		r2 := analyzeOpen(t, "2019", src)
		calls, calls2 := gcCalls(r), gcCalls(r2)
		if len(calls) != len(body) || len(calls2) != len(calls) {
			t.Fatalf("%d calls for %d statements", len(calls), len(body))
		}
		for i, c := range calls {
			d := c.Decl
			if !implicitInstance(d) || len(d.Generics) != 1 ||
				d.Generics[0].Formal.Name != "k" ||
				srcText(src, d.Generics[0].Actual) != fmt.Sprint(k) {
				t.Errorf("%q: instance %+v", body[i], d.Generics)
			}
			if c.Decl.Target.Name != calls2[i].Decl.Target.Name ||
				len(c.Args) != 1 {
				t.Errorf("%q: not deterministic", body[i])
			}
		}
	}
}

// TestIMethodAlias: an alias of a method of a named object keeps the
// object (Decl.MethodObj); an alias of a method of the enclosing
// object (no prefix) and of an ordinary subprogram keeps none.
func TestIMethodAlias(t *testing.T) {
	src := lines(
		"package p is",
		"  type t_counter is protected",
		"    procedure increment;",
		"    impure function get return natural;",
		"    alias bump is increment [];",
		"  end protected;",
		"  type t_test is protected",
		"    private variable c1 : t_counter;",
		"    private variable c2 : t_counter;",
		"    alias inc1 is c1.increment [];",
		"    alias get2 is c2.get [return natural];",
		"    impure function sum return natural;",
		"  end protected;",
		"  function f return integer;",
		"  alias g is f [return integer];",
		"end package;",
	)
	r := analyzeOpen(t, "2019", src)
	found := map[Key]*Decl{}
	walkRegions(r.Units[0].Region, func(rg *Region) {
		for _, d := range rg.Decls {
			if d.Kind == DeclAlias {
				found[d.Name] = d
			}
		}
	})
	for name, obj := range map[Key]Key{
		"bump": "", "inc1": "c1", "get2": "c2", "g": "",
	} {
		d := found[name]
		if d == nil {
			t.Fatalf("no alias %s", name)
		}
		got := Key("")
		if d.MethodObj != nil && d.MethodObj.Decl != nil {
			got = d.MethodObj.Decl.Name
		}
		if got != obj || (obj == "") != (d.MethodObj == nil) {
			t.Errorf("%s: MethodObj %v, want %q", name, got, obj)
		}
		if d.Target == nil || !d.Target.Denotes().Kind.IsSubprogram() {
			t.Errorf("%s: target %v", name, d.Target)
		}
	}
}
