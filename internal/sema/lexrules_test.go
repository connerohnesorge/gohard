package sema

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/token"
)

// TestBitStringRules: the underscore, digit, signed-empty, length and
// delimiter rules of LRM 15.8 on bit-string literals, per standard.
func TestBitStringRules(t *testing.T) {
	src := `package p is
  constant a : bit_vector := X"1234";
  constant b : bit_vector := B"1111_1111";
  constant c : bit_vector := X"a_B";
  constant e2 : bit_vector := X"_1"; --E
  constant e3 : bit_vector := X"1_"; --E
  constant e4 : bit_vector := X"1__2"; --E
  constant e5 : bit_vector := O"9"; --E
`
	checkMarked(t, []markedCase{
		{
			name: "1993",
			std:  "1993",
			src: src + `  constant e6 : bit_vector := X"2AG"; --E
  constant e7 : bit_vector := B"01_X0"; --E
  constant ok : bit_vector := B%0101%;
end;
`,
		},
		{
			name: "2008",
			std:  "2008",
			src: src + `  constant g : bit_vector(3 downto 0) := B"01-0";
  constant h : bit_vector(3 downto 0) := 4sx""; --E
  constant i : bit_vector := 4D"9";
  constant j : bit_vector := 4D"9a"; --E
  constant k : bit_vector := B%0101%; --E
  constant l : bit_vector := 32222222222sb"1"; --E
end;
`,
		},
	})
}

// TestBitStringProblemTotal: the check never panics and accepts every
// literal built of base digits with single inner underscores, under
// every standard (a property over generated literals).
func TestBitStringProblemTotal(t *testing.T) {
	stds := []token.Standard{token.Std1993, token.Std2002, token.Std2008,
		token.Std2019}
	digits := "0123456789abcdef"
	for n := range 200 {
		for base, w := range map[string]int{"B": 2, "O": 8, "X": 16} {
			v := ""
			for i := range n%7 + 1 {
				if i > 0 && (n>>i)&1 == 1 {
					v += "_"
				}
				v += string(digits[(n*7+i*3)%w])
			}
			for _, std := range stds {
				if p := bitStringProblem(base+`"`+v+`"`, std); p != "" {
					t.Fatalf("%s%q under %v: %s", base, v, std, p)
				}
			}
		}
	}
	for _, s := range []string{`"`, `X"`, `x`, `""`, `8"`, `%`} {
		for _, std := range stds {
			bitStringProblem(s, std)
		}
	}
}

// TestToolDirectivesByStandard: a grave accent starts a tool directive
// only from VHDL-2019; VHDL-2008 has the protect directives (LRM-2019
// 16.10, 24.1).
func TestToolDirectivesByStandard(t *testing.T) {
	src := "`foo bar --D\nentity e is end;\n" +
		"`protect begin_protected --P\n`protect end_protected --P\n"
	checkMarked(t, []markedCase{
		{name: "1993", std: "1993", src: strings.NewReplacer("--D", "--E",
			"--P", "--E").Replace(src)},
		{name: "2008", std: "2008",
			src: strings.ReplaceAll(src, "--D", "--E")},
		{name: "2019", std: "2019", src: src},
	})
}
