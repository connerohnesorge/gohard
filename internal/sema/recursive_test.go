package sema

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/eval"
)

// TestRecursiveTypes: a type that contains itself, directly or through
// an incomplete type, is one error at the offending element subtype,
// and analysis terminates (before the check, the layout of such a type
// recursed until the Go stack overflowed, which recover cannot catch).
// An incomplete type named as an access type's designated type stays
// legal (LRM 5.4.2).
func TestRecursiveTypes(t *testing.T) {
	arch := func(decls string) string {
		return "entity top is end;\narchitecture rtl of top is\n" +
			decls + "begin end;\n"
	}
	cases := []struct{ name, text, want string }{
		{"self", arch("  type self is record s : self; end record;\n" +
			"  signal sg : self;\n"),
			"a.vhd:3:27: sema/error: record self cannot contain an " +
				"element of its own type"},
		{"package", "package p is\n" +
			"  type self is record s : self; end record;\nend;\n",
			"a.vhd:2:27: sema/error: record self cannot contain an " +
				"element of its own type"},
		{"incomplete-record", arch("  type n;\n" +
			"  type r is record x : n; end record;\n" +
			"  type n is record y : r; end record;\n" +
			"  signal sg : r;\n"),
			"a.vhd:4:24: sema/error: n is an incomplete type"},
		{"incomplete-array", arch("  type n;\n" +
			"  type arr is array (0 to 1) of n;\n" +
			"  type n is record y : arr; end record;\n" +
			"  signal sg : n;\n"),
			"a.vhd:4:33: sema/error: n is an incomplete type"},
		{"incomplete-object", arch("  type n;\n  signal sg : n;\n" +
			"  type n is record y : integer; end record;\n"),
			"a.vhd:4:15: sema/error: n is an incomplete type"},
		{"list", arch("  type node;\n  type ptr is access node;\n" +
			"  type node is record v : integer; nxt : ptr; end record;\n" +
			"  signal sg : integer := 0;\n"), ""},
	}
	for _, c := range cases {
		r := analyze(t, map[string]string{"a.vhd": c.text}, nil)
		got := strings.TrimSpace(diagText(r))
		if c.want == "" && got != "" ||
			!strings.HasPrefix(got, c.want) ||
			strings.Count(got, "\n") != 0 {
			t.Errorf("%s: got\n%s\nwant one diagnostic %q", c.name, got,
				c.want)
		}
	}
}

// TestLayoutCycle: the layout of a type cycle that sema did not reject
// is not static instead of an endless recursion.
func TestLayoutCycle(t *testing.T) {
	rec := &Type{Class: TypeRecord}
	arr := &Type{Class: TypeArray, Elem: rec,
		Index: []*Type{{Class: TypeEnum}}}
	rec.Fields = []*Decl{{Name: "x", Type: arr}}
	ev := newEvaluator(func(Key) (*Library, bool) { return nil, false },
		0)
	// A budget large enough that only the cycle guard ends the walk.
	b := eval.NewBudget(1 << 40)
	if lt, err := ev.layout(rec, b); err == nil {
		t.Fatalf("layout of a cycle = %v, want an error", lt)
	}
	if len(ev.busy) != 0 {
		t.Errorf("busy set not emptied: %d", len(ev.busy))
	}
}
