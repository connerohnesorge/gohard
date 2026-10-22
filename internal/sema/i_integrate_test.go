package sema

import (
	"slices"
	"strings"
	"testing"
)

// TestIPureParams: under VHDL-2019 an impure function may have
// parameters of any mode and class, a pure one only mode in and class
// constant, signal or file (LRM 4.2.2.1, NVC lcs2016_02). Every
// class/mode/purity combination is checked against that rule, under
// 2019 (the rule) and 2008 (no such error).
func TestIPureParams(t *testing.T) {
	classes := []string{"", "constant ", "signal ", "variable "}
	modes := []string{"", "in ", "inout ", "out "}
	for _, std := range []string{"2008", "2019"} {
		for _, pure := range []bool{false, true} {
			for _, c := range classes {
				for _, m := range modes {
					kw := "impure"
					if pure {
						kw = "pure"
					}
					src := "package p is\n  " + kw + " function f (" + c +
						"x : " + m + "integer) return integer;\nend;\n"
					got := slices.DeleteFunc(
						errorLines(analyzeOpen(t, std, src)),
						func(s string) bool {
							return !strings.Contains(s, "pure function")
						})
					var want []string
					mode := strings.TrimSpace(m)
					switch {
					case std != "2019" || !pure:
					case mode != "" && mode != "in":
						want = []string{"2: parameter x of pure function " +
							"f must have mode in, not " + mode}
					case c == "variable ":
						want = []string{"2: parameter x of pure function " +
							"f must be a constant, signal or file, not a " +
							"variable"}
					}
					if !slices.Equal(got, want) {
						t.Errorf("%s %q: got %q, want %q", std, src, got,
							want)
					}
				}
			}
		}
	}
}

// TestIPureParamsTypes: VHDL-2019 makes a formal of a protected or
// access type with no class a variable, so a pure function cannot take
// one; an impure function can, and so can a pure function with a file
// parameter.
func TestIPureParamsTypes(t *testing.T) {
	checkExact(t, []wantCase{{
		name: "access",
		src: "package p is\n  type ip is access integer;\n" +
			"  pure function f (x : ip) return integer;\n" +
			"  impure function g (x : ip) return integer;\nend;\n",
		want: []string{"3: parameter x of pure function f must be a " +
			"constant, signal or file, not a variable"},
	}, {
		name: "file",
		src: "package p is\n  type ft is file of integer;\n" +
			"  pure function f (file x : ft) return integer;\nend;\n",
	}})
}
