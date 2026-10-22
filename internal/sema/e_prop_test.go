package sema

// e_prop_test.go checks the v1.1 Stage 1 decl and statement rules as
// properties over every small form, against a model of the rule.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestECondReturnForms analyzes every conditional return of up to
// three branches (each with an expression, unaffected or no value,
// the last one with or without a condition) in a function and in a
// procedure, and checks the errors against LRM 10.13: a function's
// branches all have a value, a procedure's statement is the single
// `return when condition`.
func TestECondReturnForms(t *testing.T) {
	values := []string{"", "1", "unaffected"}
	type form struct {
		vals []string
		tail bool // the last branch has no condition (else)
	}
	var forms []form
	var gen func(vals []string)
	gen = func(vals []string) {
		if len(vals) > 0 {
			forms = append(forms, form{vals: slices.Clone(vals)})
			if len(vals) > 1 && vals[len(vals)-1] != "" {
				forms = append(forms, form{vals: slices.Clone(vals),
					tail: true})
			}
		}
		if len(vals) == 3 {
			return
		}
		for _, v := range values {
			gen(append(vals, v))
		}
	}
	gen(nil)
	for _, proc := range []bool{false, true} {
		for _, f := range forms {
			var b strings.Builder
			for i, v := range f.vals {
				if i > 0 {
					b.WriteString(" else ")
				}
				if v != "" {
					b.WriteString(v + " ")
				}
				if !f.tail || i < len(f.vals)-1 {
					fmt.Fprintf(&b, "when x = %d", i)
				}
			}
			stmt := "return " + strings.TrimSpace(b.String()) + ";"
			head := "function f(x : integer) return integer"
			if proc {
				head = "procedure f(x : integer)"
			}
			src := lines("package p is", "  "+head+";", "end package;",
				"package body p is", "  "+head+" is", "  begin",
				"    "+stmt, "  end;", "end package body;")
			got := errorLines(analyzeOpen(t, "2019", src))
			var want []string
			bare := slices.Contains(f.vals, "")
			valued := slices.ContainsFunc(f.vals, func(v string) bool {
				return v != ""
			})
			switch {
			case !proc && bare:
				want = []string{"7: a return statement in a function " +
					"needs a value"}
			case proc && valued:
				want = []string{"7: a return statement in a procedure " +
					"has no value"}
			case proc && len(f.vals) > 1:
				want = []string{"7: a conditional return in a " +
					"procedure has one condition and no else"}
			}
			if !slices.Equal(got, want) {
				t.Errorf("%s in a %s: errors %q, want %q", stmt,
					map[bool]string{false: "function",
						true: "procedure"}[proc], got, want)
			}
		}
	}
}

// TestEProtectedNesting declares every composite nesting of depth up
// to three (array or record at each level) over an INTEGER or a
// protected leaf and checks the rules that follow from holding a
// protected object: equality and concatenation are not declared, a
// signal of the type is an error, a shared variable of it is legal
// (of the INTEGER nesting it is not), and its formals default to class
// variable.
func TestEProtectedNesting(t *testing.T) {
	for depth := 1; depth <= 3; depth++ {
		for shape := 0; shape < 1<<depth; shape++ {
			for _, leaf := range []string{"integer", "pt"} {
				eNesting(t, depth, shape, leaf)
			}
		}
	}
}

func eNesting(t *testing.T, depth, shape int, leaf string) {
	t.Helper()
	ls := []string{
		"package pk is",
		"  type pt is protected procedure inc; end protected;",
	}
	prev := leaf
	for i := range depth {
		name := fmt.Sprintf("t%d", i)
		if shape&(1<<i) != 0 {
			ls = append(ls, fmt.Sprintf("  type %s is record f : %s; "+
				"end record;", name, prev))
		} else {
			ls = append(ls, fmt.Sprintf("  type %s is array (0 to 1) "+
				"of %s;", name, prev))
		}
		prev = name
	}
	top := prev
	ls = append(ls,
		"  shared variable sv : "+top+";",
		"  signal s : "+top+";",
		"  procedure p(a : "+top+");",
		"end package;",
	)
	r := analyzeOpen(t, "2019", lines(ls...))
	prot := leaf == "pt"
	errs := errorLines(r)
	sigLine := fmt.Sprintf("%d: a signal cannot be of the type %s, "+
		"which has a subelement of a protected type", depth+4, top)
	want := []string{fmt.Sprintf("%d: the type of a shared variable "+
		"must be a protected type, not %s", depth+3, top)}
	if prot {
		want = []string{sigLine}
	}
	what := fmt.Sprintf("depth %d shape %b leaf %s", depth, shape, leaf)
	if !slices.Equal(errs, want) {
		t.Errorf("%s: errors %q, want %q", what, errs, want)
	}
	var eq, concat bool
	var class ObjClass
	for _, u := range r.Units {
		for _, d := range u.Decls {
			if d.Implicit && len(d.Params) > 0 &&
				d.Params[0].Type.Base() == findType(r, top) {
				switch string(d.Name) {
				case `"="`:
					eq = true
				case `"&"`:
					concat = true
				}
			}
			if d.Kind == DeclProcedure && string(d.Name) == "p" {
				class = d.Params[0].Class
			}
		}
	}
	if eq == prot {
		t.Errorf("%s: \"=\" declared %v", what, eq)
	}
	array := shape&(1<<(depth-1)) == 0
	if concat != (array && !prot) {
		t.Errorf("%s: \"&\" declared %v", what, concat)
	}
	wantClass := ClassConstant
	if prot {
		wantClass = ClassVariable
	}
	if class != wantClass {
		t.Errorf("%s: parameter class %v, want %v", what, class,
			wantClass)
	}
}

// findType returns the base type declared with name in t.vhd's units.
func findType(r *Result, name string) *Type {
	for _, u := range r.Units {
		for _, d := range u.Decls {
			if d.Kind == DeclType && string(d.Name) == name {
				return d.Type.Base()
			}
		}
	}
	return nil
}
