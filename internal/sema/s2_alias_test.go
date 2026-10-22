package sema

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
)

// TestMethodAliasTarget: under VHDL-2019 an alias in a protected type
// declaration must denote a method of a private variable (LRM 5.6.2,
// as NVC: nvc/test/sem/lcs2016_33.vhd); an alias of the type's own
// method or of an object is a sema/error at the alias, an alias of a
// private variable's method is legal. Under VHDL-2008 the alias is
// syntax/standard only, never this error.
func TestMethodAliasTarget(t *testing.T) {
	src := `package p is
  type counter is protected
    procedure inc;
  end protected counter;
  constant c : integer := 1;
  type t is protected
    private variable v : counter;
    procedure own;
    alias good is v.inc [];
    alias self is own [];
    alias obj is c;
  end protected t;
end;
`
	files := map[string]string{"p.vhd": src}
	r := analyze(t, files, func(c *config.Config) { c.Standard = "2019" })
	want := "p.vhd:10:11: sema/error: alias self in a protected type " +
		"declaration must denote a method of a private variable " +
		"(LRM 5.6.2)\n" +
		"p.vhd:11:11: sema/error: alias obj in a protected type " +
		"declaration must denote a method of a private variable " +
		"(LRM 5.6.2)\n"
	if got := diagText(r); got != want {
		t.Errorf("2019: got\n%s\nwant\n%s", got, want)
	}
	r = analyze(t, files, nil)
	got := diagText(r)
	if strings.Contains(got, "must denote a method") ||
		!strings.Contains(got, "syntax/standard") {
		t.Errorf("2008: got\n%s", got)
	}
}
