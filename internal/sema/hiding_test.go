package sema

import (
	"strings"
	"testing"
)

// TestDirectHidesUse: a directly visible declaration hides a
// use-visible homograph (LRM 12.4 rule a, 12.3): a local f(integer)
// is called, not the package's, with no ambiguity; a local constant
// hides the package's constant of the same name; and an overload the
// local one does not hide (f(real)) stays visible through the use
// clause.
func TestDirectHidesUse(t *testing.T) {
	src := `package p is
  function f(x : integer) return integer;
  function f(x : real) return integer;
  constant k : integer := 1;
end;
package body p is
  function f(x : integer) return integer is begin return 1; end;
  function f(x : real) return integer is begin return 2; end;
end;
use work.p.all;
entity u is end;
architecture a of u is
  function f(x : integer) return integer is begin return 3; end;
  constant k : integer := 4;
  constant c1 : integer := f(1);
  constant c2 : integer := f(1.0);
  constant c3 : integer := k;
begin end;
`
	r := analyze(t, map[string]string{"u.vhd": src}, nil)
	if got := diagText(r); got != "" {
		t.Fatalf("diagnostics:\n%s", got)
	}
	arch := strings.Index(src, "architecture a")
	at := func(s string) int { return arch + strings.Index(src[arch:], s) }
	cases := []struct {
		use, decl string
		local     bool
	}{
		{"f(1);", "function f(x : integer) return integer is begin " +
			"return 3", true},
		{"f(1.0)", "function f(x : real) return integer;", false},
		{"k;", "constant k : integer := 4", true},
	}
	for _, c := range cases {
		ref, ok := r.RefAt("u.vhd", at(c.use))
		if !ok || ref.Decl == nil || ref.Decl.File == nil {
			t.Fatalf("%s: no reference", c.use)
		}
		d := ref.Decl
		if d.Spec != nil {
			d = d.Spec // a body's reference goes to its declaration
		}
		want := strings.Index(src, c.decl)
		if c.local {
			want = at(c.decl)
		}
		if d.Span.Start < want || d.Span.Start > want+len(c.decl) {
			t.Errorf("%s resolves to %s at %d, want the declaration "+
				"at %d", c.use, ref.Decl.Text, ref.Decl.Span.Start, want)
		}
	}
}
