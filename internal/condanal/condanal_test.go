package condanal

import (
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/lexer"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

func analyze(src string, std token.Standard, defs ...Define) Result {
	return Analyze([]byte(src), Options{
		Lexer:       lexer.Options{Standard: std},
		Defines:     defs,
		ToolVersion: "1.2.3",
	})
}

// active returns the words of src outside the inactive regions.
func active(src string, res Result) []string {
	var out []string
	toks, _ := lexer.All([]byte(src), lexer.Options{
		Standard: token.Std2019, Inactive: res.Inactive}, lexer.VHDL)
	for _, t := range toks {
		if t.Kind == token.Identifier && t.Region == 0 {
			out = append(out, src[t.Start:t.End])
		}
	}
	return out
}

func codes(res Result) []string {
	var out []string
	for _, p := range res.Problems {
		out = append(out, string(p.Code))
	}
	return out
}

// TestBuiltinValues: every built-in identifier has its #31 value under
// every standard.
func TestBuiltinValues(t *testing.T) {
	for _, std := range token.Standards {
		want := map[string]string{
			"VHDL_VERSION": std.String(), "TOOL_TYPE": "SIMULATION",
			"TOOL_VENDOR": "gohard", "TOOL_NAME": "gohard",
			"TOOL_EDITION": "community", "TOOL_VERSION": "1.2.3",
		}
		bs := Builtins(std, "1.2.3")
		var names []string
		for _, b := range bs {
			names = append(names, b.Name)
			if b.Value != want[b.Name] {
				t.Errorf("%v: %s = %q", std, b.Name, b.Value)
			}
			// The value is what conditional analysis sees.
			src := "`if " + b.Name + " = \"" + b.Value + "\" then\nyes\n" +
				"`else\nno\n`end if\n"
			res := analyze(src, std)
			if got := active(src, res); !slices.Equal(got, []string{"yes"}) {
				t.Errorf("%v %s: active %v", std, b.Name, got)
			}
		}
		if !slices.Equal(names, config.Builtins) {
			t.Errorf("builtins %v, config has %v", names, config.Builtins)
		}
	}
}

func TestBranches(t *testing.T) {
	cases := []struct {
		src  string
		defs []Define
		want []string
	}{
		{
			"`if A = \"1\" then\na\n`elsif A = \"2\" then\nb\n`else\nc\n`end if\nz",
			[]Define{{"A", "2"}},
			[]string{"b", "z"},
		},
		{"`if A = \"1\" then\na\n`elsif A = \"2\" then\nb\n`else\nc\n`end\nz",
			[]Define{{"a", "3"}}, []string{"c", "z"}},
		// Nested: the inner `if of an inactive branch is not evaluated.
		{"`if A = \"1\" then\n`if B = \"x\" then\na\n`end if\n`else\nb\n" +
			"`end if", []Define{{"A", "0"}}, []string{"b"}},
		{"`if not (A = \"1\") and (A /= \"2\" or A < \"0\") then\na\n" +
			"`end if", []Define{{"A", "3"}}, []string{"a"}},
		{"`if A >= \"2019\" xor A <= \"1993\" then\na\n`end if",
			[]Define{{"A", "2008"}}, nil},
		{"`if A > \"a\" xnor A = \"b\" then\na\n`end if",
			[]Define{{"A", "b"}}, []string{"a"}},
	}
	for _, c := range cases {
		res := analyze(c.src, token.Std2019, c.defs...)
		if len(res.Problems) > 0 {
			t.Errorf("%q: problems %v", c.src, res.Problems)
		}
		if got := active(c.src, res); !slices.Equal(got, c.want) {
			t.Errorf("%q: active %v, want %v", c.src, got, c.want)
		}
	}
}

func TestProblems(t *testing.T) {
	cases := []struct {
		src  string
		std  token.Standard
		want []string
	}{
		{"`if NOPE = \"1\" then\n`end if", token.Std2019,
			[]string{"syntax/undefined-condition"}},
		{"`if A = \"1\" then\n`end if", token.Std2008,
			[]string{"syntax/standard", "syntax/standard"}},
		{"`warning \"w\"\n`error \"e\"", token.Std2019,
			[]string{"syntax/directive-warning", "syntax/directive-error"}},
		{"`if A = \"0\" then\n`error \"e\"\n`end if", token.Std2019, nil},
		{"`if A = \"1\" then\nx", token.Std2019,
			[]string{"syntax/error"}},
		{"`else\n`end if", token.Std2019,
			[]string{"syntax/error", "syntax/error"}},
		{"`if A = \"1\"\n`end if", token.Std2019, []string{"syntax/error"}},
		{"`if A then\n`end if", token.Std2019, []string{"syntax/error"}},
		{"`if A = \"1\" then\n`else\n`elsif A = \"1\" then\n`end if",
			token.Std2019, []string{"syntax/error"}},
		{"`protect key_keyowner = \"x\"", token.Std2008, nil},
		{"`warning 3", token.Std2019, []string{"syntax/error"}},
	}
	for _, c := range cases {
		res := analyze(c.src, c.std, Define{"A", "1"})
		if got := codes(res); !slices.Equal(got, c.want) {
			t.Errorf("%q: got %v, want %v", c.src, got, c.want)
		}
	}
	res := analyze("`if NOPE = \"1\" then\n`end if", token.Std2019)
	d := diag.New(res.Problems[0].Code, res.Problems[0].Args...)
	if !strings.Contains(d.Message, "--define NOPE=VALUE") ||
		!strings.Contains(d.Message, "defines:") {
		t.Errorf("message %q", d.Message)
	}
}

// TestDefineOverridesConfig: --define (config.ApplyDefines) overrides a
// defines: entry of the same name, and conditional analysis sees the
// overriding value.
func TestDefineOverridesConfig(t *testing.T) {
	c, ds := config.Parse(config.FileName,
		[]byte("defines:\n  MODE: \"slow\"\n"))
	if len(ds) > 0 {
		t.Fatalf("config: %v", ds)
	}
	c, _, ds = config.ApplyDefines(c, []string{"mode=fast"})
	if len(ds) > 0 {
		t.Fatalf("defines: %v", ds)
	}
	var defs []Define
	for _, d := range c.Defines {
		defs = append(defs, Define{d.Name, d.Value})
	}
	src := "`if MODE = \"fast\" then\nfast\n`else\nslow\n`end if"
	res := analyze(src, token.Std2019, defs...)
	if got := active(src, res); !slices.Equal(got, []string{"fast"}) {
		t.Errorf("active %v", got)
	}
}

// TestRegionsDisjoint: on generated directive nests the regions are
// sorted, disjoint and inside the input, and Analyze is deterministic.
func TestRegionsDisjoint(t *testing.T) {
	parts := []string{"`if A = \"1\" then\n", "`if A = \"0\" then\n",
		"`elsif A = \"1\" then\n", "`else\n", "`end if\n", "x\n",
		"-- `if\n", "\"`if\"\n", "`warning \"w\"\n"}
	for seed := range uint64(2000) {
		var b strings.Builder
		s := seed
		for range 8 {
			b.WriteString(parts[s%uint64(len(parts))])
			s = s*6364136223846793005 + 1442695040888963407
			s ^= s >> 29
		}
		src := b.String()
		r1 := analyze(src, token.Std2019, Define{"A", "1"})
		r2 := analyze(src, token.Std2019, Define{"A", "1"})
		if !slices.Equal(r1.Inactive, r2.Inactive) {
			t.Fatalf("%q: not deterministic", src)
		}
		end := 0
		for _, r := range r1.Inactive {
			if r.Start < end || r.End <= r.Start || r.End > len(src) {
				t.Fatalf("%q: bad regions %v", src, r1.Inactive)
			}
			end = r.End
		}
	}
}
